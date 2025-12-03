package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DBHost      string
	DBPort      int
	DBUser      string
	DBPassword  string
	DBName      string

	OpenAIKey    string
	AssistantID  string
}

func Load() *Config {

	// DB port parse
	portStr := os.Getenv("DB_PORT")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 5432 // fallback
	}

	return &Config{
		DBHost:      os.Getenv("DB_HOST"),
		DBPort:      port,
		DBUser:      os.Getenv("DB_USER"),
		DBPassword:  os.Getenv("DB_PASSWORD"),
		DBName:      os.Getenv("DB_NAME"),

		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
		AssistantID:  os.Getenv("OPENAI_ASSISTANT_ID"),
	}
}

func (c *Config) ConnString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}
