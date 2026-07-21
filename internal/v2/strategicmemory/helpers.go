package strategicmemory

import (
	"encoding/json"
	"fmt"
	"strings"
)

func cleanText(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func cleanAssistantMessage(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(
		"【】", "",
		"【 】", "",
		"właśnie", "именно",
		"verdadeiro", "реальное",
		"verdadeira", "реальная",
		"і", "и",
		"ї", "и",
		"є", "е",
		"ł", "л",
		".DEFINE_", " ",
		"DEFINE_", " ",
	)
	value = replacer.Replace(value)
	return stripInternalAssistantSections(value)
}

func stripInternalAssistantSections(value string) string {
	paragraphs := strings.Split(value, "\n\n")
	kept := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		trimmed := strings.TrimSpace(paragraph)
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(lower, "обновляю ") ||
			strings.Contains(lower, "обновляю бизнес snapshot") ||
			strings.Contains(lower, "обновляю исследовательский план") ||
			strings.Contains(lower, "обновляю профиль коммуникации") ||
			strings.Contains(lower, "обновляю стратегическую память") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.TrimSpace(strings.Join(kept, "\n\n"))
}

type dialogueHints struct {
	DoNotRepeat []string `json:"do_not_repeat"`
	NextAngles  []string `json:"next_angles"`
	TurnSignals []string `json:"turn_signals"`
}

func fallbackAssistantReply(value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return "Понял. Давайте продолжим разбирать бизнес-контекст с того места, где остановились."
}

func unavailableAssistantReply(state StateResponse) string {
	if strings.TrimSpace(state.DialogueFocus.CurrentTopic) != "" {
		return "Принял сообщение, но подробный разбор сейчас занял слишком много времени. Чтобы не подвешивать диалог, продолжим проще: по текущей теме мне важнее всего понять, что уже подтверждено фактами, а что пока остается гипотезой."
	}
	return "Принял сообщение, но подробный разбор сейчас занял слишком много времени. Давай продолжим с основы: что уже существует в бизнесе сейчас, а что пока только гипотеза или план?"
}

func dialogueHintsFromUserMessage(message string) dialogueHints {
	normalized := strings.ToLower(message)
	hints := dialogueHints{}

	if containsAny(normalized, "нет данных", "нет никакой информации", "нет информации", "нет отзыв", "нет клиентов", "нет оплат", "нет подтвержден") {
		hints.DoNotRepeat = append(hints.DoNotRepeat, "Не спрашивать снова, есть ли клиенты, оплаты, отзывы или подтверждение спроса; это уже отмечено как отсутствующее на текущей стадии.")
		hints.NextAngles = append(hints.NextAngles, "Перейти от вопроса наличия доказательств к дизайну проверки гипотезы спроса.")
		hints.TurnSignals = append(hints.TurnSignals, "user_says_data_unavailable_now")
	}
	if containsAny(normalized, "маркетингов", "агентств", "20 ", "двадцать", "технократич", "пробный период", "циклы обратной связи") {
		hints.NextAngles = append(hints.NextAngles,
			"Уточнить дизайн пилота: критерии отбора тестовой группы, сценарий пробного периода, формат обратной связи.",
			"Уточнить, какие события продукта будут считаться признаками ценности во время теста.",
		)
		hints.TurnSignals = append(hints.TurnSignals, "user_describes_validation_plan")
	}
	if containsAny(normalized, "ретенш", "retention", "возвращаем", "40%", "40 %", "2 месяц", "второй месяц") {
		hints.DoNotRepeat = append(hints.DoNotRepeat, "Не спрашивать снова, какая метрика успеха; целевой ориентир уже назван как возвращаемость/retention на второй месяц около 40%.")
		hints.NextAngles = append(hints.NextAngles,
			"Уточнить механику измерения возвращаемости на второй месяц.",
			"Уточнить, какое поведение пользователя считается реальным возвращением, а не случайным визитом.",
		)
		hints.TurnSignals = append(hints.TurnSignals, "user_names_success_metric")
	}
	if containsAny(normalized, "по кругу", "я же", "уже написал", "уже сказал", "не спрашивай", "блять", "блядь") {
		hints.DoNotRepeat = append(hints.DoNotRepeat, "Пользователь раздражён повтором; признать коррекцию и сменить угол вместо повторения старого вопроса.")
		hints.TurnSignals = append(hints.TurnSignals, "user_frustrated_by_repetition")
	}
	if containsAny(normalized, "список вопросов", "все вопросы", "вопросы списком", "одним большим сообщением", "чеклист") {
		hints.TurnSignals = append(hints.TurnSignals, "user_requests_question_list")
	}
	return hints
}

func enrichDialogueFocusFromUserMessage(focus DialogueFocus, message string) DialogueFocus {
	hints := dialogueHintsFromUserMessage(message)
	focus.DoNotRepeat = mustJSON(mergeStringSlices(rawStringSlice(focus.DoNotRepeat), hints.DoNotRepeat))
	focus.NextAngles = mustJSON(mergeStringSlices(rawStringSlice(focus.NextAngles), hints.NextAngles))
	if focus.AnswerStatus == "" {
		focus.AnswerStatus = "open"
	}
	if containsAny(strings.ToLower(message), "нет данных", "нет никакой информации", "нет информации", "нет подтвержден") {
		focus.AnswerStatus = "unavailable_now"
	}
	if containsAny(strings.ToLower(message), "ретенш", "retention", "возвращаем", "40%") {
		focus.AnswerStatus = "answered"
	}
	return focus
}

func mergeDialogueFocus(previous DialogueFocus, next DialogueFocus) DialogueFocus {
	if strings.TrimSpace(next.CurrentTopic) == "" {
		next.CurrentTopic = previous.CurrentTopic
	}
	if strings.TrimSpace(next.ResearchGoal) == "" {
		next.ResearchGoal = previous.ResearchGoal
	}
	if strings.TrimSpace(next.LastQuestion) == "" {
		next.LastQuestion = previous.LastQuestion
	}
	if strings.TrimSpace(next.ExpectedAnswerType) == "" {
		next.ExpectedAnswerType = previous.ExpectedAnswerType
	}
	if strings.TrimSpace(next.AnswerStatus) == "" {
		next.AnswerStatus = previous.AnswerStatus
	}
	next.DoNotRepeat = mustJSON(mergeStringSlices(rawStringSlice(previous.DoNotRepeat), rawStringSlice(next.DoNotRepeat)))
	next.NextAngles = mustJSON(mergeStringSlices(rawStringSlice(previous.NextAngles), rawStringSlice(next.NextAngles)))
	return next
}

func rawStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return compactStrings(values)
}

