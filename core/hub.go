package core

import (
	"encoding/json"
	"sync"

	"pusher-clone/metrics"
)

// ChannelMember holds data about a user subscribed to a presence channel.
type ChannelMember struct {
	UserID   string
	UserInfo json.RawMessage
}

// AppHub tracks the state for a single tenant (application).
type AppHub struct {
	mu       sync.RWMutex
	AppID    string
	Clients  map[string]*Client
	Channels map[string]map[*Client]*ChannelMember
}

func NewAppHub(appID string) *AppHub {
	return &AppHub{
		AppID:    appID,
		Clients:  make(map[string]*Client),
		Channels: make(map[string]map[*Client]*ChannelMember),
	}
}

func (h *AppHub) RegisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Clients[client.SocketID] = client
	metrics.ActiveConnections.WithLabelValues(h.AppID).Inc()
}

func (h *AppHub) UnregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.Clients[client.SocketID]; ok {
		delete(h.Clients, client.SocketID)
		metrics.ActiveConnections.WithLabelValues(h.AppID).Dec()

		// Remove from all channels
		var channelsToRemove []string
		for channelName, subscribers := range h.Channels {
			if _, ok := subscribers[client]; ok {
				channelsToRemove = append(channelsToRemove, channelName)
			}
		}

		for _, channelName := range channelsToRemove {
			h.removeClientFromChannel(client, channelName)
		}
		close(client.Send)
	}
}

func (h *AppHub) Unsubscribe(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.removeClientFromChannel(client, channel)
}

// removeClientFromChannel removes a client from a specific channel.
// It must be called with h.mu.Lock() held.
func (h *AppHub) removeClientFromChannel(client *Client, channel string) {
	subscribers, ok := h.Channels[channel]
	if !ok {
		return
	}

	member, ok := subscribers[client]
	if !ok {
		return
	}

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
			payload := []byte(`{"event":"pusher_internal:member_removed","channel":"` + channel + `","data":"{\"user_id\":\"` + member.UserID + `\"}"}`)
			go h.BroadcastToChannel(channel, payload, "")
		}
	}

	if len(subscribers) == 0 {
		delete(h.Channels, channel)
		metrics.ChannelsActive.WithLabelValues(h.AppID).Dec()
	}
}

func (h *AppHub) Subscribe(client *Client, channel string, member *ChannelMember) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Channels[channel] == nil {
		h.Channels[channel] = make(map[*Client]*ChannelMember)
		metrics.ChannelsActive.WithLabelValues(h.AppID).Inc()
	}

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

	metrics.MessagesPublishedTotal.WithLabelValues(h.AppID).Inc()

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
