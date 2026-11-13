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
	writeWait      = 10 * time.Second
	pongWait       = 120 * time.Second
	pingPeriod     = 60 * time.Second
	maxMessageSize = 8192

	// errorCodeUnauthorized is the Pusher protocol code for authentication
	// failures. See the Pusher Channels Protocol documentation.
	errorCodeUnauthorized = 4009
)

type PusherEvent struct {
	Event   string          `json:"event"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data"`
}

type PusherSubscribeData struct {
	Channel     string `json:"channel"`
	Auth        string `json:"auth,omitempty"`
	ChannelData string `json:"channel_data,omitempty"`
}

type PusherUnsubscribeData struct {
	Channel string `json:"channel"`
}

type ChannelData struct {
	UserID   string          `json:"user_id"`
	UserInfo json.RawMessage `json:"user_info,omitempty"`
}

type Server struct {
	globalHub     *core.GlobalHub
	configManager *config.Manager
}

func NewServer(globalHub *core.GlobalHub, manager *config.Manager) *Server {
	return &Server{
		globalHub:     globalHub,
		configManager: manager,
	}
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request, appKey string) {
	appCfg := s.configManager.GetAppByKey(appKey)

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
				return true
			}
			if len(appCfg.AllowedOrigins) == 0 {
				return true
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
		s.globalHub.Debugger.Notify(appCfg.AppID, "connection_error", "", "", "WebSocket upgrade failed", err.Error())
		return
	}

	appHub := s.globalHub.GetOrCreateAppHub(appCfg.AppID)
	socketID := generateSocketID()

	client := core.NewClient(appHub, conn, socketID)
	appHub.RegisterClient(client)

	cfg := s.configManager.GetConfig()
	if cfg != nil && cfg.Debug {
		slog.Debug("Client connected",
			"app_id", appCfg.AppID,
			"socket_id", socketID,
		)
	}

	establishedPayload := core.MarshalEvent(
		"pusher:connection_established",
		"",
		core.EncodeDataString(map[string]any{
			"socket_id":        socketID,
			"activity_timeout": int(pongWait.Seconds()),
		}),
		"",
	)
	client.Send <- establishedPayload

	s.globalHub.Debugger.Notify(appCfg.AppID, "connection", socketID, "", "", "")

	go s.writePump(client)
	go s.readPump(client, appKey)
}

var socketIDCounter uint64

func generateSocketID() string {
	counter := atomic.AddUint64(&socketIDCounter, 1)
	return fmt.Sprintf("%d.%d", time.Now().Unix(), counter)
}

func (s *Server) readPump(client *core.Client, appKey string) {
	defer func() {
		client.AppHub.UnregisterClient(client)
		client.Conn.Close()

		s.globalHub.Debugger.Notify(client.AppHub.AppID(), "disconnection", client.SocketID, "", "", "")

		cfg := s.configManager.GetConfig()
		if cfg != nil && cfg.Debug {
			slog.Debug("Client disconnected",
				"app_id", client.AppHub.AppID(),
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
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
				websocket.CloseAbnormalClosure,
			) {
				slog.Error("WebSocket read error",
					"error", err,
					"app_id", client.AppHub.AppID(),
					"socket_id", client.SocketID,
				)
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID(), "read").Inc()
				s.globalHub.Debugger.Notify(client.AppHub.AppID(), "connection_error", client.SocketID, "", "WebSocket read error", err.Error())
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
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID(), "write").Inc()
				return
			}

			n := len(client.Send)
			for i := 0; i < n; i++ {
				message, ok := <-client.Send
				if !ok {
					client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
					metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID(), "write").Inc()
					return
				}
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				metrics.WebsocketErrorsTotal.WithLabelValues(client.AppHub.AppID(), "ping").Inc()
				return
			}
		}
	}
}

func (s *Server) handlePing(client *core.Client, debug bool) {
	if debug {
		slog.Debug("Ping received",
			"app_id", client.AppHub.AppID(),
			"socket_id", client.SocketID,
			"event", "pusher:ping",
		)
	}
	client.Send <- core.MarshalEvent("pusher:pong", "", core.EncodeDataString(struct{}{}), "")
}

func (s *Server) handleSubscribe(client *core.Client, event PusherEvent, appKey string) {
	cfg := s.configManager.GetConfig()
	debug := cfg != nil && cfg.Debug
	appCfg := s.configManager.GetAppByKey(appKey)
	if appCfg == nil {
		return
	}
	appSecret := appCfg.AppSecret

	var subData PusherSubscribeData

	var dataStr string
	err := json.Unmarshal(event.Data, &dataStr)
	if err == nil {
		json.Unmarshal([]byte(dataStr), &subData)
	} else {
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
			if subData.ChannelData == "" {
				s.sendError(client, errorCodeUnauthorized, "Presence channels require channel_data.")
				return
			}

			var presenceData ChannelData
			if err := json.Unmarshal([]byte(subData.ChannelData), &presenceData); err != nil || presenceData.UserID == "" {
				s.sendError(client, errorCodeUnauthorized, "Invalid channel_data for presence channel.")
				return
			}

			member = &core.ChannelMember{
				UserID:   presenceData.UserID,
				UserInfo: presenceData.UserInfo,
			}
		}
	}

	isNewUser := client.AppHub.Subscribe(client, subData.Channel, member)

	if debug {
		if isPresence && member != nil {
			slog.Debug("User subscribed to presence channel",
				"app_id", client.AppHub.AppID(),
				"channel", subData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:subscribe",
				"user_id", member.UserID,
			)
		} else {
			slog.Debug("Client subscribed to channel",
				"app_id", client.AppHub.AppID(),
				"channel", subData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:subscribe",
			)
		}
	}

	if isPresence {
		s.handlePresenceSubscriptionSuccess(client, subData.Channel, member, isNewUser)
	} else {
		successPayload := core.MarshalEvent(
			"pusher_internal:subscription_succeeded",
			subData.Channel,
			core.EncodeDataString(struct{}{}),
			"",
		)
		client.Send <- successPayload
		s.globalHub.Debugger.Notify(client.AppHub.AppID(), "subscription", client.SocketID, subData.Channel, "", "")
	}
}

