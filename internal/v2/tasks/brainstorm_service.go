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

type BrainstormService struct {
	store            *Store
	context          *taskContextBuilder
	memory           *strategicmemory.Store
	ai               *ai.OpenAIClient
	evaluator        *TaskEvaluatorService
	compactThreshold int
}

func NewBrainstormService(
	dbx *sql.DB,
	aiClient *ai.OpenAIClient,
	evaluator *TaskEvaluatorService,
	compactThreshold int,
) *BrainstormService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	return &BrainstormService{
		store: NewStore(dbx), context: newTaskContextBuilder(dbx), memory: strategicmemory.NewStore(dbx),
		ai: aiClient, evaluator: evaluator, compactThreshold: compactThreshold,
	}
}

func (s *BrainstormService) History(ctx context.Context, workspaceID int, workstreamID int) (BrainstormHistoryResponse, error) {
	state, err := s.store.Workstream(ctx, workspaceID, workstreamID)
	if err != nil {
		return BrainstormHistoryResponse{}, err
	}
	if state.Workstream == nil {
		return BrainstormHistoryResponse{}, ErrForbidden
	}
	messages, err := s.store.BrainstormMessages(ctx, workspaceID, workstreamID, 300)
	if err != nil {
		return BrainstormHistoryResponse{}, err
	}
	return BrainstormHistoryResponse{WorkspaceID: workspaceID, Workstream: state.Workstream, Messages: messages}, nil
}

func (s *BrainstormService) HandleMessage(
	ctx context.Context,
	workspaceID int,
	userID int,
	request BrainstormMessageRequest,
) (BrainstormMessageResponse, error) {
	request.Message = strings.TrimSpace(request.Message)
	if request.WorkstreamID <= 0 || len([]rune(request.Message)) < 1 {
		return BrainstormMessageResponse{}, fmt.Errorf("invalid_brainstorm_message")
	}
	if len([]rune(request.Message)) > 50000 {
		return BrainstormMessageResponse{}, fmt.Errorf("brainstorm_message_too_long")
	}

	pack, vectorStoreIDs, fingerprint, err := s.context.Build(ctx, workspaceID, request.WorkstreamID, 40)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}
	userMessage, err := s.store.CreateBrainstormMessage(
		ctx, workspaceID, request.WorkstreamID, &userID, "user", request.Message, nil, nil,
	)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}
	session, err := s.store.BrainstormSession(ctx, workspaceID, request.WorkstreamID, s.compactThreshold, fingerprint)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}

	usedPreviousResponseID := session.PreviousResponseID
	input := brainstormTurnInput(request.Message)
	if strings.TrimSpace(session.PreviousResponseID) == "" {
		input = brainstormFreshInput(pack, request.Message)
	}
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, taskBrainstormPrompt, input, ai.ResponseContextOptions{
		PreviousResponseID: session.PreviousResponseID, VectorStoreIDs: vectorStoreIDs,
		CompactThreshold: session.CompactThreshold, PromptCacheKey: session.PromptCacheKey,
		MaxFileSearchResults: 6, MaxOutputTokens: 8000, RequestTimeout: 2 * time.Minute,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil && strings.TrimSpace(session.PreviousResponseID) != "" {
		_ = s.store.UpdateBrainstormPreviousResponseID(ctx, workspaceID, request.WorkstreamID, "")
		usedPreviousResponseID = ""
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(ctx, taskBrainstormPrompt, brainstormFreshInput(pack, request.Message), ai.ResponseContextOptions{
			VectorStoreIDs: vectorStoreIDs, CompactThreshold: session.CompactThreshold,
			PromptCacheKey: session.PromptCacheKey, MaxFileSearchResults: 6,
			MaxOutputTokens: 8000, RequestTimeout: 2 * time.Minute,
		})
		duration = time.Since(started).Milliseconds()
	}
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.Model, taskBrainstormPromptVersion, duration, 0, 0, "failed", err.Error())
		return BrainstormMessageResponse{}, err
	}

	output, err := parseBrainstormOutput(result.Text, pack)
	if err != nil {
		_ = s.store.UpdateBrainstormPreviousResponseID(ctx, workspaceID, request.WorkstreamID, "")
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.Model, taskBrainstormPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return BrainstormMessageResponse{}, err
	}
	if strings.TrimSpace(result.ResponseID) != "" {
		_ = s.store.UpdateBrainstormPreviousResponseID(ctx, workspaceID, request.WorkstreamID, result.ResponseID)
	}
	message, err := s.store.CreateBrainstormMessage(
		ctx, workspaceID, request.WorkstreamID, nil, "assistant", output.Message, output.Actions,
		map[string]any{
			"prompt_version": taskBrainstormPromptVersion, "response_id": result.ResponseID,
			"previous_response_id": usedPreviousResponseID, "context_fingerprint": fingerprint,
		},
	)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}
	s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.Model, taskBrainstormPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return BrainstormMessageResponse{
		WorkspaceID: workspaceID, AssistantMessage: output.Message, UserMessage: userMessage, Message: message,
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
	}, nil
}

