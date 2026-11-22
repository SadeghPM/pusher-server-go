package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// NotifierAdapter adapts Observer to the core.DebugNotifier interface.
type NotifierAdapter struct {
	observer *Observer
}

func NewNotifierAdapter(observer *Observer) *NotifierAdapter {
	return &NotifierAdapter{observer: observer}
}

func (n *NotifierAdapter) Notify(appID, eventType, socketID, channel, event, data string) {
	n.observer.Notify(DebugEvent{
		AppID:    appID,
		Type:     eventType,
		SocketID: socketID,
		Channel:  channel,
		Event:    event,
		Data:     data,
	})
}

// Compile-time interface check.
var _ core.DebugNotifier = (*NotifierAdapter)(nil)

const (
	// wsTicketTTL is how long a dashboard WebSocket ticket stays valid.
	wsTicketTTL = 30 * time.Second

	// authMaxFailures / authFailureWindow / authLockout bound online guessing
	// of the admin token. After too many bad tokens from one IP, that IP is
	// refused until the lockout expires.
	authMaxFailures   = 5
	authFailureWindow = 5 * time.Minute
	authLockout       = 5 * time.Minute

	// authMaxStates bounds memory during a distributed guessing attack.
	// Pruning runs at most once per interval so a burst of failures cannot
	// turn every attempt into an O(n) scan of the state map.
	authMaxStates     = 10000
	authPruneInterval = time.Minute
)

// authDecision is the outcome of an admin-token check.
type authDecision int

const (
	authAllowed authDecision = iota
	authRejected
	authLockedOut
)

// authState tracks failed admin-token attempts for a single client IP.
type authState struct {
	failures    int
	first       time.Time
	lockedUntil time.Time
}

// authThrottle is a small in-memory, per-IP brute-force guard for the admin
// token. It lives in the dashboard process and needs no external state.
type authThrottle struct {
	mu        sync.Mutex
	states    map[string]*authState
	lastPrune time.Time
}

func newAuthThrottle() *authThrottle {
	return &authThrottle{states: make(map[string]*authState)}
}

func (a *authThrottle) isLocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isLockedLocked(ip, time.Now())
}

// isLockedLocked reports whether ip is currently locked out. Must be called
// with a.mu held.
func (a *authThrottle) isLockedLocked(ip string, now time.Time) bool {
	state := a.states[ip]
	return state != nil && !state.lockedUntil.IsZero() && now.Before(state.lockedUntil)
}

func (a *authThrottle) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordFailureLocked(ip, time.Now())
}

// recordFailureLocked records a failed attempt. Must be called with a.mu held.
func (a *authThrottle) recordFailureLocked(ip string, now time.Time) {
	state := a.states[ip]
	if state == nil {
		state = &authState{}
		a.states[ip] = state
	}
	if now.Sub(state.first) >= authFailureWindow || state.first.IsZero() {
		state.first = now
		state.failures = 0
		state.lockedUntil = time.Time{}
	}
	state.failures++
	if state.failures >= authMaxFailures {
		state.lockedUntil = now.Add(authLockout)
	}

	a.pruneLocked(now)
}

// authorize checks a token and records the result while holding the lock, so
// concurrent guesses cannot all pass the lockout check before any failure is
// recorded.
func (a *authThrottle) authorize(ip, provided, expected string) authDecision {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	a.pruneLocked(now)
	if a.isLockedLocked(ip, now) {
		return authLockedOut
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
		delete(a.states, ip)
		return authAllowed
	}
	a.recordFailureLocked(ip, now)
	return authRejected
}

