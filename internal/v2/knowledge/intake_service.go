package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
)

type IntakeService struct {
	store *Store
	ai    *ai.OpenAIClient
}

type pendingReconciliation struct {
	documentID   int
	documentType string
	definition   DocumentDefinition
	entries      []documentEntry
	items        []proposedItemRecord
}

type reconciledDocument struct {
	documentID   int
	documentType string
	response     ReconcilerResponse
}

func NewIntakeService(store *Store, aiClient *ai.OpenAIClient) *IntakeService {
	return &IntakeService{store: store, ai: aiClient}
}

func (s *IntakeService) BuildPreview(ctx context.Context, workspaceID int, userID int, rawText string) (IntakePreviewResponse, error) {
	sessionID, err := s.store.CreateIntakeSession(ctx, workspaceID, userID, rawText)
	if err != nil {
		return IntakePreviewResponse{}, err
	}

	documents, err := s.store.EnsureDocuments(ctx, workspaceID)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), nil)
		return IntakePreviewResponse{}, err
	}

	routerRaw, routerResponse, err := s.callRouter(ctx, workspaceID, userID, rawText)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return IntakePreviewResponse{}, err
	}
	if err := validateRouterResponse(routerResponse); err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return IntakePreviewResponse{}, err
	}
	if err := s.store.SaveRouterResponse(ctx, sessionID, routerResponse, routerRaw); err != nil {
		return IntakePreviewResponse{}, err
	}
	if err := s.store.SaveRouterItems(ctx, sessionID, workspaceID, routerResponse.Items); err != nil {
		return IntakePreviewResponse{}, err
	}

	itemsByDocument, err := s.store.ProposedItemsByDocument(ctx, sessionID)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return IntakePreviewResponse{}, err
	}

	pending := []pendingReconciliation{}
	for documentType, items := range itemsByDocument {
		documentID := documents[documentType]
		definition, ok := documentDefinitionByType(documentType)
		if !ok || documentID == 0 {
			_ = s.store.MarkSessionFailed(ctx, sessionID, "unknown_document_type", routerRaw)
			return IntakePreviewResponse{}, fmt.Errorf("unknown document type %s", documentType)
		}

		entries, err := s.store.DocumentEntries(ctx, workspaceID, documentID)
		if err != nil {
			_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
			return IntakePreviewResponse{}, err
		}

		if len(entries) == 0 {
			if err := s.store.SaveDirectAddPatches(ctx, sessionID, workspaceID, documentID, documentType, items); err != nil {
				_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
				return IntakePreviewResponse{}, err
			}
			continue
		}

		pending = append(pending, pendingReconciliation{
			documentID:   documentID,
			documentType: documentType,
			definition:   definition,
			entries:      entries,
			items:        items,
		})
	}

	reconciled, err := s.reconcileDocuments(ctx, workspaceID, userID, pending)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return IntakePreviewResponse{}, err
	}
	for _, item := range reconciled {
		if err := s.store.SaveReconcilerResponse(ctx, sessionID, workspaceID, item.documentID, item.documentType, item.response); err != nil {
			return IntakePreviewResponse{}, err
		}
	}

	if err := s.store.MarkSessionPreviewReady(ctx, sessionID); err != nil {
		return IntakePreviewResponse{}, err
	}

	preview, err := s.store.Preview(ctx, workspaceID, sessionID)
	if err != nil {
		return IntakePreviewResponse{}, err
	}
	preview.UnroutedFragments = routerResponse.UnroutedFragments
	return preview, nil
}

