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
	GlobalHub *core.GlobalHub
	Config    *config.Config
}

func NewServer(globalHub *core.GlobalHub, cfg *config.Config) *Server {
	return &Server{
		GlobalHub: globalHub,
		Config:    cfg,
	}
}

// Handler generation per AppKey is not strictly necessary if we parse it from the URL
// But we registered explicitly in main.go before. We will extract appKey from the path now in main.go
// so we need a unified handler that takes the appKey.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request, appKey string) {
	// Find the matching AppConfig
	var appCfg *config.AppConfig
	for _, app := range s.Config.Apps {
		if app.AppKey == appKey {
			appCfg = &app
			break
		}
	}

	if appCfg == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	appHub := s.GlobalHub.GetOrCreateAppHub(appCfg.AppID)
	socketID := generateSocketID()

	client := core.NewClient(appHub, conn, socketID)
	appHub.RegisterClient(client)

	// Send connection established event
	establishedPayload := fmt.Sprintf(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"%s\",\"activity_timeout\":120}"}`, socketID)
	client.Send <- []byte(establishedPayload)

	go s.writePump(client)
	go s.readPump(client, appCfg.AppSecret, appCfg.AppKey)
}

func generateSocketID() string {
	return fmt.Sprintf("%d.%d", time.Now().Unix(), time.Now().UnixNano()%100000)
}

func (s *Server) readPump(client *core.Client, appSecret, appKey string) {
	defer func() {
		client.AppHub.UnregisterClient(client)
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

		s.handleMessage(client, message, appSecret, appKey)
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

func (s *Server) handleMessage(client *core.Client, message []byte, appSecret, appKey string) {
	var event PusherEvent
	if err := json.Unmarshal(message, &event); err != nil {
		log.Println("Invalid JSON received:", err)
		return
	}

	switch event.Event {
	case "pusher:ping":
		client.Send <- []byte(`{"event":"pusher:pong","data":"{}"}`)

	case "pusher:subscribe":
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
				expectedSig := generateSignature(appSecret, toSign)

				// Auth string format is app_key:signature
				authParts := strings.Split(subData.Auth, ":")
				if len(authParts) != 2 || authParts[0] != appKey || authParts[1] != expectedSig {
					log.Printf("Invalid signature for channel %s. Expected %s, got %s", subData.Channel, expectedSig, subData.Auth)

					errorPayload := fmt.Sprintf(`{"event":"pusher:error","data":"{\"message\":\"Invalid signature: Expected HMAC SHA256 hex digest of %s:%s, but got %s\",\"code\":null}"}`, client.SocketID, subData.Channel, subData.Auth)
					client.Send <- []byte(errorPayload)
					return
				}
			}

			client.AppHub.Subscribe(client, subData.Channel)

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
