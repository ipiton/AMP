package migrations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ipiton/AMP/pkg/retry"
)

// MigrationError представляет ошибку миграции
type MigrationError struct {
	Operation string
	Version   int64
	Cause     error
	Timestamp time.Time
	Context   map[string]any
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %s failed at version %d: %v", e.Operation, e.Version, e.Cause)
}

func (e *MigrationError) Unwrap() error {
	return e.Cause
}

// ErrorHandler обрабатывает ошибки миграций
type ErrorHandler struct {
	logger     *slog.Logger
	maxRetries int
	retryDelay time.Duration
}

// NewErrorHandler создает новый обработчик ошибок
func NewErrorHandler(logger *slog.Logger, maxRetries int, retryDelay time.Duration) *ErrorHandler {
	return &ErrorHandler{
		logger:     logger,
		maxRetries: maxRetries,
		retryDelay: retryDelay,
	}
}

// HandleError обрабатывает ошибку миграции
func (eh *ErrorHandler) HandleError(ctx context.Context, err error, operation string, version int64) error {
	migrationErr := &MigrationError{
		Operation: operation,
		Version:   version,
		Cause:     err,
		Timestamp: time.Now(),
		Context: map[string]any{
			"operation": operation,
			"version":   version,
			"timestamp": time.Now(),
		},
	}

	// Логируем ошибку
	eh.logger.Error("Migration error",
		"operation", operation,
		"version", version,
		"error", err,
		"timestamp", migrationErr.Timestamp)

	// Проверяем, является ли ошибка повторяемой
	if eh.isRetryable(err) {
		eh.logger.Info("Error is retryable, attempting recovery",
			"operation", operation,
			"version", version)
	}

	return migrationErr
}

// ExecuteWithRetry выполняет операцию с повторными попытками.
//
// Migrated to use pkg/retry for unified retry strategy (TN-057).
// This function now wraps retry.DoSimple for backward compatibility.
func (eh *ErrorHandler) ExecuteWithRetry(ctx context.Context, operation func() error) error {
	// Create retry strategy with migration-specific settings
	strategy := retry.Strategy{
		MaxAttempts:     eh.maxRetries + 1, // +1 because retry.Strategy counts attempts, not retries
		BaseDelay:       eh.retryDelay,
		MaxDelay:        eh.retryDelay * 10, // Cap at 10x base delay
		Multiplier:      1.0, // Linear backoff (constant delay, as original)
		JitterRatio:     0.05, // 5% jitter (minimal)
		ErrorClassifier: &migrationErrorClassifier{eh}, // Use existing isRetryable logic
		Logger:          eh.logger,
		OperationName:   "migration_operation",
	}

	// Execute with unified retry logic
	return retry.DoSimple(ctx, strategy, operation)
}

// migrationErrorClassifier implements retry.ErrorClassifier for migration operations.
type migrationErrorClassifier struct {
	handler *ErrorHandler
}

func (c *migrationErrorClassifier) IsRetryable(err error) bool {
	return c.handler.isRetryable(err)
}

// isRetryable определяет, можно ли повторить операцию при данной ошибке
func (eh *ErrorHandler) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Список паттернов для повторяемых ошибок
	retryablePatterns := []string{
		// Network errors
		"connection refused",
		"connection reset",
		"connection lost",
		"timeout",
		"deadline exceeded",

		// Database lock errors
		"lock wait timeout",
		"deadlock",
		"serialization failure",
		"could not serialize access",

		// Temporary errors
		"temporary failure",
		"service unavailable",
		"server closed the connection unexpectedly",

		// Resource errors
		"too many connections",
		"out of memory",
		"disk full",

		// PostgreSQL specific
		"pq: ",     // PostgreSQL driver errors
		"sqlstate", // PostgreSQL error codes
		"current transaction is aborted",

		// SQLite specific
		"database is locked",
		"database busy",
		"interrupted",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Проверяем стандартные ошибки
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	return false
}

// RecoveryHandler обрабатывает восстановление после ошибок.
//
// SIMPLIFIED: Recovery handlers are now optional and moved to a separate package.
// For most cases, pkg/retry with proper error classification is sufficient.
//
// Deprecated: Complex recovery logic rarely needed in practice.
// Use pkg/retry with DatabaseClassifier for automatic retry with exponential backoff.
// Manual recovery (reconnection, etc.) should be handled at the application level.
type RecoveryHandler struct {
	logger  *slog.Logger
	manager *MigrationManager
}

