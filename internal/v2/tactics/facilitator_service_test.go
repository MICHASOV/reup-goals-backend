package tactics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTacticsPromptTurnsExplicitDraftRequestsIntoProposals(t *testing.T) {
	if !strings.Contains(tacticsFacilitatorPrompt, "explicitly asks you to prepare a new or updated entity as a draft for confirmation") {
		t.Fatal("advisor prompt must return draft_changes for an explicit proposal request")
	}
	if !strings.Contains(tacticsFacilitatorPrompt, "never applies it automatically") {
		t.Fatal("advisor prompt must preserve user confirmation before applying a proposal")
	}
}

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

func TestTacticsConversationTurnRequiresProposalOnlyForExplicitCreationIntent(t *testing.T) {
	input := buildTacticsTurnInput("Давайте сначала обсудим механику", TacticsFacilitatorMessageRequest{
		DraftEntityTypeHint: EntityProject,
	})
	var discussion map[string]any
	if err := json.Unmarshal([]byte(input), &discussion); err != nil {
		t.Fatal(err)
	}
	if discussion["draft_entity_type_hint"] != EntityProject || discussion["required_output"] != nil {
		t.Fatalf("discussion must carry a hint without forcing an early proposal: %#v", discussion)
	}

	input = buildTacticsTurnInput("Подготовь проект как proposal для подтверждения", TacticsFacilitatorMessageRequest{
		DraftEntityTypeHint: EntityProject,
	})
	var creation map[string]any
	if err := json.Unmarshal([]byte(input), &creation); err != nil {
		t.Fatal(err)
	}
	required, ok := creation["required_output"].(map[string]any)
	if !ok || required["draft_entity_type"] != EntityProject || required["confirmation"] != "proposal_only" {
		t.Fatalf("explicit creation must require a project proposal: %#v", creation)
	}
}

func TestRequestedTacticsDraftTypeUsesHintAndMessageFallback(t *testing.T) {
	if got := requestedTacticsDraftType("Создай черновик для подтверждения", EntityRisk); got != EntityRisk {
		t.Fatalf("expected risk hint, got %q", got)
	}
	if got := requestedTacticsDraftType("Сформируй новую гипотезу", ""); got != EntityHypothesis {
		t.Fatalf("expected hypothesis from message, got %q", got)
	}
	if got := requestedTacticsDraftType("Давайте обсудим проект", EntityProject); got != "" {
		t.Fatalf("discussion must not require a proposal, got %q", got)
	}
}

func TestHasTacticsDraftTypeRequiresConfirmableCreate(t *testing.T) {
	if !hasTacticsDraftType([]TacticsDraftChange{{Apply: true, Operation: "create", EntityType: EntityProject}}, EntityProject) {
		t.Fatal("confirmable project create must satisfy the proposal contract")
	}
}

func TestParseRequiredDraftTreatsApplyAsProposalNotMutationPermission(t *testing.T) {
	raw := `{
		"message":"Подготовил черновик проекта для подтверждения.",
		"session_status":"in_progress",
		"status_reason":"Draft requested.",
		"current_focus":{"entity_type":"project","entity_id":null,"title":"Эксперимент","research_goal":"Проверить канал"},
		"decisions_detected":[],
		"open_questions":[],
		"needs_strategy_review":false,
		"strategy_review_reason":"",
		"draft_changes":[{
			"apply":false,
			"operation":"create",
			"entity_type":"project",
			"parent_entity_type":"workstream",
			"parent_entity_id":42,
			"title":"Легальный paid acquisition эксперимент",
			"description":"Проверить легальный платный канал.",
			"why_needed":"Нужен проверенный канал привлечения.",
			"success_criteria":"Получен воспроизводимый CAC.",
			"failure_criteria":"CAC не позволяет получать вклад в прибыль.",
			"expected_value":"Новый прибыльный канал.",
			"lead_department_id":3,
			"metrics":[{"name":"CAC","target":"75","unit":"USD","better_direction":"decrease"}]
		}]
	}`
	output, err := parseTacticsFacilitatorOutputForDraft(raw, EntityProject)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTacticsDraftType(output.DraftChanges, EntityProject) {
		t.Fatalf("explicit draft must remain confirmable: %#v", output.DraftChanges)
	}
}

func TestBuildRequestedProjectDraftUsesAdvisorContentAndActiveWorkstream(t *testing.T) {
	scope := &TacticsMessageScope{EntityType: EntityWorkstream, EntityID: 42, Label: "Продажи"}
	output := tacticsFacilitatorModelOutput{
		Message:      "Проверим легальный платный канал без автоматического применения.",
		StatusReason: "Проект нужен для проверки CAC.",
		CurrentFocus: TacticsFocus{
			EntityType:   EntityProject,
			Title:        "Легальный paid acquisition эксперимент",
			ResearchGoal: "Получить измеримый CAC и contribution signal.",
		},
	}
	change := buildRequestedTacticsDraft(
		EntityProject,
		"Создай проект",
		output,
		scope,
		TacticsFacilitatorState{},
	)
	if !change.Apply || change.Operation != "create" || change.EntityType != EntityProject {
		t.Fatalf("unexpected proposal: %#v", change)
	}
	if change.ParentEntityType != EntityWorkstream || change.ParentEntityID == nil || *change.ParentEntityID != 42 {
		t.Fatalf("project parent = %#v", change)
	}
	if change.Title != output.CurrentFocus.Title || change.Description != output.Message {
		t.Fatalf("advisor content was not preserved: %#v", change)
	}
}

func TestRecoverTacticsFacilitatorOutputAvoidsSerializedPayload(t *testing.T) {
	state := TacticsFacilitatorState{
		Session: TacticsSessionState{
			OpenQuestions: []string{"Какой baseline используется?"},
		},
	}
	output := recoverTacticsFacilitatorOutput(`{"message":`, state)
	if output.Message == "" || strings.HasPrefix(output.Message, "{") {
		t.Fatalf("expected safe user-facing message, got %q", output.Message)
	}
	if output.StatusReason != "The advisor response was recovered locally without an additional AI request." {
		t.Fatalf("unexpected status reason: %q", output.StatusReason)
	}
	if len(output.OpenQuestions) != 1 || output.OpenQuestions[0] != "Какой baseline используется?" {
		t.Fatalf("expected existing open questions to be preserved: %#v", output.OpenQuestions)
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

func TestParseTacticsFacilitatorOutputUnwrapsDoubleEncodedResponse(t *testing.T) {
	inner := `{"message":"Нормальный ответ пользователю.","session_status":"in_progress","current_focus":{},"decisions_detected":[],"open_questions":[],"needs_strategy_review":false}`
	raw, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	output, err := parseTacticsFacilitatorOutput(string(raw))
	if err != nil {
		t.Fatalf("double encoded response must be accepted: %v", err)
	}
	if output.Message != "Нормальный ответ пользователю." {
		t.Fatalf("unexpected message: %q", output.Message)
	}
}

func TestParseTacticsFacilitatorOutputUnwrapsSerializedMessageEnvelope(t *testing.T) {
	raw := `{
		"message":"{\"message\":\"Проверим экономику проекта.\",\"session_status\":\"in_progress\",\"current_focus\":{},\"decisions_detected\":[],\"open_questions\":[],\"needs_strategy_review\":false}",
		"session_status":"in_progress"
	}`
	output, err := parseTacticsFacilitatorOutput(raw)
	if err != nil {
		t.Fatalf("serialized envelope must be accepted: %v", err)
	}
	if output.Message != "Проверим экономику проекта." {
		t.Fatalf("unexpected message: %q", output.Message)
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
