// Package grouping provides storage abstraction for alert groups.
//
// GroupStorage interface enables distributed persistence of alert groups
// using Redis as the primary backend with automatic fallback to in-memory
// storage for graceful degradation.
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
package grouping

import (
	"context"
	"time"
)

// GroupStorage abstracts storage for alert groups.
//
// Implementations:
//   - RedisGroupStorage: Distributed storage using Redis (primary)
//   - MemoryGroupStorage: In-memory storage (fallback)
//
// Thread-safety: All methods are safe for concurrent use.
// All implementations must support context cancellation.
//
// Performance Targets (150% Quality):
//   - Store(): <5ms (baseline), <2ms (150% with pipelining)
//   - Load(): <5ms (baseline), <1ms (150%)
//   - Delete(): <5ms (baseline), <2ms (150% with pipelining)
//   - LoadAll(): <200ms for 1K groups (baseline), <100ms (150% with parallel loading)
//   - Size(): <5ms (baseline), <1ms (150%)
//
// Example Usage:
//
//	// Initialize Redis storage
//	storage, err := grouping.NewRedisGroupStorage(grouping.RedisGroupStorageConfig{
//	    RedisCache: redisCache,
//	    Logger:     logger,
//	    TTL:        24 * time.Hour,
//	})
//	if err != nil {
//	    // Fallback to memory storage
//	    storage = grouping.NewMemoryGroupStorage(logger)
//	}
//
//	// Store a group
//	err = storage.Store(ctx, group)
//
//	// Load a group
//	group, err := storage.Load(ctx, groupKey)
//
//	// Restore all groups on startup (HA recovery)
//	groups, err := storage.LoadAll(ctx)
type GroupStorage interface {
	// === Core Operations ===

	// Store saves a group to storage.
	//
	// The group is serialized (typically JSON) and persisted with TTL management.
	// For Redis: Uses pipelining for atomic operation (SET + ZADD to index).
	// For Memory: Deep copy to prevent external mutation.
	//
	// Parameters:
	//   - ctx: context for timeout and cancellation
	//   - group: group to save (must have Key and Metadata.Version)
	//
	// Returns:
	//   - error: ErrVersionMismatch (optimistic locking conflict),
	//            StorageError (Redis/serialization error)
	//
	// Thread-safe: Yes
	// Performance: <5ms (baseline), <2ms (150% target with pipelining)
	//
	// Example:
	//
	//	group := &AlertGroup{
	//	    Key: "alertname=HighCPU",
	//	    Alerts: map[string]*core.Alert{...},
	//	    Metadata: &GroupMetadata{...},
	//	}
	//	if err := storage.Store(ctx, group); err != nil {
	//	    log.Error("Failed to store group", "error", err)
	//	}
	Store(ctx context.Context, group *AlertGroup) error

	// Load retrieves a group by its key.
	//
	// For Redis: GET group:{key} and deserialize JSON.
	// For Memory: Return deep copy to prevent external mutation.
	//
	// Parameters:
	//   - ctx: context for timeout
	//   - groupKey: unique identifier for the group (from GroupKeyGenerator)
	//
	// Returns:
	//   - *AlertGroup: the group with all alerts and metadata
	//   - error: ErrNotFound (group doesn't exist),
	//            StorageError (Redis/deserialization error)
	//
	// Thread-safe: Yes
	// Performance: <5ms (baseline), <1ms (150% target)
	//
	// Example:
	//
	//	group, err := storage.Load(ctx, "alertname=HighCPU")
	//	if errors.Is(err, grouping.ErrNotFound) {
	//	    log.Info("Group not found", "key", "alertname=HighCPU")
	//	}
	Load(ctx context.Context, groupKey GroupKey) (*AlertGroup, error)

	// Delete removes a group from storage.
	//
	// For Redis: Uses pipelining (DEL + ZREM from index).
	// For Memory: Delete from map.
	//
	// Parameters:
	//   - ctx: context for timeout
	//   - groupKey: unique identifier for the group
	//
	// Returns:
	//   - error: ErrNotFound (group doesn't exist, non-fatal),
	//            StorageError (Redis error)
	//
	// Thread-safe: Yes
	// Performance: <5ms (baseline), <2ms (150% target)
	//
	// Note: ErrNotFound is typically logged but not treated as fatal error.
	//
	// Example:
	//
	//	if err := storage.Delete(ctx, "alertname=HighCPU"); err != nil {
	//	    if !errors.Is(err, grouping.ErrNotFound) {
	//	        log.Error("Failed to delete group", "error", err)
	//	    }
	//	}
	Delete(ctx context.Context, groupKey GroupKey) error

	// === Query Operations ===

	// ListKeys returns all group keys in storage.
	//
	// Used for:
	//   - Monitoring: count of active groups
	//   - LoadAll: bulk loading groups
	//   - CleanupExpiredGroups: finding candidates for cleanup
	//
	// For Redis: ZRANGE group:index 0 -1 (all members from sorted set).
	// For Memory: Iterate map keys.
	//
	// Returns:
	//   - []GroupKey: list of keys (may be empty, never nil)
	//   - error: StorageError (Redis error)
	//
	// Thread-safe: Yes
	// Performance: <10ms for 1000 groups
	//
	// Example:
	//
	//	keys, err := storage.ListKeys(ctx)
	//	log.Info("Active groups", "count", len(keys))
	ListKeys(ctx context.Context) ([]GroupKey, error)

	// Size returns the number of groups in storage.
	//
	// For Redis: ZCARD group:index (count members in sorted set).
	// For Memory: len(groups).
	//
	// Returns:
	//   - int: count of groups (0 if empty)
	//   - error: StorageError (Redis error)
	//
	// Thread-safe: Yes
	// Performance: <5ms (baseline), <1ms (150% with Redis COUNT)
	//
	// Example:
	//
	//	size, err := storage.Size(ctx)
	//	log.Info("Storage size", "groups", size)
	Size(ctx context.Context) (int, error)

	// === Batch Operations (150% Enhancement) ===

	// LoadAll loads all groups from storage.
	//
	// Used for:
	//   - Startup recovery: restore in-memory state after restart (HA)
	//   - Debugging: dump all groups for inspection
	//   - Migration: bulk export/import
	//
	// Implementation notes:
	//   - For Redis: Use parallel loading (goroutines) for performance
	//   - For Memory: Iterate map, return deep copies
	//   - Should handle partial failures gracefully (log, don't fail)
	//
	// Returns:
	//   - []*AlertGroup: list of groups (may be empty, never nil)
	//   - error: StorageError (critical failures only, partial failures logged)
	//
	// Thread-safe: Yes
	// Performance: <200ms for 1000 groups (baseline), <100ms (150% target with parallelism)
	//
	// Example:
	//
	//	groups, err := storage.LoadAll(ctx)
	//	if err != nil {
	//	    log.Error("Failed to load all groups", "error", err)
	//	    return err
	//	}
	//	log.Info("Restored groups", "count", len(groups))
	//	for _, group := range groups {
	//	    manager.restoreGroup(group) // rebuild in-memory state
	//	}
	LoadAll(ctx context.Context) ([]*AlertGroup, error)

	// StoreAll saves multiple groups atomically (optional batch optimization).
	//
	// 150% Enhancement: Batch optimization for efficiency.
	//
	// For Redis: Use pipelining to batch multiple Store operations.
	// For Memory: Iterate slice, store each group.
	//
	// Parameters:
	//   - ctx: context for timeout
	//   - groups: slice of groups to save
	//
	// Returns:
	//   - error: StorageError (partial failures possible, logged)
	//
	// Thread-safe: Yes
	// Performance: <10ms for 100 groups with pipelining
	//
	// Example:
	//
	//	groups := []*AlertGroup{group1, group2, group3}
	//	if err := storage.StoreAll(ctx, groups); err != nil {
	//	    log.Warn("Failed to store all groups", "error", err)
	//	}
	StoreAll(ctx context.Context, groups []*AlertGroup) error

	// === Health Check ===

	// Ping checks if storage is healthy.
	//
	// Used for:
	//   - Health endpoint: /health includes storage status
	//   - Fallback detection: switch to MemoryGroupStorage on failure
	//   - Monitoring: alert on storage unavailability
	//
	// For Redis: PING command.
	// For Memory: Always returns nil (memory always "healthy").
	//
	// Returns:
	//   - error: nil if healthy, error otherwise
	//
	// Thread-safe: Yes
	// Performance: <5ms (Redis PING)
	//
	// Example:
	//
	//	if err := storage.Ping(ctx); err != nil {
	//	    log.Error("Storage unhealthy", "error", err)
	//	    // Trigger fallback to MemoryGroupStorage
	//	}
	Ping(ctx context.Context) error
}