func (s *IntakeService) BuildGuidancePreview(ctx context.Context, workspaceID int, userID int, questionBlockID int, rawText string) (GuidancePreviewResponse, error) {
	if err := s.store.ValidateActiveQuestionBlock(ctx, workspaceID, questionBlockID); err != nil {
		return GuidancePreviewResponse{}, err
	}

	sessionID, err := s.store.CreateIntakeSession(ctx, workspaceID, userID, rawText)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	if err := s.store.AttachQuestionBlockToSession(ctx, sessionID, questionBlockID); err != nil {
		return GuidancePreviewResponse{}, err
	}

	intentOnly := classifyIntentOnlyMessage(rawText)
	if intentOnly.HasIntent {
		if err := s.store.MarkSessionPreviewReady(ctx, sessionID); err != nil {
			return GuidancePreviewResponse{}, err
		}
		return GuidancePreviewResponse{
			SessionID:          sessionID,
			Status:             "no_preview_needed",
			ConversationIntent: intentOnly,
			UpdatedDocuments:   []IntakeDocumentPreview{},
			Conflicts:          []IntakeConflict{},
			IgnoredItems:       []IntakeIgnoredItem{},
			UnroutedFragments:  []RouterUnroutedFragment{},
		}, nil
	}

	documents, err := s.store.EnsureDocuments(ctx, workspaceID)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), nil)
		return GuidancePreviewResponse{}, err
	}

	routerRaw, routerResponse, err := s.callRouter(ctx, workspaceID, userID, rawText)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return GuidancePreviewResponse{}, err
	}
	if err := validateRouterResponse(routerResponse); err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return GuidancePreviewResponse{}, err
	}
	if err := s.store.SaveRouterResponse(ctx, sessionID, routerResponse, routerRaw); err != nil {
		return GuidancePreviewResponse{}, err
	}

	intent := defaultConversationIntent(routerResponse.ConversationIntent)
	response := GuidancePreviewResponse{
		SessionID:          sessionID,
		Status:             "no_preview_needed",
		ConversationIntent: intent,
		UpdatedDocuments:   []IntakeDocumentPreview{},
		Conflicts:          []IntakeConflict{},
		IgnoredItems:       []IntakeIgnoredItem{},
		UnroutedFragments:  routerResponse.UnroutedFragments,
	}

	if len(routerResponse.Items) == 0 {
		if err := s.store.MarkSessionPreviewReady(ctx, sessionID); err != nil {
			return GuidancePreviewResponse{}, err
		}
		return response, nil
	}

	if err := s.store.SaveRouterItems(ctx, sessionID, workspaceID, routerResponse.Items); err != nil {
		return GuidancePreviewResponse{}, err
	}

	itemsByDocument, err := s.store.ProposedItemsByDocument(ctx, sessionID)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return GuidancePreviewResponse{}, err
	}

	pending := []pendingReconciliation{}
	for documentType, items := range itemsByDocument {
		documentID := documents[documentType]
		definition, ok := documentDefinitionByType(documentType)
		if !ok || documentID == 0 {
			_ = s.store.MarkSessionFailed(ctx, sessionID, "unknown_document_type", routerRaw)
			return GuidancePreviewResponse{}, fmt.Errorf("unknown document type %s", documentType)
		}

		entries, err := s.store.DocumentEntries(ctx, workspaceID, documentID)
		if err != nil {
			_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
			return GuidancePreviewResponse{}, err
		}

		if len(entries) == 0 {
			if err := s.store.SaveDirectAddPatches(ctx, sessionID, workspaceID, documentID, documentType, items); err != nil {
				_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
				return GuidancePreviewResponse{}, err
			}
			continue
		}

		pending = append(pending, pendingReconciliation{
			documentID:   documentID,
			documentType: documentType,
			definition:   definition,
			entries:      entries,
			items:        items,
		})
	}

	reconciled, err := s.reconcileDocuments(ctx, workspaceID, userID, pending)
	if err != nil {
		_ = s.store.MarkSessionFailed(ctx, sessionID, err.Error(), routerRaw)
		return GuidancePreviewResponse{}, err
	}
	for _, item := range reconciled {
		if err := s.store.SaveReconcilerResponse(ctx, sessionID, workspaceID, item.documentID, item.documentType, item.response); err != nil {
			return GuidancePreviewResponse{}, err
		}
	}

	if err := s.store.MarkSessionPreviewReady(ctx, sessionID); err != nil {
		return GuidancePreviewResponse{}, err
	}
	preview, err := s.store.Preview(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	response.Status = SessionPreviewReady
	response.UpdatedDocuments = preview.UpdatedDocuments
	response.Conflicts = preview.Conflicts
	response.IgnoredItems = preview.IgnoredItems
	response.UnroutedFragments = routerResponse.UnroutedFragments
	if len(response.UpdatedDocuments) == 0 && len(response.Conflicts) == 0 {
		response.Status = "no_preview_needed"
	}
	return response, nil
}

