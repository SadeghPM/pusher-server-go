package core

import (
	"github.com/gorilla/websocket"
)

type Client struct {
	Hub      *Hub
	Conn     *websocket.Conn
	Send     chan []byte
	SocketID string
}

func NewClient(hub *Hub, conn *websocket.Conn, socketID string) *Client {
	return &Client{
		Hub:      hub,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		SocketID: socketID,
	}
}
