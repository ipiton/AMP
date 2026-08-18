package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

func TestAlertGroupsHandler(t *testing.T) {
	store := memory.NewAlertStore()
	now := time.Now().UTC()

	// Ingest test alerts
	alerts := []core.AlertIngestInput{
		{
			Labels:      map[string]string{"alertname": "CPUHigh", "service": "web"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f1",
			Status:      "firing",
		},
		{
			Labels:      map[string]string{"alertname": "CPUHigh", "service": "db"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f2",
			Status:      "firing",
		},
		{
			Labels:      map[string]string{"alertname": "MemHigh", "service": "web"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f3",
			Status:      "firing",
		},
	}
	_ = store.IngestBatch(alerts, now)

	registry := &extendedFakeRegistry{
		alertStore: store,
		config:     &appconfig.Config{},
	}

	handler := AlertGroupsHandler(registry)

	t.Run("GroupByService", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?group_by=service", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", rec.Code)
		}

		var groups []core.APIGettableAlertGroup
		if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
			t.Fatal(err)
		}

		// Groups should be "web" (2 alerts) and "db" (1 alert)
		if len(groups) != 2 {
			t.Errorf("got %d groups, want 2", len(groups))
		}
	})

	t.Run("GroupByAlertname", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?group_by=alertname", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		var groups []core.APIGettableAlertGroup
		_ = json.Unmarshal(rec.Body.Bytes(), &groups)

		// Groups should be "CPUHigh" (2 alerts) and "MemHigh" (1 alert)
		if len(groups) != 2 {
			t.Errorf("got %d groups, want 2", len(groups))
		}
	})
}

func TestAlertGroupsHandler_StateAndReceiverParams(t *testing.T) {
	store := memory.NewAlertStore()
	now := time.Now().UTC()

	alerts := []core.AlertIngestInput{
		{
			Labels:      map[string]string{"alertname": "CPUHigh", "service": "web"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f1",
			Status:      "firing",
		},
		{
			Labels:      map[string]string{"alertname": "MemHigh", "service": "db"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f2",
			Status:      "firing",
		},
	}
	if err := store.IngestBatch(alerts, now); err != nil {
		t.Fatalf("IngestBatch() error = %v", err)
	}

	silences := memory.NewSilenceStore()
	if _, err := silences.CreateOrUpdate(&core.SilenceInput{
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "CPUHigh"}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "test",
		Comment:   "mute",
	}, now); err != nil {
		t.Fatalf("CreateOrUpdate() error = %v", err)
	}

	registry := &extendedFakeRegistry{
		alertStore:   store,
		silenceStore: silences,
		config: &appconfig.Config{
			Receivers: []appconfig.ReceiverConfig{{Name: "default"}},
		},
	}
	handler := AlertGroupsHandler(registry)

	t.Run("SilencedFalseDropsSuppressedGroup", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?group_by=alertname&silenced=false", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", rec.Code)
		}
		var groups []core.APIGettableAlertGroup
		if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1 (CPUHigh's group dropped)", len(groups))
		}
		if len(groups[0].Alerts) != 1 || groups[0].Alerts[0].Labels["alertname"] != "MemHigh" {
			t.Fatalf("unexpected surviving group: %+v", groups)
		}
	})

	t.Run("ReceiverRegexNoMatchDropsAllGroups", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?group_by=alertname&receiver=nope", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", rec.Code)
		}
		var groups []core.APIGettableAlertGroup
		if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
			t.Fatal(err)
		}
		if len(groups) != 0 {
			t.Fatalf("got %d groups, want 0", len(groups))
		}
	})

	t.Run("InvalidBoolParam_Returns400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?active=notabool", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got status %d, want 400", rec.Code)
		}
	})
}
