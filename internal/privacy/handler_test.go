package privacy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidRequestType(t *testing.T) {
	for _, value := range []string{"access", "export", "rectification", "restriction", "objection", "erasure"} {
		if !validRequestType(value) {
			t.Fatalf("expected %q to be valid", value)
		}
	}
	if validRequestType("download_everything_now") {
		t.Fatal("unexpected request type accepted")
	}
}

func TestDocumentsEndpointPublishesCurrentVersion(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/privacy/legal-documents", nil)
	response := httptest.NewRecorder()
	handler.Documents(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"2026-07-18"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}
