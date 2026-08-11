package navigation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/strategicmemory"
	tasksv2 "reup-goals-backend/internal/v2/tasks"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	dbx        *sql.DB
	workspaces *workspaces.Store
}

func NewHandler(dbx *sql.DB) *Handler {
	return &Handler{dbx: dbx, workspaces: workspaces.NewStore(dbx)}
}

type response struct {
	Account                account             `json:"account"`
	Workspace              workspace           `json:"workspace"`
	ContextReady           bool                `json:"context_ready"`
	SubscriptionAccess     bool                `json:"subscription_access"`
	OnboardingProgress     onboardingProgress  `json:"onboarding_progress"`
	Strategy               *strategy           `json:"strategy,omitempty"`
	StrategySessionActive  bool                `json:"strategy_session_active"`
	StrategySessionOwnerID *int                `json:"strategy_session_owner_id,omitempty"`
	StrategySessionStatus  string              `json:"strategy_session_status,omitempty"`
	MainTask               *tasksv2.FocusTask  `json:"main_task,omitempty"`
	Workstreams            []workstream        `json:"workstreams"`
	Departments            []department        `json:"departments"`
	WorkspaceDocuments     []workspaceDocument `json:"workspace_documents"`
	KnowledgeDocuments     []knowledgeDocument `json:"knowledge_documents"`
}

type onboardingProgress struct {
	ContextComplete    bool `json:"context_complete"`
	GoalComplete       bool `json:"goal_complete"`
	DirectionsComplete bool `json:"directions_complete"`
	TasksComplete      bool `json:"tasks_complete"`
	Complete           bool `json:"complete"`
}

type account struct {
	ID                int             `json:"id"`
	Email             string          `json:"email"`
	Name              string          `json:"name"`
	AvatarURL         string          `json:"avatar_url"`
	ProductTourStatus string          `json:"product_tour_status"`
	ProductTourStep   int             `json:"product_tour_step"`
	FeatureOnboarding map[string]bool `json:"feature_onboarding"`
}

type workspace struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	OwnerUserID int    `json:"owner_user_id"`
}

type strategy struct {
	ID            int    `json:"id"`
	Status        string `json:"status"`
	Version       int    `json:"version"`
	Title         string `json:"title"`
	Summary       string `json:"summary"`
	CurrentSignal string `json:"current_signal"`
	TargetSignal  string `json:"target_signal"`
	CurrentStage  string `json:"current_stage"`
	CurrentMetric string `json:"current_metric"`
	TargetStage   string `json:"target_stage"`
	TargetMetric  string `json:"target_metric"`
}

