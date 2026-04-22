package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

type fakePublisher struct {
	published []*core.Alert
}

func (p *fakePublisher) PublishToAll(_ context.Context, alert *core.Alert) error {
	p.published = append(p.published, alert)
	return nil
}

func (p *fakePublisher) PublishWithClassification(_ context.Context, alert *core.Alert, _ *core.ClassificationResult) error {
	p.published = append(p.published, alert)
	return nil
}

type fakeFilterEngine struct{}

func (f *fakeFilterEngine) ShouldBlock(_ *core.Alert, _ *core.ClassificationResult) (bool, string) {
	return false, ""
}

type fakeRegistry struct {
	alertStore   *memory.AlertStore
	silenceStore *memory.SilenceStore
	processor    *services.AlertProcessor
}

func (r *fakeRegistry) AlertStore() *memory.AlertStore   { return r.alertStore }
func (r *fakeRegistry) SilenceStore() *memory.SilenceStore { return r.silenceStore }
func (r *fakeRegistry) AlertProcessor() *services.AlertProcessor { return r.processor }
func (r *fakeRegistry) Config() *appconfig.Config        { return &appconfig.Config{} }
func (r *fakeRegistry) StartTime() time.Time             { return time.Now() }
func (r *fakeRegistry) ReloadConfig(_ context.Context) error { return nil }

func newTestProcessor(t *testing.T, publisher *fakePublisher) *services.AlertProcessor {
	t.Helper()

	processor, err := services.NewAlertProcessor(services.AlertProcessorConfig{
		FilterEngine: &fakeFilterEngine{},
		Publisher:    publisher,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewAlertProcessor() error = %v", err)
	}
	return processor
}

func TestAlertsHandler_PostLegacyPayloadUsesProcessorAndStoresAlert(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}

	handler := AlertsHandler(registry)
	payload := `[
		{
			"labels": {"alertname":"LegacyAlert","service":"amp"},
			"annotations": {"summary":"legacy"},
			"startsAt": "2026-03-08T10:00:00Z",
			"status": "firing"
		}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("expected 1 published alert, got %d", len(publisher.published))
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	getRec := httptest.NewRecorder()
	handler(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRec.Code)
	}

	var alerts []core.APIGettableAlert
	if err := json.Unmarshal(getRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode GET response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 stored alert, got %d", len(alerts))
	}
	if alerts[0].Labels["alertname"] != "LegacyAlert" {
		t.Fatalf("unexpected alertname %q", alerts[0].Labels["alertname"])
	}
}

func TestAlertsHandler_PostPrometheusPayloadUsesProcessorAndStoresAlert(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}

	handler := AlertsHandler(registry)
	payload := `[
		{
			"labels": {"alertname":"PromAlert","service":"amp"},
			"annotations": {"summary":"prometheus"},
			"state": "firing",
			"activeAt": "2026-03-08T10:00:00Z",
			"generatorURL": "http://prometheus.local/graph"
		}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("expected 1 published alert, got %d", len(publisher.published))
	}
	if total, _, _ := registry.alertStore.Stats(); total != 1 {
		t.Fatalf("expected 1 stored alert, got %d", total)
	}
}

func postAlert(t *testing.T, handler http.HandlerFunc, labels map[string]string) {
	t.Helper()
	labelJSON := "{"
	first := true
	for k, v := range labels {
		if !first {
			labelJSON += ","
		}
		labelJSON += `"` + k + `":"` + v + `"`
		first = false
	}
	labelJSON += "}"
	payload := `[{"labels":` + labelJSON + `,"startsAt":"2026-03-08T10:00:00Z","status":"firing"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func getAlerts(t *testing.T, handler http.HandlerFunc, query string) []core.APIGettableAlert {
	t.Helper()
	url := "/api/v2/alerts"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var alerts []core.APIGettableAlert
	if err := json.Unmarshal(rec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return alerts
}

func TestAlertsHandler_FilterByExactLabel(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	postAlert(t, handler, map[string]string{"alertname": "Watchdog", "severity": "critical"})
	postAlert(t, handler, map[string]string{"alertname": "OtherAlert", "severity": "warning"})

	alerts := getAlerts(t, handler, `filter=alertname%3D"Watchdog"`)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Labels["alertname"] != "Watchdog" {
		t.Errorf("unexpected alertname %q", alerts[0].Labels["alertname"])
	}
}

func TestAlertsHandler_FilterByRegex(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	postAlert(t, handler, map[string]string{"alertname": "AlertA", "severity": "critical"})
	postAlert(t, handler, map[string]string{"alertname": "AlertB", "severity": "warning"})

	alerts := getAlerts(t, handler, `filter=severity%3D~"crit.*"`)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Labels["severity"] != "critical" {
		t.Errorf("unexpected severity %q", alerts[0].Labels["severity"])
	}
}

func TestAlertsHandler_FilterBadSyntax_Returns400(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?filter=bad%3Asyntax", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAlertsHandler_FilterCombinedWithStatus(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	postAlert(t, handler, map[string]string{"alertname": "X", "severity": "critical"})
	postAlert(t, handler, map[string]string{"alertname": "Y", "severity": "warning"})

	alerts := getAlerts(t, handler, `status=firing&filter=alertname%3D"X"`)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Labels["alertname"] != "X" {
		t.Errorf("unexpected alertname %q", alerts[0].Labels["alertname"])
	}
}

func TestAlertsHandler_EmptyFilter_ReturnsAll(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	postAlert(t, handler, map[string]string{"alertname": "A", "severity": "critical"})
	postAlert(t, handler, map[string]string{"alertname": "B", "severity": "warning"})

	alerts := getAlerts(t, handler, "")
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestAlertsHandler_SilencedAlertIsSuppressed(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}

	now := time.Now().UTC()
	_, err := registry.silenceStore.CreateOrUpdate(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{
			{Name: "alertname", Value: "MutedAlert"},
		},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "test",
		Comment:   "mute",
	}, now)
	if err != nil {
		t.Fatalf("CreateOrUpdate() error = %v", err)
	}

	handler := AlertsHandler(registry)
	payload := `[
		{
			"labels": {"alertname":"MutedAlert","service":"amp"},
			"startsAt": "2026-03-08T10:00:00Z",
			"status": "firing"
		}
	]`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if len(publisher.published) != 0 {
		t.Fatalf("expected silenced alert to be skipped, got %d published", len(publisher.published))
	}
	if total, _, _ := registry.alertStore.Stats(); total != 0 {
		t.Fatalf("expected no stored alerts, got %d", total)
	}
}
