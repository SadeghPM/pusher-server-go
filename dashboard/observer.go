package dashboard

import (
	"sync"
	"time"
)

// DebugEvent represents an event occurring within the Pusher server
// to be displayed on the admin dashboard.
type DebugEvent struct {
	AppID    string    `json:"app_id"`
	Type     string    `json:"type"`      // e.g., "connection", "disconnection", "subscription", "api_message", "client_event"
	SocketID string    `json:"socket_id"` // Optional
	Channel  string    `json:"channel"`   // Optional
	Event    string    `json:"event"`     // Optional, for api_message or client_event
	Data     string    `json:"data"`      // Optional, payload data
	Time     time.Time `json:"time"`
}

// Observer manages a list of channels where debug events are broadcasted.
type Observer struct {
	mu          sync.RWMutex
	subscribers map[chan DebugEvent]struct{}
}

// NewObserver creates a new Observer.
func NewObserver() *Observer {
	return &Observer{
		subscribers: make(map[chan DebugEvent]struct{}),
	}
}

// Notify broadcasts a DebugEvent to all subscribed channels.
func (o *Observer) Notify(event DebugEvent) {
	if event.Time.IsZero() {
		event.Time = time.Now()
	}

	o.mu.RLock()
	defer o.mu.RUnlock()
	for ch := range o.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber channel is full, drop the event to prevent blocking
		}
	}
}

// Subscribe returns a channel that will receive debug events.
func (o *Observer) Subscribe() chan DebugEvent {
	ch := make(chan DebugEvent, 100) // Buffer to handle bursts
	o.mu.Lock()
	defer o.mu.Unlock()
	o.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a previously subscribed channel.
func (o *Observer) Unsubscribe(ch chan DebugEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.subscribers[ch]; ok {
		delete(o.subscribers, ch)
		close(ch)
	}
}
