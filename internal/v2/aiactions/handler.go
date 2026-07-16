package aiactions

import (
	"database/sql"
	"encoding/json"
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
	return &Handler{store: NewStore(dbx), workspaces: workspaces.NewStore(dbx)}
}

func (h *Handler) Actions(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return
	}

	if r.URL.Path == "/api/v2/ai-actions" {
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		messageID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("message_id")))
		items, err := h.store.List(r.Context(), workspace.ID, r.URL.Query().Get("scenario"), messageID, 500)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "ai_actions_list_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspace.ID, "actions": items})
		return
	}

	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	actionID, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/ai-actions/"), "/"), 10, 64)
	if err != nil || actionID <= 0 {
		api.WriteError(w, http.StatusNotFound, "ai_action_not_found")
		return
	}
	var request UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := h.store.Update(r.Context(), workspace.ID, actionID, userID, request)
	if err != nil {
		if err == sql.ErrNoRows {
			api.WriteError(w, http.StatusConflict, "ai_action_not_editable")
			return
		}
		switch err.Error() {
		case "invalid_ai_action_transition", "invalid_ai_action_payload":
			api.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			api.WriteError(w, http.StatusInternalServerError, "ai_action_update_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspace.ID, "action": item})
}
