package agent

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
	"reup-goals-backend/internal/v2/billing"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/departments"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/tactics"
	"reup-goals-backend/internal/v2/workspaces"
)

type Service struct {
	enabled      bool
	model        string
	releaseID    string
	secret       string
	maxTurns     int
	dbx          *sql.DB
	store        *Store
	tactics      *tactics.Store
	tacticsApply *tactics.Handler
	strategy     StrategyBridge
	documents    DocumentBridge
	taskResults  TaskCompletionBridge
	workspaces   *workspaces.Store
	contextIndex *contextindex.Service
	runtime      *RuntimeClient
	jobs         *jobs.Manager
	billing      *billing.Service
}

type StrategyBridge interface {
	RecordAgentUserMessage(ctx context.Context, workspaceID int, userID int, content string, metadata map[string]any) error
	RecordAgentAssistantMessage(ctx context.Context, workspaceID int, content string, metadata map[string]any) error
	SubmitAgentStrategyForReview(ctx context.Context, workspaceID int, userID int, input map[string]any) (tactics.AppliedTacticsChange, error)
}

type DocumentBridge interface {
	ApplyAgentDocument(ctx context.Context, workspaceID int, userID int, toolName string, input map[string]any) (any, error)
}

type TaskCompletionBridge interface {
	CompleteAgentTask(ctx context.Context, workspaceID int, userID int, taskID int, result string) (any, error)
}

type ServiceConfig struct {
	Enabled   bool
	Model     string
	ReleaseID string
	Secret    string
	MaxTurns  int
}

type agentJobPayload struct {
	RunID string `json:"run_id"`
}

const InteractiveJobPriority = 100

func NewService(
	dbx *sql.DB,
	cfg ServiceConfig,
	runtime *RuntimeClient,
	jobManager *jobs.Manager,
	quota *billing.Service,
	contextIndex *contextindex.Service,
	tacticsHandler *tactics.Handler,
	strategyBridge StrategyBridge,
	documentBridge DocumentBridge,
	taskCompletionBridge TaskCompletionBridge,
) *Service {
	service := &Service{
		enabled: cfg.Enabled, model: cfg.Model, releaseID: strings.TrimSpace(cfg.ReleaseID),
		secret: cfg.Secret, maxTurns: cfg.MaxTurns,
		dbx: dbx, store: NewStore(dbx), tactics: tactics.NewStore(dbx),
		tacticsApply: tacticsHandler, workspaces: workspaces.NewStore(dbx),
		strategy:     strategyBridge,
		documents:    documentBridge,
		taskResults:  taskCompletionBridge,
		contextIndex: contextIndex, runtime: runtime, jobs: jobManager, billing: quota,
	}
	if service.maxTurns <= 0 {
		service.maxTurns = 30
	}
	if service.releaseID == "" {
		service.releaseID = DefaultRelease
	}
	if cfg.Enabled && jobManager != nil {
		jobManager.RegisterWithoutTimeout(JobTypeExecute, service.handleExecuteJob)
		jobManager.RegisterWithoutTimeout(JobTypeResume, service.handleResumeJob)
	}
	return service
}

func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

func (s *Service) CreateRun(ctx context.Context, userID int, request CreateRunRequest) (Run, error) {
	if !s.Enabled() {
		return Run{}, errors.New("agent_runtime_disabled")
	}
	request.Message = strings.TrimSpace(request.Message)
	if request.ThreadID <= 0 || request.Message == "" {
		return Run{}, errors.New("invalid_agent_run")
	}
	if len([]rune(request.Message)) > 50000 || len(request.Attachments) > 24 {
		return Run{}, errors.New("invalid_agent_input_size")
	}
	for _, attachment := range request.Attachments {
		if !validAttachment(attachment) {
			return Run{}, errors.New("invalid_agent_attachment")
		}
	}
	workspace, membership, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Run{}, err
	}
	thread, err := s.tactics.AdvisorThread(ctx, workspace.ID, userID, request.ThreadID)
	if err != nil {
		return Run{}, err
	}
	if err := s.releaseStaleActiveRun(ctx, workspace.ID, userID, thread.ID); err != nil {
		return Run{}, err
	}
	scope := request.Scope
	if strings.TrimSpace(scope.Type) == "" {
		scope = Scope{Type: thread.ScopeType, ID: thread.ScopeID, Label: thread.ScopeLabel}
	}
	if !validScope(scope) {
		return Run{}, errors.New("invalid_agent_scope")
	}
	session, err := s.store.LatestThreadSession(ctx, workspace.ID, userID, thread.ID)
	if err != nil {
		return Run{}, err
	}
	sessionGeneration := 1
	migratedFrom := ""
	continuityContext := ""
	if session.Found {
		sessionGeneration = session.SessionGeneration
		if sessionGeneration <= 0 {
			sessionGeneration = 1
		}
		if !compatibleSession(session, s.releaseID, s.model, PromptVersion) ||
			strings.TrimSpace(session.PreviousResponseID) == "" {
			sessionGeneration++
			migratedFrom = strings.TrimSpace(session.AgentReleaseID)
			if migratedFrom == "" {
				migratedFrom = "legacy_advisor"
			}
		}
	}
	if !session.Found || migratedFrom != "" {
		history, historyErr := s.tactics.ScopedChatMessages(
			ctx, workspace.ID, thread.ConversationScope(), 80,
		)
		if historyErr != nil {
			return Run{}, historyErr
		}
		continuityContext = buildContinuityContext(history)
		if !session.Found && continuityContext != "" {
			migratedFrom = "legacy_advisor"
		}
	}
	userMessageID, err := s.tactics.CreateScopedChatMessage(
		ctx, workspace.ID, &userID, "user", request.Message,
		map[string]any{"agent_runtime": true},
		thread.ConversationScope(),
	)
	if err != nil {
		return Run{}, err
	}
	if scope.Type == "strategy" && s.strategy != nil {
		if err := s.strategy.RecordAgentUserMessage(ctx, workspace.ID, userID, request.Message, map[string]any{
			"agent_runtime":      true,
			"tactics_message_id": userMessageID,
			"thread_id":          thread.ID,
		}); err != nil {
			log.Printf("[WARN] strategy transcript mirror failed workspace_id=%d thread_id=%d: %v", workspace.ID, thread.ID, err)
		}
	}
	run, err := s.store.Create(
		ctx, workspace.ID, userID, thread.ID, userMessageID, scope, s.model,
		s.releaseID, sessionGeneration, migratedFrom, continuityContext,
		agentInput(request.Message, request.Attachments),
	)
	if err != nil {
		return Run{}, err
	}
	_ = s.tactics.TouchAdvisorThread(ctx, workspace.ID, userID, thread.ID, request.Message)
	if migratedFrom != "" {
		_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
			Type: "session_migrated", Stage: "starting",
			Title:  "Обновляю рабочую сессию советника",
			Detail: "История чата сохранена и перенесена в актуальную версию агента.",
		})
	}
	_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
		Type:  "run_accepted",
		Stage: "queued",
		Title: "Запрос принят, готовлю рабочий контекст",
	})
	if _, err := s.jobs.EnqueuePriority(
		ctx, workspace.ID, JobTypeExecute, run.PublicID, agentJobPayload{RunID: run.PublicID}, 3, time.Time{},
		InteractiveJobPriority,
	); err != nil {
		_ = s.store.SetFailed(ctx, run.ID, "agent_job_enqueue_failed", true)
		return Run{}, err
	}
	_ = membership
	return s.Hydrate(ctx, run, 0)
}

