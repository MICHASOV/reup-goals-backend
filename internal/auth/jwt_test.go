package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionTokenContainsSecurityClaims(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	token, err := GenerateToken(secret, 42, 7)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	claims, err := ParseTokenClaims(secret, token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.UserID != 42 || claims.AuthVersion != 7 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestTokenFromRequestPrefersBearerAndSupportsCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "cookie-token"})
	request.Header.Set("Authorization", "Bearer bearer-token")
	if token, ok := TokenFromRequest(request); !ok || token != "bearer-token" {
		t.Fatalf("expected bearer token, got %q, %v", token, ok)
	}
	request.Header.Del("Authorization")
	if token, ok := TokenFromRequest(request); !ok || token != "cookie-token" {
		t.Fatalf("expected cookie token, got %q, %v", token, ok)
	}
}

func TestSessionCookieIsHttpOnly(t *testing.T) {
	response := httptest.NewRecorder()
	SetSessionCookie(response, "token", true)
	result := response.Result()
	cookies := result.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}
}
