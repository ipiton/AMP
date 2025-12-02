package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/pressly/goose/v3"

	"github.com/ipiton/AMP/internal/database/postgres"
)

// RunMigrations выполняет все pending миграции базы данных
func RunMigrations(ctx context.Context, pool postgres.DatabaseConnection, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("Starting database migrations...")

	// Получаем путь к директории миграций относительно корня проекта
	migrationsDir := filepath.Join("migrations")

	// Для goose нужен *sql.DB, поэтому получаем его из пула
	// Поскольку мы используем pgx/v5, нужно создать *sql.DB wrapper
	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer db.Close()

	// Устанавливаем диалект PostgreSQL для goose
	if err := goose.SetDialect("postgres"); err != nil {
		logger.Error("Failed to set goose dialect", "error", err)
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	// Выполняем миграции
	if err := goose.Up(db, migrationsDir); err != nil {
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

	migrationsDir := filepath.Join("migrations")

	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer db.Close()

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

	migrationsDir := filepath.Join("migrations")

	db, err := createSQLDBFromPool(pool)
	if err != nil {
		logger.Error("Failed to create SQL DB from pool", "error", err)
		return fmt.Errorf("failed to create SQL DB: %w", err)
	}
	defer db.Close()

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
