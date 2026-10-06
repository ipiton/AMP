package application

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	handlers "github.com/ipiton/AMP/internal/application/handlers"
	appconfig "github.com/ipiton/AMP/internal/config"
)

// newHealthyLiteRegistry is ready before shutdown starts, so any readiness
// failure in the tests below comes from BeginShutdown alone.
func newHealthyLiteRegistry() *ServiceRegistry {
	runtime := &contractStorageRuntime{}
	return &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileLite,
			Storage: appconfig.StorageConfig{Backend: appconfig.StorageBackendFilesystem},
		},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		storageRuntime: runtime,
		storage:        runtime,
		initialized:    true,
	}
}

func reportChecks(t *testing.T, report map[string]any) map[string]map[string]any {
	t.Helper()
	checks, ok := report["checks"].(map[string]map[string]any)
	if !ok {
		t.Fatalf("report checks have type %T", report["checks"])
	}
	return checks
}

// PROD-GRACEFUL-SHUTDOWN: once shutdown begins, readiness must fail so load
// balancers stop routing here, while liveness stays green so the kubelet does
// not kill the pod in the middle of the drain.
func TestServiceRegistryBeginShutdown_FailsReadinessOnly(t *testing.T) {
	registry := newHealthyLiteRegistry()
	ctx := context.Background()

	if err := registry.Readiness(ctx); err != nil {
		t.Fatalf("Readiness() before shutdown error = %v, want nil", err)
	}

	registry.BeginShutdown()

	if err := registry.Readiness(ctx); err == nil {
		t.Fatalf("Readiness() after BeginShutdown must fail")
	}
	if err := registry.Liveness(ctx); err != nil {
		t.Fatalf("Liveness() after BeginShutdown error = %v, want nil", err)
	}

	readiness := registry.ReadinessReport(ctx)
	if ready, _ := readiness["ready"].(bool); ready {
		t.Fatalf("ReadinessReport ready = true after BeginShutdown")
	}
	shutdown, ok := reportChecks(t, readiness)["shutdown"]
	if !ok {
		t.Fatalf("ReadinessReport must carry a shutdown check, got %#v", readiness["checks"])
	}
	if shutdown["status"] != "unhealthy" || shutdown["required"] != true {
		t.Fatalf("shutdown check = %#v, want required unhealthy", shutdown)
	}

	liveness := registry.LivenessReport(ctx)
	if _, ok := reportChecks(t, liveness)["shutdown"]; ok {
		t.Fatalf("LivenessReport must not carry the shutdown check, got %#v", liveness["checks"])
	}
	if liveness["status"] != "healthy" {
		t.Fatalf("LivenessReport status = %v, want healthy", liveness["status"])
	}
}

// The probes the chart uses: /-/ready flips to 503, /-/healthy stays 200.
func TestServiceRegistryBeginShutdown_ProbeEndpoints(t *testing.T) {
	registry := newHealthyLiteRegistry()
	ready := handlers.AlertmanagerReadyHandler(registry)
	healthy := handlers.AlertmanagerHealthyHandler(registry)

	probe := func(h http.HandlerFunc, path string) int {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	if got := probe(ready, "/-/ready"); got != http.StatusOK {
		t.Fatalf("/-/ready before shutdown = %d, want 200", got)
	}

	registry.BeginShutdown()

	if got := probe(ready, "/-/ready"); got != http.StatusServiceUnavailable {
		t.Fatalf("/-/ready after BeginShutdown = %d, want 503", got)
	}
	if got := probe(healthy, "/-/healthy"); got != http.StatusOK {
		t.Fatalf("/-/healthy after BeginShutdown = %d, want 200", got)
	}
}
