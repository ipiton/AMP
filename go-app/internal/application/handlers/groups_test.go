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
