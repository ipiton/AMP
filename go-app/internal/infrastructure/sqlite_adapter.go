package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ipiton/AMP/internal/core"
)

// SQLiteDatabase адаптер для SQLite, реализующий общий интерфейс Database
type SQLiteDatabase struct {
	db     *sql.DB
	config *Config
	logger *slog.Logger
}

// NewSQLiteDatabase создает новый SQLite адаптер
func NewSQLiteDatabase(config *Config) (*SQLiteDatabase, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	sqlite := &SQLiteDatabase{
		config: config,
		logger: config.Logger,
	}

	return sqlite, nil
}

// Connect устанавливает соединение с SQLite
func (s *SQLiteDatabase) Connect(ctx context.Context) error {
	dbPath := s.config.SQLiteFile
	if dbPath == "" {
		dbPath = ":memory:"
	}

	// Создаем директорию для файла БД, если указан путь к файлу
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	s.logger.Info("Connecting to SQLite", "filepath", dbPath)

	// Открываем соединение с SQLite
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Настраиваем параметры соединения
	if s.config.MaxOpenConns > 0 {
		db.SetMaxOpenConns(s.config.MaxOpenConns)
	}
	if s.config.MaxIdleConns > 0 {
		db.SetMaxIdleConns(s.config.MaxIdleConns)
	}
	if s.config.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(s.config.ConnMaxLifetime)
	}
	if s.config.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(s.config.ConnMaxIdleTime)
	}

	// Включаем foreign keys
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Включаем WAL mode для лучшей производительности
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		s.logger.Warn("Failed to enable WAL mode", "error", err)
	}

	// Тестируем соединение
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping SQLite database: %w", err)
	}

	s.db = db

	s.logger.Info("Successfully connected to SQLite",
		"filepath", dbPath,
		"max_open_conns", s.config.MaxOpenConns,
		"max_idle_conns", s.config.MaxIdleConns)

	return nil
}

// Disconnect закрывает соединение с SQLite
func (s *SQLiteDatabase) Disconnect(ctx context.Context) error {
	if s.db == nil {
		return nil
	}

	s.logger.Info("Disconnecting from SQLite")

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close SQLite database: %w", err)
	}

	s.db = nil
	s.logger.Info("Successfully disconnected from SQLite")

	return nil
}

// IsConnected проверяет состояние соединения
func (s *SQLiteDatabase) IsConnected() bool {
	if s.db == nil {
		return false
	}

	// Проверяем соединение с помощью простого запроса
	return s.db.Ping() == nil
}

// Health выполняет проверку здоровья базы данных
func (s *SQLiteDatabase) Health(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	// Выполняем простой запрос для проверки работоспособности
	_, err := s.db.ExecContext(ctx, "SELECT 1")
	return err
}

// Exec выполняет SQL команду
func (s *SQLiteDatabase) Exec(ctx context.Context, sql string, args ...interface{}) (sql.Result, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	return s.db.ExecContext(ctx, sql, args...)
}

// Query выполняет SQL запрос
func (s *SQLiteDatabase) Query(ctx context.Context, sql string, args ...interface{}) (*sql.Rows, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	return s.db.QueryContext(ctx, sql, args...)
}

// QueryRow выполняет SQL запрос и возвращает одну строку
func (s *SQLiteDatabase) QueryRow(ctx context.Context, sql string, args ...interface{}) *sql.Row {
	if s.db == nil {
		return nil
	}

	return s.db.QueryRowContext(ctx, sql, args...)
}

// Begin начинает новую транзакцию
func (s *SQLiteDatabase) Begin(ctx context.Context) (*sql.Tx, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	return s.db.BeginTx(ctx, nil)
}

