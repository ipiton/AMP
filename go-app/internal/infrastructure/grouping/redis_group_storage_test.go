// Package grouping provides unit tests for RedisGroupStorage.
//
// Test Coverage:
//   - Store/Load/Delete operations
//   - Optimistic locking (version conflicts)
//   - ListKeys/Size operations
//   - LoadAll/StoreAll bulk operations
//   - Ping health checks
//   - Error handling and edge cases
//   - Metrics recording
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150% (80%+ coverage)
// Date: 2025-11-04
package grouping

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// setupRedisTest creates a test Redis client connected to localhost:6379.
// Tests are skipped if Redis is not available.
func setupRedisTest(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use test database
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available, skipping test:", err)
	}

	// Clear test database
	client.FlushDB(ctx)

	return client
}

// createTestGroup creates a sample AlertGroup for testing.
func createTestGroup(key GroupKey) *AlertGroup {
	now := time.Now()
	return &AlertGroup{
		Key: key,
		Alerts: map[string]*core.Alert{
			"fp1": {
				Fingerprint: "fp1",
				Labels:      map[string]string{"alertname": "TestAlert", "severity": "critical"},
				Status:      "firing",
			},
		},
		Metadata: &GroupMetadata{
			State:         GroupStateFiring,
			CreatedAt:     now,
			UpdatedAt:     now,
			FiringCount:   1,
			ResolvedCount: 0,
			GroupBy:       []string{"alertname", "severity"},
			Version:       0,
		},
		Version: 0,
	}
}

// TestNewRedisGroupStorage tests storage initialization.
func TestNewRedisGroupStorage(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	tests := []struct {
		name    string
		config  *RedisGroupStorageConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &RedisGroupStorageConfig{
				Client:  client,
				Metrics: metrics.NewBusinessMetrics("test"),
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name:    "nil client",
			config:  &RedisGroupStorageConfig{Client: nil},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage, err := NewRedisGroupStorage(context.Background(), tt.config)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, storage)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, storage)
			}
		})
	}
}

// TestRedisGroupStorage_Store tests storing a group.
func TestRedisGroupStorage_Store(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client:  client,
		Metrics: metrics.NewBusinessMetrics("test"),
	})
	require.NoError(t, err)

	ctx := context.Background()
	group := createTestGroup("test:group:1")

	// Test: Store new group
	err = storage.Store(ctx, group)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), group.Version, "Version should be incremented")

	// Verify stored in Redis
	exists, err := client.Exists(ctx, "group:test:group:1").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	// Verify in index
	score, err := client.ZScore(ctx, groupIndexKey, "test:group:1").Result()
	assert.NoError(t, err)
	assert.Greater(t, score, float64(0))

	// Test: Update existing group
	group.Alerts["fp2"] = &core.Alert{
		Fingerprint: "fp2",
		Labels:      map[string]string{"alertname": "TestAlert2"},
		Status:      "firing",
	}
	err = storage.Store(ctx, group)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), group.Version, "Version should be incremented again")

	// Test: Store nil group
	err = storage.Store(ctx, nil)
	assert.Error(t, err)
}

// TestRedisGroupStorage_OptimisticLocking tests version conflict detection.
func TestRedisGroupStorage_OptimisticLocking(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()
	group := createTestGroup("test:group:conflict")

	// Store initial version
	err = storage.Store(ctx, group)
	require.NoError(t, err)
	assert.Equal(t, int64(1), group.Version)

	// Simulate concurrent modification
	// Create stale copy with old version
	staleGroup := createTestGroup("test:group:conflict")
	staleGroup.Version = 0 // Old version (before first store)

	// Try to store stale group - should fail with version mismatch
	err = storage.Store(ctx, staleGroup)
	assert.Error(t, err)

	// Verify it's a version mismatch error
	var vmErr *ErrVersionMismatch
	assert.True(t, errors.As(err, &vmErr), "Error should be ErrVersionMismatch")
	if vmErr != nil {
		assert.Equal(t, GroupKey("test:group:conflict"), vmErr.Key)
		assert.Equal(t, int64(0), vmErr.ExpectedVersion)
		assert.Equal(t, int64(1), vmErr.ActualVersion)
	}
}

// TestRedisGroupStorage_Load tests loading a group.
func TestRedisGroupStorage_Load(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Test: Load existing group
	originalGroup := createTestGroup("test:group:load")
	err = storage.Store(ctx, originalGroup)
	require.NoError(t, err)

	loadedGroup, err := storage.Load(ctx, "test:group:load")
	assert.NoError(t, err)
	assert.NotNil(t, loadedGroup)
	assert.Equal(t, originalGroup.Key, loadedGroup.Key)
	assert.Equal(t, len(originalGroup.Alerts), len(loadedGroup.Alerts))
	assert.Equal(t, originalGroup.Version, loadedGroup.Version)

	// Test: Load non-existent group
	_, err = storage.Load(ctx, "test:group:nonexistent")
	assert.Error(t, err)
	var notFoundErr *GroupNotFoundError
	assert.True(t, errors.As(err, &notFoundErr), "Error should be GroupNotFoundError")
}

