package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/jobs"
)

const autoQualityAuditThrottle = 10 * time.Minute

type Service struct {
	store                  *Store
	ai                     ai.Provider
	compactThreshold       int
	qualityAuditMu         sync.Mutex
	qualityAuditReservedAt map[int]time.Time
	jobs                   *jobs.Manager
	contextIndex           *contextindex.Service
}

func (s *Service) SetContextIndex(index *contextindex.Service) {
	s.contextIndex = index
}

func (s *Service) workspaceContextVectorStoreIDs(ctx context.Context, workspaceID int, session OpenAISession) ([]string, bool) {
	fallback := vectorStoreIDsFromSession(session)
	if s.contextIndex == nil {
		return fallback, false
	}
	indexed, err := s.contextIndex.Ensure(ctx, workspaceID)
	if err != nil || len(indexed) == 0 {
		return fallback, false
	}
	return indexed, true
}

func NewService(store *Store, aiClient ai.Provider, compactThreshold int, managers ...*jobs.Manager) *Service {
	if compactThreshold <= 0 {
		compactThreshold = 60000
	}
	service := &Service{
		store:                  store,
		ai:                     aiClient,
		compactThreshold:       compactThreshold,
		qualityAuditReservedAt: map[int]time.Time{},
	}
	if len(managers) > 0 && managers[0] != nil {
		service.jobs = managers[0]
		service.registerJobHandlers()
	}
	return service
}

