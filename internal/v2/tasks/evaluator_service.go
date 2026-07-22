package tasks

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
	taskEvaluationPollInterval = 3 * time.Second
	taskEvaluationTimeout      = 2 * time.Minute
)

type TaskEvaluatorService struct {
	store  *Store
	memory *strategicmemory.Store
	ai     ai.Provider
	wake   chan struct{}
	slots  chan struct{}
}

func NewTaskEvaluatorService(dbx *sql.DB, aiClient ai.Provider) *TaskEvaluatorService {
	return &TaskEvaluatorService{
		store:  NewStore(dbx),
		memory: strategicmemory.NewStore(dbx), ai: aiClient, wake: make(chan struct{}, 1),
		slots: make(chan struct{}, 3),
	}
}

func (s *TaskEvaluatorService) StartWorker() {
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.store.RecoverStaleTaskEvaluations(recoveryCtx)
	recoveryCancel()
	go func() {
		ticker := time.NewTicker(taskEvaluationPollInterval)
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

func (s *TaskEvaluatorService) Queue(ctx context.Context, workspaceID int, userID int, taskID int, force bool) error {
	if err := s.store.QueueTaskEvaluation(ctx, workspaceID, userID, taskID, force); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *TaskEvaluatorService) processDue() {
	for {
		select {
		case s.slots <- struct{}{}:
		default:
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		job, err := s.store.ClaimDueTaskEvaluation(ctx)
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

func (s *TaskEvaluatorService) executeDetached(job TaskEvaluationJob) {
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), taskEvaluationTimeout)
	defer cancel()
	if err := s.execute(ctx, job); err != nil {
		failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer failureCancel()
		_ = s.store.FailTaskEvaluationJob(failureCtx, job, err.Error())
	}
}

func (s *TaskEvaluatorService) execute(ctx context.Context, job TaskEvaluationJob) error {
	task, err := s.store.Get(ctx, job.WorkspaceID, job.TaskID)
	if err != nil {
		return err
	}
	if task.Status == StatusArchived {
		return s.store.CompleteTaskEvaluationJob(ctx, job.ID)
	}
	input, fingerprint, err := s.buildEvaluationInput(ctx, job.WorkspaceID, task)
	if err != nil {
		return err
	}
	rawInput, _ := json.Marshal(input)
	aiCtx := ai.WithScenario(ctx, job.WorkspaceID, 0, "task_evaluator_v2", taskEvaluatorPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, taskEvaluatorPrompt, string(rawInput), ai.ResponseContextOptions{
		PromptCacheKey:  fmt.Sprintf("reupgoals-task-evaluator-workspace-%d-v5", job.WorkspaceID),
		MaxOutputTokens: 900, RequestTimeout: taskEvaluationTimeout,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, "task_evaluator_v2", s.ai.ModelName(), taskEvaluatorPromptVersion, duration, 0, 0, "failed", err.Error())
		return err
	}
	output, err := parseTaskEvaluatorOutput(result.Text)
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, "task_evaluator_v2", s.ai.ModelName(), taskEvaluatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return err
	}
	score := CalculateTaskPriority(output)
	tier := PriorityTier(score)
	if err := s.store.SaveTaskEvaluation(
		ctx, job, s.ai.ModelName(), output, score, tier, fingerprint,
		result.Usage.InputTokens, result.Usage.OutputTokens, duration,
	); err != nil {
		return err
	}
	if err := s.store.CompleteTaskEvaluationJob(ctx, job.ID); err != nil {
		return err
	}
	s.memory.LogAIRunWithUsage(ctx, job.WorkspaceID, "task_evaluator_v2", s.ai.ModelName(), taskEvaluatorPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return nil
}

func (s *TaskEvaluatorService) buildEvaluationInput(ctx context.Context, workspaceID int, task Task) (map[string]any, string, error) {
	state, err := s.store.Workstream(ctx, workspaceID, task.WorkstreamID)
	if err != nil {
		return nil, "", err
	}
	if state.Course == nil || state.Workstream == nil || state.TacticalPlan == nil || task.ProjectID == nil {
		return nil, "", ErrInvalidInput
	}

	var project *Project
	for index := range state.Projects {
		if state.Projects[index].ID == *task.ProjectID {
			copy := state.Projects[index]
			project = &copy
			break
		}
	}
	if project == nil {
		return nil, "", ErrForbidden
	}

	var strategyTitle string
	var strategySummary string
	err = s.store.dbx.QueryRowContext(ctx, `
		SELECT title, summary
		FROM v2_strategies
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, state.TacticalPlan.StrategyID, workspaceID).Scan(&strategyTitle, &strategySummary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}

	businessStage := ""
	businessSnapshot := ""
	snapshot, err := s.memory.LatestSnapshot(ctx, workspaceID)
	if err != nil {
		return nil, "", err
	}
	if snapshot != nil {
		businessStage = snapshot.BusinessStage
		businessSnapshot = truncateRunes(string(snapshot.Snapshot), 3500)
	}

	workstream := *state.Workstream
	workstream.Projects = nil
	workstream.Risks = nil
	workstream.Opportunities = nil
	workstream.TopTasks = nil

	siblings := make([]taskContextItem, 0, 20)
	for _, candidate := range state.Tasks {
		if candidate.ID == task.ID || candidate.ProjectID == nil || *candidate.ProjectID != *task.ProjectID {
			continue
		}
		siblings = append(siblings, taskContextItem{
			ID: candidate.ID, ProjectID: candidate.ProjectID, Title: candidate.Title,
			Description: truncateRunes(candidate.Description, 320), ExpectedResult: truncateRunes(candidate.ExpectedResult, 240),
			Status: candidate.Status,
		})
		if len(siblings) == 20 {
			break
		}
	}

	input := map[string]any{
		"task": map[string]any{
			"id": task.ID, "title": task.Title, "description": task.Description,
			"expected_result": task.ExpectedResult,
			"blocking_tasks":  task.BlockingTasks,
		},
		"global_company_goal": map[string]any{
			"strategy_title": strategyTitle, "strategy_summary": truncateRunes(strategySummary, 1200),
			"course_direction": state.Course.Direction, "strategic_goal": state.Course.StrategicGoal,
			"key_metric": state.Course.KeyMetric, "success_criterion": state.Course.SuccessCriterion,
		},
		"company_context": map[string]any{
			"business_stage": businessStage, "snapshot_excerpt": businessSnapshot,
		},
		"tactical_direction": workstream,
		"project_context":    project,
		"sibling_tasks":      siblings,
	}
	raw, _ := json.Marshal(input)
	hash := sha256.Sum256(raw)
	return input, hex.EncodeToString(hash[:]), nil
}

func parseTaskEvaluatorOutput(raw string) (taskEvaluatorModelOutput, error) {
	var output taskEvaluatorModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return taskEvaluatorModelOutput{}, err
	}
	output.StrategicRelevance = clampScore(output.StrategicRelevance)
	output.CourseAlignment = clampScore(output.CourseAlignment)
	output.TacticalAlignment = clampScore(output.TacticalAlignment)
	output.ExpectedImpact = clampScore(output.ExpectedImpact)
	output.Urgency = clampScore(output.Urgency)
	output.Effort = clampScore(output.Effort)
	output.Confidence = clampScore(output.Confidence)
	output.Recommendation = strings.ToLower(strings.TrimSpace(output.Recommendation))
	switch output.Recommendation {
	case RecommendationKeep, RecommendationClarify, RecommendationRework, RecommendationRemove:
	default:
		output.Recommendation = RecommendationClarify
	}
	output.PriorityReason = strings.TrimSpace(output.PriorityReason)
	output.ClarificationQuestion = strings.TrimSpace(output.ClarificationQuestion)
	cleanMissing := []string{}
	for _, item := range output.MissingInformation {
		item = strings.TrimSpace(item)
		if item != "" {
			cleanMissing = append(cleanMissing, item)
		}
	}
	output.MissingInformation = cleanMissing
	output.Flags = normalizeTaskFlags(output)
	output.BacklogCategory = normalizeBacklogCategory(output.BacklogCategory)
	if output.BacklogCategory == "" {
		switch output.Recommendation {
		case RecommendationRemove:
			output.BacklogCategory = BacklogRecommendedDelete
		case RecommendationClarify, RecommendationRework:
			output.BacklogCategory = BacklogQuestionable
		}
	}
	if output.PriorityReason == "" {
		return taskEvaluatorModelOutput{}, fmt.Errorf("empty_task_priority_reason")
	}
	return output, nil
}

func normalizeTaskFlags(output taskEvaluatorModelOutput) []string {
	allowed := map[string]bool{
		TaskFlagWeakStrategyLink:   true,
		TaskFlagLowImpact:          true,
		TaskFlagHighEffort:         true,
		TaskFlagDuplicate:          true,
		TaskFlagNeedsClarification: true,
	}
	seen := map[string]bool{}
	flags := make([]string, 0, len(output.Flags)+3)
	add := func(flag string) {
		flag = strings.ToLower(strings.TrimSpace(flag))
		if allowed[flag] && !seen[flag] {
			seen[flag] = true
			flags = append(flags, flag)
		}
	}
	for _, flag := range output.Flags {
		add(flag)
	}
	if output.StrategicRelevance < 500 || output.CourseAlignment < 500 || output.TacticalAlignment < 500 {
		add(TaskFlagWeakStrategyLink)
	}
	if output.ExpectedImpact < 400 {
		add(TaskFlagLowImpact)
	}
	if output.Effort >= 750 {
		add(TaskFlagHighEffort)
	}
	if output.Recommendation == RecommendationClarify || len(output.MissingInformation) > 0 {
		add(TaskFlagNeedsClarification)
	}
	return flags
}

func normalizeBacklogCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case BacklogFutureStage, BacklogQuestionable, BacklogRecommendedDelete:
		return value
	default:
		return ""
	}
}

func CalculateTaskPriority(output taskEvaluatorModelOutput) int {
	alignment := float64(output.StrategicRelevance)*0.45 +
		float64(output.CourseAlignment)*0.35 +
		float64(output.TacticalAlignment)*0.20
	gross := alignment*0.55 +
		float64(output.ExpectedImpact)*0.25 +
		float64(output.Urgency)*0.10 +
		float64(output.Confidence)*0.1
	score := int(math.Round(gross - float64(output.Effort)*0.15))
	if output.Recommendation == RecommendationRemove && score > 250 {
		score = 250
	}
	if output.Recommendation == RecommendationRework && score > 650 {
		score = 650
	}
	return clampScore(score)
}

func PriorityTier(score int) string {
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

func clampScore(value int) int {
	if value < 0 {
		return 0
	}
	if value > 1000 {
		return 1000
	}
	return value
}
