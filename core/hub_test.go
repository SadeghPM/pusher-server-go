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

	isNewUser := hub.Subscribe(client, "my-channel", nil)

	if isNewUser {
		t.Errorf("Expected isNewUser to be false for non-presence channel")
	}

	if len(hub.Channels["my-channel"]) != 1 {
		t.Errorf("Expected 1 subscriber in channel, got %d", len(hub.Channels["my-channel"]))
	}
}

func TestAppHubSubscribePresence(t *testing.T) {
	hub := NewAppHub("test-app")

	client1 := &Client{SocketID: "111.111", Send: make(chan []byte)}
	client2 := &Client{SocketID: "222.222", Send: make(chan []byte)}
	client3 := &Client{SocketID: "333.333", Send: make(chan []byte)}

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)

	member1 := &ChannelMember{UserID: "user_a", UserInfo: []byte(`{"name":"Alice"}`)}
	member2 := &ChannelMember{UserID: "user_a", UserInfo: []byte(`{"name":"Alice"}`)} // Same user, different client
	member3 := &ChannelMember{UserID: "user_b", UserInfo: []byte(`{"name":"Bob"}`)}

	// First client subscribing with user_a
	isNewUser1 := hub.Subscribe(client1, "presence-channel", member1)
	if !isNewUser1 {
		t.Errorf("Expected isNewUser to be true for the first connection of user_a")
	}

	// Second client subscribing with user_a
	isNewUser2 := hub.Subscribe(client2, "presence-channel", member2)
	if isNewUser2 {
		t.Errorf("Expected isNewUser to be false for the second connection of user_a")
	}

	// Third client subscribing with user_b
	isNewUser3 := hub.Subscribe(client3, "presence-channel", member3)
	if !isNewUser3 {
		t.Errorf("Expected isNewUser to be true for the first connection of user_b")
	}

	if len(hub.Channels["presence-channel"]) != 3 {
		t.Errorf("Expected 3 subscribers in channel, got %d", len(hub.Channels["presence-channel"]))
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
