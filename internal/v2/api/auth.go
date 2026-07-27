package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/billing"
)

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
		if dbx != nil {
			var version int
			var emailVerified bool
			if dbx.QueryRowContext(r.Context(), `SELECT auth_version, email_verified FROM users WHERE id=$1`, claims.UserID).
				Scan(&version, &emailVerified) != nil || version != claims.AuthVersion || !emailVerified {
				WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}

		ctx := auth.ContextWithUserID(r.Context(), claims.UserID)
		next(w, r.WithContext(ctx))
	}
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
