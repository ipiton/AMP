package silencing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// setupManagerMocks sets up common mocks for lifecycle tests.
// This includes initial sync and worker background calls.
func setupManagerMocks(mockRepo *mockRepository, silences []*silencing.Silence) {
	// Initial sync (Start method)
	mockRepo.On("ListSilences", mock.MatchedBy(func(ctx context.Context) bool {
		return true // Accept any context type
	}), infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}).Return(silences, nil).Once()

	// Workers background calls (use Maybe() to allow 0 or more calls)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil).Maybe()
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil).Maybe()
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return(silences, nil).Maybe()
}

// ==================== Test 1: Start Success ====================

// TestStart_Success verifies the manager starts successfully.
//
// Coverage:
//   - Loads active silences from repository
//   - Starts GC and sync workers
//   - Sets started flag
//
// Expected:
//   - Start() returns nil
//   - Manager is in started state
func TestStart_Success(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Mock: ListSilences returns 3 active silences (initial sync)
	now := time.Now()
	silences := []*silencing.Silence{
		{
			ID:        "start-1",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-1 * time.Hour),
			EndsAt:    now.Add(1 * time.Hour),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "start-2",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-30 * time.Minute),
			EndsAt:    now.Add(30 * time.Minute),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "start-3",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-10 * time.Minute),
			EndsAt:    now.Add(50 * time.Minute),
			CreatedBy: "test@example.com",
		},
	}
	setupManagerMocks(mockRepo, silences)

	// Create manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	// Start manager
	err := manager.Start(context.Background())

	// Verify
	assert.NoError(t, err, "Start should succeed")
	assert.True(t, manager.started.Load(), "Manager should be started")

	// Cleanup
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager.Stop(shutdownCtx)

	mockRepo.AssertExpectations(t)
}

// ==================== Test 2: Start Already Started ====================

// TestStart_AlreadyStarted verifies Start() returns error if already started.
//
// Coverage:
//   - Calling Start() twice returns error
//
// Expected:
//   - Second Start() returns error "already started"
func TestStart_AlreadyStarted(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Setup mocks
	setupManagerMocks(mockRepo, []*silencing.Silence{})

	// Create manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	// Start first time
	err := manager.Start(context.Background())
	assert.NoError(t, err, "First Start should succeed")

	// Start second time
	err = manager.Start(context.Background())
	assert.Error(t, err, "Second Start should fail")
	assert.Contains(t, err.Error(), "already started")

	// Cleanup
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager.Stop(shutdownCtx)

	mockRepo.AssertExpectations(t)
}

// ==================== Test 3: Start Initial Sync Failed ====================

// TestStart_InitialSyncFailed verifies Start() fails if initial cache sync fails.
//
// Coverage:
//   - Returns error if repository ListSilences fails
//   - Manager not started on error
//
// Expected:
//   - Start() returns error
//   - Manager is NOT started
func TestStart_InitialSyncFailed(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Mock: ListSilences fails
	mockRepo.On("ListSilences", context.Background(), infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}).Return(nil, assert.AnError)

	// Create manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	// Start manager
	err := manager.Start(context.Background())

	// Verify
	assert.Error(t, err, "Start should fail")
	assert.Contains(t, err.Error(), "initial cache sync failed")
	assert.False(t, manager.started.Load(), "Manager should NOT be started")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 4: Stop Success ====================

// TestStop_Success verifies the manager stops gracefully.
//
// Coverage:
//   - Cancels manager context
//   - Stops GC and sync workers
//   - Returns within timeout
//
// Expected:
//   - Stop() returns nil
//   - Workers stopped gracefully
func TestStop_Success(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Mock: Initial sync
	mockRepo.On("ListSilences", context.Background(), infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}).Return([]*silencing.Silence{}, nil).Once()

	// Mock: Workers
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil).Maybe()
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil).Maybe()
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil).Maybe()

	// Create and start manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)
	err := manager.Start(context.Background())
	assert.NoError(t, err)

	// Wait a bit for workers to start
	time.Sleep(100 * time.Millisecond)

	// Stop manager
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err = manager.Stop(shutdownCtx)
	duration := time.Since(start)

	// Verify
	assert.NoError(t, err, "Stop should succeed")
	assert.Less(t, duration, 5*time.Second, "Stop should complete in <5s")
	assert.True(t, manager.shutdown.Load(), "Manager should be shutdown")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 5: Stop Not Started ====================

