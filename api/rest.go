package api

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/metrics"
)

type API struct {
	globalHub     *core.GlobalHub
	configManager *config.Manager
}

func NewAPI(globalHub *core.GlobalHub, manager *config.Manager) *API {
	return &API{
		globalHub:     globalHub,
		configManager: manager,
	}
}

// Request payload from Laravel (Pusher REST API)
type TriggerPayload struct {
	Name     string   `json:"name"`
	Data     string   `json:"data"`
	Channels []string `json:"channels,omitempty"`
	Channel  string   `json:"channel,omitempty"`
	SocketID string   `json:"socket_id,omitempty"`
}

func (a *API) HandleEvents(w http.ResponseWriter, r *http.Request, appID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Find App Config
	appCfg := a.configManager.GetAppByID(appID)

	if appCfg == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Limit request body to 1MB to prevent memory exhaustion DoS
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			http.Error(w, "Payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// 1. Authenticate request using HMAC SHA256
	if err := authenticateRequest(r, body, appCfg); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		a.globalHub.Debugger.Notify(appID, "auth_error", "", "", "REST API Auth Failed", err.Error())
		return
	}

	// 2. Parse payload
	var payload TriggerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		a.globalHub.Debugger.Notify(appID, "api_error", "", "", "REST API Invalid JSON", err.Error())
		return
	}

	// Gather channels
	channels := payload.Channels
	if payload.Channel != "" {
		channels = append(channels, payload.Channel)
	}

	// 3. Broadcast to WebSockets
	appHub := a.globalHub.GetOrCreateAppHub(appID)

	// Construct the WebSocket event message
	escapedData, _ := json.Marshal(payload.Data)

	for _, channel := range channels {
		message := fmt.Sprintf(`{"event":"%s","channel":"%s","data":%s}`, payload.Name, channel, escapedData)
		appHub.BroadcastToChannel(channel, []byte(message), payload.SocketID)
	}

	metrics.RestAPIEventsTotal.WithLabelValues(appID).Inc()

	for _, channel := range channels {
		a.globalHub.Debugger.Notify(appID, "api_message", payload.SocketID, channel, payload.Name, payload.Data)
	}

	// Respond with success
	slog.Info("Broadcasted event via REST API",
		"app_id", appID,
		"event", payload.Name,
		"channels", channels,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{}`))
}

// authenticateRequest verifies the Pusher REST API request signature.
func authenticateRequest(r *http.Request, body []byte, appCfg *config.AppConfig) error {
	authKey := r.URL.Query().Get("auth_key")
	authTimestamp := r.URL.Query().Get("auth_timestamp")
	authVersion := r.URL.Query().Get("auth_version")
	bodyMD5 := r.URL.Query().Get("body_md5")
	authSignature := r.URL.Query().Get("auth_signature")

	if authKey != appCfg.AppKey {
		return errors.New("Unauthorized")
	}

	// Verify body MD5
	hasher := md5.New()
	hasher.Write(body)
	expectedMD5 := hex.EncodeToString(hasher.Sum(nil))

	if bodyMD5 != expectedMD5 {
		return errors.New("Invalid body MD5")
	}

	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)
	stringToSign := fmt.Sprintf("%s\n%s\n%s", r.Method, r.URL.Path, queryParams)

	mac := hmac.New(sha256.New, []byte(appCfg.AppSecret))
	mac.Write([]byte(stringToSign))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(authSignature), []byte(expectedSignature)) {
		return errors.New("Invalid signature")
	}

	return nil
}