// MigrateUp выполняет миграции схемы для SQLite
func (s *SQLiteDatabase) MigrateUp(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	// Создаем таблицу alerts если она не существует
	createAlertsTableSQL := `
	CREATE TABLE IF NOT EXISTS alerts (
		fingerprint TEXT PRIMARY KEY,
		alert_name TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('firing', 'resolved')),
		labels TEXT NOT NULL, -- JSON format
		annotations TEXT NOT NULL, -- JSON format
		starts_at DATETIME NOT NULL,
		ends_at DATETIME,
		generator_url TEXT,
		timestamp DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);
	CREATE INDEX IF NOT EXISTS idx_alerts_starts_at ON alerts(starts_at);
	CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at);

	-- Триггер для автоматического обновления updated_at
	CREATE TRIGGER IF NOT EXISTS update_alerts_updated_at
		AFTER UPDATE ON alerts
		FOR EACH ROW
		BEGIN
			UPDATE alerts SET updated_at = CURRENT_TIMESTAMP WHERE fingerprint = OLD.fingerprint;
		END;
	`

	if _, err := s.db.ExecContext(ctx, createAlertsTableSQL); err != nil {
		return fmt.Errorf("failed to create alerts table: %w", err)
	}

	// Создаем таблицу classifications
	createClassificationsTableSQL := `
	CREATE TABLE IF NOT EXISTS classifications (
		id TEXT PRIMARY KEY,
		alert_fingerprint TEXT NOT NULL,
		category TEXT NOT NULL, -- severity level
		confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
		reasoning TEXT NOT NULL,
		recommendations TEXT NOT NULL, -- JSON array
		metadata TEXT, -- JSON object
		processing_time REAL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (alert_fingerprint) REFERENCES alerts(fingerprint) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_classifications_alert_fingerprint ON classifications(alert_fingerprint);
	CREATE INDEX IF NOT EXISTS idx_classifications_category ON classifications(category);
	CREATE INDEX IF NOT EXISTS idx_classifications_created_at ON classifications(created_at);
	`

	if _, err := s.db.ExecContext(ctx, createClassificationsTableSQL); err != nil {
		return fmt.Errorf("failed to create classifications table: %w", err)
	}

	// Создаем таблицу publishing
	createPublishingTableSQL := `
	CREATE TABLE IF NOT EXISTS publishing (
		id TEXT PRIMARY KEY,
		alert_fingerprint TEXT NOT NULL,
		channel TEXT NOT NULL, -- slack, pagerduty, email, etc.
		status TEXT NOT NULL CHECK (status IN ('sent', 'failed')),
		message_id TEXT,
		error_message TEXT,
		processing_time REAL,
		sent_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (alert_fingerprint) REFERENCES alerts(fingerprint) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_publishing_alert_fingerprint ON publishing(alert_fingerprint);
	CREATE INDEX IF NOT EXISTS idx_publishing_channel ON publishing(channel);
	CREATE INDEX IF NOT EXISTS idx_publishing_status ON publishing(status);
	CREATE INDEX IF NOT EXISTS idx_publishing_created_at ON publishing(created_at);
	`

	if _, err := s.db.ExecContext(ctx, createPublishingTableSQL); err != nil {
		return fmt.Errorf("failed to create publishing table: %w", err)
	}

	s.logger.Info("SQLite schema migration completed successfully",
		"tables_created", []string{"alerts", "classifications", "publishing"})
	return nil
}

// SaveAlert сохраняет алерт в базу данных
func (s *SQLiteDatabase) SaveAlert(ctx context.Context, alert *core.Alert) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	// Сериализуем labels и annotations в JSON
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO alerts (
			fingerprint, alert_name, status, labels, annotations,
			starts_at, ends_at, generator_url, timestamp, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	now := time.Now()
	_, err = s.db.ExecContext(ctx, query,
		alert.Fingerprint, alert.AlertName, string(alert.Status),
		string(labelsJSON), string(annotationsJSON),
		alert.StartsAt, alert.EndsAt, alert.GeneratorURL,
		alert.Timestamp, now, now,
	)

	if err != nil {
		return fmt.Errorf("failed to save alert: %w", err)
	}

	return nil
}

