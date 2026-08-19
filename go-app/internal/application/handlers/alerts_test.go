package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func (p *fakePublisher) PublishToReceiver(_ context.Context, alert *core.Alert, _ string) error {
	p.published = append(p.published, alert)
	return nil
}

func (p *fakePublisher) PublishToReceiverWithClassification(_ context.Context, alert *core.Alert, _ *core.ClassificationResult, _ string) error {
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

// TestV1AlertsHandler_Get_ReturnsV1Envelope covers amtool audit backlog item
// 3: GET /api/v1/alerts previously 405'd; it must now return the legacy v1
// envelope ({"status":"success","data":[...]}) as a thin wrapper around the
// v2 alert listing.
func TestV1AlertsHandler_Get_ReturnsV1Envelope(t *testing.T) {
	store := memory.NewAlertStore()
	now := time.Now().UTC()
	if err := store.IngestBatch([]core.AlertIngestInput{{
		Labels:   map[string]string{"alertname": "V1Get", "severity": "critical"},
		StartsAt: now.Format(time.RFC3339),
		Status:   "firing",
	}}, now); err != nil {
		t.Fatalf("seed alert store: %v", err)
	}

	registry := &fakeRegistry{
		alertStore:   store,
		silenceStore: memory.NewSilenceStore(),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp core.APIV1AlertsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode v1 envelope: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("envelope status = %q, want %q", resp.Status, "success")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("envelope data length = %d, want 1", len(resp.Data))
	}
	got := resp.Data[0]
	if got.Labels["alertname"] != "V1Get" {
		t.Fatalf("data[0].labels[alertname] = %q, want %q", got.Labels["alertname"], "V1Get")
	}
	if len(got.Fingerprint) != 16 {
		t.Fatalf("data[0].fingerprint = %q, want 16 hex chars (upstream shape)", got.Fingerprint)
	}
	if got.Status.State == "" {
		t.Fatalf("data[0].status.state is empty")
	}

	// Raw JSON: v1 has no "receivers":[{"name":...}] objects and no mutedBy —
	// only bare receiver name strings.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw json: %v", err)
	}
	dataArr, _ := raw["data"].([]any)
	if len(dataArr) != 1 {
		t.Fatalf("raw data length = %d, want 1", len(dataArr))
	}
	firstAlert, _ := dataArr[0].(map[string]any)
	if _, hasMutedBy := firstAlert["status"].(map[string]any)["mutedBy"]; hasMutedBy {
		t.Fatalf("v1 alert status must not have mutedBy (v2-only field): %+v", firstAlert["status"])
	}
	receivers, ok := firstAlert["receivers"].([]any)
	if !ok {
		t.Fatalf("receivers field is not a bare array: %T", firstAlert["receivers"])
	}
	if len(receivers) > 0 {
		if _, isString := receivers[0].(string); !isString {
			t.Fatalf("v1 receivers[0] = %T, want a bare string", receivers[0])
		}
	}
}

// TestV1AlertsHandler_Get_RespectsFilters checks that the v1 GET wrapper
// applies the same query-param filters as v2 (active/silenced/inhibited/
// unprocessed, per the overlap the task calls for), not just an unfiltered
// dump of everything in the store.
func TestV1AlertsHandler_Get_RespectsFilters(t *testing.T) {
	store := memory.NewAlertStore()
	now := time.Now().UTC()
	if err := store.IngestBatch([]core.AlertIngestInput{
		{Labels: map[string]string{"alertname": "Keep"}, StartsAt: now.Format(time.RFC3339), Status: "firing"},
		{Labels: map[string]string{"alertname": "AlsoKeep"}, StartsAt: now.Format(time.RFC3339), Status: "firing"},
	}, now); err != nil {
		t.Fatalf("seed alert store: %v", err)
	}

	registry := &fakeRegistry{
		alertStore:   store,
		silenceStore: memory.NewSilenceStore(),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, `/api/v1/alerts?filter=alertname%3D"Keep"`, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts?filter=... status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp core.APIV1AlertsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode v1 envelope: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Labels["alertname"] != "Keep" {
		t.Fatalf("filtered v1 data = %+v, want exactly the 'Keep' alert", resp.Data)
	}
}

// TestV1AlertsHandler_Get_BadFilter_Returns400 mirrors v2's 400 status
// contract for a malformed filter= param, but the BODY must use the v1
// error envelope ({"status":"error","errorType":"bad_data","error":"..."}),
// not v2's bare {"error":"..."} shape — every v1 response carries "status".
func TestV1AlertsHandler_Get_BadFilter_Returns400(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?filter=bad", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v1/alerts?filter=bad status = %d, want 400", rec.Code)
	}

	var envelope core.APIV1ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode v1 error envelope: %v; body: %s", err, rec.Body.String())
	}
	if envelope.Status != "error" {
		t.Fatalf("envelope.Status = %q, want %q", envelope.Status, "error")
	}
	if envelope.ErrorType != "bad_data" {
		t.Fatalf("envelope.ErrorType = %q, want %q", envelope.ErrorType, "bad_data")
	}
	if envelope.Error == "" {
		t.Fatal("envelope.Error is empty, want a description of the bad filter")
	}

	// Bare v2-shape leakage guard: the body must not be a plain
	// {"error":"..."} object without "status"/"errorType".
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw json: %v", err)
	}
	if _, hasStatus := raw["status"]; !hasStatus {
		t.Fatal("v1 error body missing \"status\" field")
	}
	if _, hasErrorType := raw["errorType"]; !hasErrorType {
		t.Fatal("v1 error body missing \"errorType\" field")
	}
}

