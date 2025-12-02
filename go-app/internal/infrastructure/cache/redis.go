package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache реализация cache на базе Redis
type RedisCache struct {
	client   *redis.Client
	config   *CacheConfig
	logger   *slog.Logger
	isClosed bool
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

	// Проверяем соединение
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "error", err, "addr", config.Addr)
		return nil, NewCacheError("failed to connect to Redis", "CONNECTION_ERROR").WithCause(err)
	}

	logger.Info("Connected to Redis", "addr", config.Addr, "db", config.DB)

	return &RedisCache{
		client: client,
		config: config,
		logger: logger,
	}, nil
}

// Get получает значение по ключу и десериализует в dest
func (rc *RedisCache) Get(ctx context.Context, key string, dest interface{}) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Debug("Getting value from cache", "key", key)

	val, err := rc.client.Get(ctx, key).Result()
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

	if err := rc.client.Set(ctx, key, data, ttl).Err(); err != nil {
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

	result, err := rc.client.Del(ctx, key).Result()
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

	result, err := rc.client.Exists(ctx, key).Result()
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

	ttl, err := rc.client.TTL(ctx, key).Result()
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

	result, err := rc.client.Expire(ctx, key, ttl).Result()
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
	if err := rc.client.Ping(ctx).Err(); err != nil {
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

	return rc.client.Ping(ctx).Err()
}

// Flush очищает весь cache
func (rc *RedisCache) Flush(ctx context.Context) error {
	if rc.isClosed {
		return ErrConnectionFailed
	}

	rc.logger.Warn("Flushing entire cache")

	if err := rc.client.FlushAll(ctx).Err(); err != nil {
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

	if err := rc.client.Close(); err != nil {
		rc.logger.Error("Failed to close Redis connection", "error", err)
		return NewCacheError("failed to close Redis connection", "CLOSE_ERROR").WithCause(err)
	}

	rc.logger.Info("Redis cache connection closed")
	return nil
}

// GetClient возвращает Redis клиент для продвинутых операций
func (rc *RedisCache) GetClient() *redis.Client {
	return rc.client
}

// GetStats возвращает статистику по cache
func (rc *RedisCache) GetStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Информация о пуле соединений
	poolStats := rc.client.PoolStats()
	stats["pool_size"] = poolStats.TotalConns
	stats["idle_conns"] = poolStats.IdleConns
	stats["stale_conns"] = poolStats.StaleConns

	// Информация о Redis сервере
	info, err := rc.client.Info(ctx, "server").Result()
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

	if err := rc.client.SAdd(ctx, key, members...).Err(); err != nil {
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

	members, err := rc.client.SMembers(ctx, key).Result()
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

	if err := rc.client.SRem(ctx, key, members...).Err(); err != nil {
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

	count, err := rc.client.SCard(ctx, key).Result()
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
		Addr:     opt.Addr,
		Password: opt.Password,
		DB:       opt.DB,
		PoolSize: 10,
	}

	return NewRedisCache(config, logger)
}
