package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"pusher-clone/config"
	"pusher-clone/core"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for the clone
	},
}

// PusherEvent standard protocol event wrapper
type PusherEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"` // Kept as raw so we can extract JSON payload or string
}

// PusherSubscribeData payload for subscribe event
type PusherSubscribeData struct {
	Channel string `json:"channel"`
	Auth    string `json:"auth,omitempty"`
}

type Server struct {
	Hub    *core.Hub
	Config *config.Config
}

func NewServer(hub *core.Hub, cfg *config.Config) *Server {
	return &Server{
		Hub:    hub,
		Config: cfg,
	}
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	// Generate socket ID. Pusher format usually looks like: 12345.67890
	// We'll use a modified UUID or random string.
	// We will use standard UUID for now. Wait, Pusher spec requires specific format for socket_id to pass validation in Laravel
	// Socket id is string, typically float-like: "12345.67890"
	socketID := generateSocketID()

	client := core.NewClient(s.Hub, conn, socketID)
	s.Hub.RegisterClient(client)

	// Send connection established event
	establishedPayload := fmt.Sprintf(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"%s\",\"activity_timeout\":120}"}`, socketID)
	client.Send <- []byte(establishedPayload)

	go s.writePump(client)
	go s.readPump(client)
}

func generateSocketID() string {
	return fmt.Sprintf("%d.%d", time.Now().Unix(), time.Now().UnixNano()%100000)
}

func (s *Server) readPump(client *core.Client) {
	defer func() {
		s.Hub.UnregisterClient(client)
		client.Conn.Close()
	}()

	client.Conn.SetReadLimit(8192)
	client.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	client.Conn.SetPongHandler(func(string) error { client.Conn.SetReadDeadline(time.Now().Add(120 * time.Second)); return nil })

	for {
		_, message, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		s.handleMessage(client, message)
	}
}

func (s *Server) writePump(client *core.Client) {
	ticker := time.NewTicker(60 * time.Second) // Ping interval
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(client.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-client.Send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleMessage(client *core.Client, message []byte) {
	var event PusherEvent
	if err := json.Unmarshal(message, &event); err != nil {
		log.Println("Invalid JSON received:", err)
		return
	}

	switch event.Event {
	case "pusher:ping":
		client.Send <- []byte(`{"event":"pusher:pong","data":"{}"}`)

	case "pusher:subscribe":
		// Pusher typically sends data as a stringified JSON inside the "data" field in client events,
		// but sometimes as direct object depending on the client library version.
		// Let's handle both.
		var subData PusherSubscribeData

		// Try unmarshaling string first
		var dataStr string
		err := json.Unmarshal(event.Data, &dataStr)
		if err == nil {
			json.Unmarshal([]byte(dataStr), &subData)
		} else {
			// If not a string, try direct object
			json.Unmarshal(event.Data, &subData)
		}

		if subData.Channel != "" {
			if strings.HasPrefix(subData.Channel, "private-") {
				// Verify signature
				// Format: socket_id:channel_name
				toSign := fmt.Sprintf("%s:%s", client.SocketID, subData.Channel)
				expectedSig := generateSignature(s.Config.AppSecret, toSign)

				// Auth string format is app_key:signature
				authParts := strings.Split(subData.Auth, ":")
				if len(authParts) != 2 || authParts[0] != s.Config.AppKey || authParts[1] != expectedSig {
					log.Printf("Invalid signature for channel %s. Expected %s, got %s", subData.Channel, expectedSig, subData.Auth)

					errorPayload := fmt.Sprintf(`{"event":"pusher:error","data":"{\"message\":\"Invalid signature: Expected HMAC SHA256 hex digest of %s:%s, but got %s\",\"code\":null}"}`, client.SocketID, subData.Channel, subData.Auth)
					client.Send <- []byte(errorPayload)
					return
				}
			}

			s.Hub.Subscribe(client, subData.Channel)

			// Confirm subscription
			successPayload := fmt.Sprintf(`{"event":"pusher_internal:subscription_succeeded","channel":"%s","data":"{}"}`, subData.Channel)
			client.Send <- []byte(successPayload)
		}
	}
}

func generateSignature(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
