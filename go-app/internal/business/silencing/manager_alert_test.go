package silencing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// Mock matcher with configurable behavior
type configurableMockMatcher struct {
	mock.Mock
}

func (m *configurableMockMatcher) Matches(ctx context.Context, alert silencing.Alert, silence *silencing.Silence) (bool, error) {
	args := m.Called(ctx, alert, silence)
	return args.Bool(0), args.Error(1)
}

func (m *configurableMockMatcher) MatchesAny(ctx context.Context, alert silencing.Alert, silences []*silencing.Silence) ([]string, error) {
	args := m.Called(ctx, alert, silences)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// Test helper: create alert
func newTestAlert(labels map[string]string) *silencing.Alert {
	return &silencing.Alert{
		Labels:      labels,
		Annotations: map[string]string{},
	}
}

// TestGetActiveSilences_FromCache tests cache hit scenario.
func TestGetActiveSilences_FromCache(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Pre-populate cache
	silence1 := newTestSilence("active-1", silencing.SilenceStatusActive)
	silence2 := newTestSilence("active-2", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)

	silences, err := manager.GetActiveSilences(ctx)

	assert.NoError(t, err)
	assert.Len(t, silences, 2)

	// Verify repository was NOT called (cache hit)
	repo.AssertNotCalled(t, "ListSilences")
}

// TestGetActiveSilences_FromDB tests cache miss with fallback to database.
func TestGetActiveSilences_FromDB(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	// Cache is empty, should query repository
	expectedSilences := []*silencing.Silence{
		newTestSilence("db-1", silencing.SilenceStatusActive),
		newTestSilence("db-2", silencing.SilenceStatusActive),
	}

	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}
	repo.On("ListSilences", ctx, filter).Return(expectedSilences, nil)

	silences, err := manager.GetActiveSilences(ctx)

	assert.NoError(t, err)
	assert.Len(t, silences, 2)
	repo.AssertExpectations(t)
}

// TestGetActiveSilences_EmptyResult tests no active silences.
func TestGetActiveSilences_EmptyResult(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}
	repo.On("ListSilences", ctx, filter).Return([]*silencing.Silence{}, nil)

	silences, err := manager.GetActiveSilences(ctx)

	assert.NoError(t, err)
	assert.Empty(t, silences)
	repo.AssertExpectations(t)
}

// TestGetActiveSilences_RepositoryError tests error handling.
func TestGetActiveSilences_RepositoryError(t *testing.T) {
	manager, repo := newTestManagerWithMock()
	ctx := context.Background()

	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}
	repoErr := errors.New("database error")
	repo.On("ListSilences", ctx, filter).Return(nil, repoErr)

	silences, err := manager.GetActiveSilences(ctx)

	assert.Error(t, err)
	assert.Nil(t, silences)
	assert.Contains(t, err.Error(), "get active silences")
	repo.AssertExpectations(t)
}

// TestIsAlertSilenced_NoMatches tests alert not silenced.
func TestIsAlertSilenced_NoMatches(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Pre-populate cache with silences
	silence1 := newTestSilence("s1", silencing.SilenceStatusActive)
	silence2 := newTestSilence("s2", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)

	// Alert
	alert := newTestAlert(map[string]string{
		"alertname": "DiskFull",
		"instance":  "server-01",
	})

	// Mock matcher to return false for both silences
	matcher.On("Matches", ctx, *alert, silence1).Return(false, nil)
	matcher.On("Matches", ctx, *alert, silence2).Return(false, nil)

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	assert.NoError(t, err)
	assert.False(t, silenced)
	assert.Empty(t, ids)
	matcher.AssertExpectations(t)
}

// TestIsAlertSilenced_SingleMatch tests one silence matches.
func TestIsAlertSilenced_SingleMatch(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Pre-populate cache
	silence1 := newTestSilence("match-id", silencing.SilenceStatusActive)
	silence2 := newTestSilence("nomatch-id", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
	})

	// Mock matcher: first matches, second doesn't
	matcher.On("Matches", ctx, *alert, silence1).Return(true, nil)
	matcher.On("Matches", ctx, *alert, silence2).Return(false, nil)

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	assert.NoError(t, err)
	assert.True(t, silenced)
	assert.Len(t, ids, 1)
	assert.Contains(t, ids, "match-id")
	matcher.AssertExpectations(t)
}

