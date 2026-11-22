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

	workMu    sync.RWMutex
	workQueue chan func()
	closed    bool
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

// Close shuts down the work queue and closes each client connection. The
// clients' read pumps then unregister themselves, which keeps channel and
// connection metrics consistent. It is safe to call more than once.
func (h *AppHub) Close() {
	h.workMu.Lock()
	if h.closed {
		h.workMu.Unlock()
		return
	}
	h.closed = true
	close(h.workQueue)
	h.workMu.Unlock()

	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for _, client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	// Close the underlying connections rather than Send channels: the read
	// pump may still be handling a message and writing to Send.
	for _, client := range clients {
		if client.Conn != nil {
			client.Conn.Close()
		}
	}
}

// AppID returns the application identifier for this hub.
func (h *AppHub) AppID() string {
	return h.appID
}

// enqueue sends work to the bounded worker pool. Drops if full or if the hub
// has been closed. The read lock prevents a send on a closed work queue.
func (h *AppHub) enqueue(fn func()) {
	h.workMu.RLock()
	defer h.workMu.RUnlock()

	if h.closed {
		return
	}

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
		client.CloseSend()
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
	if client.channelCount > 0 {
		client.channelCount--
	}

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
			payload := MarshalEvent(
				"pusher_internal:member_removed",
				channel,
				EncodeDataString(map[string]string{"user_id": member.UserID}),
				"",
			)
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

// Subscribe adds a client to a channel. It is a convenience wrapper around
// TrySubscribe; callers that need to know whether a presence subscription was
// accepted must call TrySubscribe directly.
func (h *AppHub) Subscribe(client *Client, channel string, member *ChannelMember) (isNewUser, accepted bool) {
	return h.TrySubscribe(client, channel, member)
}

// TrySubscribe adds a client to a channel under the hub lock. Presence
// channels refuse new users once MaxPresenceMembersPerChannel distinct users
// are present, so concurrent subscriptions cannot race past the cap. Existing
// users (including another tab for the same user) are always allowed.
// It returns whether this is a new presence user and whether the subscription
// was accepted.
func (h *AppHub) TrySubscribe(client *Client, channel string, member *ChannelMember) (isNewUser, accepted bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subscribers := h.channels[channel]
	existing, alreadySubscribed := subscribers[client]

	if member != nil {
		// Count distinct user IDs, not connections: many tabs for one user
		// must not consume the channel's member budget.
		users := make(map[string]struct{}, len(subscribers))
		for existingClient, existingMember := range subscribers {
			if existingClient == client || existingMember == nil {
				continue
			}
			users[existingMember.UserID] = struct{}{}
		}

		_, userExists := users[member.UserID]
		if !userExists && len(users) >= MaxPresenceMembersPerChannel {
			return false, false
		}
		isNewUser = !userExists
		// Re-subscribing the same client with the same user ID is not a new
		// presence member, even when no other connection shares that ID.
		if alreadySubscribed && existing != nil && existing.UserID == member.UserID {
			isNewUser = false
		}
	}

	if subscribers == nil {
		h.channels[channel] = make(map[*Client]*ChannelMember)
		metrics.ChannelsActive.WithLabelValues(h.appID).Inc()
		h.DispatchWebhook([]WebhookEvent{
			{
				Name:    "channel_occupied",
				Channel: channel,
			},
		})
	}

	if !alreadySubscribed {
		client.channelCount++
	}
	h.channels[channel][client] = member
	return isNewUser, true
}

func (h *AppHub) RLockChannels(cb func(map[string]map[*Client]*ChannelMember)) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cb(h.channels)
}

// SubscriptionInfo returns how many channels a client is subscribed to and
// whether it is already subscribed to channel. The count is maintained under
// the hub lock, so this is O(1) rather than a scan of every channel.
func (h *AppHub) SubscriptionInfo(client *Client, channel string) (count int, subscribed bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	subscribers := h.channels[channel]
	_, subscribed = subscribers[client]
	return client.channelCount, subscribed
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
				client.TrySend(message)
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

// RemoveAppHub disconnects all clients and removes the hub for an app. It is
// used when a tenant is deleted so a recreated app cannot inherit stale
// connections or goroutines.
func (gh *GlobalHub) RemoveAppHub(appID string) {
	gh.mu.Lock()
	hub := gh.AppHubs[appID]
	delete(gh.AppHubs, appID)
	gh.mu.Unlock()

	if hub != nil {
		hub.Close()
	}
}

// Close shuts down all AppHub work queues. Call on graceful shutdown.
func (gh *GlobalHub) Close() {
	gh.mu.RLock()
	defer gh.mu.RUnlock()

	for _, hub := range gh.AppHubs {
		hub.Close()
	}
}