func (s *Service) releaseStaleActiveRun(ctx context.Context, workspaceID int, userID int, threadID int) error {
	active, err := s.store.ActiveForThread(ctx, workspaceID, userID, threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	active, err = s.reconcileOrphanedRun(ctx, active)
	if err != nil {
		return err
	}
	if active.Status == StatusFailed || active.Status == StatusCompleted || active.Status == StatusCanceled {
		return nil
	}
	if active.Status != StatusWaitingApproval {
		return errors.New("agent_run_already_active")
	}
	approvals, err := s.store.Approvals(ctx, active.ID)
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if approval.Status == "pending" {
			return errors.New("agent_run_already_active")
		}
	}
	return s.store.SetFailed(ctx, active.ID, "agent_approval_no_longer_pending", true)
}

func agentInput(message string, attachments []Attachment) string {
	if len(attachments) == 0 {
		return message
	}
	lines := make([]string, 0, len(attachments))
	for _, item := range attachments {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		reference := strings.TrimSpace(item.Key)
		if reference == "" && item.ID > 0 {
			reference = fmt.Sprintf("%d", item.ID)
		}
		lines = append(lines, fmt.Sprintf("- %s: %s (%s)", item.Type, label, reference))
	}
	if len(lines) == 0 {
		return message
	}
	return message + "\n\n[Вложенный контекст пользователя]\n" + strings.Join(lines, "\n")
}

func (s *Service) RunForUser(ctx context.Context, userID int, publicID string, afterEventID int64) (Run, error) {
	workspace, _, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.ByPublicIDForUser(ctx, publicID, workspace.ID, userID)
	if err != nil {
		return Run{}, err
	}
	run, err = s.reconcileOrphanedRun(ctx, run)
	if err != nil {
		return Run{}, err
	}
	return s.Hydrate(ctx, run, afterEventID)
}

func (s *Service) ActiveRunForThread(ctx context.Context, userID int, threadID int) (Run, error) {
	if threadID <= 0 {
		return Run{}, errors.New("invalid_agent_thread")
	}
	workspace, _, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Run{}, err
	}
	if _, err := s.tactics.AdvisorThread(ctx, workspace.ID, userID, threadID); err != nil {
		return Run{}, err
	}
	run, err := s.store.ActiveForThread(ctx, workspace.ID, userID, threadID)
	if err != nil {
		return Run{}, err
	}
	run, err = s.reconcileOrphanedRun(ctx, run)
	if err != nil {
		return Run{}, err
	}
	return s.Hydrate(ctx, run, 0)
}

func (s *Service) reconcileOrphanedRun(ctx context.Context, run Run) (Run, error) {
	if run.Status == StatusWaitingApproval {
		return s.reconcileWaitingApprovalRun(ctx, run)
	}
	if run.Status != StatusQueued && run.Status != StatusRunning {
		return run, nil
	}
	if time.Since(run.UpdatedAt) < 3*time.Second {
		return run, nil
	}
	active, err := s.store.HasActiveJob(ctx, run, s.jobs.Namespace())
	if err != nil || active {
		return run, err
	}
	if err := s.store.SetFailed(ctx, run.ID, "agent_background_job_missing", true); err != nil {
		return Run{}, err
	}
	_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
		Type: "run_failed", Stage: "recovery", Title: "Предыдущий запуск остановлен",
		Detail: "Сообщение сохранено: отправьте его повторно.",
	})
	return s.store.ByPublicIDForUser(ctx, run.PublicID, run.WorkspaceID, run.UserID)
}

