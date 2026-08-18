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
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
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
	alertStore      *memory.AlertStore
	silenceStore    *memory.SilenceStore
	silenceRepo     infrasilencing.SilenceRepository
	silenceEventPub infrasilencing.SilenceEventPublisher
	processor       *services.AlertProcessor
	config          *appconfig.Config
	routeEvaluator  services.RouteEvaluator
}

func (r *fakeRegistry) AlertStore() *memory.AlertStore     { return r.alertStore }
func (r *fakeRegistry) SilenceStore() *memory.SilenceStore { return r.silenceStore }
func (r *fakeRegistry) SilenceRepository() infrasilencing.SilenceRepository {
	return r.silenceRepo
}
func (r *fakeRegistry) SilenceEventPublisher() infrasilencing.SilenceEventPublisher {
	return r.silenceEventPub
}
func (r *fakeRegistry) AlertProcessor() *services.AlertProcessor { return r.processor }
func (r *fakeRegistry) Config() *appconfig.Config {
	if r.config != nil {
		return r.config
	}
	return &appconfig.Config{}
}
func (r *fakeRegistry) StartTime() time.Time                     { return time.Now() }
func (r *fakeRegistry) ReloadConfig(_ context.Context) error     { return nil }
func (r *fakeRegistry) ClusterStatus(_ context.Context) ClusterStatus {
	return ClusterStatus{Status: "disabled"}
}

// RouteEvaluator returns nil unless a test injects one — nil is the
// lite/legacy posture (no `route:` section).
func (r *fakeRegistry) RouteEvaluator() services.RouteEvaluator { return r.routeEvaluator }

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

// setupSilencedAlert posts a firing alert, then creates a silence matching it
// (added after ingest, so the alert reaches the store unsilenced and only
// becomes "suppressed" when GET computes state against the current silences).
func setupSilencedAlertRegistry(t *testing.T) (*fakeRegistry, http.HandlerFunc) {
	t.Helper()
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	postAlert(t, handler, map[string]string{"alertname": "Muted", "receiver": "team-a"})
	postAlert(t, handler, map[string]string{"alertname": "NotMuted", "receiver": "team-b"})

	now := time.Now().UTC()
	_, err := registry.silenceStore.CreateOrUpdate(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{
			{Name: "alertname", Value: "Muted"},
		},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "test",
		Comment:   "mute",
	}, now)
	if err != nil {
		t.Fatalf("CreateOrUpdate() error = %v", err)
	}

	return registry, handler
}

func TestAlertsHandler_ActiveParamExcludesActiveAlerts(t *testing.T) {
	_, handler := setupSilencedAlertRegistry(t)

	alerts := getAlerts(t, handler, "active=false")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert (the suppressed one), got %d", len(alerts))
	}
	if alerts[0].Status.State != "suppressed" {
		t.Fatalf("expected remaining alert to be suppressed, got state %q", alerts[0].Status.State)
	}
}

func TestAlertsHandler_SilencedParamExcludesSuppressedAlerts(t *testing.T) {
	_, handler := setupSilencedAlertRegistry(t)

	alerts := getAlerts(t, handler, "silenced=false")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert (the active one), got %d", len(alerts))
	}
	if alerts[0].Status.State != "active" {
		t.Fatalf("expected remaining alert to be active, got state %q", alerts[0].Status.State)
	}
}

func TestAlertsHandler_DefaultParams_ReturnsAllStates(t *testing.T) {
	_, handler := setupSilencedAlertRegistry(t)

	alerts := getAlerts(t, handler, "")
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts with default active/silenced/inhibited/unprocessed=true, got %d", len(alerts))
	}
}

func TestAlertsHandler_InhibitedParamIsStructuralNoop(t *testing.T) {
	// LIMITATION: InhibitedBy is always empty until the inhibition pipeline
	// (separate parity track) populates it, so inhibited=false must not
	// exclude anything today — it is wired but has no data to act on yet.
	_, handler := setupSilencedAlertRegistry(t)

	alerts := getAlerts(t, handler, "inhibited=false")
	if len(alerts) != 2 {
		t.Fatalf("expected inhibited=false to be a no-op today, got %d alerts", len(alerts))
	}
}

