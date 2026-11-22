package core

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestTrySendDisconnectsWhenBufferIsFull(t *testing.T) {
	appHub := NewAppHub("test-app", nil)
	defer appHub.Close()

	client := &Client{
		AppHub:   appHub,
		SocketID: "1.1",
		Send:     make(chan []byte, 1),
	}
	client.Send <- []byte("first")

	if client.TrySend([]byte("second")) {
		t.Fatal("Expected TrySend to fail when the outbound buffer is full")
	}
	if !client.sendClosed {
		t.Fatal("Expected a full buffer to close the client")
	}

	if msg, ok := <-client.Send; !ok || string(msg) != "first" {
		t.Fatalf("Expected the buffered message to remain readable, got %q (ok=%v)", msg, ok)
	}
	if _, ok := <-client.Send; ok {
		t.Fatal("Expected the send channel to be closed")
	}
}

func TestNewClient(t *testing.T) {
	appHub := NewAppHub("test-app", nil)
	var conn *websocket.Conn = nil
	socketID := "12345.67890"

	client := NewClient(appHub, conn, socketID)

	if client.AppHub != appHub {
		t.Errorf("Expected AppHub %v, got %v", appHub, client.AppHub)
	}

	if client.Conn != conn {
		t.Errorf("Expected Conn %v, got %v", conn, client.Conn)
	}

	if client.SocketID != socketID {
		t.Errorf("Expected SocketID %v, got %v", socketID, client.SocketID)
	}

	if client.Send == nil {
		t.Fatalf("Expected Send channel to be initialized")
	}

	if cap(client.Send) != 256 {
		t.Errorf("Expected Send channel capacity 256, got %d", cap(client.Send))
	}
}
