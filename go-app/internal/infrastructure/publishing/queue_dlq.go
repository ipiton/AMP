package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ipiton/AMP/internal/core"
)

// DLQEntry represents a failed job in the Dead Letter Queue
type DLQEntry struct {
	ID             uuid.UUID               `json:"id"`
	JobID          uuid.UUID               `json:"job_id"`
	Fingerprint    string                  `json:"fingerprint"`
	TargetName     string                  `json:"target_name"`
	TargetType     string                  `json:"target_type"`
	EnrichedAlert  *core.EnrichedAlert     `json:"enriched_alert"`
	TargetConfig   *core.PublishingTarget  `json:"target_config"`
	ErrorMessage   string                  `json:"error_message"`
	ErrorType      string                  `json:"error_type"`
	RetryCount     int                     `json:"retry_count"`
	LastRetryAt    *time.Time              `json:"last_retry_at,omitempty"`
	Priority       string                  `json:"priority"`
	FailedAt       time.Time               `json:"failed_at"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
	Replayed       bool                    `json:"replayed"`
	ReplayedAt     *time.Time              `json:"replayed_at,omitempty"`
	ReplayResult   *string                 `json:"replay_result,omitempty"`
}

// DLQFilters for querying DLQ entries
type DLQFilters struct {
	TargetName  string
	ErrorType   string
	Priority    string
	Replayed    *bool
	FailedAfter *time.Time
	Limit       int
	Offset      int
}

// DLQStats provides statistics about the DLQ
type DLQStats struct {
	TotalEntries       int            `json:"total_entries"`
	EntriesByErrorType map[string]int `json:"entries_by_error_type"`
	EntriesByTarget    map[string]int `json:"entries_by_target"`
	EntriesByPriority  map[string]int `json:"entries_by_priority"`
	OldestEntry        *time.Time     `json:"oldest_entry,omitempty"`
	NewestEntry        *time.Time     `json:"newest_entry,omitempty"`
	ReplayedCount      int            `json:"replayed_count"`
}

// DLQRepository defines interface for Dead Letter Queue operations
type DLQRepository interface {
	// Write adds a failed job to the DLQ
	Write(ctx context.Context, job *PublishingJob) error

	// Read retrieves DLQ entries with optional filtering
	Read(ctx context.Context, filters DLQFilters) ([]*DLQEntry, error)

	// Replay attempts to replay a specific DLQ entry
	Replay(ctx context.Context, id uuid.UUID) error

	// Purge removes entries older than specified duration
	Purge(ctx context.Context, olderThan time.Duration) (int64, error)

	// GetStats returns DLQ statistics
	GetStats(ctx context.Context) (*DLQStats, error)
}

// PostgreSQLDLQRepository implements DLQRepository using PostgreSQL
type PostgreSQLDLQRepository struct {
	db     *pgxpool.Pool
	queue  *PublishingQueue
	logger *slog.Logger
}

// NewPostgreSQLDLQRepository creates a new PostgreSQL DLQ repository
// Note: queue can be nil initially and set later via SetQueue() to avoid circular dependency
func NewPostgreSQLDLQRepository(db *pgxpool.Pool, queue *PublishingQueue, logger *slog.Logger) *PostgreSQLDLQRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &PostgreSQLDLQRepository{
		db:     db,
		queue:  queue,
		logger: logger,
	}
}

// SetQueue sets the publishing queue (used to resolve circular dependency during initialization)
func (r *PostgreSQLDLQRepository) SetQueue(queue *PublishingQueue) {
	r.queue = queue
}

// Write adds a failed job to the DLQ
func (r *PostgreSQLDLQRepository) Write(ctx context.Context, job *PublishingJob) error {
	// Serialize EnrichedAlert to JSONB
	enrichedAlertJSON, err := json.Marshal(job.EnrichedAlert)
	if err != nil {
		return fmt.Errorf("failed to marshal enriched alert: %w", err)
	}

	// Serialize Target to JSONB
	targetConfigJSON, err := json.Marshal(job.Target)
	if err != nil {
		return fmt.Errorf("failed to marshal target config: %w", err)
	}

	// Error message
	errorMessage := ""
	if job.LastError != nil {
		errorMessage = job.LastError.Error()
	}

	// Insert into database
	query := `
		INSERT INTO publishing_dlq (
			job_id, fingerprint, target_name, target_type,
			enriched_alert, target_config,
			error_message, error_type, retry_count, last_retry_at,
			priority, failed_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, $8, $9, $10,
			$11, $12
		)
		RETURNING id
	`

	var dlqID uuid.UUID
	err = r.db.QueryRow(
		ctx,
		query,
		job.ID,
		job.EnrichedAlert.Alert.Fingerprint,
		job.Target.Name,
		job.Target.Type,
		enrichedAlertJSON,
		targetConfigJSON,
		errorMessage,
		job.ErrorType.String(),
		job.RetryCount,
		nil, // last_retry_at (not used for initial write)
		job.Priority.String(),
		time.Now(),
	).Scan(&dlqID)

	if err != nil {
		return fmt.Errorf("failed to write to DLQ: %w", err)
	}

	r.logger.Info("Job written to DLQ",
		"dlq_id", dlqID,
		"job_id", job.ID,
		"target", job.Target.Name,
		"error_type", job.ErrorType,
	)


	return nil
}

// Read retrieves DLQ entries with optional filtering
func (r *PostgreSQLDLQRepository) Read(ctx context.Context, filters DLQFilters) ([]*DLQEntry, error) {
	query := `
		SELECT
			id, job_id, fingerprint, target_name, target_type,
			enriched_alert, target_config,
			error_message, error_type, retry_count, last_retry_at,
			priority, failed_at, created_at, updated_at,
			replayed, replayed_at, replay_result
		FROM publishing_dlq
		WHERE 1=1
	`

	args := []interface{}{}
	argCount := 1

	// Apply filters
	if filters.TargetName != "" {
		query += fmt.Sprintf(" AND target_name = $%d", argCount)
		args = append(args, filters.TargetName)
		argCount++
	}

	if filters.ErrorType != "" {
		query += fmt.Sprintf(" AND error_type = $%d", argCount)
		args = append(args, filters.ErrorType)
		argCount++
	}

	if filters.Priority != "" {
		query += fmt.Sprintf(" AND priority = $%d", argCount)
		args = append(args, filters.Priority)
		argCount++
	}

	if filters.Replayed != nil {
		query += fmt.Sprintf(" AND replayed = $%d", argCount)
		args = append(args, *filters.Replayed)
		argCount++
	}

	if filters.FailedAfter != nil {
		query += fmt.Sprintf(" AND failed_at >= $%d", argCount)
		args = append(args, *filters.FailedAfter)
		argCount++
	}

	// Order by failed_at DESC
	query += " ORDER BY failed_at DESC"

	// Limit and offset
	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
		argCount++
	} else {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, 100) // Default limit
		argCount++
	}

	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
		argCount++
	}

	// Execute query
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query DLQ: %w", err)
	}
	defer rows.Close()

	// Parse results
	entries := []*DLQEntry{}
	for rows.Next() {
		var entry DLQEntry
		var enrichedAlertJSON []byte
		var targetConfigJSON []byte

		err := rows.Scan(
			&entry.ID,
			&entry.JobID,
			&entry.Fingerprint,
			&entry.TargetName,
			&entry.TargetType,
			&enrichedAlertJSON,
			&targetConfigJSON,
			&entry.ErrorMessage,
			&entry.ErrorType,
			&entry.RetryCount,
			&entry.LastRetryAt,
			&entry.Priority,
			&entry.FailedAt,
			&entry.CreatedAt,
			&entry.UpdatedAt,
			&entry.Replayed,
			&entry.ReplayedAt,
			&entry.ReplayResult,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan DLQ entry: %w", err)
		}

		// Unmarshal JSON fields
		if err := json.Unmarshal(enrichedAlertJSON, &entry.EnrichedAlert); err != nil {
			return nil, fmt.Errorf("failed to unmarshal enriched_alert: %w", err)
		}

		if err := json.Unmarshal(targetConfigJSON, &entry.TargetConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal target_config: %w", err)
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}

// Replay attempts to replay a specific DLQ entry
func (r *PostgreSQLDLQRepository) Replay(ctx context.Context, id uuid.UUID) error {
	// Fetch entry
	entries, err := r.Read(ctx, DLQFilters{Limit: 1})
	if err != nil {
		return fmt.Errorf("failed to read DLQ entry: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("DLQ entry not found: %s", id)
	}

	entry := entries[0]

	// Check if already replayed
	if entry.Replayed {
		r.logger.Warn("DLQ entry already replayed", "dlq_id", id)
		return fmt.Errorf("entry already replayed")
	}

	// Re-submit to queue
	err = r.queue.Submit(entry.EnrichedAlert, entry.TargetConfig)

	// Update replay status
	replayResult := "success"
	if err != nil {
		replayResult = "failed"
	}

	updateQuery := `
		UPDATE publishing_dlq
		SET replayed = TRUE, replayed_at = NOW(), replay_result = $1
		WHERE id = $2
	`

	_, updateErr := r.db.Exec(ctx, updateQuery, replayResult, id)
	if updateErr != nil {
		r.logger.Error("Failed to update DLQ replay status", "error", updateErr)
	}

	if err != nil {
		return fmt.Errorf("failed to replay job: %w", err)
	}

	r.logger.Info("DLQ entry replayed successfully", "dlq_id", id)


	return nil
}

// Purge removes entries older than specified duration
func (r *PostgreSQLDLQRepository) Purge(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-olderThan)

	query := `
		DELETE FROM publishing_dlq
		WHERE failed_at < $1
	`

	result, err := r.db.Exec(ctx, query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to purge DLQ: %w", err)
	}

	rowsAffected := result.RowsAffected()

	r.logger.Info("DLQ purged",
		"rows_deleted", rowsAffected,
		"older_than", olderThan,
		"cutoff_time", cutoffTime,
	)

	return rowsAffected, nil
}

// GetStats returns DLQ statistics
func (r *PostgreSQLDLQRepository) GetStats(ctx context.Context) (*DLQStats, error) {
	stats := &DLQStats{
		EntriesByErrorType: make(map[string]int),
		EntriesByTarget:    make(map[string]int),
		EntriesByPriority:  make(map[string]int),
	}

	// Total entries
	err := r.db.QueryRow(ctx, "SELECT COUNT(*) FROM publishing_dlq").Scan(&stats.TotalEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to get total entries: %w", err)
	}

	// By error type
	rows, err := r.db.Query(ctx, "SELECT error_type, COUNT(*) FROM publishing_dlq GROUP BY error_type")
	if err != nil {
		return nil, fmt.Errorf("failed to get entries by error type: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var errorType string
		var count int
		if err := rows.Scan(&errorType, &count); err != nil {
			return nil, err
		}
		stats.EntriesByErrorType[errorType] = count
	}

	// By target
	rows, err = r.db.Query(ctx, "SELECT target_name, COUNT(*) FROM publishing_dlq GROUP BY target_name")
	if err != nil {
		return nil, fmt.Errorf("failed to get entries by target: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var target string
		var count int
		if err := rows.Scan(&target, &count); err != nil {
			return nil, err
		}
		stats.EntriesByTarget[target] = count
	}

	// By priority
	rows, err = r.db.Query(ctx, "SELECT priority, COUNT(*) FROM publishing_dlq GROUP BY priority")
	if err != nil {
		return nil, fmt.Errorf("failed to get entries by priority: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var priority string
		var count int
		if err := rows.Scan(&priority, &count); err != nil {
			return nil, err
		}
		stats.EntriesByPriority[priority] = count
	}

	// Oldest/newest entries
	r.db.QueryRow(ctx, "SELECT MIN(failed_at), MAX(failed_at) FROM publishing_dlq").Scan(&stats.OldestEntry, &stats.NewestEntry)

	// Replayed count
	r.db.QueryRow(ctx, "SELECT COUNT(*) FROM publishing_dlq WHERE replayed = TRUE").Scan(&stats.ReplayedCount)

	return stats, nil
}