func (s *Service) reconcileWaitingApprovalRun(ctx context.Context, run Run) (Run, error) {
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	pendingCount := 0
	for _, approval := range approvals {
		if approval.Status == "pending" {
			pendingCount++
		}
	}
	if pendingCount == 0 || len(draftChangesFromApprovals(approvals)) != pendingCount {
		return s.failUnrecoverableApproval(ctx, run, "agent_approval_no_longer_pending")
	}
	if run.AssistantMessageID > 0 {
		return run, nil
	}
	thread, err := s.tactics.AdvisorThread(ctx, run.WorkspaceID, run.UserID, run.ThreadID)
	if err != nil {
		return Run{}, err
	}
	messages, err := s.tactics.ScopedChatMessages(
		ctx, run.WorkspaceID, thread.ConversationScope(), 500,
	)
	if err != nil {
		return Run{}, err
	}
	messageID := proposalMessageIDForRun(run.PublicID, messages)
	if messageID <= 0 {
		return s.failUnrecoverableApproval(ctx, run, "agent_proposal_message_missing")
	}
	if err := s.store.SetAssistantMessageID(ctx, run.ID, messageID); err != nil {
		return Run{}, err
	}
	run.AssistantMessageID = messageID
	return run, nil
}

func (s *Service) failUnrecoverableApproval(ctx context.Context, run Run, reason string) (Run, error) {
	if err := s.store.SetFailed(ctx, run.ID, reason, true); err != nil {
		return Run{}, err
	}
	_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
		Type:   "run_failed",
		Stage:  "recovery",
		Title:  "Предыдущее действие безопасно закрыто",
		Detail: "Сообщение сохранено: запрос можно отправить повторно.",
	})
	return s.store.ByPublicIDForUser(ctx, run.PublicID, run.WorkspaceID, run.UserID)
}

func (s *Service) Hydrate(ctx context.Context, run Run, afterEventID int64) (Run, error) {
	events, err := s.store.Events(ctx, run.ID, afterEventID)
	if err != nil {
		return Run{}, err
	}
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	run.Events = events
	run.Approvals = approvals
	run.ProposalMessageID = run.AssistantMessageID
	run.ProposedChanges = draftChangesFromApprovals(approvals)
	return run, nil
}

func (s *Service) Decide(ctx context.Context, userID int, publicID string, request DecisionRequest) (Run, error) {
	workspace, _, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.ByPublicIDForUser(ctx, publicID, workspace.ID, userID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != StatusWaitingApproval || len(request.Decisions) == 0 {
		return Run{}, errors.New("agent_run_not_waiting_for_approval")
	}
	pending, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	pendingCount := 0
	pendingByCallID := make(map[string]Approval, len(pending))
	for _, item := range pending {
		if item.Status == "pending" {
			pendingCount++
			pendingByCallID[item.CallID] = item
		}
	}
	if pendingCount != len(request.Decisions) {
		return Run{}, errors.New("agent_approval_decision_incomplete")
	}
	role := s.participantRole(ctx, workspace.ID, userID)
	for _, decision := range request.Decisions {
		item, exists := pendingByCallID[decision.CallID]
		if !exists {
			return Run{}, errors.New("agent_approval_not_pending")
		}
		if decision.Approved && item.ToolName == "propose_department" && role != "owner" && role != "admin" {
			return Run{}, errors.New("agent_department_action_forbidden")
		}
		if decision.Approved && item.ToolName == "propose_strategy_review" &&
			!s.canSubmitStrategy(ctx, workspace.ID, userID, role) {
			return Run{}, errors.New("agent_strategy_action_forbidden")
		}
	}
	if err := s.store.Decide(ctx, run.ID, userID, request.Decisions); err != nil {
		return Run{}, err
	}
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	approvedIndices := make([]int, 0)
	speciallyApplied := make(map[int]bool)
	for _, item := range approvals {
		if item.Status != "approved" {
			continue
		}
		if item.ToolName == "propose_strategy_review" {
			if s.strategy == nil {
				err = errors.New("strategy_agent_bridge_unavailable")
			} else {
				var input map[string]any
				if err = json.Unmarshal(item.Arguments, &input); err == nil {
					var applied tactics.AppliedTacticsChange
					applied, err = s.strategy.SubmitAgentStrategyForReview(ctx, workspace.ID, userID, input)
					if err == nil {
						err = s.store.SetApprovalResult(
							ctx, run.ID, item.ActionIndex,
							map[string]any{"ok": true, "entity": applied}, nil,
						)
						speciallyApplied[item.ActionIndex] = err == nil
					}
				}
			}
			if err != nil {
				_ = s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, map[string]any{}, err)
				_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
				return Run{}, err
			}
			continue
		}
		if item.ToolName == "propose_document" || item.ToolName == "update_document" {
			if s.documents == nil {
				err = errors.New("agent_document_bridge_unavailable")
			} else {
				var input map[string]any
				if err = json.Unmarshal(item.Arguments, &input); err == nil {
					var applied any
					applied, err = s.documents.ApplyAgentDocument(
						ctx, workspace.ID, userID, item.ToolName, input,
					)
					if err == nil {
						err = s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, applied, nil)
						speciallyApplied[item.ActionIndex] = err == nil
					}
				}
			}
			if err != nil {
				_ = s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, map[string]any{}, err)
				_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
				return Run{}, err
			}
			continue
		}
		if item.ToolName == "complete_task" {
			if s.taskResults == nil {
				err = errors.New("agent_task_completion_bridge_unavailable")
			} else {
				var input map[string]any
				if err = json.Unmarshal(item.Arguments, &input); err == nil {
					var applied any
					applied, err = s.taskResults.CompleteAgentTask(
						ctx, workspace.ID, userID, intValue(input, "task_id"), stringValue(input, "result"),
					)
					if err == nil {
						err = s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, applied, nil)
						speciallyApplied[item.ActionIndex] = err == nil
					}
				}
			}
			if err != nil {
				_ = s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, map[string]any{}, err)
				_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
				return Run{}, err
			}
			continue
		}
		approvedIndices = append(approvedIndices, item.ActionIndex)
	}
	var applyResponse tactics.ApplyTacticsChangesResponse
	if len(approvedIndices) > 0 {
		applyResponse, err = s.tacticsApply.ApplyAgentChanges(ctx, workspace.ID, userID, tactics.ApplyTacticsChangesRequest{
			MessageID: run.AssistantMessageID, ActionIndices: approvedIndices,
		})
		if err != nil {
			for _, index := range approvedIndices {
				_ = s.store.SetApprovalResult(ctx, run.ID, index, map[string]any{}, err)
			}
			_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
			return Run{}, err
		}
	}
	appliedByIndex := map[int]tactics.AppliedTacticsChange{}
	for position, index := range applyResponse.AppliedIndices {
		if position < len(applyResponse.AppliedChanges) {
			appliedByIndex[index] = applyResponse.AppliedChanges[position]
		}
	}
	for _, item := range approvals {
		if item.Status != "approved" || speciallyApplied[item.ActionIndex] {
			continue
		}
		result := map[string]any{"ok": true}
		if applied, found := appliedByIndex[item.ActionIndex]; found {
			result["entity"] = applied
		}
		if err := s.store.SetApprovalResult(ctx, run.ID, item.ActionIndex, result, nil); err != nil {
			return Run{}, err
		}
	}
	// The source-of-truth changes are already committed. Finishing the run here
	// keeps confirmation atomic and avoids reporting a false failure when an
	// optional post-approval model continuation is unavailable.
	if err := s.store.SetCompleted(
		ctx, run.ID, "Изменения применены.", "Изменения применены.", "",
		run.AssistantMessageID, RuntimeUsage{},
	); err != nil {
		return Run{}, err
	}
	run, err = s.store.ByPublicIDForUser(ctx, publicID, workspace.ID, userID)
	if err != nil {
		return Run{}, err
	}
	return s.Hydrate(ctx, run, 0)
}

