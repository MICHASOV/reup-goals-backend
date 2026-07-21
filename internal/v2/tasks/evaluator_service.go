package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	taskEvaluationPollInterval = 3 * time.Second
	taskEvaluationTimeout      = 2 * time.Minute
)

type TaskEvaluatorService struct {
	store        *Store
	context      *taskContextBuilder
	memory       *strategicmemory.Store
	ai           ai.Provider
	wake         chan struct{}
	slots        chan struct{}
	contextIndex *contextindex.Service
}

func (s *TaskEvaluatorService) SetContextIndex(index *contextindex.Service) {
	s.contextIndex = index
}

func NewTaskEvaluatorService(dbx *sql.DB, aiClient ai.Provider) *TaskEvaluatorService {
	return &TaskEvaluatorService{
		store: NewStore(dbx), context: newTaskContextBuilder(dbx),
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
	pack, vectorStoreIDs, fingerprint, err := s.context.Build(ctx, job.WorkspaceID, task.WorkstreamID, 0)
	if err != nil {
		return err
	}
	var target *taskContextItem
	for i := range pack.ExistingTasks {
		if pack.ExistingTasks[i].ID == task.ID {
			pack.ExistingTasks[i].EffectivePriorityScore = 0
			pack.ExistingTasks[i].EffectivePriorityTier = ""
			pack.ExistingTasks[i].Recommendation = ""
			copy := pack.ExistingTasks[i]
			target = &copy
			break
		}
	}
	if target == nil {
		return ErrForbidden
	}
	input := map[string]any{"task": target, "context": pack}
	if s.contextIndex != nil {
		indexedIDs, indexErr := s.contextIndex.Ensure(ctx, job.WorkspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
			input = map[string]any{
				"task":                      target,
				"active_workstream_id":      task.WorkstreamID,
				"current_workspace_context": "Use file_search to evaluate this task against the current strategy, course, tactics, related entities, constraints, and existing tasks.",
			}
		}
	}
	rawInput, _ := json.Marshal(input)
	aiCtx := ai.WithScenario(ctx, job.WorkspaceID, 0, "task_evaluator_v2", taskEvaluatorPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, taskEvaluatorPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs: vectorStoreIDs, MaxFileSearchResults: 10,
		PromptCacheKey:  fmt.Sprintf("reupgoals-task-evaluator-workspace-%d-v2", job.WorkspaceID),
		MaxOutputTokens: 4000, RequestTimeout: taskEvaluationTimeout,
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
	if output.StrategicRelevance < 50 || output.CourseAlignment < 50 || output.TacticalAlignment < 50 {
		add(TaskFlagWeakStrategyLink)
	}
	if output.ExpectedImpact < 40 {
		add(TaskFlagLowImpact)
	}
	if output.Effort >= 75 {
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
	alignment := float64(output.StrategicRelevance)*0.4 +
		float64(output.CourseAlignment)*0.25 +
		float64(output.TacticalAlignment)*0.35
	gross := alignment*0.4 +
		float64(output.ExpectedImpact)*0.3 +
		float64(output.Urgency)*0.2 +
		float64(output.Confidence)*0.1
	score := int(math.Round(gross - float64(output.Effort)*0.15))
	if output.Recommendation == RecommendationRemove && score > 25 {
		score = 25
	}
	if output.Recommendation == RecommendationRework && score > 65 {
		score = 65
	}
	return clampScore(score)
}

func PriorityTier(score int) string {
	switch {
	case score >= 85:
		return "P1"
	case score >= 70:
		return "P2"
	case score >= 55:
		return "P3"
	case score >= 40:
		return "P4"
	default:
		return "P5"
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
