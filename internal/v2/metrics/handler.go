package metrics

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

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch {
	case r.URL.Path == "/api/v2/metrics/catalog":
		h.catalog(w, r, workspace.ID)
	case r.URL.Path == "/api/v2/metrics/targets":
		h.targets(w, r, workspace.ID, userID)
	case strings.HasPrefix(r.URL.Path, "/api/v2/metrics/targets/"):
		h.target(w, r, workspace.ID, userID)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request, workspaceID int) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	definitions, err := h.store.Definitions(r.Context(), workspaceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "metric_catalog_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"templates":         Catalog(r.URL.Query().Get("q"), r.URL.Query().Get("category")),
		"categories":        Categories(),
		"workspace_metrics": definitions,
	})
}

func (h *Handler) targets(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	switch r.Method {
	case http.MethodGet:
		scopeType := strings.TrimSpace(r.URL.Query().Get("scope_type"))
		scopeID, _ := strconv.Atoi(r.URL.Query().Get("scope_id"))
		if scopeType != "" && (!validScope(scopeType) || (scopeType != ScopeWorkspace && scopeID <= 0)) {
			api.WriteError(w, http.StatusBadRequest, "invalid_metric_scope")
			return
		}
		items, err := h.store.Targets(r.Context(), workspaceID, scopeType, scopeID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "metric_targets_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"targets": items})
	case http.MethodPost:
		input, ok := decodeTargetInput(w, r)
		if !ok {
			return
		}
		applyTargetDefaults(&input)
		if !validTargetInput(input) {
			api.WriteError(w, http.StatusBadRequest, "invalid_metric_target")
			return
		}
		item, err := h.store.CreateTarget(r.Context(), workspaceID, userID, input)
		writeMetricEntity(w, err, http.StatusCreated, "target", item, "metric_target_create_failed")
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) target(w http.ResponseWriter, r *http.Request, workspaceID int, userID int) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/metrics/targets/"), "/")
	if strings.HasSuffix(path, "/observations") {
		idText := strings.TrimSuffix(path, "/observations")
		targetID, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || targetID <= 0 {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		var input ObservationInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		if input.Confidence < 0 || input.Confidence > 1000 || !validObservationSource(input.SourceType) {
			api.WriteError(w, http.StatusBadRequest, "invalid_metric_observation")
			return
		}
		item, err := h.store.AddObservation(r.Context(), workspaceID, userID, targetID, input)
		writeMetricEntity(w, err, http.StatusCreated, "observation", item, "metric_observation_create_failed")
		return
	}

	targetID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || targetID <= 0 {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		input, ok := decodeTargetInput(w, r)
		if !ok {
			return
		}
		if (input.Role != "" && !validRole(input.Role)) || (input.Cadence != "" && !validCadence(input.Cadence)) {
			api.WriteError(w, http.StatusBadRequest, "invalid_metric_target")
			return
		}
		item, err := h.store.UpdateTarget(r.Context(), workspaceID, targetID, input)
		writeMetricEntity(w, err, http.StatusOK, "target", item, "metric_target_update_failed")
	case http.MethodDelete:
		err := h.store.ArchiveTarget(r.Context(), workspaceID, targetID)
		if errors.Is(err, sql.ErrNoRows) {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "metric_target_archive_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) currentWorkspace(w http.ResponseWriter, r *http.Request) (workspaces.Workspace, int, bool) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return workspaces.Workspace{}, 0, false
	}
	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return workspaces.Workspace{}, 0, false
	}
	return workspace, userID, true
}

func decodeTargetInput(w http.ResponseWriter, r *http.Request) (TargetInput, bool) {
	var input TargetInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return TargetInput{}, false
	}
	normalizeTargetInput(&input)
	return input, true
}

func validTargetInput(input TargetInput) bool {
	hasMetric := input.MetricID > 0 || input.TemplateKey != "" || input.Name != ""
	return hasMetric && validScope(input.ScopeType) &&
		(input.ScopeType == ScopeWorkspace || input.ScopeID > 0) &&
		validRole(input.Role) && validCadence(input.Cadence) &&
		(input.ValueType == "" || validValueType(input.ValueType)) &&
		(input.BetterDirection == "" || validDirection(input.BetterDirection))
}

func validScope(value string) bool {
	return value == ScopeWorkspace || value == ScopeStrategy || value == ScopeWorkstream || value == ScopeProject
}

func validRole(value string) bool {
	return value == RolePrimary || value == RoleGuardrail || value == RoleSupporting
}

func validCadence(value string) bool {
	return value == "daily" || value == "weekly" || value == "monthly" ||
		value == "quarterly" || value == "on_demand"
}

func validValueType(value string) bool {
	return value == "number" || value == "percent" || value == "currency" ||
		value == "duration" || value == "ratio"
}

func validDirection(value string) bool {
	return value == "increase" || value == "decrease" || value == "range"
}

func validObservationSource(value string) bool {
	return value == "" || value == "manual" || value == "task_result" ||
		value == "integration" || value == "ai_suggestion"
}

func writeMetricEntity(w http.ResponseWriter, err error, status int, key string, value any, internalCode string) {
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, internalCode)
		return
	}
	api.WriteJSON(w, status, map[string]any{key: value})
}
