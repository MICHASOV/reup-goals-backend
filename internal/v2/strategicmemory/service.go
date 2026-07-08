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

	contextPack := buildContextPack(workspaceID, message, state)

	rawInput, _ := json.Marshal(contextPack)
	started := time.Now()
	raw, err := s.ai.GenerateJSON(ctx, strategicMemoryPrompt, string(rawInput))
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRun(ctx, workspaceID, "strategic_memory_message", s.ai.Model, StrategicMemoryPromptVersion, duration, "failed", err.Error())
		return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
	}
	s.store.LogAIRun(ctx, workspaceID, "strategic_memory_message", s.ai.Model, StrategicMemoryPromptVersion, duration, "success", "")

	var aiResponse aiMemoryResponse
	if err := json.Unmarshal(raw, &aiResponse); err != nil {
		s.store.LogAIRun(ctx, workspaceID, "strategic_memory_message_parse", s.ai.Model, StrategicMemoryPromptVersion, duration, "failed", err.Error())
		return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
	}

	claimInputs := make([]aiMemoryResponseClaim, 0, len(aiResponse.Claims))
	for _, claim := range aiResponse.Claims {
		claimInputs = append(claimInputs, aiMemoryResponseClaim{
			ClaimText:     claim.ClaimText,
			ClaimType:     claim.ClaimType,
			TopicKey:      claim.TopicKey,
			EvidenceLevel: claim.EvidenceLevel,
			Confidence:    claim.Confidence,
		})
	}
	claimsAdded, claimsSkipped, err := s.store.InsertClaims(ctx, workspaceID, sourceID, claimInputs)
	if err != nil {
		return MessageResponse{}, err
	}

	snapshot, err := s.store.SaveSnapshot(ctx, workspaceID, aiResponse.BusinessStage, aiResponse.Snapshot)
	if err != nil {
		return MessageResponse{}, err
	}

	profile := CommunicationProfile{
		WorkspaceID:            workspaceID,
		Tone:                   aiResponse.CommunicationProfile.Tone,
		AddressStyle:           aiResponse.CommunicationProfile.AddressStyle,
		DetailLevel:            aiResponse.CommunicationProfile.DetailLevel,
		StructurePreference:    aiResponse.CommunicationProfile.StructurePreference,
		FrustrationSensitivity: aiResponse.CommunicationProfile.FrustrationSensitivity,
		KnownPreferences:       mustJSON(aiResponse.CommunicationProfile.KnownPreferences),
	}
	profile, err = s.store.UpsertCommunicationProfile(ctx, workspaceID, profile)
	if err != nil {
		return MessageResponse{}, err
	}

	dialogueFocus := mergeDialogueFocus(state.DialogueFocus, DialogueFocus{
		WorkspaceID:        workspaceID,
		CurrentTopic:       aiResponse.DialogueFocus.CurrentTopic,
		ResearchGoal:       aiResponse.DialogueFocus.ResearchGoal,
		LastQuestion:       aiResponse.DialogueFocus.LastQuestion,
		ExpectedAnswerType: aiResponse.DialogueFocus.ExpectedAnswerType,
		AnswerStatus:       aiResponse.DialogueFocus.AnswerStatus,
		DoNotRepeat:        mustJSON(mergeStringSlices(rawStringSlice(state.DialogueFocus.DoNotRepeat), aiResponse.DialogueFocus.DoNotRepeat)),
		NextAngles:         mustJSON(mergeStringSlices(rawStringSlice(state.DialogueFocus.NextAngles), aiResponse.DialogueFocus.NextAngles)),
	})
	dialogueFocus = enrichDialogueFocusFromUserMessage(dialogueFocus, message)
	dialogueFocus, err = s.store.UpsertDialogueFocus(ctx, workspaceID, dialogueFocus)
	if err != nil {
		return MessageResponse{}, err
	}

	agendaInputs := make([]ResearchAgendaItem, 0, len(aiResponse.ResearchAgenda))
	for _, item := range aiResponse.ResearchAgenda {
		agendaInputs = append(agendaInputs, ResearchAgendaItem{
			WorkspaceID:    workspaceID,
			TopicKey:       item.TopicKey,
			QuestionGoal:   item.QuestionGoal,
			WhyItMatters:   item.WhyItMatters,
			Status:         item.Status,
			Priority:       item.Priority,
			LinkedClaimIDs: json.RawMessage(`[]`),
		})
	}
	agendaUpdated, err := s.store.UpsertAgenda(ctx, workspaceID, agendaInputs)
	if err != nil {
		return MessageResponse{}, err
	}

	documentInputs := make([]StrategicDocument, 0, len(aiResponse.Documents))
	for _, doc := range aiResponse.Documents {
		documentInputs = append(documentInputs, StrategicDocument{
			WorkspaceID:    workspaceID,
			DocumentType:   doc.DocumentType,
			Title:          doc.Title,
			Markdown:       doc.Markdown,
			Status:         doc.Status,
			SourceClaimIDs: json.RawMessage(`[]`),
		})
	}
	if len(documentInputs) == 0 && (len(aiResponse.Claims) > 0 || len(aiResponse.Snapshot) > 0) {
		documentInputs = fallbackStrategicDocuments(workspaceID, aiResponse.BusinessStage, aiResponse.Snapshot)
	}
	if len(documentInputs) == 0 && len([]rune(message)) >= 120 {
		documentInputs = fallbackStrategicDocumentFromMessage(workspaceID, message)
	}
	documentsUpdated, err := s.store.UpsertDocuments(ctx, workspaceID, documentInputs)
	if err != nil {
		return MessageResponse{}, err
	}

	assistantMessage := cleanAssistantMessage(aiResponse.AssistantMessage)
	assistantMessage = fallbackAssistantReply(assistantMessage)
	_, _ = s.store.CreateRawSource(ctx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version": StrategicMemoryPromptVersion,
	})

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
		WorkspaceID:       workspaceID,
		AssistantMessage:  assistantMessage,
		ConversationState: defaultString(aiResponse.ConversationState, ConversationStateCollectingContext),
		MemoryUpdates: MemoryUpdates{
			ClaimsAdded:      claimsAdded,
			ClaimsSkipped:    claimsSkipped,
			AgendaUpdated:    agendaUpdated,
			DocumentsUpdated: documentsUpdated,
		},
		Snapshot:             snapshot,
		Documents:            documents,
		Agenda:               agenda,
		Claims:               claims,
		CommunicationProfile: profile,
		DialogueFocus:        dialogueFocus,
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

func buildContextPack(workspaceID int, message string, state StateResponse) map[string]any {
	hints := dialogueHintsFromUserMessage(message)
	doNotRepeat := mergeStringSlices(rawStringSlice(state.DialogueFocus.DoNotRepeat), hints.DoNotRepeat)
	nextAngles := mergeStringSlices(rawStringSlice(state.DialogueFocus.NextAngles), hints.NextAngles)

	return map[string]any{
		"workspace_id":        workspaceID,
		"latest_user_message": message,
		"recent_dialogue":     state.RecentMessages,
		"business_context": map[string]any{
			"snapshot":          state.Snapshot,
			"active_claims":     limitClaimsForContext(state.Claims, 90),
			"current_documents": limitDocumentsForContext(state.Documents, 12),
			"research_agenda":   limitAgendaForContext(state.Agenda, 40),
		},
		"dialogue_state": map[string]any{
			"current_focus":         state.DialogueFocus,
			"derived_do_not_repeat": doNotRepeat,
			"derived_next_angles":   nextAngles,
			"latest_message_hints":  hints,
		},
		"communication_style": state.CommunicationProfile,
		"product_goal":        "Understand the business deeply enough to later build strategy, course, tactics, and execution on facts, hypotheses, unknowns, and constraints.",
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
