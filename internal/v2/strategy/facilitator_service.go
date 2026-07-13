package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

type FacilitatorService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	memoryService    *strategicmemory.Service
	ai               *ai.OpenAIClient
	compactThreshold int
	readiness        *ReadinessService
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

func (s *FacilitatorService) SetReadinessService(readiness *ReadinessService) {
	s.readiness = readiness
}

func (s *FacilitatorService) State(ctx context.Context, workspaceID int, userID int) (StrategyFacilitatorState, error) {
	strategy, artifacts, summary, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	documents, err := s.memoryStore.ListDocuments(ctx, workspaceID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	qualityReport, err := s.memoryStore.LatestQualityReport(ctx, workspaceID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	files, err := s.memoryStore.ListFiles(ctx, workspaceID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	messages, err := s.store.RecentChatMessages(ctx, workspaceID, 40)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	sessionState, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}
	readinessRun, err := s.store.LatestCompletedReadinessAudit(ctx, workspaceID)
	if err != nil {
		return StrategyFacilitatorState{}, err
	}

	return StrategyFacilitatorState{
		WorkspaceID: workspaceID,
		Strategy:    strategy,
		Artifacts:   artifacts,
		KnowledgeBase: StrategyKnowledgeContext{
			Summary:       summary,
			Documents:     documents,
			QualityReport: qualityReport,
			Files:         files,
		},
		RecentMessages: messages,
		Session:        sessionState,
		Readiness:      readinessRun,
	}, nil
}

func (s *FacilitatorService) HandleMessage(ctx context.Context, workspaceID int, userID int, message string) (StrategyFacilitatorMessageResponse, error) {
	message = strings.TrimSpace(message)
	if len([]rune(message)) < 2 {
		return StrategyFacilitatorMessageResponse{}, fmt.Errorf("message_too_short")
	}
	if len([]rune(message)) > 50000 {
		return StrategyFacilitatorMessageResponse{}, fmt.Errorf("message_too_long")
	}

	userSourceID, err := s.store.CreateChatMessage(ctx, workspaceID, &userID, "user", message, map[string]any{})
	if err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}
	if _, err := s.store.BeginFacilitatorTurn(ctx, workspaceID, userID, userSourceID); err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}

	state, err := s.State(ctx, workspaceID, userID)
	if err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}
	session, err := s.store.OpenAIStrategySession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}
	vectorStoreIDs := s.strategyVectorStoreIDs(ctx, workspaceID)

	input := buildStrategyFacilitatorTurnInput(message, state)
	if strings.TrimSpace(session.PreviousResponseID) == "" {
		input = buildStrategyFacilitatorFreshInput(workspaceID, message, state)
	}

	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, strategyFacilitatorPrompt, input, ai.ResponseContextOptions{
		PreviousResponseID:   session.PreviousResponseID,
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       session.PromptCacheKey,
		MaxFileSearchResults: 8,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		if strings.TrimSpace(session.PreviousResponseID) != "" {
			_ = s.store.UpdateOpenAIStrategyPreviousResponseID(ctx, workspaceID, "")
			started = time.Now()
			result, err = s.ai.GenerateJSONNative(ctx, strategyFacilitatorPrompt, buildStrategyFacilitatorFreshInput(workspaceID, message, state), ai.ResponseContextOptions{
				VectorStoreIDs:       vectorStoreIDs,
				CompactThreshold:     session.CompactThreshold,
				PromptCacheKey:       session.PromptCacheKey,
				MaxFileSearchResults: 8,
			})
			duration = time.Since(started).Milliseconds()
		}
		if err != nil {
			s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_facilitator_openai_native", s.ai.Model, StrategyFacilitatorPromptVersion, duration, 0, 0, "failed", err.Error())
			return s.fallbackResponse(ctx, workspaceID, userSourceID, state), nil
		}
	}

	modelOutput, parseErr := parseStrategyFacilitatorOutput(result.Text)
	if parseErr != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_facilitator_output_parse", s.ai.Model, StrategyFacilitatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", parseErr.Error())
		_ = s.store.UpdateOpenAIStrategyPreviousResponseID(ctx, workspaceID, "")
		return s.fallbackResponse(ctx, workspaceID, userSourceID, state), nil
	}
	s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_facilitator_openai_native", s.ai.Model, StrategyFacilitatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	if strings.TrimSpace(result.ResponseID) != "" {
		_ = s.store.UpdateOpenAIStrategyPreviousResponseID(ctx, workspaceID, result.ResponseID)
	}
	assistantMessage := cleanAssistantMessage(modelOutput.Message)
	if assistantMessage == "" {
		assistantMessage = strategyFallbackAssistantReply()
	}
	if _, err := s.store.CreateChatMessage(ctx, workspaceID, nil, "assistant", assistantMessage, map[string]any{
		"prompt_version":          StrategyFacilitatorPromptVersion,
		"mode":                    "openai_native",
		"user_source_id":          userSourceID,
		"response_id":             result.ResponseID,
		"previous_response_id":    session.PreviousResponseID,
		"vector_store_ids":        vectorStoreIDs,
		"session_status":          modelOutput.SessionStatus,
		"status_reason":           modelOutput.StatusReason,
		"remaining_uncertainties": modelOutput.RemainingUncertainties,
	}); err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}

	sessionState, err := s.store.RecordFacilitatorAssessment(ctx, workspaceID, userSourceID, modelOutput)
	if err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}
	if sessionState.FacilitatorStatus == FacilitatorStatusCandidateReady && s.readiness != nil {
		_, _ = s.readiness.QueueCandidate(ctx, sessionState, state.Strategy.ID, false)
	}

	messages, err := s.store.RecentChatMessages(ctx, workspaceID, 40)
	if err != nil {
		return StrategyFacilitatorMessageResponse{}, err
	}
	return StrategyFacilitatorMessageResponse{
		WorkspaceID:      workspaceID,
		AssistantMessage: assistantMessage,
		RecentMessages:   messages,
		OpenAIResponseID: result.ResponseID,
		SessionStatus:    sessionState.FacilitatorStatus,
		SessionRevision:  sessionState.Revision,
	}, nil
}

