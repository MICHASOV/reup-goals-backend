package tasks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskEvaluatorSupportsBusinessContextWithoutStrategyOrCourse(t *testing.T) {
	if !strings.Contains(taskEvaluatorPrompt, "Never refuse to evaluate a task because an approved strategy or course is absent") {
		t.Fatal("task evaluator must support business-context-only evaluation")
	}
	if !strings.Contains(taskEvaluatorPrompt, "when no course exists") {
		t.Fatal("course alignment must define a fallback when no course exists")
	}
}

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
		CreationOptions: taskCreationOptions{
			Departments: []taskDepartmentOption{{ID: 3, Name: "Продажи"}},
			Members:     []taskMemberOption{{UserID: 5, Name: "Анна"}},
		},
	}
	raw := `{
		"message":"Давайте зафиксируем следующий шаг【】.",
		"task_actions":[
			{"action_type":"create","title":"Проверить сегмент","description":"Провести интервью","expected_result":"Пять подтвержденных проблем","project_id":12,"department_id":3,"owner_user_id":5,"due_date":"2026-08-15"},
			{"action_type":"create","title":"Проверить канал","description":"Запустить тест","expected_result":"Измеренный CAC","project_id":99,"department_id":3,"owner_deferred":true,"due_date_deferred":true},
			{"action_type":"update","task_id":7,"title":"Уточненная задача"},
			{"action_type":"archive","task_id":77,"title":"Чужая задача"}
		]
	}`
	result, err := parseBrainstormOutput(raw, pack)
	if err != nil {
		t.Fatalf("parse brainstorm output: %v", err)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("expected 2 valid actions, got %d", len(result.Actions))
	}
	if result.Message != "Давайте зафиксируем следующий шаг." {
		t.Fatalf("citation marker was not removed: %q", result.Message)
	}
	if result.Actions[0].ProjectID == nil || *result.Actions[0].ProjectID != projectID {
		t.Fatalf("known project link was lost: %#v", result.Actions[0])
	}
	if result.Actions[1].TaskID == nil || *result.Actions[1].TaskID != taskID {
		t.Fatalf("known task update was lost: %#v", result.Actions[1])
	}
	_ = unknownProjectID
	_ = unknownTaskID
}

func TestParseBrainstormOutputRejectsSerializedMessage(t *testing.T) {
	_, err := parseBrainstormOutput(`{
		"message":"{\"next_question\":\"Какой результат?\"}",
		"task_actions":[]
	}`, taskContextPack{})
	if err == nil {
		t.Fatal("serialized JSON must not reach the task chat")
	}
}

func TestTaskPriorityUsesFiveTiersAndCapsRemoveRecommendation(t *testing.T) {
	high := taskEvaluatorModelOutput{
		StrategicRelevance: 950, CourseAlignment: 950, TacticalAlignment: 950,
		ExpectedImpact: 950, Urgency: 900, Effort: 200, Confidence: 900,
		Recommendation: RecommendationKeep,
	}
	highScore := CalculateTaskPriority(high)
	if highScore < 850 || PriorityTier(highScore) != "P1" {
		t.Fatalf("expected P1, got score=%d tier=%s", highScore, PriorityTier(highScore))
	}

	high.Recommendation = RecommendationRemove
	removeScore := CalculateTaskPriority(high)
	if removeScore != 250 || PriorityTier(removeScore) != "P5" {
		t.Fatalf("remove recommendation must be capped, got score=%d tier=%s", removeScore, PriorityTier(removeScore))
	}
}

func TestTaskEvaluationInputChangedOnlyForMeaningfulEvaluationFields(t *testing.T) {
	projectID := 7
	baseline := Task{Title: "Run interviews", Description: "Interview ten customers", ExpectedResult: "Five validated insights", SuccessCriteria: "Five interviews completed", WorkstreamID: 3, ProjectID: &projectID, DepartmentID: 2, BlockingTasks: []BlockingTask{{ID: 4}}}

	unchanged := baseline
	unchanged.Status = StatusInProgress
	unchanged.DepartmentID = 9
	if taskEvaluationInputChanged(baseline, unchanged) {
		t.Fatal("execution-only changes must not trigger AI evaluation")
	}

	changed := baseline
	changed.Description = "Interview ten paying customers"
	if !taskEvaluationInputChanged(baseline, changed) {
		t.Fatal("task content change must trigger AI evaluation")
	}

	otherProjectID := 8
	changed = baseline
	changed.ProjectID = &otherProjectID
	if !taskEvaluationInputChanged(baseline, changed) {
		t.Fatal("project change must trigger AI evaluation")
	}

	changed = baseline
	changed.BlockingTasks = []BlockingTask{{ID: 5}}
	if !taskEvaluationInputChanged(baseline, changed) {
		t.Fatal("dependency change must trigger AI evaluation")
	}
}

func TestParseTaskEvaluatorOutputNormalizesValues(t *testing.T) {
	raw := `{
		"strategic_relevance":1200,
		"course_alignment":800,
		"tactical_alignment":750,
		"expected_impact":700,
		"urgency":-5,
		"effort":400,
		"confidence":650,
		"recommendation":"UNKNOWN",
		"priority_reason":" Нужна проверка результата. ",
		"clarification_question":" Какой результат должен появиться? ",
		"missing_information":[" критерий успеха ",""]
	}`
	result, err := parseTaskEvaluatorOutput(raw)
	if err != nil {
		t.Fatalf("parse evaluator output: %v", err)
	}
	if result.StrategicRelevance != 1000 || result.Urgency != 0 {
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

func TestParseTaskCompletionOutput(t *testing.T) {
	result, err := parseTaskCompletionOutput(`{
		"sufficient":false,
		"quality_score":1200,
		"reason":" Непонятно, что изменилось. ",
		"missing_information":[" конкретный результат ",""]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.QualityScore != 1000 || result.Reason != "Непонятно, что изменилось." {
		t.Fatalf("unexpected completion result: %#v", result)
	}
	if len(result.MissingInformation) != 1 || result.MissingInformation[0] != "конкретный результат" {
		t.Fatalf("unexpected missing information: %#v", result.MissingInformation)
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
