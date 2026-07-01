package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/metrics"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 120 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 60 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 8192
)

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
	GlobalHub     *core.GlobalHub
	ConfigManager *config.Manager
}

func NewServer(globalHub *core.GlobalHub, manager *config.Manager) *Server {
	return &Server{
		GlobalHub:     globalHub,
		ConfigManager: manager,
	}
}

// Handler generation per AppKey is not strictly necessary if we parse it from the URL
// But we registered explicitly in main.go before. We will extract appKey from the path now in main.go
// so we need a unified handler that takes the appKey.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request, appKey string) {
	// Find the matching AppConfig
	appCfg := s.ConfigManager.GetAppByKey(appKey)

	if appCfg == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // Allow requests without Origin header (e.g., direct WS clients like mobile apps)
			}
			if len(appCfg.AllowedOrigins) == 0 {
				return true // If not configured, allow all
			}
			for _, allowed := range appCfg.AllowedOrigins {
				if allowed == "*" || allowed == origin {
					return true
				}
			}
			return false
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		s.GlobalHub.DebugNotify(appCfg.AppID, "connection_error", "", "", "WebSocket upgrade failed", err.Error())
		return
	}

	appHub := s.GlobalHub.GetOrCreateAppHub(appCfg.AppID)
	socketID := generateSocketID()

	client := core.NewClient(appHub, conn, socketID)
	appHub.RegisterClient(client)

	cfg := s.ConfigManager.GetConfig()
	if cfg != nil && cfg.Debug {
		slog.Debug("Client connected",
			"app_id", appCfg.AppID,
			"socket_id", socketID,
		)
	}

	// Send connection established event
	establishedPayload := fmt.Sprintf(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"%s\",\"activity_timeout\":%d}"}`, socketID, int(pongWait.Seconds()))
	client.Send <- []byte(establishedPayload)

	s.GlobalHub.DebugNotify(appCfg.AppID, "connection", socketID, "", "", "")

	go s.writePump(client)
	go s.readPump(client, appKey)
}

var socketIDCounter uint32

func generateSocketID() string {
	counter := atomic.AddUint32(&socketIDCounter, 1)
	return fmt.Sprintf("%d.%d", time.Now().Unix(), counter)
}

func (s *Server) readPump(client *core.Client, appKey string) {
	defer func() {
		client.AppHub.UnregisterClient(client)
		client.Conn.Close()

		s.GlobalHub.DebugNotify(client.AppHub.AppID, "disconnection", client.SocketID, "", "", "")

		cfg := s.ConfigManager.GetConfig()
		if cfg != nil && cfg.Debug {
			slog.Debug("Client disconnected",
				"app_id", client.AppHub.AppID,
				"socket_id", client.SocketID,
			)
		}
	}()

	client.Conn.SetReadLimit(maxMessageSize)
	client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket read error",
					"error", err,
					"app_id", client.AppHub.AppID,
					"socket_id", client.SocketID,
				)
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID, "read").Inc()
				s.GlobalHub.DebugNotify(client.AppHub.AppID, "connection_error", client.SocketID, "", "WebSocket read error", err.Error())
			}
			break
		}

		s.handleMessage(client, message, appKey)
	}
}

func (s *Server) writePump(client *core.Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID, "write").Inc()
				return
			}

			// Send queued chat messages as separate websocket messages.
			n := len(client.Send)
			for i := 0; i < n; i++ {
				if err := client.Conn.WriteMessage(websocket.TextMessage, <-client.Send); err != nil {
					metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID, "write").Inc()
					return
				}
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID, "ping").Inc()
				return
			}
		}
	}
}

func (s *Server) handlePing(client *core.Client, debug bool) {
	if debug {
		slog.Debug("Ping received",
			"app_id", client.AppHub.AppID,
			"socket_id", client.SocketID,
			"event", "pusher:ping",
		)
	}
	client.Send <- []byte(`{"event":"pusher:pong","data":"{}"}`)
}

func (s *Server) handleSubscribe(client *core.Client, event PusherEvent, appKey string) {
	cfg := s.ConfigManager.GetConfig()
	debug := cfg != nil && cfg.Debug
	appCfg := s.ConfigManager.GetAppByKey(appKey)
	if appCfg == nil {
		return
	}
	appSecret := appCfg.AppSecret

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

	if subData.Channel == "" {
		return
	}

	isPrivate := strings.HasPrefix(subData.Channel, "private-")
	isPresence := strings.HasPrefix(subData.Channel, "presence-")

	var member *core.ChannelMember

	if isPrivate || isPresence {
		if !s.verifySubscriptionSignature(client, subData, appSecret, appKey, isPresence) {
			return
		}

		if isPresence {
			var presenceData ChannelData
			if err := json.Unmarshal([]byte(subData.ChannelData), &presenceData); err == nil {
				member = &core.ChannelMember{
					UserID:   presenceData.UserID,
					UserInfo: presenceData.UserInfo,
				}
			}
		}
	}

	isNewUser := client.AppHub.Subscribe(client, subData.Channel, member)

	if debug {
		if isPresence && member != nil {
			slog.Debug("User subscribed to presence channel",
				"app_id", client.AppHub.AppID,
				"channel", subData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:subscribe",
				"user_id", member.UserID,
			)
		} else {
			slog.Debug("Client subscribed to channel",
				"app_id", client.AppHub.AppID,
				"channel", subData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:subscribe",
			)
		}
	}

	if isPresence {
		s.handlePresenceSubscriptionSuccess(client, subData.Channel, member, isNewUser)
	} else {
		// Confirm subscription for public/private
		successPayload := fmt.Sprintf(`{"event":"pusher_internal:subscription_succeeded","channel":"%s","data":"{}"}`, subData.Channel)
		client.Send <- []byte(successPayload)
		s.GlobalHub.DebugNotify(client.AppHub.AppID, "subscription", client.SocketID, subData.Channel, "", "")
	}
}

func (s *Server) verifySubscriptionSignature(client *core.Client, subData PusherSubscribeData, appSecret, appKey string, isPresence bool) bool {
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
		slog.Error("Invalid signature",
			"app_id", client.AppHub.AppID,
			"channel", subData.Channel,
			"socket_id", client.SocketID,
			"expected", expectedSig,
			"got", subData.Auth,
		)
		s.GlobalHub.DebugNotify(client.AppHub.AppID, "auth_error", client.SocketID, subData.Channel, "Invalid signature", fmt.Sprintf("Expected: %s, Got: %s", expectedSig, subData.Auth))

		errorPayload := fmt.Sprintf(`{"event":"pusher:error","data":"{\"message\":\"Invalid signature: Expected HMAC SHA256 hex digest of %s:%s, but got %s\",\"code\":null}"}`, client.SocketID, subData.Channel, subData.Auth)
		client.Send <- []byte(errorPayload)
		return false
	}
	return true
}

func (s *Server) handlePresenceSubscriptionSuccess(client *core.Client, channel string, member *core.ChannelMember, isNewUser bool) {
	membersMap := client.AppHub.GetPresenceMembers(channel)

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

	successPayload := fmt.Sprintf(`{"event":"pusher_internal:subscription_succeeded","channel":"%s","data":%s}`, channel, safeDataStringBytes)
	client.Send <- []byte(successPayload)

	s.GlobalHub.DebugNotify(client.AppHub.AppID, "subscription", client.SocketID, channel, "", "")

	if isNewUser && member != nil {
		userInfoStr := "{}"
		if member.UserInfo != nil {
			userInfoStr = string(member.UserInfo)
		}

		memberData := fmt.Sprintf(`{"user_id":"%s","user_info":%s}`, member.UserID, userInfoStr)
		safeMemberDataBytes, _ := json.Marshal(memberData)

		memberAddedPayload := fmt.Sprintf(`{"event":"pusher_internal:member_added","channel":"%s","data":%s}`, channel, safeMemberDataBytes)
		client.AppHub.BroadcastToChannel(channel, []byte(memberAddedPayload), client.SocketID)

		go client.AppHub.Dispatcher.Dispatch(client.AppHub.AppID, []core.WebhookEvent{
			{
				Name:    "member_added",
				Channel: channel,
				UserID:  member.UserID,
			},
		})
	}
}

func (s *Server) handleUnsubscribe(client *core.Client, event PusherEvent, debug bool) {
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
		if debug {
			slog.Debug("Client unsubscribed from channel",
				"app_id", client.AppHub.AppID,
				"channel", unsubData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:unsubscribe",
			)
		}
	}
}

func (s *Server) handleClientEvent(client *core.Client, event PusherEvent, debug bool) {
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
					s.GlobalHub.DebugNotify(client.AppHub.AppID, "client_event", client.SocketID, channelName, event.Event, string(event.Data))

					if debug {
						slog.Debug("Client event triggered",
							"app_id", client.AppHub.AppID,
							"channel", channelName,
							"socket_id", client.SocketID,
							"event", event.Event,
						)
					}
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

func (s *Server) handleMessage(client *core.Client, message []byte, appKey string) {
	cfg := s.ConfigManager.GetConfig()
	debug := cfg != nil && cfg.Debug

	var event PusherEvent
	if err := json.Unmarshal(message, &event); err != nil {
		slog.Error("Invalid JSON received",
			"error", err,
			"app_id", client.AppHub.AppID,
			"socket_id", client.SocketID,
		)
		s.GlobalHub.DebugNotify(client.AppHub.AppID, "connection_error", client.SocketID, "", "Invalid JSON received", err.Error())
		return
	}

	switch event.Event {
	case "pusher:ping":
		s.handlePing(client, debug)
	case "pusher:subscribe":
		s.handleSubscribe(client, event, appKey)
	case "pusher:unsubscribe":
		s.handleUnsubscribe(client, event, debug)
	default:
		s.handleClientEvent(client, event, debug)
	}
}

func generateSignature(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
