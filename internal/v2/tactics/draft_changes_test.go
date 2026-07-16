package tactics

import "testing"

func TestNormalizeTacticsDraftChangesKeepsOnlyApplicableValidChanges(t *testing.T) {
	id := 42
	changes := normalizeTacticsDraftChanges([]TacticsDraftChange{
		{Apply: false, Operation: "create", EntityType: "workstream", Title: "Suggested only"},
		{Apply: true, Operation: " CREATE ", EntityType: " WORKSTREAM ", Title: "  Confirmed change  "},
		{Apply: true, Operation: "update", EntityType: "project", EntityID: &id, Title: "Updated project"},
		{Apply: true, Operation: "update", EntityType: "risk", Title: "Missing id"},
	})
	if len(changes) != 2 {
		t.Fatalf("expected 2 valid changes, got %d", len(changes))
	}
	if changes[0].Title != "Confirmed change" || changes[0].Operation != "create" {
		t.Fatalf("change was not normalized: %#v", changes[0])
	}
}

func TestNormalizeTacticsDraftChangesNormalizesProductFields(t *testing.T) {
	changes := normalizeTacticsDraftChanges([]TacticsDraftChange{{
		Apply:      true,
		Operation:  "create",
		EntityType: "workstream",
		Title:      "  Revenue engine ",
		Metrics: []TacticMetric{
			{Name: " Revenue ", Current: " 10 ", Target: " 20 "},
			{Name: " Margin ", Current: " 12% ", Target: " 25% "},
			{Name: " CAC ", Current: " 100 ", Target: " 60 "},
			{Name: " Must be dropped ", Current: " 1 ", Target: " 2 "},
		},
		ExpectedValue:   "  Higher recurring revenue ",
		Probability:     " HIGH ",
		PotentialImpact: " MEDIUM ",
		Urgency:         " LOW ",
	}})

	if len(changes) != 1 {
		t.Fatalf("expected one normalized change, got %d", len(changes))
	}
	change := changes[0]
	if len(change.Metrics) != 3 {
		t.Fatalf("expected metrics to be capped at three, got %d", len(change.Metrics))
	}
	if change.Metrics[0].Name != "Revenue" || change.Metrics[0].Target != "20" {
		t.Fatalf("metric was not normalized: %#v", change.Metrics[0])
	}
	if change.ExpectedValue != "Higher recurring revenue" || change.Probability != "high" || change.PotentialImpact != "medium" || change.Urgency != "low" {
		t.Fatalf("product fields were not normalized: %#v", change)
	}
}

func TestWorkstreamInputUsesFirstMetricForLegacyFields(t *testing.T) {
	input := WorkstreamInput{Metrics: []TacticMetric{
		{Name: " Revenue ", Current: " 10 ", Target: " 20 "},
		{Name: " Margin ", Current: " 12% ", Target: " 25% "},
	}}

	input.normalize()

	if input.MetricName != "Revenue" || input.MetricCurrent != "10" || input.MetricTarget != "20" {
		t.Fatalf("legacy metric fields do not mirror the first metric: %#v", input)
	}
}
