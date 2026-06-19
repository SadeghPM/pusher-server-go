package core

import (
	"sync"
)

// AppHub tracks the state for a single tenant (application).
type AppHub struct {
	mu       sync.RWMutex
	AppID    string
	Clients  map[string]*Client
	Channels map[string]map[*Client]bool
}

func NewAppHub(appID string) *AppHub {
	return &AppHub{
		AppID:    appID,
		Clients:  make(map[string]*Client),
		Channels: make(map[string]map[*Client]bool),
	}
}

func (h *AppHub) RegisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Clients[client.SocketID] = client
}

func (h *AppHub) UnregisterClient(client *Client) {
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

func (h *AppHub) Subscribe(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Channels[channel] == nil {
		h.Channels[channel] = make(map[*Client]bool)
	}
	h.Channels[channel][client] = true
}

func (h *AppHub) BroadcastToChannel(channel string, message []byte, excludeSocketID string) {
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

// GlobalHub manages all AppHubs across the server.
type GlobalHub struct {
	mu      sync.RWMutex
	AppHubs map[string]*AppHub // map AppID to AppHub
}

func NewGlobalHub() *GlobalHub {
	return &GlobalHub{
		AppHubs: make(map[string]*AppHub),
	}
}

func (gh *GlobalHub) GetOrCreateAppHub(appID string) *AppHub {
	gh.mu.Lock()
	defer gh.mu.Unlock()

	if hub, ok := gh.AppHubs[appID]; ok {
		return hub
	}

	newHub := NewAppHub(appID)
	gh.AppHubs[appID] = newHub
	return newHub
}

func (gh *GlobalHub) GetAppHub(appID string) *AppHub {
	gh.mu.RLock()
	defer gh.mu.RUnlock()

	return gh.AppHubs[appID]
}
