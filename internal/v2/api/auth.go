package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/billing"
)

const authValidationTTL = 5 * time.Second

type authValidationEntry struct {
	mu          sync.Mutex
	authVersion int
	expiresAt   time.Time
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
	defer entry.mu.Unlock()

	now := time.Now()
	if entry.authVersion == authVersion && now.Before(entry.expiresAt) {
		return true
	}

	var storedVersion int
	var emailVerified bool
	if err := dbx.QueryRowContext(ctx, `SELECT auth_version, email_verified FROM users WHERE id=$1`, userID).
		Scan(&storedVersion, &emailVerified); err != nil || storedVersion != authVersion || !emailVerified {
		entry.expiresAt = time.Time{}
		return false
	}
	entry.authVersion = storedVersion
	entry.expiresAt = now.Add(authValidationTTL)
	return true
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
	WriteError(w, fallbackStatus, fallbackCode)
}
