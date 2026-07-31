package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"reup-goals-backend/internal/v2/tactics"
)

func TestRunTokenRejectsTamperingAndExpiry(t *testing.T) {
	run := Run{PublicID: "run_test", WorkspaceID: 12, UserID: 34}
	token, err := signRunToken(strings.Repeat("s", 40), run, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifyRunToken(strings.Repeat("s", 40), token, run.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if claims.WorkspaceID != run.WorkspaceID || claims.UserID != run.UserID {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := verifyRunToken(strings.Repeat("x", 40), token, run.PublicID); err == nil {
		t.Fatal("token signed with another secret must be rejected")
	}
	if _, err := verifyRunToken(strings.Repeat("s", 40), token, "run_other"); err == nil {
		t.Fatal("token must be bound to the expected run")
	}
	expired, err := signRunToken(strings.Repeat("s", 40), run, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunToken(strings.Repeat("s", 40), expired, run.PublicID); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestAgentStateEncryptionIsBoundToRun(t *testing.T) {
	secret := strings.Repeat("k", 40)
	ciphertext, err := encryptState(secret, "run_one", `{"state":"pending"}`)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := decryptState(secret, "run_one", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != `{"state":"pending"}` {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := decryptState(secret, "run_two", ciphertext); err == nil {
		t.Fatal("state must not be reusable by another run")
	}
	if _, err := decryptState(strings.Repeat("z", 40), "run_one", ciphertext); err == nil {
		t.Fatal("state encrypted with another secret must be rejected")
	}
}

func TestQuotaTokenUsageDiscountsCachedInput(t *testing.T) {
	usage := RuntimeUsage{
		InputTokens: 10000, CachedInputTokens: 8000, OutputTokens: 1000, TotalTokens: 11000,
	}
	if got := quotaTokenUsage(usage); got != 3800 {
		t.Fatalf("quota usage = %d, want 3800", got)
	}
}

func TestValidAttachment(t *testing.T) {
	if !validAttachment(Attachment{Type: "project", ID: 12, Label: "Проект"}) {
		t.Fatal("valid project attachment was rejected")
	}
	if !validAttachment(Attachment{Type: "knowledge_document", Key: "finance", Label: "Финансы"}) {
		t.Fatal("valid knowledge document attachment was rejected")
	}
	if validAttachment(Attachment{Type: "project", Label: "Без идентификатора"}) {
		t.Fatal("project attachment without id must be rejected")
	}
	if validAttachment(Attachment{Type: "external_url", Key: "https://example.com", Label: "URL"}) {
		t.Fatal("unknown attachment type must be rejected")
	}
}

func TestDraftChangeSupportsCreateAndUpdate(t *testing.T) {
	created, ok := draftChange("propose_project", map[string]any{
		"direction_id":     22,
		"title":            "Проверить новый канал",
		"description":      "Контролируемый эксперимент с ограниченным бюджетом.",
		"expected_result":  "Подтверждённая экономика канала",
		"why_needed":       "Снять неопределённость роста",
		"success_criteria": "CAC ниже порога",
		"failure_criteria": "CAC выше порога",
		"expected_value":   "Рост выручки",
		"department_id":    7,
		"metric": map[string]any{
			"name": "CAC", "target": "100", "unit": "USD",
		},
	})
	if !ok || created.EntityType != tactics.EntityProject || created.Operation != "create" {
		t.Fatalf("unexpected create draft: %#v", created)
	}
	if created.ParentEntityID == nil || *created.ParentEntityID != 22 {
		t.Fatalf("unexpected project parent: %#v", created.ParentEntityID)
	}
	if created.ExpectedResult != "Подтверждённая экономика канала" || created.ExpectedValue != "Рост выручки" {
		t.Fatalf("project result fields were lost: %#v", created)
	}

	updated, ok := draftChange("propose_task", map[string]any{
		"existing_entity_id": 91,
		"project_id":         31,
		"title":              "Подготовить выборку",
		"description":        "Собрать данные для принятия решения.",
		"expected_result":    "Проверенная выборка",
		"department_id":      7,
		"owner_deferred":     true,
		"due_date_deferred":  true,
	})
	if !ok || updated.Operation != "update" || updated.EntityID == nil || *updated.EntityID != 91 {
		t.Fatalf("unexpected update draft: %#v", updated)
	}
}

func TestDepartmentDraftKeepsPeopleAndKPIs(t *testing.T) {
	change, ok := draftChange("propose_department", map[string]any{
		"name":            "Продажи",
		"description":     "Отвечает за управляемое привлечение выручки.",
		"responsibility":  "Выполнение плана валовой прибыли",
		"manager_user_id": 19,
		"member_user_ids": []any{19.0, 23.0},
		"kpis": []any{map[string]any{
			"name": "Валовая прибыль", "current": "0", "target": "1000000", "unit": "RUB",
		}},
	})
	if !ok || change.EntityType != tactics.EntityDepartment {
		t.Fatalf("unexpected department draft: %#v", change)
	}
	if change.OwnerUserID == nil || *change.OwnerUserID != 19 || len(change.MemberUserIDs) != 2 {
		t.Fatalf("department people were lost: %#v", change)
	}
	if len(change.Metrics) != 1 || change.Metrics[0].Name != "Валовая прибыль" {
		t.Fatalf("department KPI was lost: %#v", change.Metrics)
	}
}

func TestIntSlicePreservesJSONNumbers(t *testing.T) {
	got := intSlice([]any{json.Number("7"), float64(8), 9, "10", json.Number("-1"), "bad"})
	want := []int{7, 8, 9, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intSlice() = %#v, want %#v", got, want)
	}
}

func TestDraftPackageKeepsParentReferencesAndMetricDirection(t *testing.T) {
	project, ok := draftChange("propose_project", map[string]any{
		"draft_key": "project-growth", "parent_draft_key": "direction-growth",
		"title": "Проверить канал", "description": "Проверить канал на ограниченном бюджете.",
		"expected_result": "Подтверждённый канал", "why_needed": "Снять неопределённость",
		"success_criteria": "CAC ниже 100", "failure_criteria": "CAC выше 150",
		"expected_value": "Рост выручки", "department_id": 7,
		"metric": map[string]any{
			"name": "CAC", "target": "100", "unit": "USD", "better_direction": "decrease",
		},
	})
	if !ok || project.DraftKey != "project-growth" || project.ParentDraftKey != "direction-growth" {
		t.Fatalf("project package references were lost: %#v", project)
	}
	if len(project.Metrics) != 1 || project.Metrics[0].BetterDirection != "decrease" {
		t.Fatalf("metric direction was lost: %#v", project.Metrics)
	}

	task, ok := draftChange("propose_task", map[string]any{
		"draft_key": "task-sample", "parent_draft_key": "project-growth",
		"title": "Собрать выборку", "description": "Собрать исходные данные.",
		"expected_result": "Готовая выборка", "department_id": 7,
		"owner_deferred": true, "due_date_deferred": true,
	})
	if !ok || task.ParentDraftKey != "project-growth" {
		t.Fatalf("task parent reference was lost: %#v", task)
	}
}

func TestDraftChangePreservesDirectionRiskAndHypothesisFields(t *testing.T) {
	direction, ok := draftChange("propose_direction", map[string]any{
		"draft_key": "direction-retention", "title": "Удержание клиентов",
		"description": "Системно повышать повторные покупки и маржинальность клиентской базы.",
		"expected_result": "Предсказуемая повторная выручка", "ckp": "Повторные покупки растут",
		"rationale": "Это главный ограничитель прибыли", "lead_department_id": 7,
		"participant_department_ids": []any{8.0, json.Number("9")},
		"metrics": []any{map[string]any{
			"name": "Repeat revenue share", "current": "18", "target": "30",
			"unit": "%", "better_direction": "increase",
		}},
	})
	if !ok || direction.EntityType != tactics.EntityWorkstream ||
		direction.Goal == "" || direction.CKP == "" || direction.LeadDepartmentID != 7 {
		t.Fatalf("direction fields were lost: %#v", direction)
	}
	if !reflect.DeepEqual(direction.ParticipantDepartmentIDs, []int{8, 9}) {
		t.Fatalf("direction participants were lost: %#v", direction.ParticipantDepartmentIDs)
	}

	risk, ok := draftChange("propose_risk", map[string]any{
		"existing_entity_id": 44, "entity_type": "project", "entity_id": 31,
		"title": "Рост стоимости привлечения", "description": "CAC может выйти за предел экономики.",
		"severity": "high", "probability": "medium", "leading_indicators": "CAC растёт две недели",
		"mitigation_plan": "Остановить слабые сегменты", "contingency_plan": "Переключить бюджет",
		"owner_user_id": 19,
	})
	if !ok || risk.Operation != "update" || risk.ParentEntityID == nil ||
		*risk.ParentEntityID != 31 || risk.MitigationPlan == "" || risk.ContingencyPlan == "" {
		t.Fatalf("risk fields were lost: %#v", risk)
	}

	hypothesis, ok := draftChange("propose_hypothesis", map[string]any{
		"entity_type": "project", "entity_id": 31, "title": "Повторное предложение",
		"statement": "Повторным клиентам нужен отдельный оффер.",
		"expected_effect": "Рост повторной выручки", "test_method": "A/B тест на двух когортах",
		"success_signal": "Repeat revenue share выше на 5 п.п.", "owner_user_id": 19,
	})
	if !ok || hypothesis.Statement == "" || hypothesis.ExpectedEffect == "" ||
		!strings.Contains(hypothesis.TestMethod, "Критерий решения") {
		t.Fatalf("hypothesis fields were lost: %#v", hypothesis)
	}
}

func TestStrategyReviewDraftKeepsCompleteStrategy(t *testing.T) {
	change, ok := draftChange("propose_strategy_review", map[string]any{
		"strategic_goal":            "Доказать устойчивую экономику продукта",
		"current_state":             "Первые продажи и непроверенная повторяемость",
		"target_state":              "Стабильная прибыльная модель привлечения",
		"economic_engine":           "Повторные продажи финансируют привлечение",
		"key_metric":                "Contribution margin",
		"strategic_logic":           "Сначала подтверждаем экономику ядра, затем масштабируем.",
		"deliberate_non_priorities": "Новые географии и побочные продукты",
		"risks_and_assumptions":     "Спрос может оказаться недостаточно повторяемым",
	})
	if !ok || change.EntityType != "strategy_review" || change.Operation != "submit" {
		t.Fatalf("unexpected strategy review draft: %#v", change)
	}
	if change.CurrentState == "" || change.TargetState == "" || change.EconomicEngine == "" {
		t.Fatalf("strategy context was lost: %#v", change)
	}
	if change.KeyMetric != "Contribution margin" || change.DeliberateNonPriorities == "" {
		t.Fatalf("strategy decision fields were lost: %#v", change)
	}
}

func TestAgentInputAddsOnlyAttachmentReferences(t *testing.T) {
	input := agentInput("Обсудим проект", []Attachment{{
		Type: "project", ID: 42, Label: "Новый рынок",
	}})
	if !strings.Contains(input, "project: Новый рынок (42)") {
		t.Fatalf("attachment reference missing: %q", input)
	}
	if strings.Contains(input, "markdown") || strings.Contains(input, "content") {
		t.Fatalf("attachment content must not be duplicated into the user turn: %q", input)
	}
}

func TestCompatibleSessionPinsReleaseModelPromptAndGeneration(t *testing.T) {
	session := ThreadSession{
		Found:             true,
		AgentReleaseID:    DefaultRelease,
		Model:             "gpt-test",
		PromptVersion:     PromptVersion,
		SessionGeneration: 2,
	}
	if !compatibleSession(session, DefaultRelease, "gpt-test", PromptVersion) {
		t.Fatal("matching session must remain reusable")
	}
	if compatibleSession(session, "next-release", "gpt-test", PromptVersion) {
		t.Fatal("a new release must start a new session generation")
	}
	if compatibleSession(session, DefaultRelease, "gpt-next", PromptVersion) {
		t.Fatal("a model change must start a new session generation")
	}
	if compatibleSession(session, DefaultRelease, "gpt-test", "next-prompt") {
		t.Fatal("a prompt change must start a new session generation")
	}
}

func TestBuildContinuityContextUsesRecentConversationAndBoundsSize(t *testing.T) {
	messages := []tactics.TacticsChatMessage{
		{Role: "user", Content: "Старое сообщение"},
		{Role: "assistant", Content: strings.Repeat("а", 3000)},
		{Role: "user", Content: "Текущее решение компании"},
	}
	context := buildContinuityContext(messages)
	if !strings.Contains(context, "Пользователь: Текущее решение компании") {
		t.Fatalf("latest user context is missing: %q", context)
	}
	if len([]rune(context)) > continuityMaxRunes {
		t.Fatalf("continuity context is too large: %d", len([]rune(context)))
	}
}
