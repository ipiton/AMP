package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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
	// pool is swapped atomically by Reload; every read goes through
	// livePool() so an in-flight caller either sees the old pool or the new
	// one, never a torn value.
	pool atomic.Pointer[pgxpool.Pool]

	// config is replaced (never mutated in place) by Reload, so a concurrent
	// GetConfig()/Connect() reader always sees one coherent snapshot.
	config atomic.Pointer[PostgresConfig]

	logger   *slog.Logger
	metrics  *PoolMetrics
	health   HealthChecker
	isClosed atomic.Bool
	closeCh  chan struct{}

	// poolShared records that a caller took the raw *pgxpool.Pool out of
	// this wrapper via SharePool() and is holding it for the rest of the
	// process's life. Reload refuses to swap while that is true — see
	// SharePool and Reload for why.
	poolShared atomic.Bool

	// reloadMu serialises Reload against itself (Reload does read-modify-write
	// on pool+config, which atomics alone cannot make atomic as a pair).
	reloadMu sync.Mutex

	// drainGrace is how long Reload keeps the replaced pool open before
	// closing it, so queries already in flight on it can finish. Overridable
	// for tests; DefaultPoolDrainGrace in production.
	drainGrace time.Duration
}

// DefaultPoolDrainGrace is how long a pool replaced by Reload stays open
// before it is closed, letting in-flight queries finish on it.
const DefaultPoolDrainGrace = 5 * time.Second

// NewPostgresPool создает новый PostgreSQL connection pool
func NewPostgresPool(config *PostgresConfig, logger *slog.Logger) *PostgresPool {
	if logger == nil {
		logger = slog.Default()
	}

	pool := &PostgresPool{
		logger:     logger,
		metrics:    NewPoolMetrics(),
		isClosed:   atomic.Bool{},
		closeCh:    make(chan struct{}),
		drainGrace: DefaultPoolDrainGrace,
	}
	pool.config.Store(config)

	// Создаем health checker
	pool.health = NewHealthChecker(pool)

	return pool
}

// livePool returns the currently active pgxpool, or nil when not connected.
func (p *PostgresPool) livePool() *pgxpool.Pool {
	return p.pool.Load()
}

// currentConfig returns the active configuration snapshot (never nil after
// NewPostgresPool with a non-nil config).
func (p *PostgresPool) currentConfig() *PostgresConfig {
	return p.config.Load()
}

// SetDrainGrace overrides how long Reload keeps a replaced pool open before
// closing it. Intended for tests; production uses DefaultPoolDrainGrace.
func (p *PostgresPool) SetDrainGrace(d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.drainGrace = d
}

// Connect устанавливает соединение с базой данных
func (p *PostgresPool) Connect(ctx context.Context) error {
	if p.isClosed.Load() {
		return ErrConnectionClosed
	}

	cfg := p.currentConfig()

	pool, connectionTime, err := p.buildPool(ctx, cfg, "Connecting to PostgreSQL")
	if err != nil {
		return err
	}

	p.pool.Store(pool)
	p.metrics.RecordConnectionWait(connectionTime)
	p.metrics.RecordSuccessfulConnection()

	p.logger.Info("Successfully connected to PostgreSQL",
		"connection_time", connectionTime,
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns)

	// Запускаем периодические health checks
	if healthChecker, ok := p.health.(*DefaultHealthChecker); ok {
		periodicChecker := NewPeriodicHealthChecker(healthChecker, cfg.HealthCheckPeriod)
		go periodicChecker.Start(ctx)
	}

	return nil
}

