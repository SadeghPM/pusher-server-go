package core

// DebugNotifier sends debug events to observers (e.g. dashboard).
type DebugNotifier interface {
	Notify(appID, eventType, socketID, channel, event, data string)
}

// NoopDebugNotifier is used when no debug observer is configured.
type NoopDebugNotifier struct{}

func (NoopDebugNotifier) Notify(appID, eventType, socketID, channel, event, data string) {}

// Compile-time interface check.
var _ DebugNotifier = NoopDebugNotifier{}
