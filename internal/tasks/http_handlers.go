package tasks

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"reup-goals-backend/internal/analytics"
	"reup-goals-backend/internal/auth"
)

func buildCombinedTaskText(title, description string) (safeTitle, desc, combined string) {
	t := strings.TrimSpace(title)
	d := strings.TrimSpace(description)

	if t == "" && d != "" {
		t = "Без названия"
	}
	if t == "" && d == "" {
		return "", "", ""
	}

	safeTitle = t
	desc = d

	combined = safeTitle
	if desc != "" {
		combined = combined + "\n\n" + desc
	}
	return
}

func fetchActiveGoalAndSummary(dbx *sql.DB, uid int) (goalID *int, goalSummary string, err error) {
	var gid int
	var title, desc string

	err = dbx.QueryRow(`
		SELECT id, COALESCE(title,''), COALESCE(description,'')
		FROM goals
		WHERE user_id=$1 AND is_active=TRUE
		ORDER BY id DESC LIMIT 1
	`, uid).Scan(&gid, &title, &desc)
	if err != nil {
		return nil, "", err
	}

	var ctxText sql.NullString
	_ = dbx.QueryRow(`
		SELECT summary_for_ai
		FROM goal_context
		WHERE goal_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, gid).Scan(&ctxText)

	parts := []string{}
	if strings.TrimSpace(title) != "" {
		parts = append(parts, strings.TrimSpace(title))
	}
	if strings.TrimSpace(desc) != "" {
		parts = append(parts, strings.TrimSpace(desc))
	}
	if ctxText.Valid && strings.TrimSpace(ctxText.String) != "" {
		parts = append(parts, "CONTEXT:\n"+strings.TrimSpace(ctxText.String))
	}

	return &gid, strings.Join(parts, "\n\n"), nil
}

func fetchTaskFullpack(dbx *sql.DB, uid int, taskID int) (Task, error) {
	row := dbx.QueryRow(`
		SELECT 
			t.id,
			t.text,
			COALESCE(t.title,''),
			COALESCE(t.description,''),
			t.status,
			t.created_at,

			COALESCE(a.relevance, 0),
			COALESCE(a.impact, 0),
			COALESCE(a.urgency, 0),
			COALESCE(a.effort, 0),

			COALESCE(a.normalized_task, ''),
			COALESCE(a.avoidance_flag, false),
			COALESCE(a.trap_task, false),
			COALESCE(a.clarification_needed, false),
			COALESCE(a.clarification_question, ''),
			COALESCE(a.explanation_short, '')

		FROM tasks t
		LEFT JOIN LATERAL (
			SELECT 
				relevance,
				impact,
				urgency,
				effort,
				normalized_task,
				avoidance_flag,
				trap_task,
				clarification_needed,
				clarification_question,
				explanation_short
			FROM task_ai_state
			WHERE task_id = t.id
			ORDER BY updated_at DESC
			LIMIT 1
		) a ON TRUE
		 LEFT JOIN LATERAL (
		SELECT question, answer
		FROM task_clarifications
		WHERE task_id = t.id AND user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
		) c ON TRUE
		WHERE t.user_id = $1 AND t.id = $2
	`, uid, taskID)

	var (
		t        Task
		rel, imp int
		urg, eff int
	)

	err := row.Scan(
		&t.ID,
		&t.Text,
		&t.Title,
		&t.Description,
		&t.Status,
		&t.CreatedAt,
		&rel,
		&imp,
		&urg,
		&eff,
		&t.NormalizedTask,
		&t.AvoidanceFlag,
		&t.TrapTask,
		&t.ClarificationNeeded,
		&t.ClarificationQuestion,
		&t.ExplanationShort,
	)
	if err != nil {
		return Task{}, err
	}

	t.Priority = int(
		float64(rel)*0.4 +
			float64(imp)*0.4 +
			float64(urg)*0.2 -
			float64(eff)*0.1,
	)

	return t, nil
}

// -------------------------------
// HANDLERS
// -------------------------------

func GetTasksHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rows, err := dbx.Query(`
			SELECT 
				t.id,
				t.text,
				COALESCE(t.title,''),
				COALESCE(t.description,''),
				t.status,
				t.created_at,

				COALESCE(a.relevance, 0),
				COALESCE(a.impact, 0),
				COALESCE(a.urgency, 0),
				COALESCE(a.effort, 0),

				COALESCE(a.normalized_task, ''),
				COALESCE(a.avoidance_flag, false),
				COALESCE(a.trap_task, false),
				COALESCE(a.clarification_needed, false),
				COALESCE(a.clarification_question, ''),
				COALESCE(a.explanation_short, '')

			FROM tasks t
			LEFT JOIN LATERAL (
				SELECT 
					relevance,
					impact,
					urgency,
					effort,
					normalized_task,
					avoidance_flag,
					trap_task,
					clarification_needed,
					clarification_question,
					explanation_short
				FROM task_ai_state
				WHERE task_id = t.id
				ORDER BY updated_at DESC
				LIMIT 1
			) a ON TRUE
			WHERE t.user_id = $1
			ORDER BY t.id DESC
		`, uid)
		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}
		defer rows.Close()

		var result []Task
		for rows.Next() {
			var (
				t        Task
				rel, imp int
				urg, eff int
			)

			if err := rows.Scan(
				&t.ID,
				&t.Text,
				&t.Title,
				&t.Description,
				&t.Status,
				&t.CreatedAt,
				&rel,
				&imp,
				&urg,
				&eff,
				&t.NormalizedTask,
				&t.AvoidanceFlag,
				&t.TrapTask,
				&t.ClarificationNeeded,
				&t.ClarificationQuestion,
				&t.ExplanationShort,
			); err != nil {
				http.Error(w, "scan error: "+err.Error(), 500)
				return
			}

			t.Priority = int(
				float64(rel)*0.4 +
					float64(imp)*0.4 +
					float64(urg)*0.2 -
					float64(eff)*0.1,
			)

			result = append(result, t)
		}

		sort.Slice(result, func(i, j int) bool {
			if result[i].Priority != result[j].Priority {
				return result[i].Priority > result[j].Priority
			}
			if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
				return result[i].CreatedAt.Before(result[j].CreatedAt)
			}
			return result[i].ID < result[j].ID
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

func CreateTaskHandler(dbx *sql.DB, taskAI *TaskHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		safeTitle, desc, combined := buildCombinedTaskText(body.Title, body.Description)
		if combined == "" {
			http.Error(w, "empty task", 400)
			return
		}

		goalID, goalSummary, err := fetchActiveGoalAndSummary(dbx, uid)
		if err != nil {
			http.Error(w, "no active goal", 404)
			return
		}

		var taskID int
		var created time.Time
		var status string

		err = dbx.QueryRow(`
			INSERT INTO tasks (text, title, description, user_id, goal_id)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, created_at, status
		`, combined, safeTitle, desc, uid, *goalID).Scan(&taskID, &created, &status)
		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}

		// analytics: task_created
		{
			env := analytics.FromRequest(r)
			env.UserID = uid

			props := map[string]any{
				"task_id":          taskID,
				"input_method":     "text",
				"text_len":         len(combined),
				"has_deadline":     false,
				"deadline_ts":      nil,
				"created_from":     "unknown",
				"goal_id":          *goalID,
				"initial_priority": nil,
			}

			_ = analytics.Log(r.Context(), dbx, env, "task_created", props, analytics.SourceEventKeyFromRequest(r))
		}

		// AI on create
		aiOK := true
		if _, aiErr := taskAI.EvaluateAndStore(r.Context(), taskID, goalSummary, combined); aiErr != nil {
			aiOK = false
			log.Printf("[WARN] AI evaluate failed on CREATE task_id=%d: %v", taskID, aiErr)
			w.Header().Set("X-AI-Error", "1")
		}

		full, err := fetchTaskFullpack(dbx, uid, taskID)
		if err != nil {
			log.Printf("[WARN] fetchTaskFullpack failed on CREATE task_id=%d: %v", taskID, err)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          taskID,
				"text":        combined,
				"title":       safeTitle,
				"description": desc,
				"status":      status,
				"created_at":  created,
				"priority":    0,
			})
			return
		}

		// analytics: task_priority_assigned
		if aiOK {
			var rel, imp, urg, eff int
			var model string
			_ = dbx.QueryRow(`
				SELECT relevance, impact, urgency, effort, COALESCE(model_version,'')
				FROM task_ai_state
				WHERE task_id=$1
				ORDER BY updated_at DESC
				LIMIT 1
			`, taskID).Scan(&rel, &imp, &urg, &eff, &model)

			env := analytics.FromRequest(r)
			env.UserID = uid

			props := map[string]any{
				"task_id":         taskID,
				"goal_id":         *goalID,
				"priority_before": nil,
				"priority_after":  analytics.TierFromScore(full.Priority),
				"priority_source": "ai",
				"ai_scores": map[string]any{
				"relevance":     rel, // 0..100 (или как у тебя)
				"impact":        imp,
				"urgency":       urg,
				"effort":        eff,
				"model_version": model,
			},
			}

			_ = analytics.Log(r.Context(), dbx, env, "task_priority_assigned", props, analytics.SourceEventKeyFromRequest(r))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(full)
	}
}

func UpdateTaskHandler(dbx *sql.DB, taskAI *TaskHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			TaskID      int    `json:"task_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		if body.TaskID == 0 {
			http.Error(w, "task_id required", 400)
			return
		}

		safeTitle, desc, combined := buildCombinedTaskText(body.Title, body.Description)
		if combined == "" {
			http.Error(w, "empty task", 400)
			return
		}

		// update owned task
		res, err := dbx.Exec(`
			UPDATE tasks
			SET title=$1, description=$2, text=$3
			WHERE id=$4 AND user_id=$5
		`, safeTitle, desc, combined, body.TaskID, uid)
		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			http.Error(w, "task not found", 404)
			return
		}

		// analytics: task_updated
		{
			env := analytics.FromRequest(r)
			env.UserID = uid

			props := map[string]any{
				"task_id":  body.TaskID,
				"text_len": len(combined),
			}
			_ = analytics.Log(r.Context(), dbx, env, "task_updated", props, analytics.SourceEventKeyFromRequest(r))
		}

		// goal_id для event
		var gid sql.NullInt64
		_ = dbx.QueryRow(`SELECT goal_id FROM tasks WHERE id=$1 AND user_id=$2`, body.TaskID, uid).Scan(&gid)

		_, goalSummary, err := fetchActiveGoalAndSummary(dbx, uid)
		if err != nil {
			http.Error(w, "no active goal", 404)
			return
		}

		aiOK := true
		if _, aiErr := taskAI.EvaluateAndStore(r.Context(), body.TaskID, goalSummary, combined); aiErr != nil {
			aiOK = false
			log.Printf("[WARN] AI evaluate failed on UPDATE task_id=%d: %v", body.TaskID, aiErr)
			w.Header().Set("X-AI-Error", "1")
		}

		full, err := fetchTaskFullpack(dbx, uid, body.TaskID)
		if err != nil {
			log.Printf("[WARN] fetchTaskFullpack failed on UPDATE task_id=%d: %v", body.TaskID, err)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          body.TaskID,
				"text":        combined,
				"title":       safeTitle,
				"description": desc,
				"status":      "active",
				"priority":    0,
			})
			return
		}

		if aiOK {
			var rel, imp, urg, eff int
			var model string
			_ = dbx.QueryRow(`
				SELECT relevance, impact, urgency, effort, COALESCE(model_version,'')
				FROM task_ai_state
				WHERE task_id=$1
				ORDER BY updated_at DESC
				LIMIT 1
			`, body.TaskID).Scan(&rel, &imp, &urg, &eff, &model)

			env := analytics.FromRequest(r)
			env.UserID = uid

			props := map[string]any{
				"task_id":         body.TaskID,
				"goal_id":         func() any { if gid.Valid { return int(gid.Int64) }; return nil }(),
				"priority_before": nil,
				"priority_after":  analytics.TierFromScore(full.Priority),
				"priority_source": "ai",
				"ai_scores": map[string]any{
				"relevance":     rel, // 0..100 (или как у тебя)
				"impact":        imp,
				"urgency":       urg,
				"effort":        eff,
				"model_version": model,
			},
			}

			_ = analytics.Log(r.Context(), dbx, env, "task_priority_assigned", props, analytics.SourceEventKeyFromRequest(r))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(full)
	}
}

func SetTaskStatusHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			TaskID int    `json:"task_id"`
			Status string `json:"status"` // active|done|canceled
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		if body.TaskID == 0 {
			http.Error(w, "task_id required", 400)
			return
		}

		switch body.Status {
		case "active", "done", "canceled":
		default:
			http.Error(w, "invalid status", 400)
			return
		}

		var prevStatus string
		var createdAt time.Time
		var goalID sql.NullInt64
		_ = dbx.QueryRow(`
			SELECT status, created_at, goal_id
			FROM tasks
			WHERE id=$1 AND user_id=$2
		`, body.TaskID, uid).Scan(&prevStatus, &createdAt, &goalID)

		res, err := dbx.Exec(`
			UPDATE tasks
			SET status = $1
			WHERE id = $2 AND user_id = $3
		`, body.Status, body.TaskID, uid)
		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			http.Error(w, "task not found", 404)
			return
		}

		full, err := fetchTaskFullpack(dbx, uid, body.TaskID)
		if err != nil {
			http.Error(w, "fetch error: "+err.Error(), 500)
			return
		}

		// analytics: task_completed / task_uncompleted
		if prevStatus != "" && prevStatus != body.Status {
			env := analytics.FromRequest(r)
			env.UserID = uid

			tier := analytics.TierFromScore(full.Priority)
			timeSinceCreated := int(time.Now().UTC().Sub(createdAt).Seconds())


			if prevStatus != "done" && body.Status == "done" {
				props := map[string]any{
					"task_id":                       body.TaskID,
					"goal_id":                       nullableInt(goalID),
					"priority_at_completion":        tier,
					"priority_source_at_completion": "ai",
					"time_since_created_sec":        timeSinceCreated,
					"completed_from":                "unknown",
				}
				_ = analytics.Log(r.Context(), dbx, env, "task_completed", props, analytics.SourceEventKeyFromRequest(r))
			}

			if prevStatus == "done" && body.Status != "done" {
				props := map[string]any{
					"task_id":                   body.TaskID,
					"goal_id":                   nullableInt(goalID),
					"priority_at_uncomplete":    tier,
					"time_since_completed_sec":  nil,
				}
				_ = analytics.Log(r.Context(), dbx, env, "task_uncompleted", props, analytics.SourceEventKeyFromRequest(r))
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(full)
	}
}