func (s *Service) handleExecuteJob(ctx context.Context, job jobs.Job) error {
	var payload agentJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	run, err := s.store.ByPublicID(ctx, payload.RunID)
	if err != nil {
		return err
	}
	if run.Status == StatusCompleted || run.Status == StatusWaitingApproval || run.Status == StatusCanceled {
		return nil
	}
	if run.Status == StatusRunning {
		s.settleReservation(ctx, run.ReservationID, false, 0)
		if err := s.store.RequeueInterrupted(ctx, run.ID); err != nil {
			return err
		}
		run.Status = StatusQueued
		run.ReservationID = ""
		_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
			Type: "run_recovered", Stage: "starting",
			Title: "Восстанавливаю запрос после обновления сервиса",
		})
	}
	_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
		Type:  "run_started",
		Stage: "starting",
		Title: "Изучаю контекст и выбираю следующий шаг",
	})
	reservationID, stop, err := s.startBillableRun(ctx, run)
	if err != nil {
		return s.failBeforeReservation(ctx, run, job, err)
	}
	if stop {
		return nil
	}
	if err := s.store.SetRunning(ctx, run.ID, reservationID); err != nil {
		_ = s.billing.Settle(context.WithoutCancel(ctx), reservationID, false, 0)
		return s.failBeforeReservation(ctx, run, job, err)
	}
	run.ReservationID = reservationID

	vectorStoreID := run.VectorStoreID
	if vectorStoreID == "" && s.contextIndex != nil {
		var ids []string
		var indexErr error
		if strings.Contains(run.InputText, "[Вложенный контекст пользователя]") {
			ids, indexErr = s.contextIndex.Ensure(ctx, run.WorkspaceID)
		} else {
			ids, indexErr = s.contextIndex.Available(ctx, run.WorkspaceID)
		}
		if indexErr == nil && len(ids) > 0 {
			vectorStoreID = ids[0]
			_ = s.store.SetVectorStore(ctx, run.ID, vectorStoreID)
		}
	}
	brief, err := s.businessBrief(ctx, run.WorkspaceID, run.UserID)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	session, err := s.store.LatestThreadSession(
		ctx, run.WorkspaceID, run.UserID, run.ThreadID,
	)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	previousResponseID := ""
	conversationID := ""
	if compatibleSession(session, run.AgentReleaseID, run.Model, run.PromptVersion) &&
		session.SessionGeneration == run.SessionGeneration {
		previousResponseID = session.PreviousResponseID
		conversationID = session.ConversationID
	}
	runToken, err := signRunToken(s.secret, run, 6*time.Hour)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	started := time.Now()
	result, err := s.runtime.Execute(ctx, map[string]any{
		"run_id": run.PublicID, "workspace_id": run.WorkspaceID, "user_id": run.UserID,
		"participant_role": s.participantRole(ctx, run.WorkspaceID, run.UserID),
		"scope":            run.Scope, "message": run.InputText, "business_brief": brief,
		"model": run.Model, "previous_response_id": previousResponseID,
		"conversation_id": conversationID, "vector_store_id": vectorStoreID,
		"continuity_context": run.ContinuityContext,
		"run_token":          runToken, "max_turns": s.maxTurns,
	})
	if err != nil {
		s.logCall(ctx, run, time.Since(started), RuntimeUsage{}, err)
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	return s.finishRuntimeResult(ctx, run, reservationID, result, time.Since(started))
}

