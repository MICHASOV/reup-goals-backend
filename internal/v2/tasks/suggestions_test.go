package tasks

import "testing"

func TestParseTaskSuggestionsCleansAndCapsValues(t *testing.T) {
	raw := `{
		"summary":" Next cycle ",
		"suggestions":[
			{"title":"Check baseline","description":"Build the baseline","why_now":"Required first","priority":8,"due_in_days":900},
			{"title":"check baseline","description":"duplicate","why_now":"","priority":1,"due_in_days":2},
			{"title":"Run pilot","description":"Test one mechanism","why_now":"Fast evidence","priority":1,"due_in_days":7}
		]
	}`
	result, err := parseTaskSuggestions(raw)
	if err != nil {
		t.Fatalf("parse suggestions: %v", err)
	}
	if len(result.Suggestions) != 2 {
		t.Fatalf("expected duplicate removal, got %d suggestions", len(result.Suggestions))
	}
	if result.Suggestions[0].Priority != 2 || result.Suggestions[0].DueInDays != nil {
		t.Fatalf("invalid values were not normalized: %#v", result.Suggestions[0])
	}
}

func TestParseTaskSuggestionsRejectsEmptySet(t *testing.T) {
	if _, err := parseTaskSuggestions(`{"summary":"none","suggestions":[]}`); err == nil {
		t.Fatal("expected empty suggestion set to fail")
	}
}
