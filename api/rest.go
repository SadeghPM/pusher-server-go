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
	"log"
	"net/http"

	"pusher-clone/config"
	"pusher-clone/core"
)

type API struct {
	GlobalHub *core.GlobalHub
	Config    *config.Config
	appsByID  map[string]*config.AppConfig
}

func NewAPI(globalHub *core.GlobalHub, cfg *config.Config) *API {
	appsByID := make(map[string]*config.AppConfig)
	if cfg != nil {
		for i := range cfg.Apps {
			appsByID[cfg.Apps[i].AppID] = &cfg.Apps[i]
		}
	}

	return &API{
		GlobalHub: globalHub,
		Config:    cfg,
		appsByID:  appsByID,
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
	appCfg := a.appsByID[appID]

	if appCfg == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Limit request body to 1MB to prevent memory exhaustion DoS
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			http.Error(w, "Payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 1. Authenticate request using HMAC SHA256
	if err := authenticateRequest(r, body, appCfg); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// 2. Parse payload
	var payload TriggerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Gather channels
	channels := payload.Channels
	if payload.Channel != "" {
		channels = append(channels, payload.Channel)
	}

	// 3. Broadcast to WebSockets
	appHub := a.GlobalHub.GetOrCreateAppHub(appID)

	// Construct the WebSocket event message
	// Note: The data field in the REST payload is already a stringified JSON.
	// We pass it directly into the "data" field of our websocket message.
	escapedData, _ := json.Marshal(payload.Data) // Ensures proper string escaping if needed, but usually it's already a string.

	for _, channel := range channels {
		// If payload.Data is already stringified JSON, using string format directly works for Pusher clients
		message := fmt.Sprintf(`{"event":"%s","channel":"%s","data":%s}`, payload.Name, channel, escapedData)

		appHub.BroadcastToChannel(channel, []byte(message), payload.SocketID)
	}

	// Respond with success
	if a.Config != nil && a.Config.Debug {
		log.Printf("[DEBUG] Broadcasted event %s to channels %v via REST API (App ID: %s)", payload.Name, channels, appID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{}`))
}

// authenticateRequest verifies the Pusher REST API request signature.
func authenticateRequest(r *http.Request, body []byte, appCfg *config.AppConfig) error {
	// Method\nPath\nQuery params (alphabetical)
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

	// Reconstruct signature string
	// Params must be ordered alphabetically: auth_key, auth_timestamp, auth_version, body_md5
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)

	stringToSign := fmt.Sprintf("%s\n%s\n%s", r.Method, r.URL.Path, queryParams)

	mac := hmac.New(sha256.New, []byte(appCfg.AppSecret))
	mac.Write([]byte(stringToSign))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if authSignature != expectedSignature {
		return errors.New("Invalid signature")
	}

	return nil
}
