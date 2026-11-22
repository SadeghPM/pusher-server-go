package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
)

var _ core.WebhookDispatcher = (*Dispatcher)(nil)

type WebhookPayload struct {
	TimeMs int64               `json:"time_ms"`
	Events []core.WebhookEvent `json:"events"`
}

type Dispatcher struct {
	configManager *config.Manager
	client        *http.Client
	Debugger      core.DebugNotifier

	// inFlight tracks webhook deliveries so a graceful shutdown can wait for
	// them instead of exiting while requests are still running.
	inFlight sync.WaitGroup
}

func NewDispatcher(manager *config.Manager, debugger core.DebugNotifier) *Dispatcher {
	if debugger == nil {
		debugger = core.NoopDebugNotifier{}
	}
	return &Dispatcher{
		configManager: manager,
		client:        newSafeClient(),
		Debugger:      debugger,
	}
}

// newSafeClient builds an HTTP client that cannot be used for blind SSRF:
// redirects are not followed, and every dial re-resolves the destination and
// refuses addresses in loopback/private/link-local ranges. Resolving again at
// dial time (instead of trusting config validation) also defeats DNS rebinding.
func newSafeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses for %q", host)
			}
			for _, ip := range ips {
				if config.IsBlockedIP(ip.IP) {
					return nil, fmt.Errorf("webhook destination %q resolves to a disallowed address", host)
				}
			}
			// Dialing a resolved IP keeps the original hostname for TLS SNI
			// (http.Transport derives ServerName from the request URL). Try
			// each address in turn so one unreachable record does not fail
			// delivery when another would work.
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		// A trusted URL must not be able to redirect us to an internal host.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: transport,
	}
}

func (d *Dispatcher) Dispatch(appID string, events []core.WebhookEvent) {
	appCfg := d.configManager.GetAppByID(appID)
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
		d.Debugger.Notify(appID, "webhook_error", "", "", "Failed to marshal webhook payload", err.Error())
		return
	}

	signature := generateSignature(appCfg.AppSecret, body)

	for _, webhookURL := range appCfg.Webhooks {
		// Config loading and the dashboard API validate webhook URLs before
		// storing them. The HTTP client re-validates every resolved address at
		// dial time, so no blocking DNS lookup is needed here (this runs on the
		// hub's ordered work queue).
		d.inFlight.Add(1)
		go func(url string) {
			defer d.inFlight.Done()
			d.sendWebhook(url, body, appCfg.AppKey, signature, appID)
		}(webhookURL)
	}
}

// Wait blocks until all webhook deliveries started by Dispatch have finished.
// Call it after the hubs have been closed and their work queues drained.
func (d *Dispatcher) Wait() {
	d.inFlight.Wait()
}

func (d *Dispatcher) sendWebhook(url string, body []byte, appKey, signature, appID string) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Failed to create webhook request", "error", err, "url", url, "app_id", appID)
		d.Debugger.Notify(appID, "webhook_error", "", "", "Failed to create webhook request", fmt.Sprintf("URL: %s, Error: %s", url, err.Error()))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pusher-Key", appKey)
	req.Header.Set("X-Pusher-Signature", signature)

	resp, err := d.client.Do(req)
	if err != nil {
		slog.Error("Failed to send webhook", "error", err, "url", url, "app_id", appID)
		d.Debugger.Notify(appID, "webhook_error", "", "", "Failed to send webhook", fmt.Sprintf("URL: %s, Error: %s", url, err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Webhook failed with non-2xx status", "status", resp.StatusCode, "url", url, "app_id", appID)
		d.Debugger.Notify(appID, "webhook_error", "", "", "Webhook failed with non-2xx status", fmt.Sprintf("URL: %s, Status: %d", url, resp.StatusCode))
	} else {
		d.Debugger.Notify(appID, "webhook_success", "", "", "Webhook sent successfully", fmt.Sprintf("URL: %s, Status: %d", url, resp.StatusCode))
	}
}

func generateSignature(secret string, data []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
