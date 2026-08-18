package memory

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Upsert is the DB-first cache write path (SPLIT-BRAIN-RISK slice 2): the
// repository generates the silence ID and memory must adopt it verbatim, so
// that memory and database always agree on identifiers.

func TestSilenceStore_Upsert_InsertsWithProvidedID(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()
	const id = "550e8400-e29b-41d4-a716-446655440000"

	gotID, err := store.Upsert(&core.SilenceInput{
		ID:        id,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "upsert insert",
	}, now)
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if gotID != id {
		t.Fatalf("Upsert() id = %q, want caller-provided %q", gotID, id)
	}
	if _, ok := store.Get(id, now); !ok {
		t.Fatal("silence not retrievable under the provided ID")
	}
}

func TestSilenceStore_Upsert_ReplacesExisting(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()
	const id = "550e8400-e29b-41d4-a716-446655440000"

	seed := func(comment string) {
		t.Helper()
		if _, err := store.Upsert(&core.SilenceInput{
			ID:        id,
			Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
			EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
			CreatedBy: "tester",
			Comment:   comment,
		}, now); err != nil {
			t.Fatalf("Upsert(%q) error = %v", comment, err)
		}
	}

	seed("v1")
	seed("v2")

	got, ok := store.Get(id, now)
	if !ok {
		t.Fatal("silence not found after upsert")
	}
	if got.Comment != "v2" {
		t.Fatalf("comment = %q, want replacement %q", got.Comment, "v2")
	}
	if len(store.List(now)) != 1 {
		t.Fatalf("expected exactly 1 silence, got %d", len(store.List(now)))
	}
}

func TestSilenceStore_Upsert_NotifiesOnChange(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	notified := 0
	store.SetOnChange(func() { notified++ })

	if _, err := store.Upsert(&core.SilenceInput{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "notify",
	}, now); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if notified != 1 {
		t.Fatalf("onChange fired %d times, want 1", notified)
	}
}

// Rebuild and UpsertFromAPI back task 6.3's cross-replica resync/apply path
// (see ServiceRegistry.resyncSilenceStore / applySilenceEvent).

func TestSilenceStore_Rebuild_EvictsStaleEntries(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	// Seed with an entry that will NOT be present in the rebuild set —
	// mirroring a silence deleted on another replica while this replica's
	// pub/sub subscription was down.
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "Stale"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "will be evicted",
	}, now); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}

	keep := core.APISilence{
		ID:        "660e8400-e29b-41d4-a716-446655440001",
		Matchers:  []core.APISilenceMatcher{{Name: "alertname", Value: "Keep", IsEqual: true}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "survives rebuild",
	}

	if err := store.Rebuild([]core.APISilence{keep}, now); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}

	if _, ok := store.Get("550e8400-e29b-41d4-a716-446655440000", now); ok {
		t.Fatal("stale entry not in the rebuild set must be evicted")
	}
	got, ok := store.Get(keep.ID, now)
	if !ok {
		t.Fatal("entry present in the rebuild set must survive")
	}
	if got.Comment != keep.Comment {
		t.Fatalf("comment = %q, want %q", got.Comment, keep.Comment)
	}
}

func TestSilenceStore_Rebuild_NotifiesOnChange(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	notified := 0
	store.SetOnChange(func() { notified++ })

	if err := store.Rebuild(nil, now); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	if notified != 1 {
		t.Fatalf("onChange fired %d times, want 1", notified)
	}
}

func TestSilenceStore_Rebuild_InvalidItemIsError(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	bad := core.APISilence{
		ID:       "550e8400-e29b-41d4-a716-446655440000",
		Matchers: nil, // at least 1 matcher is required
		EndsAt:   now.Add(time.Hour).Format(time.RFC3339),
	}

	if err := store.Rebuild([]core.APISilence{bad}, now); err == nil {
		t.Fatal("expected error for a silence with no matchers")
	}
}

