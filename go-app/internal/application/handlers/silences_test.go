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

// deleteSilence issues DELETE /api/v2/silence/{id} through SilenceByIDHandler
// and returns the response code.
func deleteSilence(t *testing.T, registry *fakeRegistry, id string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+id, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)
	return rec.Code
}

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

// TestSilencesHandler_ExpiredSilenceStaysQueryable reproduces the exact
// amtool audit scenario (task-7.4 finding F3, lite/memory-only profile):
// `silence add` -> `silence expire <id>` -> `silence query --expired` must
// still show the silence, with status.state == "expired". Before the fix,
// DELETE hard-removed the entry and GET returned an empty list.
func TestSilencesHandler_ExpiredSilenceStaysQueryable(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		// silenceRepo deliberately nil: lite profile, matching the audit setup.
	}
	handler := SilencesHandler(registry)

	id := createSilence(t, store, []core.SilenceMatcherInput{
		{Name: "severity", Value: "critical"},
	})

	if code := deleteSilence(t, registry, id); code != http.StatusOK {
		t.Fatalf("DELETE /api/v2/silence/%s status = %d, want 200", id, code)
	}

	// amtool's default `silence query` view models: only active/pending
	// should be considered "current" by a caller that filters client-side,
	// but the server itself (this GET) must return the full set including
	// expired ones — amtool applies the --expired filter, not the server.
	silences := getSilences(t, handler, "")
	if len(silences) != 1 {
		t.Fatalf("GET /api/v2/silences after expire returned %d silences, want 1 (still queryable)", len(silences))
	}
	if silences[0].ID != id {
		t.Fatalf("returned silence id = %q, want %q", silences[0].ID, id)
	}
	if silences[0].Status.State != "expired" {
		t.Fatalf("Status.State = %q, want %q", silences[0].Status.State, "expired")
	}
}

// TestSilencesHandler_ActivePendingStates_StillCorrect guards the other half
// of F3: the expire fix must not perturb ordinary active/pending state
// computation for silences that were never DELETEd.
func TestSilencesHandler_ActivePendingStates_StillCorrect(t *testing.T) {
	store := memory.NewSilenceStore()
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
	}
	handler := SilencesHandler(registry)

	now := time.Now().UTC()

	activeID, err := store.CreateOrUpdate(&core.SilenceInput{
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "Active"}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "currently active",
	}, now)
	if err != nil {
		t.Fatalf("create active silence: %v", err)
	}

	pendingID, err := store.CreateOrUpdate(&core.SilenceInput{
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "Pending"}},
		StartsAt:  now.Add(time.Hour).Format(time.RFC3339),
		EndsAt:    now.Add(2 * time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "not started yet",
	}, now)
	if err != nil {
		t.Fatalf("create pending silence: %v", err)
	}

	states := map[string]string{}
	for _, s := range getSilences(t, handler, "") {
		states[s.ID] = s.Status.State
	}
	if states[activeID] != "active" {
		t.Fatalf("active silence state = %q, want %q", states[activeID], "active")
	}
	if states[pendingID] != "pending" {
		t.Fatalf("pending silence state = %q, want %q", states[pendingID], "pending")
	}
}
