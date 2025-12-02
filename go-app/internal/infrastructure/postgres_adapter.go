package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/ipiton/AMP/internal/core"
)

// PostgresDatabase адаптер для PostgreSQL, реализующий общий интерфейс Database
type PostgresDatabase struct {
	pool   *pgxpool.Pool
	config *Config
	logger *slog.Logger
	db     *sql.DB // Для совместимости с sql.Result
}

// NewPostgresDatabase создает новый PostgreSQL адаптер
func NewPostgresDatabase(config *Config) (*PostgresDatabase, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	pg := &PostgresDatabase{
		config: config,
		logger: config.Logger,
	}

	return pg, nil
}

// Connect устанавливает соединение с PostgreSQL
func (p *PostgresDatabase) Connect(ctx context.Context) error {
	p.logger.Info("Connecting to PostgreSQL", "dsn", p.config.DSN)

	// Создаем конфигурацию pgxpool
	poolConfig, err := pgxpool.ParseConfig(p.config.DSN)
	if err != nil {
		return fmt.Errorf("failed to parse database DSN: %w", err)
	}

	// Настраиваем параметры pool
	poolConfig.MaxConns = int32(p.config.MaxOpenConns)
	poolConfig.MinConns = int32(p.config.MaxIdleConns)
	poolConfig.MaxConnLifetime = p.config.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = p.config.ConnMaxIdleTime

	// Устанавливаем таймаут подключения
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Тестируем соединение
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	p.pool = pool
	p.db = stdlib.OpenDBFromPool(pool)

	connectionTime := time.Since(start)
	p.logger.Info("Successfully connected to PostgreSQL",
		"connection_time", connectionTime,
		"max_conns", p.config.MaxOpenConns)

	return nil
}

// Disconnect закрывает соединение с PostgreSQL
func (p *PostgresDatabase) Disconnect(ctx context.Context) error {
	if p.pool == nil {
		return nil
	}

	p.logger.Info("Disconnecting from PostgreSQL")

	p.pool.Close()
	p.pool = nil
	p.db = nil

	p.logger.Info("Successfully disconnected from PostgreSQL")
	return nil
}

// IsConnected проверяет состояние соединения
func (p *PostgresDatabase) IsConnected() bool {
	if p.pool == nil {
		return false
	}

	// Проверяем состояние pool
	stats := p.pool.Stat()
	return stats.TotalConns() > 0
}

// Health выполняет проверку здоровья базы данных
func (p *PostgresDatabase) Health(ctx context.Context) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	return p.pool.Ping(ctx)
}

// Exec выполняет SQL команду (адаптер для pgx)
func (p *PostgresDatabase) Exec(ctx context.Context, sql string, args ...interface{}) (sql.Result, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	tag, err := p.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}

	// Преобразуем pgconn.CommandTag в sql.Result совместимый формат
	return &postgresResult{tag: tag}, nil
}

// Query выполняет SQL запрос (адаптер для pgx)
func (p *PostgresDatabase) Query(ctx context.Context, sql string, args ...interface{}) (*sql.Rows, error) {
	if p.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	return p.db.QueryContext(ctx, sql, args...)
}

// QueryRow выполняет SQL запрос и возвращает одну строку
func (p *PostgresDatabase) QueryRow(ctx context.Context, sql string, args ...interface{}) *sql.Row {
	if p.db == nil {
		return nil
	}

	return p.db.QueryRowContext(ctx, sql, args...)
}

// Begin начинает новую транзакцию
func (p *PostgresDatabase) Begin(ctx context.Context) (*sql.Tx, error) {
	if p.db == nil {
		return nil, fmt.Errorf("not connected")
	}

	return p.db.BeginTx(ctx, nil)
}

