package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/cors"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
	goals "reup-goals-backend/internal/goals"
	tasks "reup-goals-backend/internal/tasks"
)

var jwtSecret = []byte("SUPER_SECRET_CHANGE_ME")

// ------------------------------------------------------------
// JWT
// ------------------------------------------------------------

func generateToken(userID int) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret)
}

func parseToken(tokenString string) (int, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return 0, err
	}
	data := token.Claims.(jwt.MapClaims)
	uidFloat, ok := data["user_id"].(float64)
	if !ok {
		return 0, err
	}
	return int(uidFloat), nil
}

// ------------------------------------------------------------
// MIDDLEWARE
// ------------------------------------------------------------

func withAuth(next http.HandlerFunc, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(h, "Bearer ")
		userID, err := parseToken(tokenString)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), "user_id", userID)
		next(w, r.WithContext(ctx))
	}
}

// ------------------------------------------------------------
// AUTH HANDLERS
// ------------------------------------------------------------

func registerHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		json.NewDecoder(r.Body).Decode(&body)
		if body.Email == "" || body.Password == "" {
			http.Error(w, "email & password required", http.StatusBadRequest)
			return
		}

		var id int
		err := dbx.QueryRow(`
			INSERT INTO users (email, password)
			VALUES ($1, $2)
			RETURNING id
		`, body.Email, body.Password).Scan(&id)

		if err != nil {
			http.Error(w, "user exists?", http.StatusBadRequest)
			return
		}

		token, _ := generateToken(id)

		json.NewEncoder(w).Encode(map[string]any{
			"user_id": id,
			"token":   token,
		})
	}
}

func loginHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		var id int
		err := dbx.QueryRow(`
			SELECT id FROM users WHERE email=$1 AND password=$2
		`, body.Email, body.Password).Scan(&id)

		if err != nil {
			http.Error(w, "invalid login", http.StatusUnauthorized)
			return
		}

		token, _ := generateToken(id)

		json.NewEncoder(w).Encode(map[string]any{
			"user_id": id,
			"token":   token,
		})
	}
}

func meHandler(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)
		var email string
		dbx.QueryRow("SELECT email FROM users WHERE id=$1", uid).Scan(&email)

		json.NewEncoder(w).Encode(map[string]any{
			"user_id": uid,
			"email":   email,
		})
	}, dbx)
}

// ------------------------------------------------------------
// GOALS (Этап 3 расширим, тут не трогаем)
// ------------------------------------------------------------

func getGoal(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

		row := dbx.QueryRow(`
			SELECT id, title, description, is_active, created_at
			FROM goals
			WHERE user_id = $1
			ORDER BY id DESC LIMIT 1
		`, uid)

		var g goals.Goal
		err := row.Scan(&g.ID, &g.Title, &g.Description, &g.IsActive, &g.CreatedAt)
		if err != nil {
			http.Error(w, "no goal", http.StatusNotFound)
			return
		}

		// ✅ ДОБАВЛЯЕМ: подтягиваем context из goal_context.summary_for_ai
		var ctx sql.NullString
		_ = dbx.QueryRow(`
			SELECT summary_for_ai
			FROM goal_context
			WHERE goal_id = $1
			LIMIT 1
		`, g.ID).Scan(&ctx)

		contextText := ""
		if ctx.Valid {
			contextText = ctx.String
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":          g.ID,
			"title":       g.Title,
			"description": g.Description,
			"context":     contextText,
			"is_active":   g.IsActive,
			"created_at":  g.CreatedAt,
		})
	}, dbx)
}

func postGoal(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Context     string `json:"context"` // ✅ NEW
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

		// ✅ NEW: сохраняем context в goal_context.summary_for_ai
		// ВАЖНО: goal_context.context_json = NOT NULL, поэтому кладём минимум пустой JSON.
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


		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":          id,
			"title":       body.Title,
			"description": body.Description,
			"context":     body.Context,
			"created_at":  created,
		})
	}, dbx)
}


// ------------------------------------------------------------
// TASKS HELPERS
// ------------------------------------------------------------

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
	// Берём активную цель
	var gid *int
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

	// Контекст (summary_for_ai) — если есть, добавим; если нет, просто пусто
	var ctxText sql.NullString
	_ = dbx.QueryRow(`
		SELECT summary_for_ai
		FROM goal_context
		WHERE goal_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, *gid).Scan(&ctxText)

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

	return gid, strings.Join(parts, "\n\n"), nil
}

func fetchTaskFullpack(dbx *sql.DB, uid int, taskID int) (tasks.Task, error) {
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
		WHERE t.user_id = $1 AND t.id = $2
	`, uid, taskID)

	var (
		t          tasks.Task
		rel, imp   int
		urg, eff   int
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
		return tasks.Task{}, err
	}

	t.Priority = int(
		float64(rel)*0.4 +
			float64(imp)*0.4 +
			float64(urg)*0.2 -
			float64(eff)*0.1,
	)

	return t, nil
}

