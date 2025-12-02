package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"log/slog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// mockCache implements cache.Cache for testing
type mockCache struct {
	data  map[string][]byte // Store JSON bytes to match Redis behavior
	err   error
	calls int
}

func newMockCache() *mockCache {
	return &mockCache{
		data: make(map[string][]byte),
	}
}

func (m *mockCache) Get(ctx context.Context, key string, dest interface{}) error {
	m.calls++
	if m.err != nil {
		return m.err
	}

	val, ok := m.data[key]
	if !ok {
		return cache.ErrNotFound
	}

	// Deserialize JSON like Redis cache does
	if err := json.Unmarshal(val, dest); err != nil {
		return cache.NewCacheError("failed to unmarshal cache value", "UNMARSHAL_ERROR").WithCause(err)
	}

	return nil
}

func (m *mockCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	m.calls++
	if m.err != nil {
		return m.err
	}

	// Serialize to JSON like Redis cache does
	data, err := json.Marshal(value)
	if err != nil {
		return cache.NewCacheError("failed to marshal cache value", "MARSHAL_ERROR").WithCause(err)
	}

	m.data[key] = data
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	m.calls++
	delete(m.data, key)
	return nil
}

func (m *mockCache) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (m *mockCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return nil
}

func (m *mockCache) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *mockCache) Ping(ctx context.Context) error {
	return nil
}

func (m *mockCache) Flush(ctx context.Context) error {
	m.data = make(map[string][]byte)
	return nil
}

// Redis SET operations (for TN-128 compatibility)
func (m *mockCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return nil // Mock implementation - no-op
}

func (m *mockCache) SMembers(ctx context.Context, key string) ([]string, error) {
	return []string{}, nil // Mock implementation - empty set
}

func (m *mockCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	return nil // Mock implementation - no-op
}

func (m *mockCache) SCard(ctx context.Context, key string) (int64, error) {
	return 0, nil // Mock implementation - empty set
}

// TestEnrichmentMode_IsValid tests IsValid method
func TestEnrichmentMode_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		mode     EnrichmentMode
		expected bool
	}{
		{
			name:     "valid transparent mode",
			mode:     EnrichmentModeTransparent,
			expected: true,
		},
		{
			name:     "valid enriched mode",
			mode:     EnrichmentModeEnriched,
			expected: true,
		},
		{
			name:     "valid transparent_with_recommendations mode",
			mode:     EnrichmentModeTransparentWithRecommendations,
			expected: true,
		},
		{
			name:     "invalid mode",
			mode:     EnrichmentMode("invalid"),
			expected: false,
		},
		{
			name:     "empty mode",
			mode:     EnrichmentMode(""),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.mode.IsValid())
		})
	}
}

// TestEnrichmentMode_String tests String method
func TestEnrichmentMode_String(t *testing.T) {
	tests := []struct {
		name     string
		mode     EnrichmentMode
		expected string
	}{
		{
			name:     "transparent mode",
			mode:     EnrichmentModeTransparent,
			expected: "transparent",
		},
		{
			name:     "enriched mode",
			mode:     EnrichmentModeEnriched,
			expected: "enriched",
		},
		{
			name:     "transparent_with_recommendations mode",
			mode:     EnrichmentModeTransparentWithRecommendations,
			expected: "transparent_with_recommendations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.mode.String())
		})
	}
}

// TestEnrichmentMode_ToMetricValue tests ToMetricValue method
func TestEnrichmentMode_ToMetricValue(t *testing.T) {
	tests := []struct {
		name     string
		mode     EnrichmentMode
		expected float64
	}{
		{
			name:     "transparent mode",
			mode:     EnrichmentModeTransparent,
			expected: 0,
		},
		{
			name:     "enriched mode",
			mode:     EnrichmentModeEnriched,
			expected: 1,
		},
		{
			name:     "transparent_with_recommendations mode",
			mode:     EnrichmentModeTransparentWithRecommendations,
			expected: 2,
		},
		{
			name:     "invalid mode defaults to enriched",
			mode:     EnrichmentMode("invalid"),
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.mode.ToMetricValue())
		})
	}
}

// TestNewEnrichmentModeManager tests constructor
func TestNewEnrichmentModeManager(t *testing.T) {
	t.Run("creates manager with default mode", func(t *testing.T) {
		mockCache := newMockCache()
		logger := slog.Default()

		manager := NewEnrichmentModeManager(mockCache, logger, nil)
		require.NotNil(t, manager)

		ctx := context.Background()
		mode, err := manager.GetMode(ctx)
		require.NoError(t, err)
		assert.Equal(t, EnrichmentModeEnriched, mode)
	})

	t.Run("creates manager with nil logger", func(t *testing.T) {
		mockCache := newMockCache()

		manager := NewEnrichmentModeManager(mockCache, nil, nil)
		require.NotNil(t, manager)
	})
}

