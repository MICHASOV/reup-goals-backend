package strategicmemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
)

const (
	knowledgeExtractionMaxOutputTokens = 12000
	knowledgeCompilerMaxOutputTokens   = 24000
	knowledgeSourceChunkRunes          = 120000
	knowledgeCandidateTimeout          = 9 * time.Minute
	knowledgeDocumentPromptVersion     = "knowledge_document_compiler_v1_2"
)

type knowledgeCandidateJobPayload struct {
	Revision        int    `json:"revision"`
	ThroughSourceID int    `json:"through_source_id"`
	CandidateReason string `json:"candidate_reason"`
}

type knowledgeCompilationSource struct {
	SourceID   int             `json:"source_id"`
	SourceType string          `json:"source_type"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (s *Service) queueManualKnowledgeCandidate(ctx context.Context, workspaceID int) (KnowledgePipelineState, bool, error) {
	state, err := s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return KnowledgePipelineState{}, false, err
	}
	if state.LastUserSourceID <= state.LastAuditedSourceID || state.Status == KnowledgePipelineReady {
		return state, false, nil
	}
	queued, err := s.queueKnowledgeCandidate(ctx, workspaceID, state, state.LastUserSourceID, "manual knowledge review")
	if err != nil {
		return state, false, err
	}
	return queued, queued.Status == KnowledgePipelineAuditCandidate, nil
}

func (s *Service) queueKnowledgeCandidate(
	ctx context.Context,
	workspaceID int,
	state KnowledgePipelineState,
	throughSourceID int,
	reason string,
) (KnowledgePipelineState, error) {
	state, started, err := s.store.TryStartKnowledgeCandidate(
		ctx, workspaceID, state.ConversationRevision, throughSourceID, reason,
	)
	if err != nil || !started {
		return state, err
	}
	payload := knowledgeCandidateJobPayload{
		Revision: state.CandidateRevision, ThroughSourceID: state.CandidateSourceID,
		CandidateReason: state.CandidateReason,
	}
	if s.jobs != nil {
		_, err = s.jobs.Enqueue(ctx, workspaceID, jobTypeKnowledgeCandidate,
			fmt.Sprintf("revision:%d", payload.Revision), payload, 3, time.Now().UTC())
		if err != nil {
			_ = s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
			return state, err
		}
		return state, nil
	}
	parentCtx := context.WithoutCancel(ctx)
	go func() {
		jobCtx, cancel := context.WithTimeout(parentCtx, knowledgeCandidateTimeout)
		defer cancel()
		if runErr := s.runKnowledgeCandidate(jobCtx, workspaceID, payload); runErr != nil {
			log.Printf("[WARN] knowledge candidate failed workspace_id=%d revision=%d: %v", workspaceID, payload.Revision, runErr)
		}
	}()
	return state, nil
}

func (s *Service) runKnowledgeCandidate(ctx context.Context, workspaceID int, payload knowledgeCandidateJobPayload) error {
	state, err := s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return err
	}
	if state.Status == KnowledgePipelineReady && state.ReadyRevision >= payload.Revision {
		return nil
	}
	if state.CandidateRevision != payload.Revision || state.CandidateSourceID != payload.ThroughSourceID {
		return nil
	}
	if state.LastUserSourceID > payload.ThroughSourceID {
		return s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
	}

	if state.LastExtractedSourceID < payload.ThroughSourceID {
		if err := s.store.UpdateKnowledgePipelineStatus(ctx, workspaceID, payload.Revision, KnowledgePipelineExtracting); err != nil {
			return err
		}
		sources, err := s.store.KnowledgeSourcesRange(ctx, workspaceID, state.LastExtractedSourceID, payload.ThroughSourceID)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			return fmt.Errorf("knowledge candidate has no new sources")
		}
		contextChanged := false
		for _, chunk := range chunkKnowledgeSources(sources, knowledgeSourceChunkRunes) {
			changed, err := s.extractKnowledgeSourceChunk(ctx, workspaceID, chunk)
			if err != nil {
				return err
			}
			contextChanged = contextChanged || changed
		}
		if contextChanged {
			if err := s.refreshMaterialContextSnapshot(ctx, workspaceID); err != nil {
				return err
			}
		}
		if err := s.store.MarkKnowledgeExtracted(ctx, workspaceID, payload.Revision, payload.ThroughSourceID); err != nil {
			return err
		}
	}

	state, err = s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return err
	}
	if state.LastUserSourceID > payload.ThroughSourceID {
		return s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
	}

	var report QualityReport
	checkpointReady := state.Status == KnowledgePipelineCompiling &&
		json.Unmarshal(state.CandidateReport, &report) == nil && report.StrategyGate.CanStartStrategy
	if !checkpointReady {
		if err := s.store.UpdateKnowledgePipelineStatus(ctx, workspaceID, payload.Revision, KnowledgePipelineReviewing); err != nil {
			return err
		}
		report, err = s.evaluateQualityAudit(ctx, workspaceID, allStrategicDocumentTypes(), "interview_candidate")
		if err != nil {
			return err
		}
		if err := s.store.CompleteKnowledgeReview(ctx, workspaceID, payload.Revision, payload.ThroughSourceID, report); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
			}
			return err
		}
		if !report.StrategyGate.CanStartStrategy {
			s.markQualityAuditCompleted(workspaceID)
			if s.contextIndex != nil {
				s.contextIndex.RefreshAsync(workspaceID)
			}
			return nil
		}
	}

	state, err = s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return err
	}
	if state.LastUserSourceID > payload.ThroughSourceID {
		return s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
	}
	documents, err := s.compileKnowledgeDocuments(ctx, workspaceID, report)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return fmt.Errorf("knowledge compiler returned no documents")
	}
	if _, err := s.store.PublishKnowledgeCompilation(
		ctx, workspaceID, payload.Revision, payload.ThroughSourceID, report, documents,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.store.SupersedeKnowledgeCandidate(ctx, workspaceID, payload.Revision)
		}
		return err
	}
	s.markQualityAuditCompleted(workspaceID)
	if s.contextIndex != nil {
		s.contextIndex.RefreshAsync(workspaceID)
	}
	return nil
}

func (s *Service) extractKnowledgeSourceChunk(ctx context.Context, workspaceID int, sources []RawSource) (bool, error) {
	claims, err := s.store.ListClaims(ctx, workspaceID, 2000)
	if err != nil {
		return false, err
	}
	agenda, err := s.store.ListAgenda(ctx, workspaceID, 500)
	if err != nil {
		return false, err
	}
	snapshot, err := s.store.LatestSnapshot(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	files, err := s.store.ListFiles(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return false, err
	}

	input := map[string]any{
		"workspace_id":      workspaceID,
		"processing_mode":   "deferred_conversation_compilation",
		"sources":           compilationSources(sources),
		"uploaded_files":    files,
		"document_catalog":  strategicDocumentCatalog(),
		"existing_claims":   limitClaimsForContext(claims, 500),
		"existing_agenda":   limitAgendaForContext(agenda, 120),
		"existing_snapshot": snapshot,
	}
	policy := knowledgeSourceChunkPolicy(sources)
	if policy.FactsOnly {
		input["facts_only"] = true
	}
	if policy.PreferredDocumentType != "" {
		input["preferred_document_type"] = policy.PreferredDocumentType
	}
	vectorStoreIDs, indexed := s.workspaceContextVectorStoreIDs(ctx, workspaceID, session)
	if indexed {
		delete(input, "existing_claims")
		delete(input, "existing_agenda")
		delete(input, "existing_snapshot")
		input["sources"] = compilationSourcesForIndexedContext(sources)
		input["current_workspace_context"] = "Use file_search to compare the new sources with current claims, open questions, and the latest snapshot. Extract only new or changed information and avoid duplicates."
	}
	rawInput, _ := json.Marshal(input)
	workerAI := s.ai.ForModel(knowledgeExtractionModel)
	aiCtx := ai.WithScenario(ctx, workspaceID, 0, "knowledge_base_deferred_extractor", StrategicMemoryPromptVersion)
	started := time.Now()
	result, err := workerAI.GenerateJSONNative(aiCtx, businessContextMaterializerPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		PromptCacheKey:       fmt.Sprintf("reupgoals-knowledge-extractor-workspace-%d-v2", workspaceID),
		MaxFileSearchResults: 12,
		MaxOutputTokens:      knowledgeExtractionMaxOutputTokens,
		RequestTimeout:       materializerTimeout,
		ReasoningEffort:      "none",
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_deferred_extractor", workerAI.ModelName(), StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return false, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_deferred_extractor", workerAI.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var materialized materializerOutput
	if err := json.Unmarshal([]byte(result.Text), &materialized); err != nil {
		return false, fmt.Errorf("deferred extractor json decode error: %w", err)
	}
	if policy.FactsOnly {
		materialized = factsOnlyMaterialization(materialized)
	}
	fallbackSourceID := lastEvidenceSourceID(sources)
	added, _, err := s.store.InsertClaims(ctx, workspaceID, fallbackSourceID, materializerItemsToClaims(materialized.ExtractedItems))
	if err != nil {
		return false, err
	}
	if _, err := s.store.UpsertAgenda(ctx, workspaceID, materializerQuestionsToAgenda(materialized.OpenQuestions, materialized.Contradictions)); err != nil {
		return false, err
	}
	if len(materialized.Snapshot) > 0 {
		if _, err := s.store.SaveSnapshot(ctx, workspaceID, materialized.BusinessStage, materialized.Snapshot); err != nil {
			return false, err
		}
	}
	return added > 0, nil
}

func (s *Service) compileKnowledgeDocuments(ctx context.Context, workspaceID int, report QualityReport) ([]StrategicDocument, error) {
	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	claims, err := s.store.ListClaims(ctx, workspaceID, 3000)
	if err != nil {
		return nil, err
	}
	agenda, err := s.store.ListAgenda(ctx, workspaceID, 500)
	if err != nil {
		return nil, err
	}
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return nil, err
	}
	input := map[string]any{
		"workspace_id":            workspaceID,
		"compilation_mode":        "full",
		"output_language":         "Russian",
		"document_catalog":        strategicDocumentCatalog(),
		"knowledge_claims":        claims,
		"research_agenda":         agenda,
		"current_snapshot":        state.Snapshot,
		"current_documents":       state.Documents,
		"quality_report":          report,
		"uploaded_file_catalog":   state.Files,
		"claim_reference_catalog": claimsForQualityAuditContext(claims),
	}
	vectorStoreIDs, indexed := s.workspaceContextVectorStoreIDs(ctx, workspaceID, session)
	if indexed {
		delete(input, "knowledge_claims")
		delete(input, "research_agenda")
		delete(input, "current_snapshot")
		delete(input, "current_documents")
		input["current_workspace_context"] = "Use file_search as the source of truth for all current claims, sources, open questions, contradictions, and existing documents."
	}
	rawInput, _ := json.Marshal(input)
	workerAI := s.ai.ForModel(knowledgeDocumentModel)
	aiCtx := ai.WithScenario(ctx, workspaceID, 0, "knowledge_base_document_compiler", knowledgeDocumentPromptVersion)
	started := time.Now()
	result, err := workerAI.GenerateJSONNative(aiCtx, documentVisualDesignerPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		PromptCacheKey:       fmt.Sprintf("reupgoals-knowledge-compiler-workspace-%d-v4", workspaceID),
		MaxFileSearchResults: 15,
		MaxOutputTokens:      knowledgeCompilerMaxOutputTokens,
		RequestTimeout:       4 * time.Minute,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_document_compiler", workerAI.ModelName(), knowledgeDocumentPromptVersion, duration, 0, 0, "failed", err.Error())
		return nil, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_document_compiler", workerAI.ModelName(), knowledgeDocumentPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var parsed documentDesignerOutput
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		return nil, fmt.Errorf("knowledge compiler json decode error: %w", err)
	}
	claimIDs := map[int]bool{}
	for _, claim := range claims {
		claimIDs[claim.ID] = true
	}
	documents := make([]StrategicDocument, 0, len(parsed.Documents))
	for _, doc := range parsed.Documents {
		docType := normalizeDocumentType(doc.DocumentType)
		markdown := strings.TrimSpace(doc.Markdown)
		if markdown == "" || !validStrategicDocumentType(docType) {
			continue
		}
		documents = append(documents, StrategicDocument{
			WorkspaceID: workspaceID, DocumentType: docType,
			Title: defaultString(doc.Title, documentTitle(docType)), Markdown: markdown,
			SourceClaimIDs: mustJSON(validDocumentClaimIDs(doc.SourceClaimIDs, claimIDs)),
			Status:         normalizeDocumentStatus(doc.Status),
		})
	}
	return documents, nil
}

func chunkKnowledgeSources(sources []RawSource, maxRunes int) [][]RawSource {
	if maxRunes <= 0 {
		maxRunes = knowledgeSourceChunkRunes
	}
	chunks := [][]RawSource{}
	current := []RawSource{}
	currentRunes := 0
	currentPolicy := ""
	for _, source := range sources {
		size := len([]rune(source.Content))
		policy := knowledgeSourcePolicyKey(source)
		if len(current) > 0 && (currentRunes+size > maxRunes || policy != currentPolicy) {
			chunks = append(chunks, current)
			current = nil
			currentRunes = 0
		}
		if len(current) == 0 {
			currentPolicy = policy
		}
		current = append(current, source)
		currentRunes += size
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func compilationSources(sources []RawSource) []knowledgeCompilationSource {
	result := make([]knowledgeCompilationSource, 0, len(sources))
	for _, source := range sources {
		role := "context"
		switch source.SourceType {
		case SourceTypeUserMessage, SourceTypeStrategyMessage, SourceTypeDocumentMessage,
			SourceTypeTacticsMessage, SourceTypeTaskDiscussion:
			role = "user"
		case SourceTypeAssistantMessage:
			role = "assistant"
		case SourceTypeFileUpload:
			role = "uploaded_file"
		case SourceTypeWorkspaceDoc, SourceTypeTaskCompletion, SourceTypeDepartment,
			SourceTypeTacticalPlan, SourceTypeWorkstream, SourceTypeProject,
			SourceTypeRisk, SourceTypeOpportunity, SourceTypeResearchResult:
			role = "business_record"
		}
		result = append(result, knowledgeCompilationSource{
			SourceID: source.ID, SourceType: source.SourceType, Role: role,
			Content: source.Content, Metadata: source.Metadata, CreatedAt: source.CreatedAt,
		})
	}
	return result
}

func compilationSourcesForIndexedContext(sources []RawSource) []knowledgeCompilationSource {
	result := compilationSources(sources)
	for index, source := range sources {
		if !structuredSourceAvailableInWorkspaceIndex(source.SourceType) {
			continue
		}
		result[index].Content = "The complete current record is available through file_search. Use its source_type and metadata to locate it."
	}
	return result
}

func structuredSourceAvailableInWorkspaceIndex(sourceType string) bool {
	switch sourceType {
	case SourceTypeWorkspaceDoc, SourceTypeTaskCompletion, SourceTypeDepartment,
		SourceTypeTacticalPlan, SourceTypeWorkstream, SourceTypeProject,
		SourceTypeRisk, SourceTypeOpportunity, SourceTypeResearchResult:
		return true
	default:
		return false
	}
}

func lastEvidenceSourceID(sources []RawSource) int {
	for index := len(sources) - 1; index >= 0; index-- {
		if sources[index].SourceType == SourceTypeUserMessage ||
			sources[index].SourceType == SourceTypeFileUpload ||
			sources[index].SourceType == SourceTypeStrategyMessage ||
			sources[index].SourceType == SourceTypeDocumentMessage ||
			sources[index].SourceType == SourceTypeTacticsMessage ||
			sources[index].SourceType == SourceTypeTaskDiscussion ||
			sources[index].SourceType == SourceTypeWorkspaceDoc ||
			sources[index].SourceType == SourceTypeTaskCompletion ||
			sources[index].SourceType == SourceTypeDepartment ||
			sources[index].SourceType == SourceTypeTacticalPlan ||
			sources[index].SourceType == SourceTypeWorkstream ||
			sources[index].SourceType == SourceTypeProject ||
			sources[index].SourceType == SourceTypeRisk ||
			sources[index].SourceType == SourceTypeOpportunity ||
			sources[index].SourceType == SourceTypeResearchResult {
			return sources[index].ID
		}
	}
	if len(sources) > 0 {
		return sources[len(sources)-1].ID
	}
	return 0
}

type knowledgeChunkPolicy struct {
	FactsOnly             bool
	PreferredDocumentType string
}

func knowledgeSourceChunkPolicy(sources []RawSource) knowledgeChunkPolicy {
	if len(sources) == 0 {
		return knowledgeChunkPolicy{}
	}
	return knowledgeSourcePolicy(sources[0])
}

func knowledgeSourcePolicyKey(source RawSource) string {
	policy := knowledgeSourcePolicy(source)
	return fmt.Sprintf("facts_only=%t|document=%s", policy.FactsOnly, policy.PreferredDocumentType)
}

func knowledgeSourcePolicy(source RawSource) knowledgeChunkPolicy {
	var metadata struct {
		FactsOnly             bool   `json:"facts_only"`
		PreferredDocumentType string `json:"preferred_document_type"`
		DocumentType          string `json:"document_type"`
	}
	_ = json.Unmarshal(source.Metadata, &metadata)
	preferredDocumentType := normalizeDocumentType(defaultString(metadata.PreferredDocumentType, metadata.DocumentType))
	if !validStrategicDocumentType(preferredDocumentType) {
		preferredDocumentType = ""
	}
	return knowledgeChunkPolicy{
		FactsOnly:             metadata.FactsOnly,
		PreferredDocumentType: preferredDocumentType,
	}
}

func allStrategicDocumentTypes() []string {
	definitions := strategicDocumentDefinitions()
	result := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, definition.DocumentType)
	}
	return result
}

func validDocumentClaimIDs(candidate []int, valid map[int]bool) []int {
	result := make([]int, 0, len(candidate))
	seen := map[int]bool{}
	for _, id := range candidate {
		if id <= 0 || !valid[id] || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func pipelineConversationState(status string) string {
	switch status {
	case KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting, KnowledgePipelineReviewing, KnowledgePipelineCompiling:
		return ConversationStateProcessingContext
	case KnowledgePipelineReady:
		return ConversationStateReadyForStrategy
	default:
		return ConversationStateCollectingContext
	}
}
