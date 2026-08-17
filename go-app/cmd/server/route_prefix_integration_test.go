package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/application"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// newRoutePrefixIntegrationMux builds the exact route composition main.go
// assembles — application.NewRouter(registry).SetupRoutes(mux) followed by
// registerLegacyDashboardRoutes(mux, registry) — against a real
// *application.ServiceRegistry, not a stub or a synthetic 2-route mux. This
// is what PARITY-B6's WithRoutePrefix actually wraps in production.
func newRoutePrefixIntegrationMux(t *testing.T) *http.ServeMux {
	t.Helper()

	// ServiceRegistry.Initialize registers Prometheus collectors via
	// promauto on the process-global registerer. Swap in a fresh registerer
	// per test so repeated registry creation across tests in this package
	// doesn't panic on duplicate collector registration (same technique as
	// futureparity_compat.go's newFutureParityCompatibilityRegistry).
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &appconfig.Config{
		Profile: appconfig.ProfileLite,
		Storage: appconfig.StorageConfig{
			Backend:        appconfig.StorageBackendFilesystem,
			FilesystemPath: filepath.Join(t.TempDir(), "route-prefix-integration.sqlite"),
		},
		Server: appconfig.ServerConfig{
			Port:                    9093,
			Host:                    "127.0.0.1",
			ReadTimeout:             30 * time.Second,
			WriteTimeout:            30 * time.Second,
			IdleTimeout:             120 * time.Second,
			GracefulShutdownTimeout: 30 * time.Second,
		},
		App: appconfig.AppConfig{
			Name:        "route-prefix-integration",
			Environment: "development",
		},
		Publishing: appconfig.PublishingConfig{
			Enabled: false,
		},
	}

	registry, err := application.NewServiceRegistry(cfg, logger)
	if err != nil {
		t.Fatalf("NewServiceRegistry() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := registry.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = registry.Shutdown(shutdownCtx)
	})

	initTemplates()

	mux := http.NewServeMux()
	application.NewRouter(registry).SetupRoutes(mux)
	registerLegacyDashboardRoutes(mux, registry)
	return mux
}

// TestRoutePrefixIntegration_RealRouterCompositionUnderPrefix exercises
// PARITY-B6's WithRoutePrefix wrapped around the actual production route
// composition (not a synthetic mux), matching main.go's wiring.
func TestRoutePrefixIntegration_RealRouterCompositionUnderPrefix(t *testing.T) {
	mux := newRoutePrefixIntegrationMux(t)
	wrapped := application.WithRoutePrefix(mux, "/am")

	t.Run("prefixed status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/am/api/v2/status", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /am/api/v2/status = %d, want 200; body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("prefixed v1 alerts post alias", func(t *testing.T) {
		payload := `[{"labels":{"alertname":"RoutePrefixIntegration"},"startsAt":"2026-03-08T10:00:00Z","status":"firing"}]`
		req := httptest.NewRequest(http.MethodPost, "/am/api/v1/alerts", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /am/api/v1/alerts = %d, want 200; body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("prefixed alertmanager healthy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/am/-/healthy", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /am/-/healthy = %d, want 200; body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("prefixed legacy dashboard route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/am/dashboard", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		// The dashboard renders against live registry state; a healthy lite
		// registry should render, but tolerate 500 the same way the wider
		// route-inventory contract does (dashboard rendering is not what
		// this test is verifying — prefix routing reaching it is).
		if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
			t.Fatalf("GET /am/dashboard = %d, want 200 or 500; body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("unprefixed status is 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v2/status (unprefixed) = %d, want 404", rec.Code)
		}
	})

	t.Run("bare root redirects to prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("GET / = %d, want 302", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/am/" {
			t.Fatalf("GET / Location = %q, want /am/", loc)
		}
	})
}

// TestRoutePrefixIntegration_InheritedFromExternalURL exercises the real
// router composition under a prefix derived from server.external_url's
// path (ResolveRoutePrefix), the same way main.go computes it when
// server.route_prefix and -web.route-prefix are both unset.
func TestRoutePrefixIntegration_InheritedFromExternalURL(t *testing.T) {
	mux := newRoutePrefixIntegrationMux(t)

	inherited := application.ResolveRoutePrefix("", "https://amp.example.com/monitoring")
	if inherited != "/monitoring" {
		t.Fatalf("ResolveRoutePrefix inherited = %q, want /monitoring", inherited)
	}
	wrapped := application.WithRoutePrefix(mux, inherited)

	req := httptest.NewRequest(http.MethodGet, "/monitoring/api/v2/status", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /monitoring/api/v2/status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	unprefixedReq := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	unprefixedRec := httptest.NewRecorder()
	wrapped.ServeHTTP(unprefixedRec, unprefixedReq)
	if unprefixedRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v2/status (unprefixed) = %d, want 404", unprefixedRec.Code)
	}
}
