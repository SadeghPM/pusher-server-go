package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pusher-clone/api"
	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/server"
)

func newTestMux() *http.ServeMux {
	manager := config.NewManagerFromConfig(&config.Config{
		Port: "6001",
		Apps: []config.AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
		},
	})
	hub := core.NewGlobalHub(nil, nil)
	return newRootMux(
		server.NewServer(hub, manager),
		api.NewAPI(hub, manager),
		manager,
	)
}

func TestHealthEndpoint(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 for /health, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Expected application/json content type, got %q", ct)
	}

	var body struct {
		Status    string `json:"status"`
		AppsCount int    `json:"apps_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("Expected status 'ok', got %q", body.Status)
	}
	if body.AppsCount != 1 {
		t.Errorf("Expected apps_count 1, got %d", body.AppsCount)
	}
}

func TestReconcileAppHubsDisconnectsRemovedAndRotatedApps(t *testing.T) {
	global := core.NewGlobalHub(nil, nil)

	kept := global.GetOrCreateAppHub("kept")
	global.GetOrCreateAppHub("rotated")
	global.GetOrCreateAppHub("removed")

	previous := []config.AppConfig{
		{AppID: "kept", AppKey: "key-1", AppSecret: "secret-1"},
		{AppID: "rotated", AppKey: "key-1", AppSecret: "secret-1"},
		{AppID: "removed", AppKey: "key-1", AppSecret: "secret-1"},
	}
	current := []config.AppConfig{
		// Origin/webhook edits must not disconnect anyone.
		{AppID: "kept", AppKey: "key-1", AppSecret: "secret-1", AllowedOrigins: []string{"https://example.com"}},
		// A rotated key must force existing clients to reconnect.
		{AppID: "rotated", AppKey: "key-2", AppSecret: "secret-2"},
	}

	reconcileAppHubs(global, previous, current)

	if got := global.GetAppHub("kept"); got != kept {
		t.Error("Expected the hub of an unchanged app to survive the reload")
	}
	if got := global.GetAppHub("rotated"); got != nil {
		t.Error("Expected the hub of an app with rotated credentials to be removed")
	}
	if got := global.GetAppHub("removed"); got != nil {
		t.Error("Expected the hub of a removed app to be removed")
	}

	kept.Close()
}

func TestUnknownPathReturns404(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatalf("Failed to GET /nope: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown path, got %d", resp.StatusCode)
	}
}
