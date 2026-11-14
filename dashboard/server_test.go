package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if !s.authorizeWebSocket(wsReq) {
		t.Fatal("Expected a fresh ticket to authorize the WebSocket request")
	}

	// ...exactly once.
	if s.authorizeWebSocket(wsReq) {
		t.Fatal("Expected a used ticket to be rejected on a second attempt")
	}
}

func TestWebSocketTicketExpiry(t *testing.T) {
	s := newDashboardTestServer()

	s.ticketsMu.Lock()
	s.tickets["expired-ticket"] = time.Now().Add(-time.Second)
	s.ticketsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&ticket=expired-ticket", nil)
	if s.authorizeWebSocket(req) {
		t.Fatal("Expected an expired ticket to be rejected")
	}
}

func TestWebSocketTokenFallback(t *testing.T) {
	s := newDashboardTestServer()

	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&token=secret-token", nil); !s.authorizeWebSocket(req) {
		t.Error("Expected the admin token in the query to authorize the request")
	}
	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1&token=wrong-token", nil); s.authorizeWebSocket(req) {
		t.Error("Expected a wrong token to be rejected")
	}
	if req := httptest.NewRequest(http.MethodGet, "/ws?app_id=app1", nil); s.authorizeWebSocket(req) {
		t.Error("Expected a request with no credentials to be rejected")
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
