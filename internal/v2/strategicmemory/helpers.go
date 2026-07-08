package strategicmemory

import (
	"fmt"
	"strings"
)

func cleanText(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func cleanAssistantMessage(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(
		"właśnie", "именно",
		"verdadeiro", "реальное",
		"verdadeira", "реальная",
		"і", "и",
		"ї", "и",
		"є", "е",
		"ł", "л",
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

func shapeAssistantReply(value string, snapshot map[string]any, latestUserMessage string) string {
	value = strings.TrimSpace(value)
	latest := strings.ToLower(latestUserMessage)
	if asksForQuestionChecklist(latest) && !hasNumberedList(value) {
		return questionChecklistReply()
	}
	if isFrustratedByRepetition(latest) && isGenericAssistantReply(value) {
		return repetitionCorrectionReply()
	}
	if value == "" {
		value = defaultAssistantReply(latest)
	}
	if strings.Contains(value, "?") {
		return value
	}
	focus := strings.ToLower(snapshotText(snapshot, "next_research_focus"))
	if strings.Contains(focus, "спрос") || strings.Contains(focus, "аудитор") || strings.Contains(focus, "клиент") {
		return value + "\n\n**Следующий вопрос**\n\nРасскажите, какие конкретные гипотезы спроса вы хотите проверить первыми: кто должен почувствовать эту боль, в какой ситуации она возникает, и какой сигнал покажет, что проблема действительно острая?"
	}
	if strings.Contains(focus, "эконом") || strings.Contains(focus, "модель") || strings.Contains(focus, "выруч") {
		return value + "\n\n**Следующий вопрос**\n\nРасскажите, как сейчас выглядит предполагаемая экономика: за что пользователь будет платить, какой ценовой диапазон кажется реалистичным, и на чём держится эта гипотеза?"
	}
	if strings.Contains(focus, "огранич") || strings.Contains(focus, "ресурс") || strings.Contains(focus, "команд") {
		return value + "\n\n**Следующий вопрос**\n\nРасскажите, какие ограничения сильнее всего влияют на запуск прямо сейчас: время, команда, деньги, доступ к аудитории, скорость разработки или что-то другое?"
	}
	return value + "\n\n**Следующий вопрос**\n\nРасскажите подробнее о текущей реальности: что уже существует, что проверено хотя бы частично, и какие ключевые неизвестности сейчас мешают двигаться увереннее?"
}

func asksForQuestionChecklist(value string) bool {
	return strings.Contains(value, "список вопросов") ||
		strings.Contains(value, "все вопросы") ||
		strings.Contains(value, "вопросы списком") ||
		strings.Contains(value, "одним большим сообщением") ||
		strings.Contains(value, "чеклист")
}

func hasNumberedList(value string) bool {
	lines := strings.Split(value, "\n")
	count := 0
	for _, line := range lines {
		if matched := strings.HasPrefix(strings.TrimSpace(line), "1.") ||
			strings.HasPrefix(strings.TrimSpace(line), "1)"); matched {
			count++
		}
		if count > 0 && (strings.HasPrefix(strings.TrimSpace(line), "2.") ||
			strings.HasPrefix(strings.TrimSpace(line), "2)")) {
			return true
		}
	}
	return false
}

func isFrustratedByRepetition(value string) bool {
	return strings.Contains(value, "по кругу") ||
		strings.Contains(value, "повтор") ||
		strings.Contains(value, "не спрашивай") ||
		strings.Contains(value, "уже сказал")
}

func isGenericAssistantReply(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" ||
		strings.Contains(value, "что ещё важно знать") ||
		strings.Contains(value, "продолжу собирать бизнес-память")
}

func defaultAssistantReply(latestUserMessage string) string {
	if isFrustratedByRepetition(latestUserMessage) {
		return repetitionCorrectionReply()
	}
	return "**Следующий вопрос**\n\nРасскажите подробнее о текущей реальности: что уже существует, что проверено хотя бы частично, и какие ключевые неизвестности сейчас мешают двигаться увереннее?"
}

func repetitionCorrectionReply() string {
	return "Понял, ты прав: к вопросу клиентов и оплат сейчас не возвращаюсь.\n\n**Следующий вопрос**\n\nДавай зайдём с другой стороны: какой сегмент тебе кажется самым вероятным первым пользователем REUP.goals, в какой конкретной ситуации у него возникает хаос с задачами, и почему именно в этот момент он должен почувствовать ценность стратегического кабинета?"
}

func questionChecklistReply() string {
	return "Да, так будет быстрее: можно ответить одним большим сообщением, а я разложу это в стратегическую память.\n\n**Список вопросов**\n\n1. Что именно представляет собой продукт сейчас: прототип, сервис, консультационная услуга, приложение или связка нескольких форматов?\n2. Для кого он в первую очередь: какой самый вероятный первый сегмент пользователей?\n3. В какой конкретной ситуации у этого сегмента возникает боль, которую решает продукт?\n4. Как пользователь сейчас решает эту проблему без вас?\n5. Почему текущие альтернативы для него недостаточны или неудобны?\n6. Какая главная гипотеза спроса у вас есть сейчас?\n7. Что уже проверено хотя бы частично, даже если это только разговоры, наблюдения или личный опыт?\n8. Что пока является чистой гипотезой без подтверждений?\n9. Какой минимальный тест спроса можно провести в ближайшее время?\n10. Какие сигналы будут означать, что гипотеза сильная: заявки, оплаты, интервью, повторяющаяся боль, готовность попробовать продукт?\n11. Какие ограничения сейчас сильнее всего мешают проверке: время, команда, деньги, доступ к аудитории, скорость разработки?\n12. Что точно не стоит делать или проверять сейчас, чтобы не распыляться?"
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
	case "fact", "self_reported_fact", "hypothesis", "assumption", "plan", "unknown", "not_tested", "not_disclosed", "evidence", "risk", "constraint", "contradiction":
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
	switch normalizeTopicKey(documentType) {
	case "company_profile", "company", "company_card":
		return "Профиль компании"
	case "market_hypothesis", "market":
		return "Гипотеза рынка"
	case "customers_and_demand", "customer", "demand":
		return "Клиенты и спрос"
	case "validation_plan":
		return "План проверки спроса"
	case "business_model":
		return "Бизнес-модель"
	case "evidence_and_unknowns":
		return "Доказательства и неизвестности"
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
