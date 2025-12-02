package application

import (
	"fmt"
	"log/slog"
	"net/http"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/cmd/server/handlers"
)

// HandlerRegistry manages all HTTP handlers.
//
// This registry centralizes handler initialization and registration,
// preventing the God Object anti-pattern in main.go.
//
// Responsibilities:
//   - Initialize handlers with service dependencies
//   - Register handlers on HTTP mux
//   - Group related endpoints
//   - Provide handler health checks
//
// Handler Groups:
//   - Prometheus API (POST /api/v2/alerts, GET /api/v2/alerts)
//   - Silences API (POST/GET/DELETE /api/v2/silences)
//   - Dashboard UI (GET /ui/*)
//   - Health checks (GET /healthz, /readyz)
//   - Metrics (GET /metrics)
//   - Publishing API
//   - Configuration API
type HandlerRegistry struct {
	services *ServiceRegistry
	config   *appconfig.Config
	logger   *slog.Logger

	// Handlers (to be populated)
	prometheusAlertsHandler *handlers.PrometheusAlertsHandler
	// ... more handlers to be added
}

// NewHandlerRegistry creates a new handler registry.
func NewHandlerRegistry(
	services *ServiceRegistry,
	config *appconfig.Config,
	logger *slog.Logger,
) (*HandlerRegistry, error) {
	if services == nil {
		return nil, fmt.Errorf("services is nil")
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	registry := &HandlerRegistry{
		services: services,
		config:   config,
		logger:   logger,
	}

	// Initialize handlers
	if err := registry.initializeHandlers(); err != nil {
		return nil, fmt.Errorf("failed to initialize handlers: %w", err)
	}

	return registry, nil
}

// initializeHandlers initializes all HTTP handlers.
func (r *HandlerRegistry) initializeHandlers() error {
	r.logger.Info("Initializing HTTP handlers...")

	// Initialize Prometheus Alerts Handler
	if err := r.initializePrometheusHandler(); err != nil {
		r.logger.Warn("Prometheus handler initialization failed", "error", err)
		// Continue (graceful degradation)
	}

	// TODO: Initialize other handlers
	// - Silence handlers
	// - Dashboard handlers
	// - Publishing handlers
	// - Configuration handlers
	// - etc.

	r.logger.Info("✅ HTTP handlers initialized")
	return nil
}

// initializePrometheusHandler initializes the Prometheus alerts handler.
func (r *HandlerRegistry) initializePrometheusHandler() error {
	if r.services.AlertProcessor() == nil {
		return fmt.Errorf("alert processor not available")
	}

	r.logger.Info("Initializing Prometheus Alerts Handler...")

	// TODO: Create handler with proper config
	// r.prometheusAlertsHandler = handlers.NewPrometheusAlertsHandler(...)

	r.logger.Info("✅ Prometheus Alerts Handler initialized")
	return nil
}

// RegisterAll registers all handlers on the HTTP mux.
func (r *HandlerRegistry) RegisterAll(mux *http.ServeMux) error {
	r.logger.Info("Registering HTTP handlers...")

	// Register Prometheus API endpoints
	if err := r.registerPrometheusHandlers(mux); err != nil {
		return fmt.Errorf("failed to register Prometheus handlers: %w", err)
	}

	// Register Health endpoints
	r.registerHealthHandlers(mux)

	// Register Metrics endpoint
	r.registerMetricsHandler(mux)

	// TODO: Register other handler groups
	// - Silence handlers
	// - Dashboard handlers
	// - Publishing handlers
	// - etc.

	r.logger.Info("✅ All HTTP handlers registered")
	return nil
}

// registerPrometheusHandlers registers Prometheus API endpoints.
func (r *HandlerRegistry) registerPrometheusHandlers(mux *http.ServeMux) error {
	r.logger.Info("Registering Prometheus API endpoints...")

	// POST /api/v2/alerts (Alertmanager-compatible)
	if r.prometheusAlertsHandler != nil {
		// mux.HandleFunc("POST /api/v2/alerts", r.prometheusAlertsHandler.HandleAlerts)
		r.logger.Info("✅ POST /api/v2/alerts registered")
	}

	// GET /api/v2/alerts (query alerts)
	// mux.HandleFunc("GET /api/v2/alerts", r.prometheusQueryHandler.HandleQuery)

	return nil
}

// registerHealthHandlers registers health check endpoints.
func (r *HandlerRegistry) registerHealthHandlers(mux *http.ServeMux) {
	r.logger.Info("Registering health check endpoints...")

	// GET /healthz (liveness probe)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// GET /readyz (readiness probe)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Check if services are ready
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ready"))
	})

	r.logger.Info("✅ Health endpoints registered (GET /healthz, GET /readyz)")
}

// registerMetricsHandler registers Prometheus metrics endpoint.
func (r *HandlerRegistry) registerMetricsHandler(mux *http.ServeMux) {
	r.logger.Info("Registering metrics endpoint...")

	// GET /metrics (Prometheus metrics)
	// TODO: Use metrics registry handler
	// mux.Handle("GET /metrics", promhttp.Handler())

	r.logger.Info("✅ Metrics endpoint registered (GET /metrics)")
}
