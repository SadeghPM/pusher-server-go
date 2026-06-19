package core

import (
	"github.com/gorilla/websocket"
)

type Client struct {
	AppHub   *AppHub
	Conn     *websocket.Conn
	Send     chan []byte
	SocketID string
}

func NewClient(appHub *AppHub, conn *websocket.Conn, socketID string) *Client {
	return &Client{
		AppHub:   appHub,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		SocketID: socketID,
	}
}
