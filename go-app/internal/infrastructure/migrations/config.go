package migrations

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadConfig загружает конфигурацию системы миграций из переменных окружения
func LoadConfig() (*MigrationConfig, error) {
	config := &MigrationConfig{}

	// Database configuration
	config.Driver = getEnvString("MIGRATION_DRIVER", "postgres")
	config.DSN = getEnvString("MIGRATION_DSN", "")
	config.Dialect = getEnvString("MIGRATION_DIALECT", config.Driver)

	// Migration settings
	config.Dir = getEnvString("MIGRATION_DIR", "migrations")
	config.Table = getEnvString("MIGRATION_TABLE", "goose_db_version")
	config.Schema = getEnvString("MIGRATION_SCHEMA", "public")

	// Safety settings
	config.Timeout = getEnvDuration("MIGRATION_TIMEOUT", 5*time.Minute)
	config.MaxRetries = getEnvInt("MIGRATION_MAX_RETRIES", 3)
	config.RetryDelay = getEnvDuration("MIGRATION_RETRY_DELAY", 5*time.Second)

	// Development settings
	config.Verbose = getEnvBool("MIGRATION_VERBOSE", false)
	config.DryRun = getEnvBool("MIGRATION_DRY_RUN", false)
	config.AllowOutOfOrder = getEnvBool("MIGRATION_ALLOW_OUT_OF_ORDER", false)

	// Safety settings
	config.NoVersioning = getEnvBool("MIGRATION_NO_VERSIONING", false)
	config.LockTimeout = getEnvDuration("MIGRATION_LOCK_TIMEOUT", 10*time.Second)

	// Monitoring
	config.EnableMetrics = getEnvBool("MIGRATION_METRICS", true)
	config.EnableTracing = getEnvBool("MIGRATION_TRACING", false)

	// Валидация конфигурации
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid migration configuration: %w", err)
	}

	return config, nil
}

// Validate проверяет корректность конфигурации
func (c *MigrationConfig) Validate() error {
	if c.Driver == "" {
		return fmt.Errorf("database driver cannot be empty")
	}

	if c.DSN == "" {
		return fmt.Errorf("database DSN cannot be empty")
	}

	if c.Dir == "" {
		return fmt.Errorf("migration directory cannot be empty")
	}

	if c.Table == "" {
		return fmt.Errorf("migration table name cannot be empty")
	}

	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if c.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}

	if c.RetryDelay <= 0 {
		return fmt.Errorf("retry delay must be positive")
	}

	if c.LockTimeout <= 0 {
		return fmt.Errorf("lock timeout must be positive")
	}

	return nil
}

// LoadBackupConfig загружает конфигурацию backup
func LoadBackupConfig() (*BackupConfig, error) {
	config := &BackupConfig{}

	config.Enabled = getEnvBool("BACKUP_ENABLED", true)
	config.Type = getEnvString("BACKUP_TYPE", "schema")
	config.Path = getEnvString("BACKUP_PATH", "./backups")
	config.RetentionDays = getEnvInt("BACKUP_RETENTION_DAYS", 30)
	config.Compress = getEnvBool("BACKUP_COMPRESS", true)
	config.Timeout = getEnvDuration("BACKUP_TIMEOUT", 10*time.Minute)

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid backup configuration: %w", err)
	}

	return config, nil
}

// Validate проверяет корректность конфигурации backup
func (bc *BackupConfig) Validate() error {
	if bc.Path == "" {
		return fmt.Errorf("backup path cannot be empty")
	}

	if bc.RetentionDays < 0 {
		return fmt.Errorf("retention days cannot be negative")
	}

	if bc.Timeout <= 0 {
		return fmt.Errorf("backup timeout must be positive")
	}

	return nil
}

// LoadHealthConfig загружает конфигурацию health checks
func LoadHealthConfig() (*HealthConfig, error) {
	config := &HealthConfig{}

	config.Enabled = getEnvBool("HEALTH_ENABLED", true)
	config.Timeout = getEnvDuration("HEALTH_TIMEOUT", 30*time.Second)
	config.RetryCount = getEnvInt("HEALTH_RETRY_COUNT", 3)
	config.RetryDelay = getEnvDuration("HEALTH_RETRY_DELAY", 5*time.Second)

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid health configuration: %w", err)
	}

	return config, nil
}

// Validate проверяет корректность конфигурации health checks
func (hc *HealthConfig) Validate() error {
	if hc.Timeout <= 0 {
		return fmt.Errorf("health timeout must be positive")
	}

	if hc.RetryCount < 0 {
		return fmt.Errorf("retry count cannot be negative")
	}

	if hc.RetryDelay <= 0 {
		return fmt.Errorf("retry delay must be positive")
	}

	return nil
}

// getEnvString получает строковую переменную окружения с значением по умолчанию
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool получает булеву переменную окружения с значением по умолчанию
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

// getEnvInt получает целочисленную переменную окружения с значением по умолчанию
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// getEnvDuration получает переменную окружения типа duration с значением по умолчанию
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

// PrintConfig выводит текущую конфигурацию в лог
func (c *MigrationConfig) PrintConfig(logger *slog.Logger) {
	logger.Info("Migration Configuration",
		"driver", c.Driver,
		"dialect", c.Dialect,
		"dir", c.Dir,
		"table", c.Table,
		"schema", c.Schema,
		"timeout", c.Timeout,
		"verbose", c.Verbose,
		"allow_out_of_order", c.AllowOutOfOrder,
		"no_versioning", c.NoVersioning,
		"enable_metrics", c.EnableMetrics,
		"enable_tracing", c.EnableTracing,
	)
}

// GetDSN возвращает DSN с маскированными credentials для логирования
func (c *MigrationConfig) GetDSN() string {
	dsn := c.DSN

	// Маскируем пароль в DSN для логирования
	if strings.Contains(dsn, "password=") {
		parts := strings.Split(dsn, "password=")
		if len(parts) > 1 {
			passwordPart := parts[1]
			if idx := strings.Index(passwordPart, " "); idx > 0 {
				passwordPart = passwordPart[:idx]
			}
			dsn = parts[0] + "password=***" + strings.TrimPrefix(parts[1], passwordPart)
		}
	}

	return dsn
}

// IsProduction проверяет, запущено ли приложение в production окружении
func (c *MigrationConfig) IsProduction() bool {
	env := getEnvString("ENV", "development")
	return env == "production" || env == "prod"
}

// IsDevelopment проверяет, запущено ли приложение в development окружении
func (c *MigrationConfig) IsDevelopment() bool {
	env := getEnvString("ENV", "development")
	return env == "development" || env == "dev"
}

// ShouldCreateBackups проверяет, нужно ли создавать backup'ы
func (c *MigrationConfig) ShouldCreateBackups() bool {
	// В production всегда создаем backup'ы
	if c.IsProduction() {
		return true
	}

	// В development проверяем настройку
	return getEnvBool("MIGRATION_BACKUP_IN_DEV", false)
}

// ShouldRunHealthChecks проверяет, нужно ли запускать health checks
func (c *MigrationConfig) ShouldRunHealthChecks() bool {
	// В production всегда запускаем health checks
	if c.IsProduction() {
		return true
	}

	// В development проверяем настройку
	return getEnvBool("MIGRATION_HEALTH_IN_DEV", true)
}
