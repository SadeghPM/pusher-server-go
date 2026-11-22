package config

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"public https", "https://1.1.1.1/hook", false},
		{"public http with port", "http://1.1.1.1:8080/hook", false},
		{"no scheme", "example.com/hook", true},
		{"file scheme", "file:///etc/passwd", true},
		{"loopback ip", "http://127.0.0.1/hook", true},
		{"localhost", "http://localhost/hook", true},
		{"private ip", "http://10.1.2.3/hook", true},
		{"metadata service", "http://169.254.169.254/latest/meta-data", true},
		{"ipv6 loopback", "http://[::1]/hook", true},
		{"embedded credentials", "http://user:pass@1.1.1.1/hook", true},
		{"missing host", "http:///hook", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWebhookURL(tc.url)
			if tc.wantErr && err == nil {
				t.Errorf("Expected %q to be rejected", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Expected %q to be accepted, got %v", tc.url, err)
			}
		})
	}
}

func TestValidateAdminToken(t *testing.T) {
	if err := ValidateAdminToken("a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"); err != nil {
		t.Errorf("Expected a random token to be accepted, got %v", err)
	}
	for _, token := range []string{
		"",
		"short",
		"my-super-secret-admin-token",
		"your-super-secret-admin-token",
		"REPLACE_WITH_A_LONG_RANDOM_TOKEN",
		"changeme",
	} {
		if err := ValidateAdminToken(token); err == nil {
			t.Errorf("Expected placeholder/short token %q to be rejected", token)
		}
	}
}

func TestNewManagerRejectsUnsafeConfig(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "app id with slash",
			data: "apps:\n  - app_id: \"bad/id\"\n    app_key: k\n    app_secret: s\n",
		},
		{
			name: "local webhook",
			data: "apps:\n  - app_id: good-app\n    app_key: k\n    app_secret: s\n    webhooks: [\"http://127.0.0.1/hook\"]\n",
		},
		{
			name: "placeholder admin token",
			data: "admin_token: my-super-secret-admin-token\napps: []\n",
		},
		{
			name: "whitespace admin token",
			data: "admin_token: \"   \"\napps: []\n",
		},
		{
			name: "invalid trusted proxy",
			data: "trusted_proxies: [\"not-an-ip\"]\napps: []\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}
			if _, err := NewManager(path); err == nil {
				t.Error("Expected NewManager to reject the config")
			}
		})
	}
}

func TestConfigNormalizesStoredValues(t *testing.T) {
	data := `
admin_token: "  a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6  "
trusted_proxies: [" 127.0.0.1 ", " 192.0.2.0/24 "]
apps:
  - app_id: good-app
    app_key: k
    app_secret: s
    webhooks: [" https://1.1.1.1/hook "]
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("Expected the config to load, got %v", err)
	}

	cfg := manager.GetConfig()
	if cfg.AdminToken != "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6" {
		t.Errorf("admin token was not trimmed: %q", cfg.AdminToken)
	}
	if got := cfg.Apps[0].Webhooks[0]; got != "https://1.1.1.1/hook" {
		t.Errorf("webhook was not trimmed: %q", got)
	}
	if got := cfg.TrustedProxies[0]; got != "127.0.0.1" {
		t.Errorf("trusted proxy was not trimmed: %q", got)
	}
	if !cfg.IsTrustedProxy(net.ParseIP("192.0.2.7")) {
		t.Error("Expected 192.0.2.7 to be recognized as a trusted proxy")
	}
	if cfg.IsTrustedProxy(net.ParseIP("198.51.100.7")) {
		t.Error("Expected an unlisted address not to be trusted")
	}
}

func TestManagerRejectsUnsafeWebhooks(t *testing.T) {
	manager := NewManagerFromConfig(&Config{})
	err := manager.AddApp(AppConfig{
		AppID:    "good-app",
		Webhooks: []string{"http://127.0.0.1/hook"},
	})
	if !errors.Is(err, ErrAppInvalid) {
		t.Errorf("Expected AddApp to reject an unsafe webhook, got %v", err)
	}

	manager = NewManagerFromConfig(&Config{
		Apps: []AppConfig{{AppID: "good-app", AppKey: "k", AppSecret: "s"}},
	})
	err = manager.UpdateApp("good-app", nil, []string{"http://localhost/hook"})
	if !errors.Is(err, ErrAppInvalid) {
		t.Errorf("Expected UpdateApp to reject an unsafe webhook, got %v", err)
	}
}
