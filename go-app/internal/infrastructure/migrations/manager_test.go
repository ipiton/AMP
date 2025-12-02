package migrations

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationManager_Connect тестирует подключение к базе данных
func TestMigrationManager_Connect(t *testing.T) {
	// Создаем временную SQLite базу данных для тестов
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Тестируем подключение
	err = manager.Connect(ctx)
	assert.NoError(t, err)

	// Тестируем отключение
	err = manager.Disconnect(ctx)
	assert.NoError(t, err)
}

// TestMigrationManager_Status тестирует получение статуса миграций
func TestMigrationManager_Status(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Получаем статус миграций
	statuses, err := manager.Status(ctx)
	assert.NoError(t, err)
	assert.IsType(t, []*MigrationStatus{}, statuses)

	// Проверяем, что статус не nil
	assert.NotNil(t, statuses)
}

// TestMigrationManager_Version тестирует получение версии миграций
func TestMigrationManager_Version(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Получаем версию
	version, err := manager.Version(ctx)
	assert.NoError(t, err)
	assert.IsType(t, int64(0), version)

	// Для новой базы данных версия должна быть 0
	assert.Equal(t, int64(0), version)
}

// TestMigrationManager_Up тестирует применение миграций
func TestMigrationManager_Up(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Применяем миграции
	err = manager.Up(ctx)
	assert.NoError(t, err)

	// Проверяем версию после применения
	version, err := manager.Version(ctx)
	assert.NoError(t, err)
	assert.Greater(t, version, int64(0))
}

// TestMigrationManager_Down тестирует откат миграций
func TestMigrationManager_Down(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Сначала применяем миграции
	err = manager.Up(ctx)
	require.NoError(t, err)

	// Получаем версию после применения
	upVersion, err := manager.Version(ctx)
	require.NoError(t, err)
	require.Greater(t, upVersion, int64(0))

	// Откатываем миграции
	err = manager.Down(ctx)
	assert.NoError(t, err)

	// Проверяем версию после отката
	downVersion, err := manager.Version(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), downVersion)
}

// TestMigrationManager_Validate тестирует валидацию миграций
func TestMigrationManager_Validate(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Валидируем миграции
	err = manager.Validate(ctx)
	assert.NoError(t, err)
}

// TestMigrationManager_List тестирует получение списка миграций
func TestMigrationManager_List(t *testing.T) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(t, err)
	defer manager.Disconnect(ctx)

	// Получаем список миграций
	migrations, err := manager.List(ctx)
	assert.NoError(t, err)
	assert.IsType(t, []*MigrationFile{}, migrations)
	assert.NotNil(t, migrations)
}

// TestMigrationConfig_Validate тестирует валидацию конфигурации
func TestMigrationConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *MigrationConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &MigrationConfig{
				Driver:      "postgres",
				DSN:         "postgres://user:pass@localhost/db",
				Dir:         "migrations",
				Table:       "goose_db_version",
				Timeout:     5 * time.Minute,
				LockTimeout: 30 * time.Second,
				RetryDelay:  5 * time.Second,
				Logger:      slog.Default(),
			},
			wantErr: false,
		},
		{
			name: "empty driver",
			config: &MigrationConfig{
				Driver:  "",
				DSN:     "postgres://user:pass@localhost/db",
				Dir:     "migrations",
				Table:   "goose_db_version",
				Timeout: 5 * time.Minute,
				Logger:  slog.Default(),
			},
			wantErr: true,
		},
		{
			name: "empty DSN",
			config: &MigrationConfig{
				Driver:  "postgres",
				DSN:     "",
				Dir:     "migrations",
				Table:   "goose_db_version",
				Timeout: 5 * time.Minute,
				Logger:  slog.Default(),
			},
			wantErr: true,
		},
		{
			name: "empty migration dir",
			config: &MigrationConfig{
				Driver:  "postgres",
				DSN:     "postgres://user:pass@localhost/db",
				Dir:     "",
				Table:   "goose_db_version",
				Timeout: 5 * time.Minute,
				Logger:  slog.Default(),
			},
			wantErr: true,
		},
		{
			name: "negative timeout",
			config: &MigrationConfig{
				Driver:  "postgres",
				DSN:     "postgres://user:pass@localhost/db",
				Dir:     "migrations",
				Table:   "goose_db_version",
				Timeout: -1 * time.Minute,
				Logger:  slog.Default(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLoadConfig тестирует загрузку конфигурации из переменных окружения
func TestLoadConfig(t *testing.T) {
	// Сохраняем оригинальные переменные окружения
	originalEnv := make(map[string]string)
	envVars := []string{
		"MIGRATION_DRIVER", "MIGRATION_DSN", "MIGRATION_DIALECT",
		"MIGRATION_DIR", "MIGRATION_TABLE", "MIGRATION_SCHEMA",
		"MIGRATION_TIMEOUT", "MIGRATION_VERBOSE", "MIGRATION_DRY_RUN",
	}

	for _, envVar := range envVars {
		originalEnv[envVar] = os.Getenv(envVar)
	}
	defer func() {
		// Восстанавливаем оригинальные переменные
		for key, value := range originalEnv {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Устанавливаем тестовые переменные окружения
	os.Setenv("MIGRATION_DRIVER", "sqlite")
	os.Setenv("MIGRATION_DSN", ":memory:")
	os.Setenv("MIGRATION_DIR", "test_migrations")
	os.Setenv("MIGRATION_VERBOSE", "true")

	config, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "sqlite", config.Driver)
	assert.Equal(t, ":memory:", config.DSN)
	assert.Equal(t, "test_migrations", config.Dir)
	assert.True(t, config.Verbose)
}

// BenchmarkMigrationManager_Up бенчмарк для применения миграций
func BenchmarkMigrationManager_Up(b *testing.B) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(b, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelError,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(b, err)

	ctx := context.Background()

	// Подключаемся к БД
	err = manager.Connect(ctx)
	require.NoError(b, err)
	defer manager.Disconnect(ctx)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Откатываем миграции для чистого состояния
		manager.Down(ctx)

		// Применяем миграции
		err = manager.Up(ctx)
		assert.NoError(b, err)
	}
}

// BenchmarkMigrationManager_Status бенчмарк для получения статуса миграций
func BenchmarkMigrationManager_Status(b *testing.B) {
	// Создаем временную SQLite базу данных
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(b, err)
	defer db.Close()

	config := &MigrationConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
		Dir:    "../../../../../migrations",
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelError,
		})),
	}

	manager, err := NewMigrationManager(config)
	require.NoError(b, err)

	ctx := context.Background()

	// Подключаемся к БД и применяем миграции
	err = manager.Connect(ctx)
	require.NoError(b, err)
	defer manager.Disconnect(ctx)

	err = manager.Up(ctx)
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := manager.Status(ctx)
		assert.NoError(b, err)
	}
}
