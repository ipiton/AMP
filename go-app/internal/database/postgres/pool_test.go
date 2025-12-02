package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresConfig_Validate проверяет валидацию конфигурации
func TestPostgresConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *PostgresConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &PostgresConfig{
				Host:              "localhost",
				Port:              5432,
				Database:          "testdb",
				User:              "testuser",
				Password:          "testpass",
				MaxConns:          10,
				MinConns:          2,
				MaxConnLifetime:   time.Hour,
				MaxConnIdleTime:   5 * time.Minute,
				HealthCheckPeriod: 30 * time.Second,
				ConnectTimeout:    30 * time.Second,
				SSLMode:           "disable",
			},
			wantErr: false,
		},
		{
			name: "missing host",
			config: &PostgresConfig{
				Port:     5432,
				Database: "testdb",
				User:     "testuser",
				MaxConns: 10,
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			config: &PostgresConfig{
				Host:     "localhost",
				Port:     70000,
				Database: "testdb",
				User:     "testuser",
				MaxConns: 10,
			},
			wantErr: true,
		},
		{
			name: "min connections > max connections",
			config: &PostgresConfig{
				Host:     "localhost",
				Port:     5432,
				Database: "testdb",
				User:     "testuser",
				MaxConns: 5,
				MinConns: 10,
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

// TestPostgresConfig_LoadFromEnv проверяет загрузку конфигурации из переменных окружения
func TestPostgresConfig_LoadFromEnv(t *testing.T) {
	// Сохраняем оригинальные значения
	originalHost := os.Getenv("DB_HOST")
	originalPort := os.Getenv("DB_PORT")
	originalDB := os.Getenv("DB_NAME")

	defer func() {
		// Восстанавливаем оригинальные значения
		os.Setenv("DB_HOST", originalHost)
		os.Setenv("DB_PORT", originalPort)
		os.Setenv("DB_NAME", originalDB)
	}()

	// Устанавливаем тестовые значения
	os.Setenv("DB_HOST", "testhost")
	os.Setenv("DB_PORT", "5433")
	os.Setenv("DB_NAME", "testdb")

	config := LoadFromEnv()

	assert.Equal(t, "testhost", config.Host)
	assert.Equal(t, 5433, config.Port)
	assert.Equal(t, "testdb", config.Database)
}

// TestPostgresPool_NewPostgresPool проверяет создание нового pool
func TestPostgresPool_NewPostgresPool(t *testing.T) {
	config := DefaultConfig()
	logger := slog.Default()

	pool := NewPostgresPool(config, logger)

	assert.NotNil(t, pool)
	assert.Equal(t, config, pool.GetConfig())
	assert.NotNil(t, pool.GetMetrics())
	assert.NotNil(t, pool.GetHealthChecker())
	assert.False(t, pool.IsConnected())
}

// TestPostgresPool_IsConnected проверяет состояние соединения
func TestPostgresPool_IsConnected(t *testing.T) {
	config := DefaultConfig()
	logger := slog.Default()
	pool := NewPostgresPool(config, logger)

	// Изначально не подключен
	assert.False(t, pool.IsConnected())

	// После закрытия все еще не подключен
	pool.isClosed.Store(true)
	assert.False(t, pool.IsConnected())
}

// TestPostgresPool_Stats проверяет получение статистики
func TestPostgresPool_Stats(t *testing.T) {
	config := DefaultConfig()
	logger := slog.Default()
	pool := NewPostgresPool(config, logger)

	stats := pool.Stats()

	// Для неподключенного pool статистика должна быть пустой
	assert.Equal(t, int32(0), stats.ActiveConnections)
	assert.Equal(t, int32(0), stats.IdleConnections)
	assert.Equal(t, int64(0), stats.TotalConnections)
}

// TestPostgresPool_GetMetrics проверяет получение метрик
func TestPostgresPool_GetMetrics(t *testing.T) {
	config := DefaultConfig()
	logger := slog.Default()
	pool := NewPostgresPool(config, logger)

	metrics := pool.GetMetrics()
	assert.NotNil(t, metrics)

	// Проверяем начальные значения метрик
	assert.Equal(t, int32(0), metrics.ActiveConnections.Load())
	assert.Equal(t, int32(0), metrics.IdleConnections.Load())
	assert.Equal(t, int64(0), metrics.TotalConnections.Load())
}

// TestDatabaseError_IsRetryable проверяет определение retryable ошибок
func TestDatabaseError_IsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{"serialization_failure", "40001", true},
		{"deadlock_detected", "40P01", true},
		{"too_many_connections", "53300", true},
		{"connection_failure", "08006", true},
		{"syntax_error", "42601", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDatabaseError(tt.code, "test error")
			assert.Equal(t, tt.expected, err.IsRetryable())
		})
	}
}

// TestDatabaseError_IsConnectionError проверяет определение connection ошибок
func TestDatabaseError_IsConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{"connection_exception", "08000", true},
		{"connection_failure", "08006", true},
		{"too_many_connections", "53300", true},
		{"syntax_error", "42601", false},
		{"undefined_table", "42P01", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDatabaseError(tt.code, "test error")
			assert.Equal(t, tt.expected, err.IsConnectionError())
		})
	}
}

