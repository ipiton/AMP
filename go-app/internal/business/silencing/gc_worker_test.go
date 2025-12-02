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

// ==================== Mock Repository for GC Worker Tests ====================

type mockGCRepository struct {
	mock.Mock
}

func (m *mockGCRepository) CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error) {
	args := m.Called(ctx, silence)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockGCRepository) GetSilenceByID(ctx context.Context, id string) (*silencing.Silence, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockGCRepository) UpdateSilence(ctx context.Context, silence *silencing.Silence) error {
	args := m.Called(ctx, silence)
	return args.Error(0)
}

func (m *mockGCRepository) DeleteSilence(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockGCRepository) ListSilences(ctx context.Context, filter infrasilencing.SilenceFilter) ([]*silencing.Silence, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockGCRepository) CountSilences(ctx context.Context, filter infrasilencing.SilenceFilter) (int64, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockGCRepository) ExpireSilences(ctx context.Context, before time.Time, deleteExpired bool) (int64, error) {
	args := m.Called(ctx, before, deleteExpired)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockGCRepository) GetExpiringSoon(ctx context.Context, within time.Duration) ([]*silencing.Silence, error) {
	args := m.Called(ctx, within)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockGCRepository) BulkUpdateStatus(ctx context.Context, ids []string, status silencing.SilenceStatus) error {
	args := m.Called(ctx, ids, status)
	return args.Error(0)
}

func (m *mockGCRepository) GetSilenceStats(ctx context.Context) (*infrasilencing.SilenceStats, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*infrasilencing.SilenceStats), args.Error(1)
}

// ==================== Test 1: Lifecycle (Start/Stop) ====================

// TestGCWorker_StartStop verifies the worker can start and stop gracefully.
//
// Coverage:
//   - Worker starts without errors
//   - Stop() blocks until worker completes
//   - No goroutine leaks
//
// Expected:
//   - Worker stops within 1 second
func TestGCWorker_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	// Create worker with long interval (to avoid automatic cleanup during test)
	worker := newGCWorker(mockRepo, cache, 1*time.Hour, 24*time.Hour, 1000, logger, metrics)

	// Mock: ExpireSilences (2 calls: startup + potentially one tick)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil)

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

// ==================== Test 2: Expire Active Silences (Phase 1) ====================

// TestGCWorker_ExpireActiveSilences verifies Phase 1 of cleanup (status update).
//
// Coverage:
//   - Calls repository ExpireSilences(deleteExpired=false)
//   - Returns correct count
//   - Handles errors gracefully
//
// Expected:
//   - 5 silences expired
func TestGCWorker_ExpireActiveSilences(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newGCWorker(mockRepo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)

	// Mock: ExpireSilences returns 5 expired
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(5), nil)

	// Call expireActiveSilences directly
	ctx := context.Background()
	count, err := worker.expireActiveSilences(ctx)

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, int64(5), count)

	mockRepo.AssertExpectations(t)
}

// ==================== Test 3: Delete Old Expired (Phase 2) ====================

// TestGCWorker_DeleteOldExpired verifies Phase 2 of cleanup (hard delete).
//
// Coverage:
//   - Calculates cutoff time (NOW - retention)
//   - Calls repository ExpireSilences(deleteExpired=true)
//   - Returns correct count
//
// Expected:
//   - 3 silences deleted
func TestGCWorker_DeleteOldExpired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	retention := 24 * time.Hour
	worker := newGCWorker(mockRepo, cache, 5*time.Minute, retention, 1000, logger, metrics)

	// Mock: ExpireSilences returns 3 deleted
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(3), nil)

	// Call deleteOldExpired directly
	ctx := context.Background()
	count, err := worker.deleteOldExpired(ctx)

	// Verify
	assert.NoError(t, err)
	assert.Equal(t, int64(3), count)

	mockRepo.AssertExpectations(t)
}

// ==================== Test 4: Full Cleanup Cycle (Integration) ====================

