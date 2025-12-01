package config

import (
    "fmt"
)

type Config struct {
    DBHost     string
    DBPort     int
    DBUser     string
    DBPassword string
    DBName     string
}

func Load() *Config {
    return &Config{
        DBHost:     "195.133.73.36",
        DBPort:     5432,
        DBUser:     "gen_user",
        DBPassword: "!%:,337nb:kPUU",
        DBName:     "default_db",   // ← ВАЖНО!
    }
}

func (c *Config) ConnString() string {
    return fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
        c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
    )
}
