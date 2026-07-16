package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
)

const autoQualityAuditThrottle = 10 * time.Minute

type Service struct {
	store                  *Store
	ai                     *ai.OpenAIClient
	compactThreshold       int
	qualityAuditMu         sync.Mutex
	qualityAuditReservedAt map[int]time.Time
}

func NewService(store *Store, aiClient *ai.OpenAIClient, compactThreshold int) *Service {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	return &Service{
		store:                  store,
		ai:                     aiClient,
		compactThreshold:       compactThreshold,
		qualityAuditReservedAt: map[int]time.Time{},
	}
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
	qualityReport, err := s.store.LatestQualityReport(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}
	messages, err := s.store.RecentMessages(ctx, workspaceID, 20)
	if err != nil {
		return StateResponse{}, err
	}
	files, err := s.store.ListFiles(ctx, workspaceID)
	if err != nil {
		return StateResponse{}, err
	}

	return StateResponse{
		WorkspaceID:          workspaceID,
		DocumentCatalog:      strategicDocumentDefinitions(),
		Snapshot:             snapshot,
		Claims:               claims,
		Agenda:               agenda,
		QualityReport:        qualityReport,
		CommunicationProfile: profile,
		DialogueFocus:        focus,
		Documents:            documents,
		RecentMessages:       messages,
		Files:                files,
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
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return MessageResponse{}, err
	}
	relevantMessages, err := s.store.RelevantMessages(ctx, workspaceID, message, 8)
	if err != nil {
		return MessageResponse{}, err
	}

	input := message
	if strings.TrimSpace(session.PreviousResponseID) == "" {
		contextPack := buildAuditorConversationInput(workspaceID, message, state, relevantMessages)
		rawInput, _ := json.Marshal(contextPack)
		input = "Контекст для ответа в формате JSON:\n" + string(rawInput)
	}
	vectorStoreIDs := vectorStoreIDsFromSession(session)
	started := time.Now()
	result, err := s.ai.GenerateTextNative(ctx, businessAuditorPrompt, input, ai.ResponseContextOptions{
		PreviousResponseID:   session.PreviousResponseID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       session.PromptCacheKey,
		MaxFileSearchResults: 8,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		if strings.TrimSpace(session.PreviousResponseID) != "" {
			_ = s.store.UpdateOpenAIPreviousResponseID(ctx, workspaceID, "")
			retryInput := buildFreshContextInput(workspaceID, message, state, relevantMessages)
			started = time.Now()
			result, err = s.ai.GenerateTextNative(ctx, businessAuditorPrompt, retryInput, ai.ResponseContextOptions{
				VectorStoreIDs:       vectorStoreIDs,
				CompactThreshold:     session.CompactThreshold,
				PromptCacheKey:       session.PromptCacheKey,
				MaxFileSearchResults: 8,
			})
			duration = time.Since(started).Milliseconds()
		}
		if err != nil {
			s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.Model, StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
			return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
		}
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.Model, StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	if strings.TrimSpace(result.ResponseID) != "" {
		_ = s.store.UpdateOpenAIPreviousResponseID(ctx, workspaceID, result.ResponseID)
	}

	assistantMessage := cleanAssistantMessage(result.Text)
	assistantMessage = fallbackAssistantReply(assistantMessage)
	_, _ = s.store.CreateRawSource(ctx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version":       StrategicMemoryPromptVersion,
		"mode":                 "openai_native",
		"user_source_id":       sourceID,
		"response_id":          result.ResponseID,
		"previous_response_id": session.PreviousResponseID,
		"vector_store_ids":     vectorStoreIDs,
	})
	s.queueBusinessContextMaterialization(workspaceID, sourceID, message, assistantMessage)

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
		OpenAIResponseID:     result.ResponseID,
	}, nil
}

func (s *Service) UploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (FileUploadResponse, error) {
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return FileUploadResponse{}, err
	}

	vectorStoreID := strings.TrimSpace(session.VectorStoreID)
	if vectorStoreID == "" {
		vectorStore, err := s.ai.CreateVectorStore(ctx, fmt.Sprintf("reupgoals-workspace-%d-strategic-context", workspaceID))
		if err != nil {
			return FileUploadResponse{}, err
		}
		vectorStoreID = vectorStore.ID
		if err := s.store.UpdateOpenAIVectorStoreID(ctx, workspaceID, vectorStoreID, s.compactThreshold); err != nil {
			return FileUploadResponse{}, err
		}
	}

	uploadedFile, err := s.ai.UploadFile(ctx, filename, "assistants", file)
	if err != nil {
		return FileUploadResponse{}, err
	}

	rawSourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeFileUpload, "Uploaded file: "+filename, map[string]any{
		"filename":        filename,
		"content_type":    contentType,
		"size_bytes":      sizeBytes,
		"openai_file_id":  uploadedFile.ID,
		"vector_store_id": vectorStoreID,
	})
	if err != nil {
		return FileUploadResponse{}, err
	}

	vectorFile, err := s.ai.AddFileToVectorStore(ctx, vectorStoreID, uploadedFile.ID)
	status := "uploaded"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	} else {
		status = defaultString(vectorFile.Status, "processing")
		vectorStoreFileID := defaultString(vectorFile.ID, uploadedFile.ID)
		readyFile, waitErr := s.ai.WaitVectorStoreFileReady(ctx, vectorStoreID, vectorStoreFileID, uploadedFile.ID, 45*time.Second)
		if waitErr != nil {
			status = "failed"
			errorText = waitErr.Error()
		} else if strings.TrimSpace(readyFile.Status) != "" {
			status = readyFile.Status
		}
	}

	fileID := rawSourceID
	item, saveErr := s.store.CreateStrategicFile(ctx, workspaceID, &fileID, uploadedFile.ID, vectorStoreID, filename, contentType, sizeBytes, status, errorText)
	if saveErr != nil {
		return FileUploadResponse{}, saveErr
	}
	if errorText != "" {
		return FileUploadResponse{WorkspaceID: workspaceID, File: item}, fmt.Errorf("%s", errorText)
	}
	return FileUploadResponse{WorkspaceID: workspaceID, File: item}, nil
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
			"quality_report":         compactQualityReportForContext(state.QualityReport),
			"current_dialogue_focus": state.DialogueFocus,
		},
		"communication_style": state.CommunicationProfile,
		"answer_goal":         "Reply to the latest user message as the AI business auditor responsible for collecting, clarifying, checking, and updating business context. Use the known context as memory, not as a questionnaire.",
	}
}

func buildFreshContextInput(workspaceID int, message string, state StateResponse, relevantMessages []ConversationMessage) string {
	contextPack := buildAuditorConversationInput(workspaceID, message, state, relevantMessages)
	rawInput, _ := json.Marshal(contextPack)
	return "Контекст для ответа в формате JSON:\n" + string(rawInput)
}

func vectorStoreIDsFromSession(session OpenAISession) []string {
	if strings.TrimSpace(session.VectorStoreID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(session.VectorStoreID)}
}

func (s *Service) Reset(ctx context.Context, workspaceID int) error {
	s.qualityAuditMu.Lock()
	delete(s.qualityAuditReservedAt, workspaceID)
	s.qualityAuditMu.Unlock()
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
