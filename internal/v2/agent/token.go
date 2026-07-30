package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

type runTokenClaims struct {
	RunID       string `json:"run_id"`
	WorkspaceID int    `json:"workspace_id"`
	UserID      int    `json:"user_id"`
	ExpiresAt   int64  `json:"expires_at"`
}

func signRunToken(secret string, run Run, ttl time.Duration) (string, error) {
	claims := runTokenClaims{
		RunID: run.PublicID, WorkspaceID: run.WorkspaceID, UserID: run.UserID,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func verifyRunToken(secret string, token string, expectedRunID string) (runTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return runTokenClaims{}, errors.New("invalid_agent_run_token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, actual) {
		return runTokenClaims{}, errors.New("invalid_agent_run_token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return runTokenClaims{}, errors.New("invalid_agent_run_token")
	}
	var claims runTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return runTokenClaims{}, errors.New("invalid_agent_run_token")
	}
	if claims.RunID == "" || claims.RunID != expectedRunID || claims.WorkspaceID <= 0 || claims.UserID <= 0 {
		return runTokenClaims{}, errors.New("invalid_agent_run_token")
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return runTokenClaims{}, errors.New("expired_agent_run_token")
	}
	return claims, nil
}

func encryptState(secret string, runID string, plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	key := sha256.Sum256([]byte("reup-agent-state:" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), []byte(runID))
	return base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func decryptState(secret string, runID string, encoded string) (string, error) {
	if strings.TrimSpace(encoded) == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid_agent_state")
	}
	key := sha256.Sum256([]byte("reup-agent-state:" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid_agent_state")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(runID))
	if err != nil {
		return "", errors.New("invalid_agent_state")
	}
	return string(plain), nil
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func intValue(input map[string]any, key string) int {
	switch value := input[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		result, _ := strconv.Atoi(value.String())
		return result
	case string:
		result, _ := strconv.Atoi(value)
		return result
	default:
		return 0
	}
}
