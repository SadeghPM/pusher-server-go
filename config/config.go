package config

import (
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	AppID          string   `yaml:"app_id"`
	AppKey         string   `yaml:"app_key"`
	AppSecret      string   `yaml:"app_secret"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type Config struct {
	Port string      `yaml:"port"`
	Apps []AppConfig `yaml:"apps"`
}

func LoadConfig(filename string) *Config {
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Fatalf("Failed to read config file %s: %v", filename, err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		log.Fatalf("Failed to parse config file %s: %v", filename, err)
	}

	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	return &cfg
}
