package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseConnection определяет интерфейс для работы с базой данных
type DatabaseConnection interface {
	// Lifecycle management
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool

	// Health monitoring
	Health(ctx context.Context) error
	Stats() PoolStats

	// Query execution
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row

	// Transaction support
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PostgresPool реализует высокопроизводительный PostgreSQL connection pool
type PostgresPool struct {
	pool     *pgxpool.Pool
	config   *PostgresConfig
	logger   *slog.Logger
	metrics  *PoolMetrics
	health   HealthChecker
	isClosed atomic.Bool
	closeCh  chan struct{}
}

// NewPostgresPool создает новый PostgreSQL connection pool
func NewPostgresPool(config *PostgresConfig, logger *slog.Logger) *PostgresPool {
	if logger == nil {
		logger = slog.Default()
	}

	pool := &PostgresPool{
		config:   config,
		logger:   logger,
		metrics:  NewPoolMetrics(),
		isClosed: atomic.Bool{},
		closeCh:  make(chan struct{}),
	}

	// Создаем health checker
	pool.health = NewHealthChecker(pool)

	return pool
}

// Connect устанавливает соединение с базой данных
func (p *PostgresPool) Connect(ctx context.Context) error {
	if p.isClosed.Load() {
		return ErrConnectionClosed
	}

	// Проверяем конфигурацию
	if err := p.config.Validate(); err != nil {
		p.logger.Error("Invalid database configuration", "error", err)
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	p.logger.Info("Connecting to PostgreSQL",
		"host", p.config.Host,
		"port", p.config.Port,
		"database", p.config.Database,
		"user", p.config.User,
		"ssl_mode", p.config.SSLMode,
		"max_conns", p.config.MaxConns,
		"min_conns", p.config.MinConns)

	// Создаем конфигурацию pgxpool
	poolConfig, err := pgxpool.ParseConfig(p.config.DSN())
	if err != nil {
		p.logger.Error("Failed to parse database DSN", "error", err)
		p.metrics.RecordConnectionError()
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Настраиваем параметры pool
	poolConfig.MaxConns = p.config.MaxConns
	poolConfig.MinConns = p.config.MinConns
	poolConfig.MaxConnLifetime = p.config.MaxConnLifetime
	poolConfig.MaxConnIdleTime = p.config.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = p.config.HealthCheckPeriod

	// Устанавливаем таймаут подключения
	connectCtx, cancel := context.WithTimeout(ctx, p.config.ConnectTimeout)
	defer cancel()

	start := time.Now()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		p.logger.Error("Failed to create connection pool", "error", err)
		p.metrics.RecordConnectionError()
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Тестируем соединение
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		p.logger.Error("Failed to ping database", "error", err)
		p.metrics.RecordConnectionError()
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	p.pool = pool
	connectionTime := time.Since(start)
	p.metrics.RecordConnectionWait(connectionTime)
	p.metrics.RecordSuccessfulConnection()

	p.logger.Info("Successfully connected to PostgreSQL",
		"connection_time", connectionTime,
		"max_conns", p.config.MaxConns,
		"min_conns", p.config.MinConns)

	// Запускаем периодические health checks
	if healthChecker, ok := p.health.(*DefaultHealthChecker); ok {
		periodicChecker := NewPeriodicHealthChecker(healthChecker, p.config.HealthCheckPeriod)
		go periodicChecker.Start(ctx)
	}

	return nil
}

// Disconnect закрывает соединение с базой данных
func (p *PostgresPool) Disconnect(ctx context.Context) error {
	if p.pool == nil {
		return nil
	}

	if p.isClosed.Load() {
		return ErrConnectionClosed
	}

	p.logger.Info("Disconnecting from PostgreSQL")

	// Закрываем канал для остановки health checks
	select {
	case p.closeCh <- struct{}{}:
	default:
		// Channel уже закрыт
	}

	// Закрываем pool
	p.pool.Close()

	p.isClosed.Store(true)
	p.logger.Info("Successfully disconnected from PostgreSQL")

	return nil
}

// IsConnected проверяет состояние соединения
func (p *PostgresPool) IsConnected() bool {
	if p.isClosed.Load() || p.pool == nil {
		return false
	}

	// Проверяем состояние pool
	stats := p.pool.Stat()
	return stats.TotalConns() > 0
}

// Health выполняет проверку здоровья базы данных
func (p *PostgresPool) Health(ctx context.Context) error {
	if p.isClosed.Load() {
		return ErrConnectionClosed
	}

	if p.pool == nil {
		return ErrNotConnected
	}

	return p.health.CheckHealth(ctx)
}

// Stats возвращает статистику connection pool
func (p *PostgresPool) Stats() PoolStats {
	if p.pool == nil {
		return PoolStats{}
	}

	// Обновляем статистику из pgxpool
	poolStats := p.pool.Stat()
	totalConns := int64(poolStats.TotalConns())
	acquireCount := int64(poolStats.AcquireCount())
	p.metrics.UpdateConnectionStats(
		int32(acquireCount),
		int32(totalConns-acquireCount),
		totalConns,
	)

	return p.metrics.Snapshot()
}

// Exec выполняет SQL команду без возврата результатов
func (p *PostgresPool) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if p.pool == nil {
		return pgconn.CommandTag{}, ErrNotConnected
	}

	start := time.Now()
	tag, err := p.pool.Exec(ctx, sql, args...)
	duration := time.Since(start)

	if err != nil {
		p.metrics.RecordQueryError()
		p.logger.Error("Query execution failed",
			"sql", sql,
			"duration", duration,
			"error", err)
		return tag, err
	}

	p.metrics.RecordQueryExecution(duration)
	p.logger.Debug("Query executed successfully",
		"sql", sql,
		"duration", duration,
		"rows_affected", tag.RowsAffected())

	return tag, nil
}

// Query выполняет SQL запрос и возвращает результаты
func (p *PostgresPool) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if p.pool == nil {
		return nil, ErrNotConnected
	}

	start := time.Now()
	rows, err := p.pool.Query(ctx, sql, args...)
	duration := time.Since(start)

	if err != nil {
		p.metrics.RecordQueryError()
		p.logger.Error("Query execution failed",
			"sql", sql,
			"duration", duration,
			"error", err)
		return nil, err
	}

	p.metrics.RecordQueryExecution(duration)
	p.logger.Debug("Query executed successfully",
		"sql", sql,
		"duration", duration)

	return rows, nil
}

// QueryRow выполняет SQL запрос и возвращает одну строку
func (p *PostgresPool) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if p.pool == nil {
		return &errorRow{err: ErrNotConnected}
	}

	start := time.Now()
	row := p.pool.QueryRow(ctx, sql, args...)
	duration := time.Since(start)

	p.metrics.RecordQueryExecution(duration)
	p.logger.Debug("Query row executed",
		"sql", sql,
		"duration", duration)

	return row
}