func (s *Service) handleResumeJob(ctx context.Context, job jobs.Job) error {
	var payload agentJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	run, err := s.store.ByPublicID(ctx, payload.RunID)
	if err != nil {
		return err
	}
	if run.Status == StatusCompleted || run.Status == StatusWaitingApproval || run.Status == StatusCanceled {
		return nil
	}
	if run.Status == StatusRunning {
		s.settleReservation(ctx, run.ReservationID, false, 0)
		if err := s.store.RequeueInterrupted(ctx, run.ID); err != nil {
			return err
		}
		run.Status = StatusQueued
		run.ReservationID = ""
		_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
			Type: "run_recovered", Stage: "starting",
			Title: "Восстанавливаю подтверждённое действие",
		})
	}
	_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
		Type:  "run_resumed",
		Stage: "starting",
		Title: "Продолжаю подтверждённое действие",
	})
	state, err := decryptState(s.secret, run.PublicID, run.StateCiphertext)
	if err != nil || state == "" {
		if err == nil {
			err = errors.New("agent_resume_state_missing")
		}
		_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
		return nil
	}
	// Approval is a continuation of an already counted user message. It may
	// invoke the provider again, but must not consume a second weekly allowance.
	if err := s.store.SetRunning(ctx, run.ID, ""); err != nil {
		return err
	}
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return s.failAttempt(ctx, run, "", job, err)
	}
	decisions := make([]Decision, 0, len(approvals))
	for _, item := range approvals {
		decisions = append(decisions, Decision{CallID: item.CallID, Approved: item.Status == "applied"})
	}
	runToken, err := signRunToken(s.secret, run, 6*time.Hour)
	if err != nil {
		return s.failAttempt(ctx, run, "", job, err)
	}
	started := time.Now()
	result, err := s.runtime.Resume(ctx, map[string]any{
		"run_id": run.PublicID, "model": run.Model, "vector_store_id": run.VectorStoreID,
		"state": state, "run_token": runToken, "decisions": decisions, "max_turns": s.maxTurns,
	})
	if err != nil {
		s.logCall(ctx, run, time.Since(started), RuntimeUsage{}, err)
		return s.failAttempt(ctx, run, "", job, err)
	}
	return s.finishRuntimeResult(ctx, run, "", result, time.Since(started))
}

func (s *Service) startBillableRun(ctx context.Context, run Run) (string, bool, error) {
	reservation, err := s.billing.Reserve(ctx, run.WorkspaceID, run.UserID, "executive_advisor")
	if err == nil {
		return reservation.ID, false, nil
	}
	if errors.Is(err, billing.ErrQuotaExceeded) || errors.Is(err, billing.ErrPaymentRequired) {
		_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
		_ = s.store.InsertEvent(ctx, run.ID, RuntimeEvent{
			Type: "run_failed", Stage: "quota", Title: "Лимит AI исчерпан", Detail: err.Error(),
		})
		return "", true, nil
	}
	return "", false, err
}

func (s *Service) failAttempt(
	ctx context.Context,
	run Run,
	reservationID string,
	job jobs.Job,
	runErr error,
) error {
	s.settleReservation(ctx, reservationID, false, 0)
	terminal := isPermanentRuntimeError(runErr) || job.Attempts >= job.MaxAttempts
	_ = s.store.SetFailed(context.WithoutCancel(ctx), run.ID, runErr.Error(), terminal)
	if terminal {
		_ = s.store.InsertEvent(context.WithoutCancel(ctx), run.ID, RuntimeEvent{
			Type: "run_failed", Stage: "failed", Title: "Не удалось завершить запрос",
			Detail: "Ответ не потерян: можно повторить сообщение.",
		})
		return nil
	}
	return runErr
}

func (s *Service) failBeforeReservation(
	ctx context.Context,
	run Run,
	job jobs.Job,
	runErr error,
) error {
	terminal := isPermanentRuntimeError(runErr) || job.Attempts >= job.MaxAttempts
	_ = s.store.SetFailed(context.WithoutCancel(ctx), run.ID, runErr.Error(), terminal)
	if terminal {
		_ = s.store.InsertEvent(context.WithoutCancel(ctx), run.ID, RuntimeEvent{
			Type: "run_failed", Stage: "failed", Title: "Не удалось запустить советника",
			Detail: "Сообщение сохранено: запрос можно повторить.",
		})
		return nil
	}
	return runErr
}

func isPermanentRuntimeError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, status := range []string{"400", "401", "403", "404", "409", "413", "422"} {
		if strings.Contains(message, "agent_runtime_http_"+status+":") {
			return true
		}
	}
	return false
}

