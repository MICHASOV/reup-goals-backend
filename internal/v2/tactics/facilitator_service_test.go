package tactics

import "testing"

func TestParseTacticsFacilitatorOutput(t *testing.T) {
	raw := `{
		"message":"Проверим **механизм изменения**, а не список задач.",
		"session_status":"candidate_ready",
		"status_reason":"The change portfolio is coherent.",
		"current_focus":{
			"entity_type":"workstream",
			"entity_id":17,
			"title":"Повторяемые продажи",
			"research_goal":"Проверить причинную связь с курсом"
		},
		"decisions_detected":["Не запускать новый рынок", "Не запускать новый рынок"],
		"open_questions":["Какой baseline конверсии?"],
		"needs_strategy_review":false,
		"strategy_review_reason":""
	}`

	output, err := parseTacticsFacilitatorOutput(raw)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if output.SessionStatus != FacilitatorStatusCandidateReady {
		t.Fatalf("unexpected status: %s", output.SessionStatus)
	}
	if output.CurrentFocus.EntityID == nil || *output.CurrentFocus.EntityID != 17 {
		t.Fatalf("unexpected focus id: %#v", output.CurrentFocus.EntityID)
	}
	if len(output.DecisionsDetected) != 1 {
		t.Fatalf("expected decisions to be deduplicated, got %#v", output.DecisionsDetected)
	}
}

func TestParseTacticsFacilitatorOutputRejectsEmptyMessage(t *testing.T) {
	_, err := parseTacticsFacilitatorOutput(`{
		"message":"",
		"session_status":"in_progress",
		"current_focus":{},
		"decisions_detected":[],
		"open_questions":[],
		"needs_strategy_review":false
	}`)
	if err == nil {
		t.Fatal("expected empty message to fail")
	}
}

func TestNormalizeTacticsStatusFallsBackToInProgress(t *testing.T) {
	if got := normalizeTacticsStatus("unknown"); got != FacilitatorStatusInProgress {
		t.Fatalf("unexpected fallback status: %s", got)
	}
}
