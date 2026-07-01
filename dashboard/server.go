package dashboard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"pusher-clone/config"
	"pusher-clone/core"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all for admin dashboard
	},
}

type Server struct {
	Observer      *Observer
	ConfigManager *config.Manager
	GlobalHub     *core.GlobalHub
}

func NewServer(observer *Observer, manager *config.Manager, hub *core.GlobalHub) *Server {
	return &Server{
		Observer:      observer,
		ConfigManager: manager,
		GlobalHub:     hub,
	}
}

func (s *Server) Start() {
	cfg := s.ConfigManager.GetConfig()
	port := cfg.DashboardPort
	if port == "" {
		port = "5174"
	}

	mux := http.NewServeMux()

	// Auth Middleware
	authMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := s.ConfigManager.GetConfig().AdminToken

			// Check Authorization header or query param
			authHeader := r.Header.Get("Authorization")
			queryToken := r.URL.Query().Get("token")

			providedToken := ""
			if strings.HasPrefix(authHeader, "Bearer ") {
				providedToken = strings.TrimPrefix(authHeader, "Bearer ")
			} else if queryToken != "" {
				providedToken = queryToken
			}

			if providedToken != token {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/apps", authMiddleware(s.handleApps))
	mux.HandleFunc("/ws", authMiddleware(s.handleWebSocket))
	mux.HandleFunc("/api/trigger", authMiddleware(s.handleTrigger))

	// Serve static files from embedded FS
	mux.Handle("/", http.FileServer(http.FS(func() fs.FS {
		f, _ := fs.Sub(uiFS, "ui")
		return f
	}())))

	addr := fmt.Sprintf(":%s", port)
	slog.Info("Starting Dashboard server", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("Dashboard server failed", "error", err)
	}
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	cfg := s.ConfigManager.GetConfig()
	apps := make([]map[string]interface{}, 0)

	for _, app := range cfg.Apps {
		apps = append(apps, map[string]interface{}{
			"app_id":          app.AppID,
			"name":            app.AppID,
			"app_key":         app.AppKey,
			"app_secret":      app.AppSecret,
			"allowed_origins": app.AllowedOrigins,
			"webhooks":        app.Webhooks,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apps)
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

	ch := s.Observer.Subscribe()
	defer s.Observer.Unsubscribe(ch)

	for event := range ch {
		if event.AppID == appID {
			if err := conn.WriteJSON(event); err != nil {
				break
			}
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

	appCfg := s.ConfigManager.GetAppByID(req.AppID)
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

	restPayload := map[string]interface{}{
		"name":    req.Event,
		"channel": req.Channel,
		"data":    dataStr,
	}
	restBody, _ := json.Marshal(restPayload)

	hasher := md5.New()
	hasher.Write(restBody)
	bodyMD5 := hex.EncodeToString(hasher.Sum(nil))

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=1.0&body_md5=%s", appCfg.AppKey, timestamp, bodyMD5)

	stringToSign := fmt.Sprintf("POST\n/apps/%s/events\n%s", req.AppID, queryParams)
	mac := hmac.New(sha256.New, []byte(appCfg.AppSecret))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))

	port := s.ConfigManager.GetConfig().Port
	if port == "" {
		port = "6001"
	}

	url := fmt.Sprintf("http://localhost:%s/apps/%s/events?%s&auth_signature=%s", port, req.AppID, queryParams, signature)

	resp, err := http.Post(url, "application/json", bytes.NewReader(restBody))
	if err != nil {
		http.Error(w, "Failed to call internal API", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to call internal API", resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusOK)
}
