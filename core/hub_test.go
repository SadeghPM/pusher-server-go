package core

import (
	"testing"
)

func TestAppHubRegistration(t *testing.T) {
	hub := NewAppHub("test-app")
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}

	hub.RegisterClient(client)

	if len(hub.Clients) != 1 {
		t.Errorf("Expected 1 client, got %d", len(hub.Clients))
	}

	hub.UnregisterClient(client)

	if len(hub.Clients) != 0 {
		t.Errorf("Expected 0 clients, got %d", len(hub.Clients))
	}
}

func TestAppHubSubscription(t *testing.T) {
	hub := NewAppHub("test-app")
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	hub.Subscribe(client, "my-channel", nil)

	if len(hub.Channels["my-channel"]) != 1 {
		t.Errorf("Expected 1 subscriber in channel, got %d", len(hub.Channels["my-channel"]))
	}
}

func TestGlobalHub(t *testing.T) {
	global := NewGlobalHub()

	hub1 := global.GetOrCreateAppHub("app1")
	hub2 := global.GetOrCreateAppHub("app2")

	if hub1.AppID != "app1" || hub2.AppID != "app2" {
		t.Errorf("GlobalHub created apps with wrong IDs")
	}

	retrievedHub := global.GetAppHub("app1")
	if retrievedHub != hub1 {
		t.Errorf("Expected to retrieve the same hub instance")
	}
}
