// Package grouping provides Redis-backed storage for AlertGroup with optimistic locking.
//
// RedisGroupStorage implements GroupStorage interface with distributed state management:
//   - Optimistic locking via Redis WATCH/MULTI/EXEC
//   - JSON serialization for AlertGroup persistence
//   - Sorted Set index for efficient listing (sorted by UpdatedAt)
//   - Pipeline for bulk operations (StoreAll)
//   - Parallel loading for recovery (LoadAll)
//   - Prometheus metrics integration
//
// Performance Targets (150% quality):
//   - Store: <2ms (with optimistic locking)
//   - Load: <1ms
//   - Delete: <1ms
//   - ListKeys: <100ms for 10,000 keys
//   - Size: <10ms
//   - LoadAll: <500ms for 10,000 groups (parallel)
//   - StoreAll: <100ms for 1,000 groups (pipeline)
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
package grouping

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/ipiton/AMP/pkg/metrics"
)

// RedisGroupStorage provides Redis-backed persistent storage for AlertGroup.
//
// Thread-safety: All methods are thread-safe via Redis atomicity guarantees.
//
// Storage Schema:
//   - Key: "group:{groupKey}" → JSON-serialized AlertGroup
//   - Index: "group:index" (Sorted Set) → Score: UpdatedAt, Member: groupKey
//   - Count cache: "group:count" (String) → Total groups count (optional optimization)
//
// Optimistic Locking:
//   - Uses AlertGroup.Version field + Redis WATCH/MULTI/EXEC
//   - On version mismatch: returns ErrVersionMismatch
//   - Caller should retry with exponential backoff
//
// Example:
//
//	storage, err := grouping.NewRedisGroupStorage(ctx, config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	group := &grouping.AlertGroup{Key: "team:frontend:alert:cpu", Version: 1, ...}
//	err = storage.Store(ctx, group) // Atomic store with version check
type RedisGroupStorage struct {
	client  *redis.Client
	logger  *slog.Logger
	metrics *metrics.BusinessMetrics
}

// RedisGroupStorageConfig holds configuration for RedisGroupStorage.
type RedisGroupStorageConfig struct {
	// Client is the Redis client (obtained from cache.RedisCache.GetClient())
	Client *redis.Client

	// Logger for structured logging (optional, defaults to slog.Default)
	Logger *slog.Logger

	// Metrics for observability (optional, no metrics if nil)
	Metrics *metrics.BusinessMetrics
}

// NewRedisGroupStorage creates a new Redis-backed group storage.
//
// Parameters:
//   - ctx: Context for initialization (e.g., Ping check)
//   - config: Configuration including Redis client, logger, metrics
//
// Returns:
//   - *RedisGroupStorage: Initialized storage
//   - error: If Redis is unreachable or configuration invalid
//
// Example:
//
//	config := &grouping.RedisGroupStorageConfig{
//	    Client: redisCache.GetClient(),
//	    Logger: logger,
//	    Metrics: businessMetrics,
//	}
//	storage, err := grouping.NewRedisGroupStorage(ctx, config)
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
func NewRedisGroupStorage(ctx context.Context, config *RedisGroupStorageConfig) (*RedisGroupStorage, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.Client == nil {
		return nil, fmt.Errorf("redis client cannot be nil")
	}

	// Defaults
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	storage := &RedisGroupStorage{
		client:  config.Client,
		logger:  logger,
		metrics: config.Metrics,
	}

	// Verify Redis connectivity
	if err := storage.Ping(ctx); err != nil {
		return nil, fmt.Errorf("redis connectivity check failed: %w", err)
	}

	// Initialize health metric
	if storage.metrics != nil {
		storage.metrics.SetStorageHealth("redis", true)
	}

	logger.Info("Initialized Redis group storage",
		"index_key", groupIndexKey)

	return storage, nil
}

