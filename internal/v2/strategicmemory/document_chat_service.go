package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
)

const documentChatPromptVersion = "business_document_collaborator_v1"

func (s *Service) DocumentChatHistory(
	ctx context.Context,
	workspaceID int,
	documentType string,
) (DocumentChatHistoryResponse, error) {
	documentType = strings.TrimSpace(documentType)
	if !validStrategicDocumentType(documentType) {
		return DocumentChatHistoryResponse{}, fmt.Errorf("invalid_document_type")
	}
	document, err := s.store.DocumentByType(ctx, workspaceID, documentType)
	if err != nil {
		return DocumentChatHistoryResponse{}, err
	}
	messages, err := s.store.DocumentChatMessages(ctx, workspaceID, documentType, 300)
	if err != nil {
		return DocumentChatHistoryResponse{}, err
	}
	return DocumentChatHistoryResponse{
		WorkspaceID: workspaceID,
		Document:    document,
		Messages:    messages,
	}, nil
}

func (s *Service) HandleDocumentChatMessage(
	ctx context.Context,
	workspaceID int,
	userID int,
	documentType string,
	message string,
) (DocumentChatMessageResponse, error) {
	documentType = strings.TrimSpace(documentType)
	message = strings.TrimSpace(message)
	if !validStrategicDocumentType(documentType) {
		return DocumentChatMessageResponse{}, fmt.Errorf("invalid_document_type")
	}
	if len([]rune(message)) < 1 {
		return DocumentChatMessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return DocumentChatMessageResponse{}, fmt.Errorf("message_too_long")
	}

	document, err := s.store.DocumentByType(ctx, workspaceID, documentType)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}
	history, err := s.store.DocumentChatMessages(ctx, workspaceID, documentType, 100)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}
	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}

	userMessage, err := s.store.CreateDocumentChatMessage(
		ctx, workspaceID, documentType, &userID, "user", message, map[string]any{},
	)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}
	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeDocumentMessage, message, map[string]any{
		"document_type":            documentType,
		"document_chat_message_id": userMessage.ID,
	})
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}

	fingerprint := claimKey(fmt.Sprintf("%s|%d|%s", documentType, document.Version, document.Markdown))
	session, err := s.store.DocumentChatSession(ctx, workspaceID, documentType, s.compactThreshold, fingerprint)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}
	globalSession, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}

	input := documentChatTurnInput(message)
	if strings.TrimSpace(session.ConversationID) == "" && strings.TrimSpace(session.PreviousResponseID) != "" {
		input = documentChatFreshInput(document, history, state, message)
	}
	vectorStoreIDs := vectorStoreIDsFromSession(globalSession)
	if s.contextIndex != nil {
		indexedIDs, indexErr := s.contextIndex.Available(ctx, workspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
		}
	}
	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "business_document_chat", documentChatPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateTextNative(aiCtx, businessDocumentCollaboratorPrompt+contextindex.RetrievalInstructions, input, ai.ResponseContextOptions{
		UseConversation:      true,
		ConversationID:       session.ConversationID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       session.PromptCacheKey,
		MaxFileSearchResults: 6,
		MaxOutputTokens:      6000,
		RequestTimeout:       2 * time.Minute,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil && strings.TrimSpace(session.ConversationID) != "" && ai.IsConversationStateError(err) {
		_ = s.store.UpdateDocumentChatConversationID(ctx, workspaceID, documentType, "")
		started = time.Now()
		result, err = s.ai.GenerateTextNative(aiCtx, businessDocumentCollaboratorPrompt+contextindex.RetrievalInstructions, documentChatFreshInput(document, history, state, message), ai.ResponseContextOptions{
			UseConversation:      true,
			VectorStoreIDs:       vectorStoreIDs,
			CompactThreshold:     session.CompactThreshold,
			PromptCacheKey:       session.PromptCacheKey,
			MaxFileSearchResults: 6,
			MaxOutputTokens:      6000,
			RequestTimeout:       2 * time.Minute,
		})
		duration = time.Since(started).Milliseconds()
	}
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_chat", s.ai.ModelName(), documentChatPromptVersion, duration, 0, 0, "failed", err.Error())
		return DocumentChatMessageResponse{}, err
	}

	assistantText := cleanAssistantMessage(result.Text)
	if assistantText == "" {
		assistantText = "Не получилось сформулировать ответ. Попробуйте уточнить сообщение."
	}
	if strings.TrimSpace(result.ConversationID) != "" && result.ConversationID != session.ConversationID {
		_ = s.store.UpdateDocumentChatConversationID(ctx, workspaceID, documentType, result.ConversationID)
	}
	assistantMessage, err := s.store.CreateDocumentChatMessage(
		ctx, workspaceID, documentType, nil, "assistant", assistantText, map[string]any{
			"prompt_version":      documentChatPromptVersion,
			"response_id":         result.ResponseID,
			"conversation_id":     result.ConversationID,
			"context_fingerprint": fingerprint,
			"source_id":           sourceID,
		},
	)
	if err != nil {
		return DocumentChatMessageResponse{}, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_chat", s.ai.ModelName(), documentChatPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	s.queueDocumentContextMaterialization(workspaceID, sourceID, documentType, message, assistantText)

	return DocumentChatMessageResponse{
		WorkspaceID:      workspaceID,
		Document:         document,
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
		InputTokens:      result.Usage.InputTokens,
		OutputTokens:     result.Usage.OutputTokens,
	}, nil
}

func documentChatFreshInput(
	document StrategicDocument,
	history []DocumentChatMessage,
	state StateResponse,
	message string,
) string {
	input := map[string]any{
		"selected_document":             document,
		"document_conversation_history": history,
		"latest_user_message":           message,
		"related_business_context": map[string]any{
			"claims":         claimsForDocumentContext(state.Claims, document.DocumentType, 40),
			"quality_report": compactQualityReportForContext(state.QualityReport),
			"snapshot":       state.Snapshot,
			"files":          state.Files,
		},
	}
	raw, _ := json.Marshal(input)
	return string(raw)
}

func documentChatTurnInput(message string) string {
	raw, _ := json.Marshal(map[string]string{"latest_user_message": message})
	return string(raw)
}

func claimsForDocumentContext(claims []Claim, documentType string, limit int) []Claim {
	result := make([]Claim, 0, limit)
	for _, claim := range claims {
		if normalizeDocumentType(claim.TopicKey) != documentType {
			continue
		}
		result = append(result, claim)
		if len(result) >= limit {
			break
		}
	}
	return result
}
