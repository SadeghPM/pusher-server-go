package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
)

type WebhookPayload struct {
	TimeMs int64               `json:"time_ms"`
	Events []core.WebhookEvent `json:"events"`
}

type Dispatcher struct {
	ConfigManager *config.Manager
	client        *http.Client
	DebugNotify   func(appID, eventType, socketID, channel, event, data string)
}

func NewDispatcher(manager *config.Manager) *Dispatcher {
	return &Dispatcher{
		ConfigManager: manager,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (d *Dispatcher) Dispatch(appID string, events []core.WebhookEvent) {
	appCfg := d.ConfigManager.GetAppByID(appID)
	if appCfg == nil || len(appCfg.Webhooks) == 0 {
		return
	}

	if len(events) == 0 {
		return
	}

	payload := WebhookPayload{
		TimeMs: time.Now().UnixMilli(),
		Events: events,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal webhook payload", "error", err, "app_id", appID)
		if d.DebugNotify != nil {
			d.DebugNotify(appID, "webhook_error", "", "", "Failed to marshal webhook payload", err.Error())
		}
		return
	}

	signature := generateSignature(appCfg.AppSecret, body)

	for _, webhookURL := range appCfg.Webhooks {
		go d.sendWebhook(webhookURL, body, appCfg.AppKey, signature, appID)
	}
}

func (d *Dispatcher) sendWebhook(url string, body []byte, appKey, signature, appID string) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Failed to create webhook request", "error", err, "url", url, "app_id", appID)
		if d.DebugNotify != nil {
			d.DebugNotify(appID, "webhook_error", "", "", "Failed to create webhook request", fmt.Sprintf("URL: %s, Error: %s", url, err.Error()))
		}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pusher-Key", appKey)
	req.Header.Set("X-Pusher-Signature", signature)

	resp, err := d.client.Do(req)
	if err != nil {
		slog.Error("Failed to send webhook", "error", err, "url", url, "app_id", appID)
		if d.DebugNotify != nil {
			d.DebugNotify(appID, "webhook_error", "", "", "Failed to send webhook", fmt.Sprintf("URL: %s, Error: %s", url, err.Error()))
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Webhook failed with non-2xx status", "status", resp.StatusCode, "url", url, "app_id", appID)
		if d.DebugNotify != nil {
			d.DebugNotify(appID, "webhook_error", "", "", "Webhook failed with non-2xx status", fmt.Sprintf("URL: %s, Status: %d", url, resp.StatusCode))
		}
	} else {
		if d.DebugNotify != nil {
			d.DebugNotify(appID, "webhook_success", "", "", "Webhook sent successfully", fmt.Sprintf("URL: %s, Status: %d", url, resp.StatusCode))
		}
	}
}

func generateSignature(secret string, data []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
