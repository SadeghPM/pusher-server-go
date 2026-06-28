package core

import (
	"testing"

	"github.com/gorilla/websocket"
)

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
