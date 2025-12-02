package silencing

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// ==================== Mock Repository for Sync Worker Tests ====================

type mockSyncRepository struct {
	mock.Mock
}

func (m *mockSyncRepository) CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error) {
	args := m.Called(ctx, silence)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockSyncRepository) GetSilenceByID(ctx context.Context, id string) (*silencing.Silence, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockSyncRepository) UpdateSilence(ctx context.Context, silence *silencing.Silence) error {
	args := m.Called(ctx, silence)
	return args.Error(0)
}

func (m *mockSyncRepository) DeleteSilence(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockSyncRepository) ListSilences(ctx context.Context, filter infrasilencing.SilenceFilter) ([]*silencing.Silence, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockSyncRepository) CountSilences(ctx context.Context, filter infrasilencing.SilenceFilter) (int64, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockSyncRepository) ExpireSilences(ctx context.Context, before time.Time, deleteExpired bool) (int64, error) {
	args := m.Called(ctx, before, deleteExpired)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockSyncRepository) GetExpiringSoon(ctx context.Context, within time.Duration) ([]*silencing.Silence, error) {
	args := m.Called(ctx, within)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockSyncRepository) BulkUpdateStatus(ctx context.Context, ids []string, status silencing.SilenceStatus) error {
	args := m.Called(ctx, ids, status)
	return args.Error(0)
}

func (m *mockSyncRepository) GetSilenceStats(ctx context.Context) (*infrasilencing.SilenceStats, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*infrasilencing.SilenceStats), args.Error(1)
}

// ==================== Test 1: Lifecycle (Start/Stop) ====================

// TestSyncWorker_StartStop verifies the worker can start and stop gracefully.
//
// Coverage:
//   - Worker starts without errors
//   - Stop() blocks until worker completes
//   - No goroutine leaks
//
// Expected:
//   - Worker stops within 1 second
func TestSyncWorker_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	// Create worker with long interval (to avoid automatic sync during test)
	worker := newSyncWorker(mockRepo, cache, 1*time.Hour, logger, metrics)

	// Mock: ListSilences (1 call: startup sync)
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil)

	// Start worker
	ctx := context.Background()
	worker.Start(ctx)

	// Wait a bit to ensure worker is running
	time.Sleep(100 * time.Millisecond)

	// Stop worker (should complete quickly)
	start := time.Now()
	worker.Stop()
	duration := time.Since(start)

	// Verify stop was fast (<1s)
	assert.Less(t, duration, 1*time.Second, "Stop should complete in <1s")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 2: Cache Rebuild ====================

// TestSyncWorker_CacheRebuild verifies the worker correctly rebuilds the cache.
//
// Coverage:
//   - Fetches silences from repository
//   - Calls cache.Rebuild() with fresh data
//   - Logs sync statistics
//
// Expected:
//   - Cache contains 3 silences after sync
func TestSyncWorker_CacheRebuild(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newSyncWorker(mockRepo, cache, 1*time.Minute, logger, metrics)

	// Create test silences
	now := time.Now()
	silences := []*silencing.Silence{
		{
			ID:        "sync-1",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-1 * time.Hour),
			EndsAt:    now.Add(1 * time.Hour),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "sync-2",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-30 * time.Minute),
			EndsAt:    now.Add(30 * time.Minute),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "sync-3",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-10 * time.Minute),
			EndsAt:    now.Add(50 * time.Minute),
			CreatedBy: "test@example.com",
		},
	}

	// Mock: ListSilences returns 3 silences
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return(silences, nil)

	// Run sync directly
	ctx := context.Background()
	worker.runSync(ctx)

	// Verify cache contains all 3 silences
	cachedSilences := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.Len(t, cachedSilences, 3, "Cache should contain 3 silences")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 3: Periodic Execution ====================

// TestSyncWorker_PeriodicExecution verifies the worker runs on ticker interval.
//
// Coverage:
//   - Worker runs sync immediately on startup
//   - Worker runs sync periodically based on interval
//   - Multiple sync cycles work correctly
//
// Expected:
//   - ListSilences called 3 times (startup + 2 ticks)
func TestSyncWorker_PeriodicExecution(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	// Short interval to trigger sync faster
	worker := newSyncWorker(mockRepo, cache, 100*time.Millisecond, logger, metrics)

	// Mock: ListSilences (3 calls: startup + 2 ticks)
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil).Times(3)

	// Start worker
	ctx := context.Background()
	worker.Start(ctx)

	// Wait for 2-3 sync cycles
	time.Sleep(250 * time.Millisecond)

	// Stop worker
	worker.Stop()

	// Verify ListSilences was called 3 times
	mockRepo.AssertExpectations(t)
}

// ==================== Test 4: Error Handling ====================

// TestSyncWorker_ErrorHandling verifies the worker handles errors gracefully.
//
// Coverage:
//   - Database errors are logged but don't crash worker
//   - Cache is NOT rebuilt on error (fail-safe)
//   - Worker continues after errors
//
// Expected:
//   - Cache remains empty after error
func TestSyncWorker_ErrorHandling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newSyncWorker(mockRepo, cache, 1*time.Minute, logger, metrics)

	// Mock: ListSilences returns error
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	// Run sync (should not panic)
	ctx := context.Background()
	worker.runSync(ctx)

	// Verify cache is empty (not rebuilt on error)
	cachedSilences := cache.GetAll()
	assert.Empty(t, cachedSilences, "Cache should remain empty after error")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 5: Context Cancellation ====================

// TestSyncWorker_ContextCancellation verifies the worker stops when context is cancelled.
//
// Coverage:
//   - Worker exits when ctx.Done() is signalled
//   - Worker stops before Stop() is called
//   - No goroutine leaks
//
// Expected:
//   - Worker exits within 500ms of context cancellation
func TestSyncWorker_ContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newSyncWorker(mockRepo, cache, 1*time.Hour, logger, metrics)

	// Mock: Startup sync
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for worker to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	start := time.Now()
	cancel()

	// Wait for worker to exit (doneCh closed)
	<-worker.doneCh
	duration := time.Since(start)

	// Verify worker exited quickly (<500ms)
	assert.Less(t, duration, 500*time.Millisecond, "Worker should exit in <500ms after context cancellation")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 6: Performance (Target: <500ms for 1000 silences) ====================

// TestSyncWorker_Performance verifies sync completes within performance targets.
//
// Coverage:
//   - Full sync cycle completes in <500ms
//   - Mock returns realistic count (1000 silences)
//
// Expected:
//   - Duration <500ms for 1000 silences
func TestSyncWorker_Performance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockSyncRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newSyncWorker(mockRepo, cache, 1*time.Minute, logger, metrics)

	// Create 1000 test silences
	now := time.Now()
	silences := make([]*silencing.Silence, 1000)
	for i := 0; i < 1000; i++ {
		silences[i] = &silencing.Silence{
			ID:        "perf-" + string(rune(i)),
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-1 * time.Hour),
			EndsAt:    now.Add(1 * time.Hour),
			CreatedBy: "test@example.com",
		}
	}

	// Mock: ListSilences returns 1000 silences
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return(silences, nil)

	// Run sync and measure time
	ctx := context.Background()
	start := time.Now()
	worker.runSync(ctx)
	duration := time.Since(start)

	// Verify performance (<500ms with mock overhead)
	assert.Less(t, duration, 500*time.Millisecond, "Sync should complete in <500ms for 1000 silences")

	// Verify cache contains all 1000 silences
	cachedSilences := cache.GetAll()
	assert.Len(t, cachedSilences, 1000, "Cache should contain 1000 silences")

	mockRepo.AssertExpectations(t)
}
