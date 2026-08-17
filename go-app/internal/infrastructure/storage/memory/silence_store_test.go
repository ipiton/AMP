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
