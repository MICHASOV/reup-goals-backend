package auth

import (
	"net/http/httptest"
	"testing"
)

func TestShouldExposeToken(t *testing.T) {
	tests := []struct {
		name            string
		browserAuthOnly bool
		nativeHeader    string
		origin          string
		want            bool
	}{
		{name: "legacy token mode", want: true},
		{name: "browser-only hides token", browserAuthOnly: true},
		{name: "native app receives token", browserAuthOnly: true, nativeHeader: "mobile", want: true},
		{name: "browser cannot opt into native token", browserAuthOnly: true, nativeHeader: "mobile", origin: "https://reupgoals.pro"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/auth/login", nil)
			request.Header.Set(nativeClientHeader, test.nativeHeader)
			request.Header.Set("Origin", test.origin)
			if got := shouldExposeToken(request, test.browserAuthOnly); got != test.want {
				t.Fatalf("shouldExposeToken() = %v, want %v", got, test.want)
			}
		})
	}
}
