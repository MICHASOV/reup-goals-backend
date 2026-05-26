package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

func hashPassword(password string) (string, error) {
	const (
		iterations = 210000
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