// TestEnrichmentModeManager_GetMode tests GetMode method
func TestEnrichmentModeManager_GetMode(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*mockCache)
		expectedMode  EnrichmentMode
		expectedError bool
	}{
		{
			name: "returns current mode from memory",
			setup: func(mc *mockCache) {
				// Mode already set in memory
			},
			expectedMode:  EnrichmentModeEnriched,
			expectedError: false,
		},
		{
			name: "loads mode from Redis on initialization",
			setup: func(mc *mockCache) {
				data := map[string]any{
					"mode":      "transparent",
					"timestamp": time.Now().Unix(),
				}
				jsonData, _ := json.Marshal(data)
				mc.data[redisKeyMode] = jsonData
			},
			expectedMode:  EnrichmentModeTransparent, // Loaded from Redis during NewEnrichmentModeManager
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCache := newMockCache()
			if tt.setup != nil {
				tt.setup(mockCache)
			}

			logger := slog.Default()
			manager := NewEnrichmentModeManager(mockCache, logger, nil)

			ctx := context.Background()
			mode, err := manager.GetMode(ctx)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedMode, mode)
			}
		})
	}
}

// TestEnrichmentModeManager_GetModeWithSource tests GetModeWithSource method
func TestEnrichmentModeManager_GetModeWithSource(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*mockCache)
		envValue       string
		expectedMode   EnrichmentMode
		expectedSource string
	}{
		{
			name: "returns mode from Redis",
			setup: func(mc *mockCache) {
				data := map[string]any{
					"mode":      "transparent",
					"timestamp": time.Now().Unix(),
				}
				jsonData, _ := json.Marshal(data)
				mc.data[redisKeyMode] = jsonData
			},
			expectedMode:   EnrichmentModeTransparent,
			expectedSource: "redis",
		},
		{
			name: "returns mode from ENV when Redis unavailable",
			setup: func(mc *mockCache) {
				// No data in Redis
			},
			envValue:       "enriched",
			expectedMode:   EnrichmentModeEnriched,
			expectedSource: "env",
		},
		{
			name: "returns default mode when both Redis and ENV unavailable",
			setup: func(mc *mockCache) {
				// No data in Redis, no ENV
			},
			expectedMode:   EnrichmentModeEnriched,
			expectedSource: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup ENV
			if tt.envValue != "" {
				os.Setenv("ENRICHMENT_MODE", tt.envValue)
				defer os.Unsetenv("ENRICHMENT_MODE")
			}

			mockCache := newMockCache()
			if tt.setup != nil {
				tt.setup(mockCache)
			}

			logger := slog.Default()
			manager := NewEnrichmentModeManager(mockCache, logger, nil)

			ctx := context.Background()
			mode, source, err := manager.GetModeWithSource(ctx)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedMode, mode)
			assert.Equal(t, tt.expectedSource, source)
		})
	}
}

// TestEnrichmentModeManager_SetMode tests SetMode method
func TestEnrichmentModeManager_SetMode(t *testing.T) {
	tests := []struct {
		name          string
		mode          EnrichmentMode
		setup         func(*mockCache)
		expectedError bool
		errorContains string
	}{
		{
			name:          "sets valid transparent mode",
			mode:          EnrichmentModeTransparent,
			expectedError: false,
		},
		{
			name:          "sets valid enriched mode",
			mode:          EnrichmentModeEnriched,
			expectedError: false,
		},
		{
			name:          "sets valid transparent_with_recommendations mode",
			mode:          EnrichmentModeTransparentWithRecommendations,
			expectedError: false,
		},
		{
			name:          "rejects invalid mode",
			mode:          EnrichmentMode("invalid"),
			expectedError: true,
			errorContains: "invalid enrichment mode",
		},
		{
			name: "handles Redis error gracefully",
			mode: EnrichmentModeTransparent,
			setup: func(mc *mockCache) {
				mc.err = errors.New("redis connection error")
			},
			expectedError: false, // Should fallback to memory
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCache := newMockCache()
			if tt.setup != nil {
				tt.setup(mockCache)
			}

			logger := slog.Default()
			manager := NewEnrichmentModeManager(mockCache, logger, nil)

			ctx := context.Background()
			err := manager.SetMode(ctx, tt.mode)

			if tt.expectedError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)

				// Verify mode was set
				mode, getErr := manager.GetMode(ctx)
				require.NoError(t, getErr)
				assert.Equal(t, tt.mode, mode)
			}

			// Clear error for next test
			if mockCache.err != nil {
				mockCache.err = nil
			}
		})
	}
}

