package config

import (
	"fmt"
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
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	OpenAIKey                     string
	OpenAIModel                   string
	OpenAIAuditorModel            string
	OpenAITranscriptionModel      string
	OpenAIAuditorMaxOutputTokens  int
	OpenAIAuditorCompactThreshold int
	OpenAIProxyURL                string
	EnableAIBenchmark             bool
	AIAdminKey                    string
	AIRequestsPerMinute           int
	AIDailyBudgetUSD              float64
	AIMonthlyBudgetUSD            float64
	AIJobWorkers                  int

	JWTSecret          string
	CORSAllowedOrigins []string
	Environment        string
	HTTPReadTimeout    time.Duration
	HTTPWriteTimeout   time.Duration
	HTTPIdleTimeout    time.Duration

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
	transcriptionModel := os.Getenv("OPENAI_TRANSCRIPTION_MODEL")
	if transcriptionModel == "" {
		transcriptionModel = "gpt-4o-transcribe"
	}
	auditorMaxOutputTokens := parseIntEnv("OPENAI_AUDITOR_MAX_OUTPUT_TOKENS", 1800)
	auditorCompactThreshold := parseIntEnv("OPENAI_AUDITOR_COMPACT_THRESHOLD", 120000)
	openAIProxyURL := os.Getenv("OPENAI_PROXY_URL")
	if openAIProxyURL == "" {
		openAIProxyURL = "socks5://127.0.0.1:10808"
	}

	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if environment == "" {
		environment = "development"
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
		cloudPaymentsPlanName = "REUP.goals Pro"
	}

	cloudPaymentsAmount := parseFloatEnv("CLOUDPAYMENTS_AMOUNT", 199)
	cloudPaymentsFirstPaymentAmount := parseFloatEnv("CLOUDPAYMENTS_FIRST_PAYMENT_AMOUNT", 1)

	cloudPaymentsCurrency := os.Getenv("CLOUDPAYMENTS_CURRENCY")
	if cloudPaymentsCurrency == "" {
		cloudPaymentsCurrency = "RUB"
	}

	cloudPaymentsTrialDays := parseIntEnv("CLOUDPAYMENTS_TRIAL_DAYS", 14)

	return &Config{
		DBHost:            os.Getenv("DB_HOST"),
		DBPort:            port,
		DBUser:            os.Getenv("DB_USER"),
		DBPassword:        os.Getenv("DB_PASSWORD"),
		DBName:            os.Getenv("DB_NAME"),
		DBMaxOpenConns:    parseIntEnv("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:    parseIntEnv("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime: parseDurationEnv("DB_CONN_MAX_LIFETIME", 30*time.Minute),

		OpenAIKey:                     os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:                   model,
		OpenAIAuditorModel:            auditorModel,
		OpenAITranscriptionModel:      transcriptionModel,
		OpenAIAuditorMaxOutputTokens:  auditorMaxOutputTokens,
		OpenAIAuditorCompactThreshold: auditorCompactThreshold,
		OpenAIProxyURL:                openAIProxyURL,
		EnableAIBenchmark:             parseBoolEnv("ENABLE_AI_BENCHMARK"),
		AIAdminKey:                    strings.TrimSpace(os.Getenv("AI_ADMIN_KEY")),
		AIRequestsPerMinute:           parseIntEnv("AI_RATE_LIMIT_PER_MINUTE", 60),
		AIDailyBudgetUSD:              parseFloatEnv("AI_DAILY_BUDGET_USD", 0),
		AIMonthlyBudgetUSD:            parseFloatEnv("AI_MONTHLY_BUDGET_USD", 0),
		AIJobWorkers:                  parseIntEnv("AI_JOB_WORKERS", 2),

		JWTSecret:          jwtSecret,
		CORSAllowedOrigins: parseCSVEnv("CORS_ALLOWED_ORIGINS"),
		Environment:        environment,
		HTTPReadTimeout:    parseDurationEnv("HTTP_READ_TIMEOUT", 90*time.Second),
		HTTPWriteTimeout:   parseDurationEnv("HTTP_WRITE_TIMEOUT", 6*time.Minute),
		HTTPIdleTimeout:    parseDurationEnv("HTTP_IDLE_TIMEOUT", 90*time.Second),

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
	return nil
}

func (c *Config) ConnString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}
