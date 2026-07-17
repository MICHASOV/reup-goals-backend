package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reup-goals-backend/internal/auth"
)

func TestRequireAuthHTTPFlow(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	token, err := auth.GenerateToken(secret, 42)
	if err != nil {
		t.Fatal(err)
	}
	handler := RequireAuth(nil, secret, func(w http.ResponseWriter, r *http.Request) {
		userID, ok := auth.UserIDFromContext(r.Context())
		if !ok || userID != 42 {
			t.Fatalf("unexpected authenticated user: %d %v", userID, ok)
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v2/bootstrap", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestRequireAuthRejectsMissingToken(t *testing.T) {
	handler := RequireAuth(nil, []byte(strings.Repeat("s", 32)), func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler must not run")
	})
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodGet, "/api/v2/bootstrap", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}
