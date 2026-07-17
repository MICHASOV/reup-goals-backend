package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	strategyReadinessMaxOutputTokens = 12000
	strategyReadinessTimeout         = 4 * time.Minute
	strategyReadinessPollInterval    = 5 * time.Second
)

type ReadinessService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	ai               ai.Provider
	compactThreshold int
	wake             chan struct{}
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
	force bool,
) (StrategyReadinessQueueItem, error) {
	if state.Revision <= 0 || state.LastUserMessageID <= 0 {
		return StrategyReadinessQueueItem{}, fmt.Errorf("strategy_readiness_no_session")
	}
	item, err := s.store.QueueReadinessAudit(ctx, state, strategyID, force)
	if err != nil {
		return StrategyReadinessQueueItem{}, err
	}
	if !item.NotBefore.After(time.Now().Add(time.Second)) {
		s.signalWorker()
	}
	return item, nil
}

func (s *ReadinessService) ForceCurrent(ctx context.Context, workspaceID int, userID int) (StrategyReadinessQueueItem, error) {
	strategy, _, _, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return StrategyReadinessQueueItem{}, err
	}
	state, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return StrategyReadinessQueueItem{}, err
	}
	return s.QueueCandidate(ctx, state, strategy.ID, true)
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
	rawInput, err := json.Marshal(input)
	if err != nil {
		return err
	}

	started := time.Now()
	aiCtx := ai.WithScenario(ctx, run.WorkspaceID, 0, "strategy_readiness_auditor", StrategyReadinessPromptVersion)
	result, err := s.ai.GenerateJSONNative(aiCtx, strategyReadinessPrompt, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       synthesisVectorStoreIDs(session),
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
	report.Verdict = normalizeReadinessVerdict(report.Verdict)
	report.Confidence = normalizeReadinessConfidence(report.Confidence)
	if len(report.BlockingGaps) > 0 {
		report.Verdict = ReadinessVerdictNotReady
		report.CanSynthesize = false
	} else if report.Verdict == ReadinessVerdictReady {
		report.CanSynthesize = true
	} else if report.Verdict == ReadinessVerdictNotReady {
		report.CanSynthesize = false
	}

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
	for index := range report.CriteriaAssessment {
		report.CriteriaAssessment[index].SourceKeys = cleanSources(report.CriteriaAssessment[index].SourceKeys)
	}
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

	if report.CriteriaAssessment == nil {
		report.CriteriaAssessment = []StrategyReadinessCriterion{}
	}
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

func normalizeReadinessVerdict(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case ReadinessVerdictReady:
		return ReadinessVerdictReady
	case ReadinessVerdictConditionallyReady:
		return ReadinessVerdictConditionallyReady
	default:
		return ReadinessVerdictNotReady
	}
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
