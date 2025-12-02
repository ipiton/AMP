package infrastructure

import (
	"context"
	"database/sql"
	"time"

	"log/slog"

	"github.com/ipiton/AMP/internal/core"
)

// Database определяет общий интерфейс для работы с базой данных
// Поддерживает как PostgreSQL, так и SQLite адаптеры
type Database interface {
	// Core database operations
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool
	Health(ctx context.Context) error

	// Alert operations (AlertStorage interface)
	SaveAlert(ctx context.Context, alert *core.Alert) error
	GetAlertByFingerprint(ctx context.Context, fingerprint string) (*core.Alert, error)
	ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error)
	UpdateAlert(ctx context.Context, alert *core.Alert) error
	DeleteAlert(ctx context.Context, fingerprint string) error
	GetAlertStats(ctx context.Context) (*core.AlertStats, error)
	CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error)

	// Classification operations
	SaveClassification(ctx context.Context, fingerprint string, result *core.ClassificationResult) error
	GetClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error)

	// Publishing operations
	LogPublishingAttempt(ctx context.Context, fingerprint, targetName string, success bool, errorMessage *string, processingTime *float64) error
	GetPublishingHistory(ctx context.Context, fingerprint string) ([]*core.PublishingLog, error)

	// Migration operations
	MigrateUp(ctx context.Context) error
	MigrateDown(ctx context.Context, steps int) error

	// Utility operations (for database stats, not alert stats)
	GetStats(ctx context.Context) (map[string]interface{}, error)

	// Low-level operations (for compatibility)
	Exec(ctx context.Context, sql string, args ...interface{}) (sql.Result, error)
	Query(ctx context.Context, sql string, args ...interface{}) (*sql.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) *sql.Row
	Begin(ctx context.Context) (*sql.Tx, error)
}

// Config определяет конфигурацию для базы данных
type Config struct {
	Driver string // "postgres" или "sqlite"
	DSN    string
	Logger *slog.Logger

	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// SQLite specific
	SQLiteFile string
}

// NewDatabase создает новый экземпляр базы данных на основе конфигурации
func NewDatabase(config *Config) (Database, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	switch config.Driver {
	case "postgres":
		return NewPostgresDatabase(config)
	case "sqlite":
		return NewSQLiteDatabase(config)
	default:
		return nil, &UnsupportedDriverError{Driver: config.Driver}
	}
}

// UnsupportedDriverError возвращается когда указан неподдерживаемый драйвер БД
type UnsupportedDriverError struct {
	Driver string
}

func (e *UnsupportedDriverError) Error() string {
	return "unsupported database driver: " + e.Driver
}
