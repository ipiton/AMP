package silencing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// Task 6.4 (alertmanager-parity): GC worker leader gating. These tests cover
// the DefaultSilenceManager side of the wrapper — that EnableLeaderGatedGC
// actually prevents Start() from auto-starting GC, and that StartGC/StopGC
// are safe to drive repeatedly (the shape a lock.LeaderElector's
// OnAcquired/OnLost would exercise across leadership flip-flops). The
// election mechanics themselves (exactly one leader, failover, renewal) are
// covered in internal/infrastructure/lock/election_test.go — these tests
// use direct StartGC/StopGC calls rather than a real LeaderElector to stay
// a pure unit test of the manager's own gating logic.

func TestDefaultSilenceManager_DefaultStart_GCRunsUnconditionally(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)
	setupManagerMocks(mockRepo, []*silencing.Silence{})

	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	require.NoError(t, manager.Start(context.Background()))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Stop(ctx)
	}()

	require.True(t, manager.IsGCRunning(), "without EnableLeaderGatedGC, Start() must run GC exactly as before task 6.4")
}

func TestDefaultSilenceManager_LeaderGatedGC_StartDoesNotRunGC(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)
	setupManagerMocks(mockRepo, []*silencing.Silence{})

	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)
	manager.EnableLeaderGatedGC()

	require.NoError(t, manager.Start(context.Background()))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Stop(ctx)
	}()

	require.False(t, manager.IsGCRunning(), "leader-gated GC must not auto-start from Start()")
}

func TestDefaultSilenceManager_StartGCStopGC_DrivenExternally(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Signaled from the mock's Run hook (holds the mock's own lock, unlike
	// reading mockRepo.Calls directly from the test goroutine — that would
	// race with the gcWorker goroutine's concurrent Called() calls).
	gcRan := make(chan struct{}, 8)

	mockRepo.On("ListSilences", mock.Anything, infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}).Return([]*silencing.Silence{}, nil).Once()
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).
		Run(func(mock.Arguments) {
			select {
			case gcRan <- struct{}{}:
			default:
			}
		}).
		Return(int64(0), nil).Maybe()
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil).Maybe()
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil).Maybe()

	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)
	manager.EnableLeaderGatedGC()
	require.NoError(t, manager.Start(context.Background()))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Stop(ctx)
	}()

	require.False(t, manager.IsGCRunning())

	// Simulate winning the election.
	manager.StartGC(context.Background())
	require.True(t, manager.IsGCRunning())
	select {
	case <-gcRan:
	case <-time.After(1 * time.Second):
		t.Fatal("GC worker should run its first pass immediately on start")
	}

	// Idempotent StartGC while already running.
	manager.StartGC(context.Background())
	require.True(t, manager.IsGCRunning())

	// Simulate losing the election.
	manager.StopGC()
	require.False(t, manager.IsGCRunning())

	// Idempotent StopGC while already stopped.
	manager.StopGC()
	require.False(t, manager.IsGCRunning())

	// Simulate winning it back — must not panic (gcWorker is rebuilt fresh;
	// a naive reuse of the stopped worker would panic closing its channels
	// twice).
	manager.StartGC(context.Background())
	require.True(t, manager.IsGCRunning())
	select {
	case <-gcRan:
	case <-time.After(1 * time.Second):
		t.Fatal("GC worker should run again after re-acquiring leadership")
	}
	manager.StopGC()
	require.False(t, manager.IsGCRunning())
}
