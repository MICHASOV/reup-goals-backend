package workspacedocs

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
	return &Handler{store: NewStore(dbx), workspaces: workspaces.NewStore(dbx)}
}

func (h *Handler) Documents(w http.ResponseWriter, r *http.Request) {
	workspace, membership, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	if r.URL.Path == "/api/v2/workspace-documents" {
		switch r.Method {
		case http.MethodGet:
			documents, err := h.store.List(r.Context(), workspace.ID, r.URL.Query().Get("include_archived") == "true")
			if err != nil {
				api.WriteError(w, http.StatusInternalServerError, "workspace_documents_list_failed")
				return
			}
			api.WriteJSON(w, http.StatusOK, map[string]any{"documents": documents})
		case http.MethodPost:
			if membership.Role != workspaces.MembershipRoleOwner {
				api.WriteError(w, http.StatusForbidden, "forbidden")
				return
			}
			var input Input
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				api.WriteError(w, http.StatusBadRequest, "invalid_json")
				return
			}
			document, err := h.store.Create(r.Context(), workspace.ID, membership.UserID, input)
			if writeDocumentError(w, err) {
				return
			}
			api.WriteJSON(w, http.StatusCreated, map[string]any{"document": document})
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}

	documentID, ok := documentIDSuffix(r.URL.Path)
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		document, err := h.store.Get(r.Context(), workspace.ID, documentID)
		if writeDocumentError(w, err) {
			return
		}
		versions, err := h.store.Versions(r.Context(), workspace.ID, documentID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "workspace_document_versions_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"document": document, "versions": versions})
	case http.MethodPatch:
		if membership.Role != workspaces.MembershipRoleOwner {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		var input Input
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		document, err := h.store.Update(r.Context(), workspace.ID, membership.UserID, documentID, input)
		if writeDocumentError(w, err) {
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"document": document})
	case http.MethodDelete:
		if membership.Role != workspaces.MembershipRoleOwner {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		document, err := h.store.Archive(r.Context(), workspace.ID, membership.UserID, documentID)
		if writeDocumentError(w, err) {
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"document": document})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, workspaces.Membership, bool) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, workspaces.Membership{}, false
	}
	workspace, membership, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_unavailable")
		return workspaces.Workspace{}, workspaces.Membership{}, false
	}
	return workspace, membership, true
}

func documentIDSuffix(path string) (int64, bool) {
	const prefix = "/api/v2/workspace-documents/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}

func writeDocumentError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.WriteError(w, http.StatusNotFound, "workspace_document_not_found")
	case errors.Is(err, ErrInvalidDocument), errors.Is(err, ErrInvalidLink), errors.Is(err, ErrInvalidParent):
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		api.WriteError(w, http.StatusInternalServerError, "workspace_document_operation_failed")
	}
	return true
}
