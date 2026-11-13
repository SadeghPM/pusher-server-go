package dashboard

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
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

type Server struct {
	observer      *Observer
	configManager *config.Manager
	globalHub     *core.GlobalHub
}

func NewServer(observer *Observer, manager *config.Manager, hub *core.GlobalHub) *Server {
	return &Server{
		observer:      observer,
		configManager: manager,
		globalHub:     hub,
	}
}

func (s *Server) Start(ctx context.Context) {
	cfg := s.configManager.GetConfig()
	port := cfg.DashboardPort
	if port == "" {
		port = "5174"
	}

	mux := http.NewServeMux()

	authMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := s.configManager.GetConfig().AdminToken
			if token == "" {
				http.Error(w, "Admin token is not configured", http.StatusServiceUnavailable)
				return
			}

			authHeader := r.Header.Get("Authorization")
			queryToken := r.URL.Query().Get("token")

			providedToken := ""
			if strings.HasPrefix(authHeader, "Bearer ") {
				providedToken = strings.TrimPrefix(authHeader, "Bearer ")
			} else if queryToken != "" {
				providedToken = queryToken
			}

			if subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/apps", authMiddleware(s.handleAppsRouter))
	mux.HandleFunc("/ws", authMiddleware(s.handleWebSocket))
	mux.HandleFunc("/api/trigger", authMiddleware(s.handleTrigger))
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cfg := s.configManager.GetConfig()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "online",
			"apps_count": len(cfg.Apps),
			"port":       cfg.Port,
			"host":       cfg.Host,
		})
	})
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
		Handler:           mux,
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
		http.Error(w, err.Error(), http.StatusConflict)
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
		http.Error(w, err.Error(), http.StatusNotFound)
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
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Tear down the tenant's hub so connected clients are disconnected and a
	// recreated app with the same ID starts from a clean state.
	s.globalHub.RemoveAppHub(req.AppID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "app_id": req.AppID})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
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
