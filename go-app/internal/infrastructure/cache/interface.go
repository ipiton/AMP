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

	// --- SET Operations (for alert tracking) ---

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
	Hits        int64
	Misses      int64
	Sets        int64
	Deletes     int64
	Errors      int64
	Connections int
	Uptime      time.Duration
}

// ErrNotFound возвращается когда ключ не найден в cache
var ErrNotFound = NewCacheError("key not found", "NOT_FOUND")

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

// WithCause sets the underlying error.
func (e *CacheError) WithCause(cause error) *CacheError {
	e.Cause = cause
	return e
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
