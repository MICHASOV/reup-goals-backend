package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogoutHandlerClearsOnlyCurrentCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request = request.WithContext(context.WithValue(request.Context(), userIDKey, 42))
	response := httptest.NewRecorder()

	LogoutHandler(nil, true).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	cookie := response.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, SessionCookieName+"=") || !strings.Contains(cookie, "Max-Age=0") {
		t.Fatalf("expected the current session cookie to be cleared, got %q", cookie)
	}
}