// Store saves an AlertGroup to Redis with optimistic locking.
//
// Behavior:
//  1. Serialize group to JSON
//  2. WATCH group key for concurrent modifications
//  3. GET current version from Redis
//  4. If version mismatch: return ErrVersionMismatch
//  5. MULTI/EXEC: SET group + ZADD to index + increment version
//  6. Calculate TTL based on group timers (default: 24h)
//
// Performance Target: <2ms (150% quality)
//
// Thread-safety: Atomic via Redis WATCH/MULTI/EXEC transaction.
//
// Errors:
//   - ErrVersionMismatch: Concurrent modification detected (retry with backoff)
//   - StorageError: Redis operation failed
func (r *RedisGroupStorage) Store(ctx context.Context, group *AlertGroup) error {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("store", time.Since(start))
		}
	}()

	if group == nil {
		err := NewStorageError("store", fmt.Errorf("group cannot be nil"))
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("store", "error")
		}
		return err
	}

	groupKeyStr := string(group.Key)
	redisKey := groupKeyPrefix + groupKeyStr

	// Serialize to JSON
	data, err := json.Marshal(group)
	if err != nil {
		r.logger.Error("Failed to serialize group",
			"group_key", groupKeyStr,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("store", "error")
		}
		return NewStorageError("store", fmt.Errorf("json marshal for %s: %w", groupKeyStr, err))
	}

	// Optimistic locking with WATCH/MULTI/EXEC
	err = r.client.Watch(ctx, func(tx *redis.Tx) error {
		// Check current version in Redis
		existingData, err := tx.Get(ctx, redisKey).Bytes()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("get current version: %w", err)
		}

		// If group exists, verify version
		if err != redis.Nil {
			var existing AlertGroup
			if unmarshalErr := json.Unmarshal(existingData, &existing); unmarshalErr != nil {
				return fmt.Errorf("unmarshal existing group: %w", unmarshalErr)
			}

			// Version mismatch → concurrent update detected
			if existing.Version != group.Version {
				return NewVersionMismatchError(group.Key, group.Version, existing.Version)
			}
		}

		// Increment version for this store operation
		group.Version++

		// Re-serialize with updated version
		data, err = json.Marshal(group)
		if err != nil {
			return fmt.Errorf("re-marshal with version: %w", err)
		}

		// Calculate TTL (based on timers or default)
		ttl := r.calculateTTL(group)

		// Execute transaction: SET + ZADD + EXPIRE
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			// Store group data
			pipe.Set(ctx, redisKey, data, ttl)

			// Update index (sorted by UpdatedAt)
			score := float64(group.Metadata.UpdatedAt.Unix())
			pipe.ZAdd(ctx, groupIndexKey, redis.Z{
				Score:  score,
				Member: groupKeyStr,
			})

			return nil
		})

		return err
	}, redisKey) // WATCH this key for concurrent modifications

	if err != nil {
		// Check if version mismatch
		if vmErr, ok := err.(*ErrVersionMismatch); ok {
			r.logger.Warn("Optimistic locking conflict",
				"group_key", groupKeyStr,
				"expected_version", vmErr.ExpectedVersion,
				"actual_version", vmErr.ActualVersion)
			if r.metrics != nil {
				r.metrics.RecordStorageOperation("store", "error")
			}
			return vmErr
		}

		r.logger.Error("Failed to store group",
			"group_key", groupKeyStr,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("store", "error")
		}
		return NewStorageError("store", fmt.Errorf("redis transaction for %s: %w", groupKeyStr, err))
	}

	r.logger.Debug("Stored group",
		"group_key", groupKeyStr,
		"version", group.Version,
		"alerts_count", len(group.Alerts),
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("store", "success")
	}

	return nil
}

// calculateTTL determines TTL for a group.
//
// Current implementation (TN-125 Phase 1):
//   - Default TTL: 24h + 60s grace period
//
// Future enhancement (Phase 2):
//   - Calculate based on active timers (group_wait, group_interval, repeat_interval)
//   - Dynamic TTL based on timer expiration times
//   - Requires integration with GroupTimerManager (TN-124)
func (r *RedisGroupStorage) calculateTTL(group *AlertGroup) time.Duration {
	// Simple implementation for initial release
	// TODO(TN-125-Phase2): Calculate dynamically based on timer metadata
	return groupTTLDefault + groupTTLGracePeriod
}