func (s *Service) finishRuntimeResult(
	ctx context.Context,
	run Run,
	reservationID string,
	result RuntimeResult,
	duration time.Duration,
) error {
	if reservationID != "" {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		settleErr := s.billing.Settle(settleCtx, reservationID, true, quotaTokenUsage(result.Usage))
		cancel()
		if settleErr != nil {
			log.Printf("[ERROR] agent quota settlement failed run_id=%s: %v", run.PublicID, settleErr)
		}
	}
	s.logCall(context.WithoutCancel(ctx), run, duration, result.Usage, nil)
	count, _ := s.store.EventCount(ctx, run.ID)
	if count == 0 {
		for _, event := range result.Events {
			_ = s.store.InsertEvent(ctx, run.ID, event)
		}
	}
	if result.Status == StatusWaitingApproval {
		return s.saveApprovalTurn(ctx, run, result)
	}
	if result.Status != StatusCompleted {
		return errors.New("invalid_agent_runtime_status")
	}
	thread, err := s.tactics.AdvisorThread(ctx, run.WorkspaceID, run.UserID, run.ThreadID)
	if err != nil {
		return err
	}
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "Готово. Я проверил контекст, но не сформировал содержательный ответ. Повторите запрос."
	}
	assistantMessageID, err := s.tactics.CreateScopedChatMessage(
		ctx, run.WorkspaceID, nil, "assistant", output,
		map[string]any{"agent_runtime": true, "agent_run_id": run.PublicID},
		thread.ConversationScope(),
	)
	if err != nil {
		return err
	}
	if run.Scope.Type == "strategy" && s.strategy != nil {
		if err := s.strategy.RecordAgentAssistantMessage(ctx, run.WorkspaceID, output, map[string]any{
			"agent_runtime":      true,
			"agent_run_id":       run.PublicID,
			"tactics_message_id": assistantMessageID,
		}); err != nil {
			return err
		}
	}
	return s.store.SetCompleted(
		ctx, run.ID, output, result.PartialOutput, result.PreviousResponseID,
		assistantMessageID, result.Usage,
	)
}

func (s *Service) saveApprovalTurn(ctx context.Context, run Run, result RuntimeResult) error {
	if len(result.Interruptions) == 0 || strings.TrimSpace(result.State) == "" {
		return errors.New("agent_approval_state_incomplete")
	}
	changes := draftChangesFromInterruptions(result.Interruptions)
	if len(changes) != len(result.Interruptions) {
		return errors.New("agent_proposal_not_applicable")
	}
	strategyReviews := 0
	specialChanges := 0
	tacticsChanges := make([]tactics.TacticsDraftChange, 0, len(changes))
	for _, change := range changes {
		switch change.EntityType {
		case "strategy_review":
			strategyReviews++
		case "workspace_document", "task_completion":
			// These changes share the approval UI but are applied through their
			// own source-of-truth services after confirmation.
			specialChanges++
		default:
			tacticsChanges = append(tacticsChanges, change)
		}
	}
	if strategyReviews > 0 && len(changes) != 1 {
		return errors.New("agent_strategy_review_must_be_separate")
	}
	if specialChanges > 0 && len(changes) != 1 {
		return errors.New("agent_special_action_must_be_separate")
	}
	if len(tacticsChanges) > 0 {
		if err := tactics.ValidateDraftChangesForConfirmation(tacticsChanges); err != nil {
			return fmt.Errorf("agent_proposal_not_applicable: %w", err)
		}
	}
	hasTacticsChanges := len(tacticsChanges) > 0
	requiresTacticalPlan := false
	for _, change := range tacticsChanges {
		if change.EntityType != tactics.EntityDepartment && change.EntityType != tactics.EntityTask {
			requiresTacticalPlan = true
			break
		}
	}
	var state tactics.CurrentResponse
	var err error
	if requiresTacticalPlan {
		state, err = s.tactics.Current(ctx, run.WorkspaceID, run.UserID)
		if err != nil || state.TacticalPlan == nil {
			if err == nil {
				err = errors.New("tactics_plan_required")
			}
			return err
		}
	}
	thread, err := s.tactics.AdvisorThread(ctx, run.WorkspaceID, run.UserID, run.ThreadID)
	if err != nil {
		return err
	}
	message := strings.TrimSpace(result.PartialOutput)
	if message == "" {
		message = "Подготовил изменения. Проверьте их перед добавлением в рабочее пространство."
	}
	assistantMessageID, err := s.tactics.CreateScopedChatMessage(
		ctx, run.WorkspaceID, nil, "assistant", message,
		map[string]any{
			"agent_runtime": true, "agent_run_id": run.PublicID, "draft_changes": changes,
		},
		thread.ConversationScope(),
	)
	if err != nil {
		return err
	}
	if run.Scope.Type == "strategy" && s.strategy != nil {
		if err := s.strategy.RecordAgentAssistantMessage(ctx, run.WorkspaceID, message, map[string]any{
			"agent_runtime":      true,
			"agent_run_id":       run.PublicID,
			"tactics_message_id": assistantMessageID,
			"draft_changes":      changes,
		}); err != nil {
			return err
		}
	}
	if hasTacticsChanges {
		var registerErr error
		if requiresTacticalPlan {
			registerErr = s.tactics.RegisterTacticsActions(
				ctx, run.WorkspaceID, state.TacticalPlan.ID, assistantMessageID, changes,
			)
		} else {
			registerErr = s.tactics.RegisterWorkspaceActions(
				ctx, run.WorkspaceID, assistantMessageID, changes,
			)
		}
		if registerErr != nil {
			return registerErr
		}
	}
	if err := s.store.InsertApprovals(ctx, run.ID, result.Interruptions); err != nil {
		return err
	}
	ciphertext, err := encryptState(s.secret, run.PublicID, result.State)
	if err != nil {
		return err
	}
	if _, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs SET assistant_message_id=$2 WHERE id=$1
	`, run.ID, assistantMessageID); err != nil {
		return err
	}
	return s.store.SetWaiting(
		ctx, run.ID, result.PartialOutput, result.PreviousResponseID, ciphertext, result.Usage,
	)
}

func (s *Service) participantRole(ctx context.Context, workspaceID int, userID int) string {
	var role string
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT role FROM workspace_memberships
		WHERE workspace_id=$1 AND user_id=$2 AND status='active'
	`, workspaceID, userID).Scan(&role)
	if role == "" {
		role = "member"
	}
	return role
}

