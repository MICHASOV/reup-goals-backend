package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsUnsafeSecret(t *testing.T) {
	config := &Config{DBHost: "db", DBUser: "user", DBName: "reup", OpenAIKey: "key", JWTSecret: "short"}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "32") {
		t.Fatalf("expected JWT length error, got %v", err)
	}
}

func TestValidateRequiresCORSInStaging(t *testing.T) {
	config := &Config{DBHost: "db", DBUser: "user", DBName: "reup", OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32), Environment: "staging"}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Fatalf("expected CORS error, got %v", err)
	}
}
