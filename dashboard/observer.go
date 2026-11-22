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

// Observer manages per-app channels where debug events are broadcast. Keeping
// subscribers separate per tenant means a busy app cannot evict the events of
// the app a dashboard is actually watching.
type Observer struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan DebugEvent]struct{}
}

// NewObserver creates a new Observer.
func NewObserver() *Observer {
	return &Observer{
		subscribers: make(map[string]map[chan DebugEvent]struct{}),
	}
}

// Notify broadcasts a DebugEvent to subscribers watching that app.
func (o *Observer) Notify(event DebugEvent) {
	if event.Time.IsZero() {
		event.Time = time.Now()
	}

	o.mu.RLock()
	defer o.mu.RUnlock()
	for ch := range o.subscribers[event.AppID] {
		select {
		case ch <- event:
		default:
			// Subscriber channel is full, drop the event to prevent blocking.
			// The buffer now only contains events for this app, so a noisy
			// neighbour can no longer starve it.
		}
	}
}

// Subscribe returns a channel that receives debug events for appID.
func (o *Observer) Subscribe(appID string) chan DebugEvent {
	ch := make(chan DebugEvent, 100) // Buffer to handle bursts
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.subscribers[appID] == nil {
		o.subscribers[appID] = make(map[chan DebugEvent]struct{})
	}
	o.subscribers[appID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a previously subscribed channel.
func (o *Observer) Unsubscribe(appID string, ch chan DebugEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	subscribers := o.subscribers[appID]
	if _, ok := subscribers[ch]; ok {
		delete(subscribers, ch)
		close(ch)
		if len(subscribers) == 0 {
			delete(o.subscribers, appID)
		}
	}
}
