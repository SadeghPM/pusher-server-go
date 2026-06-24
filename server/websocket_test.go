package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"pusher-clone/config"
	"pusher-clone/core"
)

func setupTestServer() (*httptest.Server, *Server, string, string, string) {
	appID := "test-app"
	appKey := "test-key"
	appSecret := "test-secret"

	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     appID,
				AppKey:    appKey,
				AppSecret: appSecret,
			},
		},
	}
	globalHub := core.NewGlobalHub()
	server := NewServer(globalHub, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		pathAppKey := strings.TrimPrefix(r.URL.Path, "/app/")
		server.HandleWebSocket(w, r, pathAppKey)
	})

	ts := httptest.NewServer(mux)
	return ts, server, appID, appKey, appSecret
}

func connectWebSocket(ts *httptest.Server, appKey string) (*websocket.Conn, *http.Response, error) {
	return connectWebSocketWithHeaders(ts, appKey, nil)
}

func connectWebSocketWithHeaders(ts *httptest.Server, appKey string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/app/" + appKey
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	return dialer.Dial(wsURL, headers)
}

func TestConnectionEstablished(t *testing.T) {
	ts, _, _, appKey, _ := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	// Set deadline for reading
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Read first message (connection_established)
	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	var event PusherEvent
	err = json.Unmarshal(p, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if event.Event != "pusher:connection_established" {
		t.Errorf("Expected event 'pusher:connection_established', got '%s'", event.Event)
	}

	// Verify data contains socket_id
	var data map[string]interface{}
	err = json.Unmarshal([]byte(strings.Trim(string(event.Data), "\"")), &data)
	// Usually event.Data for connection_established is an escaped JSON string: "{\"socket_id\":\"...\"}"
	// Let's try unmarshaling it safely.
	var dataStr string
	err = json.Unmarshal(event.Data, &dataStr)
	if err == nil {
		err = json.Unmarshal([]byte(dataStr), &data)
	} else {
		err = json.Unmarshal(event.Data, &data)
	}

	if err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}

	if _, ok := data["socket_id"]; !ok {
		t.Errorf("Expected socket_id in data payload")
	}
}

func TestSubscribePublicChannel(t *testing.T) {
	ts, _, _, appKey, _ := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage() // Ignore connection_established
	if err != nil {
		t.Fatalf("Failed to read connection_established message: %v", err)
	}

	// Send subscribe event
	subEvent := `{"event":"pusher:subscribe","data":{"channel":"my-channel"}}`
	err = ws.WriteMessage(websocket.TextMessage, []byte(subEvent))
	if err != nil {
		t.Fatalf("Failed to write subscribe message: %v", err)
	}

	// Wait for subscription_succeeded event
	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded message: %v", err)
	}

	var event struct {
		Event   string          `json:"event"`
		Channel string          `json:"channel"`
		Data    json.RawMessage `json:"data"`
	}
	err = json.Unmarshal(p, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if event.Event != "pusher_internal:subscription_succeeded" {
		t.Errorf("Expected event 'pusher_internal:subscription_succeeded', got '%s'", event.Event)
	}

	if event.Channel != "my-channel" {
		t.Errorf("Expected channel 'my-channel', got '%s'", event.Channel)
	}
}

func TestSubscribePrivateChannelSuccess(t *testing.T) {
	ts, _, _, appKey, appSecret := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err := ws.ReadMessage() // Read connection_established
	if err != nil {
		t.Fatalf("Failed to read connection_established message: %v", err)
	}

	// Extract socket_id
	var connEvent PusherEvent
	json.Unmarshal(p, &connEvent)
	var dataStr string
	json.Unmarshal(connEvent.Data, &dataStr)
	var data map[string]interface{}
	json.Unmarshal([]byte(dataStr), &data)
	socketID := data["socket_id"].(string)

	channelName := "private-my-channel"
	toSign := fmt.Sprintf("%s:%s", socketID, channelName)

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(toSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	auth := fmt.Sprintf("%s:%s", appKey, signature)

	// Send subscribe event
	subEvent := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s"}}`, channelName, auth)
	err = ws.WriteMessage(websocket.TextMessage, []byte(subEvent))
	if err != nil {
		t.Fatalf("Failed to write subscribe message: %v", err)
	}

	// Wait for subscription_succeeded event
	_, p, err = ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded message: %v", err)
	}

	var event struct {
		Event   string `json:"event"`
		Channel string `json:"channel"`
	}
	json.Unmarshal(p, &event)

	if event.Event != "pusher_internal:subscription_succeeded" {
		t.Errorf("Expected event 'pusher_internal:subscription_succeeded', got '%s'", event.Event)
	}
	if event.Channel != channelName {
		t.Errorf("Expected channel '%s', got '%s'", channelName, event.Channel)
	}
}

func TestSubscribePrivateChannelInvalidSignature(t *testing.T) {
	ts, _, _, appKey, _ := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage() // Read connection_established
	if err != nil {
		t.Fatalf("Failed to read connection_established message: %v", err)
	}

	channelName := "private-my-channel"
	auth := fmt.Sprintf("%s:invalid-signature", appKey)

	// Send subscribe event
	subEvent := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s"}}`, channelName, auth)
	err = ws.WriteMessage(websocket.TextMessage, []byte(subEvent))
	if err != nil {
		t.Fatalf("Failed to write subscribe message: %v", err)
	}

	// Wait for error event
	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read error message: %v", err)
	}

	var event struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	json.Unmarshal(p, &event)

	if event.Event != "pusher:error" {
		t.Errorf("Expected event 'pusher:error', got '%s'", event.Event)
	}
}

