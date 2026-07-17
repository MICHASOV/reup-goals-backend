package config

import (
	"net/url"
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

func TestConnStringEscapesCredentials(t *testing.T) {
	config := &Config{
		DBHost: "db.example.com", DBPort: 5432, DBUser: "reup user",
		DBPassword: "p@ss:word/with spaces", DBName: "reup goals", DBSSLMode: "verify-full",
	}
	parsed, err := url.Parse(config.ConnString())
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != config.DBUser || password != config.DBPassword {
		t.Fatalf("credentials were not preserved: %s", config.ConnString())
	}
	if parsed.Query().Get("sslmode") != "verify-full" {
		t.Fatalf("expected verify-full, got %q", parsed.Query().Get("sslmode"))
	}
}

func TestValidateRejectsUnencryptedRemoteDatabase(t *testing.T) {
	config := &Config{
		DBHost: "db.example.com", DBUser: "user", DBPassword: "secret", DBName: "reup",
		DBSSLMode: "disable", OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32),
		Environment: "production", CORSAllowedOrigins: []string{"https://reupgoals.pro"},
		PrivacyMode: "gdpr", DataResidencyRegion: "eu-de", PrivacyContactEmail: "privacy@example.com", GDPRTransferMechanism: "scc",
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "DB_SSLMODE") {
		t.Fatalf("expected remote database TLS error, got %v", err)
	}
}

func TestValidateAllowsLocalStagingDatabaseWithoutTLS(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBPassword: "secret", DBName: "reup",
		DBSSLMode: "disable", OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32),
		Environment: "staging", CORSAllowedOrigins: []string{"https://staging.reupgoals.pro"},
		PrivacyMode: "test", DataResidencyRegion: "eu-de",
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("expected valid local staging database config, got %v", err)
	}
}

func TestValidateRequiresCORSInStaging(t *testing.T) {
	config := &Config{DBHost: "db", DBUser: "user", DBName: "reup", OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32), Environment: "staging", PrivacyMode: "test", DataResidencyRegion: "eu-de"}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Fatalf("expected CORS error, got %v", err)
	}
}

func TestValidateRequiresRussianPrimaryRegion(t *testing.T) {
	config := &Config{
		DBHost: "db.example.com", DBUser: "user", DBPassword: "secret", DBName: "reup", DBSSLMode: "require",
		OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32), Environment: "production",
		CORSAllowedOrigins: []string{"https://reupgoals.pro"}, PrivacyMode: "ru_152fz",
		DataResidencyRegion: "eu-de", PrivacyContactEmail: "privacy@example.com", CrossBorderTransferRegistered: true,
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "ru-*") {
		t.Fatalf("expected Russian region error, got %v", err)
	}
}

func TestValidateProductionPrivacyControls(t *testing.T) {
	config := &Config{
		DBHost: "db.example.com", DBUser: "user", DBPassword: "secret", DBName: "reup", DBSSLMode: "require",
		OpenAIKey: "key", JWTSecret: strings.Repeat("x", 32), Environment: "production",
		CORSAllowedOrigins: []string{"https://reupgoals.pro"}, PrivacyMode: "dual",
		DataResidencyRegion: "ru-msk", PrivacyContactEmail: "privacy@example.com",
		CrossBorderTransferRegistered: true, GDPRTransferMechanism: "scc",
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("expected valid dual-region config, got %v", err)
	}
}