func (s *FacilitatorService) UploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (strategicmemory.FileUploadResponse, error) {
	return s.memoryService.UploadFile(ctx, workspaceID, userID, filename, contentType, sizeBytes, file)
}

func (s *FacilitatorService) strategyVectorStoreIDs(ctx context.Context, workspaceID int) []string {
	session, err := s.memoryStore.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil || strings.TrimSpace(session.VectorStoreID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(session.VectorStoreID)}
}

func (s *FacilitatorService) fallbackResponse(ctx context.Context, workspaceID int, userSourceID int, state StrategyFacilitatorState) StrategyFacilitatorMessageResponse {
	assistantMessage := strategyFallbackAssistantReply()
	modelOutput := strategyFacilitatorModelOutput{
		Message:       assistantMessage,
		SessionStatus: FacilitatorStatusContinue,
		StatusReason:  "The AI response failed, so the session remains open.",
	}
	_, _ = s.store.CreateChatMessage(ctx, workspaceID, nil, "assistant", assistantMessage, map[string]any{
		"prompt_version": StrategyFacilitatorPromptVersion,
		"fallback":       true,
		"user_source_id": userSourceID,
	})
	sessionState, _ := s.store.RecordFacilitatorAssessment(ctx, workspaceID, userSourceID, modelOutput)
	messages := state.RecentMessages
	if updated, err := s.store.RecentChatMessages(ctx, workspaceID, 40); err == nil {
		messages = updated
	}
	return StrategyFacilitatorMessageResponse{
		WorkspaceID:      workspaceID,
		AssistantMessage: assistantMessage,
		RecentMessages:   messages,
		SessionStatus:    sessionState.FacilitatorStatus,
		SessionRevision:  sessionState.Revision,
	}
}

func buildStrategyFacilitatorFreshInput(workspaceID int, message string, state StrategyFacilitatorState) string {
	contextPack := map[string]any{
		"workspace_id":        workspaceID,
		"latest_user_message": message,
		"recent_dialogue":     state.RecentMessages,
		"current_strategy": map[string]any{
			"strategy":  state.Strategy,
			"artifacts": limitArtifactsForContext(state.Artifacts, 20),
		},
		"knowledge_base": map[string]any{
			"summary":        state.KnowledgeBase.Summary,
			"documents":      limitDocumentsForStrategyContext(state.KnowledgeBase.Documents, 12),
			"quality_report": compactQualityReportForStrategyContext(state.KnowledgeBase.QualityReport),
			"uploaded_files": limitFilesForContext(state.KnowledgeBase.Files, 20),
		},
		"session_goal":              "Facilitate a strategic session using the collected company context. Reply to the latest user message and ask the next question that reduces strategic uncertainty the most.",
		"session_state":             state.Session,
		"latest_readiness_feedback": compactReadinessFeedback(state.Readiness),
	}
	rawInput, _ := json.Marshal(contextPack)
	return "Context for the strategic session in JSON:\n" + string(rawInput)
}