func (s *IntakeService) reconcileDocuments(ctx context.Context, workspaceID int, userID int, pending []pendingReconciliation) ([]reconciledDocument, error) {
	if len(pending) == 0 {
		return []reconciledDocument{}, nil
	}

	result := make([]reconciledDocument, len(pending))
	errs := make([]error, len(pending))
	var wg sync.WaitGroup
	for index, item := range pending {
		wg.Add(1)
		go func(index int, item pendingReconciliation) {
			defer wg.Done()
			_, response, err := s.callReconciler(ctx, workspaceID, userID, item.definition, item.entries, item.items)
			if err != nil {
				errs[index] = err
				return
			}
			if err := validateReconcilerResponse(response, item.documentType, item.entries, item.items); err != nil {
				errs[index] = err
				return
			}
			result[index] = reconciledDocument{
				documentID:   item.documentID,
				documentType: item.documentType,
				response:     response,
			}
		}(index, item)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *IntakeService) callRouter(ctx context.Context, workspaceID int, userID int, rawText string) (json.RawMessage, RouterResponse, error) {
	input, err := json.Marshal(map[string]any{
		"raw_text":           rawText,
		"source_segments":    sourceSegments(rawText),
		"workspace_language": "ru",
		"source_type":        "manual_text",
	})
	if err != nil {
		return nil, RouterResponse{}, err
	}

	logID, started, _ := s.store.CreateAICallLog(ctx, workspaceID, userID, "knowledge_intake_router", RouterPromptVersion, s.ai.Model, json.RawMessage(input))
	defer func(start time.Time) { _ = start }(started)
	raw, err := s.ai.GenerateJSON(ctx, routerSystemPrompt, string(input))
	s.store.FinishAICallLog(ctx, logID, started, raw, err)
	if err != nil {
		return raw, RouterResponse{}, err
	}
	var response RouterResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		retryLogID, retryStarted, _ := s.store.CreateAICallLog(ctx, workspaceID, userID, "knowledge_intake_router_retry", RouterPromptVersion, s.ai.Model, json.RawMessage(input))
		raw, retryErr := s.ai.GenerateJSON(ctx, routerSystemPrompt+jsonOnlyRetryInstruction, string(input))
		s.store.FinishAICallLog(ctx, retryLogID, retryStarted, raw, retryErr)
		if retryErr != nil {
			return raw, RouterResponse{}, err
		}
		if retryJSONErr := json.Unmarshal(raw, &response); retryJSONErr != nil {
			return raw, RouterResponse{}, retryJSONErr
		}
	}

	return raw, response, nil
}

func sourceSegments(rawText string) []string {
	normalized := strings.ReplaceAll(rawText, "\r\n", "\n")
	segments := []string{}
	for _, paragraph := range strings.Split(normalized, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if strings.Count(paragraph, ";") >= 2 {
			for _, part := range strings.Split(paragraph, ";") {
				part = strings.TrimSpace(part)
				if part != "" {
					segments = append(segments, part)
				}
			}
			continue
		}
		segments = append(segments, paragraph)
	}
	if len(segments) > 30 {
		return segments[:30]
	}
	return segments
}

func classifyIntentOnlyMessage(rawText string) ConversationIntent {
	normalized := normalizeForSignalSearch(rawText)
	if normalized == "" {
		return ConversationIntent{}
	}

	intentType := ""
	handlingNote := ""
	switch {
	case containsAny(normalized, []string{"почему", "зачем"}) && containsAny(normalized, []string{"спрашива", "вопрос"}):
		intentType = "why_question"
		handlingNote = "Пользователь спрашивает, почему задан вопрос."
	case containsAny(normalized, []string{"не хочу отвечать", "не буду отвечать", "пропусти", "пропустить", "не готов отвечать"}):
		intentType = "refusal"
		handlingNote = "Пользователь не хочет отвечать на текущий вопрос."
	case containsAny(normalized, []string{"дай совет", "посоветуй", "что мне делать", "с чего начать", "как лучше"}):
		intentType = "advice_request"
		handlingNote = "Пользователь просит совет вместо фактов для Базы знаний."
	case containsAny(normalized, []string{"раздражает", "бесит", "тупо", "плохо", "не работает", "краш"}):
		intentType = "frustration"
		handlingNote = "Пользователь выражает недовольство процессом."
	case containsAny(normalized, []string{"давай про", "хочу поговорить про", "перейдем к", "перейдём к"}):
		intentType = "topic_change_request"
		handlingNote = "Пользователь просит сменить тему."
	}
	if intentType == "" || looksLikeBusinessContext(normalized) {
		return ConversationIntent{}
	}
	return ConversationIntent{
		HasIntent:    true,
		IntentType:   intentType,
		RawText:      rawText,
		CleanText:    rawText,
		HandlingNote: handlingNote,
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func looksLikeBusinessContext(value string) bool {
	signals := []string{
		"компани", "бизнес", "продукт", "клиент", "рынок", "команда", "выруч", "прибыл", "продаж", "географ", "россия", "платящ", "сегмент", "конкурент",
	}
	hits := 0
	for _, signal := range signals {
		if strings.Contains(value, signal) {
			hits++
		}
	}
	return hits >= 2
}

func (s *IntakeService) callReconciler(ctx context.Context, workspaceID int, userID int, definition DocumentDefinition, entries []documentEntry, items []proposedItemRecord) (json.RawMessage, ReconcilerResponse, error) {
	currentEntries := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		currentEntries = append(currentEntries, map[string]string{
			"entry_id":       entryIDString(entry.ID),
			"text":           entry.Text,
			"statement_type": entry.StatementType,
		})
	}

	newItems := make([]map[string]string, 0, len(items))
	for _, item := range items {
		newItems = append(newItems, map[string]string{
			"item_id":        item.ClientItemID,
			"source_quote":   item.SourceQuote,
			"clean_text":     item.CleanText,
			"statement_type": item.StatementType,
			"confidence":     item.Confidence,
		})
	}

	input, err := json.Marshal(map[string]any{
		"document_type":   definition.Type,
		"document_title":  definition.Title,
		"current_entries": currentEntries,
		"new_items":       newItems,
	})
	if err != nil {
		return nil, ReconcilerResponse{}, err
	}

	logID, started, _ := s.store.CreateAICallLog(ctx, workspaceID, userID, "knowledge_document_reconciler", ReconcilerPromptVersion, s.ai.Model, json.RawMessage(input))
	raw, err := s.ai.GenerateJSON(ctx, reconcilerSystemPrompt, string(input))
	s.store.FinishAICallLog(ctx, logID, started, raw, err)
	if err != nil {
		return raw, ReconcilerResponse{}, err
	}
	var response ReconcilerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		retryLogID, retryStarted, _ := s.store.CreateAICallLog(ctx, workspaceID, userID, "knowledge_document_reconciler_retry", ReconcilerPromptVersion, s.ai.Model, json.RawMessage(input))
		raw, retryErr := s.ai.GenerateJSON(ctx, reconcilerSystemPrompt+jsonOnlyRetryInstruction, string(input))
		s.store.FinishAICallLog(ctx, retryLogID, retryStarted, raw, retryErr)
		if retryErr != nil {
			return raw, ReconcilerResponse{}, err
		}
		if retryJSONErr := json.Unmarshal(raw, &response); retryJSONErr != nil {
			return raw, ReconcilerResponse{}, retryJSONErr
		}
	}

	return raw, response, nil
}

