package strategy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/security"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/workspaces"
)

const maxStrategyFileBytes = 25 << 20

type Handler struct {
	store       *Store
	workspaces  *workspaces.Store
	facilitator *FacilitatorService
	synthesis   *SynthesisService
	readiness   *ReadinessService
}

func NewHandler(dbx *sql.DB, aiClient ai.Provider, compactThreshold int, managers ...*jobs.Manager) *Handler {
	synthesis := NewSynthesisService(dbx, aiClient, compactThreshold, managers...)
	readiness := NewReadinessService(dbx, aiClient, compactThreshold)
	facilitator := NewFacilitatorService(dbx, aiClient, compactThreshold, managers...)
	facilitator.SetReadinessService(readiness)
	readiness.StartWorker()
	return &Handler{
		store:       NewStore(dbx),
		workspaces:  workspaces.NewStore(dbx),
		facilitator: facilitator,
		synthesis:   synthesis,
		readiness:   readiness,
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

func (h *Handler) Versions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/strategy-versions" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		versions, err := h.store.ListVersions(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_versions_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"versions": versions})
	case http.MethodPost:
		version, created, err := h.store.CreateNextVersion(r.Context(), workspace.ID, userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_version_create_failed")
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		api.WriteJSON(w, status, map[string]any{"strategy": version, "created": created})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) ResearchRequests(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		if r.URL.Path != "/api/v2/strategy-research-requests" {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		current, _, _, err := h.store.Current(r.Context(), workspace.ID, userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_research_requests_failed")
			return
		}
		items, err := h.store.ListResearchRequests(r.Context(), workspace.ID, current.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_research_requests_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"requests": items})
	case http.MethodPatch:
		requestID, ok := strategyResearchRequestPath(r.URL.Path)
		if !ok {
			api.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		var input StrategyResearchUpdate
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		item, err := h.store.UpdateResearchRequest(r.Context(), workspace.ID, userID, requestID, input)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			api.WriteError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, ErrInvalidResearchStatus):
			api.WriteError(w, http.StatusUnprocessableEntity, "invalid_research_status")
		case errors.Is(err, ErrInvalidResearchTransition):
			api.WriteError(w, http.StatusConflict, "invalid_research_transition")
		case errors.Is(err, ErrResearchResultRequired):
			api.WriteError(w, http.StatusUnprocessableEntity, "research_result_required")
		case err != nil:
			api.WriteError(w, http.StatusInternalServerError, "strategy_research_request_update_failed")
		default:
			api.WriteJSON(w, http.StatusOK, map[string]any{"request": item})
		}
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

func strategyResearchRequestPath(path string) (int, bool) {
	const prefix = "/api/v2/strategy-research-requests/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	id, err := strconv.Atoi(value)
	return id, err == nil && id > 0
}

func (h *Handler) Facilitator(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v2/strategy-facilitator/state":
		h.facilitatorState(w, r)
	case r.URL.Path == "/api/v2/strategy-facilitator/messages":
		h.facilitatorMessage(w, r)
	case r.URL.Path == "/api/v2/strategy-facilitator/files":
		h.facilitatorFile(w, r)
	case r.URL.Path == "/api/v2/strategy-facilitator/synthesis":
		h.strategySynthesis(w, r)
	case r.URL.Path == "/api/v2/strategy-facilitator/readiness":
		h.strategyReadiness(w, r)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) strategyReadiness(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		run, err := h.readiness.Latest(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_readiness_state_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
	case http.MethodPost:
		item, err := h.readiness.ForceCurrent(r.Context(), workspace.ID, userID)
		if err != nil {
			if err.Error() == "strategy_readiness_no_session" {
				api.WriteError(w, http.StatusUnprocessableEntity, "strategy_readiness_no_session")
				return
			}
			api.WriteError(w, http.StatusInternalServerError, "strategy_readiness_start_failed")
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": item})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) strategySynthesis(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		response, err := h.synthesis.Latest(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_synthesis_state_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, response)
	case http.MethodPost:
		response, err := h.synthesis.Start(r.Context(), workspace.ID, userID)
		if err != nil {
			if err.Error() == "strategy_synthesis_no_session" {
				api.WriteError(w, http.StatusUnprocessableEntity, "strategy_synthesis_no_session")
				return
			}
			if err.Error() == "strategy_synthesis_not_ready" {
				api.WriteError(w, http.StatusConflict, "strategy_synthesis_not_ready")
				return
			}
			if err.Error() == "strategy_synthesis_stale_revision" {
				api.WriteError(w, http.StatusConflict, "strategy_synthesis_stale_revision")
				return
			}
			api.WriteError(w, http.StatusInternalServerError, "strategy_synthesis_start_failed")
			return
		}
		api.WriteJSON(w, http.StatusAccepted, response)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
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
	if errors.Is(err, ErrStrategyActivationNotReady) {
		api.WriteError(w, http.StatusConflict, "strategy_activation_not_ready")
		return
	}
	if errors.Is(err, ErrStrategyActivationStale) {
		api.WriteError(w, http.StatusConflict, "strategy_activation_stale")
		return
	}
	if errors.Is(err, ErrStrategyActivationArtifactsMissing) {
		api.WriteError(w, http.StatusConflict, "strategy_activation_artifacts_missing")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_activate_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]any{"strategy": strategy})
}

func (h *Handler) facilitatorState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	if r.URL.Query().Get("view") == "history" {
		state, err := h.facilitator.History(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "strategy_facilitator_state_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, state)
		return
	}

	state, err := h.facilitator.State(r.Context(), workspace.ID, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "strategy_facilitator_state_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, state)
}

func (h *Handler) facilitatorMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	var body StrategyFacilitatorMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	response, err := h.facilitator.HandleMessage(r.Context(), workspace.ID, userID, body.Message)
	if err != nil {
		switch err.Error() {
		case "message_too_short":
			api.WriteError(w, http.StatusUnprocessableEntity, "message_too_short")
		case "message_too_long":
			api.WriteError(w, http.StatusUnprocessableEntity, "message_too_long")
		default:
			api.WriteError(w, http.StatusInternalServerError, "strategy_facilitator_message_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) facilitatorFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxStrategyFileBytes+(2<<20))
	// #nosec G120 -- MaxBytesReader above enforces a hard request limit.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_multipart")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "file_required")
		return
	}
	defer file.Close()
	if err := security.ValidateBusinessDocument(header.Filename, header.Size, maxStrategyFileBytes); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	contentType := header.Header.Get("Content-Type")
	filename := security.SafeFilename(header.Filename)
	response, err := h.facilitator.UploadFile(r.Context(), workspace.ID, userID, filename, contentType, header.Size, file)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "strategy_file_upload_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
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
