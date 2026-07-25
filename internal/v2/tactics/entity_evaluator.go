package tactics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	entityEvaluationPollInterval = 3 * time.Second
	entityEvaluationTimeout      = 90 * time.Second
)

type TacticalEntityEvaluatorService struct {
	store  *Store
	memory *strategicmemory.Store
	ai     ai.Provider
	wake   chan struct{}
	slots  chan struct{}
}

func NewTacticalEntityEvaluatorService(dbx *sql.DB, aiClient ai.Provider) *TacticalEntityEvaluatorService {
	return &TacticalEntityEvaluatorService{
		store: NewStore(dbx), memory: strategicmemory.NewStore(dbx), ai: aiClient,
		wake: make(chan struct{}, 1), slots: make(chan struct{}, 2),
	}
}

func (s *TacticalEntityEvaluatorService) StartWorker() {
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.store.RecoverStaleEntityEvaluations(recoveryCtx)
	recoveryCancel()
	go func() {
		ticker := time.NewTicker(entityEvaluationPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-s.wake:
			}
			s.processDue()
		}
	}()
}

func (s *TacticalEntityEvaluatorService) Queue(ctx context.Context, workspaceID, userID int, entityType string, entityID int) (string, error) {
	status, err := s.store.QueueEntityEvaluation(ctx, workspaceID, userID, entityType, entityID)
	if err != nil {
		return "", err
	}
	if status == entityEvaluationQueued {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return status, nil
}

func (s *TacticalEntityEvaluatorService) processDue() {
	for {
		select {
		case s.slots <- struct{}{}:
		default:
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		job, err := s.store.ClaimDueEntityEvaluation(ctx)
		cancel()
		if errors.Is(err, sql.ErrNoRows) {
			<-s.slots
			return
		}
		if err != nil {
			<-s.slots
			return
		}
		go s.executeDetached(job)
	}
}

func (s *TacticalEntityEvaluatorService) executeDetached(job TacticalEntityEvaluationJob) {
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), entityEvaluationTimeout)
	defer cancel()
	if err := s.execute(ctx, job); err != nil {
		failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer failureCancel()
		_ = s.store.FailEntityEvaluationJob(failureCtx, job, err.Error())
	}
}

func (s *TacticalEntityEvaluatorService) execute(ctx context.Context, job TacticalEntityEvaluationJob) error {
	input, fingerprint, err := s.buildEvaluationInput(ctx, job)
	if err != nil {
		return err
	}
	latest, _, err := s.store.LatestEntityEvaluation(ctx, job.WorkspaceID, job.EntityType, job.EntityID)
	if err != nil {
		return err
	}
	if latest != nil && latest.ContextFingerprint == fingerprint {
		return s.store.CompleteEntityEvaluationJob(ctx, job)
	}
	rawInput, _ := json.Marshal(input)
	scenario := "tactical_" + job.EntityType + "_evaluator"
	aiCtx := ai.WithScenario(ctx, job.WorkspaceID, 0, scenario, tacticalEntityEvaluatorPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, tacticalEntityEvaluatorPrompt, string(rawInput), ai.ResponseContextOptions{
		PromptCacheKey:  fmt.Sprintf("reupgoals-tactical-entity-evaluator-%s-v1", job.EntityType),
		MaxOutputTokens: 600,
		RequestTimeout:  entityEvaluationTimeout,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, scenario, s.ai.ModelName(), tacticalEntityEvaluatorPromptVersion, duration, 0, 0, "failed", err.Error())
		return err
	}
	output, err := parseTacticalEntityEvaluatorOutput(result.Text)
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, scenario, s.ai.ModelName(), tacticalEntityEvaluatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return err
	}
	score := calculateTacticalEntityPriority(output)
	tier := tacticalEntityPriorityTier(score)
	if err := s.store.SaveEntityEvaluation(ctx, job, s.ai.ModelName(), output, score, tier, fingerprint, result.Usage.InputTokens, result.Usage.OutputTokens, duration); err != nil {
		return err
	}
	if err := s.store.CompleteEntityEvaluationJob(ctx, job); err != nil {
		return err
	}
	s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, scenario, s.ai.ModelName(), tacticalEntityEvaluatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return nil
}