// MigrateUp выполняет миграции схемы для PostgreSQL
// NOTE: В production используйте goose миграции из migrations/
// Этот метод для dev/test окружений
func (p *PostgresDatabase) MigrateUp(ctx context.Context) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	p.logger.Info("Running in-code migrations (simplified schema for dev/test)")

	// Создаём таблицу alerts (синхронизировано с миграцией 20250911094416)
	createAlertsTableSQL := `
	CREATE TABLE IF NOT EXISTS alerts (
		id BIGSERIAL PRIMARY KEY,
		fingerprint VARCHAR(64) NOT NULL UNIQUE,
		alert_name VARCHAR(255) NOT NULL,
		namespace VARCHAR(255),
		status VARCHAR(20) NOT NULL DEFAULT 'firing',
		labels JSONB NOT NULL DEFAULT '{}',
		annotations JSONB NOT NULL DEFAULT '{}',
		starts_at TIMESTAMP WITH TIME ZONE,
		ends_at TIMESTAMP WITH TIME ZONE,
		generator_url TEXT,
		timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_fingerprint ON alerts(fingerprint);
	CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);
	CREATE INDEX IF NOT EXISTS idx_alerts_namespace ON alerts(namespace);
	CREATE INDEX IF NOT EXISTS idx_alerts_starts_at ON alerts(starts_at);
	CREATE INDEX IF NOT EXISTS idx_alerts_labels_gin ON alerts USING GIN(labels);
	CREATE INDEX IF NOT EXISTS idx_alerts_annotations_gin ON alerts USING GIN(annotations);
	`

	_, err := p.pool.Exec(ctx, createAlertsTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create alerts table: %w", err)
	}

	// Создаем таблицу alert_classifications (синхронизировано с миграцией)
	createClassificationsTableSQL := `
	CREATE TABLE IF NOT EXISTS alert_classifications (
		id BIGSERIAL PRIMARY KEY,
		alert_fingerprint VARCHAR(64) NOT NULL,
		severity VARCHAR(20) NOT NULL,
		confidence DECIMAL(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
		reasoning TEXT,
		recommendations JSONB DEFAULT '[]',
		processing_time DECIMAL(8,3),
		metadata JSONB DEFAULT '{}',
		llm_model VARCHAR(100),
		llm_version VARCHAR(50),
		cache_hit BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_classifications_alert_fingerprint ON alert_classifications(alert_fingerprint);
	CREATE INDEX IF NOT EXISTS idx_classifications_severity ON alert_classifications(severity);
	CREATE INDEX IF NOT EXISTS idx_classifications_created_at ON alert_classifications(created_at);
	`

	_, err = p.pool.Exec(ctx, createClassificationsTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create classifications table: %w", err)
	}

	// Создаем таблицу alert_publishing_history (синхронизировано с миграцией)
	createPublishingTableSQL := `
	CREATE TABLE IF NOT EXISTS alert_publishing_history (
		id BIGSERIAL PRIMARY KEY,
		alert_fingerprint VARCHAR(64) NOT NULL,
		target_name VARCHAR(100) NOT NULL,
		target_type VARCHAR(50) NOT NULL,
		target_format VARCHAR(50) NOT NULL,
		status VARCHAR(20) NOT NULL,
		attempt_number INTEGER NOT NULL DEFAULT 1,
		response_code INTEGER,
		response_message TEXT,
		payload_size INTEGER,
		processing_time DECIMAL(8,3),
		error_details JSONB,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_publishing_history_fingerprint ON alert_publishing_history(alert_fingerprint);
	CREATE INDEX IF NOT EXISTS idx_publishing_history_target ON alert_publishing_history(target_name);
	CREATE INDEX IF NOT EXISTS idx_publishing_history_status ON alert_publishing_history(status);
	CREATE INDEX IF NOT EXISTS idx_publishing_history_created_at ON alert_publishing_history(created_at);
	`

	_, err = p.pool.Exec(ctx, createPublishingTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create publishing_history table: %w", err)
	}

	p.logger.Info("PostgreSQL in-code migration completed successfully",
		"tables_created", []string{"alerts", "alert_classifications", "alert_publishing_history"},
		"note", "For production use goose migrations")
	return nil
}

