package silencing

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// Mock repository for testing
type mockRepository struct {
	mock.Mock
}

func (m *mockRepository) CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error) {
	args := m.Called(ctx, silence)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockRepository) GetSilenceByID(ctx context.Context, id string) (*silencing.Silence, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*silencing.Silence), args.Error(1)
}

func (m *mockRepository) UpdateSilence(ctx context.Context, silence *silencing.Silence) error {
	args := m.Called(ctx, silence)
	return args.Error(0)
}

func (m *mockRepository) DeleteSilence(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockRepository) ListSilences(ctx context.Context, filter infrasilencing.SilenceFilter) ([]*silencing.Silence, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockRepository) CountSilences(ctx context.Context, filter infrasilencing.SilenceFilter) (int64, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockRepository) ExpireSilences(ctx context.Context, before time.Time, deleteExpired bool) (int64, error) {
	args := m.Called(ctx, before, deleteExpired)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockRepository) GetExpiringSoon(ctx context.Context, window time.Duration) ([]*silencing.Silence, error) {
	args := m.Called(ctx, window)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*silencing.Silence), args.Error(1)
}

func (m *mockRepository) BulkUpdateStatus(ctx context.Context, ids []string, status silencing.SilenceStatus) error {
	args := m.Called(ctx, ids, status)
	return args.Error(0)
}

func (m *mockRepository) GetSilenceStats(ctx context.Context) (*infrasilencing.SilenceStats, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*infrasilencing.SilenceStats), args.Error(1)
}

// Mock matcher (not used in CRUD tests but required for manager)
type mockMatcher struct{}

func (m *mockMatcher) Matches(ctx context.Context, alert silencing.Alert, silence *silencing.Silence) (bool, error) {
	return false, nil
}

func (m *mockMatcher) MatchesAny(ctx context.Context, alert silencing.Alert, silences []*silencing.Silence) ([]string, error) {
	return nil, nil
}

// Test helper: create started manager with mock repository
func newTestManagerWithMock() (*DefaultSilenceManager, *mockRepository) {
	repo := new(mockRepository)
	matcher := &mockMatcher{}
	logger := slog.Default()

	manager := NewDefaultSilenceManager(repo, matcher, logger, nil)
	manager.started.Store(true) // Fake Start() for testing

	return manager, repo
}

// TestCreateSilence_Success tests successful silence creation.
func TestCreateSilence_Success(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	inputSilence := newTestSilence("test-id", silencing.SilenceStatusActive)
	createdSilence := *inputSilence
	createdSilence.CreatedAt = time.Now()

	repo.On("CreateSilence", ctx, inputSilence).Return(&createdSilence, nil)

	result, err := manager.CreateSilence(ctx, inputSilence)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-id", result.ID)
	repo.AssertExpectations(t)

	// Verify cache was updated (active silence)
	cached, found := manager.cache.Get("test-id")
	assert.True(t, found, "Active silence should be in cache")
	assert.Equal(t, "test-id", cached.ID)
}

// TestCreateSilence_NotStarted tests error when manager not started.
func TestCreateSilence_NotStarted(t *testing.T) {
	manager, _ := newTestManagerWithMock()
	manager.started.Store(false) // Not started
	ctx := context.Background()

	silence := newTestSilence("test-id", silencing.SilenceStatusActive)

	_, err := manager.CreateSilence(ctx, silence)

	assert.ErrorIs(t, err, ErrManagerNotStarted)
}

// TestCreateSilence_Shutdown tests error when manager is shutting down.
func TestCreateSilence_Shutdown(t *testing.T) {
	manager, _ := newTestManagerWithMock()
	manager.shutdown.Store(true) // Shutting down
	ctx := context.Background()

	silence := newTestSilence("test-id", silencing.SilenceStatusActive)

	_, err := manager.CreateSilence(ctx, silence)

	assert.ErrorIs(t, err, ErrManagerShutdown)
}

// TestGetSilence_CacheHit tests cache hit scenario.
func TestGetSilence_CacheHit(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache
	silence := newTestSilence("cached-id", silencing.SilenceStatusActive)
	manager.cache.Set(silence)

	result, err := manager.GetSilence(ctx, "cached-id")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "cached-id", result.ID)

	// Verify repository was NOT called (cache hit)
	repo.AssertNotCalled(t, "GetSilenceByID")
}

