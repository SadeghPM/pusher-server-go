package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"pusher-clone/api"
	"pusher-clone/config"
	"pusher-clone/core"
	"pusher-clone/server"
	"pusher-clone/webhook"

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

	// Setup Hot-Reload signal listener
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGHUP)
		for {
			<-sigChan
			slog.Info("Received SIGHUP, reloading configuration")
			if err := manager.Reload(); err != nil {
				slog.Error("Failed to reload configuration", "error", err)
			} else {
				if manager.GetConfig().Debug {
					logLevel.Set(slog.LevelDebug)
				} else {
					logLevel.Set(slog.LevelInfo)
				}
			}
		}
	}()

	webhookDispatcher := webhook.NewDispatcher(manager)
	globalHub := core.NewGlobalHub(webhookDispatcher)

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
