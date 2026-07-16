package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const taskSuggestionPromptVersion = "tactical_task_designer_v0_1_0"

const taskSuggestionPrompt = `You are an execution designer inside REUP.goals.

The strategy, active course, tactical change, and project have already been chosen. Your job is to propose a small set of concrete tasks that can execute or validate the selected tactical element.

Do not redesign the strategy or tactics. Do not add generic project-management activity. Each task must produce a meaningful result, decision, evidence, or business change. Avoid duplicate tasks and avoid splitting one action into artificial micro-steps.

Use the supplied workstream, project, risks, opportunities, and existing tasks. If an instruction from the user is supplied, respect it. Prefer the minimum coherent set of tasks needed for the next execution cycle.

Return valid JSON only:
{
  "summary": "A short explanation of the proposed task set in the user's language",
  "suggestions": [
    {
      "title": "Concrete result-oriented task title",
      "description": "What must be done and what output should exist",
      "why_now": "Why this task is useful for the selected tactical element now",
      "priority": 1,
      "due_in_days": 7
    }
  ]
}

priority must be 1, 2, or 3. due_in_days may be null when a defensible deadline cannot be inferred. Return no more tasks than the context genuinely requires.`

type TaskSuggestionService struct {
	store  *Store
	ai     *ai.OpenAIClient
	memory *strategicmemory.Store
}

func NewTaskSuggestionService(dbx *sql.DB, aiClient *ai.OpenAIClient) *TaskSuggestionService {
	return &TaskSuggestionService{store: NewStore(dbx), ai: aiClient, memory: strategicmemory.NewStore(dbx)}
}

func (s *TaskSuggestionService) Generate(ctx context.Context, workspaceID int, request TaskSuggestionRequest) (TaskSuggestionResponse, error) {
	request.Instruction = strings.TrimSpace(request.Instruction)
	if request.WorkstreamID <= 0 || len([]rune(request.Instruction)) > 4000 {
		return TaskSuggestionResponse{}, fmt.Errorf("invalid_task_suggestion_request")
	}
	state, err := s.store.Workstream(ctx, workspaceID, request.WorkstreamID)
	if err != nil {
		return TaskSuggestionResponse{}, err
	}
	if state.Workstream == nil || !validSuggestionScope(state, request) {
		return TaskSuggestionResponse{}, ErrForbidden
	}
	input, _ := json.Marshal(map[string]any{
		"active_course":           state.Course,
		"tactical_plan":           state.TacticalPlan,
		"workstream":              state.Workstream,
		"selected_project_id":     request.ProjectID,
		"selected_risk_id":        request.RiskID,
		"selected_opportunity_id": request.OpportunityID,
		"existing_tasks":          state.Tasks,
		"user_instruction":        request.Instruction,
	})
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, taskSuggestionPrompt, string(input), ai.ResponseContextOptions{
		PromptCacheKey: fmt.Sprintf("reupgoals-task-designer-workspace-%d-v1", workspaceID),
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "tactical_task_designer", s.ai.Model, taskSuggestionPromptVersion, duration, 0, 0, "failed", err.Error())
		return TaskSuggestionResponse{}, err
	}
	response, err := parseTaskSuggestions(result.Text)
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "tactical_task_designer", s.ai.Model, taskSuggestionPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return TaskSuggestionResponse{}, err
	}
	s.memory.LogAIRunWithUsage(ctx, workspaceID, "tactical_task_designer", s.ai.Model, taskSuggestionPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	response.InputTokens = result.Usage.InputTokens
	response.OutputTokens = result.Usage.OutputTokens
	return response, nil
}

func validSuggestionScope(state WorkstreamResponse, request TaskSuggestionRequest) bool {
	if request.ProjectID != nil && !containsProject(state.Projects, *request.ProjectID) {
		return false
	}
	if request.RiskID != nil && !containsRisk(state.Risks, *request.RiskID) {
		return false
	}
	if request.OpportunityID != nil && !containsOpportunity(state.Opportunities, *request.OpportunityID) {
		return false
	}
	return true
}

func containsProject(items []Project, id int) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func containsRisk(items []Risk, id int) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func containsOpportunity(items []Opportunity, id int) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func parseTaskSuggestions(raw string) (TaskSuggestionResponse, error) {
	var response TaskSuggestionResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &response); err != nil {
		return TaskSuggestionResponse{}, err
	}
	response.Summary = strings.TrimSpace(response.Summary)
	clean := make([]TaskSuggestion, 0, len(response.Suggestions))
	seen := map[string]bool{}
	for _, item := range response.Suggestions {
		item.Title = strings.TrimSpace(item.Title)
		item.Description = strings.TrimSpace(item.Description)
		item.WhyNow = strings.TrimSpace(item.WhyNow)
		key := strings.ToLower(item.Title)
		if item.Title == "" || seen[key] {
			continue
		}
		seen[key] = true
		if item.Priority < 1 || item.Priority > 3 {
			item.Priority = 2
		}
		if item.DueInDays != nil && (*item.DueInDays < 1 || *item.DueInDays > 365) {
			item.DueInDays = nil
		}
		clean = append(clean, item)
		if len(clean) >= 12 {
			break
		}
	}
	if len(clean) == 0 {
		return TaskSuggestionResponse{}, fmt.Errorf("task designer returned no usable suggestions")
	}
	response.Suggestions = clean
	return response, nil
}
