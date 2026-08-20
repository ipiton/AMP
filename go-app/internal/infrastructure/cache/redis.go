package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache реализация cache на базе Redis
type RedisCache struct {
	// client is swapped atomically by Reload; every read goes through
	// liveClient() so an in-flight caller sees either the old client or the
	// new one, never a torn value.
	client atomic.Pointer[redis.Client]

	// config is replaced (never mutated in place) by Reload.
	config atomic.Pointer[CacheConfig]

	logger   *slog.Logger
	isClosed bool

	// clientShared records that a caller took the raw *redis.Client out of
	// this wrapper via ShareClient() and holds it for the rest of the
	// process's life. Reload refuses to swap while that is true — see
	// ShareClient and Reload.
	clientShared atomic.Bool

	// reloadMu serialises Reload against itself.
	reloadMu sync.Mutex
}

// NewRedisCache создает новый Redis cache
func NewRedisCache(config *CacheConfig, logger *slog.Logger) (*RedisCache, error) {
	if config == nil {
		config = &CacheConfig{
			Addr:     "localhost:6379",
			Password: "",
			DB:       0,
			PoolSize: 10,
		}
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	client, err := dialRedis(context.Background(), config, logger)
	if err != nil {
		return nil, err
	}

	logger.Info("Connected to Redis", "addr", config.Addr, "db", config.DB)

	cache := &RedisCache{logger: logger}
	cache.client.Store(client)
	cache.config.Store(config)
	return cache, nil
}

// dialRedis builds a Redis client from config and verifies it with PING
// before returning it. Shared by NewRedisCache and Reload, so a reloaded
// client is verified exactly the way a freshly created one is — a client is
// never published unless it answered a PING.
func dialRedis(ctx context.Context, config *CacheConfig, logger *slog.Logger) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            config.Addr,
		Password:        config.Password,
		DB:              config.DB,
		PoolSize:        config.PoolSize,
		MinIdleConns:    config.MinIdleConns,
		DialTimeout:     config.DialTimeout,
		ReadTimeout:     config.ReadTimeout,
		WriteTimeout:    config.WriteTimeout,
		MaxRetries:      config.MaxRetries,
		MinRetryBackoff: config.MinRetryBackoff,
		MaxRetryBackoff: config.MaxRetryBackoff,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		logger.Error("Failed to connect to Redis", "error", err, "addr", config.Addr)
		return nil, NewCacheError("failed to connect to Redis", "CONNECTION_ERROR").WithCause(err)
	}

	return client, nil
}

// liveClient returns the currently active Redis client.
func (rc *RedisCache) liveClient() *redis.Client {
	return rc.client.Load()
}

// Config returns the active cache configuration snapshot. Read-only: Reload
// replaces the pointer rather than mutating it.
func (rc *RedisCache) Config() *CacheConfig {
	return rc.config.Load()
}

// ShareClient returns the underlying *redis.Client AND records that the caller
// is keeping it: from this point on Reload refuses to swap the client
// (ErrClientHandleShared).
//
// Every long-lived Redis consumer in ServiceRegistry takes its handle this
// way — leader election (silence GC claim TTLs), the cluster heartbeat
// registry, the silence event bus, the Redis group storage, the notification
// log (delivered state) and the Redis timer storage. They captured the pointer
// at construction, so a swap could not reach them: on an addr/db change they
// would keep reading and writing a different keyspace than the cache does
// (split claim TTLs and split delivered-state — two replicas each believing
// they own a notification), and on a credential change their pool would fail
// auth the moment it refilled. Closing the replaced client instead breaks them
// outright. Making them follow the swap is tracked as
// FU-REDIS-LIVE-CLIENT-HANDLE.
func (rc *RedisCache) ShareClient() *redis.Client {
	rc.clientShared.Store(true)
	return rc.liveClient()
}

// IsClientShared reports whether a caller has taken a long-lived handle via
// ShareClient(), i.e. whether Reload will refuse to swap the client.
func (rc *RedisCache) IsClientShared() bool {
	return rc.clientShared.Load()
}

