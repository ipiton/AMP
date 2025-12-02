package migrations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
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

// ExecuteWithRetry выполняет операцию с повторными попытками
func (eh *ErrorHandler) ExecuteWithRetry(ctx context.Context, operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= eh.maxRetries; attempt++ {
		if attempt > 0 {
			eh.logger.Info("Retrying migration operation",
				"attempt", attempt,
				"max_retries", eh.maxRetries)

			select {
			case <-time.After(eh.retryDelay):
				// Продолжаем после задержки
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if err := operation(); err != nil {
			lastErr = err

			// Проверяем, можно ли повторить попытку
			if !eh.isRetryable(err) {
				break
			}

			eh.logger.Warn("Migration operation failed, retrying",
				"attempt", attempt+1,
				"error", err)
			continue
		}

		// Успешно выполнено
		if attempt > 0 {
			eh.logger.Info("Migration operation succeeded after retry",
				"attempts", attempt+1)
		}
		return nil
	}

	eh.logger.Error("Migration operation failed after all retries",
		"max_retries", eh.maxRetries,
		"last_error", lastErr)

	return lastErr
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

// RecoveryHandler обрабатывает восстановление после ошибок
type RecoveryHandler struct {
	logger  *slog.Logger
	manager *MigrationManager
}

// NewRecoveryHandler создает новый обработчик восстановления
func NewRecoveryHandler(logger *slog.Logger, manager *MigrationManager) *RecoveryHandler {
	return &RecoveryHandler{
		logger:  logger,
		manager: manager,
	}
}

// ExecuteWithRecovery выполняет операцию с автоматическим восстановлением
func (rh *RecoveryHandler) ExecuteWithRecovery(ctx context.Context, operation func() error) error {
	// Сначала пробуем выполнить операцию
	if err := operation(); err != nil {
		rh.logger.Warn("Operation failed, attempting recovery", "error", err)

		// Пробуем восстановиться
		if recoveryErr := rh.attemptRecovery(ctx, err); recoveryErr != nil {
			rh.logger.Error("Recovery failed", "original_error", err, "recovery_error", recoveryErr)
			return fmt.Errorf("operation failed and recovery unsuccessful: %w", recoveryErr)
		}

		// Повторяем операцию после восстановления
		rh.logger.Info("Recovery successful, retrying operation")
		if err := operation(); err != nil {
			rh.logger.Error("Operation failed again after recovery", "error", err)
			return err
		}
	}

	rh.logger.Info("Operation completed successfully")
	return nil
}

// attemptRecovery пытается восстановиться от ошибки
func (rh *RecoveryHandler) attemptRecovery(ctx context.Context, err error) error {
	errStr := strings.ToLower(err.Error())

	// Разные стратегии восстановления для разных типов ошибок
	if strings.Contains(errStr, "connection") || strings.Contains(errStr, "timeout") {
		return rh.recoverConnection(ctx)
	}

	if strings.Contains(errStr, "lock") || strings.Contains(errStr, "deadlock") {
		return rh.recoverLock(ctx)
	}

	if strings.Contains(errStr, "disk") || strings.Contains(errStr, "space") {
		return rh.recoverDiskSpace(ctx)
	}

	// Для неизвестных ошибок пытаемся простой переподключением
	return rh.recoverGeneric(ctx)
}

// recoverConnection восстанавливает соединение
func (rh *RecoveryHandler) recoverConnection(ctx context.Context) error {
	rh.logger.Info("Attempting connection recovery")

	// Закрываем текущее соединение
	if err := rh.manager.Disconnect(ctx); err != nil {
		rh.logger.Warn("Failed to disconnect during recovery", "error", err)
	}

	// Ждем немного
	time.Sleep(2 * time.Second)

	// Пытаемся подключиться снова
	if err := rh.manager.Connect(ctx); err != nil {
		return fmt.Errorf("failed to reconnect: %w", err)
	}

	rh.logger.Info("Connection recovery successful")
	return nil
}

// recoverLock восстанавливает от блокировок
func (rh *RecoveryHandler) recoverLock(ctx context.Context) error {
	rh.logger.Info("Attempting lock recovery")

	// Для блокировок просто ждем
	time.Sleep(5 * time.Second)

	rh.logger.Info("Lock recovery completed")
	return nil
}

// recoverDiskSpace восстанавливает от проблем с дисковым пространством
func (rh *RecoveryHandler) recoverDiskSpace(ctx context.Context) error {
	rh.logger.Warn("Disk space issue detected - manual intervention required")

	// Для проблем с диском можем только залогировать
	// В реальном приложении здесь можно вызвать cleanup
	return fmt.Errorf("disk space issue requires manual intervention")
}

// recoverGeneric пытается универсальное восстановление
func (rh *RecoveryHandler) recoverGeneric(ctx context.Context) error {
	rh.logger.Info("Attempting generic recovery")

	// Простое переподключение
	return rh.recoverConnection(ctx)
}

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
