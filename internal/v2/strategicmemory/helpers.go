package strategicmemory

import "strings"

func cleanText(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
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