// Reload rebuilds the Redis client from config and swaps it in atomically
// (INF-A slice 1, config hot reload).
//
// Sequence: build the new client -> PING-verify it -> atomic swap -> close the
// replaced client. Unlike the database pool there is no drain window: go-redis
// commands are individually short-lived and hold a pooled connection only for
// the duration of one round trip, so a command in flight has either already
// been written on the old client's connection or will be issued on the new one.
//
// Reload REFUSES (ErrClientHandleShared) when the raw client was handed out
// through ShareClient() — see that method for the full reason.
func (rc *RedisCache) Reload(ctx context.Context, config *CacheConfig) error {
	if config == nil {
		return NewCacheError("nil cache config", "INVALID_CONFIG")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if rc.isClosed {
		return ErrConnectionFailed
	}
	if rc.clientShared.Load() {
		return ErrClientHandleShared
	}

	rc.reloadMu.Lock()
	defer rc.reloadMu.Unlock()

	// Re-check under the lock: ShareClient may have been called while waiting.
	if rc.clientShared.Load() {
		return ErrClientHandleShared
	}

	newClient, err := dialRedis(ctx, config, rc.logger)
	if err != nil {
		return err
	}

	oldClient := rc.client.Swap(newClient)
	rc.config.Store(config)

	if oldClient != nil {
		if closeErr := oldClient.Close(); closeErr != nil {
			// Not fatal: the new client is already live and serving. The old
			// one leaking its idle connections until the server times them out
			// is strictly better than reporting a failed reload.
			rc.logger.Warn("failed to close the replaced Redis client", "error", closeErr)
		}
	}

	rc.logger.Info("Redis client reloaded", "addr", config.Addr, "db", config.DB, "pool_size", config.PoolSize)
	return nil
}

// Get получает значение по ключу и десериализует в dest
func (rc *RedisCache) Get(ctx context.Context, key string, dest interface{}) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Debug("Getting value from cache", "key", key)

	val, err := rc.liveClient().Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			rc.logger.Debug("Key not found in cache", "key", key)
			return ErrNotFound
		}
		rc.logger.Error("Failed to get value from cache", "key", key, "error", err)
		return NewCacheError("failed to get value from cache", "GET_ERROR").WithCause(err)
	}

	// Десериализуем JSON
	if err := json.Unmarshal([]byte(val), dest); err != nil {
		rc.logger.Error("Failed to unmarshal cache value", "key", key, "error", err)
		return NewCacheError("failed to unmarshal cache value", "UNMARSHAL_ERROR").WithCause(err)
	}

	rc.logger.Debug("Successfully got value from cache", "key", key)
	return nil
}

// Set сохраняет значение с указанным TTL
func (rc *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Debug("Setting value in cache", "key", key, "ttl", ttl)

	// Сериализуем в JSON
	data, err := json.Marshal(value)
	if err != nil {
		rc.logger.Error("Failed to marshal cache value", "key", key, "error", err)
		return NewCacheError("failed to marshal cache value", "MARSHAL_ERROR").WithCause(err)
	}

	if err := rc.liveClient().Set(ctx, key, data, ttl).Err(); err != nil {
		rc.logger.Error("Failed to set value in cache", "key", key, "error", err)
		return NewCacheError("failed to set value in cache", "SET_ERROR").WithCause(err)
	}

	rc.logger.Debug("Successfully set value in cache", "key", key, "ttl", ttl)
	return nil
}

// Delete удаляет значение по ключу
func (rc *RedisCache) Delete(ctx context.Context, key string) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Debug("Deleting value from cache", "key", key)

	result, err := rc.liveClient().Del(ctx, key).Result()
	if err != nil {
		rc.logger.Error("Failed to delete value from cache", "key", key, "error", err)
		return NewCacheError("failed to delete value from cache", "DELETE_ERROR").WithCause(err)
	}

	if result == 0 {
		rc.logger.Debug("Key not found for deletion", "key", key)
		return ErrNotFound
	}

	rc.logger.Debug("Successfully deleted value from cache", "key", key)
	return nil
}

