package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/aiactions"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/strategicmemory"
)

type BrainstormService struct {
	store            *Store
	context          *taskContextBuilder
	memory           *strategicmemory.Store
	ai               ai.Provider
	evaluator        *TaskEvaluatorService
	compactThreshold int
	contextIndex     *contextindex.Service
	recorder         *strategicmemory.SourceRecorder
}

func (s *BrainstormService) SetContextIndex(index *contextindex.Service) {
	s.contextIndex = index
}

func NewBrainstormService(
	dbx *sql.DB,
	aiClient ai.Provider,
	evaluator *TaskEvaluatorService,
	compactThreshold int,
	recorders ...*strategicmemory.SourceRecorder,
) *BrainstormService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	service := &BrainstormService{
		store: NewStore(dbx), context: newTaskContextBuilder(dbx), memory: strategicmemory.NewStore(dbx),
		ai: aiClient, evaluator: evaluator, compactThreshold: compactThreshold,
	}
	if len(recorders) > 0 {
		service.recorder = recorders[0]
	}
	return service
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
	if s.recorder != nil {
		_, _, captureErr := s.recorder.Capture(ctx, workspaceID, userID, strategicmemory.SourceCapture{
			SourceType: strategicmemory.SourceTypeTaskDiscussion,
			EntityKey:  fmt.Sprintf("task_discussion_message:%d", userMessage.ID),
			Content:    request.Message,
			FactsOnly:  true,
			Metadata: map[string]any{
				"task_discussion_message_id": userMessage.ID,
				"workstream_id":              request.WorkstreamID,
			},
		})
		if captureErr != nil {
			log.Printf("[WARN] capture task discussion workspace_id=%d message_id=%d: %v", workspaceID, userMessage.ID, captureErr)
		}
	}
	session, err := s.store.BrainstormSession(ctx, workspaceID, request.WorkstreamID, s.compactThreshold, fingerprint)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}

	if s.contextIndex != nil {
		indexedIDs, indexErr := s.contextIndex.Available(ctx, workspaceID)
		if indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
		} else if indexErr != nil {
			s.memory.LogAIRunWithUsage(ctx, workspaceID, "workspace_context_sync", s.ai.ModelName(), taskBrainstormPromptVersion, 0, 0, 0, "failed", indexErr.Error())
		}
	}
	input := brainstormTurnInput(request.Message)
	if strings.TrimSpace(session.ConversationID) == "" {
		if strings.TrimSpace(session.PreviousResponseID) != "" {
			input = brainstormFreshInput(pack, request.Message)
		} else {
			input = brainstormInitialInput(pack, request.Message)
		}
	}
	started := time.Now()
	aiCtx := ai.WithScenario(ctx, workspaceID, userID, "task_brainstorm", taskBrainstormPromptVersion)
	prompt := taskBrainstormPrompt + contextindex.RetrievalInstructions
	result, err := s.ai.GenerateJSONNative(aiCtx, prompt, input, ai.ResponseContextOptions{
		UseConversation: true, ConversationID: session.ConversationID, VectorStoreIDs: vectorStoreIDs,
		CompactThreshold: session.CompactThreshold, PromptCacheKey: session.PromptCacheKey,
		MaxFileSearchResults: 6, MaxOutputTokens: 8000,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil && strings.TrimSpace(session.ConversationID) != "" && ai.IsConversationStateError(err) {
		_ = s.store.UpdateBrainstormConversationID(ctx, workspaceID, request.WorkstreamID, "")
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(aiCtx, prompt, brainstormFreshInput(pack, request.Message), ai.ResponseContextOptions{
			UseConversation: true, VectorStoreIDs: vectorStoreIDs, CompactThreshold: session.CompactThreshold,
			PromptCacheKey: session.PromptCacheKey, MaxFileSearchResults: 6,
			MaxOutputTokens: 8000,
		})
		duration = time.Since(started).Milliseconds()
	}
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.ModelName(), taskBrainstormPromptVersion, duration, 0, 0, "failed", err.Error())
		return BrainstormMessageResponse{}, err
	}
	if strings.TrimSpace(result.ConversationID) != "" && result.ConversationID != session.ConversationID {
		_ = s.store.UpdateBrainstormConversationID(ctx, workspaceID, request.WorkstreamID, result.ConversationID)
	}

	output, err := parseBrainstormOutput(result.Text, pack)
	if err != nil {
		s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.ModelName(), taskBrainstormPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		started = time.Now()
		result, err = s.ai.GenerateJSONNative(aiCtx, prompt, "Repair your previous response. Return valid JSON matching the required output contract. The message field must contain natural user-facing prose, never JSON or a serialized object. Preserve the intended meaning and do not ask the user to repeat anything.", ai.ResponseContextOptions{
			UseConversation: true, ConversationID: result.ConversationID, VectorStoreIDs: vectorStoreIDs,
			CompactThreshold: session.CompactThreshold, PromptCacheKey: session.PromptCacheKey,
			MaxFileSearchResults: 6, MaxOutputTokens: 8000,
		})
		duration = time.Since(started).Milliseconds()
		if err == nil {
			output, err = parseBrainstormOutput(result.Text, pack)
		}
		if err != nil {
			s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.ModelName(), taskBrainstormPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
			return BrainstormMessageResponse{}, err
		}
	}
	message, err := s.store.CreateBrainstormMessage(
		ctx, workspaceID, request.WorkstreamID, nil, "assistant", output.Message, output.Actions,
		map[string]any{
			"prompt_version": taskBrainstormPromptVersion, "response_id": result.ResponseID,
			"conversation_id": result.ConversationID, "context_fingerprint": fingerprint,
		},
	)
	if err != nil {
		return BrainstormMessageResponse{}, err
	}
	if len(output.Actions) > 0 {
		actionStates, err := s.store.RegisterBrainstormActions(ctx, workspaceID, request.WorkstreamID, message.ID, output.Actions)
		if err != nil {
			return BrainstormMessageResponse{}, err
		}
		message.ActionStates = actionStates
	}
	s.memory.LogAIRunWithUsage(ctx, workspaceID, "task_brainstorm", s.ai.ModelName(), taskBrainstormPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
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
		registered, confirmed, confirmErr := s.store.aiActions.Confirm(
			ctx,
			workspaceID,
			aiactions.ScenarioTaskBrainstorm,
			request.MessageID,
			index,
			userID,
		)
		if confirmErr != nil {
			return ApplyBrainstormActionsResponse{}, confirmErr
		}
		if !confirmed {
			if registered.Status == aiactions.StatusApplied {
				continue
			}
			return ApplyBrainstormActionsResponse{}, fmt.Errorf("brainstorm_action_not_confirmable")
		}
		if len(registered.Payload) > 0 && string(registered.Payload) != "{}" {
			if err := json.Unmarshal(registered.Payload, &action); err != nil {
				_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTaskBrainstorm, request.MessageID, index, err.Error())
				return ApplyBrainstormActionsResponse{}, err
			}
		}
		claimed, claimErr := s.store.ClaimBrainstormActionApplication(
			ctx, workspaceID, request.WorkstreamID, request.MessageID, index, action.ActionType, userID,
		)
		if claimErr != nil {
			_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTaskBrainstorm, request.MessageID, index, claimErr.Error())
			return ApplyBrainstormActionsResponse{}, claimErr
		}
		if !claimed {
			continue
		}
		task, actionErr := s.applyAction(ctx, workspaceID, userID, request.WorkstreamID, action)
		if actionErr != nil {
			_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTaskBrainstorm, request.MessageID, index, actionErr.Error())
			_ = s.store.FailBrainstormActionApplication(ctx, workspaceID, request.MessageID, index, actionErr.Error())
			return ApplyBrainstormActionsResponse{}, actionErr
		}
		if err := s.store.aiActions.MarkApplied(
			ctx,
			workspaceID,
			aiactions.ScenarioTaskBrainstorm,
			request.MessageID,
			index,
			"task",
			task.ID,
		); err != nil {
			return ApplyBrainstormActionsResponse{}, err
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
		if action.DueDate != "" {
			dueDate = &action.DueDate
		}
		sourceType := SourceAISuggestion
		title := strings.TrimSpace(action.Title)
		description := strings.TrimSpace(action.Description)
		expectedResult := strings.TrimSpace(action.ExpectedResult)
		whyNow := strings.TrimSpace(action.WhyNow)
		return s.store.Create(ctx, workspaceID, userID, TaskInput{
			WorkstreamID: workstreamID, DepartmentID: action.DepartmentID,
			ProjectID: action.ProjectID, RiskID: action.RiskID,
			OpportunityID: action.OpportunityID, Title: &title, Description: &description,
			ExpectedResult: &expectedResult, WhyNow: &whyNow, OwnerUserID: action.OwnerUserID,
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
		if value := strings.TrimSpace(action.WhyNow); value != "" {
			input.WhyNow = &value
		}
		if action.ProjectID != nil {
			input.ProjectID = action.ProjectID
		}
		if action.DepartmentID != nil {
			input.DepartmentID = action.DepartmentID
		}
		if action.OwnerUserID != nil {
			input.OwnerUserID = action.OwnerUserID
		} else if action.OwnerDeferred {
			input.ClearOwner = true
		}
		if action.DueDate != "" {
			input.DueDate = &action.DueDate
		} else if action.DueDateDeferred {
			empty := ""
			input.DueDate = &empty
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

func brainstormInitialInput(pack taskContextPack, message string) string {
	raw, _ := json.Marshal(map[string]any{
		"current_user_message": message,
		"active_workstream":    pack.Workstream,
		"projects":             pack.Projects,
		"creation_options":     pack.CreationOptions,
		"context_access":       "Use file_search for the current business, strategy, course, tactics, and tasks.",
	})
	return string(raw)
}

func parseBrainstormOutput(raw string, pack taskContextPack) (brainstormModelOutput, error) {
	var output brainstormModelOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &output); err != nil {
		return brainstormModelOutput{}, err
	}
	output.Message = strings.TrimSpace(output.Message)
	output.Message = strings.ReplaceAll(output.Message, "【】", "")
	output.Message = strings.ReplaceAll(output.Message, "【 】", "")
	if output.Message == "" {
		return brainstormModelOutput{}, fmt.Errorf("empty_brainstorm_message")
	}
	if ai.LooksLikeJSONObject(output.Message) {
		return brainstormModelOutput{}, fmt.Errorf("task brainstorm serialized a structured payload into message")
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
	departments := map[int]bool{}
	for _, department := range pack.CreationOptions.Departments {
		departments[department.ID] = true
	}
	members := map[int]bool{}
	for _, member := range pack.CreationOptions.Members {
		members[member.UserID] = true
	}
	clean := []BrainstormAction{}
	for _, action := range output.Actions {
		action.ActionType = strings.ToLower(strings.TrimSpace(action.ActionType))
		action.Title = strings.TrimSpace(action.Title)
		action.Description = strings.TrimSpace(action.Description)
		action.ExpectedResult = strings.TrimSpace(action.ExpectedResult)
		action.WhyNow = strings.TrimSpace(action.WhyNow)
		action.DueDate = strings.TrimSpace(action.DueDate)
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
		if action.DepartmentID != nil && !departments[*action.DepartmentID] {
			action.DepartmentID = nil
		}
		if action.OwnerUserID != nil && !members[*action.OwnerUserID] {
			action.OwnerUserID = nil
		}
		if action.DueDate != "" {
			if _, err := time.Parse("2006-01-02", action.DueDate); err != nil {
				action.DueDate = ""
			}
		}
		if action.DueInDays != nil && (*action.DueInDays < 0 || *action.DueInDays > 3650) {
			action.DueInDays = nil
		}
		if action.ActionType == "create" {
			hasOwnerDecision := action.OwnerUserID != nil || action.OwnerDeferred
			hasDueDateDecision := action.DueDate != "" || action.DueDateDeferred || action.DueInDays != nil
			if action.ProjectID == nil || action.Description == "" || action.ExpectedResult == "" ||
				action.DepartmentID == nil || !hasOwnerDecision || !hasDueDateDecision {
				continue
			}
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
