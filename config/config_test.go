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

func TestManager_AddApp(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test_addapp")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file
	tempFile := filepath.Join(tempDir, "config.yaml")
	configData := []byte(`
port: "6001"
apps:
  - app_id: "existing-app"
    app_key: "existing-key"
    app_secret: "existing-secret"
`)
	if err := os.WriteFile(tempFile, configData, 0644); err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Test: Add a new app successfully
	newApp := AppConfig{
		AppID:          "new-app",
		AppKey:         "new-key",
		AppSecret:      "new-secret",
		AllowedOrigins: []string{"*"},
		Webhooks:       []string{"https://example.com/webhook"},
	}
	if err := manager.AddApp(newApp); err != nil {
		t.Fatalf("Expected AddApp to succeed, got error: %v", err)
	}

	// Verify in-memory lookup
	app := manager.GetAppByID("new-app")
	if app == nil {
		t.Fatal("Expected to find new-app by ID, got nil")
	}
	if app.AppKey != "new-key" {
		t.Errorf("Expected app_key 'new-key', got '%s'", app.AppKey)
	}
	if app.AppSecret != "new-secret" {
		t.Errorf("Expected app_secret 'new-secret', got '%s'", app.AppSecret)
	}

	appByKey := manager.GetAppByKey("new-key")
	if appByKey == nil || appByKey.AppID != "new-app" {
		t.Fatalf("Expected to find new-app by key, got %v", appByKey)
	}

	// Verify the config was persisted to disk
	manager2, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to reload config from disk: %v", err)
	}
	cfg := manager2.GetConfig()
	if len(cfg.Apps) != 2 {
		t.Fatalf("Expected 2 apps on disk, got %d", len(cfg.Apps))
	}
	diskApp := manager2.GetAppByID("new-app")
	if diskApp == nil || diskApp.AppKey != "new-key" {
		t.Fatalf("Expected new-app to be persisted on disk, got %v", diskApp)
	}
}

func TestManager_AddApp_DuplicateID(t *testing.T) {
	manager := NewManagerFromConfig(&Config{
		Apps: []AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
		},
	})

	err := manager.AddApp(AppConfig{
		AppID: "app1", AppKey: "different-key", AppSecret: "secret",
	})
	if err == nil {
		t.Fatal("Expected error for duplicate app_id, got nil")
	}
}

func TestManager_AddApp_DuplicateKey(t *testing.T) {
	manager := NewManagerFromConfig(&Config{
		Apps: []AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
		},
	})

	err := manager.AddApp(AppConfig{
		AppID: "app2", AppKey: "key1", AppSecret: "secret",
	})
	if err == nil {
		t.Fatal("Expected error for duplicate app_key, got nil")
	}
}

func TestManager_AddApp_AutoGenerate(t *testing.T) {
	manager := NewManagerFromConfig(&Config{})
	newApp := AppConfig{
		AppID: "slug-id",
	}
	if err := manager.AddApp(newApp); err != nil {
		t.Fatalf("Expected AddApp to succeed with only AppID, got error: %v", err)
	}

	app := manager.GetAppByID("slug-id")
	if app == nil {
		t.Fatal("Expected to find new app")
	}
	if len(app.AppKey) != 32 { // 16 bytes random hex
		t.Errorf("Expected 32-character key, got %q", app.AppKey)
	}
	if len(app.AppSecret) != 48 { // 24 bytes random hex
		t.Errorf("Expected 48-character secret, got %q", app.AppSecret)
	}
}

func TestManager_AddApp_InvalidID(t *testing.T) {
	manager := NewManagerFromConfig(&Config{})

	tests := []struct {
		name  string
		appID string
	}{
		{"empty app_id", ""},
		{"too short", "a"},
		{"too long", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"},
		{"starts with hyphen", "-abc"},
		{"ends with hyphen", "abc-"},
		{"uppercase letters", "App-Id"},
		{"invalid characters", "app_id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := manager.AddApp(AppConfig{AppID: tc.appID})
			if err == nil {
				t.Errorf("Expected error for app_id %q, got nil", tc.appID)
			}
		})
	}
}

func TestManager_GetAllApps(t *testing.T) {
	manager := NewManagerFromConfig(&Config{
		Apps: []AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
			{AppID: "app2", AppKey: "key2", AppSecret: "secret2"},
		},
	})

	apps := manager.GetAllApps()
	if len(apps) != 2 {
		t.Fatalf("Expected 2 apps, got %d", len(apps))
	}

	// Verify it's a copy (modifying returned slice shouldn't affect manager)
	apps[0].AppID = "modified"
	original := manager.GetAppByID("app1")
	if original == nil {
		t.Fatal("Original app1 should still exist after modifying returned copy")
	}
}

