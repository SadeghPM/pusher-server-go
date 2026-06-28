package core

import (
	"testing"
)

func TestAppHubRegistration(t *testing.T) {
	hub := NewAppHub("test-app", nil)
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
	hub := NewAppHub("test-app", nil)
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	hub.Subscribe(client, "my-channel", nil)

	if len(hub.Channels["my-channel"]) != 1 {
		t.Errorf("Expected 1 subscriber in channel, got %d", len(hub.Channels["my-channel"]))
	}
}

func TestAppHubUnsubscribeNonExistentChannel(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	// Attempt to unsubscribe from a channel that doesn't exist
	hub.Unsubscribe(client, "non-existent-channel")

	if len(hub.Channels) != 0 {
		t.Errorf("Expected 0 channels, got %d", len(hub.Channels))
	}
}

func TestGlobalHub(t *testing.T) {
	global := NewGlobalHub(nil)

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
	hub := NewAppHub("test-app", nil)

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

func TestAppHubBroadcastToChannel(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	channelName := "test-channel"

	// Create clients with buffered channels so sends don't block
	client1 := &Client{SocketID: "123.1", Send: make(chan []byte, 1)}
	client2 := &Client{SocketID: "123.2", Send: make(chan []byte, 1)}
	client3 := &Client{SocketID: "123.3", Send: make(chan []byte, 1)}

	// Create a client with a full buffer to test non-blocking behavior
	clientFull := &Client{SocketID: "123.full", Send: make(chan []byte, 1)}
	clientFull.Send <- []byte("initial")

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)
	hub.RegisterClient(clientFull)

	hub.Subscribe(client1, channelName, nil)
	hub.Subscribe(client2, channelName, nil)
	hub.Subscribe(client3, channelName, nil)
	hub.Subscribe(clientFull, channelName, nil)

	payload := []byte(`{"event":"my_event","data":"hello"}`)

	// Broadcast to the channel, excluding client2
	hub.BroadcastToChannel(channelName, payload, client2.SocketID)

	// Check client1 (should receive)
	select {
	case msg := <-client1.Send:
		if string(msg) != string(payload) {
			t.Errorf("Client 1 expected %s, got %s", string(payload), string(msg))
		}
	default:
		t.Errorf("Client 1 did not receive message")
	}

	// Check client2 (should be excluded)
	select {
	case <-client2.Send:
		t.Errorf("Client 2 received message but should have been excluded")
	default:
		// Expected
	}

	// Check client3 (should receive)
	select {
	case msg := <-client3.Send:
		if string(msg) != string(payload) {
			t.Errorf("Client 3 expected %s, got %s", string(payload), string(msg))
		}
	default:
		t.Errorf("Client 3 did not receive message")
	}

	// Check clientFull (should not receive, but should not block broadcast)
	select {
	case msg := <-clientFull.Send:
		if string(msg) != "initial" {
			t.Errorf("Client full expected initial message, got %s", string(msg))
		}
	default:
		t.Errorf("Client full channel should have had initial message")
	}
}
