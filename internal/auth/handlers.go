package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func RegisterHandler(dbx *sql.DB, secret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
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

		token, _ := GenerateToken(secret, id)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": id,
			"token":   token,
		})
	}
}

func LoginHandler(dbx *sql.DB, secret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		var id int
		err := dbx.QueryRow(`
			SELECT id FROM users WHERE email=$1 AND password=$2
		`, body.Email, body.Password).Scan(&id)
		if err != nil {
			http.Error(w, "invalid login", http.StatusUnauthorized)
			return
		}

		token, _ := GenerateToken(secret, id)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": id,
			"token":   token,
		})
	}
}

func MeHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var email string
		_ = dbx.QueryRow("SELECT email FROM users WHERE id=$1", uid).Scan(&email)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": uid,
			"email":   email,
		})
	}
}
