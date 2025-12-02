package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/ipiton/AMP/internal/core"
)

// PostgresHistoryRepository implements AlertHistoryRepository for PostgreSQL
type PostgresHistoryRepository struct {
	pool    *pgxpool.Pool
	storage core.AlertStorage
	logger  *slog.Logger
	metrics *HistoryMetrics
}

// HistoryMetrics contains Prometheus metrics for history operations
type HistoryMetrics struct {
	QueryDuration *prometheus.HistogramVec
	QueryErrors   *prometheus.CounterVec
	QueryResults  *prometheus.HistogramVec
	CacheHits     *prometheus.CounterVec
}

// NewPostgresHistoryRepository creates a new PostgreSQL history repository
func NewPostgresHistoryRepository(pool *pgxpool.Pool, storage core.AlertStorage, logger *slog.Logger) *PostgresHistoryRepository {
	if logger == nil {
		logger = slog.Default()
	}

	// FIXED: Added Namespace and Subsystem for proper metric naming
	// Old: alert_history_query_duration_seconds (no subsystem)
	// New: alert_history_infra_repository_query_duration_seconds
	metrics := &HistoryMetrics{
		QueryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "infra_repository",
				Name:      "query_duration_seconds",
				Help:      "Duration of alert history queries",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"operation", "status"},
		),
		QueryErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "infra_repository",
				Name:      "query_errors_total",
				Help:      "Total number of alert history query errors",
			},
			[]string{"operation", "error_type"},
		),
		QueryResults: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "infra_repository",
				Name:      "query_results_total",
				Help:      "Number of results returned by history queries",
				Buckets:   []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000},
			},
			[]string{"operation"},
		),
		CacheHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "infra_cache",
				Name:      "hits_total",
				Help:      "Total number of cache hits for history queries",
			},
			[]string{"cache_type"},
		),
	}

	return &PostgresHistoryRepository{
		pool:    pool,
		storage: storage,
		logger:  logger,
		metrics: metrics,
	}
}

// GetHistory retrieves paginated alert history with advanced filtering and sorting
func (r *PostgresHistoryRepository) GetHistory(ctx context.Context, req *core.HistoryRequest) (*core.HistoryResponse, error) {
	start := time.Now()
	operation := "get_history"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
		r.logger.Debug("GetHistory completed",
			"duration_ms", duration*1000,
			"page", req.Pagination.Page,
			"per_page", req.Pagination.PerPage)
	}()

	// Validate request
	if err := req.Validate(); err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "validation").Inc()
		return nil, fmt.Errorf("invalid history request: %w", err)
	}

	// Use existing AlertStorage for listing
	filters := req.Filters
	if filters == nil {
		filters = &core.AlertFilters{}
	}
	filters.Limit = req.Pagination.PerPage
	filters.Offset = req.Pagination.Offset()

	// Get alerts using existing storage
	alertList, err := r.storage.ListAlerts(ctx, filters)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "database").Inc()
		return nil, fmt.Errorf("failed to list alerts: %w", err)
	}

	// Calculate pagination metadata
	totalPages := int(math.Ceil(float64(alertList.Total) / float64(req.Pagination.PerPage)))

	response := &core.HistoryResponse{
		Alerts:     alertList.Alerts,
		Total:      int64(alertList.Total),
		Page:       req.Pagination.Page,
		PerPage:    req.Pagination.PerPage,
		TotalPages: totalPages,
		HasNext:    req.Pagination.Page < totalPages,
		HasPrev:    req.Pagination.Page > 1,
	}

	r.metrics.QueryResults.WithLabelValues(operation).Observe(float64(len(alertList.Alerts)))

	return response, nil
}