// ------------------------------------------------------------
// TASKS HANDLERS
// ------------------------------------------------------------

func getTasks(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {

		uid := r.Context().Value("user_id").(int)

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

		var result []tasks.Task

		for rows.Next() {
			var (
				t        tasks.Task
				rel, imp int
				urg, eff int
			)

			err := rows.Scan(
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
	// тайбрейкер: старые выше или ниже — выбери один.
	// Я предлагаю: более ранние выше (чтобы не прыгало)
	if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	}
	return result[i].ID < result[j].ID
})


		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}, dbx)
}

// ✅ ОБНОВЛЁННЫЙ CREATE: принимает title/description, сохраняет, запускает AI, отдаёт fullpack
func postTask(dbx *sql.DB, taskAI *tasks.TaskHandler) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {

		uid := r.Context().Value("user_id").(int)

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		json.NewDecoder(r.Body).Decode(&body)

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
		`, combined, safeTitle, desc, uid, goalID).Scan(&taskID, &created, &status)

		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}

		// ✅ AI on create (НЕ ЛОМАЕМ CRUD если AI упал)
		if _, aiErr := taskAI.EvaluateAndStore(context.Background(), taskID, goalSummary, combined); aiErr != nil {
			log.Printf("[WARN] AI evaluate failed on CREATE task_id=%d: %v", taskID, aiErr)
			w.Header().Set("X-AI-Error", "1")
		}


		// ✅ fullpack response (даже если AI упал)
full, err := fetchTaskFullpack(dbx, uid, taskID)
if err != nil {
    // fallback: вернём хотя бы то, что знаем
    log.Printf("[WARN] fetchTaskFullpack failed on CREATE task_id=%d: %v", taskID, err)
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{
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

w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(full)
	}, dbx)
}

// ✅ НОВЫЙ UPDATE: обновляет title/description/text, запускает AI, отдаёт fullpack
func updateTask(dbx *sql.DB, taskAI *tasks.TaskHandler) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

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
			SET title = $1, description = $2, text = $3
			WHERE id = $4 AND user_id = $5
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

		_, goalSummary, err := fetchActiveGoalAndSummary(dbx, uid)
		if err != nil {
			http.Error(w, "no active goal", 404)
			return
		}

		// ✅ AI on update (НЕ ЛОМАЕМ CRUD если AI упал)
if _, aiErr := taskAI.EvaluateAndStore(context.Background(), body.TaskID, goalSummary, combined); aiErr != nil {
    log.Printf("[WARN] AI evaluate failed on UPDATE task_id=%d: %v", body.TaskID, aiErr)
    w.Header().Set("X-AI-Error", "1")
}

		full, err := fetchTaskFullpack(dbx, uid, body.TaskID)
		if err != nil {
			log.Printf("[WARN] fetchTaskFullpack failed on UPDATE task_id=%d: %v", body.TaskID, err)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id":          body.TaskID,
				"text":        combined,
				"title":       safeTitle,
				"description": desc,
				"status":      "active",
				"priority":    0,
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(full)
	}, dbx)
}

// ------------------------------------------------------------
// MAIN
// ------------------------------------------------------------

func main() {
	cfg := config.Load()

	database, err := db.Connect(cfg.ConnString())
	if err != nil {
		log.Fatal("DB error:", err)
	}
	defer database.Close()

	aiClient := ai.New(cfg.OpenAIKey, cfg.OpenAIModel)
	taskAIHandler := tasks.New(aiClient, database)

	mux := http.NewServeMux()

	mux.Handle("/auth/register", registerHandler(database))
	mux.Handle("/auth/login", loginHandler(database))
	mux.Handle("/auth/me", meHandler(database))

	mux.Handle("/goal", getGoal(database))
	mux.Handle("/goal/create", postGoal(database))

	mux.Handle("/tasks", getTasks(database))

	// ✅ Create task теперь принимает title/description и возвращает fullpack
	mux.Handle("/task/create", postTask(database, taskAIHandler))

	// ✅ New update
	mux.Handle("/task/update", updateTask(database, taskAIHandler))

	// оставляем evaluate как отдельный endpoint (может пригодиться)
	mux.Handle("/task/evaluate", withAuth(taskAIHandler.Evaluate, database))

	handler := cors.AllowAll().Handler(mux)

	log.Println("🚀 SERVER RUNNING ON :8080")
	http.ListenAndServe(":8080", handler)
}
