package tactics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/strategicmemory"
)

type FacilitatorService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	memoryService    *strategicmemory.Service
	ai               ai.Provider
	compactThreshold int
	readiness        *TacticsReadinessService
	contextIndex     *contextindex.Service
}

func (s *FacilitatorService) SetContextIndex(index *contextindex.Service) {
	s.contextIndex = index
	s.memoryService.SetContextIndex(index)
}

func (s *FacilitatorService) SetReadinessService(readiness *TacticsReadinessService) {
	s.readiness = readiness
}

func NewFacilitatorService(dbx *sql.DB, aiClient ai.Provider, compactThreshold int, managers ...*jobs.Manager) *FacilitatorService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	memoryStore := strategicmemory.NewStore(dbx)
	return &FacilitatorService{
		store:            NewStore(dbx),
		memoryStore:      memoryStore,
		memoryService:    strategicmemory.NewService(memoryStore, aiClient, compactThreshold, managers...),
		ai:               aiClient,
		compactThreshold: compactThreshold,
	}
}

func (s *FacilitatorService) State(ctx context.Context, workspaceID int, userID int) (TacticsFacilitatorState, error) {
	current, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	strategyDocs := []TacticsStrategyDocument{}
	if current.Strategy != nil {
		strategyDocs, err = s.store.StrategyDocuments(ctx, workspaceID, current.Strategy.ID)
		if err != nil {
			return TacticsFacilitatorState{}, err
		}
	}
	knowledgeDocs, err := s.memoryStore.ListDocuments(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	quality, err := s.memoryStore.LatestQualityReport(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	files, err := s.memoryStore.ListFiles(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	communication, err := s.memoryStore.CommunicationProfile(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	messages, err := s.store.ChatMessages(ctx, workspaceID, 100)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	session, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorState{}, err
	}
	var readiness *TacticsReadinessRun
	if s.readiness != nil {
		latest, readinessErr := s.readiness.Latest(ctx, workspaceID)
		if readinessErr != nil {
			return TacticsFacilitatorState{}, readinessErr
		}
		readiness = latest.Run
	}
	return TacticsFacilitatorState{
		WorkspaceID:    workspaceID,
		Current:        current,
		StrategyDocs:   strategyDocs,
		KnowledgeDocs:  knowledgeDocs,
		KnowledgeAudit: quality,
		Files:          files,
		Communication:  communication,
		RecentMessages: messages,
		Session:        session,
		Readiness:      readiness,
	}, nil
}

func (s *FacilitatorService) History(ctx context.Context, workspaceID int, scopes ...*TacticsMessageScope) (TacticsFacilitatorHistoryState, error) {
	var scope *TacticsMessageScope
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	messages, err := s.store.ScopedChatMessages(ctx, workspaceID, scope, 300)
	if err != nil {
		return TacticsFacilitatorHistoryState{}, err
	}
	session, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return TacticsFacilitatorHistoryState{}, err
	}
	var readiness *TacticsReadinessRun
	if s.readiness != nil {
		latest, readinessErr := s.readiness.Latest(ctx, workspaceID)
		if readinessErr != nil {
			return TacticsFacilitatorHistoryState{}, readinessErr
		}
		readiness = latest.Run
	}
	return TacticsFacilitatorHistoryState{
		WorkspaceID:    workspaceID,
		RecentMessages: messages,
		Session:        session,
		Readiness:      readiness,
	}, nil
}

func (s *FacilitatorService) HistoryThread(ctx context.Context, workspaceID int, userID int, threadID int) (TacticsFacilitatorHistoryState, error) {
	thread, err := s.store.AdvisorThread(ctx, workspaceID, userID, threadID)
	if err != nil {
		return TacticsFacilitatorHistoryState{}, err
	}
	return s.History(ctx, workspaceID, thread.ConversationScope())
}

func (s *FacilitatorService) HandleMessage(ctx context.Context, workspaceID int, userID int, request TacticsFacilitatorMessageRequest) (TacticsFacilitatorMessageResponse, error) {
	message := strings.TrimSpace(request.Message)
	if len([]rune(message)) < 2 {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("message_too_long")
	}

	personalThread := request.ThreadID > 0
	contextScope := request.Scope
	conversationScope := request.Scope
	if personalThread {
		thread, err := s.store.AdvisorThread(ctx, workspaceID, userID, request.ThreadID)
		if err != nil {
			return TacticsFacilitatorMessageResponse{}, err
		}
		contextScope = thread.Scope()
		conversationScope = thread.ConversationScope()
		request.Scope = contextScope
	}

	state, err := s.State(ctx, workspaceID, userID)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	if !personalThread && state.Current.Strategy == nil {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("tactics_strategy_required")
	}
	if !personalThread && state.Current.Course == nil {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("tactics_course_required")
	}
	state.RecentMessages, err = s.store.ScopedChatMessages(ctx, workspaceID, conversationScope, 100)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}

	scopeContext, err := s.store.ScopeContext(ctx, workspaceID, contextScope)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	userMessageID, err := s.store.CreateScopedChatMessage(ctx, workspaceID, &userID, "user", message, map[string]any{
		"participant_role":  normalizeParticipantRole(request.ParticipantRole),
		"context_scope":     contextScope,
		"advisor_thread_id": request.ThreadID,
	}, conversationScope)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	if personalThread {
		if err := s.store.TouchAdvisorThread(ctx, workspaceID, userID, request.ThreadID, message); err != nil {
			return TacticsFacilitatorMessageResponse{}, err
		}
	}
	if err := s.memoryService.CaptureTacticsFacts(
		ctx, workspaceID, userID, userMessageID, message, contextScope,
	); err != nil {
		log.Printf("[WARN] capture tactics context workspace_id=%d message_id=%d: %v", workspaceID, userMessageID, err)
	}
	sessionState := state.Session
	if !personalThread {
		sessionState, err = s.store.BeginFacilitatorTurn(ctx, workspaceID, userID, userMessageID)
		if err != nil {
			return TacticsFacilitatorMessageResponse{}, err
		}
		state.Session = sessionState
	}

	fingerprint := tacticsContextFingerprint(state)
	openAISession, err := s.store.OpenAITacticsScopeSession(ctx, workspaceID, conversationScope, s.compactThreshold, fingerprint)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	vectorStoreIDs := s.vectorStoreIDs(ctx, workspaceID)
	if s.contextIndex != nil {
		indexedIDs, indexErr := s.contextIndex.Available(ctx, workspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
		} else if indexErr != nil {
			s.logAIRun(ctx, workspaceID, 0, 0, 0, "failed", "workspace context sync: "+indexErr.Error())
		}
	}
	input := buildTacticsTurnInput(message, request)
	if strings.TrimSpace(openAISession.ConversationID) == "" {
		input = buildTacticsFreshInput(message, request, scopeContext, state)
	}

	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "tactics_advisor_openai_native", TacticsFacilitatorPromptVersion)
	started := time.Now()
	prompt := tacticsFacilitatorPrompt + contextindex.RetrievalInstructions
	result, err := s.ai.GenerateJSONNative(aiCtx, prompt, input, ai.ResponseContextOptions{
		UseConversation:      true,
		ConversationID:       openAISession.ConversationID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     openAISession.CompactThreshold,
		PromptCacheKey:       openAISession.PromptCacheKey,
		MaxFileSearchResults: 8,
		MaxOutputTokens:      2600,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil && strings.TrimSpace(openAISession.ConversationID) != "" && ai.IsConversationStateError(err) {
		_ = s.store.UpdateOpenAITacticsScopeConversationID(ctx, workspaceID, conversationScope, "")
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(aiCtx, prompt, buildTacticsFreshInput(message, request, scopeContext, state), ai.ResponseContextOptions{
			UseConversation:      true,
			VectorStoreIDs:       vectorStoreIDs,
			CompactThreshold:     openAISession.CompactThreshold,
			PromptCacheKey:       openAISession.PromptCacheKey,
			MaxFileSearchResults: 8,
			MaxOutputTokens:      2600,
		})
		duration = time.Since(started).Milliseconds()
	}
	if err != nil {
		s.logAIRun(ctx, workspaceID, duration, 0, 0, "failed", err.Error())
		return s.fallbackResponse(ctx, workspaceID, userMessageID, state, conversationScope, personalThread), nil
	}
	if strings.TrimSpace(result.ConversationID) != "" && result.ConversationID != openAISession.ConversationID {
		_ = s.store.UpdateOpenAITacticsScopeConversationID(ctx, workspaceID, conversationScope, result.ConversationID)
	}

	modelOutput, parseErr := parseTacticsFacilitatorOutput(result.Text)
	if parseErr != nil {
		s.logAIRun(ctx, workspaceID, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", parseErr.Error())
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(aiCtx, prompt, "Repair your previous response. Return valid JSON matching the required output contract. The message field must contain natural user-facing prose, never JSON or a serialized object. Preserve the intended meaning and do not ask the user to repeat anything.", ai.ResponseContextOptions{
			UseConversation:      true,
			ConversationID:       result.ConversationID,
			VectorStoreIDs:       vectorStoreIDs,
			CompactThreshold:     openAISession.CompactThreshold,
			PromptCacheKey:       openAISession.PromptCacheKey,
			MaxFileSearchResults: 8,
			MaxOutputTokens:      2600,
		})
		duration = time.Since(started).Milliseconds()
		if err == nil {
			modelOutput, parseErr = parseTacticsFacilitatorOutput(result.Text)
		}
		if err != nil || parseErr != nil {
			errorText := "tactics facilitator retry failed"
			if err != nil {
				errorText = err.Error()
			} else if parseErr != nil {
				errorText = parseErr.Error()
			}
			s.logAIRun(ctx, workspaceID, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", errorText)
			return s.fallbackResponse(ctx, workspaceID, userMessageID, state, conversationScope, personalThread), nil
		}
	}
	s.logAIRun(ctx, workspaceID, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	assistantMessage := cleanTacticsAssistantMessage(modelOutput.Message)
	assistantMessageID, err := s.store.CreateScopedChatMessage(ctx, workspaceID, nil, "assistant", assistantMessage, map[string]any{
		"prompt_version":        TacticsFacilitatorPromptVersion,
		"user_source_id":        userMessageID,
		"response_id":           result.ResponseID,
		"conversation_id":       result.ConversationID,
		"vector_store_ids":      vectorStoreIDs,
		"session_status":        modelOutput.SessionStatus,
		"current_focus":         modelOutput.CurrentFocus,
		"decisions_detected":    modelOutput.DecisionsDetected,
		"open_questions":        modelOutput.OpenQuestions,
		"needs_strategy_review": modelOutput.NeedsStrategyReview,
		"draft_changes":         modelOutput.DraftChanges,
		"context_scope":         contextScope,
		"advisor_thread_id":     request.ThreadID,
	}, conversationScope)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	if state.Current.TacticalPlan != nil && len(modelOutput.DraftChanges) > 0 {
		if err := s.store.RegisterTacticsActions(
			ctx,
			workspaceID,
			state.Current.TacticalPlan.ID,
			assistantMessageID,
			modelOutput.DraftChanges,
		); err != nil {
			return TacticsFacilitatorMessageResponse{}, err
		}
	}

	if !personalThread {
		sessionState, err = s.store.RecordFacilitatorAssessment(ctx, workspaceID, userMessageID, modelOutput)
		if err != nil {
			return TacticsFacilitatorMessageResponse{}, err
		}
		if s.readiness != nil && sessionState.FacilitatorStatus == FacilitatorStatusCandidateReady && state.Current.TacticalPlan != nil {
			plan, planErr := s.store.planByID(ctx, workspaceID, state.Current.TacticalPlan.ID)
			if planErr == nil {
				_, _ = s.readiness.QueueCandidate(ctx, sessionState, plan, false)
			}
		}
	}
	messages, err := s.store.ScopedChatMessages(ctx, workspaceID, conversationScope, 100)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	return TacticsFacilitatorMessageResponse{
		WorkspaceID:       workspaceID,
		AssistantMessage:  assistantMessage,
		RecentMessages:    messages,
		OpenAIResponseID:  result.ResponseID,
		Session:           sessionState,
		ProposalMessageID: assistantMessageID,
		ProposedChanges:   modelOutput.DraftChanges,
		AppliedChanges:    []AppliedTacticsChange{},
	}, nil
}

func (s *FacilitatorService) UploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (strategicmemory.FileUploadResponse, error) {
	response, err := s.memoryService.UploadFile(ctx, workspaceID, userID, filename, contentType, sizeBytes, file)
	if err != nil {
		return response, err
	}
	_ = s.store.ResetOpenAITacticsSession(ctx, workspaceID)
	return response, nil
}

func buildTacticsFreshInput(message string, request TacticsFacilitatorMessageRequest, scopeContext any, state TacticsFacilitatorState) string {
	contextMode := "business_context_only"
	if state.Current.Strategy != nil {
		contextMode = "strategy_available"
		if state.Current.Strategy.Status == "active" {
			contextMode = "active_strategy"
		}
	}
	contextPack := map[string]any{
		"latest_user_message": message,
		"participant_role":    normalizeParticipantRole(request.ParticipantRole),
		"context_mode":        contextMode,
		"active_scope": map[string]any{
			"request": request.Scope,
			"entity":  scopeContext,
		},
		"active_course": state.Current.Course,
		"strategy": map[string]any{
			"record":         state.Current.Strategy,
			"document_index": compactTacticsStrategyDocumentIndex(state.StrategyDocs),
		},
		"knowledge_base": map[string]any{
			"document_index":        compactTacticsKnowledgeDocumentIndex(state.KnowledgeDocs),
			"latest_quality_report": compactTacticsKnowledgeQuality(state.KnowledgeAudit),
			"uploaded_files":        compactTacticsFiles(state.Files),
		},
		"current_tactical_plan": map[string]any{
			"plan":          state.Current.TacticalPlan,
			"change_index":  compactTacticsWorkstreamIndex(state.Current.Workstreams),
			"uncovered":     state.Current.Uncovered,
			"session_state": state.Session,
		},
		"communication_profile":   state.Communication,
		"recent_dialogue":         state.RecentMessages,
		"latest_quality_feedback": compactTacticsReadinessFeedback(state.Readiness),
		"instruction":             "Continue as the company's business development advisor. Use an approved strategy as the primary decision frame when it exists; otherwise advise from the available business context and state uncertainty honestly.",
	}
	raw, _ := json.Marshal(contextPack)
	return "Context for the tactical session in JSON:\n" + string(raw)
}

func compactTacticsStrategyDocumentIndex(documents []TacticsStrategyDocument) []map[string]any {
	result := make([]map[string]any, 0, len(documents))
	for _, document := range documents {
		result = append(result, map[string]any{
			"type":   document.DocumentType,
			"title":  document.Title,
			"status": document.Status,
		})
	}
	return result
}

func compactTacticsKnowledgeDocumentIndex(documents []strategicmemory.StrategicDocument) []map[string]any {
	result := make([]map[string]any, 0, len(documents))
	for _, document := range documents {
		result = append(result, map[string]any{
			"id":      document.ID,
			"type":    document.DocumentType,
			"title":   document.Title,
			"status":  document.Status,
			"version": document.Version,
		})
	}
	return result
}

func compactTacticsWorkstreamIndex(workstreams []Workstream) []map[string]any {
	result := make([]map[string]any, 0, len(workstreams))
	for _, workstream := range workstreams {
		projects := make([]map[string]any, 0, len(workstream.Projects))
		for _, project := range workstream.Projects {
			projects = append(projects, map[string]any{
				"id":     project.ID,
				"title":  project.Title,
				"status": project.Status,
			})
		}
		result = append(result, map[string]any{
			"id":       workstream.ID,
			"title":    workstream.Title,
			"status":   workstream.Status,
			"projects": projects,
		})
	}
	return result
}

func buildTacticsTurnInput(message string, request TacticsFacilitatorMessageRequest) string {
	turn := map[string]any{
		"latest_user_message": message,
		"participant_role":    normalizeParticipantRole(request.ParticipantRole),
		"active_scope":        request.Scope,
	}
	raw, _ := json.Marshal(turn)
	return string(raw)
}

func compactTacticsReadinessFeedback(run *TacticsReadinessRun) any {
	if run == nil || run.Report == nil {
		return nil
	}
	return map[string]any{
		"audited_session_revision":       run.SessionRevision,
		"audited_tactical_plan_revision": run.TacticalPlanRevision,
		"verdict":                        run.Report.Verdict,
		"can_activate":                   run.Report.CanActivate,
		"overall_score":                  run.Report.OverallScore,
		"executive_summary":              run.Report.ExecutiveSummary,
		"blocking_gaps":                  run.Report.BlockingGaps,
		"weak_zones":                     run.Report.WeakZones,
		"contradictions":                 run.Report.Contradictions,
		"additional_perspectives":        run.Report.AdditionalPerspectives,
		"facilitator_guidance":           run.Report.FacilitatorGuidance,
		"needs_strategy_review":          run.Report.NeedsStrategyReview,
		"strategy_review_reason":         run.Report.StrategyReviewReason,
	}
}

func parseTacticsFacilitatorOutput(raw string) (tacticsFacilitatorModelOutput, error) {
	var output tacticsFacilitatorModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return tacticsFacilitatorModelOutput{}, err
	}
	output.Message = cleanTacticsAssistantMessage(output.Message)
	if output.Message == "" {
		return tacticsFacilitatorModelOutput{}, fmt.Errorf("tactics facilitator returned empty message")
	}
	if ai.LooksLikeJSONObject(output.Message) {
		return tacticsFacilitatorModelOutput{}, fmt.Errorf("tactics facilitator serialized a structured payload into message")
	}
	if !utf8.ValidString(output.Message) || strings.ContainsRune(output.Message, '\uFFFD') {
		return tacticsFacilitatorModelOutput{}, fmt.Errorf("tactics facilitator returned invalid UTF-8")
	}
	output.SessionStatus = normalizeTacticsStatus(output.SessionStatus)
	output.StatusReason = strings.TrimSpace(output.StatusReason)
	output.CurrentFocus.EntityType = strings.TrimSpace(output.CurrentFocus.EntityType)
	output.CurrentFocus.Title = strings.TrimSpace(output.CurrentFocus.Title)
	output.CurrentFocus.ResearchGoal = strings.TrimSpace(output.CurrentFocus.ResearchGoal)
	output.DecisionsDetected = cleanTacticsStrings(output.DecisionsDetected, 12)
	output.OpenQuestions = cleanTacticsStrings(output.OpenQuestions, 20)
	output.StrategyReviewReason = strings.TrimSpace(output.StrategyReviewReason)
	output.DraftChanges = normalizeTacticsDraftChanges(output.DraftChanges)
	return output, nil
}

func tacticsContextFingerprint(state TacticsFacilitatorState) string {
	source := map[string]any{
		"strategy":            state.Current.Strategy,
		"course":              state.Current.Course,
		"plan":                state.Current.TacticalPlan,
		"changes":             state.Current.Workstreams,
		"uncovered":           state.Current.Uncovered,
		"strategy_documents":  state.StrategyDocs,
		"knowledge_documents": state.KnowledgeDocs,
		"knowledge_quality_id": func() int {
			if state.KnowledgeAudit == nil {
				return 0
			}
			return state.KnowledgeAudit.ID
		}(),
		"files": compactTacticsFiles(state.Files),
	}
	raw, _ := json.Marshal(source)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func compactTacticsKnowledgeQuality(report *strategicmemory.QualityReport) any {
	if report == nil {
		return nil
	}
	return map[string]any{
		"readiness_score":                    report.ReadinessScore,
		"readiness_status":                   report.ReadinessStatus,
		"summary":                            report.Overall.Summary,
		"critical_blockers":                  report.Overall.CriticalBlockers,
		"most_important_missing_information": report.Overall.MostImportantMissingInfo,
		"major_inconsistencies":              report.Overall.MajorInconsistencies,
		"blind_spots":                        report.ChatGuidance.BlindSpots,
	}
}

func compactTacticsFiles(files []strategicmemory.StrategicFile) []map[string]any {
	result := make([]map[string]any, 0, len(files))
	for _, file := range files {
		result = append(result, map[string]any{
			"id":         file.ID,
			"filename":   file.Filename,
			"status":     file.Status,
			"size_bytes": file.SizeBytes,
			"updated_at": file.UpdatedAt,
		})
	}
	return result
}

func normalizeParticipantRole(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "functional_leader", "direction_leader":
		return "functional_leader"
	default:
		return "executive"
	}
}

func cleanTacticsAssistantMessage(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "【】", "")
	value = strings.ReplaceAll(value, "【 】", "")
	return strings.TrimSpace(value)
}

func (s *FacilitatorService) vectorStoreIDs(ctx context.Context, workspaceID int) []string {
	session, err := s.memoryStore.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil || strings.TrimSpace(session.VectorStoreID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(session.VectorStoreID)}
}

func (s *FacilitatorService) logAIRun(ctx context.Context, workspaceID int, duration int64, inputTokens int, outputTokens int, status string, errorText string) {
	s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "tactics_advisor_openai_native", s.ai.ModelName(), TacticsFacilitatorPromptVersion, duration, inputTokens, outputTokens, status, errorText)
}

func (s *FacilitatorService) fallbackResponse(ctx context.Context, workspaceID int, userMessageID int, state TacticsFacilitatorState, scope *TacticsMessageScope, personalThread bool) TacticsFacilitatorMessageResponse {
	message := "Не получилось обработать ответ с первого раза. Продолжим с последней точки: какое решение или гипотезу вы хотите сейчас проверить?"
	output := tacticsFacilitatorModelOutput{
		Message:       message,
		SessionStatus: FacilitatorStatusInProgress,
		StatusReason:  "The AI response failed, so the tactical session remains in progress.",
		OpenQuestions: state.Session.OpenQuestions,
	}
	_, _ = s.store.CreateScopedChatMessage(ctx, workspaceID, nil, "assistant", message, map[string]any{
		"prompt_version": TacticsFacilitatorPromptVersion,
		"fallback":       true,
		"user_source_id": userMessageID,
	}, scope)
	session := state.Session
	if !personalThread {
		session, _ = s.store.RecordFacilitatorAssessment(ctx, workspaceID, userMessageID, output)
	}
	messages, _ := s.store.ScopedChatMessages(ctx, workspaceID, scope, 100)
	return TacticsFacilitatorMessageResponse{
		WorkspaceID:      workspaceID,
		AssistantMessage: message,
		RecentMessages:   messages,
		Session:          session,
		AppliedChanges:   []AppliedTacticsChange{},
	}
}
