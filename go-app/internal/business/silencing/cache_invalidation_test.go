package silencing

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// SPLIT-BRAIN-RISK slice 2: cache invalidation after the GC worker expires
// silences in the database. Without it, expired silences stay "active" in the
// cache and keep suppressing alerts until the next full sync.

func cacheTestSilence(id string, status silencing.SilenceStatus, endsAt time.Time) *silencing.Silence {
	return &silencing.Silence{
		ID:        id,
		CreatedBy: "tester",
		Comment:   "cache invalidation",
		StartsAt:  endsAt.Add(-time.Hour),
		EndsAt:    endsAt,
		Status:    status,
		Matchers:  []silencing.Matcher{{Name: "alertname", Value: "X", Type: silencing.MatcherTypeEqual}},
	}
}

func TestSilenceCache_MarkExpired(t *testing.T) {
	now := time.Now()
	cache := newSilenceCache()

	stale := cacheTestSilence("stale-active", silencing.SilenceStatusActive, now.Add(-time.Minute))
	fresh := cacheTestSilence("fresh-active", silencing.SilenceStatusActive, now.Add(time.Hour))
	pending := cacheTestSilence("pending", silencing.SilenceStatusPending, now.Add(2*time.Hour))
	alreadyExpired := cacheTestSilence("expired", silencing.SilenceStatusExpired, now.Add(-time.Hour))

	cache.Set(stale)
	cache.Set(fresh)
	cache.Set(pending)
	cache.Set(alreadyExpired)

	updated := cache.MarkExpired(now)
	assert.Equal(t, 1, updated, "only the stale active silence should transition")

	active := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.Len(t, active, 1)
	assert.Equal(t, "fresh-active", active[0].ID)

	expired := cache.GetByStatus(silencing.SilenceStatusExpired)
	assert.Len(t, expired, 2)

	// Readers holding the old pointer must not observe a concurrent mutation:
	// the cache replaces the entry with a copy instead of mutating in place.
	assert.Equal(t, silencing.SilenceStatusActive, stale.Status,
		"original object must not be mutated (readers may hold the pointer)")

	got, found := cache.Get("stale-active")
	assert.True(t, found)
	assert.Equal(t, silencing.SilenceStatusExpired, got.Status)

	// Idempotent: a second pass finds nothing to do.
	assert.Equal(t, 0, cache.MarkExpired(now))
}

func TestSilenceCache_DeleteExpiredBefore(t *testing.T) {
	now := time.Now()
	cache := newSilenceCache()

	oldExpired := cacheTestSilence("old-expired", silencing.SilenceStatusExpired, now.Add(-48*time.Hour))
	recentExpired := cacheTestSilence("recent-expired", silencing.SilenceStatusExpired, now.Add(-time.Hour))
	active := cacheTestSilence("active", silencing.SilenceStatusActive, now.Add(time.Hour))

	cache.Set(oldExpired)
	cache.Set(recentExpired)
	cache.Set(active)

	removed := cache.DeleteExpiredBefore(now.Add(-24 * time.Hour))
	assert.Equal(t, 1, removed)

	_, found := cache.Get("old-expired")
	assert.False(t, found, "old expired entry must be removed")
	_, found = cache.Get("recent-expired")
	assert.True(t, found, "expired entry inside retention must stay")
	_, found = cache.Get("active")
	assert.True(t, found, "active entry must stay")

	assert.Equal(t, 2, cache.Stats().Size)
}

// TestGCWorker_RunCleanup_InvalidatesCache reproduces the original hole:
// the database expires a silence but the cache keeps it active. After the fix
// the GC worker invalidates the cache in the same cleanup pass.
func TestGCWorker_RunCleanup_InvalidatesCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockRepo := new(mockGCRepository)
	cache := newSilenceCache()

	now := time.Now()
	stale := cacheTestSilence("stale-active", silencing.SilenceStatusActive, now.Add(-time.Minute))
	oldExpired := cacheTestSilence("old-expired", silencing.SilenceStatusExpired, now.Add(-48*time.Hour))
	cache.Set(stale)
	cache.Set(oldExpired)

	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, false).Return(int64(1), nil)
	mockRepo.On("ExpireSilences", mock.Anything, mock.Anything, true).Return(int64(1), nil)

	worker := newGCWorker(mockRepo, cache, time.Hour, 24*time.Hour, 1000, logger, nil)
	worker.runCleanup(context.Background())

	// Phase 1: the stale active entry no longer suppresses alerts.
	active := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.Empty(t, active, "expired silence must not stay active in cache after GC")

	// Phase 2: the hard-deleted row is gone from the cache too.
	_, found := cache.Get("old-expired")
	assert.False(t, found, "hard-deleted silence must be evicted from cache")

	mockRepo.AssertExpectations(t)
}