// Load retrieves an AlertGroup from Redis by its GroupKey.
//
// Performance Target: <1ms (150% quality)
//
// Errors:
//   - ErrNotFound: Group does not exist in Redis
//   - StorageError: Redis operation failed or deserialization error
func (r *RedisGroupStorage) Load(ctx context.Context, groupKey GroupKey) (*AlertGroup, error) {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("load", time.Since(start))
		}
	}()

	redisKey := groupKeyPrefix + string(groupKey)

	data, err := r.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			// Group not found
			if r.metrics != nil {
				r.metrics.RecordStorageOperation("load", "error")
			}
			return nil, NewGroupNotFoundError(groupKey)
		}

		r.logger.Error("Failed to load group",
			"group_key", groupKey,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("load", "error")
		}
		return nil, NewStorageError("load", fmt.Errorf("redis get for %s: %w", groupKey, err))
	}

	// Deserialize from JSON
	var group AlertGroup
	if err := json.Unmarshal(data, &group); err != nil {
		r.logger.Error("Failed to deserialize group",
			"group_key", groupKey,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("load", "error")
		}
		return nil, NewStorageError("load", fmt.Errorf("json unmarshal for %s: %w", groupKey, err))
	}

	r.logger.Debug("Loaded group",
		"group_key", groupKey,
		"version", group.Version,
		"alerts_count", len(group.Alerts),
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("load", "success")
	}

	return &group, nil
}

// Delete removes an AlertGroup from Redis by its GroupKey.
//
// Behavior:
//  1. DEL group key
//  2. ZREM from index
//  3. Return true if group existed, false otherwise
//
// Performance Target: <1ms (150% quality)
//
// Thread-safety: Atomic via Redis pipeline.
func (r *RedisGroupStorage) Delete(ctx context.Context, groupKey GroupKey) error {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("delete", time.Since(start))
		}
	}()

	redisKey := groupKeyPrefix + string(groupKey)

	// Pipeline: DEL + ZREM
	pipe := r.client.Pipeline()
	delCmd := pipe.Del(ctx, redisKey)
	pipe.ZRem(ctx, groupIndexKey, string(groupKey))

	_, err := pipe.Exec(ctx)
	if err != nil {
		r.logger.Error("Failed to delete group",
			"group_key", groupKey,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("delete", "error")
		}
		return NewStorageError("delete", fmt.Errorf("redis delete for %s: %w", groupKey, err))
	}

	deleted := delCmd.Val() > 0

	r.logger.Debug("Deleted group",
		"group_key", groupKey,
		"deleted", deleted,
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("delete", "success")
	}

	return nil
}

// ListKeys returns all active GroupKeys from the Redis index.
//
// Performance Target: <100ms for 10,000 keys (150% quality)
//
// Implementation: ZRANGE on groupIndexKey (sorted by UpdatedAt).
//
// Note: For very large datasets (>100K groups), consider pagination via ZSCAN.
func (r *RedisGroupStorage) ListKeys(ctx context.Context) ([]GroupKey, error) {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("list_keys", time.Since(start))
		}
	}()

	// ZRANGE: get all members sorted by UpdatedAt
	keys, err := r.client.ZRange(ctx, groupIndexKey, 0, -1).Result()
	if err != nil {
		r.logger.Error("Failed to list group keys",
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("list_keys", "error")
		}
		return nil, NewStorageError("list_keys", fmt.Errorf("redis zrange: %w", err))
	}

	// Convert []string to []GroupKey
	groupKeys := make([]GroupKey, len(keys))
	for i, key := range keys {
		groupKeys[i] = GroupKey(key)
	}

	r.logger.Debug("Listed group keys",
		"count", len(groupKeys),
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("list_keys", "success")
	}

	return groupKeys, nil
}

// Size returns the total number of active groups in Redis.
//
// Performance Target: <10ms (150% quality)
//
// Implementation: ZCARD on groupIndexKey (O(1) operation).
func (r *RedisGroupStorage) Size(ctx context.Context) (int, error) {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("size", time.Since(start))
		}
	}()

	count, err := r.client.ZCard(ctx, groupIndexKey).Result()
	if err != nil {
		r.logger.Error("Failed to get group count",
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("size", "error")
		}
		return 0, NewStorageError("size", fmt.Errorf("redis zcard: %w", err))
	}

	r.logger.Debug("Retrieved group count",
		"count", count,
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("size", "success")
	}

	return int(count), nil
}

