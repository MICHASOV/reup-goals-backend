package agent

import (
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
