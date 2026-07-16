package tactics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	store       *Store
	workspaces  *workspaces.Store
	facilitator *FacilitatorService
	readiness   *TacticsReadinessService
}

func NewHandler(dbx *sql.DB, aiClient *ai.OpenAIClient, compactThreshold int) *Handler {
	readiness := NewTacticsReadinessService(dbx, aiClient, compactThreshold)
	facilitator := NewFacilitatorService(dbx, aiClient, compactThreshold)
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
		state, err := h.facilitator.History(r.Context(), workspace.ID)
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
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_multipart")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "file_required")
		return
	}
	defer file.Close()
	limited := io.LimitReader(file, 25<<20)
	response, err := h.facilitator.UploadFile(r.Context(), workspace.ID, userID, header.Filename, header.Header.Get("Content-Type"), header.Size, limited)
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
	writeEntity(w, err, "project", project, "project_update_failed")
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
	writeEntity(w, err, "opportunity", opportunity, "opportunity_update_failed")
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
