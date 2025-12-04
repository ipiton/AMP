package application

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// Application represents the main application with all its dependencies.
//
// This struct coordinates the lifecycle of all services, handlers, and
// infrastructure components. It follows the Application pattern to avoid
// the God Object anti-pattern in main.go.
//
// Responsibilities:
//   - Load configuration
//   - Initialize services (database, cache, metrics)
//   - Initialize handlers (HTTP endpoints)
//   - Setup middleware (auth, logging, metrics)
//   - Start HTTP server
//   - Handle graceful shutdown
//
// Design Principles:
//   - Single Responsibility: Each component is separated
//   - Dependency Injection: All dependencies passed via constructors
//   - Lifecycle Management: Clear initialization and shutdown phases
//   - Testability: Each component can be tested independently
type Application struct {
	// Configuration
	config *appconfig.Config
	logger *slog.Logger

	// Infrastructure
	services   *ServiceRegistry
	handlers   *HandlerRegistry
	middleware *MiddlewareStack

	// HTTP Server
	server *http.Server
	mux    *http.ServeMux

	// Lifecycle
	shutdownFuncs []func() error
}

// New creates a new Application instance.
//
// This is the main constructor that initializes the application with
// the provided configuration.
//
// Example:
//
//	config := appconfig.Load()
//	app := application.New(config)
//	if err := app.Run(); err != nil {
//	    log.Fatal(err)
//	}
func New(config *appconfig.Config) *Application {
	return &Application{
		config:        config,
		logger:        slog.Default(),
		mux:           http.NewServeMux(),
		shutdownFuncs: make([]func() error, 0, 20),
	}
}

// Run starts the application and blocks until shutdown signal is received.
//
// This is the main entry point that:
//   1. Initializes all services
//   2. Registers all handlers
//   3. Starts the HTTP server
//   4. Waits for shutdown signal (SIGINT, SIGTERM)
//   5. Performs graceful shutdown
//
// Returns error if initialization or shutdown fails.
func (app *Application) Run() error {
	app.logger.Info("Starting Alert History Service",
		"version", "1.0.0",
		"profile", app.config.Profile)

	// Phase 1: Initialize Services
	if err := app.initializeServices(); err != nil {
		return fmt.Errorf("failed to initialize services: %w", err)
	}
	app.logger.Info("✅ Services initialized successfully")

	// Phase 2: Register Handlers
	if err := app.registerHandlers(); err != nil {
		return fmt.Errorf("failed to register handlers: %w", err)
	}
	app.logger.Info("✅ Handlers registered successfully")

	// Phase 3: Setup Middleware
	if err := app.setupMiddleware(); err != nil {
		return fmt.Errorf("failed to setup middleware: %w", err)
	}
	app.logger.Info("✅ Middleware configured successfully")

	// Phase 4: Start HTTP Server
	if err := app.startServer(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Phase 5: Wait for Shutdown Signal
	return app.waitForShutdown()
}

// initializeServices initializes all application services.
//
// Services include:
//   - Database (PostgreSQL/SQLite)
//   - Cache (Redis/Memory)
//   - Metrics Registry
//   - Alert Processor
//   - Classification Service
//   - Publishing Queue
//   - etc.
func (app *Application) initializeServices() error {
	ctx := context.Background()

	// Create ServiceRegistry
	registry, err := NewServiceRegistry(app.config, app.logger)
	if err != nil {
		return fmt.Errorf("failed to create service registry: %w", err)
	}

	// Initialize all services
	if err := registry.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize services: %w", err)
	}

	app.services = registry

	// Register shutdown functions for services
	app.shutdownFuncs = append(app.shutdownFuncs, func() error {
		return registry.Shutdown(ctx)
	})

	return nil
}

