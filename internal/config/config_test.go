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
	if parsed.Query().Get("connect_timeout") != "5" {
		t.Fatalf("database liveness options are missing: %s", parsed.RawQuery)
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

func TestLoadSecureCookieOverride(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "true")
	config := Load()
	if !config.SecureCookies {
		t.Fatal("expected COOKIE_SECURE to enable secure session cookies")
	}
}

func TestValidateAllowsProductionAIGatewayWithoutOpenAIKey(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBPassword: "secret", DBName: "reup", DBSSLMode: "disable",
		JWTSecret: strings.Repeat("x", 32), Environment: "production", CORSAllowedOrigins: []string{"https://reupgoals.pro"},
		PrivacyMode: "ru_152fz", DataResidencyRegion: "ru-msk", PrivacyContactEmail: "privacy@example.com",
		CrossBorderTransferRegistered: true, OpenAIBaseURL: "https://ai.reupgoals.pro/openai/v1",
		OpenAIGatewaySecret: strings.Repeat("g", 32),
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("expected gateway config without OpenAI key to be valid, got %v", err)
	}
	if config.OpenAIAuthToken() != config.OpenAIGatewaySecret {
		t.Fatal("expected the gateway secret to be used as the provider credential")
	}
}

func TestValidateRejectsOpenAIKeyOnProductionGateway(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBPassword: "secret", DBName: "reup", DBSSLMode: "disable",
		JWTSecret: strings.Repeat("x", 32), Environment: "production", CORSAllowedOrigins: []string{"https://reupgoals.pro"},
		PrivacyMode: "ru_152fz", DataResidencyRegion: "ru-msk", PrivacyContactEmail: "privacy@example.com",
		CrossBorderTransferRegistered: true, OpenAIBaseURL: "https://ai.reupgoals.pro/openai/v1",
		OpenAIGatewaySecret: strings.Repeat("g", 32), OpenAIKey: "must-not-remain-in-russia",
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "must not be stored") {
		t.Fatalf("expected production OpenAI key rejection, got %v", err)
	}
}

func TestValidateRejectsInsecureRemoteAIGateway(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBPassword: "secret", DBName: "reup", DBSSLMode: "disable",
		JWTSecret: strings.Repeat("x", 32), Environment: "staging", CORSAllowedOrigins: []string{"https://staging.reupgoals.pro"},
		PrivacyMode: "test", DataResidencyRegion: "eu-de", OpenAIBaseURL: "http://ai.example.com/openai/v1",
		OpenAIGatewaySecret: strings.Repeat("g", 32),
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("expected gateway HTTPS error, got %v", err)
	}
}

func TestValidateRejectsBillingEnforcementWithoutActivationPath(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBName: "reup", OpenAIKey: "key",
		DBSSLMode: "disable", JWTSecret: strings.Repeat("x", 32), PrivacyMode: "test",
		BillingEnforcementEnabled: true,
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "manual confirmation") {
		t.Fatalf("expected billing activation path error, got %v", err)
	}
}

func TestValidateAllowsManualBillingConfirmation(t *testing.T) {
	config := &Config{
		DBHost: "127.0.0.1", DBUser: "user", DBName: "reup", OpenAIKey: "key",
		DBSSLMode: "disable", JWTSecret: strings.Repeat("x", 32), PrivacyMode: "test",
		BillingEnforcementEnabled: true, BillingAdminKey: strings.Repeat("b", 32),
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("expected manual billing activation path to be valid, got %v", err)
	}
}