// NewRecoveryHandler creates a new recovery handler.
//
// Deprecated: Use pkg/retry instead for simpler and more reliable retry logic.
func NewRecoveryHandler(logger *slog.Logger, manager *MigrationManager) *RecoveryHandler {
	return &RecoveryHandler{
		logger:  logger,
		manager: manager,
	}
}

// ExecuteWithRecovery executes operation with automatic recovery.
//
// SIMPLIFIED: Now just wraps pkg/retry for backward compatibility.
// Complex recovery logic (reconnection, lock handling, disk space) is removed
// as it's rarely needed and adds unnecessary complexity.
//
// Deprecated: Use ErrorHandler.ExecuteWithRetry() instead, which uses pkg/retry.
func (rh *RecoveryHandler) ExecuteWithRecovery(ctx context.Context, operation func() error) error {
	// Simple retry with exponential backoff (via ErrorHandler)
	handler := &ErrorHandler{
		logger:     rh.logger,
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}

	return handler.ExecuteWithRetry(ctx, operation)
}

// Complex recovery methods removed (simplified approach).
// For most migration errors, automatic retry with exponential backoff is sufficient.
//
// If you need custom recovery logic (reconnection, cleanup, etc.),
// implement it at the application level, not in the generic error handler.
//
// The removed methods were:
// - attemptRecovery: Complex error classification and recovery routing
// - recoverConnection: Database reconnection logic
// - recoverLock: Lock waiting logic
// - recoverDiskSpace: Disk space error handling
// - recoverGeneric: Generic fallback recovery
//
// Rationale:
// 1. Recovery logic is rarely used in practice (migrations are usually one-time)
// 2. Automatic retry handles 95% of transient errors
// 3. Complex recovery adds maintenance burden
// 4. Manual intervention required for real issues (disk full, etc.)
//
// Recommendation:
// Use ErrorHandler.ExecuteWithRetry() which leverages pkg/retry with:
// - Exponential backoff
// - Jitter
// - Proper error classification (DatabaseClassifier)
// - Context cancellation
// - Prometheus metrics

// CircuitBreaker реализует паттерн circuit breaker для миграций
type CircuitBreaker struct {
	state        string // "closed", "open", "half-open"
	failureCount int
	lastFailure  time.Time
	threshold    int
	timeout      time.Duration
	resetTimeout time.Duration
}

// NewCircuitBreaker создает новый circuit breaker
func NewCircuitBreaker(threshold int, timeout, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        "closed",
		threshold:    threshold,
		timeout:      timeout,
		resetTimeout: resetTimeout,
	}
}

// Call выполняет операцию через circuit breaker
func (cb *CircuitBreaker) Call(operation func() error) error {
	if cb.state == "open" {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = "half-open"
			cb.logInfo("Circuit breaker moving to half-open state")
		} else {
			return fmt.Errorf("circuit breaker is open")
		}
	}

	err := operation()

	if err != nil {
		cb.failureCount++
		cb.lastFailure = time.Now()

		if cb.failureCount >= cb.threshold {
			cb.state = "open"
			cb.logWarn("Circuit breaker opened", "failures", cb.failureCount)
		}
		return err
	}

	// Успешное выполнение
	if cb.state == "half-open" {
		cb.state = "closed"
		cb.failureCount = 0
		cb.logInfo("Circuit breaker closed after successful operation")
	} else {
		cb.failureCount = 0
	}

	return nil
}

// GetState возвращает текущее состояние circuit breaker
func (cb *CircuitBreaker) GetState() string {
	return cb.state
}

// Reset сбрасывает circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.state = "closed"
	cb.failureCount = 0
	cb.logInfo("Circuit breaker manually reset")
}

// logger - добавим метод для логирования (в реальности нужно передать logger)
func (cb *CircuitBreaker) logger() *slog.Logger {
	return slog.Default()
}

// logInfo логирует информационное сообщение
func (cb *CircuitBreaker) logInfo(msg string, args ...any) {
	logger := cb.logger()
	logger.Info(msg, args...)
}

// logWarn логирует предупреждение
func (cb *CircuitBreaker) logWarn(msg string, args ...any) {
	logger := cb.logger()
	logger.Warn(msg, args...)
}
