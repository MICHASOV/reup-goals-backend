package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const (
	codeTypeVerifyEmail   = "verify_email"
	codeTypeResetPassword = "reset_password"

	codeTTL         = 15 * time.Minute
	resetTokenTTL   = 15 * time.Minute
	resendCooldown  = 60 * time.Second
	maxCodeAttempts = 5
)

var (
	errInvalidCode       = errors.New("invalid_code")
	errCodeExpired       = errors.New("code_expired")
	errTooManyAttempts   = errors.New("too_many_attempts")
	errResendTooSoon     = errors.New("resend_too_soon")
	errEmailSendFailed   = errors.New("email_send_failed")
	errInvalidEmail      = errors.New("invalid_email")
	errWeakPassword      = errors.New("weak_password")
	errInvalidResetToken = errors.New("invalid_reset_token")
)

func VerifyEmailHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, "invalid_json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			writeAPIError(w, errInvalidEmail.Error(), http.StatusBadRequest)
			return
		}

		codeID, userID, err := verifyCode(dbx, email, codeTypeVerifyEmail, body.Code)
		if err != nil {
			writeCodeError(w, err)
			return
		}

		tx, err := dbx.Begin()
		if err != nil {
			writeAPIError(w, "db_begin_failed", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		if _, err := tx.Exec(
			`UPDATE auth_email_codes SET used_at=NOW(), updated_at=NOW() WHERE id=$1`,
			codeID,
		); err != nil {
			writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
			return
		}

		if userID != 0 {
			if _, err := tx.Exec(
				`UPDATE users SET email_verified=TRUE WHERE id=$1`,
				userID,
			); err != nil {
				writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
				return
			}
		} else {
			if _, err := tx.Exec(
				`UPDATE users SET email_verified=TRUE WHERE lower(email)=lower($1)`,
				email,
			); err != nil {
				writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(); err != nil {
			writeAPIError(w, "db_commit_failed", http.StatusInternalServerError)
			return
		}

		writeOK(w, map[string]any{"ok": true})
	}
}

func ResendCodeHandler(dbx *sql.DB, emailService *EmailService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
			Type  string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, "invalid_json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			writeAPIError(w, errInvalidEmail.Error(), http.StatusBadRequest)
			return
		}

		if body.Type != codeTypeVerifyEmail && body.Type != codeTypeResetPassword {
			writeAPIError(w, "invalid_code_type", http.StatusBadRequest)
			return
		}

		if err := enforceCooldown(dbx, email, body.Type); err != nil {
			writeCodeError(w, err)
			return
		}

		var userID int
		err := dbx.QueryRow(`SELECT id FROM users WHERE lower(email)=lower($1)`, email).Scan(&userID)
		if err != nil {
			if body.Type == codeTypeResetPassword {
				writeOK(w, neutralForgotPasswordResponse())
				return
			}
			writeAPIError(w, errInvalidEmail.Error(), http.StatusBadRequest)
			return
		}

		if err := createAndSendCode(dbx, emailService, email, userID, body.Type); err != nil {
			writeCodeError(w, err)
			return
		}

		writeOK(w, map[string]any{"ok": true})
	}
}

func ForgotPasswordHandler(dbx *sql.DB, emailService *EmailService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeOK(w, neutralForgotPasswordResponse())
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			writeOK(w, neutralForgotPasswordResponse())
			return
		}

		var userID int
		err := dbx.QueryRow(`SELECT id FROM users WHERE lower(email)=lower($1)`, email).Scan(&userID)
		if err != nil {
			writeOK(w, neutralForgotPasswordResponse())
			return
		}

		if err := enforceCooldown(dbx, email, codeTypeResetPassword); err != nil {
			writeOK(w, neutralForgotPasswordResponse())
			return
		}

		if err := createAndSendCode(dbx, emailService, email, userID, codeTypeResetPassword); err != nil {
			writeOK(w, neutralForgotPasswordResponse())
			return
		}

		writeOK(w, neutralForgotPasswordResponse())
	}
}

func VerifyResetCodeHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, "invalid_json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			writeAPIError(w, errInvalidEmail.Error(), http.StatusBadRequest)
			return
		}

		codeID, _, err := verifyCode(dbx, email, codeTypeResetPassword, body.Code)
		if err != nil {
			writeCodeError(w, err)
			return
		}

		resetToken, err := randomToken()
		if err != nil {
			writeAPIError(w, "token_generation_failed", http.StatusInternalServerError)
			return
		}

		resetTokenHash, err := hashPassword(resetToken)
		if err != nil {
			writeAPIError(w, "token_hash_failed", http.StatusInternalServerError)
			return
		}

		if _, err := dbx.Exec(
			`UPDATE auth_email_codes
			 SET reset_token_hash=$1, reset_token_expires_at=NOW() + $2::interval, updated_at=NOW()
			 WHERE id=$3`,
			resetTokenHash,
			fmt.Sprintf("%d seconds", int(resetTokenTTL.Seconds())),
			codeID,
		); err != nil {
			writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
			return
		}

		writeOK(w, map[string]any{
			"ok":          true,
			"reset_token": resetToken,
		})
	}
}

func ResetPasswordHandler(dbx *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email       string `json:"email"`
			ResetToken  string `json:"reset_token"`
			NewPassword string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, "invalid_json", http.StatusBadRequest)
			return
		}

		email, ok := normalizeAndValidateEmail(body.Email)
		if !ok {
			writeAPIError(w, errInvalidEmail.Error(), http.StatusBadRequest)
			return
		}

		newPassword := strings.TrimSpace(body.NewPassword)
		if len(newPassword) < 6 {
			writeAPIError(w, errWeakPassword.Error(), http.StatusBadRequest)
			return
		}

		var codeID int
		var resetTokenHash string
		err := dbx.QueryRow(`
			SELECT id, reset_token_hash
			FROM auth_email_codes
			WHERE lower(email)=lower($1)
				AND code_type=$2
				AND used_at IS NULL
				AND reset_token_hash IS NOT NULL
				AND reset_token_used_at IS NULL
				AND reset_token_expires_at > NOW()
			ORDER BY updated_at DESC
			LIMIT 1
		`, email, codeTypeResetPassword).Scan(&codeID, &resetTokenHash)
		if err != nil || !passwordMatches(resetTokenHash, body.ResetToken) {
			writeAPIError(w, errInvalidResetToken.Error(), http.StatusBadRequest)
			return
		}

		passwordHash, err := hashPassword(newPassword)
		if err != nil {
			writeAPIError(w, "password_hash_failed", http.StatusInternalServerError)
			return
		}

		tx, err := dbx.Begin()
		if err != nil {
			writeAPIError(w, "db_begin_failed", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		result, err := tx.Exec(`UPDATE users SET password=$1 WHERE lower(email)=lower($2)`, passwordHash, email)
		if err != nil {
			writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
			return
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeAPIError(w, errInvalidResetToken.Error(), http.StatusBadRequest)
			return
		}

		if _, err := tx.Exec(
			`UPDATE auth_email_codes
			 SET used_at=NOW(), reset_token_used_at=NOW(), updated_at=NOW()
			 WHERE id=$1`,
			codeID,
		); err != nil {
			writeAPIError(w, "db_update_failed", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			writeAPIError(w, "db_commit_failed", http.StatusInternalServerError)
			return
		}

		writeOK(w, map[string]any{"ok": true})
	}
}

func createAndSendCode(dbx *sql.DB, emailService *EmailService, email string, userID int, codeType string) error {
	code, err := generateCode()
	if err != nil {
		return errEmailSendFailed
	}

	codeHash, err := hashPassword(code)
	if err != nil {
		return errEmailSendFailed
	}

	tx, err := dbx.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE auth_email_codes
		 SET used_at=NOW(), updated_at=NOW()
		 WHERE lower(email)=lower($1) AND code_type=$2 AND used_at IS NULL`,
		email,
		codeType,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO auth_email_codes (email, user_id, code_hash, code_type, expires_at, last_sent_at)
		 VALUES ($1, $2, $3, $4, NOW() + $5::interval, NOW())`,
		email,
		nullableUserID(userID),
		codeHash,
		codeType,
		fmt.Sprintf("%d seconds", int(codeTTL.Seconds())),
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if codeType == codeTypeVerifyEmail {
		err = emailService.SendVerificationCode(email, code)
	} else {
		err = emailService.SendPasswordResetCode(email, code)
	}
	if err != nil {
		if errors.Is(err, errEmailListUnavailable) {
			return errEmailListUnavailable
		}
		return errEmailSendFailed
	}

	return nil
}

func verifyCode(dbx *sql.DB, email, codeType, code string) (int, int, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, 0, errInvalidCode
	}

	var codeID int
	var userID sql.NullInt64
	var codeHash string
	var expiresAt time.Time
	var attempts int

	err := dbx.QueryRow(`
		SELECT id, user_id, code_hash, expires_at, attempts
		FROM auth_email_codes
		WHERE lower(email)=lower($1) AND code_type=$2 AND used_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, email, codeType).Scan(&codeID, &userID, &codeHash, &expiresAt, &attempts)
	if err != nil {
		return 0, 0, errInvalidCode
	}

	if attempts >= maxCodeAttempts {
		return 0, 0, errTooManyAttempts
	}

	if time.Now().After(expiresAt) {
		return 0, 0, errCodeExpired
	}

	if !passwordMatches(codeHash, code) {
		_, _ = dbx.Exec(
			`UPDATE auth_email_codes SET attempts=attempts + 1, updated_at=NOW() WHERE id=$1`,
			codeID,
		)
		return 0, 0, errInvalidCode
	}

	if userID.Valid {
		return codeID, int(userID.Int64), nil
	}

	return codeID, 0, nil
}

func enforceCooldown(dbx *sql.DB, email, codeType string) error {
	var lastSentAt time.Time
	err := dbx.QueryRow(`
		SELECT last_sent_at
		FROM auth_email_codes
		WHERE lower(email)=lower($1) AND code_type=$2
		ORDER BY last_sent_at DESC
		LIMIT 1
	`, email, codeType).Scan(&lastSentAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if time.Since(lastSentAt) < resendCooldown {
		return errResendTooSoon
	}
	return nil
}

func generateCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%06d", value.Int64()), nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func normalizeAndValidateEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", false
	}
	address, err := mail.ParseAddress(email)
	if err != nil {
		return "", false
	}
	if address.Address != email {
		return "", false
	}
	return email, true
}

func normalizeSecret(value string) string {
	return strings.TrimSpace(value)
}

func nullableUserID(userID int) any {
	if userID == 0 {
		return nil
	}
	return userID
}

func neutralForgotPasswordResponse() map[string]any {
	return map[string]any{
		"ok":      true,
		"message": "Если аккаунт существует, мы отправили код на email",
	}
}

func writeCodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidCode):
		writeAPIError(w, errInvalidCode.Error(), http.StatusBadRequest)
	case errors.Is(err, errCodeExpired):
		writeAPIError(w, errCodeExpired.Error(), http.StatusBadRequest)
	case errors.Is(err, errTooManyAttempts):
		writeAPIError(w, errTooManyAttempts.Error(), http.StatusTooManyRequests)
	case errors.Is(err, errResendTooSoon):
		writeAPIError(w, errResendTooSoon.Error(), http.StatusTooManyRequests)
	case errors.Is(err, errEmailSendFailed):
		writeAPIError(w, errEmailSendFailed.Error(), http.StatusBadGateway)
	case errors.Is(err, errEmailListUnavailable):
		writeAPIError(w, errEmailListUnavailable.Error(), http.StatusBadGateway)
	default:
		writeAPIError(w, "server_error", http.StatusInternalServerError)
	}
}

func writeAPIError(w http.ResponseWriter, code string, status int) {
	http.Error(w, code, status)
}

func writeOK(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}
