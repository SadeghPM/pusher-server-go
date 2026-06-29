package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewManager_WithPort(t *testing.T) {
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
	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	cfg := manager.GetConfig()

	// Verify port
	if cfg.Port != "9090" {
		t.Errorf("Expected port '9090', got '%s'", cfg.Port)
	}

	// Verify apps
	if len(cfg.Apps) != 1 {
		t.Fatalf("Expected 1 app, got %d", len(cfg.Apps))
	}

	app := manager.GetAppByID("test_app_id")
	if app == nil {
		t.Fatalf("Expected app with ID 'test_app_id', got nil")
	}
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

func TestNewManager_DefaultPort(t *testing.T) {
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
	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	cfg := manager.GetConfig()

	// Verify default port
	if cfg.Port != "6001" {
		t.Errorf("Expected default port '6001', got '%s'", cfg.Port)
	}

	// Verify default metrics port
	if cfg.MetricsPort != "9601" {
		t.Errorf("Expected default metrics port '9601', got '%s'", cfg.MetricsPort)
	}

	// Verify apps
	if len(cfg.Apps) != 1 {
		t.Fatalf("Expected 1 app, got %d", len(cfg.Apps))
	}

	app := manager.GetAppByKey("test_app_key2")
	if app == nil {
		t.Fatalf("Expected app with key 'test_app_key2', got nil")
	}
	if app.AppID != "test_app_id2" {
		t.Errorf("Expected app_id 'test_app_id2', got '%s'", app.AppID)
	}
}

func TestManager_Reload(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test_reload")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file
	tempFile := filepath.Join(tempDir, "config.yaml")
	initialConfigData := []byte(`
port: "6001"
apps:
  - app_id: "app1"
    app_key: "key1"
    app_secret: "secret1"
`)
	if err := os.WriteFile(tempFile, initialConfigData, 0644); err != nil {
		t.Fatalf("Failed to write initial temp config file: %v", err)
	}

	// Load config
	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	app1 := manager.GetAppByID("app1")
	if app1 == nil || app1.AppKey != "key1" {
		t.Fatalf("Expected app1 to have key1")
	}

	// Modify the config file
	updatedConfigData := []byte(`
port: "6001"
apps:
  - app_id: "app1"
    app_key: "key1_updated"
    app_secret: "secret1"
  - app_id: "app2"
    app_key: "key2"
    app_secret: "secret2"
`)
	if err := os.WriteFile(tempFile, updatedConfigData, 0644); err != nil {
		t.Fatalf("Failed to write updated temp config file: %v", err)
	}

	// Trigger Reload
	if err := manager.Reload(); err != nil {
		t.Fatalf("Failed to reload manager: %v", err)
	}

	// Verify updates
	app1Updated := manager.GetAppByID("app1")
	if app1Updated == nil || app1Updated.AppKey != "key1_updated" {
		t.Fatalf("Expected app1 to be updated to key1_updated, got %v", app1Updated)
	}

	app2 := manager.GetAppByID("app2")
	if app2 == nil || app2.AppKey != "key2" {
		t.Fatalf("Expected app2 to be loaded, got %v", app2)
	}
}
