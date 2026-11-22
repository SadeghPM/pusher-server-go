package core

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

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

	// registerMu serializes client registration with shutdown. Once closing is
	// set, no new client may join the hub.
	registerMu sync.Mutex
	closing    bool
	clientsWG  sync.WaitGroup
	closeOnce  sync.Once

	// workMu guards an unbounded work queue. A single worker consumes it so
	// membership broadcasts (member_added/member_removed) are delivered in the
	// order they were enqueued. The queue never drops work: webhooks and
	// cleanup broadcasts must survive bursts.
	workMu      sync.Mutex
	workCond    *sync.Cond
	workQueue   []func()
	queueClosed bool
	workers     sync.WaitGroup
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
	}
	h.workCond = sync.NewCond(&h.workMu)
	h.workers.Add(1)
	go h.worker()
	return h
}

// worker drains the ordered work queue. It exits only after the queue has
// been closed and every remaining task has run.
func (h *AppHub) worker() {
	defer h.workers.Done()
	for {
		h.workMu.Lock()
		for len(h.workQueue) == 0 && !h.queueClosed {
			h.workCond.Wait()
		}
		if len(h.workQueue) == 0 {
			h.workMu.Unlock()
			return
		}
		fn := h.workQueue[0]
		h.workQueue[0] = nil
		h.workQueue = h.workQueue[1:]
		if len(h.workQueue) == 0 {
			h.workQueue = nil
		}
		h.workMu.Unlock()
		fn()
	}
}

// Close disconnects every client and shuts down the work queue. Connections
// are closed first so each read pump unregisters while the queue is still
// open; otherwise the resulting member_removed/channel_vacated webhooks would
// be dropped at shutdown. It is safe to call more than once.
func (h *AppHub) Close() {
	h.closeOnce.Do(func() {
		h.registerMu.Lock()
		h.closing = true
		h.registerMu.Unlock()

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
			} else {
				// Clients without a connection are only used in tests; unregister
				// them directly so shutdown does not wait for a read pump.
				h.UnregisterClient(client)
			}
		}

		// Give the read pumps a bounded amount of time to unregister and
		// enqueue their cleanup work.
		done := make(chan struct{})
		go func() {
			h.clientsWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			slog.Warn("Timed out waiting for clients to unregister during hub shutdown",
				"app_id", h.appID,
				"clients", len(clients),
			)
		}

		// All cleanup work is now enqueued. Close the queue and let the worker
		// drain whatever remains before returning.
		h.workMu.Lock()
		h.queueClosed = true
		h.workCond.Broadcast()
		h.workMu.Unlock()
		h.workers.Wait()
	})
}

// AppID returns the application identifier for this hub.
func (h *AppHub) AppID() string {
	return h.appID
}

// enqueue appends work to the ordered queue. It never drops a task while the
// hub is open. After shutdown it is a no-op, matching the old closed-channel
// behavior for stray goroutines.
func (h *AppHub) enqueue(fn func()) {
	h.workMu.Lock()
	defer h.workMu.Unlock()

	if h.queueClosed {
		return
	}
	h.workQueue = append(h.workQueue, fn)
	h.workCond.Signal()
}

// DispatchWebhook dispatches webhook events asynchronously via the work queue.
func (h *AppHub) DispatchWebhook(events []WebhookEvent) {
	if len(events) == 0 {
		return
	}
	h.enqueue(func() {
		h.dispatcher.Dispatch(h.appID, events)
	})
}

// RegisterClient adds client to the hub. It returns silently, closing the
// connection, when the hub is already shutting down.
func (h *AppHub) RegisterClient(client *Client) {
	h.registerMu.Lock()
	if h.closing {
		h.registerMu.Unlock()
		client.CloseSend()
		if client.Conn != nil {
			client.Conn.Close()
		}
		return
	}

	h.mu.Lock()
	if _, exists := h.clients[client.SocketID]; exists {
		h.mu.Unlock()
		h.registerMu.Unlock()
		// The caller already started (or is about to start) this connection's
		// pumps. Close its send channel so the write pump cannot leak waiting
		// for messages from a client that was never registered.
		client.CloseSend()
		if client.Conn != nil {
			client.Conn.Close()
		}
		return
	}
	h.clients[client.SocketID] = client
	metrics.ActiveConnections.WithLabelValues(h.appID).Inc()
	h.mu.Unlock()
	h.clientsWG.Add(1)
	h.registerMu.Unlock()
}

func (h *AppHub) UnregisterClient(client *Client) {
	h.mu.Lock()

	existing, ok := h.clients[client.SocketID]
	if !ok || existing != client {
		h.mu.Unlock()
		return
	}
	delete(h.clients, client.SocketID)
	metrics.ActiveConnections.WithLabelValues(h.appID).Dec()

	// Remove from all channels. The hub lock stays held so the cleanup
	// broadcasts and webhooks are enqueued before clientsWG is released.
	var channelsToRemove []string
	for channelName, subscribers := range h.channels {
		if _, ok := subscribers[client]; ok {
			channelsToRemove = append(channelsToRemove, channelName)
		}
	}

	for _, channelName := range channelsToRemove {
		h.removeClientFromChannel(client, channelName)
	}
	h.mu.Unlock()

	client.CloseSend()
	h.clientsWG.Done()
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
			h.BroadcastToChannelAsync(channel, payload, "")

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
	return h.trySubscribe(client, channel, member, nil)
}

// TrySubscribeWithAck behaves like TrySubscribe but invokes ack while the hub
// lock is still held, immediately after the client is added. That guarantees
// the subscription_succeeded frame is queued before any broadcast to the
// channel can see the new subscriber.
func (h *AppHub) TrySubscribeWithAck(client *Client, channel string, member *ChannelMember, ack func(members map[string]json.RawMessage)) (isNewUser, accepted bool) {
	return h.trySubscribe(client, channel, member, ack)
}

func (h *AppHub) trySubscribe(client *Client, channel string, member *ChannelMember, ack func(members map[string]json.RawMessage)) (isNewUser, accepted bool) {
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

	if ack != nil {
		ack(h.presenceMembersLocked(channel))
	}
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

	return h.presenceMembersLocked(channel)
}

// presenceMembersLocked returns a snapshot of the presence members on a
// channel. It must be called with h.mu held.
func (h *AppHub) presenceMembersLocked(channel string) map[string]json.RawMessage {
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

// BroadcastToChannelAsync queues a channel broadcast on the ordered work
// queue. Membership events use this so member_added and member_removed are
// delivered in the same order they occurred.
func (h *AppHub) BroadcastToChannelAsync(channel string, message []byte, excludeSocketID string) {
	h.enqueue(func() {
		h.BroadcastToChannel(channel, message, excludeSocketID)
	})
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

// Close shuts down all AppHubs. Call on graceful shutdown.
func (gh *GlobalHub) Close() {
	gh.mu.RLock()
	hubs := make([]*AppHub, 0, len(gh.AppHubs))
	for _, hub := range gh.AppHubs {
		hubs = append(hubs, hub)
	}
	gh.mu.RUnlock()

	for _, hub := range hubs {
		hub.Close()
	}
}
