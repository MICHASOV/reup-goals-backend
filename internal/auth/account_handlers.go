package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

type AccountDataCleaner interface {
	CleanupUserData(context.Context, int) error
}

func LogoutHandler(dbx *sql.DB, secureCookie bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := dbx.ExecContext(r.Context(), `UPDATE users SET auth_version=auth_version+1 WHERE id=$1`, uid); err != nil {
			http.Error(w, "logout_failed", http.StatusInternalServerError)
			return
		}
		ClearSessionCookie(w, secureCookie)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
		})
	}
}

func DeleteAccountHandler(dbx *sql.DB, cleaners ...AccountDataCleaner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		for _, cleaner := range cleaners {
			if cleaner == nil {
				continue
			}
			if err := cleaner.CleanupUserData(r.Context(), uid); err != nil {
				http.Error(w, "external_data_cleanup_failed", http.StatusBadGateway)
				return
			}
		}

		tx, err := dbx.Begin()
		if err != nil {
			http.Error(w, "db begin failed", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// 1) task_clarifications (привязаны к user_id + task_id)
		if _, err := tx.Exec(`DELETE FROM task_clarifications WHERE user_id = $1`, uid); err != nil {
			http.Error(w, "delete task_clarifications failed", http.StatusInternalServerError)
			return
		}

		// 2) task_ai_state (привязана к task_id)
		if _, err := tx.Exec(`
			DELETE FROM task_ai_state
			WHERE task_id IN (SELECT id FROM tasks WHERE user_id = $1)
		`, uid); err != nil {
			http.Error(w, "delete task_ai_state failed", http.StatusInternalServerError)
			return
		}

		// 3) tasks
		if _, err := tx.Exec(`DELETE FROM tasks WHERE user_id = $1`, uid); err != nil {
			http.Error(w, "delete tasks failed", http.StatusInternalServerError)
			return
		}

		// 4) goal_context (привязана к goal_id)
		if _, err := tx.Exec(`
			DELETE FROM goal_context
			WHERE goal_id IN (SELECT id FROM goals WHERE user_id = $1)
		`, uid); err != nil {
			http.Error(w, "delete goal_context failed", http.StatusInternalServerError)
			return
		}

		// 5) goals
		if _, err := tx.Exec(`DELETE FROM goals WHERE user_id = $1`, uid); err != nil {
			http.Error(w, "delete goals failed", http.StatusInternalServerError)
			return
		}

		// 6) analytics_events
		if _, err := tx.Exec(`DELETE FROM analytics_events WHERE user_id = $1`, uid); err != nil {
			http.Error(w, "delete analytics_events failed", http.StatusInternalServerError)
			return
		}

		// 7) users
		if _, err := tx.Exec(`DELETE FROM users WHERE id = $1`, uid); err != nil {
			http.Error(w, "delete user failed", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			http.Error(w, "db commit failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
		})
	}
}