// TestIsAlertSilenced_MultipleMatches tests multiple silences match.
func TestIsAlertSilenced_MultipleMatches(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Pre-populate cache with 3 silences
	silence1 := newTestSilence("match-1", silencing.SilenceStatusActive)
	silence2 := newTestSilence("match-2", silencing.SilenceStatusActive)
	silence3 := newTestSilence("nomatch", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)
	manager.cache.Set(silence3)

	alert := newTestAlert(map[string]string{
		"alertname": "HighMemory",
		"severity":  "critical",
	})

	// Mock matcher: 2 matches, 1 no match
	matcher.On("Matches", ctx, *alert, silence1).Return(true, nil)
	matcher.On("Matches", ctx, *alert, silence2).Return(true, nil)
	matcher.On("Matches", ctx, *alert, silence3).Return(false, nil)

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	assert.NoError(t, err)
	assert.True(t, silenced)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, "match-1")
	assert.Contains(t, ids, "match-2")
	matcher.AssertExpectations(t)
}

// TestIsAlertSilenced_InvalidAlert tests error on nil alert.
func TestIsAlertSilenced_InvalidAlert(t *testing.T) {
	manager, _ := newTestManagerWithMock()
	ctx := context.Background()

	// Test nil alert
	silenced, ids, err := manager.IsAlertSilenced(ctx, nil)

	assert.ErrorIs(t, err, ErrInvalidAlert)
	assert.False(t, silenced)
	assert.Nil(t, ids)

	// Test alert with nil labels
	alertWithNilLabels := &silencing.Alert{Labels: nil}
	silenced, ids, err = manager.IsAlertSilenced(ctx, alertWithNilLabels)

	assert.ErrorIs(t, err, ErrInvalidAlert)
	assert.False(t, silenced)
	assert.Nil(t, ids)
}

// TestIsAlertSilenced_MatcherError tests graceful degradation on matcher error.
func TestIsAlertSilenced_MatcherError(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Pre-populate cache
	silence1 := newTestSilence("error-silence", silencing.SilenceStatusActive)
	silence2 := newTestSilence("good-silence", silencing.SilenceStatusActive)
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)

	alert := newTestAlert(map[string]string{"alertname": "Test"})

	// Mock matcher: first returns error, second matches
	matcherErr := errors.New("regex compilation error")
	matcher.On("Matches", ctx, *alert, silence1).Return(false, matcherErr)
	matcher.On("Matches", ctx, *alert, silence2).Return(true, nil)

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	// Should succeed despite first matcher error (graceful degradation)
	assert.NoError(t, err)
	assert.True(t, silenced)
	assert.Len(t, ids, 1)
	assert.Contains(t, ids, "good-silence")
	matcher.AssertExpectations(t)
}

// TestIsAlertSilenced_ContextCancelled tests early exit on context cancellation.
func TestIsAlertSilenced_ContextCancelled(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Pre-populate cache with 1 silence
	silence := newTestSilence("s1", silencing.SilenceStatusActive)
	manager.cache.Set(silence)

	alert := newTestAlert(map[string]string{"alertname": "Test"})

	// Matcher should be called (context check happens in loop)
	matcher.On("Matches", mock.Anything, *alert, silence).Return(false, nil).Maybe()

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	// Should return context error
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, silenced)
	assert.Nil(t, ids)
}

// TestIsAlertSilenced_EmptyCache tests fallback when cache is empty.
func TestIsAlertSilenced_EmptyCache(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Cache is empty, should query repository
	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}

	silence := newTestSilence("db-silence", silencing.SilenceStatusActive)
	repo.On("ListSilences", ctx, filter).Return([]*silencing.Silence{silence}, nil)

	alert := newTestAlert(map[string]string{"alertname": "Test"})

	// Mock matcher
	matcher.On("Matches", ctx, *alert, silence).Return(true, nil)

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	assert.NoError(t, err)
	assert.True(t, silenced)
	assert.Len(t, ids, 1)
	assert.Contains(t, ids, "db-silence")
	repo.AssertExpectations(t)
	matcher.AssertExpectations(t)
}

