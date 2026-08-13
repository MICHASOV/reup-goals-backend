package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLimiterRejectsRequestsAboveLimit(t *testing.T) {
	limiter := NewLimiter(2, time.Minute)
	handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for index, expected := range []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatalf("request %d: expected %d, got %d", index+1, expected, response.Code)
		}
	}
}

func TestIndependentLimitersDoNotShareAuthBudgets(t *testing.T) {
	registerLimiter := NewLimiter(1, time.Minute)
	loginLimiter := NewLimiter(1, time.Minute)
	registerHandler := registerLimiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	loginHandler := loginLimiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/auth/register", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()
	registerHandler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected registration request to pass, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	response = httptest.NewRecorder()
	loginHandler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("registration traffic must not exhaust the login budget, got %d", response.Code)
	}
}

func TestHardenLimitsJSONAndSetsHeaders(t *testing.T) {
	handler := Harden(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/tasks", strings.NewReader(strings.Repeat("x", defaultRequestLimit+1)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", response.Code)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers were not set")
	}
}

func TestClientIPOnlyTrustsForwardedHeaderFromLoopback(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.7:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(request); got != "198.51.100.7" {
		t.Fatalf("expected direct peer, got %s", got)
	}
	request.RemoteAddr = "127.0.0.1:1234"
	if got := clientIP(request); got != "203.0.113.9" {
		t.Fatalf("expected trusted forwarded address, got %s", got)
	}
}

func TestTrustedOriginRequiredForCookieMutation(t *testing.T) {
	handler := RequireTrustedOrigin([]string{"https://reupgoals.pro"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/tasks", nil)
	request.AddCookie(&http.Cookie{Name: "reupgoals_session", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden request without origin, got %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v2/tasks", nil)
	request.AddCookie(&http.Cookie{Name: "reupgoals_session", Value: "token"})
	request.Header.Set("Origin", "https://reupgoals.pro")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected trusted origin to pass, got %d", response.Code)
	}
}

func TestFileUploadRoutesUseTheirDocumentLimits(t *testing.T) {
	tests := []struct {
		path  string
		size  int64
		limit int64
	}{
		{path: "/api/v2/strategic-director/files", size: 80 << 20, limit: fileRequestLimit},
		{path: "/api/v2/strategy-facilitator/files", size: 80 << 20, limit: fileRequestLimit},
		{path: "/api/v2/tactics-facilitator/files", size: 25 << 20, limit: audioRequestLimit},
		{path: "/api/v2/tactics-advisor/files", size: 25 << 20, limit: audioRequestLimit},
		{path: "/api/v2/tasks/files", size: 25 << 20, limit: audioRequestLimit},
		{path: "/api/v2/tasks/completion-files", size: 25 << 20, limit: audioRequestLimit},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := requestBodyLimit(test.path); got != test.limit {
				t.Fatalf("expected limit %d, got %d", test.limit, got)
			}
			handler := limitRequestBodies(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader("file"))
			request.ContentLength = test.size
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("document below route limit must reach the upload handler, got %d", response.Code)
			}
		})
	}
}

func TestStrategyFileUploadRejectsOversizedBusinessDocument(t *testing.T) {
	handler := limitRequestBodies(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/strategy-facilitator/files", strings.NewReader("file"))
	request.ContentLength = fileRequestLimit + 1
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized strategy document must be rejected, got %d", response.Code)
	}
}

func TestAdvisorFileUploadRejectsOversizedDocument(t *testing.T) {
	handler := limitRequestBodies(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/tactics-advisor/files", strings.NewReader("file"))
	request.ContentLength = audioRequestLimit + 1
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized advisor document must be rejected, got %d", response.Code)
	}
}
