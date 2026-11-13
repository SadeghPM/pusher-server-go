package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
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

// normalizeHost validates and normalizes the public base URL. It requires an
// absolute http(s) URL with no path (other than an optional trailing slash),
// query or fragment, and strips a trailing slash.
func normalizeHost(raw, port string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		host = "http://localhost:" + strings.TrimPrefix(port, ":")
	}

	parsed, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL %q must start with http:// or https://", raw)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("URL %q must include a host", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL %q must not include user credentials", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL %q must not include a path", raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("URL %q must not include a query string or fragment", raw)
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

type AppConfig struct {
	AppID          string   `yaml:"app_id"`
	AppKey         string   `yaml:"app_key"`
	AppSecret      string   `yaml:"app_secret"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	Webhooks       []string `yaml:"webhooks"`
}

type Config struct {
	Port          string `yaml:"port"`
	MetricsPort   string `yaml:"metrics_port"`
	DashboardPort string `yaml:"dashboard_port"`
	// Host is the public base URL of the Pusher server (for example
	// https://pusher.example.com). It is shown to third-party app developers
	// as the Pusher URL. When empty it defaults to http://localhost:<port>.
	Host       string      `yaml:"host"`
	AdminToken string      `yaml:"admin_token"`
	Debug      bool        `yaml:"debug"`
	Apps       []AppConfig `yaml:"apps"`
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
	if cfg == nil {
		cfg = &Config{}
	}
	m := &Manager{config: cloneConfig(cfg)}
	m.buildMaps()
	return m
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneApps(apps []AppConfig) []AppConfig {
	cloned := make([]AppConfig, len(apps))
	for i, app := range apps {
		cloned[i] = app
		cloned[i].AllowedOrigins = cloneStrings(app.AllowedOrigins)
		cloned[i].Webhooks = cloneStrings(app.Webhooks)
	}
	return cloned
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.Apps = cloneApps(cfg.Apps)
	return &cloned
}

// validateConfig rejects configurations that would make tenant lookups
// ambiguous or let a connection authenticate against an empty key.
func validateConfig(cfg *Config) error {
	seenIDs := make(map[string]struct{}, len(cfg.Apps))
	seenKeys := make(map[string]struct{}, len(cfg.Apps))

	for i, app := range cfg.Apps {
		if app.AppID == "" {
			return fmt.Errorf("apps[%d]: app_id is required", i)
		}
		if app.AppKey == "" {
			return fmt.Errorf("app %q: app_key is required", app.AppID)
		}
		if app.AppSecret == "" {
			return fmt.Errorf("app %q: app_secret is required", app.AppID)
		}
		if _, exists := seenIDs[app.AppID]; exists {
			return fmt.Errorf("duplicate app_id %q", app.AppID)
		}
		if _, exists := seenKeys[app.AppKey]; exists {
			return fmt.Errorf("duplicate app_key for app %q", app.AppID)
		}
		seenIDs[app.AppID] = struct{}{}
		seenKeys[app.AppKey] = struct{}{}
	}

	return nil
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

	host, err := normalizeHost(cfg.Host, cfg.Port)
	if err != nil {
		return fmt.Errorf("invalid host in config file %q: %w", m.filename, err)
	}
	cfg.Host = host

	if err := validateConfig(&cfg); err != nil {
		return fmt.Errorf("invalid config file %q: %w", m.filename, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config != nil && reflect.DeepEqual(m.config, &cfg) {
		return nil
	}

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
	if m.config == nil {
		return nil
	}
	return cloneApps(m.config.Apps)
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

	base := m.config
	if base == nil {
		base = &Config{}
	}
	next := cloneConfig(base)
	app.AllowedOrigins = cloneStrings(app.AllowedOrigins)
	app.Webhooks = cloneStrings(app.Webhooks)
	next.Apps = append(next.Apps, app)

	if err := m.commitConfigLocked(next); err != nil {
		return err
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
	if m.config == nil {
		return fmt.Errorf("app with id %q not found", appID)
	}

	next := cloneConfig(m.config)
	found := false
	for i := range next.Apps {
		if next.Apps[i].AppID == appID {
			next.Apps[i].AllowedOrigins = cloneStrings(allowedOrigins)
			next.Apps[i].Webhooks = cloneStrings(webhooks)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("app with id %q not found", appID)
	}

	if err := m.commitConfigLocked(next); err != nil {
		return err
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
	if m.config == nil {
		return fmt.Errorf("app with id %q not found", appID)
	}

	if _, exists := m.appsByID[appID]; !exists {
		return fmt.Errorf("app with id %q not found", appID)
	}

	// Build the next immutable snapshot.
	newApps := make([]AppConfig, 0, len(m.config.Apps)-1)
	for _, app := range m.config.Apps {
		if app.AppID != appID {
			newApps = append(newApps, app)
		}
	}
	next := cloneConfig(m.config)
	next.Apps = newApps

	if err := m.commitConfigLocked(next); err != nil {
		return err
	}

	slog.Info("App deleted", "app_id", appID)
	return nil
}

// commitConfigLocked persists next and only publishes it in memory after the
// write succeeds. Must be called with m.mu held.
func (m *Manager) commitConfigLocked(next *Config) error {
	if m.filename != "" {
		if err := m.saveConfigLocked(next); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}
	m.config = next
	m.buildMaps()
	return nil
}

// saveConfigLocked writes cfg to disk atomically with private permissions.
// Must be called with m.mu held.
func (m *Manager) saveConfigLocked(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(m.filename)
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set config permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write config file %q: %w", m.filename, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync config file %q: %w", m.filename, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close config file %q: %w", m.filename, err)
	}
	if err := os.Rename(tmpName, m.filename); err != nil {
		return fmt.Errorf("failed to replace config file %q: %w", m.filename, err)
	}
	return nil
}
