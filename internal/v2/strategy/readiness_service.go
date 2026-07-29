package strategy

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
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	strategyReadinessMaxOutputTokens = 12000
	strategyReadinessTimeout         = 4 * time.Minute
	strategyReadinessPollInterval    = 5 * time.Second

	strategyReadinessConditionalThreshold = 750
	strategyReadinessReadyThreshold       = 800
	strategyReadinessCoreMinimum          = 700
)

var strategyReadinessWeights = map[string]int{
	"current_reality":                6,
	"business_stage":                 3,
	"owner_intent":                   3,
	"target_state":                   8,
	"horizon_fit":                    8,
	"strategic_challenge":            8,
	"strategic_choice":               9,
	"customer_and_market":            6,
	"way_to_win":                     8,
	"economic_engine":                10,
	"causal_logic":                   10,
	"resources_and_capabilities":     6,
	"governance_and_owner_role":      4,
	"goals_and_metrics":              4,
	"risks_assumptions_and_evidence": 4,
	"alternatives_and_scenarios":     3,
}

var strategyReadinessCriterionOrder = []string{
	"current_reality",
	"business_stage",
	"owner_intent",
	"target_state",
	"horizon_fit",
	"strategic_challenge",
	"strategic_choice",
	"customer_and_market",
	"way_to_win",
	"economic_engine",
	"causal_logic",
	"resources_and_capabilities",
	"governance_and_owner_role",
	"goals_and_metrics",
	"risks_assumptions_and_evidence",
	"alternatives_and_scenarios",
}

var strategyReadinessCriterionLabels = map[string]string{
	"current_reality":                "Current business reality",
	"business_stage":                 "Company stage and condition",
	"owner_intent":                   "Owner intent",
	"target_state":                   "Target outcome or state",
	"horizon_fit":                    "Horizon fit",
	"strategic_challenge":            "Central strategic challenge",
	"strategic_choice":               "Strategic choice and conscious refusals",
	"customer_and_market":            "Customer and market",
	"way_to_win":                     "Way to win",
	"economic_engine":                "Economic engine",
	"causal_logic":                   "Causal logic",
	"resources_and_capabilities":     "Resources and capabilities",
	"governance_and_owner_role":      "Governance and owner role",
	"goals_and_metrics":              "Goals and metrics",
	"risks_assumptions_and_evidence": "Risks, assumptions, and evidence",
	"alternatives_and_scenarios":     "Alternatives and scenarios",
}

var strategyReadinessCoreCriteria = map[string]bool{
	"target_state":     true,
	"horizon_fit":      true,
	"strategic_choice": true,
	"economic_engine":  true,
	"causal_logic":     true,
}

type ReadinessService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	ai               ai.Provider
	compactThreshold int
	wake             chan struct{}
	contextIndex     *contextindex.Service
	synthesis        *SynthesisService
}

func (s *ReadinessService) SetSynthesisService(synthesis *SynthesisService) {
	s.synthesis = synthesis
}

func (s *ReadinessService) SetContextIndex(index *contextindex.Service) {
	s.contextIndex = index
}

func NewReadinessService(
	dbx *sql.DB,
	aiClient ai.Provider,
	compactThreshold int,
) *ReadinessService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	return &ReadinessService{
		store:            NewStore(dbx),
		memoryStore:      strategicmemory.NewStore(dbx),
		ai:               aiClient,
		compactThreshold: compactThreshold,
		wake:             make(chan struct{}, 1),
	}
}

func (s *ReadinessService) StartWorker() {
	go func() {
		ticker := time.NewTicker(strategyReadinessPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-s.wake:
			}
			s.processDueAudits()
		}
	}()
}

func (s *ReadinessService) Latest(ctx context.Context, workspaceID int) (*StrategyReadinessRun, error) {
	strategy, err := s.store.getCurrent(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.store.LatestReadinessAudit(ctx, workspaceID, strategy.ID)
}

func (s *ReadinessService) QueueCandidate(
	ctx context.Context,
	state StrategySessionState,
	strategyID int,
) (StrategyReadinessQueueItem, error) {
	if state.Revision <= 0 || state.LastUserMessageID <= 0 {
		return StrategyReadinessQueueItem{}, fmt.Errorf("strategy_readiness_no_session")
	}
	item, err := s.store.QueueReadinessAudit(ctx, state, strategyID)
	if err != nil {
		return StrategyReadinessQueueItem{}, err
	}
	if !item.NotBefore.After(time.Now().Add(time.Second)) {
		s.signalWorker()
	}
	return item, nil
}

func (s *ReadinessService) signalWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ReadinessService) processDueAudits() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		run, err := s.store.ClaimDueReadinessAudit(ctx, s.ai.ModelName())
		cancel()
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			return
		}
		s.executeDetached(run)
	}
}

