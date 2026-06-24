package core

import (
	"encoding/json"
	"sync"
)

// ChannelMember holds data about a user subscribed to a presence channel.
type ChannelMember struct {
	UserID   string
	UserInfo json.RawMessage
}

// AppHub tracks the state for a single tenant (application).
type AppHub struct {
	mu             sync.RWMutex
	AppID          string
	Clients        map[string]*Client
	Channels       map[string]map[*Client]*ChannelMember
	PresenceCounts map[string]map[string]int
}

func NewAppHub(appID string) *AppHub {
	return &AppHub{
		AppID:          appID,
		Clients:        make(map[string]*Client),
		Channels:       make(map[string]map[*Client]*ChannelMember),
		PresenceCounts: make(map[string]map[string]int),
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
			if member, ok := subscribers[client]; ok {
				delete(subscribers, client)

				// If presence channel, check if this was the last connection for this user
				if member != nil {
					h.PresenceCounts[channelName][member.UserID]--
					if h.PresenceCounts[channelName][member.UserID] == 0 {
						delete(h.PresenceCounts[channelName], member.UserID)
						// It was the last connection, we should broadcast member_removed,
						// but since we are holding the lock, we should enqueue it or handle it in websocket.go.
						// Actually, we can just broadcast it right here if we use a goroutine, or we can add a method
						// to broadcast safely. To avoid deadlock, we can spin up a goroutine.
						payload := []byte(`{"event":"pusher_internal:member_removed","channel":"` + channelName + `","data":"{\"user_id\":\"` + member.UserID + `\"}"}`)
						go h.BroadcastToChannel(channelName, payload, "")
					}
				}

				if len(subscribers) == 0 {
					delete(h.Channels, channelName)
					delete(h.PresenceCounts, channelName)
				}
			}
		}
		close(client.Send)
	}
}

func (h *AppHub) Unsubscribe(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if subscribers, ok := h.Channels[channel]; ok {
		if member, ok := subscribers[client]; ok {
			delete(subscribers, client)

			// If presence channel, check if this was the last connection for this user
			if member != nil {
				h.PresenceCounts[channel][member.UserID]--
				if h.PresenceCounts[channel][member.UserID] == 0 {
					delete(h.PresenceCounts[channel], member.UserID)
					payload := []byte(`{"event":"pusher_internal:member_removed","channel":"` + channel + `","data":"{\"user_id\":\"` + member.UserID + `\"}"}`)
					go h.BroadcastToChannel(channel, payload, "")
				}
			}

			if len(subscribers) == 0 {
				delete(h.Channels, channel)
				delete(h.PresenceCounts, channel)
			}
		}
	}
}

func (h *AppHub) Subscribe(client *Client, channel string, member *ChannelMember) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Channels[channel] == nil {
		h.Channels[channel] = make(map[*Client]*ChannelMember)
	}

	if h.PresenceCounts[channel] == nil {
		h.PresenceCounts[channel] = make(map[string]int)
	}

	isNewUser := false
	if member != nil {
		if h.PresenceCounts[channel][member.UserID] == 0 {
			isNewUser = true
		}
		h.PresenceCounts[channel][member.UserID]++
	}

	h.Channels[channel][client] = member
	return isNewUser
}

func (h *AppHub) RLockChannels(cb func(map[string]map[*Client]*ChannelMember)) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cb(h.Channels)
}

func (h *AppHub) GetPresenceMembers(channel string) map[string]json.RawMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()

	members := make(map[string]json.RawMessage)
	if subscribers, ok := h.Channels[channel]; ok {
		for _, member := range subscribers {
			if member != nil {
				members[member.UserID] = member.UserInfo
			}
		}
	}
	return members
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