// SaveAlert сохраняет алерт в PostgreSQL (UPSERT)
func (p *PostgresDatabase) SaveAlert(ctx context.Context, alert *core.Alert) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	// Сериализуем labels и annotations в JSONB
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

	// Извлекаем namespace из labels
	var namespace *string
	if ns := alert.Namespace(); ns != nil {
		namespace = ns
	}

	query := `
		INSERT INTO alerts (
			fingerprint, alert_name, status, labels, annotations,
			starts_at, ends_at, generator_url, namespace, timestamp
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (fingerprint)
		DO UPDATE SET
			alert_name = EXCLUDED.alert_name,
			status = EXCLUDED.status,
			labels = EXCLUDED.labels,
			annotations = EXCLUDED.annotations,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			generator_url = EXCLUDED.generator_url,
			namespace = EXCLUDED.namespace,
			timestamp = EXCLUDED.timestamp,
			updated_at = NOW()`

	_, err = p.pool.Exec(ctx, query,
		alert.Fingerprint,
		alert.AlertName,
		string(alert.Status),
		labelsJSON,
		annotationsJSON,
		alert.StartsAt,
		alert.EndsAt,
		alert.GeneratorURL,
		namespace,
		alert.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("failed to save alert: %w", err)
	}

	p.logger.Debug("Alert saved successfully", "fingerprint", alert.Fingerprint)
	return nil
}

// GetAlertByFingerprint получает алерт по fingerprint из PostgreSQL
func (p *PostgresDatabase) GetAlertByFingerprint(ctx context.Context, fingerprint string) (*core.Alert, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := `
		SELECT fingerprint, alert_name, status, labels, annotations,
		       starts_at, ends_at, generator_url, timestamp
		FROM alerts
		WHERE fingerprint = $1`

	row := p.pool.QueryRow(ctx, query, fingerprint)

	alert := &core.Alert{}
	var labelsJSON, annotationsJSON []byte
	var endsAt, generatorURL, timestamp interface{}

	err := row.Scan(
		&alert.Fingerprint,
		&alert.AlertName,
		&alert.Status,
		&labelsJSON,
		&annotationsJSON,
		&alert.StartsAt,
		&endsAt,
		&generatorURL,
		&timestamp,
	)

	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil // Alert not found
		}
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}

	// Десериализуем JSONB поля
	if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}

	if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
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

// ListAlerts получает список алертов с фильтрами и пагинацией
func (p *PostgresDatabase) ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error) {
	if p.pool == nil {
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
	argCount := 0

	// Фильтр по статусу
	if filters.Status != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, string(*filters.Status))
	}

	// Фильтр по severity (из labels)
	if filters.Severity != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND labels->>'severity' = $%d", argCount)
		args = append(args, *filters.Severity)
	}

	// Фильтр по namespace
	if filters.Namespace != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND namespace = $%d", argCount)
		args = append(args, *filters.Namespace)
	}

	// Фильтр по времени
	if filters.TimeRange != nil {
		if filters.TimeRange.From != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at >= $%d", argCount)
			args = append(args, *filters.TimeRange.From)
		}
		if filters.TimeRange.To != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at <= $%d", argCount)
			args = append(args, *filters.TimeRange.To)
		}
	}

	// Фильтры по labels (JSONB contains)
	if len(filters.Labels) > 0 {
		labelsFilter, err := json.Marshal(filters.Labels)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		argCount++
		whereClause += fmt.Sprintf(" AND labels @> $%d", argCount)
		args = append(args, labelsFilter)
	}

	// Получаем общее количество
	countQuery := "SELECT COUNT(*) FROM alerts " + whereClause
	var total int
	if err := p.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
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
		argCount++
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
	}

	if filters.Offset > 0 {
		argCount++
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
	}

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query alerts: %w", err)
	}
	defer rows.Close()

	var alerts []*core.Alert
	for rows.Next() {
		alert := &core.Alert{}
		var labelsJSON, annotationsJSON []byte
		var endsAt, generatorURL, timestamp interface{}

		err := rows.Scan(
			&alert.Fingerprint,
			&alert.AlertName,
			&alert.Status,
			&labelsJSON,
			&annotationsJSON,
			&alert.StartsAt,
			&endsAt,
			&generatorURL,
			&timestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}

		// Десериализуем JSONB поля
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}

		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
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
func (p *PostgresDatabase) UpdateAlert(ctx context.Context, alert *core.Alert) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	// Сериализуем labels и annotations в JSONB
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

	// Извлекаем namespace из labels
	var namespace *string
	if ns := alert.Namespace(); ns != nil {
		namespace = ns
	}

	query := `
		UPDATE alerts SET
			alert_name = $2,
			status = $3,
			labels = $4,
			annotations = $5,
			starts_at = $6,
			ends_at = $7,
			generator_url = $8,
			namespace = $9,
			timestamp = $10,
			updated_at = NOW()
		WHERE fingerprint = $1`

	result, err := p.pool.Exec(ctx, query,
		alert.Fingerprint,
		alert.AlertName,
		string(alert.Status),
		labelsJSON,
		annotationsJSON,
		alert.StartsAt,
		alert.EndsAt,
		alert.GeneratorURL,
		namespace,
		alert.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("failed to update alert: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("alert not found: %s", alert.Fingerprint)
	}

	p.logger.Debug("Alert updated successfully", "fingerprint", alert.Fingerprint)
	return nil
}

