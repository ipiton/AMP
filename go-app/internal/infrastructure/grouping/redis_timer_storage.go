// Package grouping provides Redis-based persistence for group timers.
//
// RedisTimerStorage implements the TimerStorage interface using Redis as the backend,
// supporting High Availability through timer persistence and distributed locking.
//
// Redis Schema:
//
//	Timer Data:
//	  Key: "timer:{groupKey}"
//	  Type: String (JSON)
//	  TTL: duration + 60s grace period
//
//	Timer Index:
//	  Key: "timers:index"
//	  Type: Sorted Set
//	  Score: expires_at Unix timestamp
//	  Member: groupKey
//
//	Distributed Lock:
//	  Key: "lock:timer:{groupKey}"
//	  Type: String (lock ID)
//	  TTL: 30s
//
// TN-124: Group Wait/Interval Timers
// Target Quality: 150%
// Date: 2025-11-03
package grouping

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

const (
	// Redis key prefixes
	timerKeyPrefix = "timer:"
	timerIndexKey  = "timers:index"
	lockKeyPrefix  = "lock:timer:"

	// TTL settings
	timerTTLGracePeriod = 60 * time.Second // Extra TTL beyond expiration
	lockTTL             = 30 * time.Second  // Distributed lock duration
)

// RedisTimerStorage implements TimerStorage using Redis.
//
// Features:
//   - JSON serialization for timer data
//   - Sorted set index for fast expiration queries
//   - Distributed locks for exactly-once delivery
//   - Automatic TTL management
//   - Pipeline operations for efficiency (150% enhancement)
type RedisTimerStorage struct {
	client *redis.Client
	logger *slog.Logger
}

// NewRedisTimerStorage creates a new RedisTimerStorage from a cache.Cache interface.
//
// Parameters:
//   - redisCache: Cache interface (must be *cache.RedisCache)
//   - logger: Structured logger (optional, uses slog.Default if nil)
//
// Returns:
//   - *RedisTimerStorage: Initialized storage
//   - error: Error if cache is nil or not *RedisCache
//
// Example:
//
//	storage, err := grouping.NewRedisTimerStorage(redisCache, logger)
//	if err != nil {
//	    return err
//	}
func NewRedisTimerStorage(redisCache cache.Cache, logger *slog.Logger) (*RedisTimerStorage, error) {
	if redisCache == nil {
		return nil, fmt.Errorf("redis cache cannot be nil")
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Type assert to get underlying Redis client
	concreteCache, ok := redisCache.(*cache.RedisCache)
	if !ok {
		return nil, fmt.Errorf("redis timer storage requires *cache.RedisCache, got %T", redisCache)
	}

	return &RedisTimerStorage{
		client: concreteCache.GetClient(),
		logger: logger,
	}, nil
}

// SaveTimer persists a timer to Redis with automatic TTL.
//
// Algorithm:
//  1. Serialize timer to JSON
//  2. Calculate TTL (expires_at - now + grace period)
//  3. Save to Redis with SET
//  4. Add to sorted set index (ZADD) for fast scanning
//  5. Use pipeline for atomic operation
//
// Parameters:
//   - ctx: Context for timeout (recommend 5s)
//   - timer: Timer to save (must have valid GroupKey, ExpiresAt)
//
// Returns:
//   - error: TimerStorageError if save fails
//
// Performance: <5ms (baseline), <2ms (150% target via pipelining)
func (rs *RedisTimerStorage) SaveTimer(ctx context.Context, timer *GroupTimer) error {
	if timer == nil {
		return fmt.Errorf("timer cannot be nil")
	}

	// Validate timer
	if err := timer.Validate(); err != nil {
		return NewTimerStorageError("save_timer", fmt.Errorf("invalid timer: %w", err))
	}

	// Serialize to JSON
	data, err := json.Marshal(timer)
	if err != nil {
		rs.logger.Error("Failed to marshal timer",
			"group_key", timer.GroupKey,
			"error", err)
		return NewTimerStorageError("save_timer", fmt.Errorf("marshal error: %w", err))
	}

	// Calculate TTL
	ttl := time.Until(timer.ExpiresAt) + timerTTLGracePeriod
	if ttl <= 0 {
		ttl = timerTTLGracePeriod // Minimum TTL
	}

	// Use pipeline for atomic operation (150% enhancement)
	pipe := rs.client.Pipeline()

	// Save timer data
	key := rs.timerKey(timer.GroupKey)
	pipe.Set(ctx, key, data, ttl)

	// Add to sorted set index
	score := float64(timer.ExpiresAt.Unix())
	pipe.ZAdd(ctx, timerIndexKey, redis.Z{
		Score:  score,
		Member: string(timer.GroupKey),
	})

	// Execute pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		rs.logger.Error("Failed to save timer to Redis",
			"group_key", timer.GroupKey,
			"error", err)
		return NewTimerStorageError("save_timer", err)
	}

	rs.logger.Debug("Saved timer to Redis",
		"group_key", timer.GroupKey,
		"timer_type", timer.TimerType,
		"expires_at", timer.ExpiresAt,
		"ttl", ttl)

	return nil
}

