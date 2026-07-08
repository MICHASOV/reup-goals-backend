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
