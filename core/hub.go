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
	ClientChannels map[*Client]map[string]struct{}
}

func NewAppHub(appID string) *AppHub {
	return &AppHub{
		AppID:          appID,
		Clients:        make(map[string]*Client),
		Channels:       make(map[string]map[*Client]*ChannelMember),
		ClientChannels: make(map[*Client]map[string]struct{}),
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

		// Remove from all channels the client is subscribed to
		if clientChannels, ok := h.ClientChannels[client]; ok {
			for channelName := range clientChannels {
				if subscribers, exists := h.Channels[channelName]; exists {
					if member, found := subscribers[client]; found {
						delete(subscribers, client)

						// If presence channel, check if this was the last connection for this user
						if member != nil {
							hasOtherConnections := false
							for _, m := range subscribers {
								if m != nil && m.UserID == member.UserID {
									hasOtherConnections = true
									break
								}
							}
							if !hasOtherConnections {
								// It was the last connection, we should broadcast member_removed
								payload := []byte(`{"event":"pusher_internal:member_removed","channel":"` + channelName + `","data":"{\"user_id\":\"` + member.UserID + `\"}"}`)
								go h.BroadcastToChannel(channelName, payload, "")
							}
						}

						if len(subscribers) == 0 {
							delete(h.Channels, channelName)
						}
					}
				}
			}
			delete(h.ClientChannels, client)
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
			delete(h.ClientChannels[client], channel)

			// If presence channel, check if this was the last connection for this user
			if member != nil {
				hasOtherConnections := false
				for _, m := range subscribers {
					if m != nil && m.UserID == member.UserID {
						hasOtherConnections = true
						break
					}
				}
				if !hasOtherConnections {
					payload := []byte(`{"event":"pusher_internal:member_removed","channel":"` + channel + `","data":"{\"user_id\":\"` + member.UserID + `\"}"}`)
					go h.BroadcastToChannel(channel, payload, "")
				}
			}

			if len(subscribers) == 0 {
				delete(h.Channels, channel)
			}
			if len(h.ClientChannels[client]) == 0 {
				delete(h.ClientChannels, client)
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

	if h.ClientChannels[client] == nil {
		h.ClientChannels[client] = make(map[string]struct{})
	}
	h.ClientChannels[client][channel] = struct{}{}

	isNewUser := false
	if member != nil {
		// Check if user is already in channel
		userExists := false
		for _, existingMember := range h.Channels[channel] {
			if existingMember != nil && existingMember.UserID == member.UserID {
				userExists = true
				break
			}
		}
		if !userExists {
			isNewUser = true
		}
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