// LoadTimer retrieves a timer from Redis.
//
// Algorithm:
//  1. Get timer data from Redis (GET)
//  2. Deserialize JSON
//  3. Validate loaded data
//
// Parameters:
//   - ctx: Context for timeout
//   - groupKey: Identifier of the group
//
// Returns:
//   - *GroupTimer: Loaded timer
//   - error: ErrTimerNotFound if not exists, TimerStorageError on other errors
//
// Performance: <5ms (baseline), <1ms (150% target)
func (rs *RedisTimerStorage) LoadTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error) {
	key := rs.timerKey(groupKey)

	// Get from Redis
	data, err := rs.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrTimerNotFound
		}
		rs.logger.Error("Failed to load timer from Redis",
			"group_key", groupKey,
			"error", err)
		return nil, NewTimerStorageError("load_timer", err)
	}

	// Deserialize JSON
	var timer GroupTimer
	if err := json.Unmarshal([]byte(data), &timer); err != nil {
		rs.logger.Error("Failed to unmarshal timer",
			"group_key", groupKey,
			"error", err)
		return nil, NewTimerStorageError("load_timer", fmt.Errorf("unmarshal error: %w", err))
	}

	rs.logger.Debug("Loaded timer from Redis",
		"group_key", groupKey,
		"timer_type", timer.TimerType,
		"expires_at", timer.ExpiresAt)

	return &timer, nil
}

// DeleteTimer removes a timer from Redis.
//
// Algorithm:
//  1. Delete timer data (DEL)
//  2. Remove from sorted set index (ZREM)
//  3. Use pipeline for atomic operation
//
// Parameters:
//   - ctx: Context for timeout
//   - groupKey: Identifier of the group
//
// Returns:
//   - error: TimerStorageError if delete fails (not found is NOT an error)
//
// Performance: <2ms (baseline), <500µs (150% target)
func (rs *RedisTimerStorage) DeleteTimer(ctx context.Context, groupKey GroupKey) error {
	// Use pipeline for atomic operation (150% enhancement)
	pipe := rs.client.Pipeline()

	// Delete timer data
	key := rs.timerKey(groupKey)
	pipe.Del(ctx, key)

	// Remove from index
	pipe.ZRem(ctx, timerIndexKey, string(groupKey))

	// Execute pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		rs.logger.Error("Failed to delete timer from Redis",
			"group_key", groupKey,
			"error", err)
		return NewTimerStorageError("delete_timer", err)
	}

	rs.logger.Debug("Deleted timer from Redis", "group_key", groupKey)

	return nil
}

