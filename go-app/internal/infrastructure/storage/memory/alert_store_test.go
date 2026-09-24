package memory

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// PARITY-RESOLVE-TIMEOUT-ENDSAT: a firing alert received without endsAt is
// stored with endsAt = receivedAt + resolve_timeout (upstream Alertmanager),
// never left empty — an empty endsAt used to be served as endsAt == updatedAt,
// i.e. as an alert that had already resolved.

var endsAtTestNow = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

func firingInput(name string) core.AlertIngestInput {
	return core.AlertIngestInput{
		Labels:   map[string]string{"alertname": name},
		StartsAt: endsAtTestNow.Add(-time.Hour).Format(time.RFC3339),
	}
}

func ingest(t *testing.T, s *AlertStore, now time.Time, inputs ...core.AlertIngestInput) {
	t.Helper()
	if err := s.IngestBatch(inputs, now); err != nil {
		t.Fatalf("IngestBatch() error = %v", err)
	}
}

// storedAlert returns the single stored alert with the given alertname,
// resolved ones included.
func storedAlert(t *testing.T, s *AlertStore, name string) core.APIAlert {
	t.Helper()
	var found []core.APIAlert
	for _, a := range s.List("", true) {
		if a.Labels["alertname"] == name {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("stored alerts named %q = %d, want 1", name, len(found))
	}
	return found[0]
}

func assertEndsAt(t *testing.T, a core.APIAlert, want time.Time) {
	t.Helper()
	if a.EndsAt == nil {
		t.Fatalf("EndsAt = nil, want %s", want.Format(time.RFC3339))
	}
	if got := *a.EndsAt; got != want.UTC().Format(time.RFC3339) {
		t.Fatalf("EndsAt = %s, want %s", got, want.UTC().Format(time.RFC3339))
	}
}

func TestAlertStore_FiringWithoutEndsAt_DefaultResolveTimeout(t *testing.T) {
	s := NewAlertStore() // no provider ⇒ upstream default

	ingest(t, s, endsAtTestNow, firingInput("A"))

	a := storedAlert(t, s, "A")
	if a.Status != "firing" {
		t.Fatalf("Status = %q, want firing (the synthesized endsAt must not resolve the alert)", a.Status)
	}
	assertEndsAt(t, a, endsAtTestNow.Add(alertconv.DefaultResolveTimeout))
	if alertconv.DefaultResolveTimeout != 5*time.Minute {
		t.Fatalf("DefaultResolveTimeout = %s, want upstream 5m", alertconv.DefaultResolveTimeout)
	}
}

func TestAlertStore_ResolveTimeoutProvider(t *testing.T) {
	s := NewAlertStore()
	s.SetResolveTimeout(func() time.Duration { return time.Hour })
	ingest(t, s, endsAtTestNow, firingInput("A"))
	assertEndsAt(t, storedAlert(t, s, "A"), endsAtTestNow.Add(time.Hour))

	// A reload changes the provider's answer: the NEXT ingest uses it, while
	// the already stored alert keeps its window (no retroactive rewrite).
	s.SetResolveTimeout(func() time.Duration { return 10 * time.Minute })
	ingest(t, s, endsAtTestNow, firingInput("B"))
	assertEndsAt(t, storedAlert(t, s, "B"), endsAtTestNow.Add(10*time.Minute))
	assertEndsAt(t, storedAlert(t, s, "A"), endsAtTestNow.Add(time.Hour))
}

func TestAlertStore_ResolveTimeoutProvider_NonPositiveFallsBackToDefault(t *testing.T) {
	for name, d := range map[string]time.Duration{"zero": 0, "negative": -time.Minute} {
		t.Run(name, func(t *testing.T) {
			s := NewAlertStore()
			s.SetResolveTimeout(func() time.Duration { return d })
			ingest(t, s, endsAtTestNow, firingInput("A"))
			assertEndsAt(t, storedAlert(t, s, "A"), endsAtTestNow.Add(alertconv.DefaultResolveTimeout))
		})
	}
}

func TestAlertStore_ResendExtendsResolveTimeoutWindow(t *testing.T) {
	s := NewAlertStore()
	later := endsAtTestNow.Add(3 * time.Minute)

	ingest(t, s, endsAtTestNow, firingInput("A"))
	ingest(t, s, later, firingInput("A"))

	a := storedAlert(t, s, "A")
	assertEndsAt(t, a, later.Add(alertconv.DefaultResolveTimeout))
	if a.UpdatedAt != later.Format(time.RFC3339) {
		t.Fatalf("UpdatedAt = %s, want %s (re-send is an update)", a.UpdatedAt, later.Format(time.RFC3339))
	}
}

func TestAlertStore_ExplicitEndsAtIsKept(t *testing.T) {
	s := NewAlertStore()
	explicit := endsAtTestNow.Add(2 * time.Hour)
	in := firingInput("A")
	in.EndsAt = explicit.Format(time.RFC3339)

	ingest(t, s, endsAtTestNow, in)
	assertEndsAt(t, storedAlert(t, s, "A"), explicit)

	// Re-sent with the same explicit endsAt later: still not overwritten.
	ingest(t, s, endsAtTestNow.Add(time.Minute), in)
	assertEndsAt(t, storedAlert(t, s, "A"), explicit)
}

func TestAlertStore_ResolvedAlertsAreNotStamped(t *testing.T) {
	t.Run("explicit resolved status without endsAt ⇒ resolve time", func(t *testing.T) {
		s := NewAlertStore()
		ingest(t, s, endsAtTestNow, firingInput("A"))

		resolvedAt := endsAtTestNow.Add(time.Minute)
		in := firingInput("A")
		in.Status = "resolved"
		ingest(t, s, resolvedAt, in)

		a := storedAlert(t, s, "A")
		if a.Status != "resolved" {
			t.Fatalf("Status = %q, want resolved", a.Status)
		}
		assertEndsAt(t, a, resolvedAt)
	})

	t.Run("endsAt in the past ⇒ resolved with that endsAt", func(t *testing.T) {
		s := NewAlertStore()
		past := endsAtTestNow.Add(-time.Minute)
		in := firingInput("A")
		in.EndsAt = past.Format(time.RFC3339)
		ingest(t, s, endsAtTestNow, in)

		a := storedAlert(t, s, "A")
		if a.Status != "resolved" {
			t.Fatalf("Status = %q, want resolved", a.Status)
		}
		assertEndsAt(t, a, past)
	})
}

func TestAlertStore_RestoreFromPersistence_StampsFiringWithoutEndsAt(t *testing.T) {
	s := NewAlertStore()
	s.SetResolveTimeout(func() time.Duration { return 15 * time.Minute })

	persisted := &core.Alert{
		Fingerprint: "fp-a",
		AlertName:   "A",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "A"},
		StartsAt:    endsAtTestNow.Add(-time.Hour),
	}
	if err := s.RestoreFromPersistence([]*core.Alert{persisted}, endsAtTestNow); err != nil {
		t.Fatalf("RestoreFromPersistence() error = %v", err)
	}

	a := storedAlert(t, s, "A")
	if a.Status != "firing" {
		t.Fatalf("Status = %q, want firing", a.Status)
	}
	assertEndsAt(t, a, endsAtTestNow.Add(15*time.Minute))
	if persisted.EndsAt != nil {
		t.Fatalf("persisted alert was mutated: EndsAt = %v, want nil (the database keeps what the sender sent)", persisted.EndsAt)
	}
}