// TestGetSilence_CacheMiss tests cache miss with fallback to repository.
func TestGetSilence_CacheMiss(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	silence := newTestSilence("db-id", silencing.SilenceStatusActive)
	repo.On("GetSilenceByID", ctx, "db-id").Return(silence, nil)

	result, err := manager.GetSilence(ctx, "db-id")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "db-id", result.ID)
	repo.AssertExpectations(t)

	// Verify active silence was added to cache
	cached, found := manager.cache.Get("db-id")
	assert.True(t, found)
	assert.Equal(t, "db-id", cached.ID)
}

// TestGetSilence_NotFound tests silence not found error.
func TestGetSilence_NotFound(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	repo.On("GetSilenceByID", ctx, "nonexistent").Return(nil, infrasilencing.ErrSilenceNotFound)

	result, err := manager.GetSilence(ctx, "nonexistent")

	assert.ErrorIs(t, err, infrasilencing.ErrSilenceNotFound)
	assert.Nil(t, result)
	repo.AssertExpectations(t)
}

// TestUpdateSilence_InvalidatesCache tests cache invalidation on update.
func TestUpdateSilence_InvalidatesCache(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache
	silence := newTestSilence("update-id", silencing.SilenceStatusActive)
	manager.cache.Set(silence)

	// Verify in cache
	_, found := manager.cache.Get("update-id")
	require.True(t, found, "Setup: silence should be in cache")

	// Update silence (change comment)
	silence.Comment = "Updated comment"
	repo.On("UpdateSilence", ctx, silence).Return(nil)

	err := manager.UpdateSilence(ctx, silence)

	assert.NoError(t, err)
	repo.AssertExpectations(t)

	// Verify cache was updated (invalidated + re-added for active)
	cached, found := manager.cache.Get("update-id")
	assert.True(t, found, "Active silence should be re-added to cache")
	assert.Equal(t, "Updated comment", cached.Comment)
}

// TestUpdateSilence_StatusChange tests status change from active to expired.
func TestUpdateSilence_StatusChange(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache with active silence
	silence := newTestSilence("expire-id", silencing.SilenceStatusActive)
	manager.cache.Set(silence)

	// Change status to expired
	silence.Status = silencing.SilenceStatusExpired
	repo.On("UpdateSilence", ctx, silence).Return(nil)

	err := manager.UpdateSilence(ctx, silence)

	assert.NoError(t, err)
	repo.AssertExpectations(t)

	// Verify expired silence was removed from cache (not re-added)
	_, found := manager.cache.Get("expire-id")
	assert.False(t, found, "Expired silence should NOT be in cache")
}

// TestDeleteSilence_RemovesFromCache tests cache removal on delete.
func TestDeleteSilence_RemovesFromCache(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache
	silence := newTestSilence("delete-id", silencing.SilenceStatusActive)
	manager.cache.Set(silence)

	repo.On("DeleteSilence", ctx, "delete-id").Return(nil)

	err := manager.DeleteSilence(ctx, "delete-id")

	assert.NoError(t, err)
	repo.AssertExpectations(t)

	// Verify removed from cache
	_, found := manager.cache.Get("delete-id")
	assert.False(t, found, "Deleted silence should be removed from cache")
}

// TestListSilences_FastPath tests cache-based filtering (fast path).
func TestListSilences_FastPath(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache with active silences
	silence1 := newTestSilence("active-1", silencing.SilenceStatusActive)
	silence2 := newTestSilence("active-2", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)

	// Filter for active only (should use cache)
	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
	}

	silences, err := manager.ListSilences(ctx, filter)

	assert.NoError(t, err)
	assert.Len(t, silences, 2)

	// Verify repository was NOT called (cache hit)
	repo.AssertNotCalled(t, "ListSilences")
}

// TestListSilences_SlowPath tests database-based filtering (slow path).
func TestListSilences_SlowPath(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Filter with complex criteria (should query repository)
	filter := infrasilencing.SilenceFilter{
		Statuses:  []silencing.SilenceStatus{silencing.SilenceStatusActive, silencing.SilenceStatusPending},
		CreatedBy: "ops@example.com",
		Limit:     10,
	}

	expectedSilences := []*silencing.Silence{
		newTestSilence("db-1", silencing.SilenceStatusActive),
		newTestSilence("db-2", silencing.SilenceStatusPending),
	}
	repo.On("ListSilences", ctx, filter).Return(expectedSilences, nil)

	silences, err := manager.ListSilences(ctx, filter)

	assert.NoError(t, err)
	assert.Len(t, silences, 2)
	repo.AssertExpectations(t)
}