func (s *BrainstormService) ApplyActions(
	ctx context.Context,
	workspaceID int,
	userID int,
	request ApplyBrainstormActionsRequest,
) (ApplyBrainstormActionsResponse, error) {
	if request.WorkstreamID <= 0 || request.MessageID <= 0 || len(request.ActionIndices) == 0 {
		return ApplyBrainstormActionsResponse{}, fmt.Errorf("invalid_brainstorm_actions")
	}
	message, err := s.store.BrainstormAssistantMessage(ctx, workspaceID, request.WorkstreamID, request.MessageID)
	if err != nil {
		return ApplyBrainstormActionsResponse{}, err
	}
	appliedSet := map[int]bool{}
	for _, index := range message.Applied {
		appliedSet[index] = true
	}
	selected := map[int]bool{}
	result := ApplyBrainstormActionsResponse{Tasks: []Task{}, Applied: append([]int{}, message.Applied...)}
	for _, index := range request.ActionIndices {
		if selected[index] || appliedSet[index] {
			continue
		}
		selected[index] = true
		if index < 0 || index >= len(message.Actions) {
			return ApplyBrainstormActionsResponse{}, fmt.Errorf("invalid_brainstorm_action_index")
		}
		action := message.Actions[index]
		claimed, claimErr := s.store.ClaimBrainstormActionApplication(
			ctx, workspaceID, request.WorkstreamID, request.MessageID, index, action.ActionType, userID,
		)
		if claimErr != nil {
			return ApplyBrainstormActionsResponse{}, claimErr
		}
		if !claimed {
			continue
		}
		task, actionErr := s.applyAction(ctx, workspaceID, userID, request.WorkstreamID, action)
		if actionErr != nil {
			_ = s.store.FailBrainstormActionApplication(ctx, workspaceID, request.MessageID, index, actionErr.Error())
			return ApplyBrainstormActionsResponse{}, actionErr
		}
		if completeErr := s.store.CompleteBrainstormActionApplication(ctx, workspaceID, request.MessageID, index, task.ID); completeErr != nil {
			_ = s.store.FailBrainstormActionApplication(ctx, workspaceID, request.MessageID, index, completeErr.Error())
			return ApplyBrainstormActionsResponse{}, completeErr
		}
		if action.ActionType != "archive" && s.evaluator != nil {
			_ = s.evaluator.Queue(ctx, workspaceID, userID, task.ID, true)
			task, _ = s.store.Get(ctx, workspaceID, task.ID)
		}
		result.Tasks = append(result.Tasks, task)
		result.Applied = append(result.Applied, index)
	}
	return result, nil
}