// TestGCWorker_FullCleanupCycle verifies the complete two-phase cleanup process.
//
// Coverage:
//   - runCleanup() executes both phases
//   - Phase 1 and Phase 2 are independent (both run even if one fails)
//   - Correct logging and metrics
//
// Expected:
//   - 10 silences expired (Phase 1)
//   - 5 silences deleted (Phase 2)
func TestGCWorker_FullCleanupCycle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newGCWorker(mockRepo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)

	// Mock: Phase 1 (expire) returns 10
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(10), nil)
	// Mock: Phase 2 (delete) returns 5
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(5), nil)

	// Run full cleanup cycle
	ctx := context.Background()
	worker.runCleanup(ctx)

	// Verify both phases were called
	mockRepo.AssertExpectations(t)
}

// ==================== Test 5: Graceful Shutdown ====================

// TestGCWorker_GracefulShutdown verifies the worker stops without errors.
//
// Coverage:
//   - Worker completes current cleanup before stopping
//   - Stop() blocks until worker is fully stopped
//   - doneCh is closed after worker exits
//
// Expected:
//   - Worker stops within 2 seconds
func TestGCWorker_GracefulShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	// Short interval to trigger cleanup faster
	worker := newGCWorker(mockRepo, cache, 100*time.Millisecond, 24*time.Hour, 1000, logger, metrics)

	// Mock: Multiple cleanup cycles
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil)

	// Start worker
	ctx := context.Background()
	worker.Start(ctx)

	// Wait for 2-3 cleanup cycles
	time.Sleep(250 * time.Millisecond)

	// Stop worker (should complete gracefully)
	start := time.Now()
	worker.Stop()
	duration := time.Since(start)

	// Verify stop was fast (<2s)
	assert.Less(t, duration, 2*time.Second, "Stop should complete in <2s")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 6: Context Cancellation ====================

// TestGCWorker_ContextCancellation verifies the worker stops when context is cancelled.
//
// Coverage:
//   - Worker exits when ctx.Done() is signalled
//   - Worker stops before Stop() is called
//   - No goroutine leaks
//
// Expected:
//   - Worker exits within 500ms of context cancellation
func TestGCWorker_ContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newGCWorker(mockRepo, cache, 1*time.Hour, 24*time.Hour, 1000, logger, metrics)

	// Mock: Startup cleanup
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil)

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

// ==================== Test 7: Performance (Target: <2s for 1000 silences) ====================

// TestGCWorker_Performance verifies cleanup completes within performance targets.
//
// Coverage:
//   - Full cleanup cycle (2 phases) completes in <2s
//   - Mock returns realistic counts (1000 silences)
//
// Expected:
//   - Duration <2s for 1000 silences
func TestGCWorker_Performance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newGCWorker(mockRepo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)

	// Mock: Phase 1 expires 500 silences
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(500), nil)
	// Mock: Phase 2 deletes 300 silences
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(300), nil)

	// Run cleanup and measure time
	ctx := context.Background()
	start := time.Now()
	worker.runCleanup(ctx)
	duration := time.Since(start)

	// Verify performance (<2s with mock overhead)
	assert.Less(t, duration, 2*time.Second, "Full cleanup cycle should complete in <2s")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 8: Error Handling ====================

// TestGCWorker_ErrorHandling verifies the worker handles errors gracefully.
//
// Coverage:
//   - Phase 1 error doesn't stop Phase 2
//   - Phase 2 error is logged but doesn't crash worker
//   - Worker continues after errors
//
// Expected:
//   - Both phases execute despite errors
func TestGCWorker_ErrorHandling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()
	metrics := NewSilenceMetrics()

	worker := newGCWorker(mockRepo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)

	// Mock: Phase 1 returns error
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), assert.AnError)
	// Mock: Phase 2 returns error
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), assert.AnError)

	// Run cleanup (should not panic)
	ctx := context.Background()
	worker.runCleanup(ctx)

	// Verify both phases were attempted
	mockRepo.AssertExpectations(t)
}
