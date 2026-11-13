package api

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/metrics"
)

const (
	// authTimestampWindow is the maximum accepted clock skew for REST API
	// requests, as specified by the Pusher HTTP API.
	authTimestampWindow = 600 * time.Second

	// maxEventDataLength is the maximum length of the event data string in
	// bytes, matching the official Pusher HTTP API's 10 KB limit.
	maxEventDataLength = 10000
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

	if payload.Name == "" || len(payload.Name) > 200 {
		http.Error(w, "Invalid event name", http.StatusBadRequest)
		return
	}

	// Gather channels without mutating the decoded slice.
	channels := append([]string(nil), payload.Channels...)
	if payload.Channel != "" {
		channels = append(channels, payload.Channel)
	}
	if len(channels) == 0 || len(channels) > 100 {
		http.Error(w, "Invalid channels", http.StatusBadRequest)
		return
	}
	for _, channel := range channels {
		if channel == "" || len(channel) > 200 {
			http.Error(w, "Invalid channels", http.StatusBadRequest)
			return
		}
	}

	if len(payload.Data) > maxEventDataLength {
		http.Error(w, "Event message over 10k", http.StatusRequestEntityTooLarge)
		return
	}

	// 3. Broadcast to WebSockets
	appHub := a.globalHub.GetOrCreateAppHub(appID)

	// Construct the WebSocket event message.
	escapedData, err := json.Marshal(payload.Data)
	if err != nil {
		http.Error(w, "Invalid data", http.StatusBadRequest)
		return
	}

	for _, channel := range channels {
		message := core.MarshalEvent(payload.Name, channel, escapedData, "")
		appHub.BroadcastToChannel(channel, message, payload.SocketID)
	}

	metrics.RestAPIEventsTotal.WithLabelValues(appID).Inc()

	for _, channel := range channels {
		a.globalHub.Debugger.Notify(appID, "api_message", payload.SocketID, channel, payload.Name, payload.Data)
	}

	// Respond with success
	slog.Debug("Broadcasted event via REST API",
		"app_id", appID,
		"event", payload.Name,
		"channels", channels,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{}`))
}

// authenticateRequest verifies the Pusher REST API request signature using the
// current time for the timestamp window.
func authenticateRequest(r *http.Request, body []byte, appCfg *config.AppConfig) error {
	return authenticateRequestAt(r, body, appCfg, time.Now())
}

// authenticateRequestAt verifies the Pusher REST API request signature. It is
// parameterized by time so tests can verify fixed official vectors.
func authenticateRequestAt(r *http.Request, body []byte, appCfg *config.AppConfig, now time.Time) error {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return errors.New("Invalid query string")
	}

	authKey := query.Get("auth_key")
	authTimestamp := query.Get("auth_timestamp")
	authVersion := query.Get("auth_version")
	bodyMD5 := query.Get("body_md5")
	authSignature := query.Get("auth_signature")

	if subtle.ConstantTimeCompare([]byte(authKey), []byte(appCfg.AppKey)) != 1 {
		return errors.New("Unauthorized")
	}

	if authVersion != "1.0" {
		return errors.New("Invalid auth_version")
	}

	timestamp, err := strconv.ParseInt(authTimestamp, 10, 64)
	if err != nil {
		return errors.New("Invalid auth_timestamp")
	}

	nowUnix := now.Unix()
	windowSeconds := int64(authTimestampWindow / time.Second)
	if timestamp < nowUnix-windowSeconds || timestamp > nowUnix+windowSeconds {
		return errors.New("Timestamp expired")
	}

	if len(body) > 0 && bodyMD5 == "" {
		return errors.New("Missing body MD5")
	}

	if bodyMD5 != "" {
		hasher := md5.New()
		hasher.Write(body)
		expectedMD5 := hex.EncodeToString(hasher.Sum(nil))

		if subtle.ConstantTimeCompare([]byte(bodyMD5), []byte(expectedMD5)) != 1 {
			return errors.New("Invalid body MD5")
		}
	}

	queryParams, err := canonicalQueryString(query)
	if err != nil {
		return err
	}

	stringToSign := fmt.Sprintf("%s\n%s\n%s", r.Method, r.URL.Path, queryParams)

	mac := hmac.New(sha256.New, []byte(appCfg.AppSecret))
	mac.Write([]byte(stringToSign))
	expectedSignature := mac.Sum(nil)

	providedSignature, err := hex.DecodeString(authSignature)
	if err != nil {
		return errors.New("Invalid signature")
	}

	if !hmac.Equal(providedSignature, expectedSignature) {
		return errors.New("Invalid signature")
	}

	return nil
}

// canonicalQueryString reconstructs the string used for Pusher request
// signatures: every query parameter except auth_signature, sorted by key, with
// keys lowercased and values left in their decoded form.
func canonicalQueryString(query url.Values) (string, error) {
	keys := make([]string, 0, len(query))
	canonicalKey := make(map[string]string, len(query))

	for key, values := range query {
		lowerKey := strings.ToLower(key)

		if len(values) != 1 {
			return "", fmt.Errorf("Duplicate query parameter %q", lowerKey)
		}

		if lowerKey == "auth_signature" {
			continue
		}

		if _, exists := canonicalKey[lowerKey]; exists {
			return "", fmt.Errorf("Duplicate query parameter %q", lowerKey)
		}

		canonicalKey[lowerKey] = key
		keys = append(keys, lowerKey)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+query.Get(canonicalKey[key]))
	}

	return strings.Join(parts, "&"), nil
}
