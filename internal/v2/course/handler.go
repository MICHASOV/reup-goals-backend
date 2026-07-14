package course

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	store      *Store
	workspaces *workspaces.Store
}

func NewHandler(dbx *sql.DB) *Handler {
	return &Handler{
		store:      NewStore(dbx),
		workspaces: workspaces.NewStore(dbx),
	}
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/course/current" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	response, err := h.store.Current(r.Context(), workspace.ID, userID)
	if err != nil {
		log.Printf("[ERROR] course current failed workspace_id=%d user_id=%d: %v", workspace.ID, userID, err)
		api.WriteError(w, http.StatusInternalServerError, "course_current_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Course(w http.ResponseWriter, r *http.Request) {
	courseID, action, ok := coursePath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	workspace, _, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case action == "" && r.Method == http.MethodPatch:
		h.updateCourse(w, r, workspace.ID, courseID)
	case action == "activate" && r.Method == http.MethodPost:
		h.activateCourse(w, r, workspace.ID, courseID)
	case action == "refresh" && r.Method == http.MethodPost:
		h.refreshCourse(w, r, workspace.ID, courseID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) updateCourse(w http.ResponseWriter, r *http.Request, workspaceID int, courseID int) {
	var input CourseInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	input.trim()
	if input.Status != "" {
		api.WriteError(w, http.StatusUnprocessableEntity, "course_status_action_required")
		return
	}
	if input.Horizon != nil && *input.Horizon <= 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_horizon")
		return
	}

	course, err := h.store.Update(r.Context(), workspaceID, courseID, input)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "course_update_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"course": course})
}

func (h *Handler) activateCourse(w http.ResponseWriter, r *http.Request, workspaceID int, courseID int) {
	course, err := h.store.Activate(r.Context(), workspaceID, courseID)
	if h.writeCourseActionError(w, err) {
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"course": course})
}

func (h *Handler) refreshCourse(w http.ResponseWriter, r *http.Request, workspaceID int, courseID int) {
	course, err := h.store.Refresh(r.Context(), workspaceID, courseID)
	if h.writeCourseActionError(w, err) {
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"course": course})
}

func (h *Handler) writeCourseActionError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.WriteError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrCourseIncomplete):
		api.WriteError(w, http.StatusUnprocessableEntity, "course_incomplete")
	case errors.Is(err, ErrCourseStrategyStale):
		api.WriteError(w, http.StatusConflict, "course_strategy_stale")
	case errors.Is(err, ErrCourseStrategyMismatch):
		api.WriteError(w, http.StatusConflict, "course_strategy_mismatch")
	case errors.Is(err, ErrCourseArtifactsMissing):
		api.WriteError(w, http.StatusConflict, "course_strategy_artifacts_missing")
	default:
		log.Printf("[ERROR] course action failed: %v", err)
		api.WriteError(w, http.StatusInternalServerError, "course_action_failed")
	}
	return true
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, int, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, 0, false
	}
	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), uid)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return workspaces.Workspace{}, 0, false
	}
	return workspace, uid, true
}

func coursePath(path string) (int, string, bool) {
	const prefix = "/api/v2/course/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}

	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" {
		return 0, "", false
	}

	parts := strings.Split(value, "/")
	if len(parts) > 2 || parts[0] == "" {
		return 0, "", false
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		return 0, "", false
	}

	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	return id, action, true
}