// TestListSilences_EmptyResult tests list with no results.
func TestListSilences_EmptyResult(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusExpired},
	}

	repo.On("ListSilences", ctx, filter).Return([]*silencing.Silence{}, nil)

	silences, err := manager.ListSilences(ctx, filter)

	assert.NoError(t, err)
	assert.Empty(t, silences)
	repo.AssertExpectations(t)
}

// TestCRUD_Integration tests full CRUD lifecycle.
func TestCRUD_Integration(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Step 1: Create
	silence := newTestSilence("lifecycle-id", silencing.SilenceStatusActive)
	silence.CreatedAt = time.Now()
	repo.On("CreateSilence", ctx, silence).Return(silence, nil)

	created, err := manager.CreateSilence(ctx, silence)
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-id", created.ID)

	// Verify in cache
	_, found := manager.cache.Get("lifecycle-id")
	assert.True(t, found, "Created silence should be in cache")

	// Step 2: Get (cache hit)
	retrieved, err := manager.GetSilence(ctx, "lifecycle-id")
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-id", retrieved.ID)

	// Step 3: Update
	silence.Comment = "Updated"
	repo.On("UpdateSilence", ctx, silence).Return(nil)

	err = manager.UpdateSilence(ctx, silence)
	require.NoError(t, err)

	// Verify cache updated
	cached, found := manager.cache.Get("lifecycle-id")
	assert.True(t, found)
	assert.Equal(t, "Updated", cached.Comment)

	// Step 4: Delete
	repo.On("DeleteSilence", ctx, "lifecycle-id").Return(nil)

	err = manager.DeleteSilence(ctx, "lifecycle-id")
	require.NoError(t, err)

	// Verify removed from cache
	_, found = manager.cache.Get("lifecycle-id")
	assert.False(t, found, "Deleted silence should be removed from cache")

	repo.AssertExpectations(t)
}

// TestCRUD_RepositoryErrors tests error propagation from repository.
func TestCRUD_RepositoryErrors(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// CreateSilence error
	silence := newTestSilence("error-id", silencing.SilenceStatusActive)
	repoErr := errors.New("database error")
	repo.On("CreateSilence", ctx, silence).Return(nil, repoErr)

	_, err := manager.CreateSilence(ctx, silence)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create silence")

	// UpdateSilence error
	repo.On("UpdateSilence", ctx, silence).Return(repoErr)

	err = manager.UpdateSilence(ctx, silence)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update silence")

	// DeleteSilence error
	repo.On("DeleteSilence", ctx, "error-id").Return(repoErr)

	err = manager.DeleteSilence(ctx, "error-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete silence")

	// ListSilences error
	filter := infrasilencing.SilenceFilter{}
	repo.On("ListSilences", ctx, filter).Return(nil, repoErr)

	_, err = manager.ListSilences(ctx, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list silences")

	repo.AssertExpectations(t)
}

// TestCRUD_PendingSilencesNotCached tests that pending silences are not cached.
func TestCRUD_PendingSilencesNotCached(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Create pending silence
	silence := newTestSilence("pending-id", silencing.SilenceStatusPending)
	silence.CreatedAt = time.Now()
	repo.On("CreateSilence", ctx, silence).Return(silence, nil)

	created, err := manager.CreateSilence(ctx, silence)
	require.NoError(t, err)

	// Verify NOT in cache (only active silences are cached)
	_, found := manager.cache.Get("pending-id")
	assert.False(t, found, "Pending silence should NOT be cached")

	// Get pending silence (cache miss, should query repository)
	repo.On("GetSilenceByID", ctx, "pending-id").Return(created, nil)

	retrieved, err := manager.GetSilence(ctx, "pending-id")
	require.NoError(t, err)
	assert.Equal(t, "pending-id", retrieved.ID)

	// Still not cached after Get
	_, found = manager.cache.Get("pending-id")
	assert.False(t, found, "Pending silence should still NOT be cached after Get")

	repo.AssertExpectations(t)
}
