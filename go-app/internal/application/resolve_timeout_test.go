package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// PARITY-RESOLVE-TIMEOUT-ENDSAT: the memory store stamps firing alerts that
// arrive without endsAt with receivedAt + global.resolve_timeout, read from
// the LIVE config on every ingest batch.

func configWithResolveTimeout(d time.Duration) *appconfig.Config {
	rt := infraroute.Duration(d)
	return &appconfig.Config{Routing: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{ResolveTimeout: &rt},
	}}
}

func TestResolveTimeoutFromConfig(t *testing.T) {
	zero := infraroute.Duration(0)
	cases := []struct {
		name string
		cfg  *appconfig.Config
		want time.Duration
	}{
		{"nil config", nil, alertconv.DefaultResolveTimeout},
		{"no route section (lite)", &appconfig.Config{}, alertconv.DefaultResolveTimeout},
		{"no global section", &appconfig.Config{Routing: &infraroute.RouteConfig{}}, alertconv.DefaultResolveTimeout},
		{"global without resolve_timeout", &appconfig.Config{Routing: &infraroute.RouteConfig{Global: &infraroute.GlobalConfig{}}}, alertconv.DefaultResolveTimeout},
		{"non-positive resolve_timeout", &appconfig.Config{Routing: &infraroute.RouteConfig{Global: &infraroute.GlobalConfig{ResolveTimeout: &zero}}}, alertconv.DefaultResolveTimeout},
		{"explicit 1h", configWithResolveTimeout(time.Hour), time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTimeoutFromConfig(tc.cfg); got != tc.want {
				t.Fatalf("resolveTimeoutFromConfig() = %s, want %s", got, tc.want)
			}
		})
	}
}

// The alertconv fallback and the parsed-config default must stay the same
// value: GlobalConfig.Defaults() only runs when `global:` is present, and
// every other path falls back to alertconv.DefaultResolveTimeout.
func TestDefaultResolveTimeout_MatchesGlobalConfigDefaults(t *testing.T) {
	g := &infraroute.GlobalConfig{}
	g.Defaults()
	if g.ResolveTimeout == nil {
		t.Fatal("GlobalConfig.Defaults() left ResolveTimeout nil")
	}
	if got := time.Duration(*g.ResolveTimeout); got != alertconv.DefaultResolveTimeout {
		t.Fatalf("GlobalConfig default = %s, alertconv.DefaultResolveTimeout = %s; they must match", got, alertconv.DefaultResolveTimeout)
	}
}

func TestNewAlertStore_FollowsConfigReload(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	r := &ServiceRegistry{config: configWithResolveTimeout(time.Hour)}
	store := r.newAlertStore()

	post := func(name string) {
		t.Helper()
		if err := store.IngestBatch([]core.AlertIngestInput{{
			Labels:   map[string]string{"alertname": name},
			StartsAt: now.Format(time.RFC3339),
		}}, now); err != nil {
			t.Fatalf("IngestBatch() error = %v", err)
		}
	}
	endsAtOf := func(name string) string {
		t.Helper()
		for _, a := range store.List("", false) {
			if a.Labels["alertname"] == name && a.EndsAt != nil {
				return *a.EndsAt
			}
		}
		t.Fatalf("alert %q not found or has no endsAt", name)
		return ""
	}

	post("A")
	if got, want := endsAtOf("A"), now.Add(time.Hour).Format(time.RFC3339); got != want {
		t.Fatalf("A endsAt = %s, want %s (global.resolve_timeout: 1h)", got, want)
	}

	// /-/reload swaps r.config; the store must pick it up without rewiring.
	r.config = configWithResolveTimeout(2 * time.Minute)
	post("B")
	if got, want := endsAtOf("B"), now.Add(2*time.Minute).Format(time.RFC3339); got != want {
		t.Fatalf("B endsAt = %s, want %s (reloaded resolve_timeout: 2m)", got, want)
	}
	if got, want := endsAtOf("A"), now.Add(time.Hour).Format(time.RFC3339); got != want {
		t.Fatalf("A endsAt = %s after reload, want %s (no retroactive rewrite)", got, want)
	}
}

func TestRehydrateAlertStore_StampsResolveTimeoutFromConfig(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: configWithResolveTimeout(30 * time.Minute),
		storage: &fakeAlertStorage{alerts: []*core.Alert{{
			Fingerprint: "fp-rt",
			AlertName:   "NodeDown",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"alertname": "NodeDown"},
			StartsAt:    time.Now().UTC().Add(-time.Hour),
		}}},
	}
	r.alertStore = r.newAlertStore()

	before := time.Now().UTC()
	if err := r.rehydrateAlertStore(context.Background()); err != nil {
		t.Fatalf("rehydrateAlertStore: %v", err)
	}

	alerts := r.alertStore.List("", false)
	if len(alerts) != 1 || alerts[0].EndsAt == nil {
		t.Fatalf("expected 1 firing alert with endsAt, got %+v", alerts)
	}
	endsAt, err := time.Parse(time.RFC3339, *alerts[0].EndsAt)
	if err != nil {
		t.Fatalf("parse endsAt: %v", err)
	}
	// RFC3339 drops sub-seconds, so allow one second of truncation.
	if lo, hi := before.Add(30*time.Minute).Add(-time.Second), time.Now().UTC().Add(30*time.Minute); endsAt.Before(lo) || endsAt.After(hi) {
		t.Fatalf("endsAt = %s, want restore time + 30m (in [%s, %s])", endsAt, lo, hi)
	}
}
