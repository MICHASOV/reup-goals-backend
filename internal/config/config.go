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
	}
}

func (c *Config) ConnString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}