// TestV1AlertsHandler_Delete_Returns405 keeps the "unsupported method" 405
// contract for methods other than GET/POST.
func TestV1AlertsHandler_Delete_Returns405(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := V1AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alerts", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /api/v1/alerts status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow header = %q, want %q", allow, "GET, POST")
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

// TestParseBoolQueryStrict_AbsentParam_ReturnsDefault covers the case the
// param is never in the query string at all: def wins.
func TestParseBoolQueryStrict_AbsentParam_ReturnsDefault(t *testing.T) {
	query, err := url.ParseQuery("")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	got, err := parseBoolQueryStrict(query, "active", true)
	if err != nil {
		t.Fatalf("parseBoolQueryStrict() error = %v, want nil", err)
	}
	if got != true {
		t.Fatalf("parseBoolQueryStrict() = %v, want default true", got)
	}
}

// TestParseBoolQueryStrict_ValidValues covers ?x=true and ?x=false parsing
// to their literal values, independent of def.
func TestParseBoolQueryStrict_ValidValues(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
	}

	for _, tc := range cases {
		query, err := url.ParseQuery("active=" + tc.raw)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tc.raw, err)
		}

		got, err := parseBoolQueryStrict(query, "active", false)
		if err != nil {
			t.Fatalf("parseBoolQueryStrict(%q) error = %v, want nil", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("parseBoolQueryStrict(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestParseBoolQueryStrict_PresentButEmpty_Returns400Error is the F4 fix
// itself: upstream Alertmanager treats a present-but-empty bool query param
// (?active=) as a malformed value, not as "unset" — it must error, not
// silently fall back to def. query.Get alone can't distinguish "absent" from
// "present but empty" (both are ""), which is exactly the bug: the old
// implementation only ever called query.Get and treated both the same way.
func TestParseBoolQueryStrict_PresentButEmpty_Returns400Error(t *testing.T) {
	query, err := url.ParseQuery("active=")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	got, err := parseBoolQueryStrict(query, "active", true)
	if err == nil {
		t.Fatal("parseBoolQueryStrict(?active=) error = nil, want an error (upstream 400s on empty bool params)")
	}
	if got != true {
		t.Fatalf("parseBoolQueryStrict(?active=) value = %v, want def (true) returned alongside the error", got)
	}
}

// TestParseBoolQueryStrict_Garbage_ReturnsError covers a present value that
// isn't a valid boolean at all (distinct from the present-but-empty case
// above, but the same "present and invalid" family).
func TestParseBoolQueryStrict_Garbage_ReturnsError(t *testing.T) {
	query, err := url.ParseQuery("active=notabool")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if _, err := parseBoolQueryStrict(query, "active", true); err == nil {
		t.Fatal("parseBoolQueryStrict(?active=notabool) error = nil, want an error")
	}
}

// TestAlertStateFilter_PresentButEmptyBoolParam_Returns400 is the
// handler-level counterpart: GET /api/v2/alerts?active= must 400 through the
// full filter-parsing path (parseAlertStateFilters -> parseBoolQueryStrict),
// not just at the unit level.
func TestAlertStateFilter_PresentButEmptyBoolParam_Returns400(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
	}
	handler := AlertsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?active=", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v2/alerts?active= status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}