// TestEnrichmentModeManager_ValidateMode tests ValidateMode method
func TestEnrichmentModeManager_ValidateMode(t *testing.T) {
	mockCache := newMockCache()
	logger := slog.Default()
	manager := NewEnrichmentModeManager(mockCache, logger, nil)

	tests := []struct {
		name          string
		mode          EnrichmentMode
		expectedError bool
	}{
		{
			name:          "valid transparent mode",
			mode:          EnrichmentModeTransparent,
			expectedError: false,
		},
		{
			name:          "valid enriched mode",
			mode:          EnrichmentModeEnriched,
			expectedError: false,
		},
		{
			name:          "valid transparent_with_recommendations mode",
			mode:          EnrichmentModeTransparentWithRecommendations,
			expectedError: false,
		},
		{
			name:          "invalid mode",
			mode:          EnrichmentMode("invalid"),
			expectedError: true,
		},
		{
			name:          "empty mode",
			mode:          EnrichmentMode(""),
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.ValidateMode(tt.mode)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestEnrichmentModeManager_GetStats tests GetStats method
func TestEnrichmentModeManager_GetStats(t *testing.T) {
	mockCache := newMockCache()
	logger := slog.Default()
	manager := NewEnrichmentModeManager(mockCache, logger, nil)

	ctx := context.Background()

	// Set mode to trigger stats update
	err := manager.SetMode(ctx, EnrichmentModeTransparent)
	require.NoError(t, err)

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, EnrichmentModeTransparent, stats.CurrentMode)
	assert.NotEmpty(t, stats.Source)
	assert.True(t, stats.RedisAvailable)
	assert.Equal(t, int64(1), stats.TotalSwitches)
	assert.NotNil(t, stats.LastSwitchTime)
	assert.Equal(t, EnrichmentModeEnriched, stats.LastSwitchFrom)
}

// TestEnrichmentModeManager_RefreshCache tests RefreshCache method
func TestEnrichmentModeManager_RefreshCache(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*mockCache)
		envValue       string
		expectedMode   EnrichmentMode
		expectedSource string
	}{
		{
			name: "refreshes from Redis",
			setup: func(mc *mockCache) {
				data := map[string]any{
					"mode":      "transparent",
					"timestamp": time.Now().Unix(),
				}
				jsonData, _ := json.Marshal(data)
				mc.data[redisKeyMode] = jsonData
			},
			expectedMode:   EnrichmentModeTransparent,
			expectedSource: "redis",
		},
		{
			name: "falls back to ENV when Redis empty",
			setup: func(mc *mockCache) {
				// No data in Redis
			},
			envValue:       "transparent_with_recommendations",
			expectedMode:   EnrichmentModeTransparentWithRecommendations,
			expectedSource: "env",
		},
		{
			name: "falls back to default when both unavailable",
			setup: func(mc *mockCache) {
				// No data in Redis
			},
			expectedMode:   EnrichmentModeEnriched,
			expectedSource: "default",
		},
		{
			name: "handles invalid Redis data gracefully",
			setup: func(mc *mockCache) {
				data := map[string]any{
					"mode": "invalid_mode",
				}
				jsonData, _ := json.Marshal(data)
				mc.data[redisKeyMode] = jsonData
			},
			expectedMode:   EnrichmentModeEnriched,
			expectedSource: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup ENV
			if tt.envValue != "" {
				os.Setenv("ENRICHMENT_MODE", tt.envValue)
				defer os.Unsetenv("ENRICHMENT_MODE")
			}

			mockCache := newMockCache()
			if tt.setup != nil {
				tt.setup(mockCache)
			}

			logger := slog.Default()
			manager := NewEnrichmentModeManager(mockCache, logger, nil)

			ctx := context.Background()
			err := manager.RefreshCache(ctx)
			require.NoError(t, err)

			mode, source, err := manager.GetModeWithSource(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMode, mode)
			assert.Equal(t, tt.expectedSource, source)
		})
	}
}

// TestEnrichmentModeManager_ModeSwitchTracking tests mode switch tracking
func TestEnrichmentModeManager_ModeSwitchTracking(t *testing.T) {
	mockCache := newMockCache()
	logger := slog.Default()
	manager := NewEnrichmentModeManager(mockCache, logger, nil)

	ctx := context.Background()

	// Initial stats
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.TotalSwitches)

	// First switch
	err = manager.SetMode(ctx, EnrichmentModeTransparent)
	require.NoError(t, err)

	stats, err = manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalSwitches)
	assert.Equal(t, EnrichmentModeEnriched, stats.LastSwitchFrom)
	assert.NotNil(t, stats.LastSwitchTime)

	// Second switch
	err = manager.SetMode(ctx, EnrichmentModeTransparentWithRecommendations)
	require.NoError(t, err)

	stats, err = manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalSwitches)
	assert.Equal(t, EnrichmentModeTransparent, stats.LastSwitchFrom)

	// Set same mode (no switch)
	err = manager.SetMode(ctx, EnrichmentModeTransparentWithRecommendations)
	require.NoError(t, err)

	stats, err = manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalSwitches) // Should not increment
}

// TestEnrichmentModeManager_ConcurrentAccess tests concurrent access
func TestEnrichmentModeManager_ConcurrentAccess(t *testing.T) {
	mockCache := newMockCache()
	logger := slog.Default()
	manager := NewEnrichmentModeManager(mockCache, logger, nil)

	ctx := context.Background()

	// Launch multiple goroutines
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			mode := EnrichmentModeTransparent
			if id%2 == 0 {
				mode = EnrichmentModeEnriched
			}

			// Set mode
			err := manager.SetMode(ctx, mode)
			assert.NoError(t, err)

			// Get mode
			_, err = manager.GetMode(ctx)
			assert.NoError(t, err)

			// Get stats
			_, err = manager.GetStats(ctx)
			assert.NoError(t, err)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify final state is consistent
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.True(t, stats.TotalSwitches > 0)
}
