package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppID     string
	AppKey    string
	AppSecret string
	Port      string
}

func LoadConfig() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	return &Config{
		AppID:     getEnv("APP_ID", "1"),
		AppKey:    getEnv("APP_KEY", "app-key"),
		AppSecret: getEnv("APP_SECRET", "app-secret"),
		Port:      getEnv("PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