func TestPingPong(t *testing.T) {
	ts, _, _, appKey, _ := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage() // Read connection_established
	if err != nil {
		t.Fatalf("Failed to read connection_established message: %v", err)
	}

	// Send ping event
	pingEvent := `{"event":"pusher:ping","data":{}}`
	err = ws.WriteMessage(websocket.TextMessage, []byte(pingEvent))
	if err != nil {
		t.Fatalf("Failed to write ping message: %v", err)
	}

	// Wait for pong event
	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read pong message: %v", err)
	}

	var event PusherEvent
	json.Unmarshal(p, &event)

	if event.Event != "pusher:pong" {
		t.Errorf("Expected event 'pusher:pong', got '%s'", event.Event)
	}
}

func TestInvalidJSON(t *testing.T) {
	ts, _, _, appKey, _ := setupTestServer()
	defer ts.Close()

	ws, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage() // Read connection_established
	if err != nil {
		t.Fatalf("Failed to read connection_established message: %v", err)
	}

	// Send invalid JSON event
	invalidJSON := `{"event":"pusher:subscribe", data:{"channel":"my-channel"}}` // missing quotes around data
	err = ws.WriteMessage(websocket.TextMessage, []byte(invalidJSON))
	if err != nil {
		t.Fatalf("Failed to write invalid JSON message: %v", err)
	}

	// Wait a bit to ensure it doesn't crash
	time.Sleep(100 * time.Millisecond)

	// Still responsive to ping
	pingEvent := `{"event":"pusher:ping","data":{}}`
	err = ws.WriteMessage(websocket.TextMessage, []byte(pingEvent))
	if err != nil {
		t.Fatalf("Failed to write ping message: %v", err)
	}

	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read pong message after invalid JSON: %v", err)
	}

	var event PusherEvent
	json.Unmarshal(p, &event)
	if event.Event != "pusher:pong" {
		t.Errorf("Expected event 'pusher:pong', got '%s'", event.Event)
	}
}

func TestAppNotFound(t *testing.T) {
	ts, _, _, _, _ := setupTestServer()
	defer ts.Close()

	_, resp, err := connectWebSocket(ts, "invalid-app-key")
	if err == nil {
		t.Fatalf("Expected connection to fail for invalid app key")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}

func TestCheckOriginAllowedOrigins(t *testing.T) {
	appID := "test-origin-app"
	appKey := "test-origin-key"
	appSecret := "test-origin-secret"

	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:          appID,
				AppKey:         appKey,
				AppSecret:      appSecret,
				AllowedOrigins: []string{"allowed.com", "*.wildcard.com"},
			},
		},
	}
	globalHub := core.NewGlobalHub()
	server := NewServer(globalHub, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		pathAppKey := strings.TrimPrefix(r.URL.Path, "/app/")
		server.HandleWebSocket(w, r, pathAppKey)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	tests := []struct {
		name       string
		origin     string
		shouldFail bool
	}{
		{"No Origin", "", false},
		{"Exact Match", "http://allowed.com", false},
		{"Wildcard Match", "https://sub.wildcard.com", false},
		{"Wildcard Deep Match", "https://a.b.wildcard.com", false},
		{"Blocked Origin", "http://evil.com", true},
		{"Prefix Trick Blocked", "http://allowed.com.evil.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			if tt.origin != "" {
				headers.Set("Origin", tt.origin)
			}

			ws, resp, err := connectWebSocketWithHeaders(ts, appKey, headers)

			if tt.shouldFail {
				if err == nil {
					ws.Close()
					t.Fatalf("Expected connection to fail for origin %s", tt.origin)
				}
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("Expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
				}
			} else {
				if err != nil {
					t.Fatalf("Expected connection to succeed for origin %s, got error: %v", tt.origin, err)
				}
				ws.Close()
			}
		})
	}
}

func TestCheckOriginDefaultSameOrigin(t *testing.T) {
	appID := "test-origin-app"
	appKey := "test-origin-key"
	appSecret := "test-origin-secret"

	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:          appID,
				AppKey:         appKey,
				AppSecret:      appSecret,
				AllowedOrigins: []string{}, // Empty means same-origin fallback
			},
		},
	}
	globalHub := core.NewGlobalHub()
	server := NewServer(globalHub, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		pathAppKey := strings.TrimPrefix(r.URL.Path, "/app/")
		server.HandleWebSocket(w, r, pathAppKey)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Extract the host part from ts.URL (e.g., "127.0.0.1:something")
	hostUrl, _ := url.Parse(ts.URL)
	sameOrigin := "http://" + hostUrl.Host

	tests := []struct {
		name       string
		origin     string
		shouldFail bool
	}{
		{"No Origin", "", false},
		{"Same Origin", sameOrigin, false},
		{"Cross Origin Blocked", "http://evil.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			if tt.origin != "" {
				headers.Set("Origin", tt.origin)
			}

			ws, resp, err := connectWebSocketWithHeaders(ts, appKey, headers)

			if tt.shouldFail {
				if err == nil {
					ws.Close()
					t.Fatalf("Expected connection to fail for origin %s", tt.origin)
				}
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("Expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
				}
			} else {
				if err != nil {
					t.Fatalf("Expected connection to succeed for origin %s, got error: %v", tt.origin, err)
				}
				ws.Close()
			}
		})
	}
}