// registerHandlers registers all HTTP handlers.
//
// Handlers include:
//   - Prometheus alerts API (POST /api/v2/alerts)
//   - Query API (GET /api/v2/alerts)
//   - Silences API (POST/GET/DELETE /api/v2/silences)
//   - Dashboard UI (GET /ui/*)
//   - Health checks (GET /healthz, /readyz)
//   - Metrics (GET /metrics)
//   - etc.
func (app *Application) registerHandlers() error {
	// Create HandlerRegistry with services
	registry, err := NewHandlerRegistry(app.services, app.config, app.logger)
	if err != nil {
		return fmt.Errorf("failed to create handler registry: %w", err)
	}

	// Register all handlers on the mux
	if err := registry.RegisterAll(app.mux); err != nil {
		return fmt.Errorf("failed to register handlers: %w", err)
	}

	app.handlers = registry
	return nil
}

// setupMiddleware configures HTTP middleware.
//
// Middleware includes:
//   - Request logging
//   - Metrics collection
//   - Authentication
//   - Rate limiting
//   - CORS
//   - Compression
//   - Recovery (panic handler)
func (app *Application) setupMiddleware() error {
	// Create middleware stack
	stack, err := NewMiddlewareStack(app.config, app.services, app.logger)
	if err != nil {
		return fmt.Errorf("failed to create middleware stack: %w", err)
	}

	app.middleware = stack
	return nil
}

// startServer starts the HTTP server.
//
// The server listens on the configured port and handles requests
// with the middleware-wrapped mux.
func (app *Application) startServer() error {
	// Wrap mux with middleware
	handler := app.middleware.Wrap(app.mux)

	// Create HTTP server
	port := app.config.Server.Port
	if port == 0 {
		port = 8080
	}

	app.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		app.logger.Info("HTTP server starting", "port", port, "addr", app.server.Addr)
		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			app.logger.Error("HTTP server error", "error", err)
		}
	}()

	app.logger.Info("✅ HTTP server started successfully", "port", port)
	return nil
}

// waitForShutdown waits for shutdown signal and performs graceful shutdown.
//
// Shutdown signals: SIGINT (Ctrl+C), SIGTERM (Docker/Kubernetes stop)
//
// Graceful shutdown process:
//   1. Stop accepting new requests
//   2. Wait for in-flight requests to complete (30s timeout)
//   3. Shutdown all services (database, cache, etc.)
//   4. Exit cleanly
func (app *Application) waitForShutdown() error {
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigChan
	app.logger.Info("Shutdown signal received", "signal", sig.String())

	// Perform graceful shutdown
	return app.Shutdown()
}

// Shutdown performs graceful shutdown of the application.
//
// This method can be called explicitly for testing or will be called
// automatically when a shutdown signal is received.
func (app *Application) Shutdown() error {
	app.logger.Info("Initiating graceful shutdown...")

	// Shutdown HTTP server (stop accepting new requests, wait for in-flight)
	if app.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := app.server.Shutdown(ctx); err != nil {
			app.logger.Error("HTTP server shutdown error", "error", err)
		} else {
			app.logger.Info("✅ HTTP server shut down gracefully")
		}
	}

	// Shutdown all services (in reverse order of initialization)
	app.logger.Info("Shutting down services...")
	for i := len(app.shutdownFuncs) - 1; i >= 0; i-- {
		if err := app.shutdownFuncs[i](); err != nil {
			app.logger.Error("Service shutdown error", "error", err)
			// Continue shutting down other services
		}
	}
	app.logger.Info("✅ All services shut down")

	app.logger.Info("✅ Graceful shutdown complete")
	return nil
}

// Health checks if the application is healthy.
//
// Returns error if any critical component is unhealthy.
func (app *Application) Health() error {
	if app.services == nil {
		return fmt.Errorf("services not initialized")
	}

	return app.services.Health(context.Background())
}

// Readiness checks if the application is ready to serve requests.
//
// Returns error if any component is not ready.
func (app *Application) Readiness() error {
	if app.server == nil {
		return fmt.Errorf("HTTP server not started")
	}

	return app.Health()
}
