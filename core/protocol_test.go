package core

import (
	"encoding/json"
	"testing"
)

func TestMarshalEventEscapesDynamicFields(t *testing.T) {
	frame := MarshalEvent(
		`client-"quoted"`,
		`private-"channel"`,
		json.RawMessage(`"{\"ok\":true}"`),
		`user-"one"`,
	)

	if !json.Valid(frame) {
		t.Fatalf("MarshalEvent produced invalid JSON: %s", frame)
	}

	var event Event
	if err := json.Unmarshal(frame, &event); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if event.Event != `client-"quoted"` {
		t.Errorf("Unexpected event name: %q", event.Event)
	}
	if event.Channel != `private-"channel"` {
		t.Errorf("Unexpected channel: %q", event.Channel)
	}
	if event.UserID != `user-"one"` {
		t.Errorf("Unexpected user id: %q", event.UserID)
	}
}

func TestMarshalEventReplacesInvalidData(t *testing.T) {
	frame := MarshalEvent("my-event", "my-channel", json.RawMessage(`not-json`), "")
	if !json.Valid(frame) {
		t.Fatalf("MarshalEvent produced invalid JSON: %s", frame)
	}

	var event Event
	if err := json.Unmarshal(frame, &event); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}
	if string(event.Data) != `"{}"` {
		t.Errorf("Expected fallback data %q, got %q", `"{}"`, string(event.Data))
	}
}

func TestEncodeDataStringProducesJSONString(t *testing.T) {
	data := EncodeDataString(map[string]string{"socket_id": "123.456"})

	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Expected a JSON string, got %s: %v", data, err)
	}
	if decoded != `{"socket_id":"123.456"}` {
		t.Errorf("Unexpected decoded data: %q", decoded)
	}
}
