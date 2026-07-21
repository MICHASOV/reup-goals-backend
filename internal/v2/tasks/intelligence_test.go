package tasks

import (
	"encoding/json"
	"testing"
)

func TestBrainstormConversationInputsKeepWorkspaceContextOutOfEveryTurn(t *testing.T) {
	pack := taskContextPack{Workstream: &WorkstreamSummary{ID: 8, Title: "Платящий сегмент"}}
	var initial map[string]any
	if err := json.Unmarshal([]byte(brainstormInitialInput(pack, "Поштурмим задачи")), &initial); err != nil {
		t.Fatal(err)
	}
	if initial["active_workstream"] == nil || initial["strategy_documents"] != nil {
		t.Fatalf("unexpected initial brainstorm context: %#v", initial)
	}
	var turn map[string]any
	if err := json.Unmarshal([]byte(brainstormTurnInput("Еще вариант")), &turn); err != nil {
		t.Fatal(err)
	}
	if len(turn) != 1 || turn["current_user_message"] != "Еще вариант" {
		t.Fatalf("brainstorm turn repeated context: %#v", turn)
	}
}

func TestParseBrainstormOutputKeepsOnlyValidScopedActions(t *testing.T) {
	projectID := 12
	unknownProjectID := 99
	taskID := 7
	unknownTaskID := 77
	pack := taskContextPack{
		Projects:      []Project{{ID: projectID}},
		ExistingTasks: []taskContextItem{{ID: taskID, Title: "Existing"}},
	}
	raw := `{
		"message":"Давайте зафиксируем следующий шаг.",
		"task_actions":[
			{"action_type":"create","title":"Проверить сегмент","project_id":12},
			{"action_type":"create","title":"Проверить канал","project_id":99},
			{"action_type":"update","task_id":7,"title":"Уточненная задача"},
			{"action_type":"archive","task_id":77,"title":"Чужая задача"}
		]
	}`
	result, err := parseBrainstormOutput(raw, pack)
	if err != nil {
		t.Fatalf("parse brainstorm output: %v", err)
	}
	if len(result.Actions) != 3 {
		t.Fatalf("expected 3 valid actions, got %d", len(result.Actions))
	}
	if result.Actions[0].ProjectID == nil || *result.Actions[0].ProjectID != projectID {
		t.Fatalf("known project link was lost: %#v", result.Actions[0])
	}
	if result.Actions[1].ProjectID != nil {
		t.Fatalf("unknown project link must be removed: %#v", result.Actions[1])
	}
	if result.Actions[2].TaskID == nil || *result.Actions[2].TaskID != taskID {
		t.Fatalf("known task update was lost: %#v", result.Actions[2])
	}
	_ = unknownProjectID
	_ = unknownTaskID
}

func TestTaskPriorityUsesFiveTiersAndCapsRemoveRecommendation(t *testing.T) {
	high := taskEvaluatorModelOutput{
		StrategicRelevance: 95, CourseAlignment: 95, TacticalAlignment: 95,
		ExpectedImpact: 95, Urgency: 90, Effort: 20, Confidence: 90,
		Recommendation: RecommendationKeep,
	}
	highScore := CalculateTaskPriority(high)
	if highScore < 85 || PriorityTier(highScore) != "P1" {
		t.Fatalf("expected P1, got score=%d tier=%s", highScore, PriorityTier(highScore))
	}

	high.Recommendation = RecommendationRemove
	removeScore := CalculateTaskPriority(high)
	if removeScore != 25 || PriorityTier(removeScore) != "P5" {
		t.Fatalf("remove recommendation must be capped, got score=%d tier=%s", removeScore, PriorityTier(removeScore))
	}
}

func TestParseTaskEvaluatorOutputNormalizesValues(t *testing.T) {
	raw := `{
		"strategic_relevance":120,
		"course_alignment":80,
		"tactical_alignment":75,
		"expected_impact":70,
		"urgency":-5,
		"effort":40,
		"confidence":65,
		"recommendation":"UNKNOWN",
		"priority_reason":" Нужна проверка результата. ",
		"clarification_question":" Какой результат должен появиться? ",
		"missing_information":[" критерий успеха ",""]
	}`
	result, err := parseTaskEvaluatorOutput(raw)
	if err != nil {
		t.Fatalf("parse evaluator output: %v", err)
	}
	if result.StrategicRelevance != 100 || result.Urgency != 0 {
		t.Fatalf("scores were not clamped: %#v", result)
	}
	if result.Recommendation != RecommendationClarify {
		t.Fatalf("unknown recommendation must become clarify: %s", result.Recommendation)
	}
	if len(result.MissingInformation) != 1 || result.MissingInformation[0] != "критерий успеха" {
		t.Fatalf("missing information was not cleaned: %#v", result.MissingInformation)
	}
	if result.BacklogCategory != BacklogQuestionable {
		t.Fatalf("clarification must enter questionable backlog, got %q", result.BacklogCategory)
	}
	if !containsString(result.Flags, TaskFlagNeedsClarification) {
		t.Fatalf("clarification flag was not derived: %#v", result.Flags)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
