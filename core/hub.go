package core

import (
	"sync"
)

type Hub struct {
	mu       sync.RWMutex
	Clients  map[string]*Client
	Channels map[string]map[*Client]bool
}

func NewHub() *Hub {
	return &Hub{
		Clients:  make(map[string]*Client),
		Channels: make(map[string]map[*Client]bool),
	}
}

func (h *Hub) RegisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Clients[client.SocketID] = client
}

func (h *Hub) UnregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.Clients[client.SocketID]; ok {
		delete(h.Clients, client.SocketID)

		// Remove from all channels
		for channelName, subscribers := range h.Channels {
			if _, ok := subscribers[client]; ok {
				delete(subscribers, client)
				if len(subscribers) == 0 {
					delete(h.Channels, channelName)
				}
			}
		}
		close(client.Send)
	}
}

func (h *Hub) Subscribe(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Channels[channel] == nil {
		h.Channels[channel] = make(map[*Client]bool)
	}
	h.Channels[channel][client] = true
}

func (h *Hub) BroadcastToChannel(channel string, message []byte, excludeSocketID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if subscribers, ok := h.Channels[channel]; ok {
		for client := range subscribers {
			if client.SocketID != excludeSocketID {
				select {
				case client.Send <- message:
				default:
					// Cannot send, buffer full or closed
				}
			}
		}
	}
}
