package aiplatform

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
)

type Handler struct {
	dbx      *sql.DB
	adminKey string
}

type promptRequest struct {
	Name          string  `json:"name"`
	Version       string  `json:"version"`
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	Template      string  `json:"template"`
	Temperature   float64 `json:"temperature"`
	Notes         string  `json:"notes"`
	ParentVersion string  `json:"parent_version"`
}

type usagePolicyRequest struct {
	WorkspaceID       *int    `json:"workspace_id"`
	RequestsPerMinute int     `json:"requests_per_minute"`
	DailyBudgetUSD    float64 `json:"daily_budget_usd"`
	MonthlyBudgetUSD  float64 `json:"monthly_budget_usd"`
	Status            string  `json:"status"`
}

func NewHandler(dbx *sql.DB, adminKey string) *Handler {
	return &Handler{dbx: dbx, adminKey: strings.TrimSpace(adminKey)}
}

func (h *Handler) Prompts(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusForbidden, "ai_admin_required")
		return
	}
	switch {
	case r.URL.Path == "/api/v2/ai/prompts" && r.Method == http.MethodGet:
		h.list(w, r)
	case r.URL.Path == "/api/v2/ai/prompts" && r.Method == http.MethodPost:
		h.create(w, r)
	case strings.HasSuffix(r.URL.Path, "/activate") && r.Method == http.MethodPost:
		h.activate(w, r)
	case strings.HasSuffix(r.URL.Path, "/rollback") && r.Method == http.MethodPost:
		h.rollback(w, r)
	default:
		api.WriteError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) UsagePolicy(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusForbidden, "ai_admin_required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listUsagePolicies(w, r)
	case http.MethodPut:
		h.upsertUsagePolicy(w, r)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT prompt_name, prompt_version, model, provider, status, notes, parent_version,
			created_at, updated_at, activated_at
		FROM v2_ai_prompt_configs
		ORDER BY prompt_name, created_at DESC
	`)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "prompt_registry_failed")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var name, version, model, provider, status, notes, parent string
		var createdAt, updatedAt any
		var activatedAt sql.NullTime
		if err := rows.Scan(&name, &version, &model, &provider, &status, &notes, &parent, &createdAt, &updatedAt, &activatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "prompt_registry_failed")
			return
		}
		items = append(items, map[string]any{"name": name, "version": version, "model": model, "provider": provider, "status": status, "notes": notes, "parent_version": parent, "created_at": createdAt, "updated_at": updatedAt, "activated_at": nullableTime(activatedAt)})
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"prompts": items})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusForbidden, "ai_admin_required")
		return
	}
	var request promptRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	request.Name, request.Version, request.Template = strings.TrimSpace(request.Name), strings.TrimSpace(request.Version), strings.TrimSpace(request.Template)
	if request.Name == "" || request.Version == "" || request.Template == "" {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_prompt")
		return
	}
	if request.Provider == "" {
		request.Provider = "openai"
	}
	_, err := h.dbx.ExecContext(r.Context(), `
		INSERT INTO v2_ai_prompt_configs (
			prompt_name, prompt_version, model, provider, temperature, template, status, notes, parent_version, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,'draft',$7,$8,NOW())
	`, request.Name, request.Version, request.Model, request.Provider, request.Temperature, request.Template, request.Notes, request.ParentVersion)
	if err != nil {
		api.WriteError(w, http.StatusConflict, "prompt_version_exists")
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]any{"created": true})
}

func (h *Handler) listUsagePolicies(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT workspace_id, requests_per_minute, daily_budget_usd, monthly_budget_usd, status, created_at, updated_at
		FROM v2_ai_usage_policies
	`
	args := make([]any, 0, 1)
	if raw := strings.TrimSpace(r.URL.Query().Get("workspace_id")); raw != "" {
		workspaceID, err := strconv.Atoi(raw)
		if err != nil || workspaceID <= 0 {
			api.WriteError(w, http.StatusBadRequest, "invalid_workspace_id")
			return
		}
		query += ` WHERE workspace_id=$1 OR workspace_id IS NULL`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY COALESCE(workspace_id, 0), updated_at DESC`

	rows, err := h.dbx.QueryContext(r.Context(), query, args...)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "usage_policy_failed")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var workspaceID sql.NullInt64
		var rpm int
		var daily, monthly float64
		var status string
		var createdAt, updatedAt any
		if err := rows.Scan(&workspaceID, &rpm, &daily, &monthly, &status, &createdAt, &updatedAt); err != nil {
			api.WriteError(w, http.StatusInternalServerError, "usage_policy_failed")
			return
		}
		items = append(items, map[string]any{
			"workspace_id":         nullableInt(workspaceID),
			"requests_per_minute":  rpm,
			"daily_budget_usd":     daily,
			"monthly_budget_usd":   monthly,
			"status":               status,
			"created_at":           createdAt,
			"updated_at":           updatedAt,
			"applies_to_workspace": workspaceID.Valid,
		})
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"policies": items})
}

func (h *Handler) upsertUsagePolicy(w http.ResponseWriter, r *http.Request) {
	var request usagePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if request.WorkspaceID != nil && *request.WorkspaceID <= 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_workspace_id")
		return
	}
	if request.RequestsPerMinute < 0 || request.DailyBudgetUSD < 0 || request.MonthlyBudgetUSD < 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_limits")
		return
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = "active"
	}
	if request.Status != "active" && request.Status != "paused" {
		api.WriteError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}

	var workspaceID any
	if request.WorkspaceID != nil {
		workspaceID = *request.WorkspaceID
	}
	_, err := h.dbx.ExecContext(r.Context(), `
		INSERT INTO v2_ai_usage_policies (
			workspace_id, requests_per_minute, daily_budget_usd, monthly_budget_usd, status, updated_at
		) VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT ((COALESCE(workspace_id, 0))) DO UPDATE SET
			requests_per_minute=EXCLUDED.requests_per_minute,
			daily_budget_usd=EXCLUDED.daily_budget_usd,
			monthly_budget_usd=EXCLUDED.monthly_budget_usd,
			status=EXCLUDED.status,
			updated_at=NOW()
	`, workspaceID, request.RequestsPerMinute, request.DailyBudgetUSD, request.MonthlyBudgetUSD, request.Status)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "usage_policy_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"saved": true})
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusForbidden, "ai_admin_required")
		return
	}
	name, version, ok := promptPath(r.URL.Path, "activate")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.setActive(r, name, version); err != nil {
		api.WriteError(w, http.StatusNotFound, "prompt_version_not_found")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"active": true, "name": name, "version": version})
}

func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusForbidden, "ai_admin_required")
		return
	}
	name, _, ok := promptPath(r.URL.Path, "rollback")
	if !ok {
		api.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	var version string
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT prompt_version FROM v2_ai_prompt_configs
		WHERE prompt_name=$1 AND status <> 'active'
		ORDER BY COALESCE(activated_at, created_at) DESC
		LIMIT 1
	`, name).Scan(&version)
	if err != nil || h.setActive(r, name, version) != nil {
		api.WriteError(w, http.StatusNotFound, "rollback_version_not_found")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"active": true, "name": name, "version": version})
}

func (h *Handler) setActive(r *http.Request, name string, version string) error {
	tx, err := h.dbx.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE v2_ai_prompt_configs SET status='archived', updated_at=NOW() WHERE prompt_name=$1 AND status='active'`, name); err != nil {
		return err
	}
	userID, _ := auth.UserIDFromContext(r.Context())
	result, err := tx.ExecContext(r.Context(), `
		UPDATE v2_ai_prompt_configs
		SET status='active', activated_at=NOW(), activated_by=NULLIF($3,0), updated_at=NOW()
		WHERE prompt_name=$1 AND prompt_version=$2
	`, name, version, userID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (h *Handler) authorized(r *http.Request) bool {
	provided := strings.TrimSpace(r.Header.Get("X-AI-Admin-Key"))
	return h.adminKey != "" && len(provided) == len(h.adminKey) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(h.adminKey)) == 1
}

func promptPath(path string, action string) (string, string, bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v2/ai/prompts/"), "/"), "/")
	if action == "rollback" && len(parts) == 2 && parts[1] == action {
		return parts[0], "", parts[0] != ""
	}
	if len(parts) != 3 || parts[2] != action {
		return "", "", false
	}
	return parts[0], parts[1], parts[0] != "" && parts[1] != ""
}

func nullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
