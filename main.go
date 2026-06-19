package main

import (
	"fmt"
	"log"
	"net/http"

	"pusher-clone/api"
	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/server"
)

func main() {
	cfg := config.LoadConfig()
	hub := core.NewHub()

	wsServer := server.NewServer(hub, cfg)
	restAPI := api.NewAPI(hub, cfg)

	mux := http.NewServeMux()

	// WebSocket endpoint for clients
	// URL example: /app/app-key?protocol=7&client=js&version=7.0.3&flash=false
	mux.HandleFunc(fmt.Sprintf("/app/%s", cfg.AppKey), wsServer.HandleWebSocket)

	// REST API endpoint for backend (Laravel)
	// URL example: /apps/app-id/events
	mux.HandleFunc(fmt.Sprintf("/apps/%s/events", cfg.AppID), restAPI.HandleEvents)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting Pusher clone server on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