func TestSilenceStore_UpsertFromAPI_InsertsUnderSameID(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	item := core.APISilence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		Matchers:  []core.APISilenceMatcher{{Name: "alertname", Value: "X", IsEqual: true}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "from api",
	}

	gotID, err := store.UpsertFromAPI(item, now)
	if err != nil {
		t.Fatalf("UpsertFromAPI() error = %v", err)
	}
	if gotID != item.ID {
		t.Fatalf("UpsertFromAPI() id = %q, want %q", gotID, item.ID)
	}

	got, ok := store.Get(item.ID, now)
	if !ok || got.Comment != "from api" {
		t.Fatalf("silence not mirrored correctly: %+v (found=%v)", got, ok)
	}
}

// Expire (amtool audit F3): DELETE must force a silence into the "expired"
// state, not remove it — expired silences stay queryable via List()/Get()
// until something else (GC, an explicit Delete) removes them.

func TestSilenceStore_Expire_ActiveSilenceBecomesExpiredButStaysListed(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()
	const id = "550e8400-e29b-41d4-a716-446655440000"

	if _, err := store.Upsert(&core.SilenceInput{
		ID:        id,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "expire me",
	}, now); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if ok := store.Expire(id, now); !ok {
		t.Fatal("Expire() = false, want true for an existing silence")
	}

	got, ok := store.Get(id, now)
	if !ok {
		t.Fatal("silence must still be retrievable after Expire — it must not be removed")
	}
	if got.Status.State != "expired" {
		t.Fatalf("Status.State = %q, want %q", got.Status.State, "expired")
	}
	wantEndsAt, _ := time.Parse(time.RFC3339, now.Format(time.RFC3339))
	if endsAt, _ := time.Parse(time.RFC3339, got.EndsAt); !endsAt.Equal(wantEndsAt) {
		t.Fatalf("EndsAt = %v, want forced to now (%v)", endsAt, wantEndsAt)
	}

	// Regression guard for F3 itself: List() (the GET /api/v2/silences read
	// path) must include the now-expired silence.
	all := store.List(now)
	if len(all) != 1 || all[0].ID != id || all[0].Status.State != "expired" {
		t.Fatalf("List() = %+v, want exactly 1 expired silence with id %q", all, id)
	}
}

func TestSilenceStore_Expire_AlreadyExpired_DoesNotExtendEndsAt(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()
	const id = "550e8400-e29b-41d4-a716-446655440000"

	pastEndsAt := now.Add(-time.Hour)
	if err := store.Rebuild([]core.APISilence{{
		ID:        id,
		Matchers:  []core.APISilenceMatcher{{Name: "alertname", Value: "X", IsEqual: true}},
		StartsAt:  now.Add(-2 * time.Hour).Format(time.RFC3339),
		EndsAt:    pastEndsAt.Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "already expired",
	}}, now); err != nil {
		t.Fatalf("seed via Rebuild: %v", err)
	}

	if ok := store.Expire(id, now); !ok {
		t.Fatal("Expire() = false, want true")
	}

	got, ok := store.Get(id, now)
	if !ok {
		t.Fatal("silence must still be retrievable")
	}
	wantEndsAt, _ := time.Parse(time.RFC3339, pastEndsAt.Format(time.RFC3339))
	if endsAt, _ := time.Parse(time.RFC3339, got.EndsAt); !endsAt.Equal(wantEndsAt) {
		t.Fatalf("EndsAt = %v, want unchanged past value %v (Expire must not extend it)", endsAt, wantEndsAt)
	}
	if got.Status.State != "expired" {
		t.Fatalf("Status.State = %q, want %q", got.Status.State, "expired")
	}
}

func TestSilenceStore_Expire_UnknownID_ReturnsFalse(t *testing.T) {
	store := NewSilenceStore()
	if ok := store.Expire("does-not-exist", time.Now().UTC()); ok {
		t.Fatal("Expire() = true for an unknown ID, want false")
	}
}
