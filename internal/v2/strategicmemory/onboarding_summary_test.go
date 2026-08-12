package strategicmemory

import "testing"

func TestParseOnboardingSummary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "contract", raw: `{"summary_markdown":"# О компании\n\nКонтекст"}`, want: "# О компании\n\nКонтекст"},
		{name: "compatible alias", raw: `{"markdown":"# О компании\n\nКонтекст"}`, want: "# О компании\n\nКонтекст"},
		{name: "nested result", raw: `{"result":{"summary_markdown":"# О компании\n\nКонтекст"}}`, want: "# О компании\n\nКонтекст"},
		{name: "single string field", raw: `{"company_overview":"# О компании\n\nКонтекст"}`, want: "# О компании\n\nКонтекст"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOnboardingSummary(test.raw)
			if err != nil {
				t.Fatalf("parse summary: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseOnboardingSummaryRejectsEmptyAndInvalidResponses(t *testing.T) {
	for _, raw := range []string{`{}`, `{"summary_markdown":""}`, `{"result":{"status":"ready"}}`, `not json`} {
		if _, err := parseOnboardingSummary(raw); err == nil {
			t.Fatalf("expected %q to fail", raw)
		}
	}
}
