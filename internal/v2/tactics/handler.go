package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/security"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/strategicmemory"
	"reup-goals-backend/internal/v2/workspaces"
)

const maxTacticsFileBytes = 25 << 20

type Handler struct {
	store       *Store
	workspaces  *workspaces.Store
	facilitator *FacilitatorService
	readiness   *TacticsReadinessService
}

func (h *Handler) WithContextIndex(index *contextindex.Service) *Handler {
	h.facilitator.SetContextIndex(index)
	h.readiness.SetContextIndex(index)
	return h
}

func NewHandler(dbx *sql.DB, aiClient ai.Provider, compactThreshold int, managers ...*jobs.Manager) *Handler {
	readiness := NewTacticsReadinessService(dbx, aiClient, compactThreshold)
	facilitator := NewFacilitatorService(dbx, aiClient, compactThreshold, managers...)
	facilitator.SetReadinessService(readiness)
	readiness.StartWorker()
	return &Handler{
		store:       NewStore(dbx),
		workspaces:  workspaces.NewStore(dbx),
		facilitator: facilitator,
		readiness:   readiness,
	}
}

func (h *Handler) Facilitator(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/tactics-facilitator/state":
		h.facilitatorState(w, r)
	case "/api/v2/tactics-facilitator/messages":
		h.facilitatorMessage(w, r)
	case "/api/v2/tactics-facilitator/actions/apply":
		h.facilitatorApplyActions(w, r)
	case "/api/v2/tactics-facilitator/files":
		h.facilitatorFile(w, r)
	case "/api/v2/tactics-facilitator/readiness":
		h.tacticsReadiness(w, r)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) Advisor(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v2/tactics-advisor/threads":
		h.advisorThreads(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v2/tactics-advisor/threads/"):
		h.advisorThread(w, r)
	case r.URL.Path == "/api/v2/tactics-advisor/state":
		h.advisorState(w, r)
	case r.URL.Path == "/api/v2/tactics-advisor/messages":
		h.advisorMessage(w, r)
	case r.URL.Path == "/api/v2/tactics-advisor/files":
		h.facilitatorFile(w, r)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) advisorThreads(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := h.store.AdvisorThreads(r.Context(), workspace.ID, userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "advisor_threads_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"threads": items})
	case http.MethodPost:
		var body CreateAdvisorThreadRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		scopeType, scopeID := normalizeAdvisorScope(body.ScopeType, body.ScopeID)
		body.ScopeType, body.ScopeID = scopeType, scopeID
		if scopeType != EntityWorkspace {
			if _, err := h.store.ScopeContext(r.Context(), workspace.ID, &TacticsMessageScope{EntityType: scopeType, EntityID: scopeID}); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					api.WriteError(w, http.StatusNotFound, "advisor_scope_not_found")
				} else {
					api.WriteError(w, http.StatusBadRequest, "invalid_advisor_scope")
				}
				return
			}
		}
		item, err := h.store.CreateAdvisorThread(r.Context(), workspace.ID, userID, body)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "advisor_thread_create_failed")
			return
		}
		api.WriteJSON(w, http.StatusCreated, map[string]any{"thread": item})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) advisorThread(w http.ResponseWriter, r *http.Request) {
	threadID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics-advisor/threads/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	workspace, userID, current := h.currentWorkspace(w, r)
	if !current {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_json")
			return
		}
		item, err := h.store.UpdateAdvisorThread(r.Context(), workspace.ID, userID, threadID, body.Title)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.WriteError(w, http.StatusNotFound, "advisor_thread_not_found")
			} else if err.Error() == "advisor_thread_title_required" {
				api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			} else {
				api.WriteError(w, http.StatusInternalServerError, "advisor_thread_update_failed")
			}
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"thread": item})
	case http.MethodDelete:
		if err := h.store.ArchiveAdvisorThread(r.Context(), workspace.ID, userID, threadID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.WriteError(w, http.StatusNotFound, "advisor_thread_not_found")
			} else {
				api.WriteError(w, http.StatusInternalServerError, "advisor_thread_archive_failed")
			}
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) advisorState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	threadID, _ := strconv.Atoi(r.URL.Query().Get("thread_id"))
	if threadID <= 0 {
		api.WriteError(w, http.StatusBadRequest, "advisor_thread_required")
		return
	}
	state, err := h.facilitator.HistoryThread(r.Context(), workspace.ID, userID, threadID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			api.WriteError(w, http.StatusNotFound, "advisor_thread_not_found")
		} else {
			api.WriteError(w, http.StatusInternalServerError, "advisor_state_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, state)
}

func (h *Handler) advisorMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	var body TacticsFacilitatorMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if body.ThreadID <= 0 {
		api.WriteError(w, http.StatusBadRequest, "advisor_thread_required")
		return
	}
	response, err := h.facilitator.HandleMessage(r.Context(), workspace.ID, userID, body)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			api.WriteError(w, http.StatusNotFound, "advisor_thread_not_found")
		case err.Error() == "message_too_short", err.Error() == "message_too_long":
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		case err.Error() == "invalid_tactics_scope":
			api.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			api.WriteError(w, http.StatusInternalServerError, "advisor_message_failed")
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id":        response.WorkspaceID,
		"assistant_message":   response.AssistantMessage,
		"recent_messages":     response.RecentMessages,
		"openai_response_id":  response.OpenAIResponseID,
		"proposal_message_id": response.ProposalMessageID,
		"proposed_changes":    response.ProposedChanges,
		"applied_changes":     response.AppliedChanges,
	})
}

