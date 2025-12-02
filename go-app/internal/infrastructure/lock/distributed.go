package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// DistributedLock представляет распределенную блокировку на базе Redis
type DistributedLock struct {
	redis    *redis.Client
	key      string
	value    string
	ttl      time.Duration
	logger   *slog.Logger
	acquired bool
}

// LockConfig содержит конфигурацию для distributed lock
type LockConfig struct {
	// TTL для автоматического освобождения блокировки
	TTL time.Duration `env:"LOCK_TTL" default:"30s"`

	// Retry настройки
	MaxRetries    int           `env:"LOCK_MAX_RETRIES" default:"3"`
	RetryInterval time.Duration `env:"LOCK_RETRY_INTERVAL" default:"100ms"`

	// Timeout для операций
	AcquireTimeout time.Duration `env:"LOCK_ACQUIRE_TIMEOUT" default:"5s"`
	ReleaseTimeout time.Duration `env:"LOCK_RELEASE_TIMEOUT" default:"2s"`

	// Настройки для генерации уникального значения
	ValuePrefix string `env:"LOCK_VALUE_PREFIX" default:"lock"`
}

// NewDistributedLock создает новый distributed lock
func NewDistributedLock(redis *redis.Client, key string, config *LockConfig, logger *slog.Logger) *DistributedLock {
	if config == nil {
		config = &LockConfig{
			TTL:            30 * time.Second,
			MaxRetries:     3,
			RetryInterval:  100 * time.Millisecond,
			AcquireTimeout: 5 * time.Second,
			ReleaseTimeout: 2 * time.Second,
			ValuePrefix:    "lock",
		}
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Генерируем уникальное значение для блокировки
	value := generateLockValue(config.ValuePrefix)

	return &DistributedLock{
		redis:  redis,
		key:    key,
		value:  value,
		ttl:    config.TTL,
		logger: logger,
	}
}

// generateLockValue генерирует уникальное значение для блокировки
func generateLockValue(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback к timestamp + random
		return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), time.Now().Unix())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(bytes))
}

// Acquire пытается получить блокировку
func (l *DistributedLock) Acquire(ctx context.Context) (bool, error) {
	return l.AcquireWithRetry(ctx, 0)
}

// AcquireWithRetry пытается получить блокировку с повторными попытками
func (l *DistributedLock) AcquireWithRetry(ctx context.Context, maxRetries int) (bool, error) {
	if maxRetries <= 0 {
		maxRetries = 3 // Default retries
	}

	l.logger.Debug("Attempting to acquire lock", "key", l.key, "value", l.value, "ttl", l.ttl)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Создаем контекст с таймаутом для операции
		acquireCtx, cancel := context.WithTimeout(ctx, l.ttl)
		defer cancel()

		// Используем SET NX для атомарного получения блокировки
		result, err := l.redis.SetNX(acquireCtx, l.key, l.value, l.ttl).Result()
		if err != nil {
			l.logger.Error("Failed to acquire lock", "key", l.key, "attempt", attempt+1, "error", err)
			if attempt == maxRetries {
				return false, fmt.Errorf("failed to acquire lock after %d attempts: %w", maxRetries+1, err)
			}
			time.Sleep(l.retryInterval(attempt))
			continue
		}

		if result {
			l.acquired = true
			l.logger.Info("Lock acquired successfully", "key", l.key, "value", l.value, "ttl", l.ttl)
			return true, nil
		}

		l.logger.Debug("Lock already held by another process", "key", l.key, "attempt", attempt+1)
		if attempt == maxRetries {
			return false, nil
		}

		time.Sleep(l.retryInterval(attempt))
	}

	return false, nil
}

