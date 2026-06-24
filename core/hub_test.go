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

func TestAppHubGetPresenceMembers(t *testing.T) {
	hub := NewAppHub("test-app")

	// Create some clients
	client1 := &Client{SocketID: "123.456", Send: make(chan []byte)}
	client2 := &Client{SocketID: "123.457", Send: make(chan []byte)}
	client3 := &Client{SocketID: "123.458", Send: make(chan []byte)}

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)

	channelName := "presence-chat"

	// Test on empty channel
	members := hub.GetPresenceMembers(channelName)
	if len(members) != 0 {
		t.Errorf("Expected 0 presence members, got %d", len(members))
	}

	// Add user 1
	member1 := &ChannelMember{
		UserID:   "user1",
		UserInfo: []byte(`{"name":"Alice"}`),
	}
	hub.Subscribe(client1, channelName, member1)

	members = hub.GetPresenceMembers(channelName)
	if len(members) != 1 {
		t.Errorf("Expected 1 presence member, got %d", len(members))
	}
	if string(members["user1"]) != `{"name":"Alice"}` {
		t.Errorf("Expected user1 info to be `{\"name\":\"Alice\"}`, got %s", string(members["user1"]))
	}

	// Add user 2
	member2 := &ChannelMember{
		UserID:   "user2",
		UserInfo: []byte(`{"name":"Bob"}`),
	}
	hub.Subscribe(client2, channelName, member2)

	// Add another connection for user 1
	member1_copy := &ChannelMember{
		UserID:   "user1",
		UserInfo: []byte(`{"name":"Alice"}`), // Same user info
	}
	hub.Subscribe(client3, channelName, member1_copy)

	members = hub.GetPresenceMembers(channelName)
	if len(members) != 2 {
		t.Errorf("Expected 2 unique presence members, got %d", len(members))
	}
	if string(members["user1"]) != `{"name":"Alice"}` {
		t.Errorf("Expected user1 info to be `{\"name\":\"Alice\"}`, got %s", string(members["user1"]))
	}
	if string(members["user2"]) != `{"name":"Bob"}` {
		t.Errorf("Expected user2 info to be `{\"name\":\"Bob\"}`, got %s", string(members["user2"]))
	}
}
