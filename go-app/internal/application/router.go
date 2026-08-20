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
	mux.HandleFunc("/api/v2/alerts/groups", handlers.AlertGroupsHandler(rt.registry))
	mux.HandleFunc("/api/v2/silences", handlers.SilencesHandler(rt.registry))
	mux.HandleFunc("/api/v2/silence/", handlers.SilenceByIDHandler(rt.registry))
	mux.HandleFunc("/api/v2/status", handlers.StatusAPIHandler(rt.registry))
	mux.HandleFunc("/api/v2/receivers", handlers.ReceiversHandler(rt.registry))
	mux.HandleFunc("/api/v2/inhibitions", handlers.InhibitionsHandler(rt.registry))

	// API v1 — ingest alias (PARITY-4.3) + Investigation pipeline (PHASE-5B)
	// Register exact path first to prevent ServeMux from redirecting /api/v1/alerts → /api/v1/alerts/
	mux.HandleFunc("/api/v1/alerts", handlers.V1AlertsHandler(rt.registry))
	mux.HandleFunc("/api/v1/alerts/", handlers.InvestigationHandler(rt.registry))

	// Health
	mux.HandleFunc("/health", handlers.HealthHandler(rt.registry))
	mux.HandleFunc("/ready", handlers.ReadyHandler(rt.registry))
	mux.HandleFunc("/healthz", handlers.HealthHandler(rt.registry))
	mux.HandleFunc("/readyz", handlers.ReadyHandler(rt.registry))
	mux.HandleFunc("/-/healthy", handlers.AlertmanagerHealthyHandler(rt.registry))
	mux.HandleFunc("/-/ready", handlers.AlertmanagerReadyHandler(rt.registry))
	mux.HandleFunc("/-/reload", handlers.ReloadHandler(rt.registry))

	// Reload verification (INF-A slice 2). Deliberately NOT one of the probe
	// paths above: a config that needs a restart for one field is a healthy
	// process, and wiring this into liveness would crash-loop a pod over a
	// `metrics.path` edit. The config-reloader sidecar polls it after
	// signalling; operators can curl it to ask "is my ConfigMap edit live?".
	mux.HandleFunc("/health/reload", handlers.ReloadHealthHandler(rt.registry))

	// Metrics. Wrapped in the registry's exposition gate so `metrics.enabled`
	// is a live switch (INF-A slice 1) instead of dead config: a nil gate — a
	// router built before Initialize — keeps the pre-gate "always exposed"
	// behaviour.
	mux.Handle("/metrics", rt.registry.MetricsGate().Wrap(promhttp.Handler()))

	// Fallback for unknown routes
	mux.HandleFunc("/-/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/-/") {
			handlers.NotFoundHandler(w, r)
		}
	})
}
