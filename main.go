package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"pusher-clone/api"
	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/dashboard"
	"pusher-clone/server"
	"pusher-clone/webhook"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	manager, err := config.NewManager("config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	cfg := manager.GetConfig()

	// Set up slog with dynamic level
	logLevel := new(slog.LevelVar)
	if cfg.Debug {
		logLevel.Set(slog.LevelDebug)
	} else {
		logLevel.Set(slog.LevelInfo)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Setup Hot-Reload via File Watcher using fsnotify
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			slog.Error("Failed to create file watcher for hot-reload", "error", err)
			return
		}
		defer watcher.Close()

		if err := watcher.Add("config.yaml"); err != nil {
			slog.Error("Failed to add config.yaml to file watcher", "error", err)
			return
		}

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					slog.Info("Detected config.yaml change via fsnotify, auto-reloading configuration")
					if err := manager.Reload(); err != nil {
						slog.Error("Failed to auto-reload configuration", "error", err)
					} else {
						if manager.GetConfig().Debug {
							logLevel.Set(slog.LevelDebug)
						} else {
							logLevel.Set(slog.LevelInfo)
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("File watcher error", "error", err)
			}
		}
	}()

	webhookDispatcher := webhook.NewDispatcher(manager)
	globalHub := core.NewGlobalHub(webhookDispatcher)

	// Setup Debug Observer
	debugObserver := dashboard.NewObserver()
	globalHub.DebugNotify = func(appID, eventType, socketID, channel, event, data string) {
		debugObserver.Notify(dashboard.DebugEvent{
			AppID:    appID,
			Type:     eventType,
			SocketID: socketID,
			Channel:  channel,
			Event:    event,
			Data:     data,
		})
	}
	webhookDispatcher.DebugNotify = globalHub.DebugNotify

	dashServer := dashboard.NewServer(debugObserver, manager, globalHub)
	go dashServer.Start()

	wsServer := server.NewServer(globalHub, manager)
	restAPI := api.NewAPI(globalHub, manager)

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

	// Start Prometheus metrics server on a separate port
	go func() {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.Handler())
		metricsAddr := fmt.Sprintf(":%s", cfg.MetricsPort)
		slog.Info("Starting Prometheus metrics server", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, metricsMux); err != nil {
			slog.Error("Metrics ListenAndServe failed", "error", err)
		}
	}()

	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("Starting Multi-Tenant Pusher clone server", "addr", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("ListenAndServe failed", "error", err)
		os.Exit(1)
	}
}
