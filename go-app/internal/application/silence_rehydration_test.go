package application

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// fakeSilenceRepository implements only ListSilences; other repository
// methods are inherited from the embedded nil interface and panic if touched —
// the rehydration path must never call them.
type fakeSilenceRepository struct {
	infrasilencing.SilenceRepository
	silences []*coresilencing.Silence
	listErr  error
	filters  []infrasilencing.SilenceFilter
}

func (f *fakeSilenceRepository) ListSilences(_ context.Context, filter infrasilencing.SilenceFilter) ([]*coresilencing.Silence, error) {
	f.filters = append(f.filters, filter)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if filter.Offset >= len(f.silences) {
		return nil, nil
	}
	end := filter.Offset + filter.Limit
	if end > len(f.silences) {
		end = len(f.silences)
	}
	return f.silences[filter.Offset:end], nil
}

func TestRehydrateSilenceStore_RestoresActivePendingAndExpired(t *testing.T) {
	now := time.Now().UTC()

	active := &coresilencing.Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "active silence",
		StartsAt:  now.Add(-time.Hour),
		EndsAt:    now.Add(time.Hour),
		Status:    coresilencing.SilenceStatusActive,
		CreatedAt: now.Add(-time.Hour),
		Matchers:  []coresilencing.Matcher{{Name: "alertname", Value: "NodeDown", Type: coresilencing.MatcherTypeEqual}},
	}
	pending := &coresilencing.Silence{
		ID:        "660e8400-e29b-41d4-a716-446655440001",
		CreatedBy: "ops@example.com",
		Comment:   "pending silence",
		StartsAt:  now.Add(time.Hour),
		EndsAt:    now.Add(2 * time.Hour),
		Status:    coresilencing.SilenceStatusPending,
		CreatedAt: now.Add(-time.Minute),
		Matchers:  []coresilencing.Matcher{{Name: "env", Value: "prod", Type: coresilencing.MatcherTypeNotEqual}},
	}
	// F3 (alertmanager-parity amtool audit): expired silences must also
	// survive a restart now, not just active/pending, so GET
	// /api/v2/silences keeps showing status.state == "expired" for an
	// already-expired-in-place silence across a restart.
	expired := &coresilencing.Silence{
		ID:        "770e8400-e29b-41d4-a716-446655440002",
		CreatedBy: "ops@example.com",
		Comment:   "expired silence",
		StartsAt:  now.Add(-2 * time.Hour),
		EndsAt:    now.Add(-time.Hour),
		Status:    coresilencing.SilenceStatusExpired,
		CreatedAt: now.Add(-2 * time.Hour),
		Matchers:  []coresilencing.Matcher{{Name: "alertname", Value: "OldIncident", Type: coresilencing.MatcherTypeEqual}},
	}

	repo := &fakeSilenceRepository{silences: []*coresilencing.Silence{active, pending, expired}}
	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceStore: memory.NewSilenceStore(),
		silenceRepo:  repo,
	}

	if err := r.rehydrateSilenceStore(context.Background()); err != nil {
		t.Fatalf("rehydrateSilenceStore: %v", err)
	}

	// IDs must survive verbatim — DELETE by ID after a restart depends on it.
	got, ok := r.silenceStore.Get(active.ID, now)
	if !ok {
		t.Fatalf("active silence %s not restored", active.ID)
	}
	if got.Status.State != "active" {
		t.Errorf("restored active silence state = %q, want active", got.Status.State)
	}
	if len(got.Matchers) != 1 || got.Matchers[0].Name != "alertname" || !got.Matchers[0].IsEqual {
		t.Errorf("restored matchers wrong: %+v", got.Matchers)
	}

	got, ok = r.silenceStore.Get(pending.ID, now)
	if !ok {
		t.Fatalf("pending silence %s not restored", pending.ID)
	}
	if got.Status.State != "pending" {
		t.Errorf("restored pending silence state = %q, want pending", got.Status.State)
	}
	if len(got.Matchers) != 1 || got.Matchers[0].IsEqual {
		t.Errorf("negative matcher lost on rehydration: %+v", got.Matchers)
	}

	got, ok = r.silenceStore.Get(expired.ID, now)
	if !ok {
		t.Fatalf("expired silence %s not restored (F3 regression)", expired.ID)
	}
	if got.Status.State != "expired" {
		t.Errorf("restored expired silence state = %q, want expired", got.Status.State)
	}

	// The rehydration filter must ask for active+pending+expired (F3) —
	// row removal stays exclusively the GC retention worker's job, so
	// rehydration itself must not re-exclude expired rows.
	if len(repo.filters) == 0 {
		t.Fatal("ListSilences never called")
	}
	statuses := repo.filters[0].Statuses
	if len(statuses) != 3 {
		t.Fatalf("filter statuses = %v, want [active pending expired]", statuses)
	}
	wantStatuses := map[coresilencing.SilenceStatus]bool{
		coresilencing.SilenceStatusActive:  true,
		coresilencing.SilenceStatusPending: true,
		coresilencing.SilenceStatusExpired: true,
	}
	for _, s := range statuses {
		if !wantStatuses[s] {
			t.Errorf("unexpected status %q in rehydration filter", s)
		}
	}
}

func TestRehydrateSilenceStore_NilRepoIsNoop(t *testing.T) {
	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceStore: memory.NewSilenceStore(),
	}
	if err := r.rehydrateSilenceStore(context.Background()); err != nil {
		t.Fatalf("rehydrateSilenceStore with nil repo: %v", err)
	}
}

func TestRehydrateSilenceStore_ListErrorIsReturned(t *testing.T) {
	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceStore: memory.NewSilenceStore(),
		silenceRepo:  &fakeSilenceRepository{listErr: errors.New("connection refused")},
	}
	if err := r.rehydrateSilenceStore(context.Background()); err == nil {
		t.Fatal("expected error from failing repository")
	}
}
