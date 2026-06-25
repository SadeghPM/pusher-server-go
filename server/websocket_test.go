package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/app/" + appKey
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	return dialer.Dial(wsURL, nil)
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

func TestSubscribePrivateChannelMissingAuth(t *testing.T) {
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

	// Send subscribe event WITHOUT auth
	subEvent := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s"}}`, channelName)
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

func TestClientEventBroadcastPrivateChannel(t *testing.T) {
	ts, _, _, appKey, appSecret := setupTestServer()
	defer ts.Close()

	// Client 1
	ws1, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect ws1: %v", err)
	}
	defer ws1.Close()

	ws1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p1, err := ws1.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read connection_established ws1: %v", err)
	}

	var connEvent1 PusherEvent
	json.Unmarshal(p1, &connEvent1)
	var dataStr1 string
	json.Unmarshal(connEvent1.Data, &dataStr1)
	var data1 map[string]interface{}
	json.Unmarshal([]byte(dataStr1), &data1)
	socketID1 := data1["socket_id"].(string)

	// Client 2
	ws2, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect ws2: %v", err)
	}
	defer ws2.Close()

	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p2, err := ws2.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read connection_established ws2: %v", err)
	}

	var connEvent2 PusherEvent
	json.Unmarshal(p2, &connEvent2)
	var dataStr2 string
	json.Unmarshal(connEvent2.Data, &dataStr2)
	var data2 map[string]interface{}
	json.Unmarshal([]byte(dataStr2), &data2)
	socketID2 := data2["socket_id"].(string)

	channelName := "private-client-test"

	// Subscribe ws1
	auth1 := generateTestAuth(appKey, appSecret, socketID1, channelName)
	subEvent1 := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s"}}`, channelName, auth1)
	ws1.WriteMessage(websocket.TextMessage, []byte(subEvent1))
	_, _, err = ws1.ReadMessage() // subscription_succeeded
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded ws1: %v", err)
	}

	// Subscribe ws2
	auth2 := generateTestAuth(appKey, appSecret, socketID2, channelName)
	subEvent2 := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s"}}`, channelName, auth2)
	ws2.WriteMessage(websocket.TextMessage, []byte(subEvent2))
	_, _, err = ws2.ReadMessage() // subscription_succeeded
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded ws2: %v", err)
	}

	// Client 1 sends a client event (double encoded)
	clientEvent := fmt.Sprintf(`{"event":"client-typing","channel":"%s","data":"{\"user\":\"john\"}"}`, channelName)
	err = ws1.WriteMessage(websocket.TextMessage, []byte(clientEvent))
	if err != nil {
		t.Fatalf("Failed to write client event: %v", err)
	}

	// Client 2 should receive the client event
	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, pReceived, err := ws2.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read client event on ws2: %v", err)
	}

	var receivedEvent PusherEvent
	json.Unmarshal(pReceived, &receivedEvent)

	if receivedEvent.Event != "client-typing" {
		t.Errorf("Expected event 'client-typing', got '%s'", receivedEvent.Event)
	}

	if receivedEvent.Channel != channelName {
		t.Errorf("Expected channel '%s', got '%s'", channelName, receivedEvent.Channel)
	}

	// Check if data is double encoded properly
	var receivedDataStr string
	err = json.Unmarshal(receivedEvent.Data, &receivedDataStr)
	if err != nil {
		t.Fatalf("Failed to unmarshal received data string: %v", err)
	}

	if receivedDataStr != `{"user":"john"}` {
		t.Errorf("Expected data '{\"user\":\"john\"}', got '%s'", receivedDataStr)
	}
}