// Exists проверяет существование ключа
func (rc *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	if rc.isClosed {
		return false, ErrConnectionFailed
	}

	rc.logger.Debug("Checking key existence in cache", "key", key)

	result, err := rc.liveClient().Exists(ctx, key).Result()
	if err != nil {
		rc.logger.Error("Failed to check key existence", "key", key, "error", err)
		return false, NewCacheError("failed to check key existence", "EXISTS_ERROR").WithCause(err)
	}

	exists := result > 0
	rc.logger.Debug("Key existence check", "key", key, "exists", exists)
	return exists, nil
}

// TTL возвращает оставшееся время жизни ключа
func (rc *RedisCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	if rc.isClosed {
		return 0, ErrConnectionFailed
	}

	rc.logger.Debug("Getting TTL for key", "key", key)

	ttl, err := rc.liveClient().TTL(ctx, key).Result()
	if err != nil {
		rc.logger.Error("Failed to get TTL", "key", key, "error", err)
		return 0, NewCacheError("failed to get TTL", "TTL_ERROR").WithCause(err)
	}

	rc.logger.Debug("TTL retrieved", "key", key, "ttl", ttl)
	return ttl, nil
}

// Expire устанавливает TTL для существующего ключа
func (rc *RedisCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Debug("Setting TTL for key", "key", key, "ttl", ttl)

	result, err := rc.liveClient().Expire(ctx, key, ttl).Result()
	if err != nil {
		rc.logger.Error("Failed to set TTL", "key", key, "error", err)
		return NewCacheError("failed to set TTL", "EXPIRE_ERROR").WithCause(err)
	}

	if !result {
		rc.logger.Debug("Key not found for TTL setting", "key", key)
		return ErrNotFound
	}

	rc.logger.Debug("TTL set successfully", "key", key, "ttl", ttl)
	return nil
}

// HealthCheck проверяет здоровье cache
func (rc *RedisCache) HealthCheck(ctx context.Context) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	// Проверяем соединение
	if err := rc.liveClient().Ping(ctx).Err(); err != nil {
		rc.logger.Error("Cache health check failed", "error", err)
		return NewCacheError("cache health check failed", "HEALTH_CHECK_ERROR").WithCause(err)
	}

	return nil
}

// Ping проверяет соединение с cache
func (rc *RedisCache) Ping(ctx context.Context) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	return rc.liveClient().Ping(ctx).Err()
}

// Flush очищает весь cache
func (rc *RedisCache) Flush(ctx context.Context) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Warn("Flushing entire cache")

	if err := rc.liveClient().FlushAll(ctx).Err(); err != nil {
		rc.logger.Error("Failed to flush cache", "error", err)
		return NewCacheError("failed to flush cache", "FLUSH_ERROR").WithCause(err)
	}

	rc.logger.Info("Cache flushed successfully")
	return nil
}

// Close закрывает соединение с Redis
func (rc *RedisCache) Close() error {
	if rc.isClosed {
		return nil
	}

	rc.isClosed = true
	rc.logger.Info("Closing Redis cache connection")

	if err := rc.liveClient().Close(); err != nil {
		rc.logger.Error("Failed to close Redis connection", "error", err)
		return NewCacheError("failed to close Redis connection", "CLOSE_ERROR").WithCause(err)
	}

	rc.logger.Info("Redis cache connection closed")
	return nil
}

// GetClient возвращает Redis клиент для продвинутых операций.
//
// Use this for a SHORT-LIVED borrow. A caller that stores the returned pointer
// for the process's lifetime must use ShareClient() instead, so Reload knows
// it can no longer replace the client from under that holder.
func (rc *RedisCache) GetClient() *redis.Client {
	return rc.liveClient()
}