func (h *Handler) facilitatorApplyActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	var body ApplyTacticsChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	response, err := h.facilitator.ApplyConfirmedChanges(r.Context(), workspace.ID, userID, body)
	if err != nil {
		switch err.Error() {
		case "invalid_tactics_actions", "invalid_tactics_action_index":
			api.WriteError(w, http.StatusBadRequest, err.Error())
		case "tactics_plan_required":
			api.WriteError(w, http.StatusConflict, err.Error())
		case "tactics_action_not_confirmable":
			api.WriteError(w, http.StatusConflict, err.Error())
		case "tactics_action_not_applicable":
			api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			if errors.Is(err, sql.ErrNoRows) {
				api.WriteError(w, http.StatusNotFound, "tactics_action_message_not_found")
			} else {
				api.WriteError(w, http.StatusInternalServerError, "tactics_actions_apply_failed")
			}
		}
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) tacticsReadiness(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		response, err := h.readiness.Latest(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tactics_readiness_state_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, response)
	case http.MethodPost:
		item, err := h.readiness.ForceCurrent(r.Context(), workspace.ID, userID)
		if err != nil {
			switch err.Error() {
			case "tactics_readiness_no_plan", "tactics_readiness_context_incomplete":
				api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			default:
				api.WriteError(w, http.StatusInternalServerError, "tactics_readiness_start_failed")
			}
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": item})
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
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
		state, err := h.facilitator.History(r.Context(), workspace.ID, tacticsScopeFromQuery(r))
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tactics_facilitator_state_failed")
			return
		}
		api.WriteJSON(w, http.StatusOK, state)
		return
	}
	state, err := h.facilitator.State(r.Context(), workspace.ID, userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "tactics_facilitator_state_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, state)
}