func CreateTaskClarificationHandler(dbx *sql.DB, taskAI *TaskHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body struct {
			TaskID   int    `json:"task_id"`
			Question string `json:"question"`
			Answer   string `json:"answer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		if body.TaskID == 0 {
			http.Error(w, "task_id required", 400)
			return
		}
		q := strings.TrimSpace(body.Question)
		a := strings.TrimSpace(body.Answer)
		if q == "" || a == "" {
			http.Error(w, "question and answer required", 400)
			return
		}

		// check ownership + get current combined text
		var combined string
		err := dbx.QueryRow(`
			SELECT text
			FROM tasks
			WHERE id=$1 AND user_id=$2
		`, body.TaskID, uid).Scan(&combined)
		if err != nil {
			http.Error(w, "task not found", 404)
			return
		}

		// insert clarification
		_, err = dbx.Exec(`
			INSERT INTO task_clarifications (user_id, task_id, question, answer)
			VALUES ($1, $2, $3, $4)
		`, uid, body.TaskID, q, a)
		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}

		// analytics (опционально, но полезно)
		{
			env := analytics.FromRequest(r)
			env.UserID = uid
			props := map[string]any{
				"task_id":   body.TaskID,
				"q_len":     len(q),
				"answer_len": len(a),
			}
			_ = analytics.Log(r.Context(), dbx, env, "task_clarification_created", props, analytics.SourceEventKeyFromRequest(r))
		}

		// ✅ ВАЖНО: на MVP можно НЕ гонять AI.
		// Но если хочешь — можно перезапустить оценку на основе уточнения.
		// Самый простой вариант: просто добавим уточнение к тексту, который уходит в AI.
		_, goalSummary, gErr := fetchActiveGoalAndSummary(dbx, uid)
		if gErr == nil {
			augmented := combined + "\n\nCLARIFICATION:\nQ: " + q + "\nA: " + a
			if _, aiErr := taskAI.EvaluateAndStore(r.Context(), body.TaskID, goalSummary, augmented); aiErr != nil {
				log.Printf("[WARN] AI evaluate failed on CLARIFICATION task_id=%d: %v", body.TaskID, aiErr)
				w.Header().Set("X-AI-Error", "1")
			}
		}

		full, err := fetchTaskFullpack(dbx, uid, body.TaskID)
		if err != nil {
			http.Error(w, "fetch error: "+err.Error(), 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(full)
	}
}


func nullableInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return int(v.Int64)
}
