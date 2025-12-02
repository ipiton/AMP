package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// TestAlert представляет тестовую структуру алерта
type TestAlert struct {
	Fingerprint string          `json:"fingerprint"`
	Data        json.RawMessage `json:"data"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func TestSQLiteDatabase_Connect(t *testing.T) {
	// Создаем временный файл для тестирования
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	config := &Config{
		Driver:          "sqlite",
		SQLiteFile:      dbPath,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		Logger:          slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)
	require.NotNil(t, db)

	// Проверяем, что соединение не установлено до вызова Connect
	assert.False(t, db.IsConnected())

	// Устанавливаем соединение
	ctx := context.Background()
	err = db.Connect(ctx)
	require.NoError(t, err)

	// Проверяем, что соединение установлено
	assert.True(t, db.IsConnected())

	// Проверяем, что файл базы данных создан
	_, err = os.Stat(dbPath)
	assert.False(t, os.IsNotExist(err), "Database file should be created")

	// Закрываем соединение
	err = db.Disconnect(ctx)
	require.NoError(t, err)
	assert.False(t, db.IsConnected())
}

func TestSQLiteDatabase_InMemory(t *testing.T) {
	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: ":memory:",
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = db.Connect(ctx)
	require.NoError(t, err)
	assert.True(t, db.IsConnected())

	err = db.Disconnect(ctx)
	require.NoError(t, err)
	assert.False(t, db.IsConnected())
}

func TestSQLiteDatabase_Migrate(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migrate.db")

	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: dbPath,
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = db.Connect(ctx)
	require.NoError(t, err)
	defer db.Disconnect(ctx)

	// Выполняем миграцию
	err = db.MigrateUp(ctx)
	require.NoError(t, err)

	// Проверяем, что все таблицы созданы
	tables := []string{"alerts", "classifications", "publishing"}
	for _, table := range tables {
		row := db.QueryRow(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		var tableName string
		err = row.Scan(&tableName)
		require.NoError(t, err, "Table %s should exist", table)
		assert.Equal(t, table, tableName)
	}

	// Проверяем, что индексы созданы для alerts
	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='alerts'")
	require.NoError(t, err)
	defer rows.Close()

	var indexes []string
	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		require.NoError(t, err)
		indexes = append(indexes, name)
	}

	expectedIndexes := []string{"idx_alerts_status", "idx_alerts_starts_at", "idx_alerts_created_at"}
	for _, expectedIndex := range expectedIndexes {
		assert.Contains(t, indexes, expectedIndex, "Index %s should exist", expectedIndex)
	}
}

func TestSQLiteDatabase_CRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_crud.db")

	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: dbPath,
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = db.Connect(ctx)
	require.NoError(t, err)
	defer db.Disconnect(ctx)

	err = db.MigrateUp(ctx)
	require.NoError(t, err)

	// Создаем тестовый алерт
	alert := &core.Alert{
		Fingerprint: "test-fingerprint-123",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"severity": "warning", "namespace": "test"},
		Annotations: map[string]string{"description": "Test alert"},
		StartsAt:    time.Now(),
	}

	// Сохраняем алерт
	err = db.SaveAlert(ctx, alert)
	require.NoError(t, err)

	// Читаем алерт
	retrievedAlert, err := db.GetAlertByFingerprint(ctx, alert.Fingerprint)
	require.NoError(t, err)
	require.NotNil(t, retrievedAlert)

	assert.Equal(t, alert.Fingerprint, retrievedAlert.Fingerprint)
	assert.Equal(t, alert.AlertName, retrievedAlert.AlertName)
	assert.Equal(t, alert.Status, retrievedAlert.Status)
	assert.Equal(t, alert.Labels, retrievedAlert.Labels)
	assert.Equal(t, alert.Annotations, retrievedAlert.Annotations)

	// Обновляем алерт
	alert.Status = core.StatusResolved
	endsAt := time.Now()
	alert.EndsAt = &endsAt

	err = db.SaveAlert(ctx, alert)
	require.NoError(t, err)

	// Проверяем обновление
	updatedAlert, err := db.GetAlertByFingerprint(ctx, alert.Fingerprint)
	require.NoError(t, err)
	require.NotNil(t, updatedAlert)
	assert.Equal(t, core.StatusResolved, updatedAlert.Status)
	assert.NotNil(t, updatedAlert.EndsAt)

	// Проверяем получение списка алертов
	status := core.StatusResolved
	alertList, err := db.ListAlerts(ctx, &core.AlertFilters{
		Status: &status,
		Limit:  10,
		Offset: 0,
	})
	require.NoError(t, err)
	assert.Len(t, alertList.Alerts, 1)
	assert.Equal(t, alert.Fingerprint, alertList.Alerts[0].Fingerprint)
	assert.Equal(t, 1, alertList.Total)
}

func TestSQLiteDatabase_Transaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_transaction.db")

	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: dbPath,
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = db.Connect(ctx)
	require.NoError(t, err)
	defer db.Disconnect(ctx)

	err = db.MigrateUp(ctx)
	require.NoError(t, err)

	// Создаем тестовые алерты
	alert1 := &core.Alert{
		Fingerprint: "tx-test-1",
		AlertName:   "TxAlert1",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"severity": "high"},
		StartsAt:    time.Now(),
	}

	alert2 := &core.Alert{
		Fingerprint: "tx-test-2",
		AlertName:   "TxAlert2",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"severity": "low"},
		StartsAt:    time.Now(),
	}

	// Начинаем транзакцию
	tx, err := db.Begin(ctx)
	require.NoError(t, err)

	// Выполняем операции в транзакции
	_, err = tx.Exec(`
		INSERT INTO alerts (fingerprint, alert_name, status, labels, annotations, starts_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		alert1.Fingerprint, alert1.AlertName, string(alert1.Status),
		`{"severity": "high"}`, `{}`, alert1.StartsAt)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO alerts (fingerprint, alert_name, status, labels, annotations, starts_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		alert2.Fingerprint, alert2.AlertName, string(alert2.Status),
		`{"severity": "low"}`, `{}`, alert2.StartsAt)
	require.NoError(t, err)

	// Коммитим транзакцию
	err = tx.Commit()
	require.NoError(t, err)

	// Проверяем, что данные сохранены
	alertList, err := db.ListAlerts(ctx, &core.AlertFilters{
		Limit:  10,
		Offset: 0,
	})
	require.NoError(t, err)
	assert.Len(t, alertList.Alerts, 2)
	assert.Equal(t, 2, alertList.Total)

	fingerprints := make([]string, len(alertList.Alerts))
	for i, alert := range alertList.Alerts {
		fingerprints[i] = alert.Fingerprint
	}

	assert.Contains(t, fingerprints, "tx-test-1")
	assert.Contains(t, fingerprints, "tx-test-2")
}

