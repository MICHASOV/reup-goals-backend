package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Runs(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.service.Enabled() {
		api.WriteError(w, http.StatusServiceUnavailable, "agent_runtime_disabled")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/advisor/runs"), "/")
	if path == "" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		var body CreateRunRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		run, err := h.service.CreateRun(r.Context(), userID, body)
		if err != nil {
			writeServiceError(w, err, "agent_run_create_failed")
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]any{"run": run})
		return
	}
	if path == "active" {
		if r.Method != http.MethodGet {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		threadID, _ := strconv.Atoi(r.URL.Query().Get("thread_id"))
		run, err := h.service.ActiveRunForThread(r.Context(), userID, threadID)
		if err != nil {
			writeServiceError(w, err, "agent_run_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
		return
	}
	parts := strings.Split(path, "/")
	publicID := parts[0]
	if publicID == "" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "decision" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		var body DecisionRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		run, err := h.service.Decide(r.Context(), userID, publicID, body)
		if err != nil {
			writeServiceError(w, err, "agent_decision_failed")
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]any{"run": run})
		return
	}
	if len(parts) != 1 || r.Method != http.MethodGet {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	afterEventID, _ := strconv.ParseInt(r.URL.Query().Get("after_event_id"), 10, 64)
	run, err := h.service.RunForUser(r.Context(), userID, publicID, afterEventID)
	if err != nil {
		writeServiceError(w, err, "agent_run_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *Handler) InternalEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	publicID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/internal/agent/runs/"), "/")
	publicID = strings.TrimSuffix(publicID, "/events")
	if publicID == "" || !strings.HasSuffix(r.URL.Path, "/events") {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var event RuntimeEvent
	if !decodeJSON(w, r, &event) {
		return
	}
	if err := h.service.RuntimeEvent(r.Context(), publicID, bearerToken(r.Header.Get("Authorization")), event); err != nil {
		api.WriteError(w, http.StatusUnauthorized, "invalid_agent_run_token")
		return
	}
	api.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *Handler) InternalTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	toolName := strings.Trim(strings.TrimPrefix(r.URL.Path, "/internal/agent/tools/"), "/")
	if toolName == "" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var body ToolRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := h.service.ExecuteTool(
		r.Context(), toolName, bearerToken(r.Header.Get("Authorization")), body,
	)
	if err != nil {
		if strings.Contains(err.Error(), "token") {
			api.WriteError(w, http.StatusUnauthorized, "invalid_agent_run_token")
			return
		}
		api.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		api.WriteError(w, http.StatusNotFound, "not_found")
	case strings.Contains(err.Error(), "forbidden"):
		api.WriteError(w, http.StatusForbidden, err.Error())
	case strings.Contains(err.Error(), "invalid_"):
		api.WriteError(w, http.StatusBadRequest, err.Error())
	case strings.Contains(err.Error(), "not_waiting"), strings.Contains(err.Error(), "not_pending"),
		strings.Contains(err.Error(), "decision_incomplete"), strings.Contains(err.Error(), "already_active"):
		api.WriteError(w, http.StatusConflict, err.Error())
	default:
		api.WriteError(w, http.StatusInternalServerError, fallback)
	}
}