func (s *Service) State(ctx context.Context, workspaceID int) (StateResponse, error) {
	var state StateResponse
	state.WorkspaceID = workspaceID
	state.DocumentCatalog = strategicDocumentDefinitions()
	loads := []func() error{
		func() error {
			var err error
			state.Snapshot, err = s.store.LatestSnapshot(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.Claims, err = s.store.ListClaims(ctx, workspaceID, 200)
			return err
		}, func() error {
			var err error
			state.Agenda, err = s.store.ListAgenda(ctx, workspaceID, 80)
			return err
		}, func() error {
			var err error
			state.CommunicationProfile, err = s.store.CommunicationProfile(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.DialogueFocus, err = s.store.DialogueFocus(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.Documents, err = s.store.ListDocuments(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.QualityReport, err = s.store.LatestQualityReport(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.RecentMessages, err = s.store.RecentMessages(ctx, workspaceID, 20)
			return err
		}, func() error {
			var err error
			state.Files, err = s.store.ListFiles(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.Pipeline, err = s.store.KnowledgePipelineState(ctx, workspaceID)
			if err == nil {
				state.OnboardingSummary, err = s.store.OnboardingSummary(ctx, workspaceID)
			}
			return err
		},
	}
	if err := runBoundedLoads(4, loads); err != nil {
		return StateResponse{}, err
	}
	return state, nil
}

// WorkspaceState is the read model used by the knowledge-base screen. It avoids
// loading claims, research agenda and AI-only memory that the UI does not render.
func (s *Service) WorkspaceState(ctx context.Context, workspaceID int) (StateResponse, error) {
	state := StateResponse{
		WorkspaceID:     workspaceID,
		DocumentCatalog: strategicDocumentDefinitions(),
		Claims:          []Claim{},
		Agenda:          []ResearchAgendaItem{},
	}
	loads := []func() error{
		func() error {
			var err error
			state.Documents, err = s.store.ListDocuments(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.QualityReport, err = s.store.LatestQualityReport(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.RecentMessages, err = s.store.RecentMessages(ctx, workspaceID, 20)
			return err
		}, func() error {
			var err error
			state.Files, err = s.store.ListFiles(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.Pipeline, err = s.store.KnowledgePipelineState(ctx, workspaceID)
			return err
		}, func() error {
			var err error
			state.OnboardingSummary, err = s.store.OnboardingSummary(ctx, workspaceID)
			return err
		},
	}
	if err := runBoundedLoads(4, loads); err != nil {
		return StateResponse{}, err
	}
	return state, nil
}

// runBoundedLoads keeps state hydration fast without exhausting the small
// managed Postgres pool when several users open the workspace at once.
func runBoundedLoads(limit int, loads []func() error) error {
	if limit <= 0 {
		limit = 1
	}
	results := make(chan error, len(loads))
	semaphore := make(chan struct{}, limit)
	for _, load := range loads {
		semaphore <- struct{}{}
		go func(load func() error) {
			defer func() { <-semaphore }()
			results <- load()
		}(load)
	}
	var firstErr error
	for range loads {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) HandleMessage(ctx context.Context, workspaceID int, userID int, message string) (MessageResponse, error) {
	message = strings.TrimSpace(message)
	if len([]rune(message)) < 2 {
		return MessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return MessageResponse{}, fmt.Errorf("message_too_long")
	}
	currentPipeline, err := s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return MessageResponse{}, err
	}
	currentSummary, err := s.store.OnboardingSummary(ctx, workspaceID)
	if err != nil {
		return MessageResponse{}, err
	}
	if knowledgeInputLocked(currentPipeline.Status) || currentSummary != nil {
		return MessageResponse{}, fmt.Errorf("knowledge_context_locked")
	}

	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeUserMessage, message, map[string]any{})
	if err != nil {
		return MessageResponse{}, err
	}
	pipeline, err := s.store.RecordKnowledgeUserTurn(ctx, workspaceID, sourceID)
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

	turnInput := map[string]any{"latest_user_message": message}
	feedbackIncluded := false
	if pipeline.Status == KnowledgePipelineNeedsMoreContext &&
		pipeline.CandidateRevision > pipeline.FeedbackDeliveredRevision && len(pipeline.AuditFeedback) > 0 {
		var feedback any
		if json.Unmarshal(pipeline.AuditFeedback, &feedback) == nil {
			turnInput["independent_audit_feedback"] = feedback
			feedbackIncluded = true
		}
	}
	if strings.TrimSpace(session.ConversationID) == "" && len(state.RecentMessages) > 1 {
		turnInput["one_time_local_continuity"] = buildAuditorConversationInput(workspaceID, message, state, relevantMessages)
	}
	rawTurnInput, _ := json.Marshal(turnInput)
	input := string(rawTurnInput)
	vectorStoreIDs := vectorStoreIDsFromSession(session)
	if s.contextIndex != nil && hasStructuredKnowledgeContext(state) {
		indexedIDs, indexErr := s.contextIndex.Available(ctx, workspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
		} else if indexErr != nil {
			s.store.LogAIRunWithUsage(ctx, workspaceID, "workspace_context_sync", s.ai.ModelName(), StrategicMemoryPromptVersion, 0, 0, 0, "failed", indexErr.Error())
		}
	}
	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "business_auditor_openai_native", StrategicMemoryPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, businessAuditorPrompt+contextindex.RetrievalInstructions, input, ai.ResponseContextOptions{
		UseConversation:      true,
		ConversationID:       session.ConversationID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       session.PromptCacheKey,
		MaxFileSearchResults: 4,
		MaxOutputTokens:      4000,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		if strings.TrimSpace(session.ConversationID) != "" && ai.IsConversationStateError(err) {
			_ = s.store.UpdateOpenAIConversationID(ctx, workspaceID, "")
			retryInput, _ := json.Marshal(map[string]any{
				"latest_user_message":       message,
				"one_time_local_continuity": buildAuditorConversationInput(workspaceID, message, state, relevantMessages),
			})
			started = time.Now()
			result, err = s.ai.GenerateJSONNative(aiCtx, businessAuditorPrompt+contextindex.RetrievalInstructions, string(retryInput), ai.ResponseContextOptions{
				UseConversation:      true,
				VectorStoreIDs:       vectorStoreIDs,
				CompactThreshold:     session.CompactThreshold,
				PromptCacheKey:       session.PromptCacheKey,
				MaxFileSearchResults: 4,
				MaxOutputTokens:      4000,
			})
			duration = time.Since(started).Milliseconds()
		}
		if err != nil {
			s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
			if ai.IsCallRejected(err) {
				return MessageResponse{}, err
			}
			return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
		}
	}
	turn, parseErr := parseAuditorTurn(result.Text)
	if parseErr != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", parseErr.Error())
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(aiCtx, businessAuditorPrompt+contextindex.RetrievalInstructions, "Repair your previous response. Return valid JSON matching the required output contract. The reply field must contain natural user-facing prose, never JSON or a serialized object. Preserve the intended meaning and do not ask the user to repeat anything.", ai.ResponseContextOptions{
			UseConversation:      true,
			ConversationID:       result.ConversationID,
			VectorStoreIDs:       vectorStoreIDs,
			CompactThreshold:     session.CompactThreshold,
			PromptCacheKey:       session.PromptCacheKey,
			MaxFileSearchResults: 4,
			MaxOutputTokens:      4000,
		})
		duration = time.Since(started).Milliseconds()
		if err == nil {
			turn, parseErr = parseAuditorTurn(result.Text)
		}
		if err != nil || parseErr != nil {
			errorText := "business auditor response repair failed"
			if err != nil {
				errorText = err.Error()
			} else if parseErr != nil {
				errorText = parseErr.Error()
			}
			s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", errorText)
			if ai.IsCallRejected(err) {
				return MessageResponse{}, err
			}
			return s.fallbackMessageResponse(ctx, workspaceID, state, unavailableAssistantReply(state)), nil
		}
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_auditor_openai_native", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	persistCtx, cancelPersistence := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelPersistence()
	if strings.TrimSpace(result.ConversationID) != "" && result.ConversationID != session.ConversationID {
		_ = s.store.UpdateOpenAIConversationID(persistCtx, workspaceID, result.ConversationID)
	}

	if feedbackIncluded {
		_ = s.store.MarkKnowledgeFeedbackDelivered(persistCtx, workspaceID, pipeline.CandidateRevision)
	}
	contextReady, readinessReason := turn.contextReadinessDecision()
	assistantMessage := fallbackAssistantReply(cleanAssistantMessage(turn.Reply))
	if _, err := s.store.CreateRawSource(persistCtx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version":   StrategicMemoryPromptVersion,
		"mode":             "openai_native",
		"user_source_id":   sourceID,
		"response_id":      result.ResponseID,
		"conversation_id":  result.ConversationID,
		"vector_store_ids": vectorStoreIDs,
		"context_ready":    contextReady,
	}); err != nil {
		return MessageResponse{}, fmt.Errorf("save assistant message: %w", err)
	}
	if contextReady {
		if err := s.queueOnboardingSummary(persistCtx, workspaceID, userID, pipeline, sourceID, readinessReason); err != nil {
			// The user turn and the auditor answer are already durable. If the
			// background handoff fails, keep the interview usable instead of
			// turning a successful AI response into a generic client error.
			_ = s.store.DeleteOnboardingSummary(persistCtx, workspaceID, pipeline.ConversationRevision, sourceID)
			log.Printf("[WARN] onboarding summary enqueue failed workspace_id=%d revision=%d: %v", workspaceID, pipeline.ConversationRevision, err)
		} else {
			assistantMessage = ""
		}
	}

	finalState, err := s.State(persistCtx, workspaceID)
	if err != nil {
		// The AI result has already been persisted. A temporary hydration failure
		// must not turn a completed turn into a user-visible failed request.
		finalState = state
		finalState.Pipeline = pipeline
	}

	return messageResponseFromState(workspaceID, assistantMessage, result.ResponseID, finalState), nil
}

func messageResponseFromState(workspaceID int, assistantMessage string, responseID string, state StateResponse) MessageResponse {
	return MessageResponse{
		WorkspaceID:          workspaceID,
		AssistantMessage:     assistantMessage,
		ConversationState:    pipelineConversationState(state.Pipeline, state.OnboardingSummary),
		MemoryUpdates:        MemoryUpdates{},
		Snapshot:             state.Snapshot,
		Documents:            state.Documents,
		Agenda:               state.Agenda,
		Claims:               state.Claims,
		CommunicationProfile: state.CommunicationProfile,
		DialogueFocus:        state.DialogueFocus,
		OpenAIResponseID:     responseID,
		Pipeline:             state.Pipeline,
		OnboardingSummary:    state.OnboardingSummary,
	}
}

func knowledgeInputLocked(status string) bool {
	switch status {
	case KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting, KnowledgePipelineReviewing,
		KnowledgePipelineCompiling, KnowledgePipelineReady:
		return true
	default:
		return false
	}
}

type generatedOnboardingSummary struct {
	Markdown       string
	ConversationID string
}

func (s *Service) generateOnboardingSummary(
	ctx context.Context,
	workspaceID int,
	userID int,
	conversationID string,
	vectorStoreIDs []string,
	session OpenAISession,
) (generatedOnboardingSummary, error) {
	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "business_onboarding_summary", "business_onboarding_summary_v1")
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, onboardingSummaryPrompt, "Create the final factual company summary now. Return JSON only and do not include questions.", ai.ResponseContextOptions{
		UseConversation:      true,
		ConversationID:       conversationID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       session.PromptCacheKey,
		MaxFileSearchResults: 4,
		MaxOutputTokens:      3500,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_onboarding_summary", s.ai.ModelName(), "business_onboarding_summary_v1", duration, 0, 0, "failed", err.Error())
		return generatedOnboardingSummary{}, err
	}
	var output onboardingSummaryOutput
	if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_onboarding_summary", s.ai.ModelName(), "business_onboarding_summary_v1", duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return generatedOnboardingSummary{}, fmt.Errorf("onboarding summary decode failed: %w", err)
	}
	markdown := strings.TrimSpace(output.SummaryMarkdown)
	if markdown == "" {
		return generatedOnboardingSummary{}, fmt.Errorf("onboarding summary is empty")
	}
	if !strings.HasPrefix(markdown, "# ") {
		markdown = "# О компании\n\n" + markdown
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_onboarding_summary", s.ai.ModelName(), "business_onboarding_summary_v1", duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return generatedOnboardingSummary{Markdown: markdown, ConversationID: result.ConversationID}, nil
}

func parseAuditorTurn(raw string) (auditorTurnOutput, error) {
	var turn auditorTurnOutput
	if err := json.Unmarshal([]byte(raw), &turn); err != nil {
		return auditorTurnOutput{}, fmt.Errorf("business auditor response decode failed: %w", err)
	}
	turn.Reply = strings.TrimSpace(turn.Reply)
	for range 3 {
		nested, ok := embeddedAuditorTurn(turn.Reply)
		if !ok {
			break
		}
		turn.Reply = strings.TrimSpace(nested.Reply)
		turn.ContextReady = nested.ContextReady
		turn.ReadinessReason = strings.TrimSpace(nested.ReadinessReason)
		turn.LegacyAuditCandidate = nested.LegacyAuditCandidate
		turn.LegacyCandidateReason = strings.TrimSpace(nested.LegacyCandidateReason)
	}
	if turn.Reply == "" {
		return auditorTurnOutput{}, fmt.Errorf("business auditor response has empty reply")
	}
	if ai.LooksLikeJSONObject(turn.Reply) {
		return auditorTurnOutput{}, fmt.Errorf("business auditor serialized a structured payload into reply")
	}
	return turn, nil
}

func (turn auditorTurnOutput) contextReadinessDecision() (bool, string) {
	if turn.ContextReady {
		return true, strings.TrimSpace(turn.ReadinessReason)
	}
	if turn.LegacyAuditCandidate {
		return true, strings.TrimSpace(turn.LegacyCandidateReason)
	}
	return false, ""
}

func hasStructuredKnowledgeContext(state StateResponse) bool {
	return state.Snapshot != nil || len(state.Claims) > 0 || len(state.Documents) > 0 || state.QualityReport != nil
}

func (s *Service) UploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (FileUploadResponse, error) {
	return s.uploadFile(ctx, workspaceID, userID, filename, contentType, sizeBytes, file, true)
}

// UploadReferenceFile stores a workspace file without treating it as a new
// knowledge-base interview turn. Callers decide how the file is linked.
func (s *Service) UploadReferenceFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (FileUploadResponse, error) {
	return s.uploadFile(ctx, workspaceID, userID, filename, contentType, sizeBytes, file, false)
}

func (s *Service) uploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader, recordKnowledgeTurn bool) (FileUploadResponse, error) {
	if recordKnowledgeTurn {
		pipeline, err := s.store.KnowledgePipelineState(ctx, workspaceID)
		if err != nil {
			return FileUploadResponse{}, err
		}
		summary, err := s.store.OnboardingSummary(ctx, workspaceID)
		if err != nil {
			return FileUploadResponse{}, err
		}
		if knowledgeInputLocked(pipeline.Status) || summary != nil {
			return FileUploadResponse{}, fmt.Errorf("knowledge_context_locked")
		}
	}
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
			s.deleteVectorStoreBestEffort(ctx, vectorStoreID)
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
		s.deleteFileBestEffort(ctx, uploadedFile.ID)
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
		s.deleteFileBestEffort(ctx, uploadedFile.ID)
		return FileUploadResponse{}, saveErr
	}
	if errorText != "" {
		return FileUploadResponse{WorkspaceID: workspaceID, File: item}, fmt.Errorf("%s", errorText)
	}
	if recordKnowledgeTurn {
		if _, err := s.store.RecordKnowledgeUserTurn(ctx, workspaceID, rawSourceID); err != nil {
			return FileUploadResponse{}, err
		}
	}
	return FileUploadResponse{WorkspaceID: workspaceID, File: item}, nil
}

func (s *Service) fallbackMessageResponse(ctx context.Context, workspaceID int, state StateResponse, assistantMessage string) MessageResponse {
	assistantMessage = cleanAssistantMessage(fallbackAssistantReply(assistantMessage))
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, _ = s.store.CreateRawSource(persistCtx, workspaceID, nil, SourceTypeAssistantMessage, assistantMessage, map[string]any{
		"prompt_version": StrategicMemoryPromptVersion,
		"fallback":       true,
	})

	return MessageResponse{
		WorkspaceID:          workspaceID,
		AssistantMessage:     assistantMessage,
		ConversationState:    pipelineConversationState(state.Pipeline, state.OnboardingSummary),
		MemoryUpdates:        MemoryUpdates{},
		Snapshot:             state.Snapshot,
		Documents:            state.Documents,
		Agenda:               state.Agenda,
		Claims:               state.Claims,
		CommunicationProfile: state.CommunicationProfile,
		DialogueFocus:        state.DialogueFocus,
		Pipeline:             state.Pipeline,
		OnboardingSummary:    state.OnboardingSummary,
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
	if err := s.CleanupExternalResources(ctx, workspaceID); err != nil {
		return err
	}
	s.qualityAuditMu.Lock()
	delete(s.qualityAuditReservedAt, workspaceID)
	s.qualityAuditMu.Unlock()
	return s.store.Reset(ctx, workspaceID)
}

func (s *Service) CleanupUserData(ctx context.Context, userID int) error {
	workspaceIDs, err := s.store.ListOwnedWorkspaceIDs(ctx, userID)
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaceIDs {
		if err := s.CleanupExternalResources(ctx, workspaceID); err != nil {
			return fmt.Errorf("workspace %d external cleanup: %w", workspaceID, err)
		}
	}
	return nil
}

func (s *Service) CleanupExternalResources(ctx context.Context, workspaceID int) error {
	files, err := s.store.ListFiles(ctx, workspaceID)
	if err != nil {
		return err
	}
	vectorStoreID, err := s.store.ExistingOpenAIVectorStoreID(ctx, workspaceID)
	if err != nil {
		return err
	}
	contextFileIDs, conversationIDs, err := s.store.ListWorkspaceExternalContextIDs(ctx, workspaceID)
	if err != nil {
		return err
	}
	fileIDs := make(map[string]struct{})
	vectorStoreIDs := make(map[string]struct{})
	for _, item := range files {
		if id := strings.TrimSpace(item.OpenAIFileID); id != "" {
			fileIDs[id] = struct{}{}
		}
		if id := strings.TrimSpace(item.VectorStoreID); id != "" {
			vectorStoreIDs[id] = struct{}{}
		}
	}
	for _, id := range contextFileIDs {
		if id = strings.TrimSpace(id); id != "" {
			fileIDs[id] = struct{}{}
		}
	}
	if id := strings.TrimSpace(vectorStoreID); id != "" {
		vectorStoreIDs[id] = struct{}{}
	}
	if len(fileIDs) == 0 && len(vectorStoreIDs) == 0 && len(conversationIDs) == 0 {
		return nil
	}
	cleaner, ok := s.ai.(ai.ResourceCleaner)
	if !ok {
		return fmt.Errorf("ai provider does not support external resource cleanup")
	}
	for id := range vectorStoreIDs {
		if err := cleaner.DeleteVectorStore(ctx, id); err != nil {
			return fmt.Errorf("delete vector store %s: %w", id, err)
		}
	}
	for id := range fileIDs {
		if err := cleaner.DeleteFile(ctx, id); err != nil {
			return fmt.Errorf("delete file %s: %w", id, err)
		}
	}
	if len(conversationIDs) > 0 {
		conversationCleaner, ok := s.ai.(ai.ConversationCleaner)
		if !ok {
			return fmt.Errorf("ai provider does not support conversation cleanup")
		}
		for _, id := range conversationIDs {
			if err := conversationCleaner.DeleteConversation(ctx, id); err != nil {
				return fmt.Errorf("delete conversation %s: %w", id, err)
			}
		}
	}
	return nil
}

func (s *Service) deleteFileBestEffort(ctx context.Context, fileID string) {
	if cleaner, ok := s.ai.(ai.ResourceCleaner); ok {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = cleaner.DeleteFile(cleanupCtx, fileID)
	}
}

func (s *Service) deleteVectorStoreBestEffort(ctx context.Context, vectorStoreID string) {
	if cleaner, ok := s.ai.(ai.ResourceCleaner); ok {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = cleaner.DeleteVectorStore(cleanupCtx, vectorStoreID)
	}
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
