package knowledge

import "strings"

func messageRequestsFullQuestionChecklist(latestMessage string, intent ConversationIntent) bool {
	text := strings.ToLower(strings.Join([]string{
		latestMessage,
		intent.RawText,
		intent.CleanText,
		intent.HandlingNote,
	}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	fullListSignals := []string{
		"полный список",
		"весь список",
		"список всех",
		"все вопросы",
		"всех вопросов",
		"чеклист",
		"checklist",
		"опросник",
		"анкета",
	}
	scopeSignals := []string{
		"по всем блокам",
		"по всем документам",
		"базы знаний",
		"базе знаний",
		"все блоки",
		"все документы",
		"одним документом",
		"одним большим сообщением",
	}
	hasFullListSignal := false
	for _, signal := range fullListSignals {
		if strings.Contains(text, signal) {
			hasFullListSignal = true
			break
		}
	}
	if !hasFullListSignal {
		return false
	}
	for _, signal := range scopeSignals {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return strings.Contains(text, "сразу") || strings.Contains(text, "загруз")
}