func (s *TacticalEntityEvaluatorService) buildEvaluationInput(ctx context.Context, job TacticalEntityEvaluationJob) (map[string]any, string, error) {
	var strategy struct {
		ID        int
		Title     string
		Summary   string
		Version   int
		Status    string
		UpdatedAt time.Time
	}
	err := s.store.dbx.QueryRowContext(ctx, `
		SELECT id, title, summary, version, status, updated_at
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, id DESC
		LIMIT 1
	`, job.WorkspaceID).Scan(&strategy.ID, &strategy.Title, &strategy.Summary, &strategy.Version, &strategy.Status, &strategy.UpdatedAt)
	if err != nil {
		return nil, "", err
	}

	var entity any
	var parent any
	switch job.EntityType {
	case EntityWorkstream:
		workstream, err := s.store.WorkstreamDetail(ctx, job.WorkspaceID, job.EntityID)
		if err != nil {
			return nil, "", err
		}
		entity = workstreamEvaluationContext(workstream)
	case EntityProject:
		project, workstream, err := s.store.ProjectDetail(ctx, job.WorkspaceID, job.EntityID)
		if err != nil {
			return nil, "", err
		}
		entity = projectEvaluationContext(project)
		parent = workstreamEvaluationContext(workstream)
	default:
		return nil, "", errors.New("invalid_tactical_evaluation_entity")
	}

	companyContext := map[string]any{}
	snapshot, err := s.memory.LatestSnapshot(ctx, job.WorkspaceID)
	if err != nil {
		return nil, "", err
	}
	if snapshot != nil {
		companyContext["business_stage"] = snapshot.BusinessStage
		companyContext["snapshot_excerpt"] = truncateTacticalText(string(snapshot.Snapshot), 3500)
	}
	input := map[string]any{
		"entity_type": job.EntityType,
		"entity":      entity,
		"active_long_term_strategy": map[string]any{
			"id": strategy.ID, "title": strategy.Title,
			"summary": truncateTacticalText(strategy.Summary, 2200),
			"version": strategy.Version, "updated_at": strategy.UpdatedAt,
		},
		"parent_tactical_context": parent,
		"company_context":         companyContext,
	}
	raw, _ := json.Marshal(input)
	hash := sha256.Sum256(raw)
	return input, hex.EncodeToString(hash[:]), nil
}

func workstreamEvaluationContext(workstream Workstream) map[string]any {
	return map[string]any{
		"id": workstream.ID, "title": workstream.Title,
		"description": workstream.Description, "goal": workstream.Goal,
		"ckp": workstream.CKP, "reason": workstream.Reason,
		"closes_risk": workstream.ClosesRisk, "metrics": workstream.Metrics,
		"status": workstream.Status, "contribution_type": workstream.ContributionType,
		"confidence": workstream.Confidence,
	}
}

func projectEvaluationContext(project Project) map[string]any {
	return map[string]any{
		"id": project.ID, "workstream_id": project.WorkstreamID,
		"title": project.Title, "description": project.Description,
		"why_needed": project.WhyNeeded, "success_criteria": project.SuccessCriteria,
		"failure_criteria": project.FailureCriteria, "metric_name": project.MetricName,
		"expected_value": project.ExpectedValue, "status": project.Status,
		"confidence": project.Confidence,
	}
}

func parseTacticalEntityEvaluatorOutput(raw string) (tacticalEntityEvaluatorOutput, error) {
	var output tacticalEntityEvaluatorOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return tacticalEntityEvaluatorOutput{}, err
	}
	output.StrategicRelevance = clampTacticalScore(output.StrategicRelevance)
	output.ExpectedImpact = clampTacticalScore(output.ExpectedImpact)
	output.Clarity = clampTacticalScore(output.Clarity)
	output.Feasibility = clampTacticalScore(output.Feasibility)
	output.Measurability = clampTacticalScore(output.Measurability)
	output.Confidence = clampTacticalScore(output.Confidence)
	output.PriorityReason = strings.TrimSpace(output.PriorityReason)
	if output.PriorityReason == "" {
		return tacticalEntityEvaluatorOutput{}, errors.New("empty_tactical_entity_priority_reason")
	}
	missing := make([]string, 0, len(output.MissingInformation))
	for _, item := range output.MissingInformation {
		if clean := strings.TrimSpace(item); clean != "" {
			missing = append(missing, clean)
		}
	}
	output.MissingInformation = missing
	return output, nil
}

func calculateTacticalEntityPriority(output tacticalEntityEvaluatorOutput) int {
	base := float64(output.StrategicRelevance)*0.40 +
		float64(output.ExpectedImpact)*0.20 +
		float64(output.Clarity)*0.15 +
		float64(output.Feasibility)*0.15 +
		float64(output.Measurability)*0.10
	confidenceFactor := 0.70 + 0.30*(float64(output.Confidence)/1000)
	return clampTacticalScore(int(math.Round(base * confidenceFactor)))
}

func tacticalEntityPriorityTier(score int) string {
	switch {
	case score >= 850:
		return "P1"
	case score >= 700:
		return "P2"
	case score >= 550:
		return "P3"
	case score >= 400:
		return "P4"
	default:
		return "P5"
	}
}

func clampTacticalScore(value int) int {
	if value < 0 {
		return 0
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func truncateTacticalText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

type tacticalEntityEvaluatorOutput struct {
	StrategicRelevance int      `json:"strategic_relevance"`
	ExpectedImpact     int      `json:"expected_impact"`
	Clarity            int      `json:"clarity"`
	Feasibility        int      `json:"feasibility"`
	Measurability      int      `json:"measurability"`
	Confidence         int      `json:"confidence"`
	PriorityReason     string   `json:"priority_reason"`
	MissingInformation []string `json:"missing_information"`
}
