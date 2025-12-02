package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
		db.Close()
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Включаем WAL mode для лучшей производительности
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		s.logger.Warn("Failed to enable WAL mode", "error", err)
	}

	// Тестируем соединение
	if err := db.PingContext(ctx); err != nil {
		db.Close()
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
			return nil, nil // Alert not found
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

	// Фильтры по labels (JSON contains - упрощённая версия для SQLite)
	for key, value := range filters.Labels {
		whereClause += " AND json_extract(labels, '$." + key + "') = ?"
		args = append(args, value)
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
	defer rows.Close()

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
		return fmt.Errorf("alert not found: %s", alert.Fingerprint)
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
		return fmt.Errorf("alert not found: %s", fingerprint)
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
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan status stats: %w", err)
		}
		stats.AlertsByStatus[status] = count
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
	defer rows.Close()

	for rows.Next() {
		var severity string
		var count int
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, fmt.Errorf("failed to scan severity stats: %w", err)
		}
		stats.AlertsBySeverity[severity] = count
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
	defer rows.Close()

	for rows.Next() {
		var namespace string
		var count int
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, fmt.Errorf("failed to scan namespace stats: %w", err)
		}
		stats.AlertsByNamespace[namespace] = count
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

// SaveClassification сохраняет результат классификации
func (s *SQLiteDatabase) SaveClassification(ctx context.Context, fingerprint string, result *core.ClassificationResult) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	metadataJSON, err := json.Marshal(result.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	recommendationsJSON, err := json.Marshal(result.Recommendations)
	if err != nil {
		return fmt.Errorf("failed to marshal recommendations: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO classifications (
			id, alert_fingerprint, category, confidence, reasoning,
			recommendations, metadata, processing_time, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	id := fmt.Sprintf("%s_classification", fingerprint)
	now := time.Now()

	_, err = s.db.ExecContext(ctx, query,
		id, fingerprint, string(result.Severity), result.Confidence,
		result.Reasoning, string(recommendationsJSON),
		string(metadataJSON), result.ProcessingTime, now,
	)

	if err != nil {
		return fmt.Errorf("failed to save classification: %w", err)
	}

	return nil
}

// GetClassification получает результат классификации
func (s *SQLiteDatabase) GetClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := `
		SELECT category, confidence, reasoning, recommendations, metadata, processing_time
		FROM classifications WHERE alert_fingerprint = ?`

	row := s.db.QueryRowContext(ctx, query, fingerprint)

	result := &core.ClassificationResult{}
	var recommendationsJSON, metadataJSON string

	err := row.Scan(
		&result.Severity, &result.Confidence, &result.Reasoning,
		&recommendationsJSON, &metadataJSON, &result.ProcessingTime,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Classification not found
		}
		return nil, fmt.Errorf("failed to get classification: %w", err)
	}

	// Десериализуем JSON поля
	if err := json.Unmarshal([]byte(recommendationsJSON), &result.Recommendations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal recommendations: %w", err)
	}

	if err := json.Unmarshal([]byte(metadataJSON), &result.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return result, nil
}

// LogPublishingAttempt логирует попытку публикации
func (s *SQLiteDatabase) LogPublishingAttempt(ctx context.Context, fingerprint, targetName string, success bool, errorMessage *string, processingTime *float64) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	query := `
		INSERT INTO publishing (
			id, alert_fingerprint, channel, status, error_message,
			processing_time, sent_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	id := fmt.Sprintf("%s_%s_%d", fingerprint, targetName, time.Now().Unix())
	status := "failed"
	var sentAt *time.Time

	if success {
		status = "sent"
		now := time.Now()
		sentAt = &now
	}

	_, err := s.db.ExecContext(ctx, query,
		id, fingerprint, targetName, status, errorMessage,
		processingTime, sentAt, time.Now(),
	)

	if err != nil {
		return fmt.Errorf("failed to log publishing attempt: %w", err)
	}

	return nil
}

// GetPublishingHistory получает историю публикаций для алерта
func (s *SQLiteDatabase) GetPublishingHistory(ctx context.Context, fingerprint string) ([]*core.PublishingLog, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := `
		SELECT id, alert_fingerprint, channel, status, error_message,
			   processing_time, sent_at, created_at
		FROM publishing WHERE alert_fingerprint = ?
		ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("failed to query publishing history: %w", err)
	}
	defer rows.Close()

	var logs []*core.PublishingLog
	for rows.Next() {
		log := &core.PublishingLog{}
		var sentAt interface{}

		var status string
		err := rows.Scan(
			&log.ID, &log.Fingerprint, &log.TargetName, &status,
			&log.ErrorMessage, &log.ProcessingTime, &sentAt, &log.CreatedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan publishing log: %w", err)
		}

		// Конвертируем статус в bool
		log.Success = strings.ToLower(status) == "sent"

		logs = append(logs, log)
	}

	return logs, nil
}

// MigrateDown выполняет откат миграций (упрощенная версия для SQLite)
func (s *SQLiteDatabase) MigrateDown(ctx context.Context, steps int) error {
	if s.db == nil {
		return fmt.Errorf("not connected")
	}

	s.logger.Info("SQLite doesn't support complex migrations rollback",
		"steps", steps,
		"recommendation", "Use backup/restore for rollback")

	return fmt.Errorf("SQLite doesn't support complex migrations rollback")
}

// GetStats возвращает статистику базы данных
func (s *SQLiteDatabase) GetStats(ctx context.Context) (map[string]interface{}, error) {
	if s.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	stats := make(map[string]interface{})

	// Общая статистика
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts")
	var alertCount int
	if err := row.Scan(&alertCount); err != nil {
		return nil, fmt.Errorf("failed to get alert count: %w", err)
	}
	stats["alerts_count"] = alertCount

	// Статистика по классификациям
	row = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM classifications")
	var classificationCount int
	if err := row.Scan(&classificationCount); err != nil {
		return nil, fmt.Errorf("failed to get classification count: %w", err)
	}
	stats["classifications_count"] = classificationCount

	// Статистика по публикациям
	row = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM publishing")
	var publishingCount int
	if err := row.Scan(&publishingCount); err != nil {
		return nil, fmt.Errorf("failed to get publishing count: %w", err)
	}
	stats["publishing_count"] = publishingCount

	// Размер базы данных
	row = s.db.QueryRowContext(ctx, "SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()")
	var dbSize int64
	if err := row.Scan(&dbSize); err != nil {
		s.logger.Warn("Failed to get database size", "error", err)
		stats["database_size"] = "unknown"
	} else {
		stats["database_size"] = fmt.Sprintf("%d bytes", dbSize)
	}

	// Connection pool stats
	dbStats := s.db.Stats()
	stats["open_connections"] = dbStats.OpenConnections
	stats["in_use_connections"] = dbStats.InUse
	stats["idle_connections"] = dbStats.Idle
	stats["wait_count"] = dbStats.WaitCount
	stats["wait_duration"] = dbStats.WaitDuration.String()

	return stats, nil
}