func tacticsScopeFromQuery(r *http.Request) *TacticsMessageScope {
	entityType := strings.TrimSpace(r.URL.Query().Get("scope_type"))
	entityID, _ := strconv.Atoi(r.URL.Query().Get("scope_id"))
	if entityType == "" || entityID <= 0 {
		return nil
	}
	return &TacticsMessageScope{EntityType: entityType, EntityID: entityID}
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
	var body TacticsFacilitatorMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	response, err := h.facilitator.HandleMessage(r.Context(), workspace.ID, userID, body)
	if err != nil {
		switch err.Error() {
		case "message_too_short":
			api.WriteError(w, http.StatusUnprocessableEntity, "message_too_short")
		case "message_too_long":
			api.WriteError(w, http.StatusUnprocessableEntity, "message_too_long")
		case "tactics_strategy_required":
			api.WriteError(w, http.StatusConflict, "tactics_strategy_required")
		case "tactics_course_required":
			api.WriteError(w, http.StatusConflict, "tactics_course_required")
		case "invalid_tactics_scope":
			api.WriteError(w, http.StatusBadRequest, "invalid_tactics_scope")
		default:
			api.WriteError(w, http.StatusInternalServerError, "tactics_facilitator_message_failed")
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
	r.Body = http.MaxBytesReader(w, r.Body, maxTacticsFileBytes+(2<<20))
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
	if err := security.ValidateBusinessDocument(header.Filename, header.Size, maxTacticsFileBytes); err != nil {
		api.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	filename := security.SafeFilename(header.Filename)
	response, err := h.facilitator.UploadFile(r.Context(), workspace.ID, userID, filename, header.Header.Get("Content-Type"), header.Size, file)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "tactics_file_upload_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/tactics/current" {
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
		api.WriteError(w, http.StatusInternalServerError, "tactics_current_failed")
		return
	}

	api.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Tactics(w http.ResponseWriter, r *http.Request) {
	planID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	var body struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	body.Status = strings.TrimSpace(body.Status)
	if body.Status != "" && !ValidPlanStatus(body.Status) {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}
	if body.Status == PlanStatusActive {
		currentPlan, err := h.store.planByID(r.Context(), workspace.ID, planID)
		if errors.Is(err, sql.ErrNoRows) {
			api.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tactics_readiness_check_failed")
			return
		}
		contentChanged := (strings.TrimSpace(body.Title) != "" && strings.TrimSpace(body.Title) != currentPlan.Title) ||
			(strings.TrimSpace(body.Summary) != "" && strings.TrimSpace(body.Summary) != currentPlan.Summary)
		if contentChanged {
			api.WriteError(w, http.StatusConflict, "tactics_readiness_required_after_changes")
			return
		}
		latest, err := h.readiness.Latest(r.Context(), workspace.ID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tactics_readiness_check_failed")
			return
		}
		if latest.Run == nil || !latest.IsCurrent || latest.Run.TacticalPlanID != planID || !latest.Run.CanActivate {
			api.WriteError(w, http.StatusConflict, "tactics_readiness_required")
			return
		}
		snapshot, err := h.store.Current(r.Context(), workspace.ID, userID)
		if err != nil {
			api.WriteError(w, http.StatusInternalServerError, "tactics_snapshot_failed")
			return
		}
		plan, err := h.store.ActivatePlan(r.Context(), workspace.ID, userID, planID, latest.Run.ID, latest.Run.TacticalPlanRevision, snapshot)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.WriteError(w, http.StatusConflict, "tactics_readiness_required_after_changes")
				return
			}
			api.WriteError(w, http.StatusInternalServerError, "tactics_activation_failed")
			return
		}
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeTacticalPlan, plan.ID, plan)
		api.WriteJSON(w, http.StatusOK, map[string]any{"tactical_plan": plan})
		return
	}

	plan, err := h.store.UpdatePlan(r.Context(), workspace.ID, planID, body.Title, body.Summary, body.Status)
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "tactics_update_failed")
		return
	}

	h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeTacticalPlan, plan.ID, plan)
	api.WriteJSON(w, http.StatusOK, map[string]any{"tactical_plan": plan})
}

func (h *Handler) Workstreams(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	if r.URL.Path == "/api/v2/tactics/workstreams" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		input, ok := decodeWorkstreamInput(w, r)
		if !ok {
			return
		}
		if input.TacticalPlanID <= 0 || input.Title == "" {
			api.WriteError(w, http.StatusBadRequest, "invalid_workstream")
			return
		}
		workstream, err := h.store.CreateWorkstream(r.Context(), workspace.ID, userID, input)
		if err == nil {
			h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeWorkstream, workstream.ID, workstream)
		}
		writeEntity(w, err, "workstream", workstream, "workstream_create_failed")
		return
	}

	workstreamID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/workstreams/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, ok := decodeWorkstreamInput(w, r)
	if !ok {
		return
	}
	workstream, err := h.store.UpdateWorkstream(r.Context(), workspace.ID, workstreamID, input)
	if err == nil {
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeWorkstream, workstream.ID, workstream)
	}
	writeEntity(w, err, "workstream", workstream, "workstream_update_failed")
}

