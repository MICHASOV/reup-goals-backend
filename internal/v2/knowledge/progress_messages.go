package knowledge

import (
	"fmt"
	"sort"
	"strings"
)

func initialIntakeProgressMessage(rawText string) string {
	text := strings.ToLower(rawText)
	layers := []string{}
	if hasAny(text, "компан", "продукт", "сервис", "saas", "платформ") {
		layers = append(layers, "что делает компания")
	}
	if hasAny(text, "клиент", "сегмент", "спрос", "аудитор", "покупател") {
		layers = append(layers, "клиентов и спрос")
	}
	if hasAny(text, "цена", "руб", "выруч", "марж", "ltv", "cac", "эконом") {
		layers = append(layers, "экономику")
	}
	if hasAny(text, "команд", "ресурс", "разработ", "продаж", "маркет") {
		layers = append(layers, "ресурсы и команду")
	}
	if hasAny(text, "не хот", "не будем", "отказ", "огранич", "нельзя", "риск") {
		layers = append(layers, "ограничения и отказы")
	}

	if len(layers) == 0 {
		return "Читаю ответ целиком: сначала отделю проверяемые факты от общего описания, потом разложу их по документам."
	}
	if len(layers) > 4 {
		layers = layers[:4]
	}
	return fmt.Sprintf("Вижу в ответе несколько слоёв: %s. Сейчас отделю факты от формулировок и разложу их по Базе знаний.", joinHuman(layers))
}

func routerProgressMessage(response RouterResponse) string {
	count := len(response.Items)
	if count == 0 {
		return "Пока не нашёл фактов, которые можно безопасно записать в документы. Проверяю, не является ли это просто уточнением по разговору."
	}
	documents := topRouterDocuments(response, 3)
	if len(documents) == 0 {
		return fmt.Sprintf("Нашёл %d %s. Сейчас проверяю, где они обновляют документы, а где могут дублировать уже записанное.", count, pluralizeFact(count))
	}
	return fmt.Sprintf("Нашёл %d %s. Больше всего сейчас про: %s.", count, pluralizeFact(count), joinHuman(documents))
}

func routerProgressDetails(response RouterResponse) map[string]any {
	counts := map[string]int{}
	for _, item := range response.Items {
		counts[item.TargetDocument]++
	}
	return map[string]any{
		"facts":                   len(response.Items),
		"documents":               counts,
		"unrouted_fragments":      len(response.UnroutedFragments),
		"conversation_intent":     response.ConversationIntent.IntentType,
		"has_conversation_intent": response.ConversationIntent.HasIntent,
	}
}

func documentRoutingProgressMessage(itemsByDocument map[string][]proposedItemRecord) string {
	documents := []string{}
	total := 0
	for documentType, items := range itemsByDocument {
		total += len(items)
		documents = append(documents, documentTitle(documentType))
	}
	sort.Strings(documents)
	if len(documents) == 0 {
		return "Факты не легли в документы достаточно уверенно. Лучше задам уточняющий вопрос, чем запишу сомнительную формулировку."
	}
	return fmt.Sprintf("Раскладываю %d %s по %d %s: %s.", total, pluralizeFact(total), len(documents), pluralizeDocument(len(documents)), joinHuman(limitStrings(documents, 4)))
}

func documentRoutingDetails(itemsByDocument map[string][]proposedItemRecord) map[string]any {
	counts := map[string]int{}
	for documentType, items := range itemsByDocument {
		counts[documentType] = len(items)
	}
	return map[string]any{"documents": counts}
}

func previewProgressMessage(response GuidancePreviewResponse) string {
	patches := 0
	documents := []string{}
	for _, document := range response.UpdatedDocuments {
		if len(document.Patches) == 0 {
			continue
		}
		patches += len(document.Patches)
		documents = append(documents, document.Title)
	}
	if patches == 0 && len(response.Conflicts) == 0 {
		return "Сверка закончена: новых изменений для документов нет. Сейчас сформирую следующий вопрос по слабому месту контекста."
	}
	if len(response.Conflicts) > 0 {
		return fmt.Sprintf("Подготовил %d %s и вижу %d %s, где смысл лучше уточнить вопросом.", patches, pluralizeChange(patches), len(response.Conflicts), pluralizeConflict(len(response.Conflicts)))
	}
	return fmt.Sprintf("Подготовил %d %s в %d %s: %s.", patches, pluralizeChange(patches), len(documents), pluralizeDocument(len(documents)), joinHuman(limitStrings(documents, 4)))
}

func nextQuestionProgressMessage(block GuidanceQuestionBlock) string {
	focus := strings.TrimSpace(block.IntendedFocusSummary)
	if focus != "" {
		return fmt.Sprintf("Следующий вопрос строю вокруг зоны: %s.", focus)
	}
	if len(block.Questions) > 0 {
		return "Следующий вопрос готов: он должен закрыть ближайшую неопределённость, а не просто продолжить анкету."
	}
	return "Следующий шаг готов. Можно продолжать диалог."
}

func topRouterDocuments(response RouterResponse, limit int) []string {
	counts := map[string]int{}
	for _, item := range response.Items {
		counts[item.TargetDocument]++
	}
	type pair struct {
		documentType string
		count        int
	}
	pairs := []pair{}
	for documentType, count := range counts {
		pairs = append(pairs, pair{documentType: documentType, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].documentType < pairs[j].documentType
		}
		return pairs[i].count > pairs[j].count
	})
	result := []string{}
	for _, item := range pairs {
		result = append(result, documentTitle(item.documentType))
		if len(result) >= limit {
			break
		}
	}
	return result
}

func documentTitle(documentType string) string {
	if definition, ok := documentDefinitionByType(documentType); ok {
		return definition.Title
	}
	return documentType
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func joinHuman(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " и " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + " и " + values[len(values)-1]
	}
}

func pluralizeFact(count int) string {
	return pluralRu(count, "факт", "факта", "фактов")
}

func pluralizeChange(count int) string {
	return pluralRu(count, "изменение", "изменения", "изменений")
}

func pluralizeDocument(count int) string {
	return pluralRu(count, "документ", "документа", "документов")
}

func pluralizeConflict(count int) string {
	return pluralRu(count, "место", "места", "мест")
}

func pluralRu(count int, one string, few string, many string) string {
	mod10 := count % 10
	mod100 := count % 100
	if mod10 == 1 && mod100 != 11 {
		return one
	}
	if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
		return few
	}
	return many
}
