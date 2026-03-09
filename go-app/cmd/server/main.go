package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ipiton/AMP/internal/application"
	"github.com/ipiton/AMP/internal/config"
)

const (
	appName    = "Alertmanager++"
	appVersion = "0.0.1"
)

const runtimeConfigFileEnv = "AMP_CONFIG_FILE"

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
	cfg, err := config.LoadConfig(resolveRuntimeConfigPath())
	if err != nil {
		slog.Warn("Config file not found, using defaults", "error", err)
		cfg = &config.Config{
			Server: config.ServerConfig{Port: 9093},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Service Registry
	registry, err := application.NewServiceRegistry(cfg, logger)
	if err != nil {
		slog.Error("Failed to create service registry", "error", err)
		os.Exit(1)
	}

	if err := registry.Initialize(ctx); err != nil {
		slog.Error("Failed to initialize services", "error", err)
		os.Exit(1)
	}

	// Initialize templates (dashboard)
	initTemplates()

	// Create HTTP mux and router
	mux := http.NewServeMux()
	router := application.NewRouter(registry)
	router.SetupRoutes(mux)

	// Dashboard and static files (legacy/compatibility)
	registerLegacyDashboardRoutes(mux, registry)

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

		if err := registry.Shutdown(shutdownCtx); err != nil {
			slog.Error("Registry shutdown error", "error", err)
		}

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}
	}()

	slog.Info("🎯 Server listening",
		"port", port,
		"dashboard", fmt.Sprintf("http://localhost:%d/dashboard", port),
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

func resolveRuntimeConfigPath() string {
	path := strings.TrimSpace(os.Getenv(runtimeConfigFileEnv))
	if path != "" {
		return path
	}
	return "config.yaml"
}