func buildStrategyFacilitatorTurnInput(message string, state StrategyFacilitatorState) string {
	turn := map[string]any{
		"latest_user_message":       message,
		"session_state":             state.Session,
		"latest_readiness_feedback": compactReadinessFeedback(state.Readiness),
		"instruction":               "Continue the same live strategic conversation. Respond naturally to the user, use any independent-auditor feedback as judgment context, and do not expose internal status mechanics.",
	}
	raw, _ := json.Marshal(turn)
	return string(raw)
}

func compactReadinessFeedback(run *StrategyReadinessRun) any {
	if run == nil || run.Report == nil {
		return nil
	}
	return map[string]any{
		"audited_session_revision": run.SessionRevision,
		"verdict":                  run.Report.Verdict,
		"can_synthesize":           run.Report.CanSynthesize,
		"executive_summary":        run.Report.ExecutiveSummary,
		"blocking_gaps":            run.Report.BlockingGaps,
		"weak_zones":               run.Report.WeakZones,
		"additional_perspectives":  run.Report.AdditionalPerspectives,
		"facilitator_guidance":     run.Report.FacilitatorGuidance,
	}
}

func parseStrategyFacilitatorOutput(raw string) (strategyFacilitatorModelOutput, error) {
	var output strategyFacilitatorModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return strategyFacilitatorModelOutput{}, err
	}
	output.Message = cleanAssistantMessage(output.Message)
	if output.Message == "" {
		return strategyFacilitatorModelOutput{}, fmt.Errorf("strategy facilitator returned empty message")
	}
	output.SessionStatus = normalizeFacilitatorStatus(output.SessionStatus)
	output.StatusReason = strings.TrimSpace(output.StatusReason)
	output.RemainingUncertainties = cleanStringListLocal(output.RemainingUncertainties)
	return output, nil
}

func normalizeFacilitatorStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case FacilitatorStatusCandidateReady:
		return FacilitatorStatusCandidateReady
	case FacilitatorStatusNeedsResearch:
		return FacilitatorStatusNeedsResearch
	default:
		return FacilitatorStatusContinue
	}
}

func limitArtifactsForContext(items []Artifact, limit int) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"type":        item.Type,
			"title":       item.Title,
			"description": item.Description,
			"content":     limitStringRunes(item.Content, 2200),
			"status":      item.Status,
			"confidence":  item.Confidence,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func limitDocumentsForStrategyContext(items []strategicmemory.StrategicDocument, limit int) []map[string]string {
	result := make([]map[string]string, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]string{
			"document_type": item.DocumentType,
			"title":         item.Title,
			"markdown":      limitStringRunes(item.Markdown, 8000),
			"status":        item.Status,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func limitFilesForContext(items []strategicmemory.StrategicFile, limit int) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"filename":     item.Filename,
			"content_type": item.ContentType,
			"size_bytes":   item.SizeBytes,
			"status":       item.Status,
			"created_at":   item.CreatedAt,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func compactQualityReportForStrategyContext(report *strategicmemory.QualityReport) any {
	if report == nil {
		return nil
	}
	return map[string]any{
		"readiness_score":  report.ReadinessScore,
		"readiness_status": report.ReadinessStatus,
		"overall": map[string]any{
			"summary":                         report.Overall.Summary,
			"critical_blockers":               report.Overall.CriticalBlockers,
			"most_important_missing_info":     report.Overall.MostImportantMissingInfo,
			"major_inconsistencies":           report.Overall.MajorInconsistencies,
			"highest_priority_improvements":   report.Overall.HighestPriorityImprovements,
			"highest_priority_clarifications": report.Overall.HighestPriorityQuestions,
			"cross_document_quality_score":    report.Overall.CrossDocumentQualityScore,
		},
		"chat_guidance": report.ChatGuidance,
		"strategy_gate": report.StrategyGate,
	}
}

func limitStringRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n\n[truncated]"
}

func cleanAssistantMessage(value string) string {
	return strings.TrimSpace(value)
}

func strategyFallbackAssistantReply() string {
	return "Сейчас не смог стабильно продолжить стратегическую сессию. Давай повторим последний шаг: что в текущей ситуации бизнеса кажется тебе самым неопределённым или самым спорным?"
}
