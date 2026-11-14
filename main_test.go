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
