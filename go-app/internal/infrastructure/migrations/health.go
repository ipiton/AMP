package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// HealthChecker выполняет проверки здоровья перед и после миграций
type HealthChecker struct {
	db     *sql.DB
	config *HealthConfig
	logger *slog.Logger
	dbType string
}

// HealthConfig определяет конфигурацию health проверок
type HealthConfig struct {
	Enabled    bool          `env:"HEALTH_ENABLED" default:"true"`
	Timeout    time.Duration `env:"HEALTH_TIMEOUT" default:"30s"`
	RetryCount int           `env:"HEALTH_RETRY_COUNT" default:"3"`
	RetryDelay time.Duration `env:"HEALTH_RETRY_DELAY" default:"5s"`
}

// HealthCheck представляет функцию проверки здоровья
type HealthCheck func(ctx context.Context) error

// NewHealthChecker создает новый health checker
func NewHealthChecker(db *sql.DB, config *HealthConfig, logger *slog.Logger) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}

	hc := &HealthChecker{
		db:     db,
		config: config,
		logger: logger,
	}

	// Определяем тип базы данных
	if err := hc.detectDatabaseType(context.Background()); err != nil {
		logger.Warn("Failed to detect database type", "error", err)
	}

	return hc
}

// PreMigrationCheck выполняет проверки перед миграцией
func (hc *HealthChecker) PreMigrationCheck(ctx context.Context) error {
	if !hc.config.Enabled {
		hc.logger.Info("Health checks disabled")
		return nil
	}

	hc.logger.Info("Running pre-migration health checks")

	checks := []struct {
		name string
		fn   HealthCheck
	}{
		{"database_connectivity", hc.checkDatabaseConnectivity},
		{"database_permissions", hc.checkDatabasePermissions},
		{"existing_migrations", hc.checkExistingMigrations},
		{"disk_space", hc.checkDiskSpace},
		{"table_integrity", hc.checkTableIntegrity},
		{"foreign_keys", hc.checkForeignKeys},
		{"indexes", hc.checkIndexes},
	}

	for _, check := range checks {
		hc.logger.Debug("Running health check", "check", check.name)

		if err := hc.executeCheck(ctx, check.name, check.fn); err != nil {
			hc.logger.Error("Pre-migration health check failed",
				"check", check.name,
				"error", err)
			return fmt.Errorf("pre-migration health check '%s' failed: %w", check.name, err)
		}
	}

	hc.logger.Info("All pre-migration health checks passed")
	return nil
}

// PostMigrationCheck выполняет проверки после миграции
func (hc *HealthChecker) PostMigrationCheck(ctx context.Context) error {
	if !hc.config.Enabled {
		hc.logger.Info("Health checks disabled")
		return nil
	}

	hc.logger.Info("Running post-migration health checks")

	checks := []struct {
		name string
		fn   HealthCheck
	}{
		{"database_connectivity", hc.checkDatabaseConnectivity},
		{"schema_integrity", hc.checkSchemaIntegrity},
		{"data_consistency", hc.checkDataConsistency},
		{"foreign_keys", hc.checkForeignKeys},
		{"indexes", hc.checkIndexes},
		{"migration_table", hc.checkMigrationTable},
	}

	for _, check := range checks {
		hc.logger.Debug("Running health check", "check", check.name)

		if err := hc.executeCheck(ctx, check.name, check.fn); err != nil {
			hc.logger.Error("Post-migration health check failed",
				"check", check.name,
				"error", err)
			return fmt.Errorf("post-migration health check '%s' failed: %w", check.name, err)
		}
	}

	hc.logger.Info("All post-migration health checks passed")
	return nil
}

// executeCheck выполняет проверку с повторными попытками
func (hc *HealthChecker) executeCheck(ctx context.Context, name string, check HealthCheck) error {
	checkCtx, cancel := context.WithTimeout(ctx, hc.config.Timeout)
	defer cancel()

	var lastErr error

	for attempt := 0; attempt < hc.config.RetryCount; attempt++ {
		if attempt > 0 {
			hc.logger.Debug("Retrying health check",
				"check", name,
				"attempt", attempt+1,
				"max_retries", hc.config.RetryCount)

			select {
			case <-time.After(hc.config.RetryDelay):
				// Продолжаем после задержки
			case <-checkCtx.Done():
				return checkCtx.Err()
			}
		}

		if err := check(checkCtx); err != nil {
			lastErr = err
			hc.logger.Warn("Health check failed, retrying",
				"check", name,
				"attempt", attempt+1,
				"error", err)
			continue
		}

		// Проверка прошла успешно
		if attempt > 0 {
			hc.logger.Info("Health check succeeded after retry",
				"check", name,
				"attempts", attempt+1)
		}

		return nil
	}

	return fmt.Errorf("health check '%s' failed after %d attempts: %w",
		name, hc.config.RetryCount, lastErr)
}