type workstream struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	SortOrder  int       `json:"sort_order"`
	Confidence *float64  `json:"confidence,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Projects   []project `json:"projects"`
}

type project struct {
	ID           int       `json:"id"`
	WorkstreamID int       `json:"workstream_id"`
	Title        string    `json:"title"`
	SortOrder    int       `json:"sort_order"`
	Confidence   *float64  `json:"confidence,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type department struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type workspaceDocument struct {
	ID                  int64     `json:"id"`
	ParentID            *int64    `json:"parent_id,omitempty"`
	Title               string    `json:"title"`
	Status              string    `json:"status"`
	Favorite            bool      `json:"favorite"`
	Version             int       `json:"version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	LinkedDepartmentIDs []int     `json:"linked_department_ids"`
	LinkedWorkstreamIDs []int     `json:"linked_workstream_ids"`
	LinkedProjectIDs    []int     `json:"linked_project_ids"`
}

type knowledgeDocument struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Version int    `json:"version"`
}

func (h *Handler) Navigation(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/navigation" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	currentWorkspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "navigation_workspace_failed")
		return
	}

	result := response{
		Workspace: workspace{
			ID: currentWorkspace.ID, Name: currentWorkspace.Name,
			DisplayName: currentWorkspace.Name, OwnerUserID: currentWorkspace.OwnerUserID,
		},
		Workstreams:        []workstream{},
		Departments:        []department{},
		WorkspaceDocuments: []workspaceDocument{},
		KnowledgeDocuments: []knowledgeDocument{},
	}
	result.SubscriptionAccess, err = api.WorkspaceHasProductAccess(
		r.Context(), h.dbx, currentWorkspace.ID, currentWorkspace.OwnerUserID, time.Now().UTC(),
	)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "subscription_lookup_failed")
		return
	}
	if currentWorkspace.DisplayName != nil && strings.TrimSpace(*currentWorkspace.DisplayName) != "" {
		result.Workspace.DisplayName = strings.TrimSpace(*currentWorkspace.DisplayName)
	}

	type loadResult struct {
		code string
		err  error
	}
	loadResults := make(chan loadResult, 9)
	var directedTaskExists bool
	go func() {
		loadResults <- loadResult{"navigation_account_failed", h.loadAccount(r, userID, &result.Account)}
	}()
	go func() {
		strategy, sessionActive, sessionOwnerID, sessionStatus, loadErr := h.loadStrategy(r, currentWorkspace.ID)
		result.Strategy = strategy
		result.StrategySessionActive = sessionActive
		result.StrategySessionOwnerID = sessionOwnerID
		result.StrategySessionStatus = sessionStatus
		loadResults <- loadResult{"navigation_strategy_failed", loadErr}
	}()
	go func() {
		workstreams, loadErr := h.loadWorkstreams(r, currentWorkspace.ID)
		result.Workstreams = workstreams
		loadResults <- loadResult{"navigation_tactics_failed", loadErr}
	}()
	go func() {
		focus, loadErr := tasksv2.NewStore(h.dbx).FocusSummary(
			r.Context(), currentWorkspace.ID, tasksv2.FocusScopeWorkspace, 0,
		)
		if loadErr == nil {
			result.MainTask = focus.RecommendedTask
		}
		// The focus card enriches navigation, but must never block the shell.
		loadResults <- loadResult{"", nil}
	}()
	go func() {
		departments, loadErr := h.loadDepartments(r, currentWorkspace.ID)
		result.Departments = departments
		loadResults <- loadResult{"navigation_departments_failed", loadErr}
	}()
	go func() {
		documents, loadErr := h.loadWorkspaceDocuments(r, currentWorkspace.ID)
		result.WorkspaceDocuments = documents
		loadResults <- loadResult{"navigation_documents_failed", loadErr}
	}()
	go func() {
		documents, loadErr := h.loadKnowledgeDocuments(r, currentWorkspace.ID)
		result.KnowledgeDocuments = documents
		loadResults <- loadResult{"navigation_knowledge_failed", loadErr}
	}()
	go func() {
		store := strategicmemory.NewStore(h.dbx)
		pipeline, pipelineErr := store.KnowledgePipelineState(r.Context(), currentWorkspace.ID)
		if pipelineErr != nil {
			loadResults <- loadResult{"navigation_context_failed", pipelineErr}
			return
		}
		result.ContextReady = pipeline.OnboardingConfirmedAt != nil
		loadResults <- loadResult{"navigation_context_failed", nil}
	}()
	go func() {
		var loadErr error
		directedTaskExists, loadErr = h.hasDirectedTask(r, currentWorkspace.ID)
		loadResults <- loadResult{"navigation_onboarding_tasks_failed", loadErr}
	}()

	for range 9 {
		load := <-loadResults
		if load.err != nil {
			api.WriteError(w, http.StatusInternalServerError, load.code)
			return
		}
	}
	if result.Strategy != nil && result.Strategy.Status == "active" {
		result.ContextReady = true
	}
	result.OnboardingProgress = deriveOnboardingProgress(
		result.ContextReady, result.Strategy, result.Departments, directedTaskExists,
	)
	if result.ContextReady && !result.SubscriptionAccess {
		result.Strategy = nil
		result.MainTask = nil
		result.Workstreams = []workstream{}
		result.Departments = []department{}
		result.WorkspaceDocuments = []workspaceDocument{}
		result.KnowledgeDocuments = []knowledgeDocument{}
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func deriveOnboardingProgress(
	contextReady bool,
	currentStrategy *strategy,
	departments []department,
	directedTaskExists bool,
) onboardingProgress {
	goalComplete := currentStrategy != nil && (hasMeaningfulStrategyTitle(currentStrategy.Title) ||
		strings.TrimSpace(currentStrategy.Summary) != "" ||
		strings.TrimSpace(currentStrategy.TargetSignal) != "" ||
		strings.TrimSpace(currentStrategy.TargetStage) != "")
	progress := onboardingProgress{
		ContextComplete:    contextReady,
		GoalComplete:       goalComplete,
		DirectionsComplete: hasBusinessDirection(departments),
		TasksComplete:      directedTaskExists,
	}
	progress.Complete = progress.ContextComplete && progress.GoalComplete &&
		progress.DirectionsComplete && progress.TasksComplete
	return progress
}

func hasMeaningfulStrategyTitle(title string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" || title == "стратегия компании" {
		return false
	}
	if strings.HasPrefix(title, "стратегия v") {
		return false
	}
	return true
}

func hasBusinessDirection(departments []department) bool {
	for _, item := range departments {
		if !strings.EqualFold(strings.TrimSpace(item.Name), "Компания") {
			return true
		}
	}
	return false
}

func (h *Handler) hasDirectedTask(r *http.Request, workspaceID int) (bool, error) {
	var exists bool
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM v2_tasks
			WHERE workspace_id=$1
				AND department_id IS NOT NULL
				AND archived_at IS NULL
		)
	`, workspaceID).Scan(&exists)
	return exists, err
}

