package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/cors"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
	taskshandler "reup-goals-backend/internal/tasks"
)

var jwtSecret = []byte("SUPER_SECRET_CHANGE_ME")

// ------------------------------------------------------------
// JWT helpers
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

// Middleware
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
// Auth Handlers
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
// GOALS (теперь привязаны к user_id)
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

		var g Goal
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
// TASKS (тоже привязаны к user_id)
// ------------------------------------------------------------

func getTasks(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

		rows, err := dbx.Query(`
			SELECT id, text, status, created_at
			FROM tasks
			WHERE user_id=$1
			ORDER BY id DESC
		`, uid)

		if err != nil {
			http.Error(w, "db error", 500)
			return
		}

		var tasks []Task
		for rows.Next() {
			var t Task
			rows.Scan(&t.ID, &t.Text, &t.Status, &t.CreatedAt)
			tasks = append(tasks, t)
		}

		json.NewEncoder(w).Encode(tasks)
	}, dbx)
}

func postTask(dbx *sql.DB) http.HandlerFunc {
	return withAuth(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Context().Value("user_id").(int)

		var body struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		var id int
		var created time.Time
		err := dbx.QueryRow(`
			INSERT INTO tasks (text, user_id)
			VALUES ($1, $2)
			RETURNING id, created_at, status
		`, body.Text, uid).Scan(&id, &created, new(string))

		if err != nil {
			http.Error(w, "db error", 500)
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"id":         id,
			"text":       body.Text,
			"created_at": created,
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

	aiClient := ai.NewOpenAIClient()
	taskAIHandler := taskshandler.New(aiClient)

	mux := http.NewServeMux()

	// AUTH
	mux.Handle("/auth/register", registerHandler(database))
	mux.Handle("/auth/login", loginHandler(database))
	mux.Handle("/auth/me", meHandler(database))

	// GOALS
	mux.Handle("/goal", getGoal(database))
	mux.Handle("/goal/create", postGoal(database))

	// TASKS
	mux.Handle("/tasks", getTasks(database))
	mux.Handle("/task/create", postTask(database))

	// AI
	mux.HandleFunc("/task/evaluate", taskAIHandler.Evaluate)

	// CORS
	handler := cors.AllowAll().Handler(mux)

	log.Println("🚀 SERVER RUNNING ON :8080")
	http.ListenAndServe(":8080", handler)
}