func (s *Service) canSubmitStrategy(
	ctx context.Context,
	workspaceID int,
	userID int,
	role string,
) bool {
	if role == "owner" || role == "admin" {
		return true
	}
	var createdByUser bool
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM v2_strategies
			WHERE workspace_id=$1
				AND archived_at IS NULL
				AND status IN ('draft', 'ready_for_review')
				AND created_by=$2
		)
	`, workspaceID, userID).Scan(&createdByUser)
	return createdByUser
}

func (s *Service) logCall(ctx context.Context, run Run, duration time.Duration, usage RuntimeUsage, callErr error) {
	status := "success"
	errorText := ""
	if callErr != nil {
		status = "failed"
		errorText = callErr.Error()
	}
	costUsage := ai.Usage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
	}
	costUsage.InputTokenDetails.CachedTokens = usage.CachedInputTokens
	estimatedCost := ai.EstimateCost(run.Model, costUsage)
	_, _ = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_ai_call_logs (
			workspace_id, user_id, ai_module, prompt_name, prompt_version, provider, model,
			status, error, latency_ms, token_usage_input, token_usage_output, token_usage_total,
			cached_input_tokens, estimated_cost, request_id, response_id
		)
		VALUES ($1, $2, 'executive_advisor', 'executive_advisor', $3, 'openai', $4,
			$5, $6, $7, $8, $9, $10, $11, $12, '', '')
	`, run.WorkspaceID, run.UserID, PromptVersion, run.Model, status, truncate(errorText, 4000),
		duration.Milliseconds(), usage.InputTokens, usage.OutputTokens, usage.TotalTokens,
		usage.CachedInputTokens, estimatedCost)
}

