package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	// Root context cancelled on SIGINT/SIGTERM for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager, err := config.NewManager(*configPath)
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

	// Setup Hot-Reload via File Watcher using fsnotify. Watch the containing
	// directory rather than the file itself so editors that save by renaming
	// (and atomic config writes) keep working.
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			slog.Error("Failed to create file watcher for hot-reload", "error", err)
			return
		}
		defer watcher.Close()

		configDir := filepath.Dir(*configPath)
		configBase := filepath.Base(*configPath)

		if err := watcher.Add(configDir); err != nil {
			slog.Error("Failed to watch configuration directory", "dir", configDir, "error", err)
			return
		}

		applyLogLevel := func(cfg *config.Config) {
			if cfg.Debug {
				logLevel.Set(slog.LevelDebug)
			} else {
				logLevel.Set(slog.LevelInfo)
			}
		}

		var reloadTimer *time.Timer
		var reloadC <-chan time.Time
		scheduleReload := func() {
			if reloadTimer != nil {
				reloadTimer.Stop()
			}
			reloadTimer = time.NewTimer(200 * time.Millisecond)
			reloadC = reloadTimer.C
		}

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != configBase {
					continue
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
					scheduleReload()
				}
			case <-reloadC:
				reloadC = nil
				slog.Info("Detected config change, auto-reloading configuration", "filename", *configPath)
				if err := manager.Reload(); err != nil {
					slog.Error("Failed to auto-reload configuration", "error", err)
					continue
				}
				applyLogLevel(manager.GetConfig())
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("File watcher error", "error", err)
			}
		}
	}()

	// Setup Debug Observer and NotifierAdapter
	debugObserver := dashboard.NewObserver()
	notifier := dashboard.NewNotifierAdapter(debugObserver)

	webhookDispatcher := webhook.NewDispatcher(manager, notifier)
	globalHub := core.NewGlobalHub(webhookDispatcher, notifier)

	dashServer := dashboard.NewServer(debugObserver, manager, globalHub)
	go dashServer.Start(ctx)

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
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsAddr := fmt.Sprintf(":%s", cfg.MetricsPort)
	slog.Info("Starting Prometheus metrics server", "addr", metricsAddr)

	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Metrics ListenAndServe failed", "error", err)
		}
	}()

	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("Starting Multi-Tenant Pusher clone server", "addr", addr)

	mainSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ListenAndServe failed", "error", err)
			os.Exit(1)
		}
	}()

	// Block until shutdown signal
	<-ctx.Done()
	slog.Info("Shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mainSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Main server shutdown error", "error", err)
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Metrics server shutdown error", "error", err)
	}

	// Close all AppHub work queues
	globalHub.Close()

	slog.Info("All servers stopped gracefully")
}