func (h *Handler) Projects(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	if r.URL.Path == "/api/v2/tactics/projects" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		input, ok := decodeProjectInput(w, r)
		if !ok {
			return
		}
		if input.WorkstreamID <= 0 || input.Title == "" {
			api.WriteError(w, http.StatusBadRequest, "invalid_project")
			return
		}
		project, err := h.store.CreateProject(r.Context(), workspace.ID, userID, input)
		if err == nil {
			h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeProject, project.ID, project)
		}
		writeEntity(w, err, "project", project, "project_create_failed")
		return
	}

	projectID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/projects/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, ok := decodeProjectInput(w, r)
	if !ok {
		return
	}
	project, err := h.store.UpdateProject(r.Context(), workspace.ID, projectID, input)
	if err == nil {
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeProject, project.ID, project)
	}
	writeEntity(w, err, "project", project, "project_update_failed")
}

func (h *Handler) captureTacticsEntity(
	ctx context.Context,
	workspaceID int,
	userID int,
	sourceType string,
	entityID int,
	value any,
) {
	content := strategicmemory.JSONSourceContent(value)
	if content == "" {
		return
	}
	_, _, err := h.facilitator.memoryService.CaptureSource(ctx, workspaceID, userID, strategicmemory.SourceCapture{
		SourceType: sourceType,
		EntityKey:  fmt.Sprintf("%s:%d", sourceType, entityID),
		Content:    content,
		Metadata:   map[string]any{"entity_id": entityID},
	})
	if err != nil {
		log.Printf("[WARN] capture tactics entity workspace_id=%d type=%s id=%d: %v", workspaceID, sourceType, entityID, err)
	}
}

func (h *Handler) Risks(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	if r.URL.Path == "/api/v2/tactics/risks" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		input, ok := decodeRiskInput(w, r)
		if !ok {
			return
		}
		if input.EntityID <= 0 || input.Title == "" || !ValidEntityType(input.EntityType) || (input.CoverageStatus != "" && !ValidCoverageStatus(input.CoverageStatus)) {
			api.WriteError(w, http.StatusBadRequest, "invalid_risk")
			return
		}
		risk, err := h.store.CreateRisk(r.Context(), workspace.ID, userID, input)
		if err == nil {
			h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeRisk, risk.ID, risk)
		}
		writeEntity(w, err, "risk", risk, "risk_create_failed")
		return
	}

	riskID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/risks/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, ok := decodeRiskInput(w, r)
	if !ok {
		return
	}
	if input.CoverageStatus != "" && !ValidCoverageStatus(input.CoverageStatus) {
		api.WriteError(w, http.StatusBadRequest, "invalid_coverage_status")
		return
	}
	risk, err := h.store.UpdateRisk(r.Context(), workspace.ID, riskID, input)
	if err == nil {
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeRisk, risk.ID, risk)
	}
	writeEntity(w, err, "risk", risk, "risk_update_failed")
}

func (h *Handler) Opportunities(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}

	if r.URL.Path == "/api/v2/tactics/opportunities" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		input, ok := decodeOpportunityInput(w, r)
		if !ok {
			return
		}
		if input.EntityID <= 0 || input.Title == "" || !ValidEntityType(input.EntityType) || (input.CoverageStatus != "" && !ValidCoverageStatus(input.CoverageStatus)) {
			api.WriteError(w, http.StatusBadRequest, "invalid_opportunity")
			return
		}
		opportunity, err := h.store.CreateOpportunity(r.Context(), workspace.ID, userID, input)
		if err == nil {
			h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeOpportunity, opportunity.ID, opportunity)
		}
		writeEntity(w, err, "opportunity", opportunity, "opportunity_create_failed")
		return
	}

	opportunityID, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/opportunities/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, ok := decodeOpportunityInput(w, r)
	if !ok {
		return
	}
	if input.CoverageStatus != "" && !ValidCoverageStatus(input.CoverageStatus) {
		api.WriteError(w, http.StatusBadRequest, "invalid_coverage_status")
		return
	}
	opportunity, err := h.store.UpdateOpportunity(r.Context(), workspace.ID, opportunityID, input)
	if err == nil {
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeOpportunity, opportunity.ID, opportunity)
	}
	writeEntity(w, err, "opportunity", opportunity, "opportunity_update_failed")
}