func (s *Service) settleReservation(ctx context.Context, reservationID string, success bool, tokens int) {
	if reservationID == "" {
		return
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	if err := s.billing.Settle(settleCtx, reservationID, success, tokens); err != nil {
		log.Printf("[ERROR] agent quota settlement failed reservation_id=%s: %v", reservationID, err)
	}
}

func quotaTokenUsage(usage RuntimeUsage) int {
	inputTokens := max(0, usage.InputTokens)
	outputTokens := max(0, usage.OutputTokens)
	cachedTokens := usage.CachedInputTokens
	if cachedTokens < 0 || cachedTokens > inputTokens {
		cachedTokens = 0
	}
	effectiveCachedTokens := (cachedTokens + 9) / 10
	result := inputTokens - cachedTokens + effectiveCachedTokens + outputTokens
	if providerRemainder := usage.TotalTokens - inputTokens - outputTokens; providerRemainder > 0 {
		result += providerRemainder
	}
	return max(0, result)
}

func validScope(scope Scope) bool {
	if scope.ID < 0 {
		return false
	}
	switch strings.TrimSpace(scope.Type) {
	case "workspace":
		return scope.ID == 0
	case "strategy":
		return scope.ID == 0
	case "workstream", "project", "department", "document", "task":
		return scope.ID > 0
	default:
		return false
	}
}

func validAttachment(attachment Attachment) bool {
	if strings.TrimSpace(attachment.Label) == "" || len([]rune(attachment.Label)) > 300 {
		return false
	}
	switch strings.TrimSpace(attachment.Type) {
	case "knowledge_document":
		return strings.TrimSpace(attachment.Key) != ""
	case "workspace_document", "department", "uploaded_file":
		return attachment.ID > 0
	default:
		return false
	}
}

func (s *Service) businessBrief(ctx context.Context, workspaceID int, userID int) (string, error) {
	var workspaceName string
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(display_name, ''), name) FROM workspaces WHERE id=$1
	`, workspaceID).Scan(&workspaceName); err != nil {
		return "", err
	}
	var memory json.RawMessage
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT snapshot_json FROM strategic_memory_snapshots
		WHERE workspace_id=$1 ORDER BY version DESC, id DESC LIMIT 1
	`, workspaceID).Scan(&memory)
	current, err := s.tactics.Current(ctx, workspaceID, userID)
	if err != nil {
		return "", err
	}
	type briefDirection struct {
		ID              int               `json:"id"`
		Title           string            `json:"title"`
		MainValue       string            `json:"main_value"`
		Description     string            `json:"description"`
		ManagerUserID   *int              `json:"manager_user_id,omitempty"`
		KPIs            []departments.KPI `json:"kpis"`
		Status          string            `json:"status"`
		ActiveTaskCount int               `json:"active_task_count"`
	}
	departmentItems, err := departments.NewStore(s.dbx).List(ctx, workspaceID, false)
	if err != nil {
		return "", err
	}
	directions := make([]briefDirection, 0, min(len(departmentItems), 10))
	for _, department := range departmentItems {
		if len(directions) >= 10 {
			break
		}
		directions = append(directions, briefDirection{
			ID: department.ID, Title: truncateRunes(department.Name, 240),
			MainValue:     truncateRunes(department.Responsibility, 800),
			Description:   truncateRunes(department.Description, 1400),
			ManagerUserID: department.ManagerUserID, KPIs: department.KPIs,
			Status: department.Status, ActiveTaskCount: department.ActiveTaskCount,
		})
	}
	var tasksRaw json.RawMessage
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(jsonb_agg(item), '[]'::jsonb)
		FROM (
			SELECT task.id, LEFT(task.title, 300) AS title, task.status, task.department_id,
				LEFT(COALESCE(direction.name, ''), 240) AS direction_name,
				COALESCE(evaluation.priority_score, 0) AS priority_score,
				COALESCE(evaluation.priority_tier, '') AS priority_tier,
				task.blocked, task.due_date
			FROM v2_tasks task
			LEFT JOIN v2_departments direction
				ON direction.workspace_id=task.workspace_id AND direction.id=task.department_id
			LEFT JOIN LATERAL (
				SELECT priority_score, priority_tier
				FROM v2_task_evaluations
				WHERE workspace_id=task.workspace_id AND task_id=task.id
				ORDER BY created_at DESC, id DESC LIMIT 1
			) evaluation ON TRUE
			WHERE task.workspace_id=$1 AND task.archived_at IS NULL
			ORDER BY COALESCE(evaluation.priority_score, 0) DESC, task.updated_at DESC
			LIMIT 12
		) item
	`, workspaceID).Scan(&tasksRaw)
	var memoryValue any = map[string]any{
		"available_via_file_search": true,
		"note":                      "The full company memory is available through document search.",
	}
	if len(memory) > 0 && len(memory) <= 6000 {
		var parsed any
		if json.Unmarshal(memory, &parsed) == nil {
			memoryValue = parsed
		}
	}
	strategy := current.Strategy
	if strategy != nil {
		compact := *strategy
		compact.Title = truncateRunes(compact.Title, 300)
		compact.Summary = truncateRunes(compact.Summary, 3000)
		strategy = &compact
	}
	course := current.Course
	if course != nil {
		compact := *course
		compact.Title = truncateRunes(compact.Title, 300)
		compact.Direction = truncateRunes(compact.Direction, 800)
		compact.StrategicGoal = truncateRunes(compact.StrategicGoal, 1000)
		compact.Meaning = truncateRunes(compact.Meaning, 800)
		compact.KeyMetric = truncateRunes(compact.KeyMetric, 500)
		compact.SuccessCriterion = truncateRunes(compact.SuccessCriterion, 800)
		course = &compact
	}
	payload := map[string]any{
		"workspace":        map[string]any{"id": workspaceID, "name": workspaceName},
		"strategic_memory": memoryValue,
		"strategy":         strategy,
		"course":           course,
		"directions":       directions,
		"top_tasks":        json.RawMessage(tasksRaw),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if len(raw) <= 12000 {
		return string(raw), nil
	}
	payload["directions"] = directions[:min(len(directions), 5)]
	payload["top_tasks"] = json.RawMessage("[]")
	payload["strategic_memory"] = map[string]any{
		"available_via_file_search": true,
		"note":                      "Compact brief was reduced; retrieve evidence only when it is relevant.",
	}
	raw, err = json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *Service) internalRun(ctx context.Context, publicID string, token string) (Run, error) {
	claims, err := verifyRunToken(s.secret, token, publicID)
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.ByPublicID(ctx, publicID)
	if err != nil {
		return Run{}, err
	}
	if run.WorkspaceID != claims.WorkspaceID || run.UserID != claims.UserID {
		return Run{}, errors.New("invalid_agent_run_token")
	}
	return run, nil
}

func (s *Service) RuntimeEvent(ctx context.Context, publicID string, token string, event RuntimeEvent) error {
	run, err := s.internalRun(ctx, publicID, token)
	if err != nil {
		return err
	}
	return s.store.InsertEvent(ctx, run.ID, event)
}

func (s *Service) ExecuteTool(
	ctx context.Context,
	toolName string,
	token string,
	request ToolRequest,
) (any, error) {
	run, err := s.internalRun(ctx, request.RunID, token)
	if err != nil {
		return nil, err
	}
	if request.ToolCallID == "" {
		return nil, errors.New("agent_tool_call_id_required")
	}
	if isApprovalTool(toolName) {
		return s.approvedToolResult(ctx, run, toolName, request.ToolCallID)
	}
	return s.readTool(ctx, run, toolName, request.Input)
}

func (s *Service) approvedToolResult(ctx context.Context, run Run, toolName string, callID string) (any, error) {
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range approvals {
		if item.CallID != callID || item.ToolName != toolName {
			continue
		}
		if item.Status != "applied" {
			return nil, errors.New("agent_action_not_applied")
		}
		var result any
		if err := json.Unmarshal(item.Result, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, errors.New("agent_approval_not_found")
}

func (s *Service) DebugString() string {
	return fmt.Sprintf("enabled=%t model=%s release=%s", s.Enabled(), s.model, s.releaseID)
}

func compatibleSession(session ThreadSession, releaseID string, model string, promptVersion string) bool {
	return session.Found &&
		strings.TrimSpace(session.AgentReleaseID) == strings.TrimSpace(releaseID) &&
		strings.TrimSpace(session.Model) == strings.TrimSpace(model) &&
		strings.TrimSpace(session.PromptVersion) == strings.TrimSpace(promptVersion)
}

const (
	continuityMaxMessages = 32
	continuityMaxRunes    = 16000
)

func buildContinuityContext(messages []tactics.TacticsChatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	start := 0
	if len(messages) > continuityMaxMessages {
		start = len(messages) - continuityMaxMessages
	}
	lines := make([]string, 0, len(messages)-start)
	total := 0
	for index := len(messages) - 1; index >= start; index-- {
		item := messages[index]
		content := truncateRunes(strings.TrimSpace(item.Content), 2400)
		if content == "" {
			continue
		}
		role := "Пользователь"
		if item.Role == "assistant" {
			role = "Советник"
		}
		line := role + ": " + content
		lineLength := len([]rune(line))
		if total+lineLength > continuityMaxRunes {
			continue
		}
		lines = append(lines, line)
		total += lineLength
	}
	if len(lines) == 0 {
		return ""
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n\n")
}

func proposalMessageIDForRun(publicID string, messages []tactics.TacticsChatMessage) int {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return 0
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == "assistant" && message.AgentRunID == publicID &&
			len(message.ProposedChanges) > 0 {
			return message.ID
		}
	}
	return 0
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
