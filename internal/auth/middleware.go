package auth

import (
	"context"
	"net/http"
	"strings"

	"reup-goals-backend/internal/analytics"
)

type ctxKey string

const userIDKey ctxKey = "user_id"

type Middleware struct {
	secret []byte
}

func New(secret []byte) Middleware {
	return Middleware{secret: secret}
}

func (m Middleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(h, "Bearer ")
		userID, err := ParseToken(m.secret, tokenString)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)

		// прокидываем user_id в analytics context
		ctx = analytics.WithUserID(ctx, userID)

		next(w, r.WithContext(ctx))
	}
}

func UserIDFromContext(ctx context.Context) (int, bool) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return 0, false
	}
	uid, ok := v.(int)
	return uid, ok
}
