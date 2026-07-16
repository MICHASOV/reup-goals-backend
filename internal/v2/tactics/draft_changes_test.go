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