// GetAlertsByFingerprint retrieves all alerts with the same fingerprint
func (r *PostgresHistoryRepository) GetAlertsByFingerprint(ctx context.Context, fingerprint string, limit int) ([]*core.Alert, error) {
	start := time.Now()
	operation := "get_alerts_by_fingerprint"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	if fingerprint == "" {
		r.metrics.QueryErrors.WithLabelValues(operation, "validation").Inc()
		return nil, fmt.Errorf("fingerprint cannot be empty")
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	query := `
		SELECT fingerprint, alert_name, status, labels, annotations,
		       starts_at, ends_at, generator_url, timestamp
		FROM alerts
		WHERE fingerprint = $1
		ORDER BY starts_at DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, fingerprint, limit)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "database").Inc()
		return nil, fmt.Errorf("failed to query alerts by fingerprint: %w", err)
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
			r.metrics.QueryErrors.WithLabelValues(operation, "scan").Inc()
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}

		// Deserialize JSONB fields
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
		}

		// Handle nullable fields
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

	r.metrics.QueryResults.WithLabelValues(operation).Observe(float64(len(alerts)))

	return alerts, nil
}

// GetRecentAlerts retrieves the most recent alerts across all fingerprints
func (r *PostgresHistoryRepository) GetRecentAlerts(ctx context.Context, limit int) ([]*core.Alert, error) {
	start := time.Now()
	operation := "get_recent_alerts"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	// Use existing storage with appropriate filters
	filters := &core.AlertFilters{
		Limit:  limit,
		Offset: 0,
	}

	alertList, err := r.storage.ListAlerts(ctx, filters)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "database").Inc()
		return nil, fmt.Errorf("failed to get recent alerts: %w", err)
	}

	r.metrics.QueryResults.WithLabelValues(operation).Observe(float64(len(alertList.Alerts)))

	return alertList.Alerts, nil
}

// GetAggregatedStats computes statistical aggregations over a time range
func (r *PostgresHistoryRepository) GetAggregatedStats(ctx context.Context, timeRange *core.TimeRange) (*core.AggregatedStats, error) {
	start := time.Now()
	operation := "get_aggregated_stats"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	stats := &core.AggregatedStats{
		TimeRange:         timeRange,
		AlertsByStatus:    make(map[string]int64),
		AlertsBySeverity:  make(map[string]int64),
		AlertsByNamespace: make(map[string]int64),
	}

	// Build time range filter
	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argCount := 0

	if timeRange != nil {
		if timeRange.From != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at >= $%d", argCount)
			args = append(args, *timeRange.From)
		}
		if timeRange.To != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at <= $%d", argCount)
			args = append(args, *timeRange.To)
		}
	}

	// Total alerts
	totalQuery := fmt.Sprintf("SELECT COUNT(*) FROM alerts %s", whereClause)
	err := r.pool.QueryRow(ctx, totalQuery, args...).Scan(&stats.TotalAlerts)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "total_count").Inc()
		return nil, fmt.Errorf("failed to count total alerts: %w", err)
	}

	// Firing vs Resolved
	statusQuery := fmt.Sprintf(`
		SELECT status, COUNT(*)
		FROM alerts %s
		GROUP BY status`, whereClause)

	rows, err := r.pool.Query(ctx, statusQuery, args...)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "status").Inc()
		return nil, fmt.Errorf("failed to query status stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan status: %w", err)
		}
		stats.AlertsByStatus[status] = count
		if status == "firing" {
			stats.FiringAlerts = count
		} else if status == "resolved" {
			stats.ResolvedAlerts = count
		}
	}

	// Severity distribution
	severityQuery := fmt.Sprintf(`
		SELECT labels->>'severity' as severity, COUNT(*)
		FROM alerts %s
		WHERE labels->>'severity' IS NOT NULL
		GROUP BY severity`, whereClause)

	rows, err = r.pool.Query(ctx, severityQuery, args...)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "severity").Inc()
		return nil, fmt.Errorf("failed to query severity stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var severity string
		var count int64
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, fmt.Errorf("failed to scan severity: %w", err)
		}
		stats.AlertsBySeverity[severity] = count
	}

	// Namespace distribution (top 10)
	namespaceQuery := fmt.Sprintf(`
		SELECT labels->>'namespace' as namespace, COUNT(*)
		FROM alerts %s
		WHERE labels->>'namespace' IS NOT NULL
		GROUP BY namespace
		ORDER BY COUNT(*) DESC
		LIMIT 10`, whereClause)

	rows, err = r.pool.Query(ctx, namespaceQuery, args...)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "namespace").Inc()
		return nil, fmt.Errorf("failed to query namespace stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var namespace string
		var count int64
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, fmt.Errorf("failed to scan namespace: %w", err)
		}
		stats.AlertsByNamespace[namespace] = count
	}

	// Unique fingerprints
	fingerprintQuery := fmt.Sprintf("SELECT COUNT(DISTINCT fingerprint) FROM alerts %s", whereClause)
	err = r.pool.QueryRow(ctx, fingerprintQuery, args...).Scan(&stats.UniqueFingerprints)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "fingerprints").Inc()
		return nil, fmt.Errorf("failed to count unique fingerprints: %w", err)
	}

	// Average resolution time for resolved alerts
	avgResolutionQuery := fmt.Sprintf(`
		SELECT AVG(EXTRACT(EPOCH FROM (ends_at - starts_at)))
		FROM alerts
		%s AND status = 'resolved' AND ends_at IS NOT NULL`, whereClause)

	var avgSeconds *float64
	err = r.pool.QueryRow(ctx, avgResolutionQuery, args...).Scan(&avgSeconds)
	if err != nil && err.Error() != "no rows in result set" {
		r.logger.Warn("Failed to calculate avg resolution time", "error", err)
	}
	if avgSeconds != nil && *avgSeconds > 0 {
		duration := time.Duration(*avgSeconds * float64(time.Second))
		stats.AvgResolutionTime = &duration
	}

	return stats, nil
}

// GetTopAlerts returns the most frequently firing alerts
func (r *PostgresHistoryRepository) GetTopAlerts(ctx context.Context, timeRange *core.TimeRange, limit int) ([]*core.TopAlert, error) {
	start := time.Now()
	operation := "get_top_alerts"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	// Build time range filter
	whereClause := "WHERE status = 'firing'"
	args := []interface{}{}
	argCount := 0

	if timeRange != nil {
		if timeRange.From != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at >= $%d", argCount)
			args = append(args, *timeRange.From)
		}
		if timeRange.To != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at <= $%d", argCount)
			args = append(args, *timeRange.To)
		}
	}

	argCount++
	query := fmt.Sprintf(`
		SELECT
			fingerprint,
			alert_name,
			labels->>'namespace' as namespace,
			COUNT(*) as fire_count,
			MAX(starts_at) as last_fired_at,
			AVG(EXTRACT(EPOCH FROM (COALESCE(ends_at, NOW()) - starts_at))) as avg_duration
		FROM alerts
		%s
		GROUP BY fingerprint, alert_name, labels->>'namespace'
		ORDER BY fire_count DESC
		LIMIT $%d`, whereClause, argCount)

	args = append(args, limit)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "database").Inc()
		return nil, fmt.Errorf("failed to query top alerts: %w", err)
	}
	defer rows.Close()

	var topAlerts []*core.TopAlert
	for rows.Next() {
		alert := &core.TopAlert{}
		var namespace interface{}
		var avgDuration *float64

		err := rows.Scan(
			&alert.Fingerprint,
			&alert.AlertName,
			&namespace,
			&alert.FireCount,
			&alert.LastFiredAt,
			&avgDuration,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan top alert: %w", err)
		}

		if namespace != nil {
			if ns, ok := namespace.(string); ok {
				alert.Namespace = &ns
			}
		}

		alert.AvgDuration = avgDuration
		topAlerts = append(topAlerts, alert)
	}

	r.metrics.QueryResults.WithLabelValues(operation).Observe(float64(len(topAlerts)))

	return topAlerts, nil
}

// GetFlappingAlerts detects alerts that frequently transition between states
func (r *PostgresHistoryRepository) GetFlappingAlerts(ctx context.Context, timeRange *core.TimeRange, threshold int) ([]*core.FlappingAlert, error) {
	start := time.Now()
	operation := "get_flapping_alerts"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.QueryDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	if threshold <= 0 {
		threshold = 3 // Default: at least 3 state transitions
	}

	// Build time range filter
	whereClause := "WHERE 1=1"
	args := []interface{}{}
	argCount := 0

	if timeRange != nil {
		if timeRange.From != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at >= $%d", argCount)
			args = append(args, *timeRange.From)
		}
		if timeRange.To != nil {
			argCount++
			whereClause += fmt.Sprintf(" AND starts_at <= $%d", argCount)
			args = append(args, *timeRange.To)
		}
	}

	argCount++
	query := fmt.Sprintf(`
		WITH state_changes AS (
			SELECT
				fingerprint,
				alert_name,
				labels->>'namespace' as namespace,
				status,
				starts_at,
				LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at) as prev_status
			FROM alerts
			%s
		),
		transition_counts AS (
			SELECT
				fingerprint,
				alert_name,
				namespace,
				COUNT(*) FILTER (WHERE status != prev_status) as transition_count,
				MAX(starts_at) as last_transition_at
			FROM state_changes
			WHERE prev_status IS NOT NULL
			GROUP BY fingerprint, alert_name, namespace
		)
		SELECT
			fingerprint,
			alert_name,
			namespace,
			transition_count,
			CAST(transition_count AS FLOAT) / EXTRACT(EPOCH FROM (NOW() - last_transition_at)) * 3600 as flapping_score,
			last_transition_at
		FROM transition_counts
		WHERE transition_count >= $%d
		ORDER BY flapping_score DESC
		LIMIT 50`, whereClause, argCount)

	args = append(args, threshold)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(operation, "database").Inc()
		return nil, fmt.Errorf("failed to query flapping alerts: %w", err)
	}
	defer rows.Close()

	var flappingAlerts []*core.FlappingAlert
	for rows.Next() {
		alert := &core.FlappingAlert{}
		var namespace interface{}

		err := rows.Scan(
			&alert.Fingerprint,
			&alert.AlertName,
			&namespace,
			&alert.TransitionCount,
			&alert.FlappingScore,
			&alert.LastTransitionAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan flapping alert: %w", err)
		}

		if namespace != nil {
			if ns, ok := namespace.(string); ok {
				alert.Namespace = &ns
			}
		}

		flappingAlerts = append(flappingAlerts, alert)
	}

	r.metrics.QueryResults.WithLabelValues(operation).Observe(float64(len(flappingAlerts)))

	return flappingAlerts, nil
}
