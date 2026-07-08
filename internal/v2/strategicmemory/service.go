package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
)

type Service struct {
	store *Store
	ai    *ai.OpenAIClient
}

func NewService(store *Store, aiClient *ai.OpenAIClient) *Service {
	return &Service{store: store, ai: aiClient}
}

func (s *Service) State(ctx context.Context, workspaceID int) (StateResponse, error) {
	snapshot, err := s.store.LatestSnapshot(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}
	claims, err := s.store.ListClaims(ctx, workspaceID, 200)
	if err != nil {
		return StateResponse{}, err
	}
	agenda, err := s.store.ListAgenda(ctx, workspaceID, 80)
	if err != nil {
		return StateResponse{}, err
	}
	profile, err := s.store.CommunicationProfile(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}
	focus, err := s.store.DialogueFocus(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}
	documents, err := s.store.ListDocuments(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}
	messages, err := s.store.RecentMessages(ctx, workspaceID, 20)
	if err != nil {
		return StateResponse{}, err
	}

	return StateResponse{
		WorkspaceID:          workspaceID,
		Snapshot:             snapshot,
		Claims:               claims,
		Agenda:               agenda,
		CommunicationProfile: profile,
		DialogueFocus:        focus,
		Documents:            documents,
		RecentMessages:       messages,
	}, nil
}

func (s *Service) HandleMessage(ctx context.Context, workspaceID int, userID int, message string) (MessageResponse, error) {
	message = strings.TrimSpace(message)
	if len([]rune(message)) < 2 {
		return MessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return MessageResponse{}, fmt.Errorf("message_too_long")
	}

	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeUserMessage, message, map[string]any{})
	if err != nil {
		return MessageResponse{}, err
	}

	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return MessageResponse{}, err
	}
	relevantMessages, err := s.store.RelevantMessages(ctx, workspaceID, message, 8)
	if err != nil {
		return MessageResponse{}, err
	}

	contextPack := buildAuditorConversationInput(workspaceID, message, state, relevantMessages)

	rawInput, _ := json.Marshal(contextPack)
	started := time.Now()
	assistantMessage, err := s.ai.GenerateText(ctx, businessAuditorPrompt, "Контекст для ответа в формате JSON:\n"+string(rawInput))
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRun(ctx, workspaceID, "business_auditor_one_prompt", s.ai.Model, StrategicMemoryPromptVersion, duration, "failed", err.Error())
		return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
	}
	s.store.LogAIRun(ctx, workspaceID, "business_auditor_one_prompt", s.ai.Model, StrategicMemoryPromptVersion, duration, "success", "")

	assistantMessage = cleanAssistantMessage(assistantMessage)
	assistantMessage = fallbackAssistantReply(assistantMessage)
	_, _ = s.store.CreateRawSource(ctx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version": StrategicMemoryPromptVersion,
		"mode":           "one_prompt",
		"user_source_id": sourceID,
	})

	finalState, err := s.State(ctx, workspaceID)
	if err != nil {
		return MessageResponse{}, err
	}
	claims, err := s.store.ListClaims(ctx, workspaceID, 200)
	if err != nil {
		return MessageResponse{}, err
	}
	agenda, err := s.store.ListAgenda(ctx, workspaceID, 80)
	if err != nil {
		return MessageResponse{}, err
	}
	documents, err := s.store.ListDocuments(ctx, workspaceID)
	if err != nil {
		return MessageResponse{}, err
	}

	return MessageResponse{
		WorkspaceID:          workspaceID,
		AssistantMessage:     assistantMessage,
		ConversationState:    ConversationStateCollectingContext,
		MemoryUpdates:        MemoryUpdates{},
		Snapshot:             finalState.Snapshot,
		Documents:            documents,
		Agenda:               agenda,
		Claims:               claims,
		CommunicationProfile: finalState.CommunicationProfile,
		DialogueFocus:        finalState.DialogueFocus,
	}, nil
}

func (s *Service) fallbackMessageResponse(ctx context.Context, workspaceID int, state StateResponse, assistantMessage string) MessageResponse {
	assistantMessage = cleanAssistantMessage(fallbackAssistantReply(assistantMessage))
	_, _ = s.store.CreateRawSource(ctx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version": StrategicMemoryPromptVersion,
		"fallback":       true,
	})

	return MessageResponse{
		WorkspaceID:          workspaceID,
		AssistantMessage:     assistantMessage,
		ConversationState:    ConversationStateCollectingContext,
		MemoryUpdates:        MemoryUpdates{},
		Snapshot:             state.Snapshot,
		Documents:            state.Documents,
		Agenda:               state.Agenda,
		Claims:               state.Claims,
		CommunicationProfile: state.CommunicationProfile,
		DialogueFocus:        state.DialogueFocus,
	}
}

func buildAuditorConversationInput(workspaceID int, message string, state StateResponse, relevantMessages []ConversationMessage) map[string]any {
	return map[string]any{
		"workspace_id":            workspaceID,
		"latest_user_message":     message,
		"recent_dialogue":         state.RecentMessages,
		"relevant_older_dialogue": relevantMessages,
		"known_business_context": map[string]any{
			"compact_snapshot":       state.Snapshot,
			"existing_claims":        limitClaimsForContext(state.Claims, 80),
			"current_documents":      limitDocumentsForContext(state.Documents, 10),
			"open_research_agenda":   limitAgendaForContext(state.Agenda, 30),
			"current_dialogue_focus": state.DialogueFocus,
		},
		"communication_style": state.CommunicationProfile,
		"answer_goal":         "Reply to the latest user message as the AI business auditor responsible for collecting, clarifying, checking, and updating business context. Use the known context as memory, not as a questionnaire.",
	}
}

func (s *Service) Reset(ctx context.Context, workspaceID int) error {
	return s.store.Reset(ctx, workspaceID)
}

func limitClaimsForContext(claims []Claim, limit int) []Claim {
	if len(claims) <= limit {
		return claims
	}
	return claims[:limit]
}

func limitAgendaForContext(items []ResearchAgendaItem, limit int) []ResearchAgendaItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitDocumentsForContext(items []StrategicDocument, limit int) []map[string]string {
	result := make([]map[string]string, 0, len(items))
	for _, item := range items {
		markdown := item.Markdown
		if len([]rune(markdown)) > 2200 {
			runes := []rune(markdown)
			markdown = string(runes[:2200])
		}
		result = append(result, map[string]string{
			"document_type": item.DocumentType,
			"title":         item.Title,
			"markdown":      markdown,
			"status":        item.Status,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}