func (h *Handler) loadAccount(r *http.Request, userID int, target *account) error {
	var featureOnboarding []byte
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT id, email, COALESCE(name, ''), COALESCE(avatar_url, ''),
			product_tour_status, product_tour_step, feature_onboarding_json
		FROM users WHERE id=$1
	`, userID).Scan(
		&target.ID, &target.Email, &target.Name, &target.AvatarURL,
		&target.ProductTourStatus, &target.ProductTourStep, &featureOnboarding,
	)
	if err != nil {
		return err
	}
	target.FeatureOnboarding = map[string]bool{}
	return json.Unmarshal(featureOnboarding, &target.FeatureOnboarding)
}

type productTourRequest struct {
	Status string `json:"status"`
	Step   int    `json:"step"`
}

type productTourResponse struct {
	Status string `json:"status"`
	Step   int    `json:"step"`
}

var featureOnboardingKeys = map[string]struct{}{
	"advisor_package":  {},
	"first_task":       {},
	"task_evaluation":  {},
	"task_completion":  {},
	"course_review":    {},
	"first_metric":     {},
	"first_risk":       {},
	"first_hypothesis": {},
}

func (h *Handler) UpdateProductTour(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/navigation/product-tour" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var request productTourRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if request.Status != "in_progress" && request.Status != "completed" && request.Status != "skipped" {
		api.WriteError(w, http.StatusBadRequest, "product_tour_status_invalid")
		return
	}
	if request.Step < 0 || request.Step > 5 {
		api.WriteError(w, http.StatusBadRequest, "product_tour_step_invalid")
		return
	}

	var result productTourResponse
	err := h.dbx.QueryRowContext(r.Context(), `
		UPDATE users
		SET
			product_tour_status=CASE
				WHEN product_tour_status IN ('completed', 'skipped') THEN product_tour_status
				ELSE $2
			END,
			product_tour_step=CASE
				WHEN product_tour_status IN ('completed', 'skipped') THEN product_tour_step
				ELSE GREATEST(product_tour_step, $3)
			END,
			product_tour_completed_at=CASE
				WHEN product_tour_status IN ('completed', 'skipped') THEN product_tour_completed_at
				WHEN $2 IN ('completed', 'skipped') THEN NOW()
				ELSE NULL
			END
		WHERE id=$1
		RETURNING product_tour_status, product_tour_step
	`, userID, request.Status, request.Step).Scan(&result.Status, &result.Step)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "product_tour_update_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, result)
}

type featureOnboardingRequest struct {
	Key string `json:"key"`
}

func (h *Handler) UpdateFeatureOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/navigation/feature-onboarding" {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var request featureOnboardingRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	request.Key = strings.TrimSpace(request.Key)
	if _, exists := featureOnboardingKeys[request.Key]; !exists {
		api.WriteError(w, http.StatusUnprocessableEntity, "feature_onboarding_key_invalid")
		return
	}
	var raw []byte
	err := h.dbx.QueryRowContext(r.Context(), `
		UPDATE users
		SET feature_onboarding_json=jsonb_set(
			COALESCE(feature_onboarding_json, '{}'::jsonb),
			ARRAY[$2]::TEXT[],
			'true'::jsonb,
			TRUE
		)
		WHERE id=$1
		RETURNING feature_onboarding_json
	`, userID, request.Key).Scan(&raw)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "feature_onboarding_update_failed")
		return
	}
	result := map[string]bool{}
	if err := json.Unmarshal(raw, &result); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "feature_onboarding_decode_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"feature_onboarding": result})
}

func (h *Handler) loadStrategy(r *http.Request, workspaceID int) (*strategy, bool, *int, string, error) {
	var item strategy
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT strategy.id, strategy.status, strategy.version, strategy.title, strategy.summary,
			COALESCE((
				SELECT COALESCE(NULLIF(business_stage, 'unknown'), '')
				FROM strategic_memory_snapshots
				WHERE workspace_id=$1
				ORDER BY version DESC, id DESC
				LIMIT 1
			), '')
		FROM v2_strategies strategy
		WHERE strategy.workspace_id=$1
			AND strategy.archived_at IS NULL
			AND strategy.status IN ('draft', 'ready_for_review', 'active')
		ORDER BY
			CASE strategy.status
				WHEN 'draft' THEN 1
				WHEN 'ready_for_review' THEN 1
				ELSE 3
			END,
			strategy.version DESC,
			strategy.created_at DESC,
			strategy.id DESC
		LIMIT 1
	`, workspaceID).Scan(&item.ID, &item.Status, &item.Version, &item.Title, &item.Summary, &item.CurrentStage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil, "", nil
	}
	if err != nil {
		return nil, false, nil, "", err
	}
	var sessionActive bool
	if err := h.dbx.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1
			FROM v2_strategies
			WHERE workspace_id=$1
				AND archived_at IS NULL
				AND status IN ('draft', 'ready_for_review')
		)
	`, workspaceID).Scan(&sessionActive); err != nil {
		return nil, false, nil, "", err
	}
	var sessionOwner sql.NullInt64
	var sessionStatus string
	if sessionActive {
		if err := h.dbx.QueryRowContext(r.Context(), `
			SELECT strategy.status, COALESCE(strategy.created_by, session.last_user_id)
			FROM v2_strategies strategy
			LEFT JOIN v2_strategy_session_state session ON session.workspace_id=strategy.workspace_id
			WHERE strategy.workspace_id=$1
				AND strategy.archived_at IS NULL
				AND strategy.status IN ('draft', 'ready_for_review')
			ORDER BY strategy.version DESC, strategy.id DESC
			LIMIT 1
		`, workspaceID).Scan(&sessionStatus, &sessionOwner); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil, "", err
		}
	}
	var sessionOwnerID *int
	if sessionOwner.Valid {
		value := int(sessionOwner.Int64)
		sessionOwnerID = &value
	}
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT document.document_type, document.primary_signal, document.frame_title,
			document.frame_subtitle
		FROM v2_strategy_synthesis_documents document
		JOIN v2_strategy_synthesis_runs run ON run.id=document.run_id
		WHERE run.workspace_id=$1 AND run.strategy_id=$2 AND run.status='completed'
			AND document.document_type IN ('strategic_diagnosis', 'goals_and_metrics')
			AND run.id=(
				SELECT id FROM v2_strategy_synthesis_runs
				WHERE workspace_id=$1 AND strategy_id=$2 AND status='completed'
				ORDER BY created_at DESC, id DESC LIMIT 1
			)
	`, workspaceID, item.ID)
	if err != nil {
		return nil, false, nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var documentType, primarySignal, frameTitle, frameSubtitle string
		if err := rows.Scan(&documentType, &primarySignal, &frameTitle, &frameSubtitle); err != nil {
			return nil, false, nil, "", err
		}
		signal := strings.TrimSpace(primarySignal)
		if signal == "" {
			signal = strings.TrimSpace(frameTitle)
		}
		if documentType == "strategic_diagnosis" {
			item.CurrentSignal = signal
			item.CurrentMetric = signal
			item.TargetStage = strings.TrimSpace(frameTitle)
			if item.CurrentStage == "" {
				item.CurrentStage = strings.TrimSpace(frameSubtitle)
			}
		} else {
			item.TargetSignal = signal
			item.TargetMetric = signal
			if item.TargetStage == "" {
				item.TargetStage = strings.TrimSpace(frameTitle)
			}
		}
	}
	if item.CurrentMetric == "" {
		item.CurrentMetric = item.CurrentSignal
	}
	if item.TargetMetric == "" {
		item.TargetMetric = item.TargetSignal
	}
	return &item, sessionActive, sessionOwnerID, sessionStatus, rows.Err()
}

