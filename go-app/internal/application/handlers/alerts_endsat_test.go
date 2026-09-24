package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// TestAlertsHandler_PostWithoutEndsAt_ServesResolveTimeout is the end-to-end
// regression for PARITY-RESOLVE-TIMEOUT-ENDSAT: an upstream-shaped postable
// alert with no endsAt used to come back from every read API with
// endsAt == startsAt == updatedAt, i.e. already resolved for any consumer that
// (like Alertmanager itself) treats a past endsAt as resolved.
func TestAlertsHandler_PostWithoutEndsAt_ServesResolveTimeout(t *testing.T) {
	publisher := &fakePublisher{}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(), // no provider ⇒ upstream default 5m
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, publisher),
		config:       &appconfig.Config{Receivers: []appconfig.ReceiverConfig{{Name: "default"}}},
	}

	// Upstream postable shape: no status, no endsAt (amtool / curl clients).
	payload := `[{"labels":{"alertname":"NoEndsAt","service":"amp"},"startsAt":"2026-09-24T09:00:00Z"}]`

	before := time.Now().UTC()
	rec := httptest.NewRecorder()
	AlertsHandler(registry)(rec, httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload)))
	after := time.Now().UTC()
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// The ingest pipeline (dedup, DB, publishing) sees the alert exactly as
	// sent: the timeout endsAt is an API-layer value (Spec D1 / ADR-010).
	if len(publisher.published) != 1 {
		t.Fatalf("published = %d, want 1", len(publisher.published))
	}
	if publisher.published[0].EndsAt != nil {
		t.Fatalf("pipeline alert EndsAt = %v, want nil (must not be stamped before ProcessAlert)", publisher.published[0].EndsAt)
	}

	get := func(h http.HandlerFunc, path string) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	var v2 []core.APIGettableAlert
	if err := json.Unmarshal(get(AlertsHandler(registry), "/api/v2/alerts"), &v2); err != nil {
		t.Fatalf("decode v2: %v", err)
	}
	if len(v2) != 1 {
		t.Fatalf("v2 alerts = %d, want 1", len(v2))
	}
	a := v2[0]

	endsAt, err := time.Parse(time.RFC3339, a.EndsAt)
	if err != nil {
		t.Fatalf("parse endsAt %q: %v", a.EndsAt, err)
	}
	// RFC3339 drops sub-seconds, so allow one second of truncation.
	lo := before.Add(alertconv.DefaultResolveTimeout).Add(-time.Second)
	hi := after.Add(alertconv.DefaultResolveTimeout)
	if endsAt.Before(lo) || endsAt.After(hi) {
		t.Fatalf("endsAt = %s, want receivedAt + 5m (in [%s, %s])", a.EndsAt, lo.Format(time.RFC3339), hi.Format(time.RFC3339))
	}
	if a.EndsAt == a.UpdatedAt || a.EndsAt == a.StartsAt {
		t.Fatalf("endsAt = %s equals updatedAt/startsAt (%s / %s): the old 'already resolved' bug", a.EndsAt, a.UpdatedAt, a.StartsAt)
	}
	if a.Status.State != "active" {
		t.Fatalf("state = %q, want active", a.Status.State)
	}

	var v1 core.APIV1AlertsResponse
	if err := json.Unmarshal(get(V1AlertsHandler(registry), "/api/v1/alerts"), &v1); err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	if len(v1.Data) != 1 || v1.Data[0].EndsAt != a.EndsAt {
		t.Fatalf("v1 endsAt = %+v, want %s (same as v2)", v1.Data, a.EndsAt)
	}

	var groups []core.APIGettableAlertGroup
	if err := json.Unmarshal(get(AlertGroupsHandler(registry), "/api/v2/alerts/groups"), &groups); err != nil {
		t.Fatalf("decode groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Alerts) != 1 || groups[0].Alerts[0].EndsAt != a.EndsAt {
		t.Fatalf("groups endsAt = %+v, want %s (same as v2)", groups, a.EndsAt)
	}
}

// An explicit endsAt from the sender is served as-is (not replaced by the
// resolve_timeout window).
func TestAlertsHandler_PostWithExplicitEndsAt_IsKept(t *testing.T) {
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		processor:    newTestProcessor(t, &fakePublisher{}),
	}
	payload := `[{"labels":{"alertname":"WithEndsAt"},"startsAt":"2026-09-24T09:00:00Z","endsAt":"2099-01-01T00:00:00Z"}]`

	rec := httptest.NewRecorder()
	AlertsHandler(registry)(rec, httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	AlertsHandler(registry)(rec, httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil))
	var alerts []core.APIGettableAlert
	if err := json.Unmarshal(rec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(alerts) != 1 || alerts[0].EndsAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("alerts = %+v, want endsAt 2099-01-01T00:00:00Z", alerts)
	}
}