// buildPool validates cfg, creates a pgxpool from it and verifies the
// connection with Ping before handing it back. Shared by Connect and Reload
// so a reloaded pool is verified exactly the way a freshly connected one is —
// a new pool is never published unless it answered a Ping.
func (p *PostgresPool) buildPool(
	ctx context.Context,
	cfg *PostgresConfig,
	logMessage string,
) (*pgxpool.Pool, time.Duration, error) {
	if cfg == nil {
		return nil, 0, fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}

	// Проверяем конфигурацию
	if err := cfg.Validate(); err != nil {
		p.logger.Error("Invalid database configuration", "error", err)
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	p.logger.Info(logMessage,
		"host", cfg.Host,
		"port", cfg.Port,
		"database", cfg.Database,
		"user", cfg.User,
		"ssl_mode", cfg.SSLMode,
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns)

	// Создаем конфигурацию pgxpool
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		p.logger.Error("Failed to parse database DSN", "error", err)
		p.metrics.RecordConnectionError()
		return nil, 0, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Настраиваем параметры pool
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod

	// Устанавливаем таймаут подключения
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	start := time.Now()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		p.logger.Error("Failed to create connection pool", "error", err)
		p.metrics.RecordConnectionError()
		return nil, 0, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Тестируем соединение
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		p.logger.Error("Failed to ping database", "error", err)
		p.metrics.RecordConnectionError()
		return nil, 0, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	return pool, time.Since(start), nil
}

// Reload rebuilds the connection pool from cfg and swaps it in atomically
// (INF-A slice 1, config hot reload).
//
// Sequence: build the new pool -> Ping-verify it -> atomic swap -> drain the
// replaced pool for drainGrace, then close it. Queries already in flight on
// the replaced pool keep running on it: pgxpool.Close waits for acquired
// connections to be released, and the grace window means a caller that
// already holds a connection is never cut off mid-statement. Callers that
// take a fresh connection after the swap get the new pool.
//
// Not restarted by a reload: the periodic health checker started by Connect
// (review M7). It follows livePool(), so it keeps checking the CURRENT pool and
// never breaks — but a changed HealthCheckPeriod keeps the old interval until
// the process restarts.
//
// Reload REFUSES (ErrPoolHandleShared) when the raw *pgxpool.Pool was handed
// out through SharePool(): those holders captured the pointer and would keep
// using the replaced pool after this closed it. Making them follow the swap
// is a separate refactor (they take *pgxpool.Pool in their constructors);
// until then, refusing loudly is the only honest answer — see the caller in
// internal/config/reloadable_database.go, which turns the refusal into a
// restart-required warning.
func (p *PostgresPool) Reload(ctx context.Context, cfg *PostgresConfig) error {
	if p.isClosed.Load() {
		return ErrConnectionClosed
	}
	if p.poolShared.Load() {
		return ErrPoolHandleShared
	}

	p.reloadMu.Lock()
	defer p.reloadMu.Unlock()

	// Re-check under the lock: SharePool may have been called while we waited.
	if p.poolShared.Load() {
		return ErrPoolHandleShared
	}

	newPool, connectionTime, err := p.buildPool(ctx, cfg, "Rebuilding PostgreSQL pool for config reload")
	if err != nil {
		return err
	}

	oldPool := p.pool.Swap(newPool)
	p.config.Store(cfg)
	p.metrics.RecordConnectionWait(connectionTime)
	p.metrics.RecordSuccessfulConnection()

	p.logger.Info("PostgreSQL pool reloaded",
		"connection_time", connectionTime,
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns,
		"drain_grace", p.drainGrace)

	if oldPool != nil {
		grace := p.drainGrace
		logger := p.logger
		go func() {
			if grace > 0 {
				time.Sleep(grace)
			}
			// Close blocks until every acquired connection is returned, which
			// is what makes the drain graceful rather than a deadline. The
			// flip side (review M2): a connection a caller never releases
			// pins this goroutine and the old pool for as long as it is held.
			// Bounding it would mean cutting off a live query, so the leak is
			// preferred and logged either side instead.
			logger.Info("closing the previous PostgreSQL pool", "drain_grace", grace)
			oldPool.Close()
			logger.Info("previous PostgreSQL pool drained and closed", "drain_grace", grace)
		}()
	}

	return nil
}

// Disconnect закрывает соединение с базой данных
func (p *PostgresPool) Disconnect(ctx context.Context) error {
	pool := p.livePool()
	if pool == nil {
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
	pool.Close()

	p.isClosed.Store(true)
	p.logger.Info("Successfully disconnected from PostgreSQL")

	return nil
}

// IsConnected проверяет состояние соединения
func (p *PostgresPool) IsConnected() bool {
	pool := p.livePool()
	if p.isClosed.Load() || pool == nil {
		return false
	}

	// Проверяем состояние pool
	stats := pool.Stat()
	return stats.TotalConns() > 0
}

// Health выполняет проверку здоровья базы данных
func (p *PostgresPool) Health(ctx context.Context) error {
	if p.isClosed.Load() {
		return ErrConnectionClosed
	}

	if p.livePool() == nil {
		return ErrNotConnected
	}

	return p.health.CheckHealth(ctx)
}

// Stats возвращает статистику connection pool
func (p *PostgresPool) Stats() PoolStats {
	pool := p.livePool()
	if pool == nil {
		return PoolStats{}
	}

	// Обновляем статистику из pgxpool
	poolStats := pool.Stat()
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
	pool := p.livePool()
	if pool == nil {
		return pgconn.CommandTag{}, ErrNotConnected
	}

	start := time.Now()
	tag, err := pool.Exec(ctx, sql, args...)
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
	pool := p.livePool()
	if pool == nil {
		return nil, ErrNotConnected
	}

	start := time.Now()
	rows, err := pool.Query(ctx, sql, args...)
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
	pool := p.livePool()
	if pool == nil {
		return &errorRow{err: ErrNotConnected}
	}

	start := time.Now()
	row := pool.QueryRow(ctx, sql, args...)
	duration := time.Since(start)

	p.metrics.RecordQueryExecution(duration)
	p.logger.Debug("Query row executed",
		"sql", sql,
		"duration", duration)

	return row
}

// Begin начинает новую транзакцию
func (p *PostgresPool) Begin(ctx context.Context) (pgx.Tx, error) {
	pool := p.livePool()
	if pool == nil {
		return nil, ErrNotConnected
	}

	tx, err := pool.Begin(ctx)
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
	pool := p.livePool()
	if pool == nil {
		return ErrNotConnected
	}

	// Получаем соединение из пула
	conn, err := pool.Acquire(ctx)
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
//
// The returned snapshot must be treated as read-only: Reload replaces the
// pointer rather than mutating it, so a caller that edits it in place would
// corrupt a config another goroutine is already reading.
func (p *PostgresPool) GetConfig() *PostgresConfig {
	return p.currentConfig()
}

// GetMetrics возвращает метрики pool
func (p *PostgresPool) GetMetrics() *PoolMetrics {
	return p.metrics
}

// GetHealthChecker возвращает health checker
func (p *PostgresPool) GetHealthChecker() HealthChecker {
	return p.health
}

// Pool returns the underlying pgxpool.Pool for advanced operations.
//
// Use this for a SHORT-LIVED borrow (a nil check, one query, a stats read).
// A caller that stores the returned pointer for the process's lifetime must
// use SharePool() instead, so Reload knows it can no longer replace the pool
// from under that holder.
func (p *PostgresPool) Pool() *pgxpool.Pool {
	return p.livePool()
}

// SharePool returns the underlying pgxpool.Pool AND records that the caller
// is keeping it: from this point on Reload refuses to swap the pool
// (ErrPoolHandleShared), because the holder captured a raw pointer and would
// keep issuing queries on a pool that Reload had closed — or, worse, on a
// pool pointing at the previous database after a host change, splitting
// writes across two servers.
//
// Every long-lived consumer in ServiceRegistry (the storage adapter, the
// silence repository, the investigation repository, the investigation tools
// *sql.DB) takes its handle this way, which is why database hot reload
// reports "restart required" in the standard profile today. Removing that
// limitation means giving those consumers a handle that follows the swap;
// tracked as FU-DB-LIVE-POOL-HANDLE.
func (p *PostgresPool) SharePool() *pgxpool.Pool {
	// Under reloadMu (review M1): without it a SharePool racing a Reload could
	// hand out the very pool that Reload is about to close. Reload's own
	// double-check narrows that window but does not close it, and this flag is
	// advertised as THE safety mechanism, so it has to be airtight rather than
	// merely unreachable-in-practice.
	p.reloadMu.Lock()
	defer p.reloadMu.Unlock()

	p.poolShared.Store(true)
	return p.livePool()
}

// IsPoolShared reports whether a caller has taken a long-lived handle via
// SharePool(), i.e. whether Reload will refuse to swap the pool.
func (p *PostgresPool) IsPoolShared() bool {
	return p.poolShared.Load()
}

// errorRow implements pgx.Row for error cases
type errorRow struct {
	err error
}

func (r *errorRow) Scan(dest ...interface{}) error {
	return r.err
}
