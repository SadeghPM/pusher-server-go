package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/metrics"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 120 * time.Second
	pingPeriod = 60 * time.Second
	// readGrace gives clients that use activity_timeout instead of control
	// pongs a margin before the read deadline expires. Without it, a client
	// pinging exactly at 120s races the server's own deadline.
	readGrace      = 30 * time.Second
	maxMessageSize = 8192

	// errorCodeApplicationNotFound is the Pusher protocol code for an app
	// that no longer exists (for example, after it was removed from config).
	errorCodeApplicationNotFound = 4001
	// errorCodeUnauthorized is the Pusher protocol code for authentication
	// failures. See the Pusher Channels Protocol documentation.
	errorCodeUnauthorized = 4009
	// errorCodeOverCapacity reports that a channel cannot accept more members.
	errorCodeOverCapacity = 4100
	// errorCodeClientEventRateLimit reports that client events are arriving
	// faster than the per-connection limit allows.
	errorCodeClientEventRateLimit = 4301
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

// UnmarshalJSON accepts both string and integer user_id values. Laravel's
// Pusher broadcaster sends the authenticated model's primary key directly, so
// integer IDs are common even though the Pusher protocol documents user_id as
// a string. Numeric IDs are normalized to their decimal string form.
func (c *ChannelData) UnmarshalJSON(data []byte) error {
	var raw struct {
		UserID   json.RawMessage `json:"user_id"`
		UserInfo json.RawMessage `json:"user_info"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	userID, err := parsePresenceUserID(raw.UserID)
	if err != nil {
		return err
	}

	c.UserID = userID
	c.UserInfo = raw.UserInfo
	return nil
}

func parsePresenceUserID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("user_id is required")
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}

	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		literal := v.String()
		// Accept integer identifiers only; a float or exponent is not a
		// meaningful user ID and would not match Pusher's string convention.
		if _, err := strconv.ParseInt(literal, 10, 64); err != nil {
			if _, err := strconv.ParseUint(literal, 10, 64); err != nil {
				return "", fmt.Errorf("user_id must be a string or integer")
			}
		}
		return literal, nil
	default:
		return "", fmt.Errorf("user_id must be a string or integer")
	}
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
	// TrySend: if the send buffer is full the connection is unusable anyway,
	// and a client that is being torn down concurrently must not panic.
	client.TrySend(establishedPayload)

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
	client.Conn.SetReadDeadline(time.Now().Add(pongWait + readGrace))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(pongWait + readGrace))
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

		// Any frame from the client proves the connection is alive. Pusher
		// clients commonly send pusher:ping instead of relying on control
		// pongs, so only extending the deadline on pong would disconnect them.
		client.Conn.SetReadDeadline(time.Now().Add(pongWait + readGrace))
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
	// TrySend disconnects a stalled client instead of blocking the read pump
	// behind a full send buffer.
	client.TrySend(core.MarshalEvent("pusher:pong", "", core.EncodeDataString(struct{}{}), ""))
}

func (s *Server) handleSubscribe(client *core.Client, event PusherEvent, appKey string) {
	cfg := s.configManager.GetConfig()
	debug := cfg != nil && cfg.Debug
	appCfg := s.configManager.GetAppByKey(appKey)
	if appCfg == nil {
		// The app was removed or its key was rotated while this connection was
		// alive. Tell the client so it reconnects instead of waiting forever.
		s.sendError(client, errorCodeApplicationNotFound, "Application does not exist.")
		return
	}
	appSecret := appCfg.AppSecret

	var subData PusherSubscribeData

	// Pusher clients may double-encode the subscribe data as a JSON string;
	// accept both the encoded string and a plain object.
	var dataStr string
	if err := json.Unmarshal(event.Data, &dataStr); err == nil {
		if err := json.Unmarshal([]byte(dataStr), &subData); err != nil {
			s.sendError(client, 0, "Invalid subscribe data: could not parse data.")
			return
		}
	} else if err := json.Unmarshal(event.Data, &subData); err != nil {
		s.sendError(client, 0, "Invalid subscribe data: could not parse data.")
		return
	}

	if subData.Channel == "" {
		s.sendError(client, 0, "Invalid subscribe data: missing channel.")
		return
	}
	if !core.IsValidChannelName(subData.Channel) {
		s.sendError(client, 0, "Invalid channel name.")
		return
	}

	// A single connection must not be able to create unbounded channels.
	channelCount, alreadySubscribed := client.AppHub.SubscriptionInfo(client, subData.Channel)
	if !alreadySubscribed && channelCount >= core.MaxChannelsPerConnection {
		s.sendError(client, 0, "Too many channels for this connection.")
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
			if len(subData.ChannelData) > core.MaxPresenceChannelDataBytes {
				s.sendError(client, errorCodeUnauthorized, "channel_data is too large.")
				return
			}

			var presenceData ChannelData
			if err := json.Unmarshal([]byte(subData.ChannelData), &presenceData); err != nil || presenceData.UserID == "" {
				s.sendError(client, errorCodeUnauthorized, "Invalid channel_data for presence channel.")
				return
			}
			if len(presenceData.UserID) > core.MaxPresenceUserIDLength {
				s.sendError(client, errorCodeUnauthorized, "Presence user_id is too long.")
				return
			}
			if len(presenceData.UserInfo) > core.MaxPresenceUserInfoBytes {
				s.sendError(client, errorCodeUnauthorized, "Presence user_info is too large.")
				return
			}

			// Presence size is enforced atomically in TrySubscribe, which
			// counts distinct user IDs rather than connections.

			member = &core.ChannelMember{
				UserID:   presenceData.UserID,
				UserInfo: presenceData.UserInfo,
			}
		}
	}

	ack := func(members map[string]json.RawMessage) {
		if isPresence {
			s.sendPresenceSubscriptionSuccess(client, subData.Channel, members)
			return
		}
		successPayload := core.MarshalEvent(
			"pusher_internal:subscription_succeeded",
			subData.Channel,
			core.EncodeDataString(struct{}{}),
			"",
		)
		client.TrySend(successPayload)
	}

	// The ack is queued while the hub lock is held, so a concurrent broadcast
	// cannot reach this client before subscription_succeeded.
	isNewUser, accepted := client.AppHub.TrySubscribeWithAck(client, subData.Channel, member, ack)
	if !accepted {
		s.sendError(client, errorCodeOverCapacity, "Presence channel is full.")
		return
	}

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

	s.globalHub.Debugger.Notify(client.AppHub.AppID(), "subscription", client.SocketID, subData.Channel, "", "")

	if isPresence && isNewUser && member != nil {
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
			subData.Channel,
			core.EncodeDataString(memberData),
			"",
		)
		// Queue broadcast and webhook together so the ordered worker delivers
		// member_added before any later member_removed for this channel.
		client.AppHub.BroadcastToChannelAsync(subData.Channel, memberAddedPayload, client.SocketID)

		client.AppHub.DispatchWebhook([]core.WebhookEvent{
			{
				Name:    "member_added",
				Channel: subData.Channel,
				UserID:  member.UserID,
			},
		})
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
// Per the protocol, the code field is optional: a code of 0 sends no code,
// which is right for generic client errors that have no documented code and
// must not close the connection.
func (s *Server) sendError(client *core.Client, code int, message string) {
	data := map[string]any{"message": message}
	if code != 0 {
		data["code"] = code
	}
	payload := core.MarshalEvent(
		"pusher:error",
		"",
		core.EncodeDataString(data),
		"",
	)
	// TrySend disconnects a stalled client rather than blocking the read pump.
	client.TrySend(payload)
}

// sendPresenceSubscriptionSuccess builds and queues the presence member
// snapshot. It is called from TrySubscribeWithAck while the hub lock is held,
// so the snapshot and the frame are consistent with the new subscription.
func (s *Server) sendPresenceSubscriptionSuccess(client *core.Client, channel string, membersMap map[string]json.RawMessage) {
	ids := make([]string, 0, len(membersMap))
	hash := make(map[string]json.RawMessage, len(membersMap))
	for id, info := range membersMap {
		ids = append(ids, id)
		// Normalize a missing user_info to an empty object so the hash never
		// mixes null values with objects (matches member_added handling).
		if len(info) == 0 {
			info = json.RawMessage(`{}`)
		}
		hash[id] = info
	}

	presenceHash := map[string]interface{}{
		"presence": map[string]interface{}{
			"ids":   ids,
			"hash":  hash,
			"count": len(ids),
		},
	}

	successPayload := core.MarshalEvent(
		"pusher_internal:subscription_succeeded",
		channel,
		core.EncodeDataString(presenceHash),
		"",
	)
	client.TrySend(successPayload)
}

func (s *Server) handleUnsubscribe(client *core.Client, event PusherEvent, debug bool) {
	var unsubData PusherUnsubscribeData

	// Accept both the double-encoded string and a plain object, like
	// handleSubscribe does. An unsubscribe with no channel stays a harmless
	// no-op.
	var dataStr string
	if err := json.Unmarshal(event.Data, &dataStr); err == nil {
		if err := json.Unmarshal([]byte(dataStr), &unsubData); err != nil {
			s.sendError(client, 0, "Invalid unsubscribe data: could not parse data.")
			return
		}
	} else if err := json.Unmarshal(event.Data, &unsubData); err != nil {
		s.sendError(client, 0, "Invalid unsubscribe data: could not parse data.")
		return
	}

	if unsubData.Channel != "" {
		if !core.IsValidChannelName(unsubData.Channel) {
			s.sendError(client, 0, "Invalid channel name.")
			return
		}
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
	if !strings.HasPrefix(event.Event, "client-") {
		return
	}

	// Fixed-window rate limit, applied before any channel work so abusive
	// frames cannot consume broadcast resources.
	now := time.Now()
	if client.ClientEventWindowStart.IsZero() || now.Sub(client.ClientEventWindowStart) >= core.ClientEventWindow {
		client.ClientEventWindowStart = now
		client.ClientEventCount = 0
	}
	client.ClientEventCount++
	if client.ClientEventCount > core.ClientEventLimit {
		// Notify once per window instead of amplifying every dropped frame.
		if client.ClientEventCount == core.ClientEventLimit+1 {
			s.sendError(client, errorCodeClientEventRateLimit, "Client event rate limit exceeded.")
		}
		return
	}

	if len(event.Event) > 200 {
		return
	}
	channelName := event.Channel
	if !core.IsValidChannelName(channelName) {
		return
	}

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
