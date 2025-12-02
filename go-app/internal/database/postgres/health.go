package postgres

import (
	"context"
	"time"
)

// HealthChecker определяет интерфейс для проверки здоровья connection pool
type HealthChecker interface {
	CheckHealth(ctx context.Context) error
	GetStats() PoolStats
	IsHealthy() bool
	LastCheckTime() time.Time
}

// DefaultHealthChecker реализует проверку здоровья с помощью простого SQL запроса
type DefaultHealthChecker struct {
	pool      *PostgresPool
	lastCheck time.Time
	isHealthy bool
}

// NewHealthChecker создает новый health checker
func NewHealthChecker(pool *PostgresPool) HealthChecker {
	return &DefaultHealthChecker{
		pool:      pool,
		lastCheck: time.Now(),
		isHealthy: false,
	}
}

// CheckHealth выполняет проверку здоровья database connection
func (h *DefaultHealthChecker) CheckHealth(ctx context.Context) error {
	// Создаем контекст с таймаутом для health check
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Выполняем простой запрос для проверки соединения
	rows, err := h.pool.pool.Query(checkCtx, "SELECT 1")
	if err != nil {
		h.pool.metrics.RecordHealthCheck(false)
		h.isHealthy = false
		h.lastCheck = time.Now()
		return err
	}
	defer rows.Close()

	// Проверяем что запрос вернул результат
	if !rows.Next() {
		h.pool.metrics.RecordHealthCheck(false)
		h.isHealthy = false
		h.lastCheck = time.Now()
		return ErrHealthCheckFailed
	}

	var result int
	if err := rows.Scan(&result); err != nil {
		h.pool.metrics.RecordHealthCheck(false)
		h.isHealthy = false
		h.lastCheck = time.Now()
		return err
	}

	// Проверяем что результат корректный
	if result != 1 {
		h.pool.metrics.RecordHealthCheck(false)
		h.isHealthy = false
		h.lastCheck = time.Now()
		return ErrHealthCheckFailed
	}

	h.pool.metrics.RecordHealthCheck(true)
	h.isHealthy = true
	h.lastCheck = time.Now()
	return nil
}

// GetStats возвращает текущую статистику pool
func (h *DefaultHealthChecker) GetStats() PoolStats {
	return h.pool.metrics.Snapshot()
}

// IsHealthy возвращает текущее состояние здоровья
func (h *DefaultHealthChecker) IsHealthy() bool {
	return h.isHealthy
}

// LastCheckTime возвращает время последнего health check
func (h *DefaultHealthChecker) LastCheckTime() time.Time {
	return h.lastCheck
}

// PeriodicHealthChecker выполняет периодические проверки здоровья
type PeriodicHealthChecker struct {
	checker   HealthChecker
	interval  time.Duration
	stopCh    chan struct{}
	isRunning bool
}

// NewPeriodicHealthChecker создает periodic health checker
func NewPeriodicHealthChecker(checker HealthChecker, interval time.Duration) *PeriodicHealthChecker {
	return &PeriodicHealthChecker{
		checker:   checker,
		interval:  interval,
		stopCh:    make(chan struct{}),
		isRunning: false,
	}
}

// Start запускает периодические проверки здоровья
func (p *PeriodicHealthChecker) Start(ctx context.Context) {
	if p.isRunning {
		return
	}

	p.isRunning = true

	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				p.isRunning = false
				return
			case <-p.stopCh:
				p.isRunning = false
				return
			case <-ticker.C:
				// Выполняем health check в фоне
				checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

				if err := p.checker.CheckHealth(checkCtx); err != nil {
					// Логируем ошибку, но продолжаем
					// (логирование будет добавлено позже)
				}

				cancel()
			}
		}
	}()
}

// Stop останавливает периодические проверки
func (p *PeriodicHealthChecker) Stop() {
	if !p.isRunning {
		return
	}

	select {
	case p.stopCh <- struct{}{}:
	default:
		// Channel уже закрыт или заполнен
	}
}

// IsRunning возвращает статус выполнения
func (p *PeriodicHealthChecker) IsRunning() bool {
	return p.isRunning
}

// CircuitBreakerHealthChecker добавляет circuit breaker паттерн
type CircuitBreakerHealthChecker struct {
	checker      HealthChecker
	failureCount int
	maxFailures  int
	resetTimeout time.Duration
	lastFailure  time.Time
	state        CircuitBreakerState
}

// CircuitBreakerState представляет состояние circuit breaker
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

// NewCircuitBreakerHealthChecker создает health checker с circuit breaker
func NewCircuitBreakerHealthChecker(checker HealthChecker, maxFailures int, resetTimeout time.Duration) *CircuitBreakerHealthChecker {
	return &CircuitBreakerHealthChecker{
		checker:      checker,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// CheckHealth выполняет проверку с circuit breaker логикой
func (c *CircuitBreakerHealthChecker) CheckHealth(ctx context.Context) error {
	switch c.state {
	case StateOpen:
		// Если circuit breaker открыт, проверяем не пора ли переходить в half-open
		if time.Since(c.lastFailure) > c.resetTimeout {
			c.state = StateHalfOpen
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

	// Выполняем проверку здоровья
	err := c.checker.CheckHealth(ctx)

	if err != nil {
		c.failureCount++
		c.lastFailure = time.Now()

		if c.failureCount >= c.maxFailures {
			c.state = StateOpen
		}
		return err
	}

	// Успешная проверка
	c.failureCount = 0
	c.state = StateClosed
	return nil
}

// GetStats возвращает статистику
func (c *CircuitBreakerHealthChecker) GetStats() PoolStats {
	return c.checker.GetStats()
}

// IsHealthy возвращает состояние здоровья с учетом circuit breaker
func (c *CircuitBreakerHealthChecker) IsHealthy() bool {
	return c.checker.IsHealthy() && c.state != StateOpen
}

// LastCheckTime возвращает время последней проверки
func (c *CircuitBreakerHealthChecker) LastCheckTime() time.Time {
	return c.checker.LastCheckTime()
}

// GetState возвращает текущее состояние circuit breaker
func (c *CircuitBreakerHealthChecker) GetState() CircuitBreakerState {
	return c.state
}

// GetFailureCount возвращает количество неудачных проверок
func (c *CircuitBreakerHealthChecker) GetFailureCount() int {
	return c.failureCount
}
