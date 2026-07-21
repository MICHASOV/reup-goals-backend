package operations

import (
	"database/sql"
	"net/http"
	"time"

	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/v2/api"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/workspaces"
)

type Handler struct {
	dbx        *sql.DB
	workspaces *workspaces.Store
	jobs       *jobs.Manager
}

type Warning struct {
	Key      string `json:"key"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Message  string `json:"message"`
}

func NewHandler(dbx *sql.DB, manager *jobs.Manager) *Handler {
	return &Handler{dbx: dbx, workspaces: workspaces.NewStore(dbx), jobs: manager}
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v2/operations/overview" {
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
	workspace, _, err := h.workspaces.GetOrCreateDefault(r.Context(), userID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "workspace_lookup_failed")
		return
	}

	queueStats, err := h.jobs.Stats(r.Context(), workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "operations_queue_failed")
		return
	}
	httpStats, err := h.httpStats(r, workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "operations_http_failed")
		return
	}
	aiStats, err := h.aiStats(r, workspace.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "operations_ai_failed")
		return
	}
	warnings, err := h.warnings(r, workspace.ID, queueStats)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "operations_warnings_failed")
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspace.ID,
		"http":         httpStats,
		"ai":           aiStats,
		"queue":        queueStats,
		"warnings":     warnings,
		"generated_at": time.Now().UTC(),
	})
}

func (h *Handler) httpStats(r *http.Request, workspaceID int) (map[string]any, error) {
	var requests, errorsCount int
	var average, p95 float64
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status_code >= 500),
			COALESCE(AVG(latency_ms), 0),
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)
		FROM v2_http_request_logs
		WHERE workspace_id=$1 AND created_at > NOW() - INTERVAL '24 hours'
	`, workspaceID).Scan(&requests, &errorsCount, &average, &p95)
	return map[string]any{"requests_24h": requests, "errors_24h": errorsCount, "average_latency_ms": average, "p95_latency_ms": p95}, err
}

func (h *Handler) aiStats(r *http.Request, workspaceID int) (map[string]any, error) {
	var calls, failures, inputTokens, cachedInputTokens, outputTokens int
	var cost float64
	err := h.dbx.QueryRowContext(r.Context(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status <> 'success'),
			COALESCE(SUM(token_usage_input), 0), COALESCE(SUM(cached_input_tokens), 0),
			COALESCE(SUM(token_usage_output), 0),
			COALESCE(SUM(estimated_cost), 0)
		FROM v2_ai_call_logs
		WHERE workspace_id=$1 AND created_at > NOW() - INTERVAL '24 hours'
	`, workspaceID).Scan(&calls, &failures, &inputTokens, &cachedInputTokens, &outputTokens, &cost)
	if err != nil {
		return nil, err
	}
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT ai_module, model, status,
			COALESCE(token_usage_input, 0), COALESCE(cached_input_tokens, 0),
			COALESCE(token_usage_output, 0), COALESCE(estimated_cost, 0), created_at
		FROM v2_ai_call_logs
		WHERE workspace_id=$1 AND created_at > NOW() - INTERVAL '24 hours'
		ORDER BY created_at DESC, id DESC
		LIMIT 20
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recent := make([]map[string]any, 0)
	for rows.Next() {
		var module, model, status string
		var input, cached, output int
		var estimatedCost float64
		var createdAt time.Time
		if err := rows.Scan(&module, &model, &status, &input, &cached, &output, &estimatedCost, &createdAt); err != nil {
			return nil, err
		}
		recent = append(recent, map[string]any{
			"module": module, "model": model, "status": status,
			"input_tokens": input, "cached_input_tokens": cached, "output_tokens": output,
			"estimated_cost_usd": estimatedCost, "created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"calls_24h": calls, "failures_24h": failures, "input_tokens_24h": inputTokens,
		"cached_input_tokens_24h": cachedInputTokens, "output_tokens_24h": outputTokens,
		"estimated_cost_usd_24h": cost, "recent_calls": recent,
	}, nil
}

func (h *Handler) warnings(r *http.Request, workspaceID int, queue jobs.QueueStats) ([]Warning, error) {
	result := make([]Warning, 0)
	rows, err := h.dbx.QueryContext(r.Context(), `
		SELECT warning_key, severity, title, message
		FROM v2_system_warnings
		WHERE (workspace_id=$1 OR workspace_id IS NULL) AND status='active'
		ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Warning
		if err := rows.Scan(&item.Key, &item.Severity, &item.Title, &item.Message); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if queue.Failed > 0 {
		result = append(result, Warning{Key: "background_jobs_failed", Severity: "warning", Title: "Не все фоновые обновления завершены", Message: "Система повторит временные ошибки автоматически; окончательно неуспешные операции доступны в мониторинге."})
	}
	return result, rows.Err()
}
