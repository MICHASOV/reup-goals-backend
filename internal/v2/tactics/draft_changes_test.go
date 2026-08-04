package tactics

import "testing"

func TestNormalizeTacticsDraftChangesKeepsOnlyApplicableValidChanges(t *testing.T) {
	id := 42
	changes := normalizeTacticsDraftChanges([]TacticsDraftChange{
		{Apply: false, Operation: "create", EntityType: "workstream", Title: "Suggested only"},
		{
			Apply: true, Operation: " CREATE ", EntityType: " WORKSTREAM ", Title: "  Confirmed change  ",
			Description: "Изменить бизнес", Goal: "Получить результат", CKP: "Проверенный результат",
			Reason: "Это главный ограничитель", LeadDepartmentID: 3,
			Metrics: []TacticMetric{{Name: "Выручка", Target: "100"}},
		},
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
		Apply:            true,
		Operation:        "create",
		EntityType:       "workstream",
		Title:            "  Revenue engine ",
		Description:      " Improve recurring revenue ",
		Goal:             " Validate the engine ",
		CKP:              " Predictable recurring revenue ",
		Reason:           " Revenue is the current constraint ",
		LeadDepartmentID: 7,
		Metrics: []TacticMetric{
			{Name: " Revenue ", Current: " 10 ", Target: " 20 "},
			{Name: " Margin ", Current: " 12 ", Target: " 25 ", Unit: "%"},
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

func TestNormalizeTacticsDraftChangesRejectsIncompleteCreate(t *testing.T) {
	changes := normalizeTacticsDraftChanges([]TacticsDraftChange{{
		Apply: true, Operation: "create", EntityType: EntityProject, Title: "Проект без ответственных",
	}})
	if len(changes) != 0 {
		t.Fatalf("incomplete create must remain a conversation question, got %#v", changes)
	}
}

func TestTaskDraftRequiresDirectionButNotLegacyProject(t *testing.T) {
	directionID := 7
	ownerID := 11
	changes := normalizeTacticsDraftChanges([]TacticsDraftChange{{
		Apply: true, Operation: "create", EntityType: EntityTask,
		Title: "Собрать выборку", Description: "Собрать данные для принятия решения.",
		WhyNow: "Без фактов нельзя принять решение", ExpectedResult: "Проверенная выборка",
		DepartmentID: &directionID, OwnerUserID: &ownerID, DueDate: "2026-08-10",
	}})
	if len(changes) != 1 {
		t.Fatalf("direction task must be confirmable without a legacy project: %#v", changes)
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

func TestOrderedTacticsActionIndicesPlacesSelectedParentFirst(t *testing.T) {
	changes := []TacticsDraftChange{
		{Apply: true, EntityType: "project", ParentDraftKey: "direction"},
		{Apply: true, EntityType: "workstream", DraftKey: "direction"},
	}
	ordered, err := orderedTacticsActionIndices(changes, []int{0, 1})
	if err != nil {
		t.Fatalf("order changes: %v", err)
	}
	if len(ordered) != 2 || ordered[0] != 1 || ordered[1] != 0 {
		t.Fatalf("parent must be applied first, got %v", ordered)
	}
}

func TestOrderedTacticsActionIndicesRequiresSelectedParent(t *testing.T) {
	changes := []TacticsDraftChange{
		{Apply: true, EntityType: "workstream", DraftKey: "direction"},
		{Apply: true, EntityType: "project", ParentDraftKey: "direction"},
	}
	if _, err := orderedTacticsActionIndices(changes, []int{1}); err == nil {
		t.Fatal("expected missing parent selection to fail")
	}
}

func TestOrderedTacticsActionIndicesRejectsNonApplicableChange(t *testing.T) {
	changes := []TacticsDraftChange{{Apply: false, EntityType: "project"}}
	if _, err := orderedTacticsActionIndices(changes, []int{0}); err == nil {
		t.Fatal("expected non-applicable change to fail")
	}
}

func TestValidateDraftChangesForConfirmationRejectsIncompletePackage(t *testing.T) {
	changes := []TacticsDraftChange{{
		Apply: true, Operation: "create", EntityType: EntityProject,
		Title: "Проект без родителя", Description: "Описание проекта",
	}}
	if err := ValidateDraftChangesForConfirmation(changes); err == nil {
		t.Fatal("incomplete project package must be rejected before confirmation")
	}
}

func TestParseDraftMetricNumberSupportsLegacyDisplayValues(t *testing.T) {
	tests := map[string]float64{
		"85000":               85000,
		"85 000":              85000,
		"75–85 000 EUR/month": 85000,
		"12,5 %":              12.5,
		"-1200":               -1200,
	}
	for input, want := range tests {
		got, err := parseDraftMetricNumber(input)
		if err != nil {
			t.Fatalf("parseDraftMetricNumber(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseDraftMetricNumber(%q) = %v, want %v", input, got, want)
		}
	}
}