func TestAlertsHandler_UnprocessedParam(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	// A resolved alert (never fired through this store) computes state
	// "unprocessed" per alertconv.ToGettableAlert.
	payload := `[{"labels":{"alertname":"ResolvedOnIngest"},"startsAt":"2026-03-08T10:00:00Z","status":"resolved"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	alerts := getAlerts(t, handler, "resolved=true")
	if len(alerts) != 1 || alerts[0].Status.State != "unprocessed" {
		t.Fatalf("expected 1 unprocessed alert, got %+v", alerts)
	}

	excluded := getAlerts(t, handler, "resolved=true&unprocessed=false")
	if len(excluded) != 0 {
		t.Fatalf("expected unprocessed=false to exclude the resolved alert, got %d", len(excluded))
	}
}

func TestAlertsHandler_ReceiverParamRegexMatch(t *testing.T) {
	registry, handler := setupSilencedAlertRegistry(t)
	_ = registry

	teamA := getAlerts(t, handler, `receiver=team-a`)
	if len(teamA) != 1 || teamA[0].Labels["alertname"] != "Muted" {
		t.Fatalf("expected receiver=team-a to match only the team-a alert, got %+v", teamA)
	}

	anyTeam := getAlerts(t, handler, `receiver=team-.*`)
	if len(anyTeam) != 2 {
		t.Fatalf("expected receiver=team-.* to match both alerts, got %d", len(anyTeam))
	}

	none := getAlerts(t, handler, `receiver=team-c`)
	if len(none) != 0 {
		t.Fatalf("expected receiver=team-c to match nothing, got %d", len(none))
	}
}

func TestAlertsHandler_InvalidBoolParam_Returns400(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := AlertsHandler(registry)

	for _, param := range []string{"active", "silenced", "inhibited", "unprocessed"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+param+"=notabool", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s=notabool: got status %d, want 400", param, rec.Code)
		}
	}
}

func TestAlertsHandler_InvalidReceiverRegex_Returns400(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=%5B", nil) // "[" — unbalanced class
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestV1AlertsHandler_PostAliasesV2Ingest(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := V1AlertsHandler(registry)

	payload := `[{"labels":{"alertname":"V1Alias","service":"amp"},"startsAt":"2026-03-08T10:00:00Z","status":"firing"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/alerts status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if len(publisher.published) != 1 {
		t.Fatalf("expected 1 published alert via v1 alias, got %d", len(publisher.published))
	}
	if total, _, _ := registry.alertStore.Stats(); total != 1 {
		t.Fatalf("expected 1 stored alert via v1 alias, got %d", total)
	}
}

func TestV1AlertsHandler_PostInvalidPayload_Returns400(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString(`[]`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v1/alerts (empty array) status = %d, want 400", rec.Code)
	}
}

func TestV1AlertsHandler_Get_Returns405(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/v1/alerts status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "POST" {
		t.Errorf("Allow header = %q, want %q", allow, "POST")
	}
}

func TestAlertsHandler_OldStatusResolvedAliasCombinedWithNewParams(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
	}
	handler := AlertsHandler(registry)

	payload := `[{"labels":{"alertname":"OldAlias"},"startsAt":"2026-03-08T10:00:00Z","status":"resolved"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// status=resolved is still a working alias for resolved=true.
	alerts := getAlerts(t, handler, "status=resolved")
	if len(alerts) != 1 {
		t.Fatalf("expected status=resolved alias to still work, got %d alerts", len(alerts))
	}

	// Combined with the new unprocessed=false, the resolved alert is excluded.
	excluded := getAlerts(t, handler, "status=resolved&unprocessed=false")
	if len(excluded) != 0 {
		t.Fatalf("expected status=resolved&unprocessed=false to exclude it, got %d", len(excluded))
	}
}
