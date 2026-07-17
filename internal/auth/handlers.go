package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"reup-goals-backend/internal/legal"
	"reup-goals-backend/internal/v2/workspaces"
)

func RegisterHandler(dbx *sql.DB, secret []byte, emailService *EmailService, secureCookie bool, browserAuthOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Email       string                  `json:"email"`
			Password    string                  `json:"password"`
			Acceptances []legal.AcceptanceInput `json:"acceptances"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			http.Error(w, "invalid_email", http.StatusBadRequest)
			return
		}
		password := normalizeSecret(body.Password)
		if password == "" {
			http.Error(w, "email & password required", http.StatusBadRequest)
			return
		}
		if len(password) < 12 || len(password) > 1024 {
			http.Error(w, "weak_password", http.StatusBadRequest)
			return
		}
		acceptances, err := legal.ValidateRegistrationAcceptances(body.Acceptances)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		passwordHash, err := hashPassword(password)
		if err != nil {
			http.Error(w, "password hashing failed", http.StatusInternalServerError)
			return
		}

		subjectKey, err := legal.NewSubjectKey()
		if err != nil {
			http.Error(w, "registration_failed", http.StatusInternalServerError)
			return
		}
		tx, err := dbx.BeginTx(r.Context(), nil)
		if err != nil {
			http.Error(w, "registration_failed", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		var id int
		err = tx.QueryRowContext(r.Context(), `
			INSERT INTO users (email, password, privacy_subject_id)
			VALUES ($1, $2, $3)
			RETURNING id
		`, email, passwordHash, subjectKey).Scan(&id)

		if err != nil {
			http.Error(w, "user_already_exists", http.StatusBadRequest)
			return
		}
		if err := legal.StoreAcceptances(r.Context(), tx, id, subjectKey, acceptances, r.Header.Get("X-Request-ID")); err != nil {
			http.Error(w, "legal_acceptance_store_failed", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, "registration_failed", http.StatusInternalServerError)
			return
		}

		workspaceStore := workspaces.NewStore(dbx)
		if _, _, err := workspaceStore.GetOrCreateDefault(r.Context(), id); err != nil {
			cleanupFailedRegistration(dbx, id, subjectKey)
			http.Error(w, "workspace_create_failed", http.StatusInternalServerError)
			return
		}

		if err := createAndSendCode(dbx, emailService, email, id, codeTypeVerifyEmail); err != nil {
			cleanupFailedRegistration(dbx, id, subjectKey)
			writeCodeError(w, err)
			return
		}

		token, err := GenerateToken(secret, id, 1)
		if err != nil {
			http.Error(w, "token generation failed", http.StatusInternalServerError)
			return
		}
		SetSessionCookie(w, token, secureCookie)

		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"user_id": id}
		if !browserAuthOnly {
			response["token"] = token
		}
		_ = json.NewEncoder(w).Encode(response)
	}
}

func cleanupFailedRegistration(dbx *sql.DB, userID int, subjectKey string) {
	tx, err := dbx.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM legal_acceptances WHERE subject_key=$1`, subjectKey); err != nil {
		return
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=$1`, userID); err != nil {
		return
	}
	_ = tx.Commit()
}

func LoginHandler(dbx *sql.DB, secret []byte, secureCookie bool, browserAuthOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			http.Error(w, "invalid_email", http.StatusBadRequest)
			return
		}
		password := normalizeSecret(body.Password)

		if email == "" || password == "" {
			http.Error(w, "email & password required", http.StatusBadRequest)
			return
		}
		if len(password) > 1024 {
			http.Error(w, "invalid login", http.StatusUnauthorized)
			return
		}

		var id int
		var storedPassword string
		var authVersion int
		err := dbx.QueryRow(`
			SELECT id, password, auth_version FROM users WHERE email=$1
		`, email).Scan(&id, &storedPassword, &authVersion)

		if err != nil || !passwordMatches(storedPassword, password) {
			http.Error(w, "invalid login", http.StatusUnauthorized)
			return
		}

		if passwordNeedsRehash(storedPassword) {
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

		token, err := GenerateToken(secret, id, authVersion)
		if err != nil {
			http.Error(w, "token generation failed", http.StatusInternalServerError)
			return
		}
		SetSessionCookie(w, token, secureCookie)

		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"user_id": id}
		if !browserAuthOnly {
			response["token"] = token
		}
		_ = json.NewEncoder(w).Encode(response)
	}
}

func MeHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
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