// TestStop_NotStarted verifies Stop() returns error if manager not started.
//
// Coverage:
//   - Calling Stop() on unstarted manager returns error
//
// Expected:
//   - Stop() returns error "not started"
func TestStop_NotStarted(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Create manager (don't start)
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	// Stop manager
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Stop(shutdownCtx)

	// Verify
	assert.Error(t, err, "Stop should fail")
	assert.Contains(t, err.Error(), "not started")
}

// ==================== Test 6: Stop Idempotent ====================

// TestStop_Idempotent verifies Stop() can be called multiple times.
//
// Coverage:
//   - Calling Stop() twice is safe (no-op second time)
//
// Expected:
//   - First Stop() succeeds
//   - Second Stop() is no-op (returns nil)
func TestStop_Idempotent(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Mock: Initial sync
	mockRepo.On("ListSilences", context.Background(), infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}).Return([]*silencing.Silence{}, nil).Once()

	// Mock: Workers
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(0), nil).Maybe()
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(0), nil).Maybe()
	mockRepo.On("ListSilences", mock.Anything, mock.Anything).Return([]*silencing.Silence{}, nil).Maybe()

	// Create and start manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)
	err := manager.Start(context.Background())
	assert.NoError(t, err)

	// Stop first time
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.Stop(shutdownCtx)
	assert.NoError(t, err, "First Stop should succeed")

	// Stop second time
	err = manager.Stop(shutdownCtx)
	assert.NoError(t, err, "Second Stop should be no-op")

	mockRepo.AssertExpectations(t)
}

// ==================== Test 7: GetStats Success ====================

// TestGetStats_Success verifies GetStats() returns correct statistics.
//
// Coverage:
//   - Returns cache statistics
//   - Returns repository counts (from cache)
//
// Expected:
//   - GetStats() returns valid stats
//   - ActiveSilences = 3
func TestGetStats_Success(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Mock: Initial sync with 3 active silences
	now := time.Now()
	silences := []*silencing.Silence{
		{
			ID:        "stats-1",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-1 * time.Hour),
			EndsAt:    now.Add(1 * time.Hour),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "stats-2",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-30 * time.Minute),
			EndsAt:    now.Add(30 * time.Minute),
			CreatedBy: "test@example.com",
		},
		{
			ID:        "stats-3",
			Status:    silencing.SilenceStatusActive,
			StartsAt:  now.Add(-10 * time.Minute),
			EndsAt:    now.Add(50 * time.Minute),
			CreatedBy: "test@example.com",
		},
	}
	setupManagerMocks(mockRepo, silences)

	// Create and start manager
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)
	err := manager.Start(context.Background())
	assert.NoError(t, err)

	// Get stats
	stats, err := manager.GetStats(context.Background())

	// Verify
	assert.NoError(t, err, "GetStats should succeed")
	assert.NotNil(t, stats, "Stats should not be nil")
	assert.Equal(t, 3, stats.CacheSize, "Cache size should be 3")
	assert.Equal(t, int64(3), stats.ActiveSilences, "Active silences should be 3")
	assert.Equal(t, 3, stats.CacheByStatus[silencing.SilenceStatusActive], "Cache should have 3 active silences")

	// Cleanup
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager.Stop(shutdownCtx)

	mockRepo.AssertExpectations(t)
}

// ==================== Test 8: GetStats Not Started ====================

// TestGetStats_NotStarted verifies GetStats() returns error if manager not started.
//
// Coverage:
//   - Calling GetStats() on unstarted manager returns error
//
// Expected:
//   - GetStats() returns ErrManagerNotStarted
func TestGetStats_NotStarted(t *testing.T) {
	logger := slog.Default()
	mockRepo := new(mockRepository)
	mockMatcher := new(mockMatcher)

	// Create manager (don't start)
	manager := NewDefaultSilenceManager(mockRepo, mockMatcher, logger, nil)

	// Get stats
	stats, err := manager.GetStats(context.Background())

	// Verify
	assert.Error(t, err, "GetStats should fail")
	assert.Equal(t, ErrManagerNotStarted, err, "Should return ErrManagerNotStarted")
	assert.Nil(t, stats, "Stats should be nil")
}
