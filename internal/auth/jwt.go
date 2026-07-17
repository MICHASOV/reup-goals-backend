package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	SessionCookieName = "reupgoals_session"
	sessionIssuer     = "reupgoals-api"
	sessionAudience   = "reupgoals-app"
	tokenTTL          = 7 * 24 * time.Hour
)

type SessionClaims struct {
	UserID      int `json:"user_id"`
	AuthVersion int `json:"auth_version"`
	jwt.RegisteredClaims
}

func GenerateToken(secret []byte, userID int, authVersion ...int) (string, error) {
	version := 1
	if len(authVersion) > 0 && authVersion[0] > 0 {
		version = authVersion[0]
	}
	now := time.Now().UTC()
	claims := SessionClaims{
		UserID: userID, AuthVersion: version,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: sessionIssuer, Audience: jwt.ClaimStrings{sessionAudience},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(secret)
}

func ParseToken(secret []byte, tokenString string) (int, error) {
	claims, err := ParseTokenClaims(secret, tokenString)
	if err != nil {
		return 0, err
	}
	return claims.UserID, nil
}

func ParseTokenClaims(secret []byte, tokenString string) (SessionClaims, error) {
	if len(secret) == 0 {
		return SessionClaims{}, errors.New("empty jwt secret")
	}
	claims := SessionClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(sessionIssuer), jwt.WithAudience(sessionAudience))
	if err != nil {
		return SessionClaims{}, err
	}
	if token == nil || !token.Valid {
		return SessionClaims{}, errors.New("invalid token")
	}
	if claims.UserID <= 0 || claims.AuthVersion <= 0 {
		return SessionClaims{}, errors.New("missing session claims")
	}
	return claims, nil
}

func TokenFromRequest(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), true
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", false
	}
	return strings.TrimSpace(cookie.Value), true
}

func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	// #nosec G124 -- secure is always true in staging/production; false supports local HTTP development.
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(tokenTTL.Seconds()), Expires: time.Now().Add(tokenTTL),
	})
}

func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	// #nosec G124 -- deletion must use the same Secure attribute as the original cookie.
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}