// Redis key prefixes for namespace isolation.
const (
	// groupKeyPrefix is the prefix for group data keys.
	// Format: "group:{groupKey}" → stores JSON-serialized AlertGroup
	// Example: "group:alertname=HighCPU"
	groupKeyPrefix = "group:"

	// groupIndexKey is the sorted set index for fast ListKeys() and cleanup.
	// Score: updated_at Unix timestamp
	// Member: groupKey
	// Used for: ZRANGE (list all), ZRANGEBYSCORE (find expired)
	groupIndexKey = "group:index"

	// groupCountKey is the cached size counter (optional optimization).
	// TTL: 60s
	// Used for: Fast Size() queries without ZCARD
	groupCountKey = "group:count"

	// groupLockPrefix is the prefix for distributed locks.
	// Format: "lock:group:{groupKey}" → stores lock ID (UUID)
	// TTL: 30s
	// Used for: Optimistic locking during concurrent updates
	groupLockPrefix = "lock:group:"
)

// TTL settings for groups and locks.
const (
	// groupTTLDefault is the default TTL for groups.
	// Configurable via RedisGroupStorageConfig.TTL.
	// Prevents memory leaks for abandoned groups.
	groupTTLDefault = 24 * time.Hour

	// groupTTLGracePeriod is the extra TTL beyond group expiration.
	// Prevents race conditions during cleanup.
	// Total TTL = calculated TTL + grace period
	groupTTLGracePeriod = 60 * time.Second

	// groupCountCacheTTL is the TTL for cached Size() result.
	// Optional optimization to avoid ZCARD on every call.
	groupCountCacheTTL = 60 * time.Second

	// Note: lockTTL constant is already defined in redis_timer_storage.go (30s)
	// and is shared across timer and group storage for consistency.
)
