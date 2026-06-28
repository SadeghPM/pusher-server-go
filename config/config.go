package config

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

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

type Manager struct {
	mu        sync.RWMutex
	filename  string
	config    *Config
	appsByID  map[string]*AppConfig
	appsByKey map[string]*AppConfig
}

func NewManager(filename string) (*Manager, error) {
	m := &Manager{filename: filename}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

func NewManagerFromConfig(cfg *Config) *Manager {
	m := &Manager{config: cfg}
	m.buildMaps()
	return m
}

func (m *Manager) buildMaps() {
	appsByID := make(map[string]*AppConfig)
	appsByKey := make(map[string]*AppConfig)
	if m.config != nil {
		for i := range m.config.Apps {
			appsByID[m.config.Apps[i].AppID] = &m.config.Apps[i]
			appsByKey[m.config.Apps[i].AppKey] = &m.config.Apps[i]
		}
	}
	m.appsByID = appsByID
	m.appsByKey = appsByKey
}

func (m *Manager) Reload() error {
	data, err := os.ReadFile(m.filename)
	if err != nil {
		return fmt.Errorf("failed to read config file %q: %w", m.filename, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config file %q: %w", m.filename, err)
	}

	if cfg.Port == "" {
		cfg.Port = "6001"
	}
	if cfg.MetricsPort == "" {
		cfg.MetricsPort = "9601"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = &cfg
	m.buildMaps()

	slog.Info("Configuration reloaded successfully", "filename", m.filename)
	return nil
}

func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

func (m *Manager) GetAppByID(appID string) *AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appsByID[appID]
}

func (m *Manager) GetAppByKey(appKey string) *AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appsByKey[appKey]
}