// TestIsAlertSilenced_GetActiveSilencesError tests fail-safe behavior.
func TestIsAlertSilenced_GetActiveSilencesError(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Repository returns error
	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000,
	}
	repo.On("ListSilences", ctx, filter).Return(nil, errors.New("database error"))

	alert := newTestAlert(map[string]string{"alertname": "Test"})

	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	// Fail-safe: should return (false, nil, nil) on error (not block alerts)
	assert.NoError(t, err)
	assert.False(t, silenced)
	assert.Nil(t, ids)
	repo.AssertExpectations(t)
}

// TestIsAlertSilenced_Performance100Silences tests performance with 100 silences.
func TestIsAlertSilenced_Performance100Silences(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Pre-populate cache with 100 silences
	for i := 0; i < 100; i++ {
		silence := newTestSilence(string(rune('a'+i%26))+string(rune('0'+i/26)), silencing.SilenceStatusActive)
		manager.cache.Set(silence)
	}

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"instance":  "server-01",
	})

	// Mock matcher to return false for all (worst case)
	matcher.On("Matches", mock.Anything, *alert, mock.Anything).Return(false, nil)

	start := time.Now()
	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.False(t, silenced)
	assert.Empty(t, ids)

	// Performance target: <5ms for 100 silences (realistic with mock overhead)
	assert.Less(t, duration, 5*time.Millisecond, "Should complete in <5ms")

	// Verify matcher was called 100 times
	matcher.AssertNumberOfCalls(t, "Matches", 100)
}

// TestAlertFiltering_Integration tests end-to-end alert filtering.
func TestAlertFiltering_Integration(t *testing.T) {
	repo := new(mockRepository)
	matcher := new(configurableMockMatcher)
	manager := NewDefaultSilenceManager(repo, matcher, nil, nil)
	manager.started.Store(true)

	ctx := context.Background()

	// Scenario: Create 3 silences, 2 match the alert
	silence1 := &silencing.Silence{
		ID:        "silence-1",
		CreatedBy: "ops@example.com",
		Comment:   "Maintenance window",
		StartsAt:  time.Now().Add(-1 * time.Hour),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Status:    silencing.SilenceStatusActive,
		Matchers: []silencing.Matcher{
			{Name: "alertname", Value: "HighCPU", Type: silencing.MatcherTypeEqual},
		},
	}

	silence2 := &silencing.Silence{
		ID:        "silence-2",
		CreatedBy: "ops@example.com",
		Comment:   "Known issue",
		StartsAt:  time.Now().Add(-1 * time.Hour),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Status:    silencing.SilenceStatusActive,
		Matchers: []silencing.Matcher{
			{Name: "job", Value: "api-.*", Type: silencing.MatcherTypeRegex},
		},
	}

	silence3 := &silencing.Silence{
		ID:        "silence-3",
		CreatedBy: "ops@example.com",
		Comment:   "Different alert",
		StartsAt:  time.Now().Add(-1 * time.Hour),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Status:    silencing.SilenceStatusActive,
		Matchers: []silencing.Matcher{
			{Name: "alertname", Value: "DiskFull", Type: silencing.MatcherTypeEqual},
		},
	}

	// Add to cache
	manager.cache.Set(silence1)
	manager.cache.Set(silence2)
	manager.cache.Set(silence3)

	// Test alert
	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"instance":  "server-01",
		"severity":  "critical",
	})

	// Mock matcher: silence1 and silence2 match, silence3 doesn't
	matcher.On("Matches", ctx, *alert, silence1).Return(true, nil)
	matcher.On("Matches", ctx, *alert, silence2).Return(true, nil)
	matcher.On("Matches", ctx, *alert, silence3).Return(false, nil)

	// Test IsAlertSilenced
	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)

	require.NoError(t, err)
	assert.True(t, silenced, "Alert should be silenced")
	assert.Len(t, ids, 2, "Should match 2 silences")
	assert.Contains(t, ids, "silence-1")
	assert.Contains(t, ids, "silence-2")
	assert.NotContains(t, ids, "silence-3")

	matcher.AssertExpectations(t)
}