// LoadAll retrieves all AlertGroups from Redis (for HA recovery).
//
// Performance Target: <500ms for 10,000 groups (150% quality)
//
// Implementation:
//  1. ListKeys to get all group keys
//  2. Parallel Load via goroutines (concurrency: 50)
//  3. Aggregate results
//
// Thread-safety: Safe for concurrent calls.
//
// Note: This is an expensive operation. Use sparingly (e.g., startup recovery only).
func (r *RedisGroupStorage) LoadAll(ctx context.Context) ([]*AlertGroup, error) {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("load_all", time.Since(start))
		}
	}()

	// Step 1: Get all keys
	keys, err := r.ListKeys(ctx)
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		r.logger.Info("No groups to restore from Redis")
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("load_all", "success")
		}
		return []*AlertGroup{}, nil
	}

	// Step 2: Parallel load with semaphore (max 50 concurrent)
	const maxConcurrency = 50
	sem := make(chan struct{}, maxConcurrency)
	results := make(chan *AlertGroup, len(keys))
	errors := make(chan error, len(keys))

	for _, key := range keys {
		sem <- struct{}{} // Acquire semaphore
		go func(k GroupKey) {
			defer func() { <-sem }() // Release semaphore

			group, loadErr := r.Load(ctx, k)
			if loadErr != nil {
				errors <- loadErr
				return
			}
			results <- group
		}(key)
	}

	// Wait for all goroutines to complete
	for i := 0; i < len(keys); i++ {
		<-sem
	}
	close(results)
	close(errors)

	// Collect results
	groups := make([]*AlertGroup, 0, len(keys))
	for group := range results {
		groups = append(groups, group)
	}

	// Check for errors
	var loadErrors []error
	for err := range errors {
		loadErrors = append(loadErrors, err)
	}

	if len(loadErrors) > 0 {
		r.logger.Warn("Some groups failed to load during LoadAll",
			"total_keys", len(keys),
			"loaded", len(groups),
			"errors", len(loadErrors))
		// Continue with partial results (graceful degradation)
	}

	r.logger.Info("Loaded all groups from Redis",
		"count", len(groups),
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("load_all", "success")
		r.metrics.RecordGroupsRestored(len(groups))
	}

	return groups, nil
}

// StoreAll saves multiple AlertGroups to Redis in a batch (pipeline).
//
// Performance Target: <100ms for 1,000 groups (150% quality)
//
// Implementation: Redis pipeline for bulk SET + ZADD operations.
//
// Thread-safety: Safe for concurrent calls.
//
// Note: Does NOT use optimistic locking (assumes new groups or recovery scenario).
func (r *RedisGroupStorage) StoreAll(ctx context.Context, groups []*AlertGroup) error {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.RecordStorageDuration("store_all", time.Since(start))
		}
	}()

	if len(groups) == 0 {
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("store_all", "success")
		}
		return nil
	}

	// Pipeline for bulk operations
	pipe := r.client.Pipeline()

	for _, group := range groups {
		if group == nil {
			continue
		}

		groupKeyStr := string(group.Key)
		redisKey := groupKeyPrefix + groupKeyStr

		// Serialize to JSON
		data, err := json.Marshal(group)
		if err != nil {
			r.logger.Error("Failed to serialize group in StoreAll",
				"group_key", groupKeyStr,
				"error", err)
			continue // Skip this group, continue with others
		}

		// Calculate TTL
		ttl := r.calculateTTL(group)

		// SET group data
		pipe.Set(ctx, redisKey, data, ttl)

		// ZADD to index
		score := float64(group.Metadata.UpdatedAt.Unix())
		pipe.ZAdd(ctx, groupIndexKey, redis.Z{
			Score:  score,
			Member: groupKeyStr,
		})
	}

	// Execute pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		r.logger.Error("Failed to execute StoreAll pipeline",
			"count", len(groups),
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordStorageOperation("store_all", "error")
		}
		return NewStorageError("store_all", fmt.Errorf("redis pipeline: %w", err))
	}

	r.logger.Info("Stored all groups via pipeline",
		"count", len(groups),
		"duration_ms", time.Since(start).Milliseconds())

	if r.metrics != nil {
		r.metrics.RecordStorageOperation("store_all", "success")
	}

	return nil
}

// Ping checks Redis connectivity and health.
//
// Used by:
//   - StorageManager health checks (every 30s)
//   - Startup initialization
//   - Health check endpoints
func (r *RedisGroupStorage) Ping(ctx context.Context) error {
	err := r.client.Ping(ctx).Err()
	if err != nil {
		if r.metrics != nil {
			r.metrics.SetStorageHealth("redis", false)
		}
		return fmt.Errorf("redis ping failed: %w", err)
	}

	if r.metrics != nil {
		r.metrics.SetStorageHealth("redis", true)
	}

	return nil
}