// Release освобождает блокировку
func (l *DistributedLock) Release(ctx context.Context) error {
	if !l.acquired {
		l.logger.Warn("Attempting to release lock that was not acquired", "key", l.key)
		return nil
	}

	l.logger.Debug("Releasing lock", "key", l.key, "value", l.value)

	// Lua скрипт для атомарного освобождения блокировки
	// Проверяем, что значение совпадает (защита от освобождения чужой блокировки)
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`

	// Создаем контекст с таймаутом для операции
	releaseCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := l.redis.Eval(releaseCtx, script, []string{l.key}, l.value).Result()
	if err != nil {
		l.logger.Error("Failed to release lock", "key", l.key, "error", err)
		return fmt.Errorf("failed to release lock: %w", err)
	}

	// Проверяем результат
	if result.(int64) == 1 {
		l.acquired = false
		l.logger.Info("Lock released successfully", "key", l.key)
		return nil
	}

	l.logger.Warn("Lock was not released (possibly already expired or held by another process)", "key", l.key)
	return nil
}

// Extend продлевает время жизни блокировки
func (l *DistributedLock) Extend(ctx context.Context, newTTL time.Duration) error {
	if !l.acquired {
		return fmt.Errorf("cannot extend lock that was not acquired")
	}

	l.logger.Debug("Extending lock", "key", l.key, "newTTL", newTTL)

	// Lua скрипт для атомарного продления блокировки
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("expire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`

	extendCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := l.redis.Eval(extendCtx, script, []string{l.key}, l.value, int(newTTL.Seconds())).Result()
	if err != nil {
		l.logger.Error("Failed to extend lock", "key", l.key, "error", err)
		return fmt.Errorf("failed to extend lock: %w", err)
	}

	if result.(int64) == 1 {
		l.ttl = newTTL
		l.logger.Info("Lock extended successfully", "key", l.key, "newTTL", newTTL)
		return nil
	}

	return fmt.Errorf("failed to extend lock (possibly already expired or held by another process)")
}

// IsAcquired проверяет, получена ли блокировка
func (l *DistributedLock) IsAcquired() bool {
	return l.acquired
}

// GetKey возвращает ключ блокировки
func (l *DistributedLock) GetKey() string {
	return l.key
}

// GetValue возвращает значение блокировки
func (l *DistributedLock) GetValue() string {
	return l.value
}

// GetTTL возвращает TTL блокировки
func (l *DistributedLock) GetTTL() time.Duration {
	return l.ttl
}

// retryInterval вычисляет интервал между повторными попытками
func (l *DistributedLock) retryInterval(attempt int) time.Duration {
	// Exponential backoff с jitter
	baseInterval := 100 * time.Millisecond
	interval := time.Duration(attempt+1) * baseInterval

	// Добавляем случайный jitter (±25%)
	jitter := time.Duration(float64(interval) * 0.25 * (2*float64(time.Now().UnixNano()%1000)/1000 - 1))
	return interval + jitter
}

// LockManager управляет множественными блокировками
type LockManager struct {
	redis  *redis.Client
	config *LockConfig
	logger *slog.Logger
	locks  map[string]*DistributedLock
}

// NewLockManager создает новый менеджер блокировок
func NewLockManager(redis *redis.Client, config *LockConfig, logger *slog.Logger) *LockManager {
	if config == nil {
		config = &LockConfig{
			TTL:            30 * time.Second,
			MaxRetries:     3,
			RetryInterval:  100 * time.Millisecond,
			AcquireTimeout: 5 * time.Second,
			ReleaseTimeout: 2 * time.Second,
			ValuePrefix:    "lock",
		}
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &LockManager{
		redis:  redis,
		config: config,
		logger: logger,
		locks:  make(map[string]*DistributedLock),
	}
}

// AcquireLock создает и получает новую блокировку
func (lm *LockManager) AcquireLock(ctx context.Context, key string) (*DistributedLock, error) {
	lock := NewDistributedLock(lm.redis, key, lm.config, lm.logger)

	acquired, err := lock.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	if !acquired {
		return nil, fmt.Errorf("failed to acquire lock for key: %s", key)
	}

	lm.locks[key] = lock
	return lock, nil
}

// ReleaseLock освобождает блокировку
func (lm *LockManager) ReleaseLock(ctx context.Context, key string) error {
	lock, exists := lm.locks[key]
	if !exists {
		lm.logger.Warn("Attempting to release lock that was not managed", "key", key)
		return nil
	}

	err := lock.Release(ctx)
	if err != nil {
		return err
	}

	delete(lm.locks, key)
	return nil
}

// ReleaseAll освобождает все управляемые блокировки
func (lm *LockManager) ReleaseAll(ctx context.Context) error {
	var lastErr error

	for key, lock := range lm.locks {
		if err := lock.Release(ctx); err != nil {
			lm.logger.Error("Failed to release lock", "key", key, "error", err)
			lastErr = err
		}
	}

	lm.locks = make(map[string]*DistributedLock)
	return lastErr
}

// GetLock возвращает блокировку по ключу
func (lm *LockManager) GetLock(key string) (*DistributedLock, bool) {
	lock, exists := lm.locks[key]
	return lock, exists
}

// ListLocks возвращает список всех управляемых блокировок
func (lm *LockManager) ListLocks() []string {
	keys := make([]string, 0, len(lm.locks))
	for key := range lm.locks {
		keys = append(keys, key)
	}
	return keys
}

// Close освобождает все блокировки и очищает ресурсы
func (lm *LockManager) Close(ctx context.Context) error {
	return lm.ReleaseAll(ctx)
}