// GetAlertByFingerprint получает алерт по fingerprint
func (s *SQLiteDatabase) GetAlertByFingerprint(ctx context.Context, fingerprint string) (*core.Alert, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := `
		SELECT fingerprint, alert_name, status, labels, annotations,
			   starts_at, ends_at, generator_url, timestamp
		FROM alerts WHERE fingerprint = ?`

	row := s.db.QueryRowContext(ctx, query, fingerprint)

	alert := &core.Alert{}
	var labelsJSON, annotationsJSON string
	var endsAt, generatorURL, timestamp interface{}

	err := row.Scan(
		&alert.Fingerprint, &alert.AlertName, &alert.Status,
		&labelsJSON, &annotationsJSON, &alert.StartsAt,
		&endsAt, &generatorURL, &timestamp,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Same sentinel as PostgresStorageAdapter so callers can errors.Is
			// regardless of profile (Lite vs Standard).
			return nil, core.ErrAlertNotFound
		}
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}

	// Десериализуем JSON поля
	if err := json.Unmarshal([]byte(labelsJSON), &alert.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}

	if err := json.Unmarshal([]byte(annotationsJSON), &alert.Annotations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
	}

	// Обработка nullable полей
	if endsAt != nil {
		if t, ok := endsAt.(time.Time); ok {
			alert.EndsAt = &t
		}
	}

	if generatorURL != nil {
		if s, ok := generatorURL.(string); ok {
			alert.GeneratorURL = &s
		}
	}

	if timestamp != nil {
		if t, ok := timestamp.(time.Time); ok {
			alert.Timestamp = &t
		}
	}

	return alert, nil
}

