package core

import (
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"pusher-clone/metrics"
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

	// ClientEventWindowStart and ClientEventCount implement a fixed-window
	// rate limit for events sent by this client. They are only touched by the
	// connection's read pump, so no lock is needed.
	ClientEventWindowStart time.Time
	ClientEventCount       int

	// channelCount is the number of channels this client is subscribed to.
	// It is guarded by the owning AppHub's mutex.
	channelCount int
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
// false when the client is closing. If the outbound buffer is full, the client
// is disconnected: silently dropping frames would corrupt the event stream for
// a client that later catches up, so reconnecting is the safer behavior.
func (c *Client) TrySend(msg []byte) bool {
	c.sendMu.RLock()
	if c.sendClosed {
		c.sendMu.RUnlock()
		return false
	}

	select {
	case c.Send <- msg:
		c.sendMu.RUnlock()
		return true
	default:
		c.sendMu.RUnlock()
		c.disconnectSlowClient()
		return false
	}
}

// disconnectSlowClient closes a client whose outbound buffer is full. It is
// idempotent and safe to call while other goroutines are sending.
func (c *Client) disconnectSlowClient() {
	c.sendMu.Lock()
	if c.sendClosed {
		c.sendMu.Unlock()
		return
	}
	c.sendClosed = true
	close(c.Send)
	c.sendMu.Unlock()

	appID := ""
	if c.AppHub != nil {
		appID = c.AppHub.AppID()
	}
	metrics.MessagesDroppedTotal.WithLabelValues(appID, "client_buffer_full").Inc()
	slog.Warn("Disconnecting slow client: send buffer full",
		"app_id", appID,
		"socket_id", c.SocketID,
	)

	if c.Conn != nil {
		c.Conn.Close()
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
