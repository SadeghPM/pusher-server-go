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

	closeSend sync.Once
}

func NewClient(appHub *AppHub, conn *websocket.Conn, socketID string) *Client {
	return &Client{
		AppHub:   appHub,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		SocketID: socketID,
	}
}

// CloseSend closes the outbound message channel exactly once. The write pump
// observes the close and shuts down the WebSocket connection.
func (c *Client) CloseSend() {
	c.closeSend.Do(func() {
		close(c.Send)
	})
}