func TestSQLiteDatabase_Health(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_health.db")

	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: dbPath,
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Health должен вернуть ошибку, если соединение не установлено
	err = db.Health(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")

	err = db.Connect(ctx)
	require.NoError(t, err)
	defer db.Disconnect(ctx)

	// Health должен работать после подключения
	err = db.Health(ctx)
	assert.NoError(t, err)
}

func TestSQLiteDatabase_Query(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_query.db")

	config := &Config{
		Driver:     "sqlite",
		SQLiteFile: dbPath,
		Logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = db.Connect(ctx)
	require.NoError(t, err)
	defer db.Disconnect(ctx)

	err = db.MigrateUp(ctx)
	require.NoError(t, err)

	// Создаем тестовые алерты
	alerts := []*core.Alert{
		{
			Fingerprint: "query-test-1",
			AlertName:   "QueryAlert1",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"severity": "high"},
			StartsAt:    time.Now(),
		},
		{
			Fingerprint: "query-test-2",
			AlertName:   "QueryAlert2",
			Status:      core.StatusResolved,
			Labels:      map[string]string{"severity": "low"},
			StartsAt:    time.Now(),
		},
	}

	// Сохраняем алерты
	for _, alert := range alerts {
		err := db.SaveAlert(ctx, alert)
		require.NoError(t, err)
	}

	// Тестируем ListAlerts с фильтрами
	severity := "high"
	highSeverityList, err := db.ListAlerts(ctx, &core.AlertFilters{
		Severity: &severity,
		Limit:    10,
		Offset:   0,
	})
	require.NoError(t, err)
	assert.Len(t, highSeverityList.Alerts, 1)
	assert.Equal(t, "query-test-1", highSeverityList.Alerts[0].Fingerprint)
	assert.Equal(t, 1, highSeverityList.Total)

	// Тестируем ListAlerts без фильтров
	allAlertsList, err := db.ListAlerts(ctx, &core.AlertFilters{
		Limit:  10,
		Offset: 0,
	})
	require.NoError(t, err)
	assert.Len(t, allAlertsList.Alerts, 2)
	assert.Equal(t, 2, allAlertsList.Total)

	// Проверяем, что алерты отсортированы по starts_at DESC
	assert.Equal(t, "query-test-2", allAlertsList.Alerts[0].Fingerprint) // Более поздний
	assert.Equal(t, "query-test-1", allAlertsList.Alerts[1].Fingerprint) // Более ранний
}

// BenchmarkSQLiteDatabase_CRUD бенчмарк для CRUD операций
func BenchmarkSQLiteDatabase_CRUD(b *testing.B) {
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	config := &Config{
		Driver:          "sqlite",
		SQLiteFile:      dbPath,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		Logger:          slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	db, err := NewSQLiteDatabase(config)
	require.NoError(b, err)

	ctx := context.Background()
	err = db.Connect(ctx)
	require.NoError(b, err)
	defer db.Disconnect(ctx)

	err = db.MigrateUp(ctx)
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		alert := &core.Alert{
			Fingerprint: fmt.Sprintf("bench-fingerprint-%d", i),
			AlertName:   fmt.Sprintf("BenchAlert%d", i),
			Status:      core.StatusFiring,
			Labels:      map[string]string{"severity": "info"},
			Annotations: map[string]string{"description": "Benchmark alert"},
			StartsAt:    time.Now(),
		}

		// Create
		err := db.SaveAlert(ctx, alert)
		require.NoError(b, err)

		// Read
		_, err = db.GetAlertByFingerprint(ctx, alert.Fingerprint)
		require.NoError(b, err)

		// Update
		alert.Status = core.StatusResolved
		err = db.SaveAlert(ctx, alert)
		require.NoError(b, err)

		// Query with filters
		status := core.StatusResolved
		_, err = db.ListAlerts(ctx, &core.AlertFilters{
			Status: &status,
			Limit:  1,
			Offset: 0,
		})
		require.NoError(b, err)
	}
}
