package core

type WebhookEvent struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Event   string `json:"event,omitempty"` // For channel_occupied, channel_vacated
	UserID  string `json:"user_id,omitempty"` // For member_added, member_removed
}

type WebhookDispatcher interface {
	Dispatch(appID string, events []WebhookEvent)
}

// NoopWebhookDispatcher is a dummy dispatcher used for testing or when webhooks are disabled.
type NoopWebhookDispatcher struct{}

func (d *NoopWebhookDispatcher) Dispatch(appID string, events []WebhookEvent) {}