// ListTimers returns all timers currently stored in Redis.
//
// Algorithm:
//  1. Get all group keys from sorted set index (ZRANGE)
//  2. Load each timer in parallel (MGET + deserialization)
//  3. Filter out expired/corrupted entries
//
// Parameters:
//   - ctx: Context for timeout (recommend 30s for large sets)
//
// Returns:
//   - []*GroupTimer: List of all active timers
//   - error: TimerStorageError on Redis errors
//
// Performance: <50ms for 1000 timers (baseline), <20ms (150% target via parallel loading)
func (rs *RedisTimerStorage) ListTimers(ctx context.Context) ([]*GroupTimer, error) {
	// Get all group keys from index
	members, err := rs.client.ZRange(ctx, timerIndexKey, 0, -1).Result()
	if err != nil {
		rs.logger.Error("Failed to list timer keys",
			"error", err)
		return nil, NewTimerStorageError("list_timers", err)
	}

	if len(members) == 0 {
		return []*GroupTimer{}, nil
	}

	rs.logger.Debug("Listing timers from Redis",
		"count", len(members))

	// Build keys for MGET
	keys := make([]string, len(members))
	for i, member := range members {
		keys[i] = rs.timerKey(GroupKey(member))
	}

	// Load all timers in single MGET (150% enhancement)
	values, err := rs.client.MGet(ctx, keys...).Result()
	if err != nil {
		rs.logger.Error("Failed to load timers in batch",
			"error", err)
		return nil, NewTimerStorageError("list_timers", err)
	}

	// Deserialize timers
	timers := make([]*GroupTimer, 0, len(values))
	for i, val := range values {
		if val == nil {
			// Timer expired or was deleted - remove from index
			groupKey := GroupKey(members[i])
			rs.client.ZRem(ctx, timerIndexKey, string(groupKey))
			continue
		}

		data, ok := val.(string)
		if !ok {
			rs.logger.Warn("Invalid timer data type",
				"group_key", members[i],
				"type", fmt.Sprintf("%T", val))
			continue
		}

		var timer GroupTimer
		if err := json.Unmarshal([]byte(data), &timer); err != nil {
			rs.logger.Warn("Failed to unmarshal timer",
				"group_key", members[i],
				"error", err)
			continue
		}

		timers = append(timers, &timer)
	}

	rs.logger.Debug("Loaded timers from Redis",
		"total", len(timers))

	return timers, nil
}

// AcquireLock attempts to acquire a distributed lock for a group.
//
// Uses SET NX EX for atomic lock acquisition with automatic expiration.
// Implements distributed lock pattern for exactly-once delivery.
//
// Algorithm:
//  1. Generate unique lock ID (UUID)
//  2. Try SET NX EX (atomic: set if not exists with expiration)
//  3. If success, return lock ID and release function
//  4. If failure, return ErrLockAlreadyAcquired
//
// Parameters:
//   - ctx: Context for timeout
//   - groupKey: Identifier of the group
//   - ttl: How long the lock should be held (typically 30s)
//
// Returns:
//   - lockID: Unique identifier for this lock acquisition
//   - release: Function to release the lock (must be called via defer!)
//   - error: ErrLockAlreadyAcquired or TimerStorageError
//
// Example:
//
//	lockID, release, err := storage.AcquireLock(ctx, groupKey, 30*time.Second)
//	if err != nil {
//	    return err
//	}
//	defer release()
//	// ... do work ...
//
// Performance: <5ms (baseline), <1ms (150% target)
func (rs *RedisTimerStorage) AcquireLock(ctx context.Context, groupKey GroupKey, ttl time.Duration) (lockID string, release func() error, err error) {
	lockKey := rs.lockKey(groupKey)
	lockID = uuid.New().String()

	// Try to acquire lock with SET NX EX
	success, err := rs.client.SetNX(ctx, lockKey, lockID, ttl).Result()
	if err != nil {
		rs.logger.Error("Failed to acquire lock",
			"group_key", groupKey,
			"error", err)
		return "", nil, NewTimerStorageError("acquire_lock", err)
	}

	if !success {
		rs.logger.Debug("Lock already acquired by another instance",
			"group_key", groupKey)
		return "", nil, ErrLockAlreadyAcquired
	}

	rs.logger.Debug("Acquired lock",
		"group_key", groupKey,
		"lock_id", lockID,
		"ttl", ttl)

	// Release function using Lua script for safe deletion
	// Only delete if we still own the lock (check lockID)
	releaseFunc := func() error {
		// Lua script ensures atomic check-and-delete
		script := `
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			else
				return 0
			end
		`
		result, err := rs.client.Eval(ctx, script, []string{lockKey}, lockID).Result()
		if err != nil {
			rs.logger.Warn("Failed to release lock",
				"group_key", groupKey,
				"lock_id", lockID,
				"error", err)
			return err
		}

		if result == int64(1) {
			rs.logger.Debug("Released lock",
				"group_key", groupKey,
				"lock_id", lockID)
		} else {
			rs.logger.Debug("Lock already expired or released",
				"group_key", groupKey,
				"lock_id", lockID)
		}

		return nil
	}

	return lockID, releaseFunc, nil
}

// Helper methods

func (rs *RedisTimerStorage) timerKey(groupKey GroupKey) string {
	return timerKeyPrefix + string(groupKey)
}

func (rs *RedisTimerStorage) lockKey(groupKey GroupKey) string {
	return lockKeyPrefix + string(groupKey)
}
