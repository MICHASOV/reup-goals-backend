package strategy

import "testing"

func TestValidResearchTransition(t *testing.T) {
	tests := []struct {
		current string
		next    string
		valid   bool
	}{
		{ResearchStatusProposed, ResearchStatusAccepted, true},
		{ResearchStatusProposed, ResearchStatusRejected, true},
		{ResearchStatusAccepted, ResearchStatusInProgress, true},
		{ResearchStatusInProgress, ResearchStatusCompleted, true},
		{ResearchStatusCompleted, ResearchStatusInProgress, false},
		{ResearchStatusRejected, ResearchStatusAccepted, false},
	}
	for _, test := range tests {
		if actual := validResearchTransition(test.current, test.next); actual != test.valid {
			t.Fatalf("transition %s -> %s: expected %v, got %v", test.current, test.next, test.valid, actual)
		}
	}
}

func TestNormalizeResearchStatusRejectsUnknownValues(t *testing.T) {
	if normalized := normalizeResearchStatus(" accepted "); normalized != ResearchStatusAccepted {
		t.Fatalf("expected accepted, got %q", normalized)
	}
	if normalized := normalizeResearchStatus("paused"); normalized != "" {
		t.Fatalf("unknown status must be rejected, got %q", normalized)
	}
}
