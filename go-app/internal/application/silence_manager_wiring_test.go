package application

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// ExpireSilences satisfies infrasilencing.SilenceRepository for
// fakeSilenceRepository (defined in silence_rehydration_test.go) so the
// gcWorker started by DefaultSilenceManager.Start() doesn't panic on the
// embedded nil interface.
func (f *fakeSilenceRepository) ExpireSilences(_ context.Context, _ time.Time, _ bool) (int64, error) {
	return 0, nil
}

// TestInitializeSilenceManager_LiteProfileSkips verifies task 2.1's skip
// condition: lite profile has no persistent silence repository, so the
// manager must not be constructed (and NewDefaultSilenceManager's nil-repo
// panic must never be reached).
func TestInitializeSilenceManager_LiteProfileSkips(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileLite},
		// Deliberately non-nil to prove the profile check short-circuits
		// before the nil-repo check would even matter.
		silenceRepo: &fakeSilenceRepository{},
	}

	if err := r.initializeSilenceManager(context.Background()); err != nil {
		t.Fatalf("initializeSilenceManager in lite profile: %v", err)
	}
	if r.silenceManager != nil {
		t.Fatal("silenceManager must stay nil in lite profile")
	}
}

// TestInitializeSilenceManager_NilRepoSkips verifies the second skip
// condition: standard profile but persistence init failed/was skipped, so
// silenceRepo is nil. Must be a clean skip, not a panic.
func TestInitializeSilenceManager_NilRepoSkips(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileStandard},
		// silenceRepo intentionally left nil.
	}

	if err := r.initializeSilenceManager(context.Background()); err != nil {
		t.Fatalf("initializeSilenceManager with nil repo: %v", err)
	}
	if r.silenceManager != nil {
		t.Fatal("silenceManager must stay nil when silenceRepo is nil")
	}
}

// TestInitializeSilenceManager_StartsAndStops verifies the happy path: a
// present repository in standard profile gets a running manager, and
// Stop() returns promptly (no goroutine leak) within a short timeout.
func TestInitializeSilenceManager_StartsAndStops(t *testing.T) {
	repo := &fakeSilenceRepository{}
	r := &ServiceRegistry{
		logger:      slog.Default(),
		config:      &appconfig.Config{Profile: appconfig.ProfileStandard},
		silenceRepo: repo,
	}

	if err := r.initializeSilenceManager(context.Background()); err != nil {
		t.Fatalf("initializeSilenceManager: %v", err)
	}
	if r.silenceManager == nil {
		t.Fatal("silenceManager must be set on successful start")
	}

	// GC/stats worker did its job: GetStats must succeed once started (it
	// errors with ErrManagerNotStarted otherwise) — proves Start() actually
	// ran the initial cache sync rather than silently no-op'ing.
	stats, err := r.silenceManager.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats after start: %v", err)
	}
	if stats == nil {
		t.Fatal("GetStats returned nil stats")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.silenceManager.Stop(stopCtx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within timeout — possible goroutine leak")
	}
}

// TestInitializeSilenceManager_StartErrorPropagates verifies that a failing
// initial cache sync (simulating an unreachable database) surfaces as an
// error from initializeSilenceManager, so Initialize()'s caller can log a
// degraded reason instead of silently pretending the manager is running.
func TestInitializeSilenceManager_StartErrorPropagates(t *testing.T) {
	repo := &fakeSilenceRepository{listErr: errors.New("connection refused")}
	r := &ServiceRegistry{
		logger:      slog.Default(),
		config:      &appconfig.Config{Profile: appconfig.ProfileStandard},
		silenceRepo: repo,
	}

	if err := r.initializeSilenceManager(context.Background()); err == nil {
		t.Fatal("expected error when initial cache sync fails")
	}
	if r.silenceManager != nil {
		t.Fatal("silenceManager must stay nil when Start() fails")
	}
}