// DeleteAlert удаляет алерт по fingerprint
func (p *PostgresDatabase) DeleteAlert(ctx context.Context, fingerprint string) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	query := "DELETE FROM alerts WHERE fingerprint = $1"

	result, err := p.pool.Exec(ctx, query, fingerprint)
	if err != nil {
		return fmt.Errorf("failed to delete alert: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("alert not found: %s", fingerprint)
	}

	p.logger.Debug("Alert deleted successfully", "fingerprint", fingerprint)
	return nil
}

// GetAlertStats возвращает статистику по алертам (реализация AlertStorage интерфейса)
func (p *PostgresDatabase) GetAlertStats(ctx context.Context) (*core.AlertStats, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	stats := &core.AlertStats{
		AlertsByStatus:    make(map[string]int),
		AlertsBySeverity:  make(map[string]int),
		AlertsByNamespace: make(map[string]int),
	}

	// Общее количество алертов
	row := p.pool.QueryRow(ctx, "SELECT COUNT(*) FROM alerts")
	if err := row.Scan(&stats.TotalAlerts); err != nil {
		return nil, fmt.Errorf("failed to get total alerts count: %w", err)
	}

	// Статистика по статусам
	rows, err := p.pool.Query(ctx, "SELECT status, COUNT(*) FROM alerts GROUP BY status")
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
	rows, err = p.pool.Query(ctx, `
		SELECT labels->>'severity' as severity, COUNT(*)
		FROM alerts
		WHERE labels->>'severity' IS NOT NULL
		GROUP BY labels->>'severity'
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
	rows, err = p.pool.Query(ctx, `
		SELECT namespace, COUNT(*)
		FROM alerts
		WHERE namespace IS NOT NULL
		GROUP BY namespace
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
	row = p.pool.QueryRow(ctx, "SELECT MIN(starts_at), MAX(starts_at) FROM alerts")
	if err := row.Scan(&oldestAlert, &newestAlert); err != nil && err.Error() != "no rows in result set" {
		return nil, fmt.Errorf("failed to get alert time range: %w", err)
	}

	stats.OldestAlert = oldestAlert
	stats.NewestAlert = newestAlert

	return stats, nil
}

// CleanupOldAlerts удаляет старые алерты из PostgreSQL
func (p *PostgresDatabase) CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error) {
	if p.pool == nil {
		return 0, fmt.Errorf("not connected")
	}

	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)

	query := "DELETE FROM alerts WHERE starts_at < $1"

	result, err := p.pool.Exec(ctx, query, cutoffDate)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old alerts: %w", err)
	}

	rowsAffected := result.RowsAffected()
	p.logger.Info("Old alerts cleaned up",
		"retention_days", retentionDays,
		"deleted_count", int(rowsAffected))

	return int(rowsAffected), nil
}

// SaveClassification сохраняет результат классификации
func (p *PostgresDatabase) SaveClassification(ctx context.Context, fingerprint string, result *core.ClassificationResult) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	classificationData, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal classification: %w", err)
	}

	query := `
		INSERT INTO classifications (alert_fingerprint, classification_data, confidence, category)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (alert_fingerprint)
		DO UPDATE SET
			classification_data = EXCLUDED.classification_data,
			confidence = EXCLUDED.confidence,
			category = EXCLUDED.category,
			updated_at = NOW()`

	_, err = p.pool.Exec(ctx, query, fingerprint, classificationData, result.Confidence, string(result.Severity))
	if err != nil {
		return fmt.Errorf("failed to save classification: %w", err)
	}

	return nil
}