// Begin начинает новую транзакцию
func (p *PostgresPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if p.pool == nil {
		return nil, ErrNotConnected
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		p.metrics.RecordQueryError()
		p.logger.Error("Failed to begin transaction", "error", err)
		return nil, err
	}

	p.logger.Debug("Transaction started")
	return tx, nil
}

// PrepareStatement подготавливает SQL statement для повторного использования
func (p *PostgresPool) PrepareStatement(ctx context.Context, name, sql string) error {
	if p.pool == nil {
		return ErrNotConnected
	}

	// Получаем соединение из пула
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		p.logger.Error("Failed to acquire connection for statement preparation",
			"name", name,
			"error", err)
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer conn.Release()

	// Подготавливаем statement на соединении
	_, err = conn.Exec(ctx, "PREPARE "+name+" AS "+sql)
	if err != nil {
		p.logger.Error("Failed to prepare statement",
			"name", name,
			"sql", sql,
			"error", err)
		return fmt.Errorf("%w: %v", ErrPreparedStatementFailed, err)
	}

	p.logger.Info("Prepared statement", "name", name)
	return nil
}

// Close закрывает connection pool
func (p *PostgresPool) Close() error {
	return p.Disconnect(context.Background())
}

// GetConfig возвращает текущую конфигурацию
func (p *PostgresPool) GetConfig() *PostgresConfig {
	return p.config
}

// GetMetrics возвращает метрики pool
func (p *PostgresPool) GetMetrics() *PoolMetrics {
	return p.metrics
}

// GetHealthChecker возвращает health checker
func (p *PostgresPool) GetHealthChecker() HealthChecker {
	return p.health
}

// Pool returns the underlying pgxpool.Pool for advanced operations
// This is useful when you need direct access to pgxpool features
func (p *PostgresPool) Pool() *pgxpool.Pool {
	return p.pool
}

// errorRow implements pgx.Row for error cases
type errorRow struct {
	err error
}

func (r *errorRow) Scan(dest ...interface{}) error {
	return r.err
}
