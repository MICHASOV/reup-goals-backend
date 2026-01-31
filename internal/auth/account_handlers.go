package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// JWT stateless => сервер ничего не “разлогинивает”.
		// Фронт просто удаляет токен.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
		})
	}
}

func DeleteAccountHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
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
