package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"reup-goals-backend/internal/auth"
)

func RequireAuth(secret []byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		tokenString := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		userID, err := auth.ParseToken(secret, tokenString)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		ctx := auth.ContextWithUserID(r.Context(), userID)
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