func (s *BrainstormService) applyAction(
	ctx context.Context,
	workspaceID int,
	userID int,
	workstreamID int,
	action BrainstormAction,
) (Task, error) {
	switch action.ActionType {
	case "create":
		dueDate := dueDateFromDays(action.DueInDays)
		sourceType := SourceAISuggestion
		title := strings.TrimSpace(action.Title)
		description := strings.TrimSpace(action.Description)
		expectedResult := strings.TrimSpace(action.ExpectedResult)
		successCriteria := strings.TrimSpace(action.SuccessCriteria)
		whyNow := strings.TrimSpace(action.WhyNow)
		return s.store.Create(ctx, workspaceID, userID, TaskInput{
			WorkstreamID: workstreamID, ProjectID: action.ProjectID, RiskID: action.RiskID,
			OpportunityID: action.OpportunityID, Title: &title, Description: &description,
			ExpectedResult: &expectedResult, SuccessCriteria: &successCriteria, WhyNow: &whyNow,
			DueDate: dueDate, SourceType: &sourceType,
		})
	case "update":
		if action.TaskID == nil {
			return Task{}, ErrForbidden
		}
		current, err := s.store.Get(ctx, workspaceID, *action.TaskID)
		if err != nil || current.WorkstreamID != workstreamID {
			return Task{}, ErrForbidden
		}
		input := TaskInput{WorkstreamID: workstreamID}
		if value := strings.TrimSpace(action.Title); value != "" {
			input.Title = &value
		}
		if value := strings.TrimSpace(action.Description); value != "" {
			input.Description = &value
		}
		if value := strings.TrimSpace(action.ExpectedResult); value != "" {
			input.ExpectedResult = &value
		}
		if value := strings.TrimSpace(action.SuccessCriteria); value != "" {
			input.SuccessCriteria = &value
		}
		if value := strings.TrimSpace(action.WhyNow); value != "" {
			input.WhyNow = &value
		}
		if action.ProjectID != nil {
			input.ProjectID = action.ProjectID
		}
		return s.store.Update(ctx, workspaceID, userID, current.ID, input)
	case "archive":
		if action.TaskID == nil {
			return Task{}, ErrForbidden
		}
		current, err := s.store.Get(ctx, workspaceID, *action.TaskID)
		if err != nil || current.WorkstreamID != workstreamID {
			return Task{}, ErrForbidden
		}
		return s.store.UpdateStatus(ctx, workspaceID, userID, current.ID, StatusArchived, nil)
	default:
		return Task{}, ErrForbidden
	}
}

func brainstormFreshInput(pack taskContextPack, message string) string {
	raw, _ := json.Marshal(map[string]any{"context": pack, "current_user_message": message})
	return string(raw)
}

func brainstormTurnInput(message string) string {
	raw, _ := json.Marshal(map[string]any{"current_user_message": message})
	return string(raw)
}

func parseBrainstormOutput(raw string, pack taskContextPack) (brainstormModelOutput, error) {
	var output brainstormModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return brainstormModelOutput{}, err
	}
	output.Message = strings.TrimSpace(output.Message)
	if output.Message == "" {
		return brainstormModelOutput{}, fmt.Errorf("empty_brainstorm_message")
	}
	existingTasks := map[int]bool{}
	for _, task := range pack.ExistingTasks {
		existingTasks[task.ID] = true
	}
	projects := map[int]bool{}
	for _, project := range pack.Projects {
		projects[project.ID] = true
	}
	risks := map[int]bool{}
	for _, risk := range pack.Risks {
		risks[risk.ID] = true
	}
	opportunities := map[int]bool{}
	for _, opportunity := range pack.Opportunities {
		opportunities[opportunity.ID] = true
	}
	clean := []BrainstormAction{}
	for _, action := range output.Actions {
		action.ActionType = strings.ToLower(strings.TrimSpace(action.ActionType))
		action.Title = strings.TrimSpace(action.Title)
		action.Description = strings.TrimSpace(action.Description)
		action.ExpectedResult = strings.TrimSpace(action.ExpectedResult)
		action.SuccessCriteria = strings.TrimSpace(action.SuccessCriteria)
		action.WhyNow = strings.TrimSpace(action.WhyNow)
		action.Reason = strings.TrimSpace(action.Reason)
		if action.ActionType == "create" && action.Title == "" {
			continue
		}
		if (action.ActionType == "update" || action.ActionType == "archive") && (action.TaskID == nil || !existingTasks[*action.TaskID]) {
			continue
		}
		if action.ActionType != "create" && action.ActionType != "update" && action.ActionType != "archive" {
			continue
		}
		if action.ProjectID != nil && !projects[*action.ProjectID] {
			action.ProjectID = nil
		}
		if action.RiskID != nil && !risks[*action.RiskID] {
			action.RiskID = nil
		}
		if action.OpportunityID != nil && !opportunities[*action.OpportunityID] {
			action.OpportunityID = nil
		}
		if action.DueInDays != nil && (*action.DueInDays < 0 || *action.DueInDays > 3650) {
			action.DueInDays = nil
		}
		clean = append(clean, action)
	}
	output.Actions = clean
	return output, nil
}

func dueDateFromDays(days *int) *string {
	if days == nil || *days < 0 {
		return nil
	}
	value := time.Now().UTC().AddDate(0, 0, *days).Format("2006-01-02")
	return &value
}
