package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

func createSilence(t *testing.T, store *memory.SilenceStore, matchers []core.SilenceMatcherInput) string {
	t.Helper()
	now := time.Now().UTC()
	id, err := store.CreateOrUpdate(&core.SilenceInput{
		Matchers:  matchers,
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "test",
		Comment:   "test silence",
	}, now)
	if err != nil {
		t.Fatalf("CreateOrUpdate() error = %v", err)
	}
	return id
}

func getSilences(t *testing.T, handler http.HandlerFunc, query string) []core.APISilence {
	t.Helper()
	url := "/api/v2/silences"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var silences []core.APISilence
	if err := json.Unmarshal(rec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return silences
}

func TestSilencesHandler_FilterByMatcherName(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	createSilence(t, store, []core.SilenceMatcherInput{
		{Name: "alertname", Value: "Watchdog"},
	})
	createSilence(t, store, []core.SilenceMatcherInput{
		{Name: "severity", Value: "critical"},
	})

	silences := getSilences(t, handler, `filter=alertname%3D"Watchdog"`)
	if len(silences) != 1 {
		t.Fatalf("expected 1 silence, got %d", len(silences))
	}
	found := false
	for _, m := range silences[0].Matchers {
		if m.Name == "alertname" {
			found = true
			break
		}
	}
	if !found {
		t.Error("returned silence does not have alertname matcher")
	}
}

func TestSilencesHandler_FilterNoMatch_ReturnsEmpty(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	createSilence(t, store, []core.SilenceMatcherInput{
		{Name: "alertname", Value: "Watchdog"},
	})

	silences := getSilences(t, handler, `filter=nonexistent%3D"x"`)
	if len(silences) != 0 {
		t.Fatalf("expected 0 silences, got %d", len(silences))
	}
}

func TestSilencesHandler_FilterBadSyntax_Returns400(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/silences?filter=bad", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestSilencesHandler_EmptyFilter_ReturnsAll(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	createSilence(t, store, []core.SilenceMatcherInput{{Name: "alertname", Value: "A"}})
	createSilence(t, store, []core.SilenceMatcherInput{{Name: "alertname", Value: "B"}})

	silences := getSilences(t, handler, "")
	if len(silences) != 2 {
		t.Fatalf("expected 2 silences, got %d", len(silences))
	}
}

func TestSilencesHandler_PostGet_RoundTrip(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	now := time.Now().UTC()
	body := `{"matchers":[{"name":"alertname","value":"TestAlert","isRegex":false,"isEqual":true}],"startsAt":"` +
		now.Add(-time.Minute).Format(time.RFC3339) + `","endsAt":"` +
		now.Add(time.Hour).Format(time.RFC3339) + `","createdBy":"tester","comment":"round trip"}`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", strings.NewReader(body))
	postRec := httptest.NewRecorder()
	handler(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body: %s", postRec.Code, postRec.Body.String())
	}

	silences := getSilences(t, handler, "")
	if len(silences) != 1 {
		t.Fatalf("expected 1 silence, got %d", len(silences))
	}
}
