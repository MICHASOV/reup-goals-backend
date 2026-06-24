package strategy

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	if r.URL.Path != "/api/v2/strategy/current" {
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

	strategy, artifacts, summary, err := h.store.Current(r.Context(), workspace.ID, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_current_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{
		"strategy":       strategy,
		"artifacts":      artifacts,
		"knowledge_base": summary,
	})
}

func (h *Handler) Strategy(w http.ResponseWriter, r *http.Request) {
	strategyID, action, ok := strategyPath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case action == "" && r.Method == http.MethodPatch:
		h.updateStrategy(w, r, workspace.ID, strategyID)
	case action == "activate" && r.Method == http.MethodPost:
		h.activateStrategy(w, r, workspace.ID, userID, strategyID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) Artifacts(w http.ResponseWriter, r *http.Request) {
	artifactID, ok := artifactPath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, _, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	var body struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	body.Status = strings.TrimSpace(body.Status)
	if body.Status != "" && !ValidArtifactStatus(body.Status) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}

	artifact, err := h.store.UpdateArtifact(r.Context(), workspace.ID, artifactID, body.Content, body.Status)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_artifact_update_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"artifact": artifact})
}

func (h *Handler) updateStrategy(w http.ResponseWriter, r *http.Request, workspaceID int, strategyID int) {
	var body struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	strategy, err := h.store.Update(r.Context(), workspaceID, strategyID, body.Title, body.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_update_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"strategy": strategy})
}

func (h *Handler) activateStrategy(w http.ResponseWriter, r *http.Request, workspaceID int, userID int, strategyID int) {
	strategy, err := h.store.Activate(r.Context(), workspaceID, strategyID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_activate_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"strategy": strategy})
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

func strategyPath(path string) (int, string, bool) {
	const prefix = "/api/v2/strategy/"
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

func artifactPath(path string) (int, bool) {
	const prefix = "/api/v2/strategy/artifacts/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}

	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, false
	}

	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, false
	}

	return id, true
}
