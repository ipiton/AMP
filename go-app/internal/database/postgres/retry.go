package postgres

import (
	"context"
	"math/rand"
	"time"

	"log/slog"
)

// RetryConfig содержит настройки для retry механизма
type RetryConfig struct {
	MaxRetries    int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	JitterFactor  float64
}

// DefaultRetryConfig возвращает конфигурацию retry по умолчанию
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		JitterFactor:  0.1,
	}
}

// RetryExecutor выполняет операции с retry логикой
type RetryExecutor struct {
	config RetryConfig
	logger *slog.Logger
}

// NewRetryExecutor создает новый retry executor
func NewRetryExecutor(config RetryConfig, logger *slog.Logger) *RetryExecutor {
	if logger == nil {
		logger = slog.Default()
	}

	return &RetryExecutor{
		config: config,
		logger: logger,
	}
}

// Execute выполняет операцию с retry логикой
func (r *RetryExecutor) Execute(ctx context.Context, operation func() error) error {
	var lastErr error
	delay := r.config.InitialDelay

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		// Выполняем операцию
		err := operation()
		if err == nil {
			// Успешное выполнение
			if attempt > 0 {
				r.logger.Info("Operation succeeded after retry",
					"attempt", attempt+1,
					"total_attempts", attempt+1)
			}
			return nil
		}

		lastErr = err

		// Проверяем, нужно ли повторять попытку
		if attempt < r.config.MaxRetries && r.shouldRetry(err) {
			r.logger.Warn("Operation failed, retrying",
				"attempt", attempt+1,
				"max_retries", r.config.MaxRetries,
				"delay", delay,
				"error", err)

			// Ждем перед следующей попыткой
			if !r.waitWithContext(ctx, delay) {
				// Контекст отменен
				return ctx.Err()
			}

			// Увеличиваем задержку для следующей попытки
			delay = r.nextDelay(delay)
		} else {
			// Последняя попытка или ошибка не retryable
			break
		}
	}

	r.logger.Error("Operation failed after all retries",
		"max_retries", r.config.MaxRetries,
		"error", lastErr)

	return lastErr
}

// ExecuteWithResult выполняет операцию с retry логикой и возвращает результат
func (r *RetryExecutor) ExecuteWithResult(ctx context.Context, operation func() (interface{}, error)) (interface{}, error) {
	var lastResult interface{}
	var lastErr error
	delay := r.config.InitialDelay

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		// Выполняем операцию
		result, err := operation()
		if err == nil {
			// Успешное выполнение
			if attempt > 0 {
				r.logger.Info("Operation succeeded after retry",
					"attempt", attempt+1,
					"total_attempts", attempt+1)
			}
			return result, nil
		}

		lastResult = result
		lastErr = err

		// Проверяем, нужно ли повторять попытку
		if attempt < r.config.MaxRetries && r.shouldRetry(err) {
			r.logger.Warn("Operation failed, retrying",
				"attempt", attempt+1,
				"max_retries", r.config.MaxRetries,
				"delay", delay,
				"error", err)

			// Ждем перед следующей попыткой
			if !r.waitWithContext(ctx, delay) {
				// Контекст отменен
				return nil, ctx.Err()
			}

			// Увеличиваем задержку для следующей попытки
			delay = r.nextDelay(delay)
		} else {
			// Последняя попытка или ошибка не retryable
			break
		}
	}

	r.logger.Error("Operation failed after all retries",
		"max_retries", r.config.MaxRetries,
		"error", lastErr)

	return lastResult, lastErr
}

// shouldRetry определяет, стоит ли повторять операцию при данной ошибке
func (r *RetryExecutor) shouldRetry(err error) bool {
	return IsRetryable(err)
}

// waitWithContext ждет указанное время с учетом контекста
func (r *RetryExecutor) waitWithContext(ctx context.Context, delay time.Duration) bool {
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

// nextDelay рассчитывает следующую задержку с exponential backoff и jitter
func (r *RetryExecutor) nextDelay(currentDelay time.Duration) time.Duration {
	// Exponential backoff
	nextDelay := time.Duration(float64(currentDelay) * r.config.BackoffFactor)

	// Ограничиваем максимальной задержкой
	if nextDelay > r.config.MaxDelay {
		nextDelay = r.config.MaxDelay
	}

	// Добавляем jitter для предотвращения thundering herd
	if r.config.JitterFactor > 0 {
		jitter := time.Duration(float64(nextDelay) * r.config.JitterFactor * rand.Float64())
		nextDelay += jitter
	}

	return nextDelay
}

// CircuitBreaker реализует circuit breaker паттерн
type CircuitBreaker struct {
	state        CircuitBreakerState
	failureCount int
	maxFailures  int
	resetTimeout time.Duration
	lastFailure  time.Time
	lastSuccess  time.Time
}

// NewCircuitBreaker создает новый circuit breaker
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        StateClosed,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
	}
}

// Call выполняет операцию через circuit breaker
func (cb *CircuitBreaker) Call(operation func() error) error {
	switch cb.state {
	case StateOpen:
		// Если circuit breaker открыт, проверяем не пора ли переходить в half-open
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = StateHalfOpen
		} else {
			return ErrCircuitBreakerOpen
		}
	case StateHalfOpen:
		// В half-open состоянии выполняем проверку
		fallthrough
	case StateClosed:
		// В закрытом состоянии выполняем обычную проверку
		break
	}

	// Выполняем операцию
	err := operation()

	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// recordFailure записывает неудачную попытку
func (cb *CircuitBreaker) recordFailure() {
	cb.failureCount++
	cb.lastFailure = time.Now()

	if cb.failureCount >= cb.maxFailures {
		cb.state = StateOpen
	}
}

// recordSuccess записывает успешную попытку
func (cb *CircuitBreaker) recordSuccess() {
	cb.failureCount = 0
	cb.lastSuccess = time.Now()
	cb.state = StateClosed
}

// GetState возвращает текущее состояние circuit breaker
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	return cb.state
}

// GetFailureCount возвращает количество неудачных попыток
func (cb *CircuitBreaker) GetFailureCount() int {
	return cb.failureCount
}

// IsOpen проверяет, открыт ли circuit breaker
func (cb *CircuitBreaker) IsOpen() bool {
	return cb.state == StateOpen
}

// Reset сбрасывает circuit breaker в исходное состояние
func (cb *CircuitBreaker) Reset() {
	cb.state = StateClosed
	cb.failureCount = 0
	cb.lastFailure = time.Time{}
	cb.lastSuccess = time.Now()
}
