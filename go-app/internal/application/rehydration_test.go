package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// fakeAlertStorage implements only ListAlerts; other AlertStorage methods are
// inherited from the embedded nil interface and panic if touched — the
// rehydration path must never call them.
type fakeAlertStorage struct {
	core.AlertStorage
	alerts []*core.Alert
}

func (f *fakeAlertStorage) ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error) {
	if filters.Offset >= len(f.alerts) {
		return &core.AlertList{Alerts: nil}, nil
	}
	end := filters.Offset + filters.Limit
	if end > len(f.alerts) {
		end = len(f.alerts)
	}
	return &core.AlertList{Alerts: f.alerts[filters.Offset:end]}, nil
}

func TestRehydrateAlertStore_RestoresFiringAlerts(t *testing.T) {
	starts := time.Now().UTC().Add(-time.Hour)
	stored := &core.Alert{
		Fingerprint: "fp-rehydrate",
		AlertName:   "NodeDown",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "NodeDown"},
		Annotations: map[string]string{"summary": "node down"},
		StartsAt:    starts,
	}

	r := &ServiceRegistry{
		logger:     slog.Default(),
		alertStore: memory.NewAlertStore(),
		storage:    &fakeAlertStorage{alerts: []*core.Alert{stored}},
	}

	if err := r.rehydrateAlertStore(context.Background()); err != nil {
		t.Fatalf("rehydrateAlertStore: %v", err)
	}

	alerts := r.alertStore.List("", true)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 restored alert, got %d", len(alerts))
	}
	if alerts[0].Fingerprint != "fp-rehydrate" {
		t.Fatalf("unexpected fingerprint %q", alerts[0].Fingerprint)
	}
}

func TestRehydrateAlertStore_NilStorageIsNoop(t *testing.T) {
	r := &ServiceRegistry{logger: slog.Default(), alertStore: memory.NewAlertStore()}
	if err := r.rehydrateAlertStore(context.Background()); err != nil {
		t.Fatalf("nil storage must be a no-op, got %v", err)
	}
}

// TestRehydrateAlertStore_FieldMapping replaces the old
// TestPersistedAlertToAPIAlert_Mapping: rehydration now flows []*core.Alert
// straight into the store, so the mapping is asserted on the store output.
func TestRehydrateAlertStore_FieldMapping(t *testing.T) {
	starts := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	ends := starts.Add(30 * time.Minute)
	url := "http://prom/graph"

	stored := &core.Alert{
		Fingerprint:  "fp",
		Status:       core.StatusResolved,
		Labels:       map[string]string{"a": "b"},
		StartsAt:     starts,
		EndsAt:       &ends,
		GeneratorURL: &url,
	}

	r := &ServiceRegistry{
		logger:     slog.Default(),
		alertStore: memory.NewAlertStore(),
		storage:    &fakeAlertStorage{alerts: []*core.Alert{stored}},
	}
	if err := r.rehydrateAlertStore(context.Background()); err != nil {
		t.Fatalf("rehydrateAlertStore: %v", err)
	}

	alerts := r.alertStore.List("", true)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 restored alert, got %d", len(alerts))
	}
	api := alerts[0]
	if api.StartsAt != "2026-08-17T10:00:00Z" {
		t.Fatalf("StartsAt = %q", api.StartsAt)
	}
	if api.EndsAt == nil || *api.EndsAt != "2026-08-17T10:30:00Z" {
		t.Fatalf("EndsAt = %v", api.EndsAt)
	}
	if api.Status != "resolved" || api.GeneratorURL != url || api.Fingerprint != "fp" {
		t.Fatalf("bad mapping: %+v", api)
	}
}
