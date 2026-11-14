package core

import (
	"sync"

	"github.com/gorilla/websocket"
)

type Client struct {
	AppHub   *AppHub
	Conn     *websocket.Conn
	Send     chan []byte
	SocketID string

	// sendMu guards Send against the close race: senders hold the read lock
	// while enqueueing, CloseSend holds the write lock. Without it, a send
	// (even a non-blocking select send) that races with close would panic.
	sendMu     sync.RWMutex
	sendClosed bool
}

func NewClient(appHub *AppHub, conn *websocket.Conn, socketID string) *Client {
	return &Client{
		AppHub:   appHub,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		SocketID: socketID,
	}
}

// TrySend queues msg on the outbound channel without blocking. It returns
// false when the client is closing or its buffer is full, in which case the
// message is dropped. Dropping keeps the read pump responsive: a stalled
// client cannot wedge its read loop behind a full buffer.
func (c *Client) TrySend(msg []byte) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()

	if c.sendClosed {
		return false
	}

	select {
	case c.Send <- msg:
		return true
	default:
		return false
	}
}

// CloseSend closes the outbound message channel exactly once. The write pump
// observes the close and shuts down the WebSocket connection.
func (c *Client) CloseSend() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if c.sendClosed {
		return
	}
	c.sendClosed = true
	close(c.Send)
}