// GetStats возвращает статистику по cache
func (rc *RedisCache) GetStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Информация о пуле соединений
	poolStats := rc.liveClient().PoolStats()
	stats["pool_size"] = poolStats.TotalConns
	stats["idle_conns"] = poolStats.IdleConns
	stats["stale_conns"] = poolStats.StaleConns

	// Информация о Redis сервере
	info, err := rc.liveClient().Info(ctx, "server").Result()
	if err == nil {
		stats["redis_info"] = info
	}

	// Проверка здоровья
	stats["healthy"] = true
	if err := rc.HealthCheck(ctx); err != nil {
		stats["healthy"] = false
		stats["health_error"] = err.Error()
	}

	return stats, nil
}

// WithCause добавляет причину к ошибке cache
func (e *CacheError) WithCause(cause error) *CacheError {
	e.Cause = cause
	return e
}

// SAdd adds one or more members to a SET (TN-128: Redis SET operations for alert tracking)
func (rc *RedisCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	if key == "" {
		return NewCacheError("key cannot be empty", "INVALID_KEY")
	}

	if len(members) == 0 {
		return nil // No-op if no members
	}

	if err := rc.liveClient().SAdd(ctx, key, members...).Err(); err != nil {
		rc.logger.Error("Failed to add members to SET", "key", key, "error", err)
		return NewCacheError("failed to add members to SET", "SADD_ERROR").WithCause(err)
	}

	rc.logger.Debug("Added members to SET", "key", key, "count", len(members))
	return nil
}

// SMembers returns all members of a SET (TN-128: Redis SET operations)
func (rc *RedisCache) SMembers(ctx context.Context, key string) ([]string, error) {
	if rc.isClosed {
		return nil, ErrConnectionFailed
	}

	if key == "" {
		return nil, NewCacheError("key cannot be empty", "INVALID_KEY")
	}

	members, err := rc.liveClient().SMembers(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			// Key doesn't exist, return empty slice
			return []string{}, nil
		}
		rc.logger.Error("Failed to get SET members", "key", key, "error", err)
		return nil, NewCacheError("failed to get SET members", "SMEMBERS_ERROR").WithCause(err)
	}

	rc.logger.Debug("Retrieved SET members", "key", key, "count", len(members))
	return members, nil
}

// SRem removes one or more members from a SET (TN-128: Redis SET operations)
func (rc *RedisCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	if key == "" {
		return NewCacheError("key cannot be empty", "INVALID_KEY")
	}

	if len(members) == 0 {
		return nil // No-op if no members
	}

	if err := rc.liveClient().SRem(ctx, key, members...).Err(); err != nil {
		rc.logger.Error("Failed to remove members from SET", "key", key, "error", err)
		return NewCacheError("failed to remove members from SET", "SREM_ERROR").WithCause(err)
	}

	rc.logger.Debug("Removed members from SET", "key", key, "count", len(members))
	return nil
}

// SCard returns the number of members in a SET (TN-128: Redis SET operations)
func (rc *RedisCache) SCard(ctx context.Context, key string) (int64, error) {
	if rc.isClosed {
		return 0, ErrConnectionFailed
	}

	if key == "" {
		return 0, NewCacheError("key cannot be empty", "INVALID_KEY")
	}

	count, err := rc.liveClient().SCard(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			// Key doesn't exist, return 0
			return 0, nil
		}
		rc.logger.Error("Failed to get SET cardinality", "key", key, "error", err)
		return 0, NewCacheError("failed to get SET cardinality", "SCARD_ERROR").WithCause(err)
	}

	rc.logger.Debug("Retrieved SET cardinality", "key", key, "count", count)
	return count, nil
}

// NewRedisCacheFromURL создает Redis cache из URL строки
func NewRedisCacheFromURL(url string, logger *slog.Logger) (*RedisCache, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, NewCacheError("failed to parse Redis URL", "PARSE_URL_ERROR").WithCause(err)
	}

	config := &CacheConfig{
		Addr:         opt.Addr,
		Password:     opt.Password,
		DB:           opt.DB,
		PoolSize:     10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}

	return NewRedisCache(config, logger)
}
