package core

import (
	"testing"
)

func TestHubRegistration(t *testing.T) {
	hub := NewHub()
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

func TestHubSubscription(t *testing.T) {
	hub := NewHub()
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	hub.Subscribe(client, "my-channel")

	if len(hub.Channels["my-channel"]) != 1 {
		t.Errorf("Expected 1 subscriber in channel, got %d", len(hub.Channels["my-channel"]))
	}
}