func (h *Handler) loadWorkstreams(r *http.Request, workspaceID int) ([]workstream, error) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		WITH current_plan AS (
			SELECT id
			FROM v2_tactical_plans
			WHERE workspace_id=$1 AND archived_at IS NULL
			ORDER BY (status='active') DESC, updated_at DESC, id DESC
			LIMIT 1
		)
		SELECT workstream.id, workstream.title, workstream.sort_order, workstream.confidence,
			workstream.created_at, project.id, project.title, project.sort_order,
			project.confidence, project.created_at
		FROM current_plan
		JOIN v2_tactical_workstreams workstream
			ON workstream.tactical_plan_id=current_plan.id AND workstream.workspace_id=$1
				AND workstream.archived_at IS NULL
		LEFT JOIN v2_tactical_projects project
			ON project.workstream_id=workstream.id AND project.workspace_id=$1
				AND project.archived_at IS NULL
		ORDER BY workstream.sort_order, workstream.id, project.sort_order, project.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []workstream{}
	index := map[int]int{}
	for rows.Next() {
		var workstreamID int
		var title string
		var sortOrder int
		var confidence sql.NullFloat64
		var createdAt time.Time
		var projectID sql.NullInt64
		var projectTitle sql.NullString
		var projectSortOrder sql.NullInt64
		var projectConfidence sql.NullFloat64
		var projectCreatedAt sql.NullTime
		if err := rows.Scan(
			&workstreamID, &title, &sortOrder, &confidence, &createdAt,
			&projectID, &projectTitle, &projectSortOrder, &projectConfidence, &projectCreatedAt,
		); err != nil {
			return nil, err
		}
		position, exists := index[workstreamID]
		if !exists {
			item := workstream{ID: workstreamID, Title: title, SortOrder: sortOrder, CreatedAt: createdAt, Projects: []project{}}
			if confidence.Valid {
				value := confidence.Float64
				item.Confidence = &value
			}
			items = append(items, item)
			position = len(items) - 1
			index[workstreamID] = position
		}
		if projectID.Valid {
			item := project{
				ID: int(projectID.Int64), WorkstreamID: workstreamID, Title: projectTitle.String,
				SortOrder: int(projectSortOrder.Int64), CreatedAt: projectCreatedAt.Time,
			}
			if projectConfidence.Valid {
				value := projectConfidence.Float64
				item.Confidence = &value
			}
			items[position].Projects = append(items[position].Projects, item)
		}
	}
	return items, rows.Err()
}