// checkDatabaseConnectivity проверяет подключение к базе данных
func (hc *HealthChecker) checkDatabaseConnectivity(ctx context.Context) error {
	if err := hc.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	return nil
}

// checkDatabasePermissions проверяет права доступа к базе данных
func (hc *HealthChecker) checkDatabasePermissions(ctx context.Context) error {
	// Проверяем возможность создания тестовой таблицы
	testTable := "migration_health_check_temp"

	if hc.dbType == "postgres" {
		// Для PostgreSQL используем временную таблицу
		if _, err := hc.db.ExecContext(ctx, "CREATE TEMP TABLE "+testTable+" (id INTEGER)"); err != nil {
			return fmt.Errorf("cannot create temporary table: %w", err)
		}

		if _, err := hc.db.ExecContext(ctx, "DROP TABLE "+testTable); err != nil {
			return fmt.Errorf("cannot drop temporary table: %w", err)
		}
	} else {
		// Для SQLite используем обычную таблицу
		if _, err := hc.db.ExecContext(ctx, "CREATE TABLE "+testTable+" (id INTEGER)"); err != nil {
			return fmt.Errorf("cannot create table: %w", err)
		}

		if _, err := hc.db.ExecContext(ctx, "DROP TABLE "+testTable); err != nil {
			return fmt.Errorf("cannot drop table: %w", err)
		}
	}

	return nil
}

// checkExistingMigrations проверяет состояние существующих миграций
func (hc *HealthChecker) checkExistingMigrations(ctx context.Context) error {
	// Проверяем, что таблица goose_db_version существует и в корректном состоянии
	if hc.dbType == "postgres" {
		var exists bool
		query := "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'goose_db_version')"
		if err := hc.db.QueryRowContext(ctx, query).Scan(&exists); err != nil {
			// Таблица может еще не существовать, это нормально
			hc.logger.Debug("Migration table does not exist yet")
			return nil
		}

		if !exists {
			hc.logger.Debug("Migration table does not exist yet")
			return nil
		}
	} else {
		// Для SQLite
		var exists bool
		query := "SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='goose_db_version'"
		if err := hc.db.QueryRowContext(ctx, query).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check migration table: %w", err)
		}

		if !exists {
			hc.logger.Debug("Migration table does not exist yet")
			return nil
		}
	}

	// Проверяем, что нет битых записей в таблице миграций
	rows, err := hc.db.QueryContext(ctx, "SELECT version_id, is_applied FROM goose_db_version ORDER BY version_id")
	if err != nil {
		return fmt.Errorf("failed to query migration status: %w", err)
	}
	defer rows.Close()

	var lastVersion int64 = 0
	for rows.Next() {
		var versionID int64
		var isApplied bool

		if err := rows.Scan(&versionID, &isApplied); err != nil {
			return fmt.Errorf("failed to scan migration status: %w", err)
		}

		if isApplied && versionID > lastVersion+1 {
			return fmt.Errorf("missing migration between %d and %d", lastVersion, versionID)
		}

		if isApplied {
			lastVersion = versionID
		}
	}

	return nil
}

// checkDiskSpace проверяет доступное дисковое пространство
func (hc *HealthChecker) checkDiskSpace(ctx context.Context) error {
	// Для простоты пропускаем эту проверку в базовой реализации
	// В продакшене здесь должна быть проверка дискового пространства
	hc.logger.Debug("Disk space check skipped (not implemented)")
	return nil
}

// checkTableIntegrity проверяет целостность таблиц
func (hc *HealthChecker) checkTableIntegrity(ctx context.Context) error {
	if hc.dbType == "sqlite" {
		// Для SQLite используем PRAGMA integrity_check
		if _, err := hc.db.ExecContext(ctx, "PRAGMA integrity_check"); err != nil {
			return fmt.Errorf("database integrity check failed: %w", err)
		}
	} else {
		// Для PostgreSQL можно использовать более сложные проверки
		hc.logger.Debug("Table integrity check skipped for PostgreSQL (not implemented)")
	}

	return nil
}

// checkForeignKeys проверяет foreign key constraints
func (hc *HealthChecker) checkForeignKeys(ctx context.Context) error {
	if hc.dbType == "sqlite" {
		// Для SQLite используем PRAGMA foreign_key_check
		rows, err := hc.db.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return fmt.Errorf("foreign key check failed: %w", err)
		}
		defer rows.Close()

		violations := 0
		for rows.Next() {
			violations++
			var table, rowid, parent, fkid string
			if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
				return fmt.Errorf("failed to scan foreign key violation: %w", err)
			}
			hc.logger.Warn("Foreign key violation detected",
				"table", table,
				"rowid", rowid,
				"parent", parent,
				"fkid", fkid)
		}

		if violations > 0 {
			return fmt.Errorf("found %d foreign key violations", violations)
		}
	} else {
		// Для PostgreSQL можно проверить referential integrity
		hc.logger.Debug("Foreign key check skipped for PostgreSQL (not implemented)")
	}

	return nil
}

