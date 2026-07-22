package tasks

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

const taskCompletionPromptVersion = "task_completion_evaluator_v1_1_0"

const taskCompletionPrompt = `You evaluate whether a completed task result is documented well enough to be useful to the business.

Use the task, its expected result, company goal, project context, the user's completion statement, and any attached result files. Attached files are supporting evidence; they do not replace a clear completion statement.

Decide whether the result explains what actually changed or was produced. Do not require unnecessary ceremony. A short answer is sufficient when it names a concrete outcome. Mark it insufficient when it only says that work was done, repeats the task, gives no observable outcome, or makes an unsupported claim where evidence is necessary.

Write reason and missing_information in the same language as the completion statement.

Return JSON only:
{
  "sufficient": true,
  "quality_score": 0,
  "reason": "A short concrete explanation in the user's language",
  "missing_information": ["Only material details needed to understand the result"]
}

quality_score uses a 0-1000 scale.`

type TaskCompletionService struct {
	store            *Store
	evaluator        *TaskEvaluatorService
	memory           *strategicmemory.Store
	memoryService    *strategicmemory.Service
	ai               ai.Provider
	compactThreshold int
}

func NewTaskCompletionService(dbx *sql.DB, aiClient ai.Provider, evaluator *TaskEvaluatorService, compactThreshold int) *TaskCompletionService {
	if compactThreshold <= 0 {
		compactThreshold = 60000
	}
	return &TaskCompletionService{
		store: NewStore(dbx), evaluator: evaluator, memory: strategicmemory.NewStore(dbx),
		memoryService: strategicmemory.NewService(strategicmemory.NewStore(dbx), aiClient, compactThreshold),
		ai:            aiClient, compactThreshold: compactThreshold,
	}
}

func (s *TaskCompletionService) UploadFile(ctx context.Context, workspaceID int, userID int, filename string, contentType string, sizeBytes int64, file io.Reader) (strategicmemory.FileUploadResponse, error) {
	return s.memoryService.UploadReferenceFile(ctx, workspaceID, userID, filename, contentType, sizeBytes, file)
}

func (s *TaskCompletionService) Complete(ctx context.Context, workspaceID int, userID int, taskID int, request CompleteTaskRequest) (Task, error) {
	resultText := strings.TrimSpace(request.Result)
	if resultText == "" || len([]rune(resultText)) > 20000 {
		return Task{}, ErrCompletionResultRequired
	}
	if _, err := s.store.Get(ctx, workspaceID, taskID); err != nil {
		return Task{}, err
	}
	if err := s.store.ReplaceCompletionFiles(ctx, workspaceID, taskID, request.FileIDs); err != nil {
		return Task{}, err
	}

	status := StatusDone
	updated, err := s.store.Update(ctx, workspaceID, userID, taskID, TaskInput{
		Status: &status, CompletionResult: &resultText,
	})
	if err != nil {
		return Task{}, err
	}

	input, _, contextErr := s.evaluator.buildEvaluationInput(ctx, workspaceID, updated)
	if contextErr != nil {
		input = map[string]any{
			"task": map[string]any{
				"id": updated.ID, "title": updated.Title, "description": updated.Description,
				"expected_result": updated.ExpectedResult,
			},
		}
		if snapshot, snapshotErr := s.memory.LatestSnapshot(ctx, workspaceID); snapshotErr == nil && snapshot != nil {
			input["company_context"] = map[string]any{
				"business_stage":   snapshot.BusinessStage,
				"snapshot_excerpt": truncateRunes(string(snapshot.Snapshot), 3500),
			}
		}
	}
	input["completion"] = map[string]any{
		"result": resultText,
		"files":  updated.CompletionFiles,
	}
	rawInput, _ := json.Marshal(input)

	options := ai.ResponseContextOptions{
		PromptCacheKey:  fmt.Sprintf("reupgoals-task-completion-workspace-%d-v1-1", workspaceID),
		MaxOutputTokens: 500,
		RequestTimeout:  90 * time.Second,
	}
	if len(updated.CompletionFiles) > 0 {
		session, sessionErr := s.memory.OpenAISession(ctx, workspaceID, s.compactThreshold)
		if sessionErr == nil && strings.TrimSpace(session.VectorStoreID) != "" {
			options.VectorStoreIDs = []string{strings.TrimSpace(session.VectorStoreID)}
			options.MaxFileSearchResults = 6
		}
	}

	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "task_completion_evaluator", taskCompletionPromptVersion)
	started := time.Now()
	generated, generateErr := s.ai.GenerateJSONNative(aiCtx, taskCompletionPrompt, string(rawInput), options)
	duration := time.Since(started).Milliseconds()
	if generateErr != nil {
		_ = s.store.SaveCompletionEvaluationFailure(ctx, workspaceID, taskID, s.ai.ModelName(), generateErr.Error(), duration)
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_completion_evaluator", s.ai.ModelName(), taskCompletionPromptVersion, duration, 0, 0, "failed", generateErr.Error())
		return s.store.Get(ctx, workspaceID, taskID)
	}

	output, parseErr := parseTaskCompletionOutput(generated.Text)
	if parseErr != nil {
		_ = s.store.SaveCompletionEvaluationFailure(ctx, workspaceID, taskID, s.ai.ModelName(), parseErr.Error(), duration)
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_completion_evaluator", s.ai.ModelName(), taskCompletionPromptVersion, duration, generated.Usage.InputTokens, generated.Usage.OutputTokens, "failed", parseErr.Error())
		return s.store.Get(ctx, workspaceID, taskID)
	}
	if err := s.store.SaveCompletionEvaluation(ctx, workspaceID, taskID, s.ai.ModelName(), output, generated.Usage.InputTokens, generated.Usage.OutputTokens, duration); err != nil {
		return Task{}, err
	}
	s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_completion_evaluator", s.ai.ModelName(), taskCompletionPromptVersion, duration, generated.Usage.InputTokens, generated.Usage.OutputTokens, "success", "")
	return s.store.Get(ctx, workspaceID, taskID)
}

func parseTaskCompletionOutput(raw string) (taskCompletionModelOutput, error) {
	var output taskCompletionModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return taskCompletionModelOutput{}, err
	}
	output.QualityScore = clampScore(output.QualityScore)
	output.Reason = strings.TrimSpace(output.Reason)
	cleanMissing := make([]string, 0, len(output.MissingInformation))
	for _, item := range output.MissingInformation {
		if cleaned := strings.TrimSpace(item); cleaned != "" {
			cleanMissing = append(cleanMissing, cleaned)
		}
	}
	output.MissingInformation = cleanMissing
	if output.Reason == "" {
		return taskCompletionModelOutput{}, fmt.Errorf("empty_task_completion_reason")
	}
	return output, nil
}
