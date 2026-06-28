package config

import (
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	AppID          string   `yaml:"app_id"`
	AppKey         string   `yaml:"app_key"`
	AppSecret      string   `yaml:"app_secret"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	Webhooks       []string `yaml:"webhooks"`
}

type Config struct {
	Port        string      `yaml:"port"`
	MetricsPort string      `yaml:"metrics_port"`
	Debug       bool        `yaml:"debug"`
	Apps        []AppConfig `yaml:"apps"`
}

func LoadConfig(filename string) *Config {
	data, err := os.ReadFile(filename)
	if err != nil {
		slog.Error("Failed to read config file", "filename", filename, "error", err)
		os.Exit(1)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		slog.Error("Failed to parse config file", "filename", filename, "error", err)
		os.Exit(1)
	}

	if cfg.Port == "" {
		cfg.Port = "6001"
	}
	if cfg.MetricsPort == "" {
		cfg.MetricsPort = "9601"
	}

	return &cfg
}
