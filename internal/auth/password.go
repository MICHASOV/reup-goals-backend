package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrCurrentPasswordInvalid = errors.New("current_password_invalid")

func ChangePassword(ctx context.Context, dbx *sql.DB, userID int, currentPassword, newPassword string) error {
	currentPassword = normalizeSecret(currentPassword)
	newPassword = normalizeSecret(newPassword)
	if len(newPassword) < 12 || len(newPassword) > 1024 {
		return errWeakPassword
	}

	var storedPassword string
	if err := dbx.QueryRowContext(ctx, `SELECT password FROM users WHERE id=$1`, userID).Scan(&storedPassword); err != nil {
		return err
	}
	if !passwordMatches(storedPassword, currentPassword) {
		return ErrCurrentPasswordInvalid
	}

	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = dbx.ExecContext(ctx, `UPDATE users SET password=$1, auth_version=auth_version+1 WHERE id=$2`, passwordHash, userID)
	return err
}

func hashPassword(password string) (string, error) {
	const (
		iterations = 600000
		keyLength  = 32
	)

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLength)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"pbkdf2_sha256$%d$%s$%s",
		iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func passwordNeedsRehash(stored string) bool {
	if !isPasswordHash(stored) {
		return true
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 4 {
		return true
	}
	iterations, err := strconv.Atoi(parts[1])
	return err != nil || iterations < 600000
}

func isPasswordHash(stored string) bool {
	return strings.HasPrefix(stored, "pbkdf2_sha256$")
}

func passwordMatches(storedPassword, candidatePassword string) bool {
	if isPasswordHash(storedPassword) {
		parts := strings.Split(storedPassword, "$")
		if len(parts) != 4 {
			return false
		}

		iterations, err := strconv.Atoi(parts[1])
		if err != nil {
			return false
		}

		salt, err := base64.RawStdEncoding.DecodeString(parts[2])
		if err != nil {
			return false
		}

		expectedKey, err := base64.RawStdEncoding.DecodeString(parts[3])
		if err != nil {
			return false
		}

		actualKey, err := pbkdf2.Key(
			sha256.New,
			candidatePassword,
			salt,
			iterations,
			len(expectedKey),
		)
		if err != nil {
			return false
		}

		return subtle.ConstantTimeCompare(actualKey, expectedKey) == 1
	}

	return storedPassword == candidatePassword
}
