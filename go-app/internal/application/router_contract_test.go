package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

type contractPublisher struct{}

func (p *contractPublisher) PublishToAll(_ context.Context, _ *core.Alert) error {
	return nil
}

func (p *contractPublisher) PublishWithClassification(_ context.Context, _ *core.Alert, _ *core.ClassificationResult) error {
	return nil
}

type contractFilterEngine struct{}

func (f *contractFilterEngine) ShouldBlock(_ *core.Alert, _ *core.ClassificationResult) (bool, string) {
	return false, ""
}

type contractStorageRuntime struct {
	healthErr error
}

func (s *contractStorageRuntime) SaveAlert(context.Context, *core.Alert) error {
	return nil
}

func (s *contractStorageRuntime) GetAlertByFingerprint(context.Context, string) (*core.Alert, error) {
	return nil, core.ErrAlertNotFound
}

func (s *contractStorageRuntime) ListAlerts(context.Context, *core.AlertFilters) (*core.AlertList, error) {
	return &core.AlertList{}, nil
}

func (s *contractStorageRuntime) UpdateAlert(context.Context, *core.Alert) error {
	return nil
}

func (s *contractStorageRuntime) DeleteAlert(context.Context, string) error {
	return nil
}

func (s *contractStorageRuntime) GetAlertStats(context.Context) (*core.AlertStats, error) {
	return &core.AlertStats{}, nil
}

func (s *contractStorageRuntime) CleanupOldAlerts(context.Context, int) (int, error) {
	return 0, nil
}

func (s *contractStorageRuntime) Health(context.Context) error {
	return s.healthErr
}

func (s *contractStorageRuntime) Disconnect(context.Context) error {
	return nil
}

func newActiveContractMux(t *testing.T, storageHealthErr error) *http.ServeMux {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	processor, err := services.NewAlertProcessor(services.AlertProcessorConfig{
		FilterEngine: &contractFilterEngine{},
		Publisher:    &contractPublisher{},
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("NewAlertProcessor() error = %v", err)
	}

	storageRuntime := &contractStorageRuntime{healthErr: storageHealthErr}
	registry := &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileLite,
			Storage: appconfig.StorageConfig{
				Backend:        appconfig.StorageBackendFilesystem,
				FilesystemPath: ":memory:",
			},
		},
		logger:         logger,
		alertStore:     memory.NewAlertStore(),
		silenceStore:   memory.NewSilenceStore(),
		alertProcessor: processor,
		storageRuntime: storageRuntime,
		storage:        storageRuntime,
		initialized:    true,
	}

	mux := http.NewServeMux()
	NewRouter(registry).SetupRoutes(mux)
	return mux
}

func TestActiveRuntimeContract_PresentEndpoints(t *testing.T) {
	mux := newActiveContractMux(t, nil)

	alertPayload := `[
		{
			"labels": {"alertname":"ActiveRuntimeAlert","service":"amp"},
			"annotations": {"summary":"active runtime"},
			"startsAt": "2026-03-08T10:00:00Z",
			"status": "firing"
		}
	]`

	silencePayload := `{
		"matchers": [{"name":"alertname","value":"ActiveRuntimeAlert","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "active-runtime-contract",
		"comment": "maintenance window"
	}`

	probes := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "alerts get", method: http.MethodGet, path: "/api/v2/alerts", status: http.StatusOK},
		{name: "alerts post", method: http.MethodPost, path: "/api/v2/alerts", body: alertPayload, status: http.StatusOK},
		{name: "alerts put not allowed", method: http.MethodPut, path: "/api/v2/alerts", status: http.StatusMethodNotAllowed},
		{name: "silences get", method: http.MethodGet, path: "/api/v2/silences", status: http.StatusOK},
		{name: "silences post", method: http.MethodPost, path: "/api/v2/silences", body: silencePayload, status: http.StatusOK},
		{name: "silence by id get", method: http.MethodGet, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusNotFound},
		{name: "silence by id delete", method: http.MethodDelete, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusNotFound},
		{name: "silence by id post not allowed", method: http.MethodPost, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusMethodNotAllowed},
		{name: "health", method: http.MethodGet, path: "/health", status: http.StatusOK},
		{name: "ready", method: http.MethodGet, path: "/ready", status: http.StatusOK},
		{name: "healthz", method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{name: "readyz", method: http.MethodGet, path: "/readyz", status: http.StatusOK},
		{name: "alertmanager healthy get", method: http.MethodGet, path: "/-/healthy", status: http.StatusOK},
		{name: "alertmanager ready get", method: http.MethodGet, path: "/-/ready", status: http.StatusOK},
		{name: "metrics", method: http.MethodGet, path: "/metrics", status: http.StatusOK},
	}

	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			req := httptest.NewRequest(probe.method, probe.path, bytes.NewBufferString(probe.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != probe.status {
				t.Fatalf("%s %s expected %d, got %d body=%q", probe.method, probe.path, probe.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent(t *testing.T) {
	mux := newActiveContractMux(t, nil)

	probes := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "status not mounted", method: http.MethodGet, path: "/api/v2/status", status: http.StatusNotFound},
		{name: "receivers not mounted", method: http.MethodGet, path: "/api/v2/receivers", status: http.StatusNotFound},
		{name: "alert groups not mounted", method: http.MethodGet, path: "/api/v2/alerts/groups", status: http.StatusNotFound},
		{name: "reload not mounted", method: http.MethodPost, path: "/-/reload", status: http.StatusNotFound},
		{name: "alerts v1 alias not mounted", method: http.MethodPost, path: "/api/v1/alerts", status: http.StatusNotFound},
		{name: "config api not mounted", method: http.MethodGet, path: "/api/v2/config", status: http.StatusNotFound},
		{name: "classification api not mounted", method: http.MethodGet, path: "/api/v2/classification/health", status: http.StatusNotFound},
		{name: "history api not mounted", method: http.MethodGet, path: "/history", status: http.StatusNotFound},
	}

	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			req := httptest.NewRequest(probe.method, probe.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != probe.status {
				t.Fatalf("%s %s expected %d, got %d body=%q", probe.method, probe.path, probe.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestActiveRuntimeContract_HealthEndpointsReflectReadiness(t *testing.T) {
	mux := newActiveContractMux(t, errors.New("storage unavailable"))

	probes := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "health stays live", method: http.MethodGet, path: "/health", status: http.StatusOK},
		{name: "healthz stays live", method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{name: "ready fails", method: http.MethodGet, path: "/ready", status: http.StatusServiceUnavailable},
		{name: "readyz fails", method: http.MethodGet, path: "/readyz", status: http.StatusServiceUnavailable},
		{name: "alertmanager healthy stays live", method: http.MethodGet, path: "/-/healthy", status: http.StatusOK},
		{name: "alertmanager ready fails", method: http.MethodGet, path: "/-/ready", status: http.StatusServiceUnavailable},
	}

	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			req := httptest.NewRequest(probe.method, probe.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != probe.status {
				t.Fatalf("%s %s expected %d, got %d body=%q", probe.method, probe.path, probe.status, rec.Code, rec.Body.String())
			}
		})
	}
}
