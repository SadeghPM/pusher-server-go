package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
)

func newDashboardTestServer() *Server {
	cfg := &config.Config{
		AdminToken: "secret-token",
		Apps: []config.AppConfig{
			{AppID: "app1", AppKey: "key1", AppSecret: "secret1"},
		},
	}
	return NewServer(NewObserver(), config.NewManagerFromConfig(cfg), core.NewGlobalHub(nil, nil))
}

func TestWebSocketTicketFlow(t *testing.T) {
	s := newDashboardTestServer()

	// Unauthenticated ticket requests must be rejected by the caller's auth
	// middleware; handleTicket itself only issues tickets.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ticket", nil)
	s.handleTicket(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Expected 201 for ticket issuance, got %d", rr.Code)
	}

	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Ticket == "" {
		t.Fatalf("Expected a ticket in the response, got: %s", rr.Body.String())
	}

	// A ticket authorizes the WebSocket request...
	wsReq := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&ticket="+body.Ticket, nil)
	if s.authorizeWebSocket(wsReq) != wsAuthOK {
		t.Fatal("Expected a fresh ticket to authorize the WebSocket request")
	}

	// ...exactly once.
	if s.authorizeWebSocket(wsReq) != wsAuthUnauthorized {
		t.Fatal("Expected a used ticket to be rejected on a second attempt")
	}
}

func TestWebSocketTicketExpiry(t *testing.T) {
	s := newDashboardTestServer()

	s.ticketsMu.Lock()
	s.tickets["expired-ticket"] = time.Now().Add(-time.Second)
	s.ticketsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&ticket=expired-ticket", nil)
	if s.authorizeWebSocket(req) != wsAuthUnauthorized {
		t.Fatal("Expected an expired ticket to be rejected")
	}
}

func TestWebSocketRejectsQueryToken(t *testing.T) {
	s := newDashboardTestServer()

	// The long-lived token must never be accepted in a URL: access logs and
	// browser history would leak it. Tickets are the only browser credential.
	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&token=secret-token", nil); s.authorizeWebSocket(req) != wsAuthUnauthorized {
		t.Error("Expected the admin token in the query string to be rejected")
	}
	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&token=wrong-token", nil); s.authorizeWebSocket(req) != wsAuthUnauthorized {
		t.Error("Expected a wrong token in the query string to be rejected")
	}
	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil); s.authorizeWebSocket(req) != wsAuthUnauthorized {
		t.Error("Expected a request with no credentials to be rejected")
	}

	// A Bearer header remains available to non-browser clients.
	req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	if s.authorizeWebSocket(req) != wsAuthOK {
		t.Error("Expected a Bearer token to authorize the request")
	}
}

func TestWebSocketBearerAttemptsAreThrottled(t *testing.T) {
	s := newDashboardTestServer()

	for i := 0; i < authMaxFailures; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		if got := s.authorizeWebSocket(req); got != wsAuthUnauthorized {
			t.Fatalf("Attempt %d: expected unauthorized, got %v", i+1, got)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	if got := s.authorizeWebSocket(req); got != wsAuthLockedOut {
		t.Fatalf("Expected the WebSocket path to honor the lockout, got %v", got)
	}

	// Even the correct token is refused while the lockout is active.
	req = httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	if got := s.authorizeWebSocket(req); got != wsAuthLockedOut {
		t.Fatalf("Expected the correct token to be refused during lockout, got %v", got)
	}
}

func TestClientIPTrustsForwardedForOnlyFromProxy(t *testing.T) {
	cfg := &config.Config{
		AdminToken:     "secret-token",
		TrustedProxies: []string{"10.0.0.1", "192.0.2.0/24"},
	}
	s := NewServer(NewObserver(), config.NewManagerFromConfig(cfg), core.NewGlobalHub(nil, nil))

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"direct client ignores XFF", "198.51.100.5:1234", "203.0.113.9", "198.51.100.5"},
		{"trusted proxy uses XFF", "10.0.0.1:1234", "203.0.113.9", "203.0.113.9"},
		{"rightmost untrusted in chain", "10.0.0.1:1234", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		{"skips trusted proxies", "10.0.0.1:1234", "203.0.113.9, 192.0.2.7", "203.0.113.9"},
		{"malformed XFF falls back to peer", "10.0.0.1:1234", "not-an-ip", "10.0.0.1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Forwarded-For", tc.xff)
			if got := s.clientIP(req); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthThrottleLocksAfterFailures(t *testing.T) {
	guard := newAuthThrottle()
	for i := 0; i < authMaxFailures; i++ {
		if guard.isLocked("192.0.2.10") {
			t.Fatal("Locked out too early")
		}
		guard.recordFailure("192.0.2.10")
	}
	if !guard.isLocked("192.0.2.10") {
		t.Error("Expected the IP to be locked after repeated failures")
	}
	if guard.isLocked("192.0.2.11") {
		t.Error("Other IPs must not be affected")
	}
	guard.clear("192.0.2.10")
	if guard.isLocked("192.0.2.10") {
		t.Error("Expected a successful login to clear the lock")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Content-Security-Policy":      "default-src 'self'",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		got := rr.Header().Get(header)
		if want == "default-src 'self'" {
			if !strings.Contains(got, want) {
				t.Errorf("%s = %q, want it to contain %q", header, got, want)
			}
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if csp := rr.Header().Get("Content-Security-Policy"); strings.Contains(csp, "fonts.googleapis.com") {
		t.Errorf("CSP must not allow third-party font hosts: %s", csp)
	}
}

func TestHealthOmitsDeploymentDetails(t *testing.T) {
	cfg := &config.Config{
		AdminToken: "secret-token",
		Port:       "6001",
		Host:       "https://internal.example.com",
		Apps:       []config.AppConfig{{AppID: "app1", AppKey: "key1", AppSecret: "secret1"}},
	}
	s := NewServer(NewObserver(), config.NewManagerFromConfig(cfg), core.NewGlobalHub(nil, nil))

	rr := httptest.NewRecorder()
	s.handleHealth(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	body := rr.Body.String()
	if !strings.Contains(body, `"status":"online"`) || !strings.Contains(body, `"apps_count":1`) {
		t.Errorf("Unexpected health body: %s", body)
	}
	if strings.Contains(body, "6001") || strings.Contains(body, "internal.example.com") {
		t.Errorf("Health endpoint leaked deployment details: %s", body)
	}
}

func TestAppErrorStatus(t *testing.T) {
	if got := appErrorStatus(config.ErrAppExists); got != http.StatusConflict {
		t.Errorf("Expected 409 for ErrAppExists, got %d", got)
	}
	if got := appErrorStatus(config.ErrAppInvalid); got != http.StatusBadRequest {
		t.Errorf("Expected 400 for ErrAppInvalid, got %d", got)
	}
	if got := appErrorStatus(config.ErrAppNotFound); got != http.StatusNotFound {
		t.Errorf("Expected 404 for ErrAppNotFound, got %d", got)
	}
	if got := appErrorStatus(nil); got != http.StatusInternalServerError {
		t.Errorf("Expected 500 for unknown errors, got %d", got)
	}
}
