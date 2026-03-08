package application

import (
	"net/http"
	"strings"

	"github.com/ipiton/AMP/internal/application/handlers"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Router handles HTTP routing and connects handlers to services.
type Router struct {
	registry *ServiceRegistry
}

// NewRouter creates a new router instance.
func NewRouter(registry *ServiceRegistry) *Router {
	return &Router{
		registry: registry,
	}
}

// SetupRoutes configures all HTTP routes on the provided mux.
func (rt *Router) SetupRoutes(mux *http.ServeMux) {
	// API v2
	mux.HandleFunc("/api/v2/alerts", handlers.AlertsHandler(rt.registry))
	mux.HandleFunc("/api/v2/silences", handlers.SilencesHandler(rt.registry))
	mux.HandleFunc("/api/v2/silence/", handlers.SilenceByIDHandler(rt.registry))

	// Health
	mux.HandleFunc("/health", handlers.HealthHandler)
	mux.HandleFunc("/ready", handlers.ReadyHandler)
	mux.HandleFunc("/healthz", handlers.HealthHandler)
	mux.HandleFunc("/readyz", handlers.ReadyHandler)
	mux.HandleFunc("/-/healthy", handlers.AlertmanagerHealthyHandler)
	mux.HandleFunc("/-/ready", handlers.AlertmanagerReadyHandler)

	// Metrics
	mux.Handle("/metrics", promhttp.Handler())

	// Fallback for unknown routes
	mux.HandleFunc("/-/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/-/") {
			handlers.NotFoundHandler(w, r)
		}
	})
}