// pruneLocked drops states whose lockout or failure window has passed. It runs
// at most once per authPruneInterval, and evicts arbitrary entries only when
// the hard size cap is exceeded. Must be called with a.mu held.
func (a *authThrottle) pruneLocked(now time.Time) {
	// Scan at most once per interval. Eviction below keeps the map bounded
	// without turning every failure during a distributed attack into an O(n)
	// scan.
	if now.Sub(a.lastPrune) >= authPruneInterval {
		a.lastPrune = now
		for key, candidate := range a.states {
			if !candidate.lockedUntil.IsZero() {
				if !now.Before(candidate.lockedUntil) {
					delete(a.states, key)
				}
				continue
			}
			if !candidate.first.IsZero() && now.Sub(candidate.first) >= authFailureWindow {
				delete(a.states, key)
			}
		}
	}

	// If a distributed attack still exceeds the cap, evict entries rather
	// than growing without bound. This is best-effort memory protection.
	for len(a.states) > authMaxStates {
		for key := range a.states {
			delete(a.states, key)
			break
		}
	}
}

func (a *authThrottle) clear(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.states, ip)
}

// peerIP extracts the peer address from RemoteAddr (ignoring the port).
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientIP returns the address used for rate limiting. X-Forwarded-For is only
// consulted when the direct peer is a configured trusted proxy; otherwise any
// client could spoof the header and evade the lockout.
func (s *Server) clientIP(r *http.Request) string {
	peer := peerIP(r)
	cfg := s.configManager.GetConfig()
	peerAddr := net.ParseIP(peer)
	if cfg == nil || peerAddr == nil || !cfg.IsTrustedProxy(peerAddr) {
		return peer
	}

	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if xff == "" {
		return peer
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			// A malformed chain is untrustworthy; fall back to the peer.
			return peer
		}
		if !cfg.IsTrustedProxy(ip) {
			return ip.String()
		}
	}
	return peer
}

// securityHeaders applies defense-in-depth headers to every dashboard
// response. The CSP keeps all assets local (no third-party fonts or scripts)
// while still allowing the UI's small inline theme script and style attrs.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self' 'unsafe-inline'",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data:",
			"font-src 'self' data:",
			"connect-src 'self' ws: wss:",
			"object-src 'none'",
			"base-uri 'none'",
			"form-action 'self'",
			"frame-ancestors 'none'",
		}, "; "))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type Server struct {
	observer      *Observer
	configManager *config.Manager
	globalHub     *core.GlobalHub

	// One-time, short-lived tickets for opening the dashboard WebSocket
	// (/ws) without sending the long-lived admin token in the URL, where it
	// can leak into access logs.
	ticketsMu sync.Mutex
	tickets   map[string]time.Time // ticket -> expiry

	// authGuard throttles failed admin-token attempts per client IP.
	authGuard *authThrottle
}

func NewServer(observer *Observer, manager *config.Manager, hub *core.GlobalHub) *Server {
	return &Server{
		observer:      observer,
		configManager: manager,
		globalHub:     hub,
		tickets:       make(map[string]time.Time),
		authGuard:     newAuthThrottle(),
	}
}

func (s *Server) Start(ctx context.Context) {
	cfg := s.configManager.GetConfig()
	port := cfg.DashboardPort
	if port == "" {
		port = "5174"
	}

	mux := http.NewServeMux()

	// The admin token is only accepted via the Authorization header. Passing
	// it in a query string leaks it into access logs, browser history and
	// Referer headers, so ?token= is deliberately not supported anywhere.
	authMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := s.configManager.GetConfig().AdminToken
			if token == "" {
				http.Error(w, "Admin token is not configured", http.StatusServiceUnavailable)
				return
			}

			ip := s.clientIP(r)
			authHeader := r.Header.Get("Authorization")
			providedToken := ""
			if strings.HasPrefix(authHeader, "Bearer ") {
				providedToken = strings.TrimPrefix(authHeader, "Bearer ")
			}

			switch s.authGuard.authorize(ip, providedToken, token) {
			case authLockedOut:
				http.Error(w, "Too many failed attempts. Try again later.", http.StatusTooManyRequests)
				return
			case authRejected:
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/apps", authMiddleware(s.handleAppsRouter))
	// /ws does its own authorization and only accepts a one-time ticket
	// issued via POST /api/ticket; the long-lived token never appears in URLs.
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/trigger", authMiddleware(s.handleTrigger))
	mux.HandleFunc("/api/ticket", authMiddleware(s.handleTicket))
	// Liveness probe for the UI and uptime monitors. Keep it minimal: no port,
	// host or other deployment details are exposed to unauthenticated callers.
	mux.HandleFunc("/api/health", s.handleHealth)
	uiContent, err := fs.Sub(uiFS, "ui")
	if err != nil {
		slog.Error("Failed to load dashboard UI assets", "error", err)
		return
	}

	// /admin serves the control room. The landing page lives at /.
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, uiContent, "admin.html")
	})
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})

	mux.Handle("/", http.FileServer(http.FS(uiContent)))

	addr := fmt.Sprintf(":%s", port)
	slog.Info("Starting Dashboard server", "addr", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Dashboard server failed", "error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("Shutting down Dashboard server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Dashboard server shutdown error", "error", err)
	}
}

