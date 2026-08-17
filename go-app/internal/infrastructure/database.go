package infrastructure

import (
	"time"

	"log/slog"
)

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
