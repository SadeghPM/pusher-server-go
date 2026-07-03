package core

import (
	"encoding/json"
	"log/slog"
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
	mu         sync.RWMutex
	appID      string
	clients    map[string]*Client
	channels   map[string]map[*Client]*ChannelMember
	dispatcher WebhookDispatcher
	workQueue  chan func()
}

func NewAppHub(appID string, dispatcher WebhookDispatcher) *AppHub {
	if dispatcher == nil {
		dispatcher = &NoopWebhookDispatcher{}
	}
	h := &AppHub{
		appID:      appID,
		clients:    make(map[string]*Client),
		channels:   make(map[string]map[*Client]*ChannelMember),
		dispatcher: dispatcher,
		workQueue:  make(chan func(), 256),
	}
	// Start fixed worker pool for async dispatch/broadcast work.
	for i := 0; i < 4; i++ {
		go func() {
			for fn := range h.workQueue {
				fn()
			}
		}()
	}
	return h
}

// Close drains and shuts down the work queue. Call on graceful shutdown.
func (h *AppHub) Close() {
	close(h.workQueue)
}

// AppID returns the application identifier for this hub.
func (h *AppHub) AppID() string {
	return h.appID
}

// enqueue sends work to the bounded worker pool. Drops if full.
func (h *AppHub) enqueue(fn func()) {
	select {
	case h.workQueue <- fn:
	default:
		slog.Warn("Work queue full, dropping async task", "app_id", h.appID)
	}
}

// DispatchWebhook dispatches webhook events asynchronously via the work queue.
func (h *AppHub) DispatchWebhook(events []WebhookEvent) {
	h.enqueue(func() {
		h.dispatcher.Dispatch(h.appID, events)
	})
}

func (h *AppHub) RegisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client.SocketID] = client
	metrics.ActiveConnections.WithLabelValues(h.appID).Inc()
}

func (h *AppHub) UnregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[client.SocketID]; ok {
		delete(h.clients, client.SocketID)
		metrics.ActiveConnections.WithLabelValues(h.appID).Dec()

		// Remove from all channels
		var channelsToRemove []string
		for channelName, subscribers := range h.channels {
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
	subscribers, ok := h.channels[channel]
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
			h.enqueue(func() { h.BroadcastToChannel(channel, payload, "") })

			h.DispatchWebhook([]WebhookEvent{
				{
					Name:    "member_removed",
					Channel: channel,
					UserID:  member.UserID,
				},
			})
		}
	}

	if len(subscribers) == 0 {
		delete(h.channels, channel)
		metrics.ChannelsActive.WithLabelValues(h.appID).Dec()
		h.DispatchWebhook([]WebhookEvent{
			{
				Name:    "channel_vacated",
				Channel: channel,
			},
		})
	}
}

func (h *AppHub) Subscribe(client *Client, channel string, member *ChannelMember) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.channels[channel] == nil {
		h.channels[channel] = make(map[*Client]*ChannelMember)
		metrics.ChannelsActive.WithLabelValues(h.appID).Inc()
		h.DispatchWebhook([]WebhookEvent{
			{
				Name:    "channel_occupied",
				Channel: channel,
			},
		})
	}

	isNewUser := false
	if member != nil {
		// Check if user is already in channel
		userExists := false
		for _, existingMember := range h.channels[channel] {
			if existingMember != nil && existingMember.UserID == member.UserID {
				userExists = true
				break
			}
		}
		if !userExists {
			isNewUser = true
		}
	}

	h.channels[channel][client] = member
	return isNewUser
}

func (h *AppHub) RLockChannels(cb func(map[string]map[*Client]*ChannelMember)) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cb(h.channels)
}

func (h *AppHub) GetPresenceMembers(channel string) map[string]json.RawMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()

	members := make(map[string]json.RawMessage)
	if subscribers, ok := h.channels[channel]; ok {
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

	metrics.MessagesPublishedTotal.WithLabelValues(h.appID).Inc()

	if subscribers, ok := h.channels[channel]; ok {
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
	mu         sync.RWMutex
	AppHubs    map[string]*AppHub // map AppID to AppHub
	dispatcher WebhookDispatcher
	Debugger   DebugNotifier
}

func NewGlobalHub(dispatcher WebhookDispatcher, debugger DebugNotifier) *GlobalHub {
	if dispatcher == nil {
		dispatcher = &NoopWebhookDispatcher{}
	}
	if debugger == nil {
		debugger = NoopDebugNotifier{}
	}
	return &GlobalHub{
		AppHubs:    make(map[string]*AppHub),
		dispatcher: dispatcher,
		Debugger:   debugger,
	}
}

func (gh *GlobalHub) GetOrCreateAppHub(appID string) *AppHub {
	gh.mu.Lock()
	defer gh.mu.Unlock()

	if hub, ok := gh.AppHubs[appID]; ok {
		return hub
	}

	newHub := NewAppHub(appID, gh.dispatcher)
	gh.AppHubs[appID] = newHub
	return newHub
}

func (gh *GlobalHub) GetAppHub(appID string) *AppHub {
	gh.mu.RLock()
	defer gh.mu.RUnlock()

	return gh.AppHubs[appID]
}

// Close shuts down all AppHub work queues. Call on graceful shutdown.
func (gh *GlobalHub) Close() {
	gh.mu.RLock()
	defer gh.mu.RUnlock()

	for _, hub := range gh.AppHubs {
		hub.Close()
	}
}
