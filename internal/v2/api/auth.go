package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/billing"
)

const (
	authValidationTTL        = 15 * time.Second
	authValidationStaleGrace = time.Minute
	authValidationTimeout    = 3 * time.Second
)

type authValidationEntry struct {
	mu          sync.Mutex
	authVersion int
	valid       bool
	refreshing  bool
	expiresAt   time.Time
	staleUntil  time.Time
}

var authValidationEntries sync.Map

func RequireAuth(dbx *sql.DB, secret []byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenString, ok := auth.TokenFromRequest(r)
		if !ok {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		claims, err := auth.ParseTokenClaims(secret, tokenString)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if dbx != nil && !validAuthenticatedUser(r.Context(), dbx, claims.UserID, claims.AuthVersion) {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		ctx := auth.ContextWithUserID(r.Context(), claims.UserID)
		next(w, r.WithContext(ctx))
	}
}

func validAuthenticatedUser(ctx context.Context, dbx *sql.DB, userID int, authVersion int) bool {
	value, _ := authValidationEntries.LoadOrStore(userID, &authValidationEntry{})
	entry := value.(*authValidationEntry)
	entry.mu.Lock()

	now := time.Now()
	if entry.valid && entry.authVersion == authVersion && now.Before(entry.expiresAt) {
		entry.mu.Unlock()
		return true
	}
	if entry.valid && entry.authVersion == authVersion && now.Before(entry.staleUntil) {
		if !entry.refreshing {
			entry.refreshing = true
			go refreshAuthenticatedUser(context.WithoutCancel(ctx), dbx, userID, authVersion, entry)
		}
		entry.mu.Unlock()
		return true
	}
	entry.mu.Unlock()
	if dbx == nil {
		return false
	}

	var storedVersion int
	var emailVerified bool
	queryCtx, cancel := context.WithTimeout(ctx, authValidationTimeout)
	defer cancel()
	if err := dbx.QueryRowContext(queryCtx, `SELECT auth_version, email_verified FROM users WHERE id=$1`, userID).
		Scan(&storedVersion, &emailVerified); err != nil || storedVersion != authVersion || !emailVerified {
		entry.mu.Lock()
		entry.valid = false
		entry.expiresAt = time.Time{}
		entry.staleUntil = time.Time{}
		entry.mu.Unlock()
		return false
	}
	entry.mu.Lock()
	entry.authVersion = storedVersion
	entry.valid = true
	entry.expiresAt = now.Add(authValidationTTL)
	entry.staleUntil = now.Add(authValidationStaleGrace)
	entry.mu.Unlock()
	return true
}

func refreshAuthenticatedUser(parent context.Context, dbx *sql.DB, userID int, authVersion int, entry *authValidationEntry) {
	ctx, cancel := context.WithTimeout(parent, authValidationTimeout)
	defer cancel()

	var storedVersion int
	var emailVerified bool
	err := dbx.QueryRowContext(ctx, `SELECT auth_version, email_verified FROM users WHERE id=$1`, userID).
		Scan(&storedVersion, &emailVerified)
	now := time.Now()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.refreshing = false
	if err != nil {
		// Keep the short stale window during a transient database slowdown,
		// then try again without blocking the user's current page load.
		entry.expiresAt = now.Add(time.Second)
		return
	}
	if storedVersion != authVersion || !emailVerified {
		entry.valid = false
		entry.expiresAt = time.Time{}
		entry.staleUntil = time.Time{}
		return
	}
	entry.authVersion = storedVersion
	entry.valid = true
	entry.expiresAt = now.Add(authValidationTTL)
	entry.staleUntil = now.Add(authValidationStaleGrace)
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func WriteError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]string{"error": code})
}

func WriteAIError(w http.ResponseWriter, err error, fallbackStatus int, fallbackCode string) {
	if errors.Is(err, billing.ErrQuotaExceeded) {
		WriteError(w, http.StatusTooManyRequests, billing.ErrQuotaExceeded.Error())
		return
	}
	if errors.Is(err, billing.ErrPaymentRequired) {
		WriteError(w, http.StatusPaymentRequired, billing.ErrPaymentRequired.Error())
		return
	}
	if errors.Is(err, ai.ErrRateLimitExceeded) {
		WriteError(w, http.StatusTooManyRequests, ai.ErrRateLimitExceeded.Error())
		return
	}
	if errors.Is(err, ai.ErrDailyBudgetExceeded) {
		WriteError(w, http.StatusTooManyRequests, ai.ErrDailyBudgetExceeded.Error())
		return
	}
	if errors.Is(err, ai.ErrMonthlyBudgetExceeded) {
		WriteError(w, http.StatusTooManyRequests, ai.ErrMonthlyBudgetExceeded.Error())
		return
	}
	WriteError(w, fallbackStatus, fallbackCode)
}