// ListAlerts получает список алертов с типизированными фильтрами
func (s *SQLiteDatabase) ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Если filters nil, создаем дефолтный
	if filters == nil {
		filters = &core.AlertFilters{
			Limit:  100,
			Offset: 0,
		}
	}

	// Строим WHERE clause
	whereClause := "WHERE 1=1"
	args := []interface{}{}

	// Фильтр по статусу
	if filters.Status != nil {
		whereClause += " AND status = ?"
		args = append(args, string(*filters.Status))
	}

	// Фильтр по severity (из labels)
	if filters.Severity != nil {
		whereClause += " AND json_extract(labels, '$.severity') = ?"
		args = append(args, *filters.Severity)
	}

	// Фильтр по namespace
	if filters.Namespace != nil {
		whereClause += " AND json_extract(labels, '$.namespace') = ?"
		args = append(args, *filters.Namespace)
	}

	// Фильтр по времени
	if filters.TimeRange != nil {
		if filters.TimeRange.From != nil {
			whereClause += " AND starts_at >= ?"
			args = append(args, *filters.TimeRange.From)
		}
		if filters.TimeRange.To != nil {
			whereClause += " AND starts_at <= ?"
			args = append(args, *filters.TimeRange.To)
		}
	}

	// Фильтры по labels (JSON contains - упрощённая версия для SQLite).
	// JSON path передаётся параметром: конкатенация ключа в SQL — инъекция.
	for key, value := range filters.Labels {
		whereClause += " AND json_extract(labels, ?) = ?"
		args = append(args, "$."+key, value)
	}

	// Получаем общее количество
	countQuery := "SELECT COUNT(*) FROM alerts " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alerts: %w", err)
	}

	// Получаем alerts с пагинацией
	query := `
		SELECT fingerprint, alert_name, status, labels, annotations,
		       starts_at, ends_at, generator_url, timestamp
		FROM alerts ` + whereClause + `
		ORDER BY starts_at DESC`

	// Добавляем пагинацию
	if filters.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filters.Limit)
	}

	if filters.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filters.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var alerts []*core.Alert
	for rows.Next() {
		alert := &core.Alert{}
		var labelsJSON, annotationsJSON string
		var endsAt, generatorURL, timestamp interface{}

		err := rows.Scan(
			&alert.Fingerprint, &alert.AlertName, &alert.Status,
			&labelsJSON, &annotationsJSON, &alert.StartsAt,
			&endsAt, &generatorURL, &timestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}

		// Десериализуем JSON поля
		if err := json.Unmarshal([]byte(labelsJSON), &alert.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}

		if err := json.Unmarshal([]byte(annotationsJSON), &alert.Annotations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
		}

		// Обработка nullable полей
		if endsAt != nil {
			if t, ok := endsAt.(time.Time); ok {
				alert.EndsAt = &t
			}
		}

		if generatorURL != nil {
			if s, ok := generatorURL.(string); ok {
				alert.GeneratorURL = &s
			}
		}

		if timestamp != nil {
			if t, ok := timestamp.(time.Time); ok {
				alert.Timestamp = &t
			}
		}

		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate alerts: %w", err)
	}

	return &core.AlertList{
		Alerts: alerts,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateAlert обновляет существующий алерт
func (s *SQLiteDatabase) UpdateAlert(ctx context.Context, alert *core.Alert) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	// Сериализуем labels и annotations в JSON
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

	query := `
		UPDATE alerts SET
			alert_name = ?,
			status = ?,
			labels = ?,
			annotations = ?,
			starts_at = ?,
			ends_at = ?,
			generator_url = ?,
			timestamp = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE fingerprint = ?`

	result, err := s.db.ExecContext(ctx, query,
		alert.AlertName,
		string(alert.Status),
		string(labelsJSON),
		string(annotationsJSON),
		alert.StartsAt,
		alert.EndsAt,
		alert.GeneratorURL,
		alert.Timestamp,
		alert.Fingerprint,
	)
	if err != nil {
		return fmt.Errorf("failed to update alert: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("update alert %s: %w", alert.Fingerprint, core.ErrAlertNotFound)
	}

	return nil
}

// DeleteAlert удаляет алерт по fingerprint
func (s *SQLiteDatabase) DeleteAlert(ctx context.Context, fingerprint string) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	query := "DELETE FROM alerts WHERE fingerprint = ?"

	result, err := s.db.ExecContext(ctx, query, fingerprint)
	if err != nil {
		return fmt.Errorf("failed to delete alert: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("delete alert %s: %w", fingerprint, core.ErrAlertNotFound)
	}

	return nil
}

// GetAlertStats возвращает статистику по алертам (реализация AlertStorage интерфейса)
func (s *SQLiteDatabase) GetAlertStats(ctx context.Context) (*core.AlertStats, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	stats := &core.AlertStats{
		AlertsByStatus:    make(map[string]int),
		AlertsBySeverity:  make(map[string]int),
		AlertsByNamespace: make(map[string]int),
	}

	// Общее количество алертов
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts")
	if err := row.Scan(&stats.TotalAlerts); err != nil {
		return nil, fmt.Errorf("failed to get total alerts count: %w", err)
	}

	// Статистика по статусам
	rows, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM alerts GROUP BY status")
	if err != nil {
		return nil, fmt.Errorf("failed to get status stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan status stats: %w", err)
		}
		stats.AlertsByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stats rows: %w", err)
	}

	// Статистика по severity (из labels)
	rows, err = s.db.QueryContext(ctx, `
		SELECT json_extract(labels, '$.severity') as severity, COUNT(*)
		FROM alerts
		WHERE json_extract(labels, '$.severity') IS NOT NULL
		GROUP BY json_extract(labels, '$.severity')
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get severity stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var severity string
		var count int
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, fmt.Errorf("failed to scan severity stats: %w", err)
		}
		stats.AlertsBySeverity[severity] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stats rows: %w", err)
	}

	// Статистика по namespace
	rows, err = s.db.QueryContext(ctx, `
		SELECT json_extract(labels, '$.namespace') as namespace, COUNT(*)
		FROM alerts
		WHERE json_extract(labels, '$.namespace') IS NOT NULL
		GROUP BY json_extract(labels, '$.namespace')
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var namespace string
		var count int
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, fmt.Errorf("failed to scan namespace stats: %w", err)
		}
		stats.AlertsByNamespace[namespace] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stats rows: %w", err)
	}

	// Самый старый и новый алерты
	var oldestAlert, newestAlert *time.Time
	row = s.db.QueryRowContext(ctx, "SELECT MIN(starts_at), MAX(starts_at) FROM alerts")
	if err := row.Scan(&oldestAlert, &newestAlert); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get alert time range: %w", err)
	}

	stats.OldestAlert = oldestAlert
	stats.NewestAlert = newestAlert

	return stats, nil
}

// CleanupOldAlerts удаляет старые алерты
func (s *SQLiteDatabase) CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("not connected")
	}

	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)

	query := "DELETE FROM alerts WHERE starts_at < ?"
	result, err := s.db.ExecContext(ctx, query, cutoffDate)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old alerts: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	s.logger.Info("Old alerts cleaned up",
		"retention_days", retentionDays,
		"deleted_count", int(rowsAffected))

	return int(rowsAffected), nil
}
