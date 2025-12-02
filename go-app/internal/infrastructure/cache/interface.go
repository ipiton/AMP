package cache

import (
	"context"
	"time"
)

// Cache определяет интерфейс для работы с кэшем
type Cache interface {
	// Get получает значение по ключу и десериализует в dest
	Get(ctx context.Context, key string, dest interface{}) error

	// Set сохраняет значение с указанным TTL
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// Delete удаляет значение по ключу
	Delete(ctx context.Context, key string) error

	// Exists проверяет существование ключа
	Exists(ctx context.Context, key string) (bool, error)

	// TTL возвращает оставшееся время жизни ключа
	TTL(ctx context.Context, key string) (time.Duration, error)

	// Expire устанавливает TTL для существующего ключа
	Expire(ctx context.Context, key string, ttl time.Duration) error

	// HealthCheck проверяет здоровье cache
	HealthCheck(ctx context.Context) error

	// Ping проверяет соединение с cache
	Ping(ctx context.Context) error

	// Flush очищает весь cache
	Flush(ctx context.Context) error

	// --- Redis SET Operations (for alert tracking) ---

	// SAdd добавляет один или несколько элементов в SET
	SAdd(ctx context.Context, key string, members ...interface{}) error

	// SMembers возвращает все элементы SET
	SMembers(ctx context.Context, key string) ([]string, error)

	// SRem удаляет один или несколько элементов из SET
	SRem(ctx context.Context, key string, members ...interface{}) error

	// SCard возвращает количество элементов в SET
	SCard(ctx context.Context, key string) (int64, error)
}

// CacheStats содержит статистику по работе cache
type CacheStats struct {
	Hits         int64
	Misses       int64
	Sets         int64
	Deletes      int64
	Errors       int64
	Connections  int
	Uptime       time.Duration
}

// CacheConfig содержит конфигурацию cache
type CacheConfig struct {
	// Redis connection settings
	Addr     string        `env:"REDIS_ADDR" default:"localhost:6379"`
	Password string        `env:"REDIS_PASSWORD" default:""`
	DB       int           `env:"REDIS_DB" default:"0"`

	// Pool settings
	PoolSize     int           `env:"REDIS_POOL_SIZE" default:"10"`
	MinIdleConns int           `env:"REDIS_MIN_IDLE_CONNS" default:"1"`
	MaxConnAge   time.Duration `env:"REDIS_MAX_CONN_AGE" default:"30m"`

	// Timeout settings
	DialTimeout  time.Duration `env:"REDIS_DIAL_TIMEOUT" default:"5s"`
	ReadTimeout  time.Duration `env:"REDIS_READ_TIMEOUT" default:"3s"`
	WriteTimeout time.Duration `env:"REDIS_WRITE_TIMEOUT" default:"3s"`

	// Retry settings
	MaxRetries      int           `env:"REDIS_MAX_RETRIES" default:"3"`
	MinRetryBackoff time.Duration `env:"REDIS_MIN_RETRY_BACKOFF" default:"8ms"`
	MaxRetryBackoff time.Duration `env:"REDIS_MAX_RETRY_BACKOFF" default:"512ms"`

	// Circuit breaker settings
	CircuitBreakerEnabled bool          `env:"REDIS_CIRCUIT_BREAKER_ENABLED" default:"true"`
	CircuitBreakerTimeout time.Duration `env:"REDIS_CIRCUIT_BREAKER_TIMEOUT" default:"10s"`

	// Monitoring
	MetricsEnabled bool `env:"REDIS_METRICS_ENABLED" default:"true"`
}

// Validate проверяет корректность конфигурации
func (c *CacheConfig) Validate() error {
	if c.Addr == "" {
		return ErrInvalidConfig
	}
	if c.PoolSize <= 0 {
		return ErrInvalidConfig
	}
	if c.DialTimeout <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

// ErrNotFound возвращается когда ключ не найден в cache
var ErrNotFound = NewCacheError("key not found", "NOT_FOUND")

// ErrInvalidConfig возвращается при неверной конфигурации
var ErrInvalidConfig = NewCacheError("invalid cache configuration", "CONFIG_ERROR")

// ErrConnectionFailed возвращается при проблемах с соединением
var ErrConnectionFailed = NewCacheError("connection failed", "CONNECTION_ERROR")

// CacheError представляет ошибку cache
type CacheError struct {
	Message string
	Code    string
	Cause   error
}

func (e *CacheError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *CacheError) Unwrap() error {
	return e.Cause
}

// NewCacheError создает новую ошибку cache
func NewCacheError(message, code string) *CacheError {
	return &CacheError{
		Message: message,
		Code:    code,
	}
}

// IsNotFound проверяет является ли ошибка ошибкой "не найдено"
func IsNotFound(err error) bool {
	if cacheErr, ok := err.(*CacheError); ok {
		return cacheErr.Code == "NOT_FOUND"
	}
	return false
}

// IsConnectionError проверяет является ли ошибка ошибкой соединения
func IsConnectionError(err error) bool {
	if cacheErr, ok := err.(*CacheError); ok {
		return cacheErr.Code == "CONNECTION_ERROR"
	}
	return false
}
