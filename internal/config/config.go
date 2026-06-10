package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	OpenAIKey   string
	OpenAIModel string

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
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     port,
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),

		OpenAIKey:   os.Getenv("OPENAI_API_KEY"),
		OpenAIModel: model,

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

func (c *Config) ConnString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}
