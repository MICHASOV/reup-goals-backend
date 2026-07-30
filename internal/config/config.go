package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBHost            string
	DBPort            int
	DBUser            string
	DBPassword        string
	DBName            string
	DBSSLMode         string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration

	OpenAIKey                     string
	OpenAIModel                   string
	OpenAIAuditorModel            string
	OpenAIAdvisorModel            string
	OpenAITaskModel               string
	OpenAITranscriptionModel      string
	OpenAIAuditorMaxOutputTokens  int
	OpenAIAuditorCompactThreshold int
	OpenAIAdvisorCompactThreshold int
	OpenAIProxyURL                string
	EnableAIBenchmark             bool
	AIAdminKey                    string
	AIRequestsPerMinute           int
	AIDailyBudgetUSD              float64
	AIMonthlyBudgetUSD            float64
	AIJobWorkers                  int
	AgentRuntimeEnabled           bool
	AgentRuntimeURL               string
	AgentRuntimeSecret            string
	AgentRuntimeMaxTurns          int

	JWTSecret                     string
	CORSAllowedOrigins            []string
	Environment                   string
	HTTPReadTimeout               time.Duration
	HTTPWriteTimeout              time.Duration
	HTTPIdleTimeout               time.Duration
	BrowserAuthOnly               bool
	SecureCookies                 bool
	PrivacyMode                   string
	DataResidencyRegion           string
	CrossBorderTransferRegistered bool
	GDPRTransferMechanism         string
	PrivacyContactEmail           string
	RetentionInterval             time.Duration
	AuthCodeRetention             time.Duration
	HTTPRequestLogRetention       time.Duration
	ProductEventRetention         time.Duration
	AICallLogRetention            time.Duration
	BackgroundJobRetention        time.Duration
	LegalEvidenceRetention        time.Duration
	PrivacyRequestRetention       time.Duration

	UnisenderAPIKey           string
	UnisenderBaseURL          string
	UnisenderSenderEmail      string
	UnisenderSenderName       string
	UnisenderListID           string
	UnisenderServiceListTitle string

	CloudPaymentsPublicID           string
	CloudPaymentsAPISecret          string
	CloudPaymentsBaseURL            string
	CloudPaymentsPlanName           string
	CloudPaymentsAmount             float64
	CloudPaymentsFirstPaymentAmount float64
	CloudPaymentsCurrency           string
	CloudPaymentsTrialDays          int

	TopPaymentsCheckoutURL    string
	BillingPaymentsEnabled    bool
	BillingEnforcementEnabled bool
	BillingAdminKey           string
	FrontendBaseURL           string
	AppVersion                string
	SupportEmail              string
	DocumentationURL          string
	ChangelogURL              string
}