func validateRouterResponse(response RouterResponse) error {
	seen := map[string]bool{}
	for _, item := range response.Items {
		if strings.TrimSpace(item.ClientItemID) == "" {
			return fmt.Errorf("router item without client_item_id")
		}
		if seen[item.ClientItemID] {
			return fmt.Errorf("duplicate router item id %s", item.ClientItemID)
		}
		seen[item.ClientItemID] = true
		if strings.TrimSpace(item.CleanText) == "" {
			return fmt.Errorf("router item without clean_text")
		}
		if !ValidStatementType(item.StatementType) {
			return fmt.Errorf("invalid statement_type %s", item.StatementType)
		}
		if !ValidDocumentType(item.TargetDocument) {
			return fmt.Errorf("invalid target_document %s", item.TargetDocument)
		}
		if !ValidConfidence(item.Confidence) {
			return fmt.Errorf("invalid confidence %s", item.Confidence)
		}
	}

	return nil
}

func validateReconcilerResponse(response ReconcilerResponse, documentType string, entries []documentEntry, items []proposedItemRecord) error {
	if response.DocumentType != documentType {
		return fmt.Errorf("reconciler document_type mismatch")
	}

	itemIDs := map[string]bool{}
	for _, item := range items {
		itemIDs[item.ClientItemID] = true
	}
	entryIDs := map[string]bool{"": true}
	for _, entry := range entries {
		entryIDs[entryIDString(entry.ID)] = true
	}

	usedItems := map[string]bool{}
	for _, patch := range response.Patches {
		if patch.PatchType != PatchTypeAdd && patch.PatchType != PatchTypeUpdate {
			return fmt.Errorf("invalid patch_type %s", patch.PatchType)
		}
		if strings.TrimSpace(patch.NewText) == "" {
			return fmt.Errorf("patch without new_text")
		}
		if patch.PatchType == PatchTypeUpdate && !entryIDs[patch.TargetEntryID] {
			return fmt.Errorf("invented target_entry_id %s", patch.TargetEntryID)
		}
		for _, itemID := range patch.SourceItemIDs {
			if !itemIDs[itemID] {
				return fmt.Errorf("invented source_item_id %s", itemID)
			}
			usedItems[itemID] = true
		}
	}

	for _, conflict := range response.Conflicts {
		if !entryIDs[conflict.ExistingEntryID] {
			return fmt.Errorf("invented existing_entry_id %s", conflict.ExistingEntryID)
		}
		if strings.TrimSpace(conflict.NewText) == "" {
			return fmt.Errorf("conflict without new_text")
		}
		for _, itemID := range conflict.SourceItemIDs {
			if !itemIDs[itemID] {
				return fmt.Errorf("invented source_item_id %s", itemID)
			}
			usedItems[itemID] = true
		}
	}

	for _, ignored := range response.IgnoredItems {
		for _, itemID := range ignored.SourceItemIDs {
			if !itemIDs[itemID] {
				return fmt.Errorf("invented source_item_id %s", itemID)
			}
			usedItems[itemID] = true
		}
	}

	for itemID := range itemIDs {
		if !usedItems[itemID] {
			return fmt.Errorf("item %s was not reconciled", itemID)
		}
	}

	return nil
}

func entryIDString(id int) string {
	return "entry_" + strconv.Itoa(id)
}
