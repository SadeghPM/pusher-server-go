package core

import "encoding/json"

// Event is a Pusher protocol event frame. Data is kept as raw JSON so callers
// can either preserve a client-provided payload or send a JSON-encoded string,
// which is the convention used by the Pusher protocol.
type Event struct {
	Event   string          `json:"event"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data"`
	UserID  string          `json:"user_id,omitempty"`
}

// MarshalEvent serializes a Pusher event. Invalid or missing data is replaced
// with an empty JSON string so a bad payload can never produce an invalid
// frame or be used for JSON injection through channel or event names.
func MarshalEvent(event, channel string, data json.RawMessage, userID string) []byte {
	if len(data) == 0 || !json.Valid(data) {
		data = json.RawMessage(`"{}"`)
	}

	frame, err := json.Marshal(Event{
		Event:   event,
		Channel: channel,
		Data:    data,
		UserID:  userID,
	})
	if err != nil {
		// Every field is a string or already-valid raw JSON, so this should be
		// unreachable. Keep the fallback valid so callers can always send it.
		return []byte(`{"event":"pusher:error","data":"{}"}`)
	}
	return frame
}

// EncodeData returns v as raw JSON.
func EncodeData(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}

// EncodeDataString JSON-encodes v and then wraps the result in a JSON string,
// matching Pusher's convention that event data is a serialized JSON string.
func EncodeDataString(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`"{}"`)
	}

	encoded, err := json.Marshal(string(data))
	if err != nil {
		return json.RawMessage(`"{}"`)
	}
	return encoded
}