func Load() *Config {

	// Парсим DB_PORT
	portStr := os.Getenv("DB_PORT")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 5432 // fallback
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini" // дефолтная модель (можешь заменить на нужную)
	}
	auditorModel := os.Getenv("OPENAI_AUDITOR_MODEL")
	if auditorModel == "" {
		auditorModel = model
	}
	advisorModel := os.Getenv("OPENAI_ADVISOR_MODEL")
	if advisorModel == "" {
		advisorModel = "gpt-5.4-mini"
	}
	taskModel := os.Getenv("OPENAI_TASK_MODEL")
	if taskModel == "" {
		taskModel = "gpt-4o-mini"
	}
	transcriptionModel := os.Getenv("OPENAI_TRANSCRIPTION_MODEL")
	if transcriptionModel == "" {
		transcriptionModel = "gpt-4o-transcribe"
	}
	auditorMaxOutputTokens := parseIntEnv("OPENAI_AUDITOR_MAX_OUTPUT_TOKENS", 1800)
	auditorCompactThreshold := parseIntEnv("OPENAI_AUDITOR_COMPACT_THRESHOLD", 24000)
	advisorCompactThreshold := parseIntEnv("OPENAI_ADVISOR_COMPACT_THRESHOLD", 24000)
	openAIProxyURL := os.Getenv("OPENAI_PROXY_URL")
	if openAIProxyURL == "" {
		openAIProxyURL = "socks5://127.0.0.1:10808"
	}
	agentRuntimeURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_RUNTIME_URL")), "/")
	if agentRuntimeURL == "" {
		agentRuntimeURL = "http://127.0.0.1:8091"
	}

	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if environment == "" {
		environment = "development"
	}
	privacyMode := strings.ToLower(strings.TrimSpace(os.Getenv("PRIVACY_MODE")))
	if privacyMode == "" {
		privacyMode = "development"
	}

	unisenderBaseURL := os.Getenv("UNISENDER_BASE_URL")
	if unisenderBaseURL == "" {
		unisenderBaseURL = "https://api.unisender.com/ru/api"
	}

	unisenderSenderName := os.Getenv("UNISENDER_SENDER_NAME")
	if unisenderSenderName == "" {
		unisenderSenderName = "REUP.goals"
	}

	unisenderServiceListTitle := os.Getenv("UNISENDER_SERVICE_LIST_TITLE")
	if unisenderServiceListTitle == "" {
		unisenderServiceListTitle = "REUP.goals service emails"
	}

	cloudPaymentsBaseURL := os.Getenv("CLOUDPAYMENTS_BASE_URL")
	if cloudPaymentsBaseURL == "" {
		cloudPaymentsBaseURL = "https://api.cloudpayments.ru"
	}

	cloudPaymentsPlanName := os.Getenv("CLOUDPAYMENTS_PLAN_NAME")
	if cloudPaymentsPlanName == "" {
		cloudPaymentsPlanName = "REUP.goals Founder"
	}

	cloudPaymentsAmount := parseFloatEnv("CLOUDPAYMENTS_AMOUNT", 3490)
	cloudPaymentsFirstPaymentAmount := parseFloatEnv("CLOUDPAYMENTS_FIRST_PAYMENT_AMOUNT", 3490)

	cloudPaymentsCurrency := os.Getenv("CLOUDPAYMENTS_CURRENCY")
	if cloudPaymentsCurrency == "" {
		cloudPaymentsCurrency = "RUB"
	}

	cloudPaymentsTrialDays := parseIntEnv("CLOUDPAYMENTS_TRIAL_DAYS", 0)
	frontendBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL")), "/")
	if frontendBaseURL == "" {
		frontendBaseURL = "http://localhost:3000"
	}
	appVersion := strings.TrimSpace(os.Getenv("APP_VERSION"))
	if appVersion == "" {
		appVersion = "REUP.goals v2"
	}
	supportEmail := strings.TrimSpace(os.Getenv("SUPPORT_EMAIL"))
	if supportEmail == "" {
		supportEmail = "support@reupgoals.pro"
	}

	dbSSLMode := strings.ToLower(strings.TrimSpace(os.Getenv("DB_SSLMODE")))
	if dbSSLMode == "" {
		dbSSLMode = "disable"
	}

	return &Config{
		DBHost:            os.Getenv("DB_HOST"),
		DBPort:            port,
		DBUser:            os.Getenv("DB_USER"),
		DBPassword:        os.Getenv("DB_PASSWORD"),
		DBName:            os.Getenv("DB_NAME"),
		DBSSLMode:         dbSSLMode,
		DBMaxOpenConns:    parseIntEnv("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:    parseIntEnv("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime: parseDurationEnv("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBConnMaxIdleTime: parseDurationEnv("DB_CONN_MAX_IDLE_TIME", time.Minute),

		OpenAIKey:                     os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:                   model,
		OpenAIAuditorModel:            auditorModel,
		OpenAIAdvisorModel:            advisorModel,
		OpenAITaskModel:               taskModel,
		OpenAITranscriptionModel:      transcriptionModel,
		OpenAIAuditorMaxOutputTokens:  auditorMaxOutputTokens,
		OpenAIAuditorCompactThreshold: auditorCompactThreshold,
		OpenAIAdvisorCompactThreshold: advisorCompactThreshold,
		OpenAIProxyURL:                openAIProxyURL,
		EnableAIBenchmark:             parseBoolEnv("ENABLE_AI_BENCHMARK"),
		AIAdminKey:                    strings.TrimSpace(os.Getenv("AI_ADMIN_KEY")),
		AIRequestsPerMinute:           parseIntEnv("AI_RATE_LIMIT_PER_MINUTE", 60),
		AIDailyBudgetUSD:              parseFloatEnv("AI_DAILY_BUDGET_USD", 0),
		AIMonthlyBudgetUSD:            parseFloatEnv("AI_MONTHLY_BUDGET_USD", 0),
		AIJobWorkers:                  parseIntEnv("AI_JOB_WORKERS", 2),
		AgentRuntimeEnabled:           parseBoolEnv("AGENT_RUNTIME_ENABLED"),
		AgentRuntimeURL:               agentRuntimeURL,
		AgentRuntimeSecret:            strings.TrimSpace(os.Getenv("AGENT_RUNTIME_SECRET")),
		AgentRuntimeMaxTurns:          parseIntEnv("AGENT_RUNTIME_MAX_TURNS", 12),

		JWTSecret:                     jwtSecret,
		CORSAllowedOrigins:            parseCSVEnv("CORS_ALLOWED_ORIGINS"),
		Environment:                   environment,
		HTTPReadTimeout:               parseDurationEnv("HTTP_READ_TIMEOUT", 90*time.Second),
		HTTPWriteTimeout:              parseDurationEnv("HTTP_WRITE_TIMEOUT", 0),
		HTTPIdleTimeout:               parseDurationEnv("HTTP_IDLE_TIMEOUT", 90*time.Second),
		BrowserAuthOnly:               parseBoolEnv("BROWSER_AUTH_ONLY"),
		SecureCookies:                 parseBoolEnv("COOKIE_SECURE"),
		PrivacyMode:                   privacyMode,
		DataResidencyRegion:           strings.ToLower(strings.TrimSpace(os.Getenv("DATA_RESIDENCY_REGION"))),
		CrossBorderTransferRegistered: parseBoolEnv("CROSS_BORDER_TRANSFER_REGISTERED"),
		GDPRTransferMechanism:         strings.ToLower(strings.TrimSpace(os.Getenv("GDPR_TRANSFER_MECHANISM"))),
		PrivacyContactEmail:           strings.TrimSpace(os.Getenv("PRIVACY_CONTACT_EMAIL")),
		RetentionInterval:             parseDurationEnv("RETENTION_INTERVAL", 24*time.Hour),
		AuthCodeRetention:             daysEnv("AUTH_CODE_RETENTION_DAYS", 30),
		HTTPRequestLogRetention:       daysEnv("HTTP_REQUEST_LOG_RETENTION_DAYS", 90),
		ProductEventRetention:         daysEnv("PRODUCT_EVENT_RETENTION_DAYS", 365),
		AICallLogRetention:            daysEnv("AI_CALL_LOG_RETENTION_DAYS", 180),
		BackgroundJobRetention:        daysEnv("BACKGROUND_JOB_RETENTION_DAYS", 30),
		LegalEvidenceRetention:        daysEnv("LEGAL_EVIDENCE_RETENTION_DAYS", 1095),
		PrivacyRequestRetention:       daysEnv("PRIVACY_REQUEST_RETENTION_DAYS", 1095),

		UnisenderAPIKey:           os.Getenv("UNISENDER_API_KEY"),
		UnisenderBaseURL:          unisenderBaseURL,
		UnisenderSenderEmail:      os.Getenv("UNISENDER_SENDER_EMAIL"),
		UnisenderSenderName:       unisenderSenderName,
		UnisenderListID:           os.Getenv("UNISENDER_LIST_ID"),
		UnisenderServiceListTitle: unisenderServiceListTitle,

		CloudPaymentsPublicID:           os.Getenv("CLOUDPAYMENTS_PUBLIC_ID"),
		CloudPaymentsAPISecret:          os.Getenv("CLOUDPAYMENTS_API_SECRET"),
		CloudPaymentsBaseURL:            cloudPaymentsBaseURL,
		CloudPaymentsPlanName:           cloudPaymentsPlanName,
		CloudPaymentsAmount:             cloudPaymentsAmount,
		CloudPaymentsFirstPaymentAmount: cloudPaymentsFirstPaymentAmount,
		CloudPaymentsCurrency:           cloudPaymentsCurrency,
		CloudPaymentsTrialDays:          cloudPaymentsTrialDays,

		TopPaymentsCheckoutURL:    strings.TrimSpace(os.Getenv("TOPPAYMENTS_CHECKOUT_URL")),
		BillingPaymentsEnabled:    parseBoolEnv("BILLING_PAYMENTS_ENABLED"),
		BillingEnforcementEnabled: parseBoolEnv("BILLING_ENFORCEMENT_ENABLED"),
		BillingAdminKey:           strings.TrimSpace(os.Getenv("BILLING_ADMIN_KEY")),
		FrontendBaseURL:           frontendBaseURL,
		AppVersion:                appVersion,
		SupportEmail:              supportEmail,
		DocumentationURL:          strings.TrimSpace(os.Getenv("DOCUMENTATION_URL")),
		ChangelogURL:              strings.TrimSpace(os.Getenv("CHANGELOG_URL")),
	}
}

func parseCSVEnv(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			result = append(result, item)
		}
	}

	return result
}

func parseIntEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func parseFloatEnv(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}

	return parsed
}

func parseBoolEnv(key string) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func daysEnv(key string, fallback int) time.Duration {
	days := parseIntEnv(key, fallback)
	if days <= 0 {
		days = fallback
	}
	return time.Duration(days) * 24 * time.Hour
}

func (c *Config) Validate() error {
	missing := make([]string, 0)
	for key, value := range map[string]string{
		"DB_HOST": c.DBHost, "DB_USER": c.DBUser, "DB_NAME": c.DBName,
		"JWT_SECRET": c.JWTSecret, "OPENAI_API_KEY": c.OpenAIKey,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must contain at least 32 characters")
	}
	if (c.Environment == "production" || c.Environment == "staging") && len(c.CORSAllowedOrigins) == 0 {
		return fmt.Errorf("CORS_ALLOWED_ORIGINS is required in %s", c.Environment)
	}
	if c.DBSSLMode != "disable" && c.DBSSLMode != "require" && c.DBSSLMode != "verify-ca" && c.DBSSLMode != "verify-full" {
		return fmt.Errorf("DB_SSLMODE must be disable, require, verify-ca, or verify-full")
	}
	if !validPrivacyMode(c.PrivacyMode) {
		return fmt.Errorf("PRIVACY_MODE must be development, test, gdpr, ru_152fz, or dual")
	}
	if c.Environment == "production" || c.Environment == "staging" {
		if strings.TrimSpace(c.DBPassword) == "" {
			return fmt.Errorf("DB_PASSWORD is required in %s", c.Environment)
		}
		if !isLoopbackDatabaseHost(c.DBHost) && c.DBSSLMode == "disable" {
			return fmt.Errorf("DB_SSLMODE must enable TLS for a remote database in %s", c.Environment)
		}
		if c.EnableAIBenchmark {
			return fmt.Errorf("ENABLE_AI_BENCHMARK must be disabled in %s", c.Environment)
		}
		for _, origin := range c.CORSAllowedOrigins {
			if strings.Contains(origin, "*") || !strings.HasPrefix(origin, "https://") {
				return fmt.Errorf("CORS origin %q must be an explicit HTTPS origin in %s", origin, c.Environment)
			}
		}
		if strings.TrimSpace(c.DataResidencyRegion) == "" {
			return fmt.Errorf("DATA_RESIDENCY_REGION is required in %s", c.Environment)
		}
	}
	if c.Environment == "production" {
		if c.PrivacyMode == "development" || c.PrivacyMode == "test" {
			return fmt.Errorf("production requires an explicit GDPR, ru_152fz, or dual PRIVACY_MODE")
		}
		if strings.TrimSpace(c.PrivacyContactEmail) == "" {
			return fmt.Errorf("PRIVACY_CONTACT_EMAIL is required in production")
		}
		if c.PrivacyMode == "ru_152fz" || c.PrivacyMode == "dual" {
			if !strings.HasPrefix(c.DataResidencyRegion, "ru-") {
				return fmt.Errorf("Russian personal-data mode requires a ru-* primary DATA_RESIDENCY_REGION")
			}
			if !c.CrossBorderTransferRegistered {
				return fmt.Errorf("CROSS_BORDER_TRANSFER_REGISTERED must be true before external AI processing in Russian personal-data mode")
			}
		}
		if (c.PrivacyMode == "gdpr" || c.PrivacyMode == "dual") && strings.TrimSpace(c.GDPRTransferMechanism) == "" {
			return fmt.Errorf("GDPR_TRANSFER_MECHANISM is required for production external processing")
		}
	}
	if (strings.TrimSpace(c.CloudPaymentsPublicID) == "") != (strings.TrimSpace(c.CloudPaymentsAPISecret) == "") {
		return fmt.Errorf("CLOUDPAYMENTS_PUBLIC_ID and CLOUDPAYMENTS_API_SECRET must be configured together")
	}
	if c.BillingAdminKey != "" && len(c.BillingAdminKey) < 32 {
		return fmt.Errorf("BILLING_ADMIN_KEY must contain at least 32 characters")
	}
	if c.BillingPaymentsEnabled &&
		strings.TrimSpace(c.TopPaymentsCheckoutURL) == "" &&
		strings.TrimSpace(c.CloudPaymentsPublicID) == "" {
		return fmt.Errorf("BILLING_PAYMENTS_ENABLED requires a configured payment provider")
	}
	if c.BillingEnforcementEnabled && !c.BillingPaymentsEnabled && strings.TrimSpace(c.BillingAdminKey) == "" {
		return fmt.Errorf("billing enforcement requires either real payments or manual confirmation")
	}
	if c.AgentRuntimeEnabled {
		if len(c.AgentRuntimeSecret) < 32 {
			return fmt.Errorf("AGENT_RUNTIME_SECRET must contain at least 32 characters when the agent runtime is enabled")
		}
		runtimeURL, err := url.Parse(c.AgentRuntimeURL)
		if err != nil || runtimeURL.Scheme == "" || runtimeURL.Host == "" {
			return fmt.Errorf("AGENT_RUNTIME_URL must be a valid absolute URL")
		}
		if c.AgentRuntimeMaxTurns < 2 || c.AgentRuntimeMaxTurns > 30 {
			return fmt.Errorf("AGENT_RUNTIME_MAX_TURNS must be between 2 and 30")
		}
	}
	return nil
}

func validPrivacyMode(value string) bool {
	switch value {
	case "development", "test", "gdpr", "ru_152fz", "dual":
		return true
	default:
		return false
	}
}

func (c *Config) ConnString() string {
	connection := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort)),
		Path:   "/" + c.DBName,
	}
	if c.DBPassword == "" {
		connection.User = url.User(c.DBUser)
	} else {
		connection.User = url.UserPassword(c.DBUser, c.DBPassword)
	}
	query := connection.Query()
	query.Set("sslmode", c.DBSSLMode)
	query.Set("connect_timeout", "5")
	connection.RawQuery = query.Encode()
	return connection.String()
}

func isLoopbackDatabaseHost(host string) bool {
	normalized := strings.TrimSpace(strings.ToLower(host))
	if normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1" {
		return true
	}
	return net.ParseIP(normalized) != nil && net.ParseIP(normalized).IsLoopback()
}
