package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/rs/cors"

	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
)

// ----------------------
//   DTO / MODELS
// ----------------------

type Goal struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

type Task struct {
	ID        int       `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ----------------------
//        MAIN
// ----------------------

func main() {
	cfg := config.Load()

	database, err := db.Connect(cfg.ConnString())
	if err != nil {
		log.Fatal("❌ Failed to connect DB:", err)
	}
	defer database.Close()

	log.Println("✅ Connected to PostgreSQL!")

	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// ----- GOALS API -----
	mux.HandleFunc("/goal", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getGoal(database, w, r)
		case http.MethodPost:
			postGoal(database, w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// ----- TASKS API (MVP-версия, пока без AI) -----
	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getTasks(database, w, r)
		case http.MethodPost:
			postTask(database, w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	handler := c.Handler(mux)

	log.Println("🚀 API server is running on :8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}

// ----------------------
//     GOAL HANDLERS
// ----------------------

func getGoal(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	row := database.QueryRowContext(
		r.Context(),
		`SELECT id, title, description, is_active, created_at 
         FROM goals 
         ORDER BY id DESC 
         LIMIT 1`,
	)

	var g Goal
	err := row.Scan(&g.ID, &g.Title, &g.Description, &g.IsActive, &g.CreatedAt)
	if err != nil {
		http.Error(w, "no goal found", http.StatusNotFound)
		return
	}

	if err := json.NewEncoder(w).Encode(g); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}

func postGoal(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if body.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	row := database.QueryRowContext(
		r.Context(),
		`INSERT INTO goals (title, description, is_active) 
         VALUES ($1, $2, TRUE) 
         RETURNING id, created_at`,
		body.Title,
		body.Description,
	)

	var g Goal
	g.Title = body.Title
	g.Description = body.Description
	g.IsActive = true

	if err := row.Scan(&g.ID, &g.CreatedAt); err != nil {
		http.Error(w, "db insert error", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(g); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}

// ----------------------
//     TASK HANDLERS
// ----------------------

func getTasks(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rows, err := database.QueryContext(
		r.Context(),
		`SELECT id, text, status, created_at 
         FROM tasks 
         ORDER BY id DESC`,
	)
	if err != nil {
		http.Error(w, "db query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tasks []Task

	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Text, &t.Status, &t.CreatedAt); err != nil {
			http.Error(w, "db scan error", http.StatusInternalServerError)
			return
		}
		tasks = append(tasks, t)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "db rows error", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(tasks); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}

func postTask(database *sql.DB, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		Text string `json:"text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if body.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	row := database.QueryRowContext(
		r.Context(),
		`INSERT INTO tasks (text) 
         VALUES ($1) 
         RETURNING id, status, created_at`,
		body.Text,
	)

	var t Task
	t.Text = body.Text

	if err := row.Scan(&t.ID, &t.Status, &t.CreatedAt); err != nil {
		http.Error(w, "db insert error", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(t); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}
