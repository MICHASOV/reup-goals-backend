package strategicmemory

import (
	"context"
	"strings"
)

// CaptureStrategyFacts records strategy dialogue as a deferred, facts-only
// knowledge source. Extraction happens later at a knowledge review checkpoint.
func (s *Service) CaptureStrategyFacts(
	ctx context.Context,
	workspaceID int,
	userID int,
	strategyMessageID int,
	userMessage string,
) error {
	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeStrategyMessage, userMessage, map[string]any{
		"strategy_chat_message_id": strategyMessageID,
		"facts_only":               true,
	})
	if err != nil {
		return err
	}
	_, err = s.store.RecordKnowledgeUserTurn(ctx, workspaceID, sourceID)
	return err
}

func (s *Service) captureDocumentDiscussion(
	ctx context.Context,
	workspaceID int,
	userID int,
	documentType string,
	documentChatMessageID int,
	userMessage string,
) (int, error) {
	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeDocumentMessage, userMessage, map[string]any{
		"document_type":            strings.TrimSpace(documentType),
		"preferred_document_type":  strings.TrimSpace(documentType),
		"document_chat_message_id": documentChatMessageID,
	})
	if err != nil {
		return 0, err
	}
	if _, err := s.store.RecordKnowledgeUserTurn(ctx, workspaceID, sourceID); err != nil {
		return 0, err
	}
	return sourceID, nil
}
