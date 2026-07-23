package tactics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTacticsConversationTurnContainsOnlyMessageAndScope(t *testing.T) {
	workstreamID := 42
	input := buildTacticsTurnInput("Разберем риск", TacticsFacilitatorMessageRequest{
		ParticipantRole: "owner",
		Scope:           &TacticsMessageScope{EntityType: "workstream", EntityID: workstreamID},
	})
	var turn map[string]any
	if err := json.Unmarshal([]byte(input), &turn); err != nil {
		t.Fatal(err)
	}
	if len(turn) != 3 || turn["latest_user_message"] != "Разберем риск" {
		t.Fatalf("unexpected compact tactics turn: %#v", turn)
	}
	if turn["knowledge_base"] != nil || turn["strategy"] != nil || turn["current_tactical_plan"] != nil {
		t.Fatalf("tactics turn repeated workspace context: %#v", turn)
	}
}

func TestParseTacticsFacilitatorOutput(t *testing.T) {
	raw := `{
		"message":"Проверим **механизм изменения**【 】, а не список задач.",
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
	if output.Message != "Проверим **механизм изменения**, а не список задач." {
		t.Fatalf("citation marker was not removed: %q", output.Message)
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

func TestParseTacticsFacilitatorOutputRejectsSerializedMessage(t *testing.T) {
	_, err := parseTacticsFacilitatorOutput(`{
		"message":"{\"next_move\":\"Создать проект\"}",
		"session_status":"in_progress",
		"current_focus":{},
		"decisions_detected":[],
		"open_questions":[],
		"needs_strategy_review":false
	}`)
	if err == nil {
		t.Fatal("serialized JSON must not reach the tactics chat")
	}
}

func TestNormalizeTacticsStatusFallsBackToInProgress(t *testing.T) {
	if got := normalizeTacticsStatus("unknown"); got != FacilitatorStatusInProgress {
		t.Fatalf("unexpected fallback status: %s", got)
	}
}

func TestAdvisorThreadSeparatesConversationFromBusinessScope(t *testing.T) {
	thread := AdvisorThread{
		ID:         81,
		ScopeType:  EntityProject,
		ScopeID:    17,
		ScopeLabel: "Проверка B2B-сегмента",
	}
	contextScope := thread.Scope()
	conversationScope := thread.ConversationScope()
	if contextScope == nil || contextScope.EntityType != EntityProject || contextScope.EntityID != 17 {
		t.Fatalf("unexpected business scope: %#v", contextScope)
	}
	if conversationScope.EntityType != EntityAdvisorThread || conversationScope.EntityID != 81 {
		t.Fatalf("unexpected private conversation scope: %#v", conversationScope)
	}
}

func TestTacticsFreshInputUsesDocumentIndexesInsteadOfDocumentBodies(t *testing.T) {
	state := TacticsFacilitatorState{
		Current: CurrentResponse{Workstreams: []Workstream{}},
		StrategyDocs: []TacticsStrategyDocument{{
			DocumentType: "strategic_diagnosis",
			Title:        "Диагноз",
			Status:       "ready",
			Content:      "SENSITIVE_FULL_STRATEGY_BODY",
		}},
	}
	input := buildTacticsFreshInput("Проверим идею", TacticsFacilitatorMessageRequest{}, nil, state)
	if strings.Contains(input, "SENSITIVE_FULL_STRATEGY_BODY") {
		t.Fatal("fresh advisor context must use file search instead of repeating full documents")
	}
	if !strings.Contains(input, "strategic_diagnosis") {
		t.Fatal("document index should remain available")
	}
}