func (s *ReadinessService) executeDetached(run StrategyReadinessRun) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), strategyReadinessTimeout)
		defer cancel()
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer failureCancel()
				_ = s.store.FailReadinessAudit(failureCtx, run, time.Since(started).Milliseconds(), fmt.Sprintf("strategy readiness panic: %v", recovered))
			}
		}()
		if err := s.execute(ctx, run); err != nil {
			failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer failureCancel()
			_ = s.store.FailReadinessAudit(failureCtx, run, time.Since(started).Milliseconds(), err.Error())
		}
	}()
}

func (s *ReadinessService) execute(ctx context.Context, run StrategyReadinessRun) error {
	strategy, err := s.store.StrategyByID(ctx, run.WorkspaceID, run.StrategyID)
	if err != nil {
		return err
	}
	documents, err := s.memoryStore.ListDocuments(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	qualityReport, err := s.memoryStore.LatestQualityReport(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	files, err := s.memoryStore.ListFiles(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	messages, err := s.store.ChatMessages(ctx, run.WorkspaceID, 500)
	if err != nil {
		return err
	}
	messages = messagesThroughID(messages, run.ValidatedThroughMessageID)
	session, err := s.memoryStore.OpenAISession(ctx, run.WorkspaceID, s.compactThreshold)
	if err != nil {
		return err
	}
	state, err := s.store.SessionState(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	if state.Revision != run.SessionRevision || state.LastUserMessageID != run.ValidatedThroughMessageID {
		return s.store.SupersedeReadinessAudit(ctx, run)
	}

	catalog, sourceIndex := buildSynthesisSourceCatalog(documents, messages, files)
	input := buildStrategySynthesisInput(strategy, documents, qualityReport, messages, files, catalog)
	input["output_stage"] = "independent_strategy_readiness_audit"
	input["session_revision"] = run.SessionRevision
	input["validated_through_message_id"] = run.ValidatedThroughMessageID
	input["facilitator_assessment"] = map[string]any{
		"status":                  state.FacilitatorStatus,
		"reason":                  state.StatusReason,
		"remaining_uncertainties": state.RemainingUncertainties,
	}
	vectorStoreIDs := synthesisVectorStoreIDs(session)
	if s.contextIndex != nil {
		indexedIDs, indexErr := s.contextIndex.Ensure(ctx, run.WorkspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
			delete(input, "knowledge_base_documents")
			delete(input, "knowledge_base_quality")
			delete(input, "uploaded_files")
			input["current_workspace_context"] = "Use file_search for the current Knowledge Base, uploaded files, existing strategy materials, and any other evidence relevant to the long-term strategy."
		}
	}
	rawInput, err := json.Marshal(input)
	if err != nil {
		return err
	}

	started := time.Now()
	aiCtx := ai.WithScenario(ctx, run.WorkspaceID, 0, "strategy_readiness_auditor", StrategyReadinessPromptVersion)
	result, err := s.ai.GenerateJSONNative(aiCtx, strategyReadinessPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-strategy-readiness-workspace-%d-v1", run.WorkspaceID),
		MaxFileSearchResults: 20,
		MaxOutputTokens:      strategyReadinessMaxOutputTokens,
		RequestTimeout:       strategyReadinessTimeout - 15*time.Second,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "strategy_readiness_auditor", s.ai.ModelName(), StrategyReadinessPromptVersion, duration, 0, 0, "failed", err.Error())
		return err
	}

	var report StrategyReadinessReport
	if err := json.Unmarshal([]byte(result.Text), &report); err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "strategy_readiness_auditor", s.ai.ModelName(), StrategyReadinessPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return fmt.Errorf("strategy readiness json decode error: %w", err)
	}
	report = normalizeReadinessReport(report, run, sourceIndex)
	_, err = s.store.CompleteReadinessAudit(ctx, run, report, result.Usage.InputTokens, result.Usage.OutputTokens, duration)
	if err != nil {
		return err
	}
	s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "strategy_readiness_auditor", s.ai.ModelName(), StrategyReadinessPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	if report.CanSynthesize && s.synthesis != nil && run.CreatedBy != nil {
		if _, synthesisErr := s.synthesis.StartForRevision(
			ctx,
			run.WorkspaceID,
			*run.CreatedBy,
			run.SessionRevision,
			run.ValidatedThroughMessageID,
		); synthesisErr != nil {
			log.Printf(
				"[WARN] automatic strategy synthesis failed workspace_id=%d revision=%d: %v",
				run.WorkspaceID,
				run.SessionRevision,
				synthesisErr,
			)
		}
	}
	if s.contextIndex != nil {
		s.contextIndex.RefreshAsync(run.WorkspaceID)
	}

	return nil
}

func messagesThroughID(messages []StrategyChatMessage, throughID int) []StrategyChatMessage {
	if throughID <= 0 {
		return messages
	}
	result := make([]StrategyChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.ID <= throughID {
			result = append(result, message)
		}
	}
	return result
}

func normalizeReadinessReport(
	report StrategyReadinessReport,
	run StrategyReadinessRun,
	sourceIndex map[string]strategySynthesisSourceCatalogItem,
) StrategyReadinessReport {
	report.SessionRevision = run.SessionRevision
	report.ValidatedThroughMessageID = run.ValidatedThroughMessageID
	report.Confidence = normalizeReadinessConfidence(report.Confidence)
	report.ExecutiveSummary = strings.TrimSpace(report.ExecutiveSummary)

	cleanSources := func(items []string) []string {
		result := []string{}
		for _, item := range items {
			item = strings.TrimSpace(item)
			if _, ok := sourceIndex[item]; !ok || containsString(result, item) {
				continue
			}
			result = append(result, item)
		}
		return result
	}

	criteriaByCode := map[string]StrategyReadinessCriterion{}
	for _, criterion := range report.CriteriaAssessment {
		code := strings.TrimSpace(strings.ToLower(criterion.CriterionCode))
		if _, ok := strategyReadinessWeights[code]; !ok {
			continue
		}
		criterion.CriterionCode = code
		criterion.Area = strings.TrimSpace(criterion.Area)
		if criterion.Area == "" {
			criterion.Area = strategyReadinessCriterionLabels[code]
		}
		criterion.Score = clampStrategyReadinessScore(criterion.Score)
		criterion.Status = strategyReadinessStatusForScore(criterion.Score)
		criterion.Assessment = strings.TrimSpace(criterion.Assessment)
		criterion.Strengths = cleanStringListLocal(criterion.Strengths)
		criterion.Gaps = cleanStringListLocal(criterion.Gaps)
		criterion.SourceKeys = cleanSources(criterion.SourceKeys)
		criteriaByCode[code] = criterion
	}

	report.CriteriaAssessment = make([]StrategyReadinessCriterion, 0, len(strategyReadinessCriterionOrder))
	weightedTotal := 0
	missingRequiredCriterion := false
	coreCriterionBelowMinimum := false
	for _, code := range strategyReadinessCriterionOrder {
		criterion, ok := criteriaByCode[code]
		if !ok {
			missingRequiredCriterion = true
			criterion = StrategyReadinessCriterion{
				CriterionCode: code,
				Area:          strategyReadinessCriterionLabels[code],
				Score:         1,
				Status:        "missing",
				Assessment:    "The auditor did not return an assessment for this required criterion.",
				Strengths:     []string{},
				Gaps:          []string{"Required criterion was not assessed."},
				SourceKeys:    []string{},
			}
		}
		if strategyReadinessCoreCriteria[code] && criterion.Score < strategyReadinessCoreMinimum {
			coreCriterionBelowMinimum = true
		}
		weightedTotal += criterion.Score * strategyReadinessWeights[code]
		report.CriteriaAssessment = append(report.CriteriaAssessment, criterion)
	}
	report.OverallScore = (weightedTotal + 50) / 100
	report.ReadinessPercent = float64(report.OverallScore) / 10

	for index := range report.BlockingGaps {
		report.BlockingGaps[index].SourceKeys = cleanSources(report.BlockingGaps[index].SourceKeys)
	}
	for index := range report.WeakZones {
		report.WeakZones[index].SourceKeys = cleanSources(report.WeakZones[index].SourceKeys)
	}
	for index := range report.Contradictions {
		report.Contradictions[index].SourceKeys = cleanSources(report.Contradictions[index].SourceKeys)
	}
	for index := range report.CriticalAssumptions {
		report.CriticalAssumptions[index].SourceKeys = cleanSources(report.CriticalAssumptions[index].SourceKeys)
	}
	for index := range report.AdditionalPerspectives {
		report.AdditionalPerspectives[index].SourceKeys = cleanSources(report.AdditionalPerspectives[index].SourceKeys)
	}
	report.SynthesisGuidance.ImportantSourceKeys = cleanSources(report.SynthesisGuidance.ImportantSourceKeys)

	if report.BlockingGaps == nil {
		report.BlockingGaps = []StrategyReadinessIssue{}
	}
	if report.WeakZones == nil {
		report.WeakZones = []StrategyReadinessWeakZone{}
	}
	if report.Contradictions == nil {
		report.Contradictions = []StrategyReadinessContradiction{}
	}
	if report.CriticalAssumptions == nil {
		report.CriticalAssumptions = []StrategyReadinessAssumption{}
	}
	if report.AdditionalPerspectives == nil {
		report.AdditionalPerspectives = []StrategyReadinessPerspective{}
	}
	if report.FacilitatorGuidance == nil {
		report.FacilitatorGuidance = []StrategyReadinessFacilitatorGuide{}
	}

	switch {
	case len(report.BlockingGaps) > 0 || missingRequiredCriterion || report.OverallScore < strategyReadinessConditionalThreshold:
		report.Verdict = ReadinessVerdictNotReady
		report.CanSynthesize = false
	case report.OverallScore < strategyReadinessReadyThreshold || coreCriterionBelowMinimum:
		report.Verdict = ReadinessVerdictConditionallyReady
		report.CanSynthesize = false
	default:
		report.Verdict = ReadinessVerdictReady
		report.CanSynthesize = true
	}

	report.FacilitatorGuidance = normalizeFacilitatorGuidance(report.FacilitatorGuidance, report.Verdict)
	if len(report.FacilitatorGuidance) == 0 {
		report.FacilitatorGuidance = defaultFacilitatorGuidance(report)
	}
	if report.Verdict == ReadinessVerdictReady {
		for index := range report.AdditionalPerspectives {
			report.AdditionalPerspectives[index].IsBlocking = false
		}
	}
	report.SynthesisGuidance.WarningsToPreserve = cleanStringListLocal(report.SynthesisGuidance.WarningsToPreserve)
	report.SynthesisGuidance.AssumptionsToPreserve = cleanStringListLocal(report.SynthesisGuidance.AssumptionsToPreserve)
	report.SynthesisGuidance.ResearchToInclude = cleanStringListLocal(report.SynthesisGuidance.ResearchToInclude)
	return report
}

func clampStrategyReadinessScore(value int) int {
	if value < 1 {
		return 1
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func strategyReadinessStatusForScore(score int) string {
	switch {
	case score < 200:
		return "missing"
	case score < 600:
		return "weak"
	case score < 850:
		return "sufficient"
	default:
		return "strong"
	}
}

func normalizeFacilitatorGuidance(
	items []StrategyReadinessFacilitatorGuide,
	verdict string,
) []StrategyReadinessFacilitatorGuide {
	result := make([]StrategyReadinessFacilitatorGuide, 0, len(items))
	for _, item := range items {
		item.Priority = strings.TrimSpace(strings.ToLower(item.Priority))
		if item.Priority != "high" && item.Priority != "low" {
			item.Priority = "medium"
		}
		item.Area = strings.TrimSpace(item.Area)
		item.ResearchGoal = strings.TrimSpace(item.ResearchGoal)
		item.WhyItMatters = strings.TrimSpace(item.WhyItMatters)
		item.ContextToCarry = strings.TrimSpace(item.ContextToCarry)
		if item.Area == "" && item.ResearchGoal == "" && item.WhyItMatters == "" && item.ContextToCarry == "" {
			continue
		}
		if verdict == ReadinessVerdictReady {
			item.Blocking = false
		}
		result = append(result, item)
	}
	return result
}

func normalizeReadinessConfidence(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "high", "low":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return "medium"
	}
}

func defaultFacilitatorGuidance(report StrategyReadinessReport) []StrategyReadinessFacilitatorGuide {
	if report.Verdict == ReadinessVerdictReady {
		return []StrategyReadinessFacilitatorGuide{{
			Priority:       "low",
			Area:           "strategy integrity",
			ResearchGoal:   "Preserve the explicit choices, assumptions, and unresolved research during synthesis and future revisions.",
			WhyItMatters:   "The strategy is ready, but its quality depends on keeping the boundary between evidence and uncertainty visible.",
			ContextToCarry: strings.TrimSpace(report.ExecutiveSummary),
			Blocking:       false,
		}}
	}
	return []StrategyReadinessFacilitatorGuide{{
		Priority:       "high",
		Area:           "readiness gaps",
		ResearchGoal:   "Resolve the highest-impact unresolved point before requesting another readiness audit.",
		WhyItMatters:   "The independent audit found that the current material is not yet sufficient for a reliable synthesis.",
		ContextToCarry: strings.TrimSpace(report.ExecutiveSummary),
		Blocking:       report.Verdict == ReadinessVerdictNotReady,
	}}
}