func (s *Server) verifySubscriptionSignature(client *core.Client, subData PusherSubscribeData, appSecret, appKey string, isPresence bool) bool {
	toSign := fmt.Sprintf("%s:%s", client.SocketID, subData.Channel)
	if isPresence {
		toSign = fmt.Sprintf("%s:%s:%s", client.SocketID, subData.Channel, subData.ChannelData)
	}
	expectedSig := generateSignature(appSecret, toSign)

	authParts := strings.Split(subData.Auth, ":")
	if len(authParts) != 2 || authParts[0] != appKey || !hmac.Equal([]byte(authParts[1]), []byte(expectedSig)) {
		slog.Error("Invalid signature",
			"app_id", client.AppHub.AppID(),
			"channel", subData.Channel,
			"socket_id", client.SocketID,
		)
		s.globalHub.Debugger.Notify(client.AppHub.AppID(), "auth_error", client.SocketID, subData.Channel, "Invalid signature", "The provided auth signature is invalid.")

		// Never echo the expected signature back to the client or into logs:
		// it is a secret-derived value and would make signature forgery easier.
		s.sendError(client, errorCodeUnauthorized, "Invalid signature.")
		return false
	}
	return true
}

// sendError sends a Pusher error event with a serialized error object as data.
func (s *Server) sendError(client *core.Client, code int, message string) {
	payload := core.MarshalEvent(
		"pusher:error",
		"",
		core.EncodeDataString(map[string]any{
			"message": message,
			"code":    code,
		}),
		"",
	)
	client.Send <- payload
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

	successPayload := core.MarshalEvent(
		"pusher_internal:subscription_succeeded",
		channel,
		core.EncodeDataString(presenceHash),
		"",
	)
	client.Send <- successPayload

	s.globalHub.Debugger.Notify(client.AppHub.AppID(), "subscription", client.SocketID, channel, "", "")

	if isNewUser && member != nil {
		userInfo := member.UserInfo
		if len(userInfo) == 0 {
			userInfo = json.RawMessage(`{}`)
		}

		memberData := map[string]interface{}{
			"user_id":   member.UserID,
			"user_info": userInfo,
		}
		memberAddedPayload := core.MarshalEvent(
			"pusher_internal:member_added",
			channel,
			core.EncodeDataString(memberData),
			"",
		)
		client.AppHub.BroadcastToChannel(channel, memberAddedPayload, client.SocketID)

		client.AppHub.DispatchWebhook([]core.WebhookEvent{
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
				"app_id", client.AppHub.AppID(),
				"channel", unsubData.Channel,
				"socket_id", client.SocketID,
				"event", "pusher:unsubscribe",
			)
		}
	}
}

func (s *Server) handleClientEvent(client *core.Client, event PusherEvent, debug bool) {
	if strings.HasPrefix(event.Event, "client-") {
		channelName := event.Channel

		if channelName != "" {
			isPrivate := strings.HasPrefix(channelName, "private-")
			isPresence := strings.HasPrefix(channelName, "presence-")

			if isPrivate || isPresence {
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
					s.globalHub.Debugger.Notify(client.AppHub.AppID(), "client_event", client.SocketID, channelName, event.Event, string(event.Data))

					if debug {
						slog.Debug("Client event triggered",
							"app_id", client.AppHub.AppID(),
							"channel", channelName,
							"socket_id", client.SocketID,
							"event", event.Event,
						)
					}

					userID := ""
					if isPresence && member != nil {
						userID = member.UserID
					}
					payload := core.MarshalEvent(event.Event, channelName, event.Data, userID)
					client.AppHub.BroadcastToChannel(channelName, payload, client.SocketID)
				}
			}
		}
	}
}

func (s *Server) handleMessage(client *core.Client, message []byte, appKey string) {
	cfg := s.configManager.GetConfig()
	debug := cfg != nil && cfg.Debug

	var event PusherEvent
	if err := json.Unmarshal(message, &event); err != nil {
		slog.Error("Invalid JSON received",
			"error", err,
			"app_id", client.AppHub.AppID(),
			"socket_id", client.SocketID,
		)
		s.globalHub.Debugger.Notify(client.AppHub.AppID(), "connection_error", client.SocketID, "", "Invalid JSON received", err.Error())
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
