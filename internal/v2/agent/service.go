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

	"reup-goals-backend/internal/v2/billing"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/tactics"
	"reup-goals-backend/internal/v2/workspaces"
)

type Service struct {
	enabled      bool
	model        string
	secret       string
	maxTurns     int
	dbx          *sql.DB
	store        *Store
	tactics      *tactics.Store
	tacticsApply *tactics.Handler
	workspaces   *workspaces.Store
	contextIndex *contextindex.Service
	runtime      *RuntimeClient
	jobs         *jobs.Manager
	billing      *billing.Service
}

type ServiceConfig struct {
	Enabled  bool
	Model    string
	Secret   string
	MaxTurns int
}

type agentJobPayload struct {
	RunID string `json:"run_id"`
}

func NewService(
	dbx *sql.DB,
	cfg ServiceConfig,
	runtime *RuntimeClient,
	jobManager *jobs.Manager,
	quota *billing.Service,
	contextIndex *contextindex.Service,
	tacticsHandler *tactics.Handler,
) *Service {
	service := &Service{
		enabled: cfg.Enabled, model: cfg.Model, secret: cfg.Secret, maxTurns: cfg.MaxTurns,
		dbx: dbx, store: NewStore(dbx), tactics: tactics.NewStore(dbx),
		tacticsApply: tacticsHandler, workspaces: workspaces.NewStore(dbx),
		contextIndex: contextIndex, runtime: runtime, jobs: jobManager, billing: quota,
	}
	if service.maxTurns <= 0 {
		service.maxTurns = 12
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
	workspace, membership, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Run{}, err
	}
	thread, err := s.tactics.AdvisorThread(ctx, workspace.ID, userID, request.ThreadID)
	if err != nil {
		return Run{}, err
	}
	if _, err := s.store.ActiveForThread(ctx, workspace.ID, userID, thread.ID); err == nil {
		return Run{}, errors.New("agent_run_already_active")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, err
	}
	scope := request.Scope
	if strings.TrimSpace(scope.Type) == "" {
		scope = Scope{Type: thread.ScopeType, ID: thread.ScopeID, Label: thread.ScopeLabel}
	}
	if !validScope(scope) {
		return Run{}, errors.New("invalid_agent_scope")
	}
	userMessageID, err := s.tactics.CreateScopedChatMessage(
		ctx, workspace.ID, &userID, "user", request.Message,
		map[string]any{"agent_runtime": true},
		thread.ConversationScope(),
	)
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.Create(
		ctx, workspace.ID, userID, thread.ID, userMessageID, scope, s.model,
		agentInput(request.Message, request.Attachments),
	)
	if err != nil {
		return Run{}, err
	}
	_ = s.tactics.TouchAdvisorThread(ctx, workspace.ID, userID, thread.ID, request.Message)
	if _, err := s.jobs.Enqueue(
		ctx, workspace.ID, JobTypeExecute, run.PublicID, agentJobPayload{RunID: run.PublicID}, 3, time.Time{},
	); err != nil {
		_ = s.store.SetFailed(ctx, run.ID, "agent_job_enqueue_failed", true)
		return Run{}, err
	}
	_ = membership
	return s.Hydrate(ctx, run, 0)
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
	return s.Hydrate(ctx, run, 0)
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
			return Run{}, errors.New("agent_action_forbidden")
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
	for _, item := range approvals {
		if item.Status == "approved" {
			approvedIndices = append(approvedIndices, item.ActionIndex)
		}
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
		if item.Status != "approved" {
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
	if err := s.store.QueueResume(ctx, run.ID); err != nil {
		return Run{}, err
	}
	if _, err := s.jobs.Enqueue(
		ctx, workspace.ID, JobTypeResume, run.PublicID, agentJobPayload{RunID: run.PublicID}, 3, time.Time{},
	); err != nil {
		_ = s.store.SetFailed(ctx, run.ID, "agent_resume_enqueue_failed", true)
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
	reservationID, stop, err := s.startBillableRun(ctx, run)
	if err != nil || stop {
		return err
	}
	if err := s.store.SetRunning(ctx, run.ID, reservationID); err != nil {
		_ = s.billing.Settle(context.WithoutCancel(ctx), reservationID, false, 0)
		return err
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
	previousResponseID, conversationID, err := s.store.LatestThreadSession(
		ctx, run.WorkspaceID, run.UserID, run.ThreadID,
	)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
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
		"run_token": runToken, "max_turns": s.maxTurns,
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
	state, err := decryptState(s.secret, run.PublicID, run.StateCiphertext)
	if err != nil || state == "" {
		if err == nil {
			err = errors.New("agent_resume_state_missing")
		}
		_ = s.store.SetFailed(ctx, run.ID, err.Error(), true)
		return nil
	}
	reservationID, stop, err := s.startBillableRun(ctx, run)
	if err != nil || stop {
		return err
	}
	if err := s.store.SetRunning(ctx, run.ID, reservationID); err != nil {
		_ = s.billing.Settle(context.WithoutCancel(ctx), reservationID, false, 0)
		return err
	}
	approvals, err := s.store.Approvals(ctx, run.ID)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	decisions := make([]Decision, 0, len(approvals))
	for _, item := range approvals {
		decisions = append(decisions, Decision{CallID: item.CallID, Approved: item.Status == "applied"})
	}
	runToken, err := signRunToken(s.secret, run, 6*time.Hour)
	if err != nil {
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	started := time.Now()
	result, err := s.runtime.Resume(ctx, map[string]any{
		"run_id": run.PublicID, "model": run.Model, "vector_store_id": run.VectorStoreID,
		"state": state, "run_token": runToken, "decisions": decisions, "max_turns": s.maxTurns,
	})
	if err != nil {
		s.logCall(ctx, run, time.Since(started), RuntimeUsage{}, err)
		return s.failAttempt(ctx, run, reservationID, job, err)
	}
	return s.finishRuntimeResult(ctx, run, reservationID, result, time.Since(started))
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
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	_ = s.billing.Settle(settleCtx, reservationID, false, 0)
	cancel()
	terminal := job.Attempts >= job.MaxAttempts
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

func (s *Service) finishRuntimeResult(
	ctx context.Context,
	run Run,
	reservationID string,
	result RuntimeResult,
	duration time.Duration,
) error {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	settleErr := s.billing.Settle(settleCtx, reservationID, true, quotaTokenUsage(result.Usage))
	cancel()
	if settleErr != nil {
		log.Printf("[ERROR] agent quota settlement failed run_id=%s: %v", run.PublicID, settleErr)
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
	state, err := s.tactics.Current(ctx, run.WorkspaceID, run.UserID)
	if err != nil || state.TacticalPlan == nil {
		if err == nil {
			err = errors.New("tactics_plan_required")
		}
		return err
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
	if err := s.tactics.RegisterTacticsActions(
		ctx, run.WorkspaceID, state.TacticalPlan.ID, assistantMessageID, changes,
	); err != nil {
		return err
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

func (s *Service) logCall(ctx context.Context, run Run, duration time.Duration, usage RuntimeUsage, callErr error) {
	status := "success"
	errorText := ""
	if callErr != nil {
		status = "failed"
		errorText = callErr.Error()
	}
	_, _ = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_ai_call_logs (
			workspace_id, user_id, ai_module, prompt_name, prompt_version, provider, model,
			status, error, latency_ms, token_usage_input, token_usage_output, token_usage_total,
			cached_input_tokens, estimated_cost, request_id, response_id
		)
		VALUES ($1, $2, 'executive_advisor', 'executive_advisor', $3, 'openai', $4,
			$5, $6, $7, $8, $9, $10, $11, 0, '', '')
	`, run.WorkspaceID, run.UserID, PromptVersion, run.Model, status, truncate(errorText, 4000),
		duration.Milliseconds(), usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CachedInputTokens)
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
	case "strategy", "workstream", "project", "department", "document", "task":
		return scope.ID > 0
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
	type briefProject struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		Metric string `json:"metric,omitempty"`
	}
	type briefDirection struct {
		ID       int            `json:"id"`
		Title    string         `json:"title"`
		Goal     string         `json:"goal"`
		CKP      string         `json:"ckp"`
		Status   string         `json:"status"`
		Projects []briefProject `json:"projects"`
	}
	directions := make([]briefDirection, 0, min(len(current.Workstreams), 10))
	for _, workstream := range current.Workstreams {
		if len(directions) >= 10 {
			break
		}
		item := briefDirection{
			ID: workstream.ID, Title: workstream.Title, Goal: workstream.Goal,
			CKP: workstream.CKP, Status: workstream.Status, Projects: []briefProject{},
		}
		for _, project := range workstream.Projects {
			if len(item.Projects) >= 5 {
				break
			}
			item.Projects = append(item.Projects, briefProject{
				ID: project.ID, Title: project.Title, Status: project.Status, Metric: project.MetricName,
			})
		}
		directions = append(directions, item)
	}
	var tasksRaw json.RawMessage
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(jsonb_agg(item), '[]'::jsonb)
		FROM (
			SELECT task.id, task.title, task.status, task.project_id, task.workstream_id,
				COALESCE(evaluation.priority_score, 0) AS priority_score,
				COALESCE(evaluation.priority_tier, '') AS priority_tier,
				task.blocked, task.due_date
			FROM v2_tasks task
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
	if len(memory) > 0 && len(memory) <= 8000 {
		var parsed any
		if json.Unmarshal(memory, &parsed) == nil {
			memoryValue = parsed
		}
	}
	payload := map[string]any{
		"workspace":        map[string]any{"id": workspaceID, "name": workspaceName},
		"strategic_memory": memoryValue,
		"strategy":         current.Strategy,
		"course":           current.Course,
		"directions":       directions,
		"top_tasks":        json.RawMessage(tasksRaw),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if len(raw) <= 20000 {
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
	if strings.HasPrefix(toolName, "propose_") {
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
	return fmt.Sprintf("enabled=%t model=%s", s.Enabled(), s.model)
}
