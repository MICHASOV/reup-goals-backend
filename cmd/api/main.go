package main

import (
	"encoding/json"
	"log"
	"net/http"

	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
)

type Goal struct {
	ID        int    `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

func enableCors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func main() {
	cfg := config.Load()

	database, err := db.Connect(cfg.ConnString())
	if err != nil {
		log.Fatal("❌ Failed to connect DB:", err)
	}
	defer database.Close()

	log.Println("✅ Connected to PostgreSQL!")

	// Health
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		enableCors(w)
		if r.Method == http.MethodOptions {
			return
		}
		w.Write([]byte("OK"))
	})

	// Goal API
	http.HandleFunc("/goal", func(w http.ResponseWriter, r *http.Request) {
		enableCors(w)
		if r.Method == http.MethodOptions {
			return
		}

		switch r.Method {
		case http.MethodGet:
			getGoal(database, w, r)
		case http.MethodPost:
			postGoal(database, w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	log.Println("🚀 API server is running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getGoal(database *db.DB, w http.ResponseWriter, r *http.Request) {
	row := database.Conn.QueryRow(
		r.Context(),
		`SELECT id, text, created_at FROM goals ORDER BY id DESC LIMIT 1`,
	)

	var g Goal
	err := row.Scan(&g.ID, &g.Text, &g.CreatedAt)
	if err != nil {
		http.Error(w, "no goal found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(g)
}

func postGoal(database *db.DB, w http.ResponseWriter, r *http.Request) {
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

	row := database.Conn.QueryRow(
		r.Context(),
		`INSERT INTO goals(text) VALUES ($1) RETURNING id`,
		body.Text,
	)

	var id int
	if err := row.Scan(&id); err != nil {
		http.Error(w, "db insert error", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"id":   id,
		"text": body.Text,
	})
}
