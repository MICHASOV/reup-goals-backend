package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/billing"
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

func TestWriteAIErrorPreservesKnownGovernanceCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"weekly quota", ai.RejectCall(billing.ErrQuotaExceeded), http.StatusTooManyRequests, "ai_weekly_limit_reached"},
		{"payment", ai.RejectCall(billing.ErrPaymentRequired), http.StatusPaymentRequired, "payment_required"},
		{"rate", ai.RejectCall(ai.ErrRateLimitExceeded), http.StatusTooManyRequests, "ai_rate_limit_exceeded"},
		{"daily budget", ai.RejectCall(ai.ErrDailyBudgetExceeded), http.StatusTooManyRequests, "ai_daily_budget_exceeded"},
		{"monthly budget", ai.RejectCall(ai.ErrMonthlyBudgetExceeded), http.StatusTooManyRequests, "ai_monthly_budget_exceeded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			WriteAIError(response, test.err, http.StatusInternalServerError, "fallback")
			if response.Code != test.wantStatus {
				t.Fatalf("unexpected status: got %d want %d", response.Code, test.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["error"] != test.wantCode {
				t.Fatalf("unexpected code: got %q want %q", body["error"], test.wantCode)
			}
		})
	}
}

func TestWriteAIErrorUsesFallbackForUnknownFailures(t *testing.T) {
	response := httptest.NewRecorder()
	WriteAIError(response, errors.New("provider_unavailable"), http.StatusBadGateway, "ai_failed")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}