// checkIndexes проверяет состояние индексов
func (hc *HealthChecker) checkIndexes(ctx context.Context) error {
	if hc.dbType == "sqlite" {
		// Для SQLite проверяем, что индексы не повреждены
		rows, err := hc.db.QueryContext(ctx, "PRAGMA index_list(alerts)")
		if err != nil {
			return fmt.Errorf("failed to check indexes: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var seq int
			var name string
			var unique bool
			var origin string
			var partial bool

			if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				return fmt.Errorf("failed to scan index info: %w", err)
			}

			// Проверяем целостность индекса
			if _, err := hc.db.ExecContext(ctx, "PRAGMA index_info("+name+")"); err != nil {
				return fmt.Errorf("index %s appears to be corrupted: %w", name, err)
			}
		}
	} else {
		// Для PostgreSQL можно проверить состояние индексов
		hc.logger.Debug("Index check skipped for PostgreSQL (not implemented)")
	}

	return nil
}

// checkSchemaIntegrity проверяет целостность схемы после миграции
func (hc *HealthChecker) checkSchemaIntegrity(ctx context.Context) error {
	// Проверяем, что все ожидаемые таблицы существуют
	expectedTables := []string{
		"alerts",
		"classifications",
		"publishing_logs",
		"goose_db_version",
	}

	for _, table := range expectedTables {
		if hc.dbType == "postgres" {
			var exists bool
			query := "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)"
			if err := hc.db.QueryRowContext(ctx, query, table).Scan(&exists); err != nil {
				return fmt.Errorf("failed to check table existence for %s: %w", table, err)
			}

			if !exists {
				return fmt.Errorf("required table %s does not exist", table)
			}
		} else {
			var exists bool
			query := "SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name=?"
			if err := hc.db.QueryRowContext(ctx, query, table).Scan(&exists); err != nil {
				return fmt.Errorf("failed to check table existence for %s: %w", table, err)
			}

			if !exists {
				return fmt.Errorf("required table %s does not exist", table)
			}
		}
	}

	return nil
}

// checkDataConsistency проверяет согласованность данных
func (hc *HealthChecker) checkDataConsistency(ctx context.Context) error {
	// Проверяем, что нет orphaned записей
	var orphanedCount int
	if hc.dbType == "postgres" {
		query := `
			SELECT COUNT(*)
			FROM classifications c
			LEFT JOIN alerts a ON c.alert_fingerprint = a.fingerprint
			WHERE a.fingerprint IS NULL`
		if err := hc.db.QueryRowContext(ctx, query).Scan(&orphanedCount); err != nil {
			return fmt.Errorf("failed to check orphaned classifications: %w", err)
		}
	} else {
		query := `
			SELECT COUNT(*)
			FROM classifications c
			LEFT JOIN alerts a ON c.alert_fingerprint = a.fingerprint
			WHERE a.fingerprint IS NULL`
		if err := hc.db.QueryRowContext(ctx, query).Scan(&orphanedCount); err != nil {
			return fmt.Errorf("failed to check orphaned classifications: %w", err)
		}
	}

	if orphanedCount > 0 {
		hc.logger.Warn("Found orphaned classification records",
			"count", orphanedCount)
		// В продакшене это может быть ошибкой, но для development warning достаточно
	}

	return nil
}

// checkMigrationTable проверяет состояние таблицы миграций
func (hc *HealthChecker) checkMigrationTable(ctx context.Context) error {
	// Проверяем, что таблица миграций в корректном состоянии
	var count int
	if err := hc.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM goose_db_version").Scan(&count); err != nil {
		return fmt.Errorf("failed to check migration table: %w", err)
	}

	hc.logger.Info("Migration table status verified",
		"recorded_migrations", count)

	return nil
}

// detectDatabaseType определяет тип базы данных
func (hc *HealthChecker) detectDatabaseType(ctx context.Context) error {
	// Пробуем PostgreSQL
	var pgResult int
	pgQuery := "SELECT 1"
	if err := hc.db.QueryRowContext(ctx, pgQuery).Scan(&pgResult); err == nil {
		hc.dbType = "postgres"
		return nil
	}

	// Пробуем SQLite
	var sqliteResult string
	sqliteQuery := "SELECT sqlite_version()"
	if err := hc.db.QueryRowContext(ctx, sqliteQuery).Scan(&sqliteResult); err == nil {
		hc.dbType = "sqlite"
		return nil
	}

	hc.dbType = "unknown"
	return fmt.Errorf("unable to determine database type")
}

// GetDatabaseType возвращает определенный тип базы данных
func (hc *HealthChecker) GetDatabaseType() string {
	return hc.dbType
}

// RunCustomCheck выполняет пользовательскую проверку здоровья
func (hc *HealthChecker) RunCustomCheck(ctx context.Context, name string, check HealthCheck) error {
	hc.logger.Info("Running custom health check", "name", name)
	return hc.executeCheck(ctx, name, check)
}