func (h *Handler) Hypotheses(w http.ResponseWriter, r *http.Request) {
	workspace, userID, ok := h.currentWorkspace(w, r)
	if !ok {
		return
	}
	if r.URL.Path == "/api/v2/tactics/hypotheses" {
		if r.Method != http.MethodPost {
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		input, ok := decodeHypothesisInput(w, r)
		if !ok {
			return
		}
		if input.Status == "" {
			input.Status = "draft"
		}
		if input.EntityID <= 0 || input.Title == "" || !ValidHypothesisEntityType(input.EntityType) ||
			!ValidHypothesisStatus(input.Status) || !validHypothesisConfidence(input.Confidence) {
			api.WriteError(w, http.StatusBadRequest, "invalid_hypothesis")
			return
		}
		item, err := h.store.CreateHypothesis(r.Context(), workspace.ID, userID, input)
		if err == nil {
			h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeHypothesis, int(item.ID), item)
		}
		writeEntity(w, err, "hypothesis", item, "hypothesis_create_failed")
		return
	}

	id, ok := numericSuffix(r.URL.Path, "/api/v2/tactics/hypotheses/")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPatch {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	input, ok := decodeHypothesisInput(w, r)
	if !ok {
		return
	}
	if (input.Status != "" && !ValidHypothesisStatus(input.Status)) ||
		!validHypothesisConfidence(input.Confidence) {
		api.WriteError(w, http.StatusBadRequest, "invalid_hypothesis")
		return
	}
	item, err := h.store.UpdateHypothesis(r.Context(), workspace.ID, int64(id), input)
	if err == nil {
		h.captureTacticsEntity(r.Context(), workspace.ID, userID, strategicmemory.SourceTypeHypothesis, int(item.ID), item)
	}
	writeEntity(w, err, "hypothesis", item, "hypothesis_update_failed")
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

func decodeWorkstreamInput(w http.ResponseWriter, r *http.Request) (WorkstreamInput, bool) {
	var input WorkstreamInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return WorkstreamInput{}, false
	}
	input.trim()
	if input.Status != "" && !ValidWorkstreamStatus(input.Status) {
		api.WriteError(w, http.StatusBadRequest, "invalid_status")
		return WorkstreamInput{}, false
	}
	return input, true
}

func decodeProjectInput(w http.ResponseWriter, r *http.Request) (ProjectInput, bool) {
	var input ProjectInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return ProjectInput{}, false
	}
	input.trim()
	if input.Status != "" && !ValidProjectStatus(input.Status) {
		api.WriteError(w, http.StatusBadRequest, "invalid_status")
		return ProjectInput{}, false
	}
	return input, true
}

func decodeRiskInput(w http.ResponseWriter, r *http.Request) (RiskInput, bool) {
	var input RiskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return RiskInput{}, false
	}
	input.trim()
	if (input.ProbabilityValue != nil && (*input.ProbabilityValue < 0 || *input.ProbabilityValue > 100)) ||
		(input.ImpactScore != nil && (*input.ImpactScore < 1 || *input.ImpactScore > 5)) {
		api.WriteError(w, http.StatusBadRequest, "invalid_risk_score")
		return RiskInput{}, false
	}
	return input, true
}

func decodeOpportunityInput(w http.ResponseWriter, r *http.Request) (OpportunityInput, bool) {
	var input OpportunityInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return OpportunityInput{}, false
	}
	input.trim()
	return input, true
}

func decodeHypothesisInput(w http.ResponseWriter, r *http.Request) (HypothesisInput, bool) {
	var input HypothesisInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return HypothesisInput{}, false
	}
	input.trim()
	return input, true
}

func validHypothesisConfidence(value *int) bool {
	return value == nil || (*value >= 0 && *value <= 1000)
}

func writeEntity(w http.ResponseWriter, err error, key string, value any, internalCode string) {
	if errors.Is(err, sql.ErrNoRows) {
		api.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, internalCode)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{key: value})
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
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
