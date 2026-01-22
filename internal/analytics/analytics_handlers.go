package analytics

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// app_opened — базовая метрика “открыли приложение”
func AppOpenedHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			ColdStart bool   `json:"cold_start"`
			From      string `json:"from"` // push/deeplink/icon/unknown
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		env := FromRequest(r)
		env.UserID = uid

		props := map[string]any{
			"cold_start": body.ColdStart,
			"from":       body.From,
		}

		_ = Log(r.Context(), dbx, env, "app_opened", props, SourceEventKeyFromRequest(r))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// focus_task_shown — когда на экране фокуса показали “главную задачу”
func FocusTaskShownHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			TaskID   int    `json:"task_id"`
			GoalID   *int   `json:"goal_id"`        // optional
			Priority string `json:"priority_tier"`  // P1/P2/P3 optional
			Source   string `json:"source"`        // initial/refresh/return/unknown
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		env := FromRequest(r)
		env.UserID = uid

		props := map[string]any{
			"task_id":       body.TaskID,
			"goal_id":       body.GoalID,
			"priority_tier": body.Priority,
			"source":        body.Source,
		}

		_ = Log(r.Context(), dbx, env, "focus_task_shown", props, SourceEventKeyFromRequest(r))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// focus_task_changed — когда “главная задача” изменилась
func FocusTaskChangedHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			FromTaskID int    `json:"from_task_id"`
			ToTaskID   int    `json:"to_task_id"`
			Reason     string `json:"reason"` // completed/reprioritized/manual/unknown
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		env := FromRequest(r)
		env.UserID = uid

		props := map[string]any{
			"from_task_id": body.FromTaskID,
			"to_task_id":   body.ToTaskID,
			"reason":       body.Reason,
		}

		_ = Log(r.Context(), dbx, env, "focus_task_changed", props, SourceEventKeyFromRequest(r))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}
