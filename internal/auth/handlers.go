package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

func RegisterHandler(dbx *sql.DB, secret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		email := strings.ToLower(strings.TrimSpace(body.Email))
		password := strings.TrimSpace(body.Password)

		if email == "" || password == "" {
			http.Error(w, "email & password required", http.StatusBadRequest)
			return
		}

		passwordHash, err := hashPassword(password)
		if err != nil {
			http.Error(w, "password hashing failed", http.StatusInternalServerError)
			return
		}

		var id int
		err = dbx.QueryRow(`
			INSERT INTO users (email, password)
			VALUES ($1, $2)
			RETURNING id
		`, email, passwordHash).Scan(&id)

		if err != nil {
			http.Error(w, "user exists", http.StatusBadRequest)
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

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		email := strings.ToLower(strings.TrimSpace(body.Email))
		password := strings.TrimSpace(body.Password)

		if email == "" || password == "" {
			http.Error(w, "email & password required", http.StatusBadRequest)
			return
		}

		var id int
		var storedPassword string
		err := dbx.QueryRow(`
			SELECT id, password FROM users WHERE email=$1
		`, email).Scan(&id, &storedPassword)

		if err != nil || !passwordMatches(storedPassword, password) {
			http.Error(w, "invalid login", http.StatusUnauthorized)
			return
		}

		if !isPasswordHash(storedPassword) {
			passwordHash, err := hashPassword(password)
			if err != nil {
				http.Error(w, "password migration failed", http.StatusInternalServerError)
				return
			}

			if _, err := dbx.Exec(
				`UPDATE users SET password=$1 WHERE id=$2`,
				passwordHash,
				id,
			); err != nil {
				http.Error(w, "password migration failed", http.StatusInternalServerError)
				return
			}
		}

		token, err := GenerateToken(secret, id)
		if err != nil {
			http.Error(w, "token generation failed", http.StatusInternalServerError)
			return
		}

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
		err := dbx.QueryRow(`SELECT email FROM users WHERE id=$1`, uid).Scan(&email)
		if err != nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": uid,
			"email":   email,
		})
	}
}
