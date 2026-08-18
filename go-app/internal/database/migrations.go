package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/ipiton/AMP/internal/database/postgres"
)

// migrationLockID is the Postgres advisory-lock key goose uses to serialize
// migrations across replicas. It's a fixed, arbitrary int64 distinct from
// goose's own lock.DefaultLockID so this doesn't collide with any other
// goose-based locking elsewhere in the process (see
// internal/infrastructure/migrations for a separate, unlocked manager).
const migrationLockID int64 = 8823647501982361

// migrationLockPeriodSeconds/migrationLockFailureThreshold bound how long
// RunMigrations will block waiting for another replica to finish migrating
// a fresh database before giving up. period * failureThreshold = the total
// wait budget (here: 5s * 36 = 180s / 3min). goose's lock.WithLockTimeout
// takes the period in whole seconds (uint64), not a time.Duration.
const (
	migrationLockPeriodSeconds      uint64 = 5
	migrationLockFailureThreshold   uint64 = 36
	migrationUnlockPeriodSeconds    uint64 = 2
	migrationUnlockFailureThreshold uint64 = 30
)

// RunMigrations выполняет все pending миграции базы данных
func RunMigrations(ctx context.Context, pool postgres.DatabaseConnection, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("Starting database migrations...")

	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	// Для goose нужен *sql.DB, поэтому получаем его из пула
	// Поскольку мы используем pgx/v5, нужно создать *sql.DB wrapper
	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Session-level pg_advisory_lock so N replicas starting concurrently
	// against a fresh database don't race to create goose's version table
	// or apply the same migration twice. The second (and later) replica
	// blocks here until the first releases the lock, then re-checks
	// pending migrations under the lock and no-ops (already applied).
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(migrationLockID),
		lock.WithLockTimeout(migrationLockPeriodSeconds, migrationLockFailureThreshold),
		lock.WithUnlockTimeout(migrationUnlockPeriodSeconds, migrationUnlockFailureThreshold),
	)
	if err != nil {
		return fmt.Errorf("failed to configure migration lock: %w", err)
	}
	lockWaitBudget := time.Duration(migrationLockPeriodSeconds) * time.Second * time.Duration(migrationLockFailureThreshold)

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		os.DirFS(migrationsDir),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	logger.Info("Acquiring database migration lock (blocks if another replica is migrating)...",
		"lock_id", migrationLockID,
		"timeout", lockWaitBudget,
	)

	if _, err := provider.Up(ctx); err != nil {
		// go-retry (used internally by lock.SessionLocker) returns the plain
		// "failed to acquire lock" error once its retry budget is exhausted,
		// or ctx.Err() if the caller's context was canceled/deadlined first —
		// neither is context.DeadlineExceeded, so match on message content.
		if strings.Contains(err.Error(), "acquire lock") || ctx.Err() != nil {
			return fmt.Errorf("timed out waiting for migration lock held by another replica after %s: %w",
				lockWaitBudget, err)
		}
		logger.Error("Failed to run migrations", "error", err)
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Info("✅ Database migrations completed successfully")
	return nil
}

// RunMigrationsDown откатывает миграции на указанное количество шагов
func RunMigrationsDown(ctx context.Context, pool postgres.DatabaseConnection, steps int, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("Starting database migration rollback", "steps", steps)

	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.SetDialect("postgres"); err != nil {
		logger.Error("Failed to set goose dialect", "error", err)
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	if err := goose.DownTo(db, migrationsDir, int64(steps)); err != nil {
		logger.Error("Failed to rollback migrations", "error", err, "steps", steps)
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	logger.Info("✅ Database migration rollback completed", "steps", steps)
	return nil
}

// GetMigrationStatus возвращает статус миграций
func GetMigrationStatus(ctx context.Context, pool postgres.DatabaseConnection, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.SetDialect("postgres"); err != nil {
		logger.Error("Failed to set goose dialect", "error", err)
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	if err := goose.Status(db, migrationsDir); err != nil {
		logger.Error("Failed to get migration status", "error", err)
		return fmt.Errorf("failed to get migration status: %w", err)
	}

	return nil
}

// createSQLDBFromPool создает *sql.DB из нашего connection pool
// Это необходимо для совместимости с goose, который работает с database/sql
func createSQLDBFromPool(pool postgres.DatabaseConnection) (*sql.DB, error) {
	// Проверяем, что у нас есть доступ к pgxpool через интерфейс
	// Для простоты будем использовать DSN из конфигурации
	// В реальном приложении может потребоваться более сложная логика

	// Получаем конфигурацию из пула
	if pgPool, ok := pool.(*postgres.PostgresPool); ok {
		config := pgPool.GetConfig()

		// Создаем стандартное SQL подключение
		db, err := sql.Open("pgx", config.DSN())
		if err != nil {
			return nil, fmt.Errorf("failed to open SQL DB: %w", err)
		}

		// Настраиваем параметры подключения
		db.SetMaxOpenConns(int(config.MaxConns))
		db.SetMaxIdleConns(int(config.MinConns))
		db.SetConnMaxLifetime(config.MaxConnLifetime)
		db.SetConnMaxIdleTime(config.MaxConnIdleTime)

		return db, nil
	}

	return nil, fmt.Errorf("unsupported pool type")
}

func resolveMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("migrations"),
		filepath.Join("go-app", "migrations"),
	}

	if _, filename, _, ok := runtime.Caller(0); ok {
		baseDir := filepath.Dir(filename)
		candidates = append(candidates, filepath.Clean(filepath.Join(baseDir, "..", "..", "migrations")))
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("migrations directory not found; checked: %v", candidates)
}
