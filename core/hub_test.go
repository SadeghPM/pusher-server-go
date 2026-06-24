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

func TestAppHubBroadcastToChannel(t *testing.T) {
	hub := NewAppHub("test-app")

	client1 := &Client{SocketID: "1", Send: make(chan []byte, 1)}
	client2 := &Client{SocketID: "2", Send: make(chan []byte, 1)}
	client3 := &Client{SocketID: "3", Send: make(chan []byte, 1)}

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)

	hub.Subscribe(client1, "test-channel", nil)
	hub.Subscribe(client2, "test-channel", nil)
	hub.Subscribe(client3, "test-channel", nil)

	// Scenario 1: Broadcast to all
	msg1 := []byte("hello all")
	hub.BroadcastToChannel("test-channel", msg1, "")

	for _, c := range []*Client{client1, client2, client3} {
		select {
		case msg := <-c.Send:
			if string(msg) != string(msg1) {
				t.Errorf("Client %s expected '%s', got '%s'", c.SocketID, string(msg1), string(msg))
			}
		default:
			t.Errorf("Client %s did not receive message", c.SocketID)
		}
	}

	// Scenario 2: Broadcast excluding one client
	msg2 := []byte("hello except 2")
	hub.BroadcastToChannel("test-channel", msg2, "2")

	for _, c := range []*Client{client1, client3} {
		select {
		case msg := <-c.Send:
			if string(msg) != string(msg2) {
				t.Errorf("Client %s expected '%s', got '%s'", c.SocketID, string(msg2), string(msg))
			}
		default:
			t.Errorf("Client %s did not receive message", c.SocketID)
		}
	}

	select {
	case <-client2.Send:
		t.Errorf("Client 2 should have been excluded but received a message")
	default:
	}

	// Scenario 3: Broadcast to full buffer (should not block)
	client1.Send <- []byte("full")
	msg3 := []byte("hello full")
	hub.BroadcastToChannel("test-channel", msg3, "")

	// Client 1's buffer was full, so msg3 is dropped
	// Let's clear the first message
	droppedMsg := <-client1.Send
	if string(droppedMsg) != "full" {
		t.Errorf("Expected to read 'full' from client 1's buffer, got '%s'", string(droppedMsg))
	}

	select {
	case <-client1.Send:
		t.Errorf("Client 1 should not have received the second message due to full buffer")
	default:
	}

	for _, c := range []*Client{client2, client3} {
		select {
		case msg := <-c.Send:
			if string(msg) != string(msg3) {
				t.Errorf("Client %s expected '%s', got '%s'", c.SocketID, string(msg3), string(msg))
			}
		default:
			t.Errorf("Client %s did not receive message", c.SocketID)
		}
	}
}
