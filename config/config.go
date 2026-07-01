package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"

	"gopkg.in/yaml.v3"
)

// slugRegex matches valid slug identifiers: lowercase letters, digits, and hyphens.
// Must start and end with a letter or digit, 2-64 characters.
var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// IsValidSlug checks if a string is a valid slug (lowercase, digits, hyphens, 2-64 chars).
func IsValidSlug(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	return slugRegex.MatchString(s)
}

// generateRandomHex returns a hex-encoded random string of the given byte length.
func generateRandomHex(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type AppConfig struct {
	AppID          string   `yaml:"app_id"`
	AppKey         string   `yaml:"app_key"`
	AppSecret      string   `yaml:"app_secret"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	Webhooks       []string `yaml:"webhooks"`
}

type Config struct {
	Port          string      `yaml:"port"`
	MetricsPort   string      `yaml:"metrics_port"`
	DashboardPort string      `yaml:"dashboard_port"`
	AdminToken    string      `yaml:"admin_token"`
	Debug         bool        `yaml:"debug"`
	Apps          []AppConfig `yaml:"apps"`
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
	if cfg.DashboardPort == "" {
		cfg.DashboardPort = "5174"
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

func (m *Manager) GetAllApps() []AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	apps := make([]AppConfig, len(m.config.Apps))
	copy(apps, m.config.Apps)
	return apps
}

// AddApp adds a new app to the configuration, rebuilds the lookup maps,
// and persists the updated configuration to disk.
// If AppKey or AppSecret are empty, they will be auto-generated.
func (m *Manager) AddApp(app AppConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate app_id is provided
	if app.AppID == "" {
		return fmt.Errorf("app_id is required")
	}

	// Validate slug format
	if !IsValidSlug(app.AppID) {
		return fmt.Errorf("app_id must be a valid slug (lowercase letters, digits, hyphens, 2-64 chars, no leading/trailing hyphen)")
	}

	// Auto-generate app_key if not provided
	if app.AppKey == "" {
		key, err := generateRandomHex(16)
		if err != nil {
			return fmt.Errorf("failed to generate app_key: %w", err)
		}
		app.AppKey = key
	}

	// Auto-generate app_secret if not provided
	if app.AppSecret == "" {
		secret, err := generateRandomHex(24)
		if err != nil {
			return fmt.Errorf("failed to generate app_secret: %w", err)
		}
		app.AppSecret = secret
	}

	// Check for duplicate app_id
	if _, exists := m.appsByID[app.AppID]; exists {
		return fmt.Errorf("app with id %q already exists", app.AppID)
	}

	// Check for duplicate app_key
	if _, exists := m.appsByKey[app.AppKey]; exists {
		return fmt.Errorf("app with key %q already exists", app.AppKey)
	}

	m.config.Apps = append(m.config.Apps, app)
	m.buildMaps()

	// Persist to disk
	if m.filename != "" {
		if err := m.saveConfigLocked(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}

	slog.Info("New app added", "app_id", app.AppID, "app_key", app.AppKey)
	return nil
}

// UpdateApp updates the mutable fields (AllowedOrigins, Webhooks) of an existing app.
func (m *Manager) UpdateApp(appID string, allowedOrigins []string, webhooks []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if appID == "" {
		return fmt.Errorf("app_id is required")
	}

	// Find the app in the slice
	found := false
	for i := range m.config.Apps {
		if m.config.Apps[i].AppID == appID {
			m.config.Apps[i].AllowedOrigins = allowedOrigins
			m.config.Apps[i].Webhooks = webhooks
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("app with id %q not found", appID)
	}

	m.buildMaps()

	// Persist to disk
	if m.filename != "" {
		if err := m.saveConfigLocked(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}

	slog.Info("App updated", "app_id", appID)
	return nil
}

// DeleteApp removes an app from the configuration by its ID,
// rebuilds the lookup maps, and persists the change to disk.
func (m *Manager) DeleteApp(appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if appID == "" {
		return fmt.Errorf("app_id is required")
	}

	if _, exists := m.appsByID[appID]; !exists {
		return fmt.Errorf("app with id %q not found", appID)
	}

	// Remove from the slice
	newApps := make([]AppConfig, 0, len(m.config.Apps)-1)
	for _, app := range m.config.Apps {
		if app.AppID != appID {
			newApps = append(newApps, app)
		}
	}
	m.config.Apps = newApps
	m.buildMaps()

	// Persist to disk
	if m.filename != "" {
		if err := m.saveConfigLocked(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}

	slog.Info("App deleted", "app_id", appID)
	return nil
}

// saveConfigLocked writes the current config to the file. Must be called with m.mu held.
func (m *Manager) saveConfigLocked() error {
	data, err := yaml.Marshal(m.config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(m.filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %q: %w", m.filename, err)
	}
	return nil
}
