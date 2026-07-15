package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	tacticsReadinessMaxOutputTokens = 14000
	tacticsReadinessTimeout         = 4 * time.Minute
	tacticsReadinessPollInterval    = 5 * time.Second
)

var tacticsReadinessWeights = map[string]int{
	"course_alignment":        15,
	"change_logic":            12,
	"causal_coherence":        12,
	"portfolio_focus":         10,
	"measurability":           10,
	"project_quality":         10,
	"feasibility_resources":   10,
	"risks_opportunities":     8,
	"dependencies_sequencing": 5,
	"evidence_assumptions":    5,
	"strategy_consistency":    3,
}

var tacticsReadinessCriterionOrder = []string{
	"course_alignment",
	"change_logic",
	"causal_coherence",
	"portfolio_focus",
	"measurability",
	"project_quality",
	"feasibility_resources",
	"risks_opportunities",
	"dependencies_sequencing",
	"evidence_assumptions",
	"strategy_consistency",
}

type TacticsReadinessService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	ai               *ai.OpenAIClient
	compactThreshold int
	wake             chan struct{}
}

type tacticsReadinessSource struct {
	Key        string `json:"key"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Label      string `json:"label"`
}

func NewTacticsReadinessService(dbx *sql.DB, aiClient *ai.OpenAIClient, compactThreshold int) *TacticsReadinessService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	return &TacticsReadinessService{
		store:            NewStore(dbx),
		memoryStore:      strategicmemory.NewStore(dbx),
		ai:               aiClient,
		compactThreshold: compactThreshold,
		wake:             make(chan struct{}, 1),
	}
}

func (s *TacticsReadinessService) StartWorker() {
	go func() {
		ticker := time.NewTicker(tacticsReadinessPollInterval)
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

func (s *TacticsReadinessService) Latest(ctx context.Context, workspaceID int) (TacticsReadinessResponse, error) {
	run, err := s.store.LatestTacticsReadinessAudit(ctx, workspaceID)
	if err != nil {
		return TacticsReadinessResponse{}, err
	}
	isCurrent, err := s.store.IsTacticsReadinessCurrent(ctx, workspaceID, run)
	if err != nil {
		return TacticsReadinessResponse{}, err
	}
	return TacticsReadinessResponse{Run: run, IsCurrent: isCurrent}, nil
}

func (s *TacticsReadinessService) QueueCandidate(
	ctx context.Context,
	state TacticsSessionState,
	plan TacticalPlan,
	force bool,
) (TacticsReadinessQueueItem, error) {
	if plan.ID <= 0 || plan.Revision <= 0 {
		return TacticsReadinessQueueItem{}, fmt.Errorf("tactics_readiness_no_plan")
	}
	if !force && (state.Revision <= 0 || state.LastUserMessageID <= 0) {
		return TacticsReadinessQueueItem{}, fmt.Errorf("tactics_readiness_no_session")
	}
	item, err := s.store.QueueTacticsReadinessAudit(ctx, state, plan, force)
	if err != nil {
		return TacticsReadinessQueueItem{}, err
	}
	if !item.NotBefore.After(time.Now().Add(time.Second)) {
		s.signalWorker()
	}
	return item, nil
}

func (s *TacticsReadinessService) ForceCurrent(ctx context.Context, workspaceID int, userID int) (TacticsReadinessQueueItem, error) {
	current, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return TacticsReadinessQueueItem{}, err
	}
	if current.TacticalPlan == nil || current.Strategy == nil || current.Course == nil {
		return TacticsReadinessQueueItem{}, fmt.Errorf("tactics_readiness_context_incomplete")
	}
	state, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return TacticsReadinessQueueItem{}, err
	}
	if state.LastUserID == nil {
		state.LastUserID = &userID
	}
	return s.QueueCandidate(ctx, state, *current.TacticalPlan, true)
}

func (s *TacticsReadinessService) signalWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *TacticsReadinessService) processDueAudits() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		run, err := s.store.ClaimDueTacticsReadinessAudit(ctx, s.ai.Model)
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

func (s *TacticsReadinessService) executeDetached(run TacticsReadinessRun) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), tacticsReadinessTimeout)
		defer cancel()
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer failureCancel()
				_ = s.store.FailTacticsReadinessAudit(failureCtx, run, time.Since(started).Milliseconds(), fmt.Sprintf("tactics readiness panic: %v", recovered))
			}
		}()
		if err := s.execute(ctx, run); err != nil {
			failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer failureCancel()
			_ = s.store.FailTacticsReadinessAudit(failureCtx, run, time.Since(started).Milliseconds(), err.Error())
		}
	}()
}

func (s *TacticsReadinessService) execute(ctx context.Context, run TacticsReadinessRun) error {
	userID := 0
	if run.CreatedBy != nil {
		userID = *run.CreatedBy
	}
	current, err := s.store.Current(ctx, run.WorkspaceID, userID)
	if err != nil {
		return err
	}
	if current.TacticalPlan == nil || current.TacticalPlan.ID != run.TacticalPlanID || current.Course == nil || current.Strategy == nil {
		return fmt.Errorf("tactics readiness context is no longer available")
	}
	state, err := s.store.SessionState(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	if state.Revision != run.SessionRevision || state.LastUserMessageID != run.ValidatedThroughMessageID || current.TacticalPlan.Revision != run.TacticalPlanRevision {
		return s.store.SupersedeTacticsReadinessAudit(ctx, run)
	}

	strategyDocs, err := s.store.StrategyDocuments(ctx, run.WorkspaceID, run.StrategyID)
	if err != nil {
		return err
	}
	knowledgeDocs, err := s.memoryStore.ListDocuments(ctx, run.WorkspaceID)
	if err != nil {
		return err
	}
	knowledgeQuality, err := s.memoryStore.LatestQualityReport(ctx, run.WorkspaceID)
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
	messages = tacticsMessagesThroughID(messages, run.ValidatedThroughMessageID)
	openAISession, err := s.memoryStore.OpenAISession(ctx, run.WorkspaceID, s.compactThreshold)
	if err != nil {
		return err
	}

	catalog, sourceIndex := buildTacticsReadinessSourceCatalog(current, strategyDocs, knowledgeDocs, messages, files)
	input := map[string]any{
		"output_stage":                 "independent_tactics_quality_and_readiness_audit",
		"session_revision":             run.SessionRevision,
		"tactical_plan_revision":       run.TacticalPlanRevision,
		"validated_through_message_id": run.ValidatedThroughMessageID,
		"active_strategy":              current.Strategy,
		"strategy_documents":           strategyDocs,
		"active_course":                current.Course,
		"knowledge_base": map[string]any{
			"documents":      knowledgeDocs,
			"quality_report": knowledgeQuality,
		},
		"tactical_system": map[string]any{
			"plan":        current.TacticalPlan,
			"workstreams": current.Workstreams,
			"uncovered":   current.Uncovered,
		},
		"facilitator_assessment": map[string]any{
			"status":                 state.FacilitatorStatus,
			"reason":                 state.StatusReason,
			"current_focus":          state.CurrentFocus,
			"decisions":              state.Decisions,
			"open_questions":         state.OpenQuestions,
			"needs_strategy_review":  state.NeedsStrategyReview,
			"strategy_review_reason": state.StrategyReviewReason,
		},
		"tactical_session_transcript": messages,
		"uploaded_files":              files,
		"source_catalog":              catalog,
	}
	rawInput, err := json.Marshal(input)
	if err != nil {
		return err
	}

	vectorStoreIDs := []string{}
	if strings.TrimSpace(openAISession.VectorStoreID) != "" {
		vectorStoreIDs = append(vectorStoreIDs, strings.TrimSpace(openAISession.VectorStoreID))
	}
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, tacticsReadinessPrompt, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     openAISession.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-tactics-readiness-workspace-%d-v1", run.WorkspaceID),
		MaxFileSearchResults: 20,
		MaxOutputTokens:      tacticsReadinessMaxOutputTokens,
		RequestTimeout:       tacticsReadinessTimeout - 15*time.Second,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "tactics_readiness_auditor", s.ai.Model, TacticsReadinessPromptVersion, duration, 0, 0, "failed", err.Error())
		return err
	}

	var report TacticsReadinessReport
	if err := json.Unmarshal([]byte(result.Text), &report); err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "tactics_readiness_auditor", s.ai.Model, TacticsReadinessPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return fmt.Errorf("tactics readiness json decode error: %w", err)
	}
	report = normalizeTacticsReadinessReport(report, run, sourceIndex)
	_, err = s.store.CompleteTacticsReadinessAudit(ctx, run, report, result.Usage.InputTokens, result.Usage.OutputTokens, duration)
	if err != nil {
		return err
	}
	s.memoryStore.LogAIRunWithUsage(ctx, run.WorkspaceID, "tactics_readiness_auditor", s.ai.Model, TacticsReadinessPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return nil
}

func tacticsMessagesThroughID(messages []TacticsChatMessage, throughID int) []TacticsChatMessage {
	if throughID <= 0 {
		return messages
	}
	result := make([]TacticsChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.ID <= throughID {
			result = append(result, message)
		}
	}
	return result
}

func buildTacticsReadinessSourceCatalog(
	current CurrentResponse,
	strategyDocs []TacticsStrategyDocument,
	knowledgeDocs []strategicmemory.StrategicDocument,
	messages []TacticsChatMessage,
	files []strategicmemory.StrategicFile,
) ([]tacticsReadinessSource, map[string]tacticsReadinessSource) {
	items := []tacticsReadinessSource{}
	add := func(item tacticsReadinessSource) {
		if strings.TrimSpace(item.Key) == "" {
			return
		}
		items = append(items, item)
	}
	if current.Strategy != nil {
		add(tacticsReadinessSource{Key: fmt.Sprintf("strategy:%d", current.Strategy.ID), SourceType: "strategy", SourceID: strconv.Itoa(current.Strategy.ID), Label: current.Strategy.Title})
	}
	if current.Course != nil {
		add(tacticsReadinessSource{Key: fmt.Sprintf("course:%d", current.Course.ID), SourceType: "course", SourceID: strconv.Itoa(current.Course.ID), Label: current.Course.Title})
	}
	if current.TacticalPlan != nil {
		add(tacticsReadinessSource{Key: fmt.Sprintf("plan:%d", current.TacticalPlan.ID), SourceType: "tactical_plan", SourceID: strconv.Itoa(current.TacticalPlan.ID), Label: current.TacticalPlan.Title})
	}
	for _, doc := range strategyDocs {
		add(tacticsReadinessSource{Key: "strategy_document:" + doc.DocumentType, SourceType: "strategy_document", SourceID: doc.DocumentType, Label: doc.Title})
	}
	for _, doc := range knowledgeDocs {
		add(tacticsReadinessSource{Key: "knowledge:" + doc.DocumentType, SourceType: "knowledge_document", SourceID: strconv.Itoa(doc.ID), Label: doc.Title})
	}
	for _, message := range messages {
		add(tacticsReadinessSource{Key: fmt.Sprintf("message:%d", message.ID), SourceType: "tactics_message", SourceID: strconv.Itoa(message.ID), Label: message.Role + " message"})
	}
	for _, file := range files {
		add(tacticsReadinessSource{Key: fmt.Sprintf("file:%d", file.ID), SourceType: "uploaded_file", SourceID: strconv.Itoa(file.ID), Label: file.Filename})
	}
	for _, workstream := range current.Workstreams {
		add(tacticsReadinessSource{Key: fmt.Sprintf("workstream:%d", workstream.ID), SourceType: "workstream", SourceID: strconv.Itoa(workstream.ID), Label: workstream.Title})
		for _, project := range workstream.Projects {
			add(tacticsReadinessSource{Key: fmt.Sprintf("project:%d", project.ID), SourceType: "project", SourceID: strconv.Itoa(project.ID), Label: project.Title})
		}
		for _, risk := range workstream.Risks {
			add(tacticsReadinessSource{Key: fmt.Sprintf("risk:%d", risk.ID), SourceType: "risk", SourceID: strconv.Itoa(risk.ID), Label: risk.Title})
		}
		for _, opportunity := range workstream.Opportunities {
			add(tacticsReadinessSource{Key: fmt.Sprintf("opportunity:%d", opportunity.ID), SourceType: "opportunity", SourceID: strconv.Itoa(opportunity.ID), Label: opportunity.Title})
		}
	}
	for _, risk := range current.Uncovered.Risks {
		add(tacticsReadinessSource{Key: fmt.Sprintf("risk:%d", risk.ID), SourceType: "risk", SourceID: strconv.Itoa(risk.ID), Label: risk.Title})
	}
	for _, opportunity := range current.Uncovered.Opportunities {
		add(tacticsReadinessSource{Key: fmt.Sprintf("opportunity:%d", opportunity.ID), SourceType: "opportunity", SourceID: strconv.Itoa(opportunity.ID), Label: opportunity.Title})
	}
	index := make(map[string]tacticsReadinessSource, len(items))
	deduplicated := make([]tacticsReadinessSource, 0, len(items))
	for _, item := range items {
		if _, exists := index[item.Key]; exists {
			continue
		}
		index[item.Key] = item
		deduplicated = append(deduplicated, item)
	}
	return deduplicated, index
}

func normalizeTacticsReadinessReport(
	report TacticsReadinessReport,
	run TacticsReadinessRun,
	sourceIndex map[string]tacticsReadinessSource,
) TacticsReadinessReport {
	report.SessionRevision = run.SessionRevision
	report.TacticalPlanRevision = run.TacticalPlanRevision
	report.ValidatedThroughMessageID = run.ValidatedThroughMessageID
	report.Confidence = normalizeTacticsReadinessConfidence(report.Confidence)
	report.ExecutiveSummary = strings.TrimSpace(report.ExecutiveSummary)

	cleanSources := func(values []string) []string {
		result := []string{}
		seen := map[string]bool{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if _, ok := sourceIndex[value]; !ok || seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, value)
		}
		return result
	}

	criteriaByCode := map[string]TacticsReadinessCriterion{}
	for _, criterion := range report.CriteriaAssessment {
		code := strings.TrimSpace(strings.ToLower(criterion.CriterionCode))
		if _, ok := tacticsReadinessWeights[code]; !ok {
			continue
		}
		criterion.CriterionCode = code
		criterion.Score = clampScore(criterion.Score)
		criterion.Strengths = cleanTacticsStrings(criterion.Strengths, 12)
		criterion.Gaps = cleanTacticsStrings(criterion.Gaps, 12)
		criterion.SourceKeys = cleanSources(criterion.SourceKeys)
		criteriaByCode[code] = criterion
	}
	report.CriteriaAssessment = make([]TacticsReadinessCriterion, 0, len(tacticsReadinessCriterionOrder))
	weightedScore := 0
	for _, code := range tacticsReadinessCriterionOrder {
		criterion, ok := criteriaByCode[code]
		if !ok {
			criterion = TacticsReadinessCriterion{
				CriterionCode: code,
				Score:         0,
				Assessment:    "The auditor did not return an assessment for this required criterion.",
				Strengths:     []string{},
				Gaps:          []string{"Required criterion was not assessed."},
				SourceKeys:    []string{},
			}
		}
		weightedScore += criterion.Score * tacticsReadinessWeights[code]
		report.CriteriaAssessment = append(report.CriteriaAssessment, criterion)
	}
	report.OverallScore = weightedScore / 100

	for index := range report.CourseCoverage {
		report.CourseCoverage[index].SourceKeys = cleanSources(report.CourseCoverage[index].SourceKeys)
	}
	for index := range report.EntityAssessments {
		report.EntityAssessments[index].SourceKeys = cleanSources(report.EntityAssessments[index].SourceKeys)
	}
	cleanIssues := func(items []TacticsReadinessIssue) []TacticsReadinessIssue {
		if items == nil {
			return []TacticsReadinessIssue{}
		}
		for index := range items {
			items[index].SourceKeys = cleanSources(items[index].SourceKeys)
		}
		return items
	}
	report.BlockingGaps = cleanIssues(report.BlockingGaps)
	report.WeakZones = cleanIssues(report.WeakZones)
	report.Contradictions = cleanIssues(report.Contradictions)
	for index := range report.CriticalAssumptions {
		report.CriticalAssumptions[index].SourceKeys = cleanSources(report.CriticalAssumptions[index].SourceKeys)
	}
	for index := range report.RedundantOrMisalignedItems {
		report.RedundantOrMisalignedItems[index].SourceKeys = cleanSources(report.RedundantOrMisalignedItems[index].SourceKeys)
	}
	for index := range report.AdditionalPerspectives {
		report.AdditionalPerspectives[index].SourceKeys = cleanSources(report.AdditionalPerspectives[index].SourceKeys)
	}

	if report.CourseCoverage == nil {
		report.CourseCoverage = []TacticsCourseCoverage{}
	}
	if report.EntityAssessments == nil {
		report.EntityAssessments = []TacticsEntityAssessment{}
	}
	if report.CriticalAssumptions == nil {
		report.CriticalAssumptions = []TacticsReadinessAssumption{}
	}
	if report.RedundantOrMisalignedItems == nil {
		report.RedundantOrMisalignedItems = []TacticsMisalignedInitiative{}
	}
	if report.AdditionalPerspectives == nil {
		report.AdditionalPerspectives = []TacticsReadinessPerspective{}
	}
	report.FacilitatorGuidance = normalizeTacticsFacilitatorGuidance(report.FacilitatorGuidance)
	if len(report.FacilitatorGuidance) == 0 {
		report.FacilitatorGuidance = defaultTacticsFacilitatorGuidance(report)
	}
	report.ActivationGuidance.ConditionsToActivate = cleanTacticsStrings(report.ActivationGuidance.ConditionsToActivate, 20)
	report.ActivationGuidance.WarningsToPreserve = cleanTacticsStrings(report.ActivationGuidance.WarningsToPreserve, 20)
	report.ActivationGuidance.FirstReviewSignals = cleanTacticsStrings(report.ActivationGuidance.FirstReviewSignals, 20)
	report.StrategyReviewReason = strings.TrimSpace(report.StrategyReviewReason)

	switch {
	case len(report.BlockingGaps) > 0 || report.OverallScore < 65:
		report.Verdict = TacticsReadinessVerdictNotReady
		report.CanActivate = false
	case report.OverallScore < 80:
		report.Verdict = TacticsReadinessVerdictConditionallyReady
		report.CanActivate = true
	default:
		report.Verdict = TacticsReadinessVerdictReady
		report.CanActivate = true
	}
	if report.Verdict == TacticsReadinessVerdictReady {
		for index := range report.AdditionalPerspectives {
			report.AdditionalPerspectives[index].IsBlocking = false
		}
	}
	return report
}

func normalizeTacticsFacilitatorGuidance(items []TacticsReadinessFacilitatorGuide) []TacticsReadinessFacilitatorGuide {
	result := []TacticsReadinessFacilitatorGuide{}
	for _, item := range items {
		item.Priority = strings.TrimSpace(strings.ToLower(item.Priority))
		if item.Priority != "high" && item.Priority != "low" {
			item.Priority = "medium"
		}
		item.Area = strings.TrimSpace(item.Area)
		item.ResearchGoal = strings.TrimSpace(item.ResearchGoal)
		item.WhyItMatters = strings.TrimSpace(item.WhyItMatters)
		item.ContextToCarry = strings.TrimSpace(item.ContextToCarry)
		if item.Area == "" && item.ResearchGoal == "" && item.ContextToCarry == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func defaultTacticsFacilitatorGuidance(report TacticsReadinessReport) []TacticsReadinessFacilitatorGuide {
	if report.CanActivate {
		return []TacticsReadinessFacilitatorGuide{{
			Priority:       "low",
			Area:           "tactical integrity",
			ResearchGoal:   "Preserve the explicit conditions, assumptions, and review signals when the plan is activated.",
			WhyItMatters:   "The tactic is usable, but it should remain a learning system rather than a static list of initiatives.",
			ContextToCarry: report.ExecutiveSummary,
			Blocking:       false,
		}}
	}
	return []TacticsReadinessFacilitatorGuide{{
		Priority:       "high",
		Area:           "tactics readiness",
		ResearchGoal:   "Resolve the highest-impact blocking gap before requesting another independent audit.",
		WhyItMatters:   "The current system of changes is not yet reliable enough to activate.",
		ContextToCarry: report.ExecutiveSummary,
		Blocking:       true,
	}}
}

func normalizeTacticsReadinessConfidence(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "high", "low":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return "medium"
	}
}

func clampScore(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
