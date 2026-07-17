package operations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reup-goals-backend/internal/auth"
)

func TestNormalizedPath(t *testing.T) {
	got := normalizedPath("/api/v2/tasks/42/status")
	if got != "/api/v2/tasks/:id/status" {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestEventName(t *testing.T) {
	got := eventName("POST", "/api/v2/strategy/17/activate")
	if got != "post.api.v2.strategy.:id.activate" {
		t.Fatalf("unexpected event name: %s", got)
	}
}

func TestRequestUserIDSupportsCookieAndBearerSessions(t *testing.T) {
	secret := []byte(strings.Repeat("x", 32))
	token, err := auth.GenerateToken(secret, 42)
	if err != nil {
		t.Fatal(err)
	}

	for _, setup := range []func(*httptest.ResponseRecorder, *http.Request){
		func(_ *httptest.ResponseRecorder, request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+token)
		},
		func(recorder *httptest.ResponseRecorder, request *http.Request) {
			auth.SetSessionCookie(recorder, token, true)
			request.Header.Set("Cookie", recorder.Header().Get("Set-Cookie"))
		},
	} {
		request := httptest.NewRequest("GET", "/api/v2/tasks", nil)
		recorder := httptest.NewRecorder()
		setup(recorder, request)
		if userID, ok := requestUserID(secret, request); !ok || userID != 42 {
			t.Fatalf("expected user 42, got %d ok=%v", userID, ok)
		}
	}
}
