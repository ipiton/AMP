package main

import (
	"context"
	"flag"
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
	// -web.route-prefix mirrors upstream Alertmanager's flag of the same
	// name (PARITY-B6). When set, it overrides server.route_prefix from
	// config.
	routePrefixFlag := flag.String("web.route-prefix", "",
		"Prefix for the internal routes of web endpoints. Overrides server.route_prefix in config when set.")
	flag.Parse()

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
	// PARITY-B6: effective route prefix — explicit -web.route-prefix flag
	// wins; otherwise use server.route_prefix, falling back to inheriting
	// the path component of server.external_url (upstream's own default
	// derivation for --web.route-prefix), or no prefix if neither is set.
	cfg.Server.RoutePrefix = application.ResolveRoutePrefix(cfg.Server.RoutePrefix, cfg.Server.ExternalURL)
	if *routePrefixFlag != "" {
		cfg.Server.RoutePrefix = *routePrefixFlag
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

	// PARITY-B6: mount everything under server.route_prefix / -web.route-prefix
	// when configured. Empty prefix (the default) leaves mux unwrapped.
	rootHandler := application.WithRoutePrefix(mux, cfg.Server.RoutePrefix)

	// Start server
	port := cfg.Server.Port
	if port == 0 {
		port = 9093
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      rootHandler,
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
		"routePrefix", application.NormalizeRoutePrefix(cfg.Server.RoutePrefix),
		"dashboard", fmt.Sprintf("http://localhost:%d%s/dashboard", port, application.NormalizeRoutePrefix(cfg.Server.RoutePrefix)),
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
