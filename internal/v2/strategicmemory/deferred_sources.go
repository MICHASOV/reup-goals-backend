package strategicmemory

import (
	"context"
	"fmt"
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
	_, _, err := s.captureDeferredSource(ctx, workspaceID, userID, SourceCapture{
		SourceType: SourceTypeStrategyMessage,
		EntityKey:  fmt.Sprintf("strategy_message:%d", strategyMessageID),
		Content:    userMessage,
		FactsOnly:  true,
		Metadata: map[string]any{
			"strategy_chat_message_id": strategyMessageID,
		},
	})
	return err
}

func (s *Service) CaptureTacticsFacts(
	ctx context.Context,
	workspaceID int,
	userID int,
	tacticsMessageID int,
	userMessage string,
	scope any,
) error {
	_, _, err := s.captureDeferredSource(ctx, workspaceID, userID, SourceCapture{
		SourceType: SourceTypeTacticsMessage,
		EntityKey:  fmt.Sprintf("tactics_message:%d", tacticsMessageID),
		Content:    userMessage,
		FactsOnly:  true,
		Metadata: map[string]any{
			"tactics_chat_message_id": tacticsMessageID,
			"scope":                   scope,
		},
	})
	return err
}

func (s *Service) CaptureTaskDiscussionFacts(
	ctx context.Context,
	workspaceID int,
	userID int,
	messageID int,
	workstreamID int,
	userMessage string,
) error {
	_, _, err := s.captureDeferredSource(ctx, workspaceID, userID, SourceCapture{
		SourceType: SourceTypeTaskDiscussion,
		EntityKey:  fmt.Sprintf("task_discussion_message:%d", messageID),
		Content:    userMessage,
		FactsOnly:  true,
		Metadata: map[string]any{
			"task_discussion_message_id": messageID,
			"workstream_id":              workstreamID,
		},
	})
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
	sourceID, _, err := s.captureDeferredSource(ctx, workspaceID, userID, SourceCapture{
		SourceType:            SourceTypeDocumentMessage,
		EntityKey:             fmt.Sprintf("document_message:%d", documentChatMessageID),
		Content:               userMessage,
		PreferredDocumentType: strings.TrimSpace(documentType),
		Metadata: map[string]any{
			"document_type":            strings.TrimSpace(documentType),
			"document_chat_message_id": documentChatMessageID,
		},
	})
	if err != nil {
		return 0, err
	}
	return sourceID, nil
}
