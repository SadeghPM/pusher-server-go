package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_WithPort(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file
	tempFile := filepath.Join(tempDir, "config.yaml")
	configData := []byte(`
port: "9090"
apps:
  - app_id: "test_app_id"
    app_key: "test_app_key"
    app_secret: "test_app_secret"
`)
	if err := os.WriteFile(tempFile, configData, 0644); err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	// Load config
	cfg := LoadConfig(tempFile)

	// Verify port
	if cfg.Port != "9090" {
		t.Errorf("Expected port '9090', got '%s'", cfg.Port)
	}

	// Verify apps
	if len(cfg.Apps) != 1 {
		t.Fatalf("Expected 1 app, got %d", len(cfg.Apps))
	}

	app := cfg.Apps[0]
	if app.AppID != "test_app_id" {
		t.Errorf("Expected app_id 'test_app_id', got '%s'", app.AppID)
	}
	if app.AppKey != "test_app_key" {
		t.Errorf("Expected app_key 'test_app_key', got '%s'", app.AppKey)
	}
	if app.AppSecret != "test_app_secret" {
		t.Errorf("Expected app_secret 'test_app_secret', got '%s'", app.AppSecret)
	}
}

func TestLoadConfig_DefaultPort(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file without port
	tempFile := filepath.Join(tempDir, "config.yaml")
	configData := []byte(`
apps:
  - app_id: "test_app_id2"
    app_key: "test_app_key2"
    app_secret: "test_app_secret2"
`)
	if err := os.WriteFile(tempFile, configData, 0644); err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	// Load config
	cfg := LoadConfig(tempFile)

	// Verify default port
	if cfg.Port != "6001" {
		t.Errorf("Expected default port '6001', got '%s'", cfg.Port)
	}

	// Verify apps
	if len(cfg.Apps) != 1 {
		t.Fatalf("Expected 1 app, got %d", len(cfg.Apps))
	}

	app := cfg.Apps[0]
	if app.AppID != "test_app_id2" {
		t.Errorf("Expected app_id 'test_app_id2', got '%s'", app.AppID)
	}
}