// TestIsRetryable проверяет функцию определения retryable ошибок
func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"database retryable error", NewDatabaseError("40001", "serialization failure"), true},
		{"database connection error", NewDatabaseError("08006", "connection failure"), true},
		{"connection error", NewConnectionError("connect", "timeout"), true},
		{"timeout error", NewTimeoutError("query", "30s"), true},
		{"database non-retryable error", NewDatabaseError("42601", "syntax error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsRetryable(tt.err))
		})
	}
}

// TestMetrics_RecordQueryExecution проверяет запись метрик выполнения запросов
func TestMetrics_RecordQueryExecution(t *testing.T) {
	metrics := NewPoolMetrics()

	duration := 100 * time.Millisecond

	// Записываем несколько выполнений
	metrics.RecordQueryExecution(duration)
	metrics.RecordQueryExecution(duration * 2)
	metrics.RecordQueryExecution(duration * 3)

	// Проверяем общее количество запросов
	assert.Equal(t, int64(3), metrics.TotalQueries.Load())

	// Проверяем общее время выполнения
	totalTime := metrics.QueryExecutionTime.Load()
	expectedTotal := duration + (duration * 2) + (duration * 3)
	assert.Equal(t, expectedTotal.Nanoseconds(), totalTime)
}

// TestMetrics_GetAverageQueryTime проверяет расчет среднего времени выполнения
func TestMetrics_GetAverageQueryTime(t *testing.T) {
	metrics := NewPoolMetrics()

	// Без запросов среднее время должно быть 0
	assert.Equal(t, time.Duration(0), metrics.GetAverageQueryTime())

	// Добавляем запросы
	duration1 := 100 * time.Millisecond
	duration2 := 200 * time.Millisecond

	metrics.RecordQueryExecution(duration1)
	metrics.RecordQueryExecution(duration2)

	// Среднее время должно быть (100ms + 200ms) / 2 = 150ms
	expectedAverage := 150 * time.Millisecond
	assert.Equal(t, expectedAverage, metrics.GetAverageQueryTime())
}

// TestMetrics_GetSuccessRate проверяет расчет процента успешных операций
func TestMetrics_GetSuccessRate(t *testing.T) {
	metrics := NewPoolMetrics()

	// Без операций процент должен быть 100%
	assert.Equal(t, 100.0, metrics.GetSuccessRate())

	// Добавляем успешные операции
	metrics.RecordQueryExecution(100 * time.Millisecond)
	metrics.RecordQueryExecution(200 * time.Millisecond)

	// Процент должен быть 100%
	assert.Equal(t, 100.0, metrics.GetSuccessRate())

	// Добавляем ошибку
	metrics.RecordQueryError()

	// Процент должен быть 2/3 ≈ 66.67%
	assert.InDelta(t, 66.67, metrics.GetSuccessRate(), 0.01)
}

// TestDefaultConfig проверяет конфигурацию по умолчанию
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, "localhost", config.Host)
	assert.Equal(t, 5432, config.Port)
	assert.Equal(t, "alerthistory", config.Database)
	assert.Equal(t, "alerthistory", config.User)
	assert.Equal(t, "disable", config.SSLMode)
	assert.Equal(t, int32(20), config.MaxConns)
	assert.Equal(t, int32(2), config.MinConns)
	assert.Equal(t, time.Hour, config.MaxConnLifetime)
	assert.Equal(t, 5*time.Minute, config.MaxConnIdleTime)
	assert.Equal(t, 30*time.Second, config.HealthCheckPeriod)
}

// TestPostgresConfig_ConnectionString проверяет генерацию строки подключения
func TestPostgresConfig_ConnectionString(t *testing.T) {
	config := &PostgresConfig{
		Host:     "testhost",
		Port:     5433,
		User:     "testuser",
		Password: "testpass",
		Database: "testdb",
		SSLMode:  "require",
	}

	expected := "host=testhost port=5433 user=testuser password=testpass dbname=testdb sslmode=require"
	assert.Equal(t, expected, config.ConnectionString())
}

// TestPostgresConfig_DSN проверяет генерацию DSN
func TestPostgresConfig_DSN(t *testing.T) {
	config := &PostgresConfig{
		Host:     "testhost",
		Port:     5433,
		User:     "testuser",
		Password: "testpass",
		Database: "testdb",
		SSLMode:  "require",
	}

	expected := "postgres://testuser:testpass@testhost:5433/testdb?sslmode=require"
	assert.Equal(t, expected, config.DSN())
}

// BenchmarkPostgresPool_Query бенчмарк для выполнения запросов
func BenchmarkPostgresPool_Query(b *testing.B) {
	// Этот бенчмарк требует реальной базы данных
	b.Skip("Skipping benchmark - requires real database connection")

	config := DefaultConfig()
	logger := slog.Default()
	pool := NewPostgresPool(config, logger)

	ctx := context.Background()

	// Подключаемся (предполагаем, что база доступна)
	err := pool.Connect(ctx)
	require.NoError(b, err)
	defer pool.Disconnect(ctx)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			row := pool.QueryRow(ctx, "SELECT 1")
			var result int
			err := row.Scan(&result)
			if err != nil {
				b.Fatal(err)
			}
			_ = result // prevent unused variable error
		}
	})
}
