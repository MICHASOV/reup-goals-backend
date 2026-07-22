package tasks

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	store      *Store
	workspaces *workspaces.Store
	brainstorm *BrainstormService
	evaluator  *TaskEvaluatorService
}

func (h *Handler) WithContextIndex(index *contextindex.Service) *Handler {
	h.brainstorm.SetContextIndex(index)
	return h
}

func NewHandler(dbx *sql.DB, brainstormAI ai.Provider, evaluatorAI ai.Provider, compactThreshold int) *Handler {
	evaluator := NewTaskEvaluatorService(dbx, evaluatorAI)
	evaluator.StartWorker()
	return &Handler{
		store:      NewStore(dbx),
		workspaces: workspaces.NewStore(dbx),
		brainstorm: NewBrainstormService(dbx, brainstormAI, evaluator, compactThreshold),
		evaluator:  evaluator,
	}
}

func (h *Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case r.URL.Path == "/api/v2/tasks":
		h.tasks(w, r, workspace.ID, userID)
	case r.URL.Path == "/api/v2/tasks/overview":
		h.overview(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/tasks/brainstorm":
		h.brainstormHistory(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/tasks/brainstorm/messages":
		h.brainstormMessage(w, r, workspace.ID, userID)
	case r.URL.Path == "/api/v2/tasks/brainstorm/actions/apply":
		h.applyBrainstormActions(w, r, workspace.ID, userID)
	case strings.HasPrefix(r.URL.Path, "/api/v2/tasks/workstreams/"):
		h.workstream(w, r, workspace.ID)
	case strings.HasPrefix(r.URL.Path, "/api/v2/tasks/"):
		h.task(w, r, workspace.ID, userID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) brainstormHistory(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workstreamID, ok := intQuery(r, "workstream_id")
	if !ok {
		api.WriteError(w, http.StatusBadRequest, "invalid_workstream_id")
		return
	}
	response, err := h.brainstorm.History(r.Context(), workspaceID, workstreamID)
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "task_brainstorm_history_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) brainstormMessage(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var request BrainstormMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	response, err := h.brainstorm.HandleMessage(r.Context(), workspaceID, userID, request)
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		switch err.Error() {
		case "invalid_brainstorm_message", "brainstorm_message_too_long":
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			api.WriteError(w, http.StatusBadGateway, "task_brainstorm_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) applyBrainstormActions(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var request ApplyBrainstormActionsRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	response, err := h.brainstorm.ApplyActions(r.Context(), workspaceID, userID, request)
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		if err.Error() == "brainstorm_action_not_confirmable" {
			api.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if strings.HasPrefix(err.Error(), "invalid_brainstorm_") {
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		api.WriteError(w, http.StatusInternalServerError, "task_brainstorm_apply_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	response, err := h.store.Overview(r.Context(), workspaceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "tasks_overview_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) workstream(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workstreamID, ok := numericSuffix(r.URL.Path, "/api/v2/tasks/workstreams/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	response, err := h.store.Workstream(r.Context(), workspaceID, workstreamID)
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "tasks_workstream_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) tasks(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	switch r.Method {
	case http.MethodGet:
		page := api.ParsePagination(r, 100, 200)
		filter := ListFilter{
			IncludeArchived: r.URL.Query().Get("include_archived") == "true",
			Query:           strings.TrimSpace(r.URL.Query().Get("q")),
			Limit:           page.Limit,
			Offset:          page.Offset,
		}
		if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
			if !ValidStatus(status) {
				api.WriteError(w, http.StatusBadRequest, "invalid_status")
				return
			}
			filter.Status = &status
		}
		if workstreamID, ok := intQuery(r, "workstream_id"); ok {
			filter.WorkstreamID = &workstreamID
		}
		if departmentID, ok := intQuery(r, "department_id"); ok {
			filter.DepartmentID = &departmentID
		}
		if projectID, ok := intQuery(r, "project_id"); ok {
			filter.ProjectID = &projectID
		}
		tasks, err := h.store.List(r.Context(), workspaceID, filter)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tasks_list_failed")
			return
		}
		total, err := h.store.Count(r.Context(), workspaceID, filter)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tasks_count_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "pagination": api.PaginationMeta(page, total)})
	case http.MethodPost:
		input, ok := decodeTaskInput(w, r)
		if !ok {
			return
		}
		task, err := h.store.Create(r.Context(), workspaceID, userID, input)
		if err == nil {
			_ = h.evaluator.Queue(r.Context(), workspaceID, userID, task.ID, true)
			task, _ = h.store.Get(r.Context(), workspaceID, task.ID)
		}
		writeTask(w, task, err, "task_create_failed")
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) task(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/v2/tasks/")
	if strings.HasSuffix(trimmed, "/evaluate") {
		idPart := strings.TrimSuffix(trimmed, "/evaluate")
		taskID, err := strconv.Atoi(strings.Trim(idPart, "/"))
		if err != nil || taskID <= 0 {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		h.evaluateTask(w, r, workspaceID, userID, taskID)
		return
	}
	if strings.HasSuffix(trimmed, "/priority") {
		idPart := strings.TrimSuffix(trimmed, "/priority")
		taskID, err := strconv.Atoi(strings.Trim(idPart, "/"))
		if err != nil || taskID <= 0 {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		h.manualPriority(w, r, workspaceID, userID, taskID)
		return
	}
	if strings.HasSuffix(trimmed, "/status") {
		idPart := strings.TrimSuffix(trimmed, "/status")
		taskID, err := strconv.Atoi(strings.Trim(idPart, "/"))
		if err != nil || taskID <= 0 {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		h.updateStatus(w, r, workspaceID, userID, taskID)
		return
	}
	if strings.HasSuffix(trimmed, "/move") {
		idPart := strings.TrimSuffix(trimmed, "/move")
		taskID, err := strconv.Atoi(strings.Trim(idPart, "/"))
		if err != nil || taskID <= 0 {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		h.updateStatus(w, r, workspaceID, userID, taskID)
		return
	}

	taskID, err := strconv.Atoi(strings.Trim(trimmed, "/"))
	if err != nil || taskID <= 0 {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		task, err := h.store.Get(r.Context(), workspaceID, taskID)
		writeTask(w, task, err, "task_get_failed")
	case http.MethodPatch:
		input, ok := decodeTaskInput(w, r)
		if !ok {
			return
		}
		current, err := h.store.Get(r.Context(), workspaceID, taskID)
		if err != nil {
			writeTask(w, current, err, "task_get_failed")
			return
		}
		task, err := h.store.Update(r.Context(), workspaceID, userID, taskID, input)
		if err == nil && taskEvaluationInputChanged(current, task) {
			if queueErr := h.evaluator.Queue(r.Context(), workspaceID, userID, taskID, true); queueErr == nil {
				task, _ = h.store.Get(r.Context(), workspaceID, taskID)
			}
		}
		writeTask(w, task, err, "task_update_failed")
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func taskEvaluationInputChanged(before Task, after Task) bool {
	return before.Title != after.Title ||
		before.Description != after.Description ||
		before.ExpectedResult != after.ExpectedResult ||
		before.SuccessCriteria != after.SuccessCriteria ||
		before.WorkstreamID != after.WorkstreamID ||
		!sameOptionalInt(before.ProjectID, after.ProjectID) ||
		!sameIntSet(blockingTaskIDs(before.BlockingTasks), blockingTaskIDs(after.BlockingTasks))
}

func sameIntSet(left []int, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameOptionalInt(left *int, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (h *Handler) evaluateTask(w http.ResponseWriter, r *http.Request, workspaceID int, userID int, taskID int) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	task, err := h.store.Get(r.Context(), workspaceID, taskID)
	if err != nil {
		writeTask(w, task, err, "task_get_failed")
		return
	}
	if task.Evaluation != nil || task.EvaluationStatus == EvaluationQueued || task.EvaluationStatus == EvaluationRunning {
		api.WriteJSON(w, http.StatusOK, map[string]any{"task": task})
		return
	}
	if err := h.evaluator.Queue(r.Context(), workspaceID, userID, taskID, false); err != nil {
		writeTask(w, Task{}, err, "task_evaluation_queue_failed")
		return
	}
	task, err = h.store.Get(r.Context(), workspaceID, taskID)
	writeTask(w, task, err, "task_get_failed")
}

func (h *Handler) manualPriority(w http.ResponseWriter, r *http.Request, workspaceID int, userID int, taskID int) {
	api.WriteError(w, http.StatusConflict, "task_priority_is_ai_managed")
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request, workspaceID int, userID int, taskID int) {
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var body struct {
		Status        string `json:"status"`
		PriorityOrder *int   `json:"priority_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !ValidStatus(body.Status) {
		api.WriteError(w, http.StatusBadRequest, "invalid_status")
		return
	}
	task, err := h.store.UpdateStatus(r.Context(), workspaceID, userID, taskID, body.Status, body.PriorityOrder)
	writeTask(w, task, err, "task_status_failed")
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, int, bool) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, 0, false
	}
	workspace, membership, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_unavailable")
		return workspaces.Workspace{}, 0, false
	}
	if membership.UserID != userID || membership.WorkspaceID != workspace.ID {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return workspaces.Workspace{}, 0, false
	}
	return workspace, userID, true
}

func decodeTaskInput(w http.ResponseWriter, r *http.Request) (TaskInput, bool) {
	var input TaskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return TaskInput{}, false
	}
	input.normalize()
	return input, true
}

func writeTask(w http.ResponseWriter, task Task, err error, fallback string) {
	if errors.Is(err, ErrNoActiveCourse) {
		api.WriteError(w, http.StatusConflict, "no_active_course")
		return
	}
	if errors.Is(err, ErrNoTacticalPlan) {
		api.WriteError(w, http.StatusConflict, "no_tactical_plan")
		return
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.Is(err, ErrInvalidOwner) {
		api.WriteError(w, http.StatusUnprocessableEntity, ErrInvalidOwner.Error())
		return
	}
	if errors.Is(err, ErrInvalidInput) {
		api.WriteError(w, http.StatusUnprocessableEntity, ErrInvalidInput.Error())
		return
	}
	if errors.Is(err, ErrInvalidDependency) || errors.Is(err, ErrDependencyCycle) {
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, fallback)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"task": task})
}

func numericSuffix(path string, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	value, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(path, prefix), "/"))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func intQuery(r *http.Request, key string) (int, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed > 0
}
