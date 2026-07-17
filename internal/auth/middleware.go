package auth

import (
	"context"
	"database/sql"
	"net/http"

	"reup-goals-backend/internal/analytics"
)

type ctxKey string

const userIDKey ctxKey = "user_id"

type Middleware struct {
	dbx    *sql.DB
	secret []byte
}

func New(dbx *sql.DB, secret []byte) Middleware {
	return Middleware{dbx: dbx, secret: secret}
}

func (m Middleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenString, ok := TokenFromRequest(r)
		if !ok {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		claims, err := ParseTokenClaims(m.secret, tokenString)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if !sessionVersionMatches(r.Context(), m.dbx, claims.UserID, claims.AuthVersion) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := ContextWithUserID(r.Context(), claims.UserID)

		next(w, r.WithContext(ctx))
	}
}

func sessionVersionMatches(ctx context.Context, dbx *sql.DB, userID int, expected int) bool {
	if dbx == nil || userID <= 0 || expected <= 0 {
		return false
	}
	var actual int
	return dbx.QueryRowContext(ctx, `SELECT auth_version FROM users WHERE id=$1`, userID).Scan(&actual) == nil && actual == expected
}

func ContextWithUserID(ctx context.Context, userID int) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	return analytics.WithUserID(ctx, userID)
}

func UserIDFromContext(ctx context.Context) (int, bool) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return 0, false
	}
	uid, ok := v.(int)
	return uid, ok
}