func mergeStringSlices(groups ...[]string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, item := range compactStrings(group) {
			key := strings.ToLower(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, item)
		}
	}
	return result
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeClaimType(value string) string {
	switch strings.TrimSpace(value) {
	case "fact", "self_reported_fact", "historical_fact", "process", "problem", "constraint", "risk", "opportunity", "hypothesis", "goal", "plan", "decision", "task", "metric", "result", "opinion", "assumption", "open_question", "contradiction", "unknown", "not_tested", "not_disclosed", "evidence":
		return value
	default:
		return "self_reported_fact"
	}
}

func normalizeEvidenceLevel(value string) string {
	switch strings.TrimSpace(value) {
	case "none", "founder_belief", "theoretical", "self_reported", "customer_signal", "payment", "metric", "repeated_pattern", "external_document":
		return value
	default:
		return "self_reported"
	}
}

func normalizeConfidence(value string) string {
	switch strings.TrimSpace(value) {
	case "low", "medium", "high":
		return value
	default:
		return "medium"
	}
}

func normalizeBusinessStage(value string) string {
	switch strings.TrimSpace(value) {
	case "idea", "launch", "validation", "early_traction", "growth", "scale", "mature", "turnaround":
		return value
	default:
		return "unknown"
	}
}

func normalizeAgendaStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "open", "answered", "unavailable_now", "deferred", "do_not_ask_again":
		return value
	default:
		return "open"
	}
}

func normalizePriority(value string) string {
	switch strings.TrimSpace(value) {
	case "low", "medium", "high", "critical":
		return value
	default:
		return "medium"
	}
}

func normalizeTopicKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Trim(value, "._")
	if value == "" {
		return "general"
	}
	return value
}

