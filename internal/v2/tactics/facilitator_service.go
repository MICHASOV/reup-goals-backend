package tactics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

type FacilitatorService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	memoryService    *strategicmemory.Service
	ai               *ai.OpenAIClient
	compactThreshold int
	readiness        *TacticsReadinessService
}

func (s *FacilitatorService) SetReadinessService(readiness *TacticsReadinessService) {
	s.readiness = readiness
}

func NewFacilitatorService(dbx *sql.DB, aiClient *ai.OpenAIClient, compactThreshold int) *FacilitatorService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	memoryStore := strategicmemory.NewStore(dbx)
	return &FacilitatorService{
		store:            NewStore(dbx),
		memoryStore:      memoryStore,
		memoryService:    strategicmemory.NewService(memoryStore, aiClient, compactThreshold),
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

func (s *FacilitatorService) History(ctx context.Context, workspaceID int) (TacticsFacilitatorHistoryState, error) {
	messages, err := s.store.ChatMessages(ctx, workspaceID, 300)
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

func (s *FacilitatorService) HandleMessage(ctx context.Context, workspaceID int, userID int, request TacticsFacilitatorMessageRequest) (TacticsFacilitatorMessageResponse, error) {
	message := strings.TrimSpace(request.Message)
	if len([]rune(message)) < 2 {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("message_too_long")
	}

	state, err := s.State(ctx, workspaceID, userID)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	if state.Current.Strategy == nil {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("tactics_strategy_required")
	}
	if state.Current.Course == nil {
		return TacticsFacilitatorMessageResponse{}, fmt.Errorf("tactics_course_required")
	}

	scopeContext, err := s.store.ScopeContext(ctx, workspaceID, request.Scope)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	userMessageID, err := s.store.CreateChatMessage(ctx, workspaceID, &userID, "user", message, map[string]any{
		"participant_role": normalizeParticipantRole(request.ParticipantRole),
		"scope":            request.Scope,
	})
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	sessionState, err := s.store.BeginFacilitatorTurn(ctx, workspaceID, userID, userMessageID)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	state.Session = sessionState

	fingerprint := tacticsContextFingerprint(state)
	openAISession, err := s.store.OpenAITacticsSession(ctx, workspaceID, s.compactThreshold, fingerprint)
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}
	vectorStoreIDs := s.vectorStoreIDs(ctx, workspaceID)
	usedPreviousResponseID := openAISession.PreviousResponseID
	input := buildTacticsTurnInput(message, request, scopeContext, state)
	if strings.TrimSpace(openAISession.PreviousResponseID) == "" {
		input = buildTacticsFreshInput(message, request, scopeContext, state)
	}

	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, tacticsFacilitatorPrompt, input, ai.ResponseContextOptions{
		PreviousResponseID:   openAISession.PreviousResponseID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     openAISession.CompactThreshold,
		PromptCacheKey:       openAISession.PromptCacheKey,
		MaxFileSearchResults: 8,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil && strings.TrimSpace(openAISession.PreviousResponseID) != "" {
		_ = s.store.UpdateOpenAITacticsPreviousResponseID(ctx, workspaceID, "")
		usedPreviousResponseID = ""
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(ctx, tacticsFacilitatorPrompt, buildTacticsFreshInput(message, request, scopeContext, state), ai.ResponseContextOptions{
			VectorStoreIDs:       vectorStoreIDs,
			CompactThreshold:     openAISession.CompactThreshold,
			PromptCacheKey:       openAISession.PromptCacheKey,
			MaxFileSearchResults: 8,
		})
		duration = time.Since(started).Milliseconds()
	}
	if err != nil {
		s.logAIRun(ctx, workspaceID, duration, 0, 0, "failed", err.Error())
		return s.fallbackResponse(ctx, workspaceID, userMessageID, state), nil
	}

	modelOutput, parseErr := parseTacticsFacilitatorOutput(result.Text)
	if parseErr != nil {
		_ = s.store.UpdateOpenAITacticsPreviousResponseID(ctx, workspaceID, "")
		s.logAIRun(ctx, workspaceID, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", parseErr.Error())
		return s.fallbackResponse(ctx, workspaceID, userMessageID, state), nil
	}
	s.logAIRun(ctx, workspaceID, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	if strings.TrimSpace(result.ResponseID) != "" {
		_ = s.store.UpdateOpenAITacticsPreviousResponseID(ctx, workspaceID, result.ResponseID)
	}

	assistantMessage := cleanTacticsAssistantMessage(modelOutput.Message)
	assistantMessageID, err := s.store.CreateChatMessage(ctx, workspaceID, nil, "assistant", assistantMessage, map[string]any{
		"prompt_version":        TacticsFacilitatorPromptVersion,
		"user_source_id":        userMessageID,
		"response_id":           result.ResponseID,
		"previous_response_id":  usedPreviousResponseID,
		"vector_store_ids":      vectorStoreIDs,
		"session_status":        modelOutput.SessionStatus,
		"current_focus":         modelOutput.CurrentFocus,
		"decisions_detected":    modelOutput.DecisionsDetected,
		"open_questions":        modelOutput.OpenQuestions,
		"needs_strategy_review": modelOutput.NeedsStrategyReview,
		"draft_changes":         modelOutput.DraftChanges,
	})
	if err != nil {
		return TacticsFacilitatorMessageResponse{}, err
	}

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
	messages, err := s.store.ChatMessages(ctx, workspaceID, 100)
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
	contextPack := map[string]any{
		"latest_user_message": message,
		"participant_role":    normalizeParticipantRole(request.ParticipantRole),
		"active_scope": map[string]any{
			"request": request.Scope,
			"entity":  scopeContext,
		},
		"active_course": state.Current.Course,
		"strategy": map[string]any{
			"record":    state.Current.Strategy,
			"documents": state.StrategyDocs,
		},
		"knowledge_base": map[string]any{
			"documents":             state.KnowledgeDocs,
			"latest_quality_report": compactTacticsKnowledgeQuality(state.KnowledgeAudit),
			"uploaded_files":        compactTacticsFiles(state.Files),
		},
		"current_tactical_plan": map[string]any{
			"plan":          state.Current.TacticalPlan,
			"changes":       state.Current.Workstreams,
			"uncovered":     state.Current.Uncovered,
			"session_state": state.Session,
		},
		"communication_profile":   state.Communication,
		"recent_dialogue":         state.RecentMessages,
		"latest_quality_feedback": compactTacticsReadinessFeedback(state.Readiness),
		"instruction":             "Continue as a tactical consultant. Reply naturally to the latest user message and make the next move that most improves the company's system of changes for realizing its active course.",
	}
	raw, _ := json.Marshal(contextPack)
	return "Context for the tactical session in JSON:\n" + string(raw)
}

func buildTacticsTurnInput(message string, request TacticsFacilitatorMessageRequest, scopeContext any, state TacticsFacilitatorState) string {
	turn := map[string]any{
		"latest_user_message": message,
		"participant_role":    normalizeParticipantRole(request.ParticipantRole),
		"active_scope": map[string]any{
			"request": request.Scope,
			"entity":  scopeContext,
		},
		"session_state":           state.Session,
		"latest_quality_feedback": compactTacticsReadinessFeedback(state.Readiness),
		"instruction":             "Continue the same tactical conversation. Respond naturally, preserve the active course as the governing constraint, and do not expose internal status mechanics.",
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
	s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "tactics_facilitator_openai_native", s.ai.Model, TacticsFacilitatorPromptVersion, duration, inputTokens, outputTokens, status, errorText)
}

func (s *FacilitatorService) fallbackResponse(ctx context.Context, workspaceID int, userMessageID int, state TacticsFacilitatorState) TacticsFacilitatorMessageResponse {
	message := "Не получилось обработать ответ с первого раза. Давайте продолжим с последней точки: какое изменение в бизнесе вы сейчас считаете главным для реализации курса?"
	output := tacticsFacilitatorModelOutput{
		Message:       message,
		SessionStatus: FacilitatorStatusInProgress,
		StatusReason:  "The AI response failed, so the tactical session remains in progress.",
		OpenQuestions: state.Session.OpenQuestions,
	}
	_, _ = s.store.CreateChatMessage(ctx, workspaceID, nil, "assistant", message, map[string]any{
		"prompt_version": TacticsFacilitatorPromptVersion,
		"fallback":       true,
		"user_source_id": userMessageID,
	})
	session, _ := s.store.RecordFacilitatorAssessment(ctx, workspaceID, userMessageID, output)
	messages, _ := s.store.ChatMessages(ctx, workspaceID, 100)
	return TacticsFacilitatorMessageResponse{
		WorkspaceID:      workspaceID,
		AssistantMessage: message,
		RecentMessages:   messages,
		Session:          session,
		AppliedChanges:   []AppliedTacticsChange{},
	}
}