// handleHealth is an unauthenticated liveness probe. It intentionally omits
// deployment details such as the listening port and public host.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cfg := s.configManager.GetConfig()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "online",
		"apps_count": len(cfg.Apps),
	})
}

// appErrorStatus maps config.Manager errors to HTTP status codes. The zero
// value is handled by the default branch: unexpected failures are reported as
// 500 rather than being mistaken for client errors.
func appErrorStatus(err error) int {
	switch {
	case errors.Is(err, config.ErrAppExists):
		return http.StatusConflict
	case errors.Is(err, config.ErrAppInvalid):
		return http.StatusBadRequest
	case errors.Is(err, config.ErrAppNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) handleAppsRouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleApps(w, r)
	case http.MethodPost:
		s.handleCreateApp(w, r)
	case http.MethodPut:
		s.handleUpdateApp(w, r)
	case http.MethodDelete:
		s.handleDeleteApp(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func appResponse(app config.AppConfig, host string) map[string]interface{} {
	return map[string]interface{}{
		"app_id":          app.AppID,
		"name":            app.AppID,
		"app_key":         app.AppKey,
		"app_secret":      app.AppSecret,
		"allowed_origins": app.AllowedOrigins,
		"webhooks":        app.Webhooks,
		"host":            host,
	}
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	cfg := s.configManager.GetConfig()
	apps := make([]map[string]interface{}, 0)

	for _, app := range cfg.Apps {
		apps = append(apps, appResponse(app, cfg.Host))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apps)
}

type CreateAppRequest struct {
	AppID string `json:"app_id"`
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB limit
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var req CreateAppRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.AppID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}

	newApp := config.AppConfig{
		AppID: req.AppID,
	}

	if err := s.configManager.AddApp(newApp); err != nil {
		http.Error(w, err.Error(), appErrorStatus(err))
		return
	}

	created := s.configManager.GetAppByID(req.AppID)
	if created == nil {
		http.Error(w, "Internal error: app was created but could not be read back", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(appResponse(*created, s.configManager.GetConfig().Host))
}

type UpdateAppRequest struct {
	AppID          string   `json:"app_id"`
	AllowedOrigins []string `json:"allowed_origins"`
	Webhooks       []string `json:"webhooks"`
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB limit
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var req UpdateAppRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.AppID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}

	if err := s.configManager.UpdateApp(req.AppID, req.AllowedOrigins, req.Webhooks); err != nil {
		http.Error(w, err.Error(), appErrorStatus(err))
		return
	}

	updated := s.configManager.GetAppByID(req.AppID)
	if updated == nil {
		http.Error(w, "Internal error: app was updated but could not be read back", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(appResponse(*updated, s.configManager.GetConfig().Host))
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB limit
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var req struct {
		AppID string `json:"app_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.AppID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}

	if err := s.configManager.DeleteApp(req.AppID); err != nil {
		http.Error(w, err.Error(), appErrorStatus(err))
		return
	}

	// Tear down the tenant's hub so connected clients are disconnected and a
	// recreated app with the same ID starts from a clean state.
	s.globalHub.RemoveAppHub(req.AppID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "app_id": req.AppID})
}

// handleTicket issues a one-time, short-lived ticket for opening the /ws
// WebSocket. The request must already be authenticated with the admin token.
func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		slog.Error("Failed to generate WebSocket ticket", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	ticket := hex.EncodeToString(buf)

	s.ticketsMu.Lock()
	s.purgeExpiredTicketsLocked(time.Now())
	s.tickets[ticket] = time.Now().Add(wsTicketTTL)
	s.ticketsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"ticket": ticket})
}

// purgeExpiredTicketsLocked drops stale tickets. Must be called with
// s.ticketsMu held.
func (s *Server) purgeExpiredTicketsLocked(now time.Time) {
	for ticket, expiry := range s.tickets {
		if now.After(expiry) {
			delete(s.tickets, ticket)
		}
	}
}

// wsAuthResult is the outcome of authorizing a dashboard WebSocket request.
type wsAuthResult int

const (
	wsAuthOK wsAuthResult = iota
	wsAuthUnauthorized
	wsAuthLockedOut
)

// authorizeWebSocket validates the /ws request. A one-time ticket is required
// for browser clients so the long-lived admin token never enters a URL. An
// Authorization: Bearer header is also accepted for non-browser clients and is
// subject to the same brute-force lockout as the REST API.
func (s *Server) authorizeWebSocket(r *http.Request) wsAuthResult {
	token := s.configManager.GetConfig().AdminToken
	if token == "" {
		return wsAuthUnauthorized
	}

	if ticket := r.URL.Query().Get("ticket"); ticket != "" {
		s.ticketsMu.Lock()
		expiry, ok := s.tickets[ticket]
		if ok {
			// Consume the ticket: it may only ever be used once.
			delete(s.tickets, ticket)
		}
		s.ticketsMu.Unlock()
		if ok && time.Now().Before(expiry) {
			return wsAuthOK
		}
		return wsAuthUnauthorized
	}

	// A token in the URL is never accepted: query strings end up in access
	// logs, browser history and Referer headers.
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return wsAuthUnauthorized
	}
	providedToken := strings.TrimPrefix(authHeader, "Bearer ")

	switch s.authGuard.authorize(s.clientIP(r), providedToken, token) {
	case authAllowed:
		return wsAuthOK
	case authLockedOut:
		return wsAuthLockedOut
	default:
		return wsAuthUnauthorized
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	switch s.authorizeWebSocket(r) {
	case wsAuthLockedOut:
		http.Error(w, "Too many failed attempts. Try again later.", http.StatusTooManyRequests)
		return
	case wsAuthUnauthorized:
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade dashboard websocket", "error", err)
		return
	}
	defer conn.Close()

	ch := s.observer.Subscribe()
	defer s.observer.Unsubscribe(ch)

	// Read (and discard) client frames so a closed browser tab is noticed and
	// the subscriber is removed promptly instead of leaking a goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(1024)
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.AppID != appID {
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

type TriggerRequest struct {
	AppID   string      `json:"app_id"`
	Channel string      `json:"channel"`
	Event   string      `json:"event"`
	Data    interface{} `json:"data"`
}

func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1*1024*1024) // 1MB limit
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var req TriggerRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if req.Channel == "" || req.Event == "" {
		http.Error(w, "channel and event are required", http.StatusBadRequest)
		return
	}
	if !core.IsValidChannelName(req.Channel) || len(req.Event) > 200 {
		http.Error(w, "Invalid channel or event name", http.StatusBadRequest)
		return
	}

	appCfg := s.configManager.GetAppByID(req.AppID)
	if appCfg == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	var dataStr string
	switch v := req.Data.(type) {
	case string:
		dataStr = v
	default:
		b, _ := json.Marshal(v)
		dataStr = string(b)
	}

	appHub := s.globalHub.GetOrCreateAppHub(req.AppID)
	escapedData, _ := json.Marshal(dataStr)
	message := core.MarshalEvent(req.Event, req.Channel, escapedData, "")
	appHub.BroadcastToChannel(req.Channel, message, "")
	s.globalHub.Debugger.Notify(req.AppID, "api_message", "", req.Channel, req.Event, dataStr)

	w.WriteHeader(http.StatusOK)
}
