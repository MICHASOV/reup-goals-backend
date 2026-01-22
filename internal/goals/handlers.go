package goals

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"reup-goals-backend/internal/analytics"
	"reup-goals-backend/internal/auth"
)

func GetGoalHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		row := dbx.QueryRow(`
			SELECT id, title, description, is_active, created_at
			FROM goals
			WHERE user_id = $1
			ORDER BY id DESC LIMIT 1
		`, uid)

		var (
			id        int
			title     string
			desc      string
			isActive  bool
			createdAt time.Time
		)
		if err := row.Scan(&id, &title, &desc, &isActive, &createdAt); err != nil {
			http.Error(w, "no goal", http.StatusNotFound)
			return
		}

		// context из goal_context.summary_for_ai
		var ctx sql.NullString
		_ = dbx.QueryRow(`
			SELECT summary_for_ai
			FROM goal_context
			WHERE goal_id = $1
			ORDER BY updated_at DESC
			LIMIT 1
		`, id).Scan(&ctx)

		contextText := ""
		if ctx.Valid {
			contextText = ctx.String
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          id,
			"title":       title,
			"description": desc,
			"context":     contextText,
			"is_active":   isActive,
			"created_at":  createdAt,
		})
	}
}

func CreateGoalHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Context     string `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		var id int
		var created time.Time
		err := dbx.QueryRow(`
			INSERT INTO goals (title, description, is_active, user_id)
			VALUES ($1, $2, TRUE, $3)
			RETURNING id, created_at
		`, body.Title, body.Description, uid).Scan(&id, &created)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}

		// сохраняем context в goal_context.summary_for_ai
		_, err = dbx.Exec(`
			INSERT INTO goal_context (goal_id, context_json, summary_for_ai, updated_at)
			VALUES ($1, '{}'::jsonb, $2, now())
			ON CONFLICT (goal_id) DO UPDATE SET
				context_json = COALESCE(goal_context.context_json, '{}'::jsonb),
				summary_for_ai = EXCLUDED.summary_for_ai,
				updated_at = now()
		`, id, body.Context)
		if err != nil {
			http.Error(w, "db error (goal_context): "+err.Error(), 500)
			return
		}

		// analytics: goal_created (НЕ логируем сырой текст)
		{
			env := analytics.FromRequest(r)
			env.UserID = uid

			textLen :=
				len(strings.TrimSpace(body.Title)) +
					len(strings.TrimSpace(body.Description)) +
					len(strings.TrimSpace(body.Context))

			props := map[string]any{
				"goal_id":      id,
				"text_len":     textLen,
				"input_method": "text",
				"has_template": false,
			}

			_ = analytics.Log(r.Context(), dbx, env, "goal_created", props, analytics.SourceEventKeyFromRequest(r))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          id,
			"title":       body.Title,
			"description": body.Description,
			"context":     strings.TrimSpace(body.Context),
			"is_active":   true,
			"created_at":  created,
		})
	}
}

func UpdateGoalHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Context     string `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// берём активную цель
		var goalID int
		var prevTitle, prevDesc string
		err := dbx.QueryRow(`
			SELECT id, COALESCE(title,''), COALESCE(description,'')
			FROM goals
			WHERE user_id=$1 AND is_active=TRUE
			ORDER BY id DESC
			LIMIT 1
		`, uid).Scan(&goalID, &prevTitle, &prevDesc)
		if err != nil {
			http.Error(w, "no active goal", http.StatusNotFound)
			return
		}

		// prev context
		var prevCtx sql.NullString
		_ = dbx.QueryRow(`
			SELECT summary_for_ai
			FROM goal_context
			WHERE goal_id=$1
			ORDER BY updated_at DESC
			LIMIT 1
		`, goalID).Scan(&prevCtx)

		_, err = dbx.Exec(`
			UPDATE goals
			SET title=$1, description=$2
			WHERE id=$3 AND user_id=$4
		`, body.Title, body.Description, goalID, uid)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}

		_, err = dbx.Exec(`
			INSERT INTO goal_context (goal_id, context_json, summary_for_ai, updated_at)
			VALUES ($1, '{}'::jsonb, $2, now())
			ON CONFLICT (goal_id) DO UPDATE SET
				context_json = COALESCE(goal_context.context_json, '{}'::jsonb),
				summary_for_ai = EXCLUDED.summary_for_ai,
				updated_at = now()
		`, goalID, body.Context)
		if err != nil {
			http.Error(w, "db error (goal_context): "+err.Error(), 500)
			return
		}

		// analytics: goal_updated
		{
			env := analytics.FromRequest(r)
			env.UserID = uid

			changedTitle := strings.TrimSpace(prevTitle) != strings.TrimSpace(body.Title)
			changedDesc := strings.TrimSpace(prevDesc) != strings.TrimSpace(body.Description)
			changedCtx := strings.TrimSpace(prevCtx.String) != strings.TrimSpace(body.Context)

			textLen :=
				len(strings.TrimSpace(body.Title)) +
					len(strings.TrimSpace(body.Description)) +
					len(strings.TrimSpace(body.Context))

			props := map[string]any{
				"goal_id":  goalID,
				"text_len": textLen,
				"changed": map[string]any{
					"title":       changedTitle,
					"description": changedDesc,
					"context":     changedCtx,
				},
				"input_method": "text",
			}

			_ = analytics.Log(r.Context(), dbx, env, "goal_updated", props, analytics.SourceEventKeyFromRequest(r))
		}

		// ✅ КЛЮЧЕВОЕ ИСПРАВЛЕНИЕ: возвращаем НОРМАЛЬНЫЙ JSON как у /goal
		var isActive bool
		var createdAt time.Time
		_ = dbx.QueryRow(`
			SELECT is_active, created_at
			FROM goals
			WHERE id=$1 AND user_id=$2
		`, goalID, uid).Scan(&isActive, &createdAt)

		var ctx sql.NullString
		_ = dbx.QueryRow(`
			SELECT summary_for_ai
			FROM goal_context
			WHERE goal_id=$1
			ORDER BY updated_at DESC
			LIMIT 1
		`, goalID).Scan(&ctx)

		contextText := ""
		if ctx.Valid {
			contextText = ctx.String
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          goalID,                // ✅ было goal_id — из-за этого ломался фронт
			"title":       body.Title,
			"description": body.Description,
			"context":     contextText,
			"is_active":   isActive,
			"created_at":  createdAt,
		})
	}
}

func ResetGoalHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		tx, err := dbx.BeginTx(r.Context(), nil)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}
		defer func() { _ = tx.Rollback() }()

		// деактивируем все цели
		resGoals, err := tx.ExecContext(r.Context(), `
			UPDATE goals
			SET is_active=FALSE
			WHERE user_id=$1 AND is_active=TRUE
		`, uid)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}
		goalsDeactivated, _ := resGoals.RowsAffected()

		// гасим активные таски (чтобы пользователь реально “сбросился”)
		resTasks, err := tx.ExecContext(r.Context(), `
			UPDATE tasks
			SET status='canceled'
			WHERE user_id=$1 AND status='active'
		`, uid)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}
		tasksCanceled, _ := resTasks.RowsAffected()

		if err := tx.Commit(); err != nil {
			http.Error(w, "db error", 500)
			return
		}

		// analytics: goal_reset
		{
			env := analytics.FromRequest(r)
			env.UserID = uid

			props := map[string]any{
				"goals_deactivated": goalsDeactivated,
				"tasks_canceled":    tasksCanceled,
				"reset_from":        "unknown",
			}
			_ = analytics.Log(r.Context(), dbx, env, "goal_reset", props, analytics.SourceEventKeyFromRequest(r))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"goals_deactivated": goalsDeactivated,
			"tasks_canceled":    tasksCanceled,
		})
	}
}
