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
// GOALS
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

		json.NewEncoder(w).Encode(g)
	}, dbx)
}

func postGoal(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

		var body struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		json.NewDecoder(r.Body).Decode(&body)

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

		json.NewEncoder(w).Encode(map[string]any{
			"id":         id,
			"title":      body.Title,
			"created_at": created,
		})
	}, dbx)
}

// ------------------------------------------------------------
// TASKS
// ------------------------------------------------------------

func getTasks(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {

		uid := r.Context().Value("user_id").(int)

		rows, err := dbx.Query(`
			SELECT 
				t.id,
				t.text,
				t.status,
				t.created_at,

				COALESCE(a.relevance, 0),
				COALESCE(a.impact, 0),
				COALESCE(a.urgency, 0),
				COALESCE(a.effort, 0),

				COALESCE(a.normalized_task, ''),
				COALESCE(a.avoidance_flag, false),
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
				t          tasks.Task
				rel, imp   int
				urg, eff   int
				normalized string
				avoidance  bool
				explain    string
			)

			err := rows.Scan(
				&t.ID,
				&t.Text,
				&t.Status,
				&t.CreatedAt,
				&rel,
				&imp,
				&urg,
				&eff,
				&normalized,
				&avoidance,
				&explain,
			)

			if err != nil {
				http.Error(w, "scan error: "+err.Error(), 500)
				return
			}

			t.NormalizedTask = normalized
			t.Avoidance = avoidance
			t.Explanation = explain

			t.Priority = int(
				float64(rel)*0.4 +
					float64(imp)*0.4 +
					float64(urg)*0.2 -
					float64(eff)*0.1,
			)

			result = append(result, t)
		}

		sort.Slice(result, func(i, j int) bool {
			return result[i].Priority > result[j].Priority
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}, dbx)
}

func postTask(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {

		uid := r.Context().Value("user_id").(int)

		var body struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Text == "" {
			http.Error(w, "empty task text", 400)
			return
		}

		var goalID *int
		dbx.QueryRow(`
			SELECT id FROM goals
			WHERE user_id=$1 AND is_active=TRUE
			ORDER BY id DESC LIMIT 1
		`, uid).Scan(&goalID)

		var taskID int
		var created time.Time
		var status string

		err := dbx.QueryRow(`
			INSERT INTO tasks (text, user_id, goal_id)
			VALUES ($1, $2, $3)
			RETURNING id, created_at, status
		`, body.Text, uid, goalID).Scan(&taskID, &created, &status)

		if err != nil {
			http.Error(w, "db error: "+err.Error(), 500)
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"id":         taskID,
			"text":       body.Text,
			"created_at": created,
			"status":     status,
			"priority":   0,
		})
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
	mux.Handle("/task/create", postTask(database))

	mux.Handle("/task/evaluate", withAuth(taskAIHandler.Evaluate, database))

	handler := cors.AllowAll().Handler(mux)

	log.Println("🚀 SERVER RUNNING ON :8080")
	http.ListenAndServe(":8080", handler)
}
