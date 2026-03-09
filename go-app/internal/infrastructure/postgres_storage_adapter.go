package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipiton/AMP/internal/core"
)

// PostgresStorageAdapter exposes AlertStorage over an existing pgx pool.
//
// Lifecycle ownership stays with the caller; Disconnect is intentionally a no-op
// so ServiceRegistry can keep PostgresPool as the single connection owner.
type PostgresStorageAdapter struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewPostgresStorageAdapter(pool *pgxpool.Pool, logger *slog.Logger) (*PostgresStorageAdapter, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pool is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &PostgresStorageAdapter{
		pool:   pool,
		logger: logger,
	}, nil
}

func (p *PostgresStorageAdapter) Health(ctx context.Context) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}
	return p.pool.Ping(ctx)
}

func (p *PostgresStorageAdapter) Disconnect(ctx context.Context) error {
	_ = ctx
	return nil
}

func (p *PostgresStorageAdapter) SaveAlert(ctx context.Context, alert *core.Alert) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

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

	if _, err := p.pool.Exec(ctx, query,
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
	); err != nil {
		return fmt.Errorf("failed to save alert: %w", err)
	}

	return nil
}

func (p *PostgresStorageAdapter) GetAlertByFingerprint(ctx context.Context, fingerprint string) (*core.Alert, error) {
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, core.ErrAlertNotFound
		}
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}

	if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}
	if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
	}

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

func (p *PostgresStorageAdapter) ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	if filters == nil {
		filters = &core.AlertFilters{
			Limit:  100,
			Offset: 0,
		}
	}

	whereClause := "WHERE 1=1"
	args := make([]any, 0, 8)
	argCount := 0

	if filters.Status != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, string(*filters.Status))
	}
	if filters.Severity != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND labels->>'severity' = $%d", argCount)
		args = append(args, *filters.Severity)
	}
	if filters.Namespace != nil {
		argCount++
		whereClause += fmt.Sprintf(" AND namespace = $%d", argCount)
		args = append(args, *filters.Namespace)
	}
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
	if len(filters.Labels) > 0 {
		labelsFilter, err := json.Marshal(filters.Labels)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		argCount++
		whereClause += fmt.Sprintf(" AND labels @> $%d", argCount)
		args = append(args, labelsFilter)
	}

	countQuery := "SELECT COUNT(*) FROM alerts " + whereClause
	var total int
	if err := p.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alerts: %w", err)
	}

	query := `
		SELECT fingerprint, alert_name, status, labels, annotations,
		       starts_at, ends_at, generator_url, timestamp
		FROM alerts ` + whereClause + `
		ORDER BY starts_at DESC`

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

	alerts := make([]*core.Alert, 0, filters.Limit)
	for rows.Next() {
		alert := &core.Alert{}
		var labelsJSON, annotationsJSON []byte
		var endsAt, generatorURL, timestamp interface{}

		if err := rows.Scan(
			&alert.Fingerprint,
			&alert.AlertName,
			&alert.Status,
			&labelsJSON,
			&annotationsJSON,
			&alert.StartsAt,
			&endsAt,
			&generatorURL,
			&timestamp,
		); err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}

		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
		}

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

func (p *PostgresStorageAdapter) UpdateAlert(ctx context.Context, alert *core.Alert) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}
	annotationsJSON, err := json.Marshal(alert.Annotations)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

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
		return core.ErrAlertNotFound
	}

	return nil
}

func (p *PostgresStorageAdapter) DeleteAlert(ctx context.Context, fingerprint string) error {
	if p.pool == nil {
		return fmt.Errorf("not connected")
	}

	result, err := p.pool.Exec(ctx, "DELETE FROM alerts WHERE fingerprint = $1", fingerprint)
	if err != nil {
		return fmt.Errorf("failed to delete alert: %w", err)
	}
	if result.RowsAffected() == 0 {
		return core.ErrAlertNotFound
	}

	return nil
}

func (p *PostgresStorageAdapter) GetAlertStats(ctx context.Context) (*core.AlertStats, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}

	stats := &core.AlertStats{
		AlertsByStatus:    make(map[string]int),
		AlertsBySeverity:  make(map[string]int),
		AlertsByNamespace: make(map[string]int),
	}

	if err := p.pool.QueryRow(ctx, "SELECT COUNT(*) FROM alerts").Scan(&stats.TotalAlerts); err != nil {
		return nil, fmt.Errorf("failed to get total alerts count: %w", err)
	}

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

	var oldestAlert, newestAlert *time.Time
	if err := p.pool.QueryRow(ctx, "SELECT MIN(starts_at), MAX(starts_at) FROM alerts").Scan(&oldestAlert, &newestAlert); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to get alert time range: %w", err)
	}

	stats.OldestAlert = oldestAlert
	stats.NewestAlert = newestAlert
	return stats, nil
}

func (p *PostgresStorageAdapter) CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error) {
	if p.pool == nil {
		return 0, fmt.Errorf("not connected")
	}

	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)
	result, err := p.pool.Exec(ctx, "DELETE FROM alerts WHERE starts_at < $1", cutoffDate)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old alerts: %w", err)
	}

	rowsAffected := int(result.RowsAffected())
	p.logger.Info("Old alerts cleaned up",
		"retention_days", retentionDays,
		"deleted_count", rowsAffected,
	)
	return rowsAffected, nil
}