func TestManager_DeleteApp(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test_deleteapp")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file with two apps
	tempFile := filepath.Join(tempDir, "config.yaml")
	configData := []byte(`
port: "6001"
apps:
  - app_id: "app1"
    app_key: "key1"
    app_secret: "secret1"
  - app_id: "app2"
    app_key: "key2"
    app_secret: "secret2"
`)
	if err := os.WriteFile(tempFile, configData, 0644); err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Verify both apps exist
	if manager.GetAppByID("app1") == nil {
		t.Fatal("Expected app1 to exist")
	}
	if manager.GetAppByID("app2") == nil {
		t.Fatal("Expected app2 to exist")
	}

	// Delete app1
	if err := manager.DeleteApp("app1"); err != nil {
		t.Fatalf("Expected DeleteApp to succeed, got: %v", err)
	}

	// Verify app1 is gone from memory
	if manager.GetAppByID("app1") != nil {
		t.Fatal("Expected app1 to be deleted from memory")
	}
	if manager.GetAppByKey("key1") != nil {
		t.Fatal("Expected key1 lookup to return nil")
	}

	// Verify app2 still exists
	if manager.GetAppByID("app2") == nil {
		t.Fatal("Expected app2 to still exist")
	}

	// Verify config list length
	apps := manager.GetAllApps()
	if len(apps) != 1 {
		t.Fatalf("Expected 1 app remaining, got %d", len(apps))
	}

	// Verify persisted to disk
	manager2, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to reload config from disk: %v", err)
	}
	if len(manager2.GetAllApps()) != 1 {
		t.Fatalf("Expected 1 app on disk, got %d", len(manager2.GetAllApps()))
	}
	if manager2.GetAppByID("app1") != nil {
		t.Fatal("Expected app1 to be deleted from disk")
	}
	if manager2.GetAppByID("app2") == nil {
		t.Fatal("Expected app2 to persist on disk")
	}
}

func TestManager_DeleteApp_NotFound(t *testing.T) {
	manager := NewManagerFromConfig(&Config{
		Apps: []AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
		},
	})

	err := manager.DeleteApp("non-existent")
	if err == nil {
		t.Fatal("Expected error for deleting non-existent app, got nil")
	}
}

func TestManager_DeleteApp_EmptyID(t *testing.T) {
	manager := NewManagerFromConfig(&Config{})

	err := manager.DeleteApp("")
	if err == nil {
		t.Fatal("Expected error for empty app_id, got nil")
	}
}

func TestManager_UpdateApp(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "config_test_updateapp")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary config file
	tempFile := filepath.Join(tempDir, "config.yaml")
	configData := []byte(`
port: "6001"
apps:
  - app_id: "test-app"
    app_key: "key1"
    app_secret: "secret1"
    allowed_origins: ["*"]
    webhooks: ["https://example.com/w1"]
`)
	if err := os.WriteFile(tempFile, configData, 0644); err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	manager, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Update app
	origins := []string{"http://localhost:3000", "https://app.com"}
	webhooks := []string{"https://example.com/w2"}
	if err := manager.UpdateApp("test-app", origins, webhooks); err != nil {
		t.Fatalf("Expected UpdateApp to succeed, got error: %v", err)
	}

	// Verify memory lookup
	app := manager.GetAppByID("test-app")
	if app == nil {
		t.Fatal("Expected to find app")
	}
	if len(app.AllowedOrigins) != 2 || app.AllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("Unexpected allowed origins: %v", app.AllowedOrigins)
	}
	if len(app.Webhooks) != 1 || app.Webhooks[0] != "https://example.com/w2" {
		t.Errorf("Unexpected webhooks: %v", app.Webhooks)
	}

	// Verify disk persistence
	manager2, err := NewManager(tempFile)
	if err != nil {
		t.Fatalf("Failed to reload config from disk: %v", err)
	}
	diskApp := manager2.GetAppByID("test-app")
	if diskApp == nil {
		t.Fatal("Expected to find app on disk")
	}
	if len(diskApp.AllowedOrigins) != 2 || diskApp.AllowedOrigins[1] != "https://app.com" {
		t.Errorf("Unexpected allowed origins on disk: %v", diskApp.AllowedOrigins)
	}
}

func TestManager_UpdateApp_NotFound(t *testing.T) {
	manager := NewManagerFromConfig(&Config{})

	err := manager.UpdateApp("non-existent", []string{"*"}, nil)
	if err == nil {
		t.Fatal("Expected error for updating non-existent app, got nil")
	}
}