func documentTitle(documentType string) string {
	switch normalizeDocumentType(documentType) {
	case "company_governance":
		return "Компания и управление"
	case "strategy_development":
		return "Стратегия и развитие"
	case "product_value":
		return "Продукт и ценность"
	case "customers_market_competition":
		return "Клиенты, рынок и конкуренты"
	case "marketing_sales_relationships":
		return "Маркетинг, продажи и клиентские отношения"
	case "operations_execution":
		return "Операционная деятельность и исполнение"
	case "team_organization":
		return "Команда и организация"
	case "technology_data_assets":
		return "Технологии, данные и активы"
	case "finance_economics":
		return "Финансы и экономика"
	case "legal_compliance":
		return "Право и соответствие требованиям"
	case "hypotheses_assumptions":
		return "Гипотезы и непроверенные предположения"
	case "open_questions":
		return "Открытые вопросы"
	case "contradictions_changes":
		return "Противоречия и изменения"
	case "constraints":
		return "Ограничения"
	default:
		return "Стратегическая память"
	}
}

func fallbackStrategicDocuments(workspaceID int, businessStage string, snapshot map[string]any) []StrategicDocument {
	if len(snapshot) == 0 {
		return nil
	}

	sections := []string{
		"# Текущий снимок компании",
	}
	if businessStage != "" && businessStage != "unknown" {
		sections = append(sections, fmt.Sprintf("**Стадия:** %s", businessStageLabel(businessStage)))
	}
	appendTextSection := func(title string, key string) {
		if value := snapshotText(snapshot, key); value != "" {
			sections = append(sections, fmt.Sprintf("## %s\n%s", title, value))
		}
	}
	appendListSection := func(title string, key string) {
		items := snapshotList(snapshot, key)
		if len(items) == 0 {
			return
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, "- "+item)
		}
		sections = append(sections, fmt.Sprintf("## %s\n%s", title, strings.Join(lines, "\n")))
	}

	appendTextSection("Короткое описание", "short_summary")
	appendTextSection("Продукт", "product")
	appendTextSection("Клиент", "customer")
	appendTextSection("Спрос", "demand")
	appendTextSection("Рынок", "market")
	appendTextSection("Экономика", "economics")
	appendTextSection("Команда", "team")
	appendListSection("Ограничения", "constraints")
	appendListSection("Доказательства", "evidence")
	appendListSection("Гипотезы", "hypotheses")
	appendListSection("Неизвестности", "unknowns")
	appendTextSection("Следующий фокус исследования", "next_research_focus")

	if len(sections) <= 1 {
		return nil
	}
	return []StrategicDocument{
		{
			WorkspaceID:  workspaceID,
			DocumentType: "company_snapshot",
			Title:        "Текущий снимок компании",
			Markdown:     strings.Join(sections, "\n\n"),
			Status:       DefaultStrategicDocumentStatus,
		},
	}
}

func fallbackStrategicDocumentFromMessage(workspaceID int, message string) []StrategicDocument {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return []StrategicDocument{
		{
			WorkspaceID:  workspaceID,
			DocumentType: "company_snapshot",
			Title:        "Первичный контекст компании",
			Markdown: strings.Join([]string{
				"# Первичный контекст компании",
				"## Исходное описание",
				message,
				"## Статус",
				"Этот документ создан как первичная фиксация бизнес-контекста. Его нужно уточнять через дальнейший диалог со стратегическим директором.",
			}, "\n\n"),
			Status: DefaultStrategicDocumentStatus,
		},
	}
}

func businessStageLabel(value string) string {
	switch normalizeBusinessStage(value) {
	case "idea":
		return "идея / гипотеза"
	case "launch":
		return "запуск"
	case "validation":
		return "проверка спроса"
	case "early_traction":
		return "первые подтверждения спроса"
	case "growth":
		return "рост"
	case "scale":
		return "масштабирование"
	case "mature":
		return "зрелый бизнес"
	case "turnaround":
		return "перестройка"
	default:
		return "не определена"
	}
}

func snapshotText(snapshot map[string]any, key string) string {
	value, ok := snapshot[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func snapshotList(snapshot map[string]any, key string) []string {
	value, ok := snapshot[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return compactStrings(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" && text != "<nil>" {
				items = append(items, text)
			}
		}
		return items
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" || text == "<nil>" {
			return nil
		}
		return []string{text}
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func clampScore(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func normalizeReadinessStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "ready", "ready_for_strategy_development":
		return "ready"
	case "ready_with_limitations", "ready_with_important_limitations":
		return "ready_with_limitations"
	case "not_ready", "not_ready_for_strategy_development", "insufficient":
		return "not_ready"
	default:
		return "not_ready"
	}
}
