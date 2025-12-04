package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/pkg/metrics"
)

const (
	appName    = "Alertmanager++"
	appVersion = "0.0.1"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("🚀 Starting Alertmanager++",
		"version", appVersion,
		"profile", "OSS Core",
	)

	// Load configuration
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		slog.Warn("Config file not found, using defaults", "error", err)
		cfg = &config.Config{
			Server: config.ServerConfig{Port: 9093},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize business metrics
	businessMetrics := metrics.NewBusinessMetrics()
	_ = businessMetrics // Used for metrics recording
	slog.Info("✅ Metrics initialized")

	// Create HTTP mux
	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// API endpoints (stub for now)
	mux.HandleFunc("/api/v2/alerts", alertsHandler)
	mux.HandleFunc("/api/v2/silences", silencesHandler)
	mux.HandleFunc("/api/v2/status", statusHandler)

	// Alertmanager-compatible webhook endpoint
	mux.HandleFunc("/webhook", webhookHandler)

	// Start server
	port := cfg.Server.Port
	if port == 0 {
		port = 9093
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		slog.Info("Shutting down server...")
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 30*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}
	}()

	slog.Info("🎯 Server listening",
		"port", port,
		"health", "/health",
		"metrics", "/metrics",
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

// healthHandler returns server health status
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy","version":"` + appVersion + `"}`))
}

// readyHandler returns readiness status
func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ready":true}`))
}

// alertsHandler handles Alertmanager-compatible alerts API
func alertsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		w.Write([]byte(`{"status":"success","data":[]}`))
	case http.MethodPost:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// silencesHandler handles silences API
func silencesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"success","data":[]}`))
}

// statusHandler returns Alertmanager-compatible status
func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{
		"cluster": {"status": "ready"},
		"versionInfo": {"version": "` + appVersion + `"},
		"config": {"original": ""},
		"uptime": "0s"
	}`))
}

// webhookHandler receives alert webhooks
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// For now, just acknowledge receipt
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"received"}`))
}
