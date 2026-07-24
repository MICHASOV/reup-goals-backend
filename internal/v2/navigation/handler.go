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
	Account            account             `json:"account"`
	Workspace          workspace           `json:"workspace"`
	ContextReady       bool                `json:"context_ready"`
	Strategy           *strategy           `json:"strategy,omitempty"`
	Workstreams        []workstream        `json:"workstreams"`
	Departments        []department        `json:"departments"`
	WorkspaceDocuments []workspaceDocument `json:"workspace_documents"`
	KnowledgeDocuments []knowledgeDocument `json:"knowledge_documents"`
}

type account struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type workspace struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type strategy struct {
	ID            int    `json:"id"`
	Status        string `json:"status"`
	Version       int    `json:"version"`
	Summary       string `json:"summary"`
	CurrentSignal string `json:"current_signal"`
	TargetSignal  string `json:"target_signal"`
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
	Title               string    `json:"title"`
	Version             int       `json:"version"`
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
			DisplayName: currentWorkspace.Name,
		},
		Workstreams:        []workstream{},
		Departments:        []department{},
		WorkspaceDocuments: []workspaceDocument{},
		KnowledgeDocuments: []knowledgeDocument{},
	}
	if currentWorkspace.DisplayName != nil && strings.TrimSpace(*currentWorkspace.DisplayName) != "" {
		result.Workspace.DisplayName = strings.TrimSpace(*currentWorkspace.DisplayName)
	}

	type loadResult struct {
		code string
		err  error
	}
	loadResults := make(chan loadResult, 7)
	go func() {
		loadResults <- loadResult{"navigation_account_failed", h.loadAccount(r, userID, &result.Account)}
	}()
	go func() {
		strategy, loadErr := h.loadStrategy(r, currentWorkspace.ID)
		result.Strategy = strategy
		loadResults <- loadResult{"navigation_strategy_failed", loadErr}
	}()
	go func() {
		workstreams, loadErr := h.loadWorkstreams(r, currentWorkspace.ID)
		result.Workstreams = workstreams
		loadResults <- loadResult{"navigation_tactics_failed", loadErr}
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
		quality, qualityErr := strategicmemory.NewStore(h.dbx).LatestQualityReport(r.Context(), currentWorkspace.ID)
		if qualityErr == nil {
			result.ContextReady = quality != nil && quality.StrategyGate.CanStartStrategy
		}
		loadResults <- loadResult{"navigation_quality_failed", qualityErr}
	}()

	for range 7 {
		load := <-loadResults
		if load.err != nil {
			api.WriteError(w, http.StatusInternalServerError, load.code)
			return
		}
	}
	api.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) loadAccount(r *http.Request, userID int, target *account) error {
	return h.dbx.QueryRowContext(r.Context(), `
		SELECT id, email, COALESCE(name, ''), COALESCE(avatar_url, '')
		FROM users WHERE id=$1
	`, userID).Scan(&target.ID, &target.Email, &target.Name, &target.AvatarURL)
}

func (h *Handler) loadStrategy(r *http.Request, workspaceID int) (*strategy, error) {
	var item strategy
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT id, status, version, summary
		FROM v2_strategies
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY (status='active') DESC, version DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&item.ID, &item.Status, &item.Version, &item.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT document.document_type, document.primary_signal, document.frame_title
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
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var documentType, primarySignal, frameTitle string
		if err := rows.Scan(&documentType, &primarySignal, &frameTitle); err != nil {
			return nil, err
		}
		signal := strings.TrimSpace(primarySignal)
		if signal == "" {
			signal = strings.TrimSpace(frameTitle)
		}
		if documentType == "strategic_diagnosis" {
			item.CurrentSignal = signal
		} else {
			item.TargetSignal = signal
		}
	}
	return &item, rows.Err()
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
		SELECT id, title, version, updated_at, linked_department_ids,
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
			&item.ID, &item.Title, &item.Version, &item.UpdatedAt,
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
