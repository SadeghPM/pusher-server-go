package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"pusher-clone/api"
	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/server"
)

func main() {
	cfg := config.LoadConfig("config.yaml")
	globalHub := core.NewGlobalHub()

	wsServer := server.NewServer(globalHub, cfg)
	restAPI := api.NewAPI(globalHub, cfg)

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// WebSocket endpoint for clients
		// URL example: /app/app-key?protocol=7&client=js&version=7.0.3&flash=false
		if strings.HasPrefix(path, "/app/") {
			// Extract appKey
			appKey := strings.TrimPrefix(path, "/app/")
			wsServer.HandleWebSocket(w, r, appKey)
			return
		}

		// REST API endpoint for backend (Laravel)
		// URL example: /apps/app-id/events
		if strings.HasPrefix(path, "/apps/") && strings.HasSuffix(path, "/events") {
			// Extract appID
			parts := strings.Split(path, "/")
			if len(parts) >= 4 {
				appID := parts[2]
				restAPI.HandleEvents(w, r, appID)
				return
			}
		}

		http.NotFound(w, r)
	})

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting Multi-Tenant Pusher clone server on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