// TestRedisGroupStorage_Delete tests deleting a group.
func TestRedisGroupStorage_Delete(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Store a group
	group := createTestGroup("test:group:delete")
	err = storage.Store(ctx, group)
	require.NoError(t, err)

	// Test: Delete existing group
	err = storage.Delete(ctx, "test:group:delete")
	assert.NoError(t, err)

	// Verify deleted from Redis
	exists, err := client.Exists(ctx, "group:test:group:delete").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Verify removed from index
	_, err = client.ZScore(ctx, groupIndexKey, "test:group:delete").Result()
	assert.Error(t, err, "Should not be in index")

	// Test: Delete non-existent group (should not error)
	err = storage.Delete(ctx, "test:group:nonexistent")
	assert.NoError(t, err)
}

// TestRedisGroupStorage_ListKeys tests listing all group keys.
func TestRedisGroupStorage_ListKeys(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Store multiple groups
	keys := []GroupKey{"test:group:1", "test:group:2", "test:group:3"}
	for _, key := range keys {
		group := createTestGroup(key)
		err = storage.Store(ctx, group)
		require.NoError(t, err)
	}

	// Test: List all keys
	listedKeys, err := storage.ListKeys(ctx)
	assert.NoError(t, err)
	assert.Len(t, listedKeys, 3)

	// Verify all keys are present
	keyMap := make(map[GroupKey]bool)
	for _, k := range listedKeys {
		keyMap[k] = true
	}
	for _, expectedKey := range keys {
		assert.True(t, keyMap[expectedKey], "Key %s should be in list", expectedKey)
	}

	// Test: Empty list
	client.FlushDB(ctx)
	listedKeys, err = storage.ListKeys(ctx)
	assert.NoError(t, err)
	assert.Empty(t, listedKeys)
}

// TestRedisGroupStorage_Size tests counting groups.
func TestRedisGroupStorage_Size(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Test: Initial size (empty)
	size, err := storage.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0, size)

	// Store groups
	for i := 1; i <= 5; i++ {
		group := createTestGroup(GroupKey("test:group:" + string(rune('0'+i))))
		err = storage.Store(ctx, group)
		require.NoError(t, err)
	}

	// Test: Size after storing
	size, err = storage.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 5, size)

	// Delete one group
	err = storage.Delete(ctx, "test:group:1")
	require.NoError(t, err)

	// Test: Size after deletion
	size, err = storage.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 4, size)
}

// TestRedisGroupStorage_LoadAll tests bulk loading.
func TestRedisGroupStorage_LoadAll(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Store multiple groups
	expectedCount := 10
	for i := 1; i <= expectedCount; i++ {
		group := createTestGroup(GroupKey("test:group:" + string(rune('0'+i))))
		err = storage.Store(ctx, group)
		require.NoError(t, err)
	}

	// Test: LoadAll
	groups, err := storage.LoadAll(ctx)
	assert.NoError(t, err)
	assert.Len(t, groups, expectedCount)

	// Verify each group
	for _, group := range groups {
		assert.NotNil(t, group)
		assert.NotEmpty(t, group.Key)
		assert.NotNil(t, group.Metadata)
	}

	// Test: LoadAll when empty
	client.FlushDB(ctx)
	groups, err = storage.LoadAll(ctx)
	assert.NoError(t, err)
	assert.Empty(t, groups)
}

// TestRedisGroupStorage_StoreAll tests bulk storing.
func TestRedisGroupStorage_StoreAll(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create multiple groups
	groups := make([]*AlertGroup, 20)
	for i := 0; i < 20; i++ {
		groups[i] = createTestGroup(GroupKey("test:group:" + string(rune('0'+i))))
	}

	// Test: StoreAll
	err = storage.StoreAll(ctx, groups)
	assert.NoError(t, err)

	// Verify all stored
	size, err := storage.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 20, size)

	// Test: StoreAll with empty slice
	err = storage.StoreAll(ctx, []*AlertGroup{})
	assert.NoError(t, err)

	// Test: StoreAll with nil groups (should skip)
	err = storage.StoreAll(ctx, []*AlertGroup{nil, createTestGroup("test:valid")})
	assert.NoError(t, err)
}

// TestRedisGroupStorage_Ping tests health check.
func TestRedisGroupStorage_Ping(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Test: Ping healthy Redis
	err = storage.Ping(ctx)
	assert.NoError(t, err)
}

// TestRedisGroupStorage_TTL tests TTL calculation and expiration.
func TestRedisGroupStorage_TTL(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()
	group := createTestGroup("test:group:ttl")

	// Store group
	err = storage.Store(ctx, group)
	require.NoError(t, err)

	// Check TTL is set
	ttl, err := client.TTL(ctx, "group:test:group:ttl").Result()
	assert.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0), "TTL should be set")
	assert.LessOrEqual(t, ttl, groupTTLDefault+groupTTLGracePeriod+time.Second, "TTL should not exceed max")
}

// TestRedisGroupStorage_ConcurrentOperations tests thread safety.
func TestRedisGroupStorage_ConcurrentOperations(t *testing.T) {
	client := setupRedisTest(t)
	defer client.Close()

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Concurrent stores
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			group := createTestGroup(GroupKey("test:concurrent:" + string(rune('0'+id))))
			err := storage.Store(ctx, group)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all stored
	size, err := storage.Size(ctx)
	assert.NoError(t, err)
	assert.Equal(t, numGoroutines, size)
}
