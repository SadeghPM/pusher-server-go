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
	"sync/atomic"
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
	Event   string          `json:"event"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data"` // Kept as raw so we can extract JSON payload or string
}

// PusherSubscribeData payload for subscribe event
type PusherSubscribeData struct {
	Channel     string `json:"channel"`
	Auth        string `json:"auth,omitempty"`
	ChannelData string `json:"channel_data,omitempty"`
}

// PusherUnsubscribeData payload for unsubscribe event
type PusherUnsubscribeData struct {
	Channel string `json:"channel"`
}

// ChannelData represents the decoded channel_data for presence channels
type ChannelData struct {
	UserID   string          `json:"user_id"`
	UserInfo json.RawMessage `json:"user_info,omitempty"`
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

var socketIDCounter atomic.Uint64

func generateSocketID() string {
	counter := socketIDCounter.Add(1)
	return fmt.Sprintf("%d.%d", time.Now().Unix(), counter)
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

			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

			// Send queued chat messages as separate websocket messages.
			n := len(client.Send)
			for i := 0; i < n; i++ {
				if err := client.Conn.WriteMessage(websocket.TextMessage, <-client.Send); err != nil {
					return
				}
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
			isPrivate := strings.HasPrefix(subData.Channel, "private-")
			isPresence := strings.HasPrefix(subData.Channel, "presence-")

			var member *core.ChannelMember
			var presenceData ChannelData

			if isPrivate || isPresence {
				// Verify signature
				// Format: socket_id:channel_name
				toSign := fmt.Sprintf("%s:%s", client.SocketID, subData.Channel)
				if isPresence {
					toSign = fmt.Sprintf("%s:%s:%s", client.SocketID, subData.Channel, subData.ChannelData)
				}
				expectedSig := generateSignature(appSecret, toSign)

				// Auth string format is app_key:signature
				authParts := strings.Split(subData.Auth, ":")
				if len(authParts) != 2 || authParts[0] != appKey || authParts[1] != expectedSig {
					log.Printf("Invalid signature for channel %s. Expected %s, got %s", subData.Channel, expectedSig, subData.Auth)

					errorPayload := fmt.Sprintf(`{"event":"pusher:error","data":"{\"message\":\"Invalid signature: Expected HMAC SHA256 hex digest of %s:%s, but got %s\",\"code\":null}"}`, client.SocketID, subData.Channel, subData.Auth)
					client.Send <- []byte(errorPayload)
					return
				}

				if isPresence {
					if err := json.Unmarshal([]byte(subData.ChannelData), &presenceData); err == nil {
						member = &core.ChannelMember{
							UserID:   presenceData.UserID,
							UserInfo: presenceData.UserInfo,
						}
					}
				}
			}

			isNewUser := client.AppHub.Subscribe(client, subData.Channel, member)

			if isPresence {
				membersMap := client.AppHub.GetPresenceMembers(subData.Channel)

				ids := make([]string, 0, len(membersMap))
				for id := range membersMap {
					ids = append(ids, id)
				}

				presenceHash := map[string]interface{}{
					"presence": map[string]interface{}{
						"ids":   ids,
						"hash":  membersMap,
						"count": len(membersMap),
					},
				}

				presenceHashBytes, _ := json.Marshal(presenceHash)
				safeDataStringBytes, _ := json.Marshal(string(presenceHashBytes))

				successPayload := fmt.Sprintf(`{"event":"pusher_internal:subscription_succeeded","channel":"%s","data":%s}`, subData.Channel, safeDataStringBytes)
				client.Send <- []byte(successPayload)

				if isNewUser && member != nil {
					userInfoStr := "{}"
					if member.UserInfo != nil {
						userInfoStr = string(member.UserInfo)
					}

					memberData := fmt.Sprintf(`{"user_id":"%s","user_info":%s}`, member.UserID, userInfoStr)
					safeMemberDataBytes, _ := json.Marshal(memberData)

					memberAddedPayload := fmt.Sprintf(`{"event":"pusher_internal:member_added","channel":"%s","data":%s}`, subData.Channel, safeMemberDataBytes)
					client.AppHub.BroadcastToChannel(subData.Channel, []byte(memberAddedPayload), client.SocketID)
				}
			} else {
				// Confirm subscription for public/private
				successPayload := fmt.Sprintf(`{"event":"pusher_internal:subscription_succeeded","channel":"%s","data":"{}"}`, subData.Channel)
				client.Send <- []byte(successPayload)
			}
		}

	case "pusher:unsubscribe":
		var unsubData PusherUnsubscribeData

		var dataStr string
		err := json.Unmarshal(event.Data, &dataStr)
		if err == nil {
			json.Unmarshal([]byte(dataStr), &unsubData)
		} else {
			json.Unmarshal(event.Data, &unsubData)
		}

		if unsubData.Channel != "" {
			client.AppHub.Unsubscribe(client, unsubData.Channel)
		}

	default:
		if strings.HasPrefix(event.Event, "client-") {
			// Find the channel from the event (the wrapper PusherEvent already extracts it for client events usually)
			// But for double encoding sometimes it's just event.Channel
			channelName := event.Channel

			// If not extracted, try parsing the channel out manually if it was somehow nested differently,
			// but Pusher standard places `channel` alongside `event` and `data` in the JSON structure.

			if channelName != "" {
				isPrivate := strings.HasPrefix(channelName, "private-")
				isPresence := strings.HasPrefix(channelName, "presence-")

				if isPrivate || isPresence {
					// Verify client is subscribed to this channel
					isSubscribed := false
					var member *core.ChannelMember

					client.AppHub.RLockChannels(func(channels map[string]map[*core.Client]*core.ChannelMember) {
						if subscribers, ok := channels[channelName]; ok {
							if m, ok := subscribers[client]; ok {
								isSubscribed = true
								member = m
							}
						}
					})

					if isSubscribed {
						// Double encoding: standard Pusher channels protocol requires stringified JSON.
						// Wait, client events format: {"event": "client-...", "channel": "presence-...", "data": ...}

						dataStr := string(event.Data)
						if isPresence && member != nil {
							// Append user_id to the event for presence channels
							// To do this we serialize it properly
							payload := fmt.Sprintf(`{"event":"%s","channel":"%s","data":%s,"user_id":"%s"}`, event.Event, channelName, dataStr, member.UserID)
							client.AppHub.BroadcastToChannel(channelName, []byte(payload), client.SocketID)
						} else {
							payload := fmt.Sprintf(`{"event":"%s","channel":"%s","data":%s}`, event.Event, channelName, dataStr)
							client.AppHub.BroadcastToChannel(channelName, []byte(payload), client.SocketID)
						}
					}
				}
			}
		}
	}
}

func generateSignature(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