func TestClientEventBroadcastPresenceChannel(t *testing.T) {
	ts, _, _, appKey, appSecret := setupTestServer()
	defer ts.Close()

	// Setup Client 1 (john)
	ws1, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect ws1: %v", err)
	}
	defer ws1.Close()

	ws1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p1, _ := ws1.ReadMessage()

	var connEvent1 PusherEvent
	json.Unmarshal(p1, &connEvent1)
	var dataStr1 string
	json.Unmarshal(connEvent1.Data, &dataStr1)
	var data1 map[string]interface{}
	json.Unmarshal([]byte(dataStr1), &data1)
	socketID1 := data1["socket_id"].(string)

	// Setup Client 2 (jane)
	ws2, _, err := connectWebSocket(ts, appKey)
	if err != nil {
		t.Fatalf("Failed to connect ws2: %v", err)
	}
	defer ws2.Close()

	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p2, _ := ws2.ReadMessage()

	var connEvent2 PusherEvent
	json.Unmarshal(p2, &connEvent2)
	var dataStr2 string
	json.Unmarshal(connEvent2.Data, &dataStr2)
	var data2 map[string]interface{}
	json.Unmarshal([]byte(dataStr2), &data2)
	socketID2 := data2["socket_id"].(string)

	channelName := "presence-client-test"

	// Subscribe ws1
	channelData1 := `{"user_id":"john_id","user_info":{"name":"John"}}`
	auth1 := generateTestPresenceAuth(appKey, appSecret, socketID1, channelName, channelData1)
	subEvent1 := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s","channel_data":"%s"}}`, channelName, auth1, strings.ReplaceAll(channelData1, `"`, `\"`))
	ws1.WriteMessage(websocket.TextMessage, []byte(subEvent1))
	_, _, err = ws1.ReadMessage() // subscription_succeeded
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded ws1: %v", err)
	}

	// Subscribe ws2
	channelData2 := `{"user_id":"jane_id","user_info":{"name":"Jane"}}`
	auth2 := generateTestPresenceAuth(appKey, appSecret, socketID2, channelName, channelData2)
	subEvent2 := fmt.Sprintf(`{"event":"pusher:subscribe","data":{"channel":"%s","auth":"%s","channel_data":"%s"}}`, channelName, auth2, strings.ReplaceAll(channelData2, `"`, `\"`))
	ws2.WriteMessage(websocket.TextMessage, []byte(subEvent2))

	// ws2 receives subscription_succeeded
	_, _, err = ws2.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read subscription_succeeded ws2: %v", err)
	}

	// ws1 should receive member_added
	_, _, err = ws1.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read member_added ws1: %v", err)
	}

	// Client 1 (john) sends a client event (double encoded)
	clientEvent := fmt.Sprintf(`{"event":"client-ping","channel":"%s","data":"{\"text\":\"ping\"}"}`, channelName)
	err = ws1.WriteMessage(websocket.TextMessage, []byte(clientEvent))
	if err != nil {
		t.Fatalf("Failed to write client event: %v", err)
	}

	// Client 2 (jane) should receive the client event
	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, pReceived, err := ws2.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read client event on ws2: %v", err)
	}

	var receivedEvent struct {
		Event   string          `json:"event"`
		Channel string          `json:"channel"`
		UserID  string          `json:"user_id"`
		Data    json.RawMessage `json:"data"`
	}
	json.Unmarshal(pReceived, &receivedEvent)

	if receivedEvent.Event != "client-ping" {
		t.Errorf("Expected event 'client-ping', got '%s'", receivedEvent.Event)
	}

	if receivedEvent.Channel != channelName {
		t.Errorf("Expected channel '%s', got '%s'", channelName, receivedEvent.Channel)
	}

	// The user_id should be appended to the event by the server
	if receivedEvent.UserID != "john_id" {
		t.Errorf("Expected user_id 'john_id', got '%s'", receivedEvent.UserID)
	}

	// Check if data is double encoded properly
	var receivedDataStr string
	err = json.Unmarshal(receivedEvent.Data, &receivedDataStr)
	if err != nil {
		t.Fatalf("Failed to unmarshal received data string: %v", err)
	}

	if receivedDataStr != `{"text":"ping"}` {
		t.Errorf("Expected data '{\"text\":\"ping\"}', got '%s'", receivedDataStr)
	}
}

func generateTestAuth(appKey, appSecret, socketID, channel string) string {
	toSign := fmt.Sprintf("%s:%s", socketID, channel)
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(toSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s:%s", appKey, signature)
}

func generateTestPresenceAuth(appKey, appSecret, socketID, channel, channelData string) string {
	toSign := fmt.Sprintf("%s:%s:%s", socketID, channel, channelData)
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(toSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s:%s", appKey, signature)
}
