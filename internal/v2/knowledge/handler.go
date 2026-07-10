package knowledge

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

func (h *Handler) Blocks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/knowledge-base/blocks" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	blocks, err := h.store.List(r.Context(), workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_blocks_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspace.ID,
		"blocks":       blocks,
	})
}

func (h *Handler) Block(w http.ResponseWriter, r *http.Request) {
	blockID, ok := blockIDFromPath(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}

	workspace, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBlock(w, r, workspace.ID, blockID)
	case http.MethodPatch:
		h.updateBlock(w, r, workspace.ID, blockID)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) getBlock(w http.ResponseWriter, r *http.Request, workspaceID int, blockID int) {
	block, err := h.store.Get(r.Context(), workspaceID, blockID)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_block_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"block": block})
}

func (h *Handler) updateBlock(w http.ResponseWriter, r *http.Request, workspaceID int, blockID int) {
	var body struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	body.Status = strings.TrimSpace(body.Status)
	if body.Status != "" && !ValidStatus(body.Status) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}

	block, err := h.store.Update(r.Context(), workspaceID, blockID, body.Content, body.Status)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "knowledge_block_update_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"block": block})
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, false
	}

	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), uid)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return workspaces.Workspace{}, false
	}

	return workspace, true
}

func blockIDFromPath(path string) (int, bool) {
	const prefix = "/api/v2/knowledge-base/blocks/"
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