// GetClassification получает результат классификации
func (p *PostgresDatabase) GetClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := "SELECT classification_data FROM classifications WHERE alert_fingerprint = $1"

	var classificationData []byte
	err := p.pool.QueryRow(ctx, query, fingerprint).Scan(&classificationData)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil // Classification not found
		}
		return nil, fmt.Errorf("failed to get classification: %w", err)
	}

	var result core.ClassificationResult
	if err := json.Unmarshal(classificationData, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal classification: %w", err)
	}

	return &result, nil
}

// LogPublishingAttempt логирует попытку публикации
func (p *PostgresDatabase) LogPublishingAttempt(ctx context.Context, fingerprint, targetName string, success bool, errorMessage *string, processingTime *float64) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	query := `
		INSERT INTO publishing_logs (
			alert_fingerprint, target_name, success, error_message,
			processing_time, created_at
		) VALUES ($1, $2, $3, $4, $5, NOW())`

	_, err := p.pool.Exec(ctx, query, fingerprint, targetName, success, errorMessage, processingTime)
	if err != nil {
		return fmt.Errorf("failed to log publishing attempt: %w", err)
	}

	return nil
}

// GetPublishingHistory получает историю публикаций для алерта
func (p *PostgresDatabase) GetPublishingHistory(ctx context.Context, fingerprint string) ([]*core.PublishingLog, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	query := `
		SELECT id, alert_fingerprint, target_name, success,
			   error_message, processing_time, created_at
		FROM publishing_logs
		WHERE alert_fingerprint = $1
		ORDER BY created_at DESC`

	rows, err := p.pool.Query(ctx, query, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("failed to query publishing history: %w", err)
	}
	defer rows.Close()

	var logs []*core.PublishingLog
	for rows.Next() {
		log := &core.PublishingLog{}
		err := rows.Scan(
			&log.ID, &log.Fingerprint, &log.TargetName, &log.Success,
			&log.ErrorMessage, &log.ProcessingTime, &log.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan publishing log: %w", err)
		}
		logs = append(logs, log)
	}

	return logs, nil
}

// MigrateDown выполняет rollback миграций (не поддерживается для PostgreSQL)
func (p *PostgresDatabase) MigrateDown(ctx context.Context, steps int) error {
	p.logger.Info("PostgreSQL doesn't support complex migrations rollback",
		"recommendation", "Use database backups for rollback")
	return fmt.Errorf("PostgreSQL doesn't support complex migrations rollback")
}

// GetStats возвращает статистику PostgreSQL базы данных
func (p *PostgresDatabase) GetStats(ctx context.Context) (map[string]interface{}, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	stats := make(map[string]interface{})

	// Общая статистика
	row := p.pool.QueryRow(ctx, "SELECT COUNT(*) FROM alerts")
	var alertCount int
	if err := row.Scan(&alertCount); err != nil {
		return nil, fmt.Errorf("failed to get alert count: %w", err)
	}
	stats["alerts_count"] = alertCount

	// Статистика по классификациям
	row = p.pool.QueryRow(ctx, "SELECT COUNT(*) FROM classifications")
	var classificationCount int
	if err := row.Scan(&classificationCount); err != nil {
		return nil, fmt.Errorf("failed to get classification count: %w", err)
	}
	stats["classifications_count"] = classificationCount

	// Статистика по публикациям
	row = p.pool.QueryRow(ctx, "SELECT COUNT(*) FROM publishing_logs")
	var publishingCount int
	if err := row.Scan(&publishingCount); err != nil {
		return nil, fmt.Errorf("failed to get publishing count: %w", err)
	}
	stats["publishing_count"] = publishingCount

	// Connection pool stats
	poolStats := p.pool.Stat()
	stats["total_connections"] = poolStats.TotalConns()
	stats["idle_connections"] = poolStats.IdleConns()
	stats["acquired_connections"] = poolStats.AcquiredConns()
	stats["empty_acquire_count"] = poolStats.EmptyAcquireCount()

	return stats, nil
}

// postgresResult адаптер для преобразования pgconn.CommandTag в sql.Result
type postgresResult struct {
	tag pgconn.CommandTag
}

func (r *postgresResult) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("LastInsertId not supported for PostgreSQL")
}

func (r *postgresResult) RowsAffected() (int64, error) {
	return r.tag.RowsAffected(), nil
}