func (h *Handler) loadDepartments(r *http.Request, workspaceID int) ([]department, error) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT id, name FROM v2_departments
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY sort_order, id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []department{}
	for rows.Next() {
		var item department
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *Handler) loadWorkspaceDocuments(r *http.Request, workspaceID int) ([]workspaceDocument, error) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT id, parent_id, title, status, favorite, version, created_at, updated_at, linked_department_ids,
			linked_workstream_ids, linked_project_ids
		FROM workspace_documents
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY favorite DESC, updated_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []workspaceDocument{}
	for rows.Next() {
		var item workspaceDocument
		var departmentRaw, workstreamRaw, projectRaw []byte
		if err := rows.Scan(
			&item.ID, &item.ParentID, &item.Title, &item.Status, &item.Favorite,
			&item.Version, &item.CreatedAt, &item.UpdatedAt,
			&departmentRaw, &workstreamRaw, &projectRaw,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(departmentRaw, &item.LinkedDepartmentIDs)
		_ = json.Unmarshal(workstreamRaw, &item.LinkedWorkstreamIDs)
		_ = json.Unmarshal(projectRaw, &item.LinkedProjectIDs)
		if item.LinkedDepartmentIDs == nil {
			item.LinkedDepartmentIDs = []int{}
		}
		if item.LinkedWorkstreamIDs == nil {
			item.LinkedWorkstreamIDs = []int{}
		}
		if item.LinkedProjectIDs == nil {
			item.LinkedProjectIDs = []int{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *Handler) loadKnowledgeDocuments(r *http.Request, workspaceID int) ([]knowledgeDocument, error) {
	var hideInternalDocuments bool
	if err := h.dbx.QueryRowContext(r.Context(), `
		SELECT
			EXISTS (
				SELECT 1 FROM workspace_documents
				WHERE workspace_id=$1 AND system_key='company_overview' AND archived_at IS NULL
			)
			OR COALESCE((
				SELECT onboarding_confirmed_at IS NOT NULL
				FROM strategic_knowledge_pipeline_state
				WHERE workspace_id=$1
			), FALSE)
	`, workspaceID).Scan(&hideInternalDocuments); err != nil {
		return nil, err
	}
	if hideInternalDocuments {
		return []knowledgeDocument{}, nil
	}
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT document_type, status, version
		FROM strategic_documents
		WHERE workspace_id=$1
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]knowledgeDocument{}
	for rows.Next() {
		var item knowledgeDocument
		if err := rows.Scan(&item.Type, &item.Status, &item.Version); err != nil {
			return nil, err
		}
		stored[item.Type] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]knowledgeDocument, 0, len(strategicmemory.DocumentDefinitions()))
	for _, definition := range strategicmemory.DocumentDefinitions() {
		item := stored[definition.DocumentType]
		item.Type = definition.DocumentType
		item.Title = definition.Title
		if item.Status == "" {
			item.Status = "empty"
		}
		items = append(items, item)
	}
	return items, nil
}
