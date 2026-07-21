package departments

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

func (h *Handler) Departments(w http.ResponseWriter, r *http.Request) {
	workspace, membership, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	if err := h.store.EnsureDefault(r.Context(), workspace.ID, workspace.OwnerUserID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "department_default_failed")
		return
	}

	if r.URL.Path == "/api/v2/departments" {
		switch r.Method {
		case http.MethodGet:
			items, err := h.store.List(r.Context(), workspace.ID, r.URL.Query().Get("include_archived") == "true")
			if err != nil {
				api.WriteError(w, http.StatusInternalServerError, "departments_list_failed")
				return
			}
			api.WriteJSON(w, http.StatusOK, map[string]any{"departments": items})
		case http.MethodPost:
			if !canManage(membership) {
				api.WriteError(w, http.StatusForbidden, "forbidden")
				return
			}
			var input Input
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				api.WriteError(w, http.StatusBadRequest, "invalid_json")
				return
			}
			item, err := h.store.Create(r.Context(), workspace.ID, membership.UserID, input)
			if writeDepartmentError(w, err) {
				return
			}
			api.WriteJSON(w, http.StatusCreated, item)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}

	departmentID, ok := numericSuffix(r.URL.Path, "/api/v2/departments/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := h.store.Get(r.Context(), workspace.ID, departmentID)
		if writeDepartmentError(w, err) {
			return
		}
		api.WriteJSON(w, http.StatusOK, item)
	case http.MethodPatch:
		if !canManage(membership) {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		var input Input
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		item, err := h.store.Update(r.Context(), workspace.ID, departmentID, input)
		if writeDepartmentError(w, err) {
			return
		}
		api.WriteJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManage(membership) {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		if writeDepartmentError(w, h.store.Archive(r.Context(), workspace.ID, departmentID)) {
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) Responsibilities(w http.ResponseWriter, r *http.Request) {
	workspace, membership, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := h.store.ListResponsibilities(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "responsibilities_list_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"responsibilities": items})
	case http.MethodPut:
		if !canManage(membership) {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		var input Responsibility
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		item, err := h.store.SetResponsibility(r.Context(), workspace.ID, input)
		if writeDepartmentError(w, err) {
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"responsibility": item})
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

func canManage(membership workspaces.Membership) bool {
	return membership.Role == workspaces.MembershipRoleOwner
}

func writeDepartmentError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.WriteError(w, http.StatusNotFound, "department_not_found")
	case errors.Is(err, ErrInvalidDepartment), errors.Is(err, ErrInvalidMember), errors.Is(err, ErrInvalidResponsibility):
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrDuplicateDepartment):
		api.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrDepartmentInUse), errors.Is(err, ErrLastDepartment):
		api.WriteError(w, http.StatusConflict, err.Error())
	default:
		api.WriteError(w, http.StatusInternalServerError, "department_operation_failed")
	}
	return true
}

func numericSuffix(path string, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, false
	}
	id, err := strconv.Atoi(value)
	return id, err == nil && id > 0
}
