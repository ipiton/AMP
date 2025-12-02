package silencing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// PostgresSilenceRepository implements SilenceRepository for PostgreSQL.
// It provides persistent storage for silence rules with ACID guarantees.
//
// Features:
//   - CRUD operations for silences
//   - Optimistic locking for concurrent updates
//   - JSONB storage for flexible matcher structures
//   - Context-aware operations (cancellation, deadlines)
//   - Comprehensive metrics tracking
//   - Structured logging
//
// Thread-safety: All methods are safe for concurrent use.
type PostgresSilenceRepository struct {
	pool    *pgxpool.Pool
	logger  *slog.Logger
	metrics *SilenceMetrics
}

// NewPostgresSilenceRepository creates a new PostgreSQL silence repository.
//
// Parameters:
//   - pool: PostgreSQL connection pool (required)
//   - logger: Structured logger (optional, defaults to slog.Default())
//
// Returns:
//   - *PostgresSilenceRepository: Initialized repository
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	repo := NewPostgresSilenceRepository(pool, slog.Default())
func NewPostgresSilenceRepository(pool *pgxpool.Pool, logger *slog.Logger) *PostgresSilenceRepository {
	if logger == nil {
		logger = slog.Default()
	}

	return &PostgresSilenceRepository{
		pool:    pool,
		logger:  logger,
		metrics: NewSilenceMetrics(),
	}
}

// CreateSilence implements SilenceRepository.CreateSilence.
//
// This method:
//  1. Validates the silence via silence.Validate()
//  2. Generates a UUID if silence.ID is empty
//  3. Calculates the initial status based on StartsAt/EndsAt
//  4. Marshals matchers to JSONB format
//  5. Inserts the silence into the database
//  6. Records metrics (operations, duration, active silences)
//  7. Logs the operation
//
// Performance target: <10ms for single insert
//
// Example:
//
//	silence := &silencing.Silence{
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Planned maintenance",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []silencing.Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: silencing.MatcherTypeEqual},
//	    },
//	}
//	created, err := repo.CreateSilence(ctx, silence)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Created silence: %s\n", created.ID)
func (r *PostgresSilenceRepository) CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error) {
	start := time.Now()
	operation := "create"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	// Step 1: Validate silence before insert
	if err := silence.Validate(); err != nil {
		r.metrics.Errors.WithLabelValues(operation, "validation").Inc()
		return nil, fmt.Errorf("%w: %s", ErrValidation, err)
	}

	// Step 2: Generate UUID if not set
	if silence.ID == "" {
		silence.ID = uuid.New().String()
	}

	// Step 3: Validate UUID format
	if _, err := uuid.Parse(silence.ID); err != nil {
		r.metrics.Errors.WithLabelValues(operation, "invalid_uuid").Inc()
		return nil, fmt.Errorf("%w: %s", ErrInvalidUUID, err)
	}

	// Step 4: Calculate initial status
	silence.Status = silence.CalculateStatus()

	// Step 5: Marshal matchers to JSONB
	matchersJSON, err := json.Marshal(silence.Matchers)
	if err != nil {
		r.metrics.Errors.WithLabelValues(operation, "marshal").Inc()
		return nil, fmt.Errorf("marshal matchers: %w", err)
	}

	// Step 6: Insert silence
	query := `
		INSERT INTO silences (id, created_by, comment, starts_at, ends_at, matchers, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		RETURNING created_at
	`

	var createdAt time.Time
	err = r.pool.QueryRow(ctx, query,
		silence.ID,
		silence.CreatedBy,
		silence.Comment,
		silence.StartsAt,
		silence.EndsAt,
		matchersJSON,
		silence.Status,
	).Scan(&createdAt)

	if err != nil {
		r.metrics.Errors.WithLabelValues(operation, "insert").Inc()

		// Check for duplicate key error (23505)
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return nil, fmt.Errorf("%w: silence with ID %s already exists", ErrSilenceExists, silence.ID)
		}

		return nil, fmt.Errorf("insert silence: %w", err)
	}

	silence.CreatedAt = createdAt
	r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	r.metrics.ActiveSilences.WithLabelValues(string(silence.Status)).Inc()

	r.logger.Info("silence created",
		"silence_id", silence.ID,
		"created_by", silence.CreatedBy,
		"starts_at", silence.StartsAt.Format(time.RFC3339),
		"ends_at", silence.EndsAt.Format(time.RFC3339),
		"status", silence.Status,
		"matchers_count", len(silence.Matchers),
	)

	return silence, nil
}

// GetSilenceByID implements SilenceRepository.GetSilenceByID.
//
// This method:
//  1. Validates the UUID format
//  2. Executes an indexed SELECT query by ID
//  3. Scans the row into a Silence struct
//  4. Unmarshals JSONB matchers
//  5. Records metrics
//
// Performance target: <3ms for indexed UUID lookup
//
// Example:
//
//	silence, err := repo.GetSilenceByID(ctx, "550e8400-e29b-41d4-a716-446655440000")
//	if err == ErrSilenceNotFound {
//	    fmt.Println("Silence not found")
//	    return
//	}
//	fmt.Printf("Silence: %+v\n", silence)
func (r *PostgresSilenceRepository) GetSilenceByID(ctx context.Context, id string) (*silencing.Silence, error) {
	start := time.Now()
	operation := "get_by_id"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	// Step 1: Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "invalid_uuid").Inc()
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidUUID, err)
	}

	// Step 2: Execute SELECT query
	query := `
		SELECT id, created_by, comment, starts_at, ends_at, matchers, status, created_at, updated_at
		FROM silences
		WHERE id = $1
	`

	var silence silencing.Silence
	var matchersJSON []byte
	var updatedAt *time.Time

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&silence.ID,
		&silence.CreatedBy,
		&silence.Comment,
		&silence.StartsAt,
		&silence.EndsAt,
		&matchersJSON,
		&silence.Status,
		&silence.CreatedAt,
		&updatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			r.metrics.Errors.WithLabelValues(operation, "not_found").Inc()
			return nil, fmt.Errorf("%w: silence with ID %s", ErrSilenceNotFound, id)
		}
		r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		return nil, fmt.Errorf("query silence: %w", err)
	}

	// Step 3: Unmarshal JSONB matchers
	if err := json.Unmarshal(matchersJSON, &silence.Matchers); err != nil {
		r.metrics.Errors.WithLabelValues(operation, "unmarshal").Inc()
		return nil, fmt.Errorf("unmarshal matchers: %w", err)
	}

	silence.UpdatedAt = updatedAt
	r.metrics.Operations.WithLabelValues(operation, "success").Inc()

	r.logger.Debug("silence retrieved",
		"silence_id", silence.ID,
		"status", silence.Status,
	)

	return &silence, nil
}

// UpdateSilence implements SilenceRepository.UpdateSilence.
//
// This method uses optimistic locking to prevent concurrent modification issues.
// It compares the silence.UpdatedAt value before performing the update.
// If another transaction has modified the silence, the update fails with ErrSilenceConflict.
//
// Optimistic locking flow:
//  1. Client reads silence (with UpdatedAt timestamp)
//  2. Client modifies silence
//  3. Client calls UpdateSilence
//  4. Server checks if UpdatedAt matches database value
//  5. If match: update succeeds, new UpdatedAt returned
//  6. If mismatch: update fails with ErrSilenceConflict
//  7. Client should fetch fresh data and retry
//
// Performance target: <10ms for single update
//
// Example:
//
//	// Fetch silence
//	silence, _ := repo.GetSilenceByID(ctx, id)
//
//	// Modify silence
//	silence.Comment = "Extended maintenance window"
//	silence.EndsAt = silence.EndsAt.Add(1 * time.Hour)
//
//	// Update
//	err := repo.UpdateSilence(ctx, silence)
//	if err == ErrSilenceConflict {
//	    // Another transaction modified the silence, retry
//	    silence, _ = repo.GetSilenceByID(ctx, id)
//	    silence.Comment = "Extended maintenance window"
//	    err = repo.UpdateSilence(ctx, silence)
//	}
func (r *PostgresSilenceRepository) UpdateSilence(ctx context.Context, silence *silencing.Silence) error {
	start := time.Now()
	operation := "update"

	defer func() {
		duration := time.Since(start).Seconds()
		r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
	}()

	// Step 1: Validate silence
	if err := silence.Validate(); err != nil {
		r.metrics.Errors.WithLabelValues(operation, "validation").Inc()
		return fmt.Errorf("%w: %s", ErrValidation, err)
	}

	// Step 2: Calculate current status
	silence.Status = silence.CalculateStatus()

	// Step 3: Marshal matchers to JSONB
	matchersJSON, err := json.Marshal(silence.Matchers)
	if err != nil {
		r.metrics.Errors.WithLabelValues(operation, "marshal").Inc()
		return fmt.Errorf("marshal matchers: %w", err)
	}

	// Step 4: Execute UPDATE with optimistic locking
	// The WHERE clause checks both ID and UpdatedAt to detect concurrent modifications
	query := `
		UPDATE silences
		SET created_by = $1,
			comment = $2,
			starts_at = $3,
			ends_at = $4,
			matchers = $5,
			status = $6,
			updated_at = NOW()
		WHERE id = $7
		  AND (updated_at IS NULL OR updated_at = $8)
		RETURNING updated_at
	`

	var updatedAt time.Time
	err = r.pool.QueryRow(ctx, query,
		silence.CreatedBy,
		silence.Comment,
		silence.StartsAt,
		silence.EndsAt,
		matchersJSON,
		silence.Status,
		silence.ID,
		silence.UpdatedAt,
	).Scan(&updatedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			// Check if silence exists
			exists, _ := r.silenceExists(ctx, silence.ID)
			if !exists {
				r.metrics.Errors.WithLabelValues(operation, "not_found").Inc()
				return fmt.Errorf("%w: silence with ID %s", ErrSilenceNotFound, silence.ID)
			}
			// Optimistic lock conflict
			r.metrics.Errors.WithLabelValues(operation, "conflict").Inc()
			return fmt.Errorf("%w: silence was modified by another transaction", ErrSilenceConflict)
		}
		r.metrics.Errors.WithLabelValues(operation, "update").Inc()
		return fmt.Errorf("update silence: %w", err)
	}

	silence.UpdatedAt = &updatedAt
	r.metrics.Operations.WithLabelValues(operation, "success").Inc()

	r.logger.Info("silence updated",
		"silence_id", silence.ID,
		"created_by", silence.CreatedBy,
		"status", silence.Status,
		"updated_at", updatedAt.Format(time.RFC3339),
	)

	return nil
}

// DeleteSilence implements SilenceRepository.DeleteSilence.
//
// This is a hard delete (permanent removal from database).
// For soft deletion, consider using UpdateSilence to set status=expired instead.
//
// Performance target: <5ms for single delete
//
// Example:
//
//	err := repo.DeleteSilence(ctx, silenceID)
//	if err == ErrSilenceNotFound {
//	    fmt.Println("Silence already deleted")
//	    return
//	}
func (r *PostgresSilenceRepository) DeleteSilence(ctx context.Context, id string) error {
	start := time.Now()
	operation := "delete"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	// Step 1: Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "invalid_uuid").Inc()
		}
		return fmt.Errorf("%w: %s", ErrInvalidUUID, err)
	}

	// Step 2: Execute DELETE query
	query := `DELETE FROM silences WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		r.metrics.Errors.WithLabelValues(operation, "delete").Inc()
		return fmt.Errorf("delete silence: %w", err)
	}

	if result.RowsAffected() == 0 {
		r.metrics.Errors.WithLabelValues(operation, "not_found").Inc()
		return fmt.Errorf("%w: silence with ID %s", ErrSilenceNotFound, id)
	}

	r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	r.metrics.ActiveSilences.WithLabelValues("deleted").Dec()

	r.logger.Info("silence deleted", "silence_id", id)

	return nil
}

// silenceExists checks if a silence with the given ID exists in the database.
// This is a helper method used by UpdateSilence to distinguish between
// "not found" and "optimistic lock conflict" errors.
func (r *PostgresSilenceRepository) silenceExists(ctx context.Context, id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM silences WHERE id = $1)`

	var exists bool
	err := r.pool.QueryRow(ctx, query, id).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// ListSilences implements SilenceRepository.ListSilences.
func (r *PostgresSilenceRepository) ListSilences(ctx context.Context, filter SilenceFilter) ([]*silencing.Silence, error) {
	start := time.Now()
	operation := "list"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	filter.ApplyDefaults()
	if err := filter.Validate(); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "validation").Inc()
		}
		return nil, err
	}

	query, args := r.buildListQuery(filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return nil, fmt.Errorf("query silences: %w", err)
	}
	defer rows.Close()

	silences := []*silencing.Silence{}
	for rows.Next() {
		var silence silencing.Silence
		var matchersJSON []byte
		var updatedAt *time.Time

		err := rows.Scan(
			&silence.ID, &silence.CreatedBy, &silence.Comment,
			&silence.StartsAt, &silence.EndsAt, &matchersJSON,
			&silence.Status, &silence.CreatedAt, &updatedAt,
		)
		if err != nil {
			if r.metrics != nil {
				r.metrics.Errors.WithLabelValues(operation, "scan").Inc()
			}
			return nil, fmt.Errorf("scan silence: %w", err)
		}

		if err := json.Unmarshal(matchersJSON, &silence.Matchers); err != nil {
			if r.metrics != nil {
				r.metrics.Errors.WithLabelValues(operation, "unmarshal").Inc()
			}
			return nil, fmt.Errorf("unmarshal matchers: %w", err)
		}

		silence.UpdatedAt = updatedAt
		silences = append(silences, &silence)
	}

	if err := rows.Err(); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "rows").Inc()
		}
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	return silences, nil
}

// CountSilences implements SilenceRepository.CountSilences.
func (r *PostgresSilenceRepository) CountSilences(ctx context.Context, filter SilenceFilter) (int64, error) {
	start := time.Now()
	operation := "count"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	query, args := r.buildCountQuery(filter)
	var count int64
	err := r.pool.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return 0, fmt.Errorf("count silences: %w", err)
	}

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	return count, nil
}

// ExpireSilences implements SilenceRepository.ExpireSilences.
//
// This method transitions silences to "expired" status if their ends_at
// is before the given time. If deleteExpired is true, expired silences
// are permanently deleted instead.
//
// Performance target: <50ms for 1000 silences
//
// Example:
//
//	// Expire silences ended before now
//	count, err := repo.ExpireSilences(ctx, time.Now(), false)
//
//	// Delete silences expired 30+ days ago
//	cutoff := time.Now().Add(-30 * 24 * time.Hour)
//	count, err := repo.ExpireSilences(ctx, cutoff, true)
func (r *PostgresSilenceRepository) ExpireSilences(ctx context.Context, before time.Time, deleteExpired bool) (int64, error) {
	start := time.Now()
	operation := "expire"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	var query string
	var args []interface{}

	if deleteExpired {
		// DELETE expired silences
		query = `
			DELETE FROM silences
			WHERE status = $1
			  AND ends_at < $2
		`
		args = []interface{}{silencing.SilenceStatusExpired, before}
		operation = "delete_expired"
	} else {
		// UPDATE active/pending silences to expired
		query = `
			UPDATE silences
			SET status = $1,
			    updated_at = NOW()
			WHERE status IN ($2, $3)
			  AND ends_at < $4
		`
		args = []interface{}{
			silencing.SilenceStatusExpired,
			silencing.SilenceStatusActive,
			silencing.SilenceStatusPending,
			before,
		}
	}

	r.logger.Debug("expiring silences",
		"before", before,
		"delete_expired", deleteExpired,
	)

	result, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return 0, fmt.Errorf("expire silences: %w", err)
	}

	affected := result.RowsAffected()

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	r.logger.Info("silences expired",
		"count", affected,
		"before", before,
		"deleted", deleteExpired,
	)

	return affected, nil
}

// GetExpiringSoon implements SilenceRepository.GetExpiringSoon.
//
// This method returns silences that will expire within the given window.
// Useful for sending expiration notifications.
//
// Performance target: <30ms for 100 results
//
// Example:
//
//	// Get silences expiring in next 24 hours
//	silences, err := repo.GetExpiringSoon(ctx, 24*time.Hour)
func (r *PostgresSilenceRepository) GetExpiringSoon(ctx context.Context, window time.Duration) ([]*silencing.Silence, error) {
	start := time.Now()
	operation := "get_expiring_soon"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	now := time.Now()
	expiresBy := now.Add(window)

	query := `
		SELECT id, created_by, comment, starts_at, ends_at,
		       matchers, status, created_at, updated_at
		FROM silences
		WHERE status IN ($1, $2)
		  AND ends_at > $3
		  AND ends_at <= $4
		ORDER BY ends_at ASC
		LIMIT 1000
	`

	r.logger.Debug("getting expiring silences",
		"window", window,
		"expires_by", expiresBy,
	)

	rows, err := r.pool.Query(ctx, query,
		silencing.SilenceStatusActive,
		silencing.SilenceStatusPending,
		now,
		expiresBy,
	)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return nil, fmt.Errorf("query expiring silences: %w", err)
	}
	defer rows.Close()

	silences := []*silencing.Silence{}
	for rows.Next() {
		var silence silencing.Silence
		var matchersJSON []byte
		var updatedAt *time.Time

		err := rows.Scan(
			&silence.ID, &silence.CreatedBy, &silence.Comment,
			&silence.StartsAt, &silence.EndsAt, &matchersJSON,
			&silence.Status, &silence.CreatedAt, &updatedAt,
		)
		if err != nil {
			if r.metrics != nil {
				r.metrics.Errors.WithLabelValues(operation, "scan").Inc()
			}
			return nil, fmt.Errorf("scan silence: %w", err)
		}

		if err := json.Unmarshal(matchersJSON, &silence.Matchers); err != nil {
			if r.metrics != nil {
				r.metrics.Errors.WithLabelValues(operation, "unmarshal").Inc()
			}
			return nil, fmt.Errorf("unmarshal matchers: %w", err)
		}

		silence.UpdatedAt = updatedAt
		silences = append(silences, &silence)
	}

	if err := rows.Err(); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "rows").Inc()
		}
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	r.logger.Debug("expiring silences found",
		"count", len(silences),
		"window", window,
	)

	return silences, nil
}

// BulkUpdateStatus implements SilenceRepository.BulkUpdateStatus.
//
// This method updates the status of multiple silences in a single transaction.
// Useful for mass operations (e.g., expiring multiple silences, bulk cancellation).
//
// Performance target: <100ms for 1000 silences
//
// Example:
//
//	// Bulk expire 100 silences
//	ids := []string{"uuid1", "uuid2", ..., "uuid100"}
//	err := repo.BulkUpdateStatus(ctx, ids, silencing.SilenceStatusExpired)
func (r *PostgresSilenceRepository) BulkUpdateStatus(ctx context.Context, ids []string, status silencing.SilenceStatus) error {
	start := time.Now()
	operation := "bulk_update_status"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	// Step 1: Validation
	if len(ids) == 0 {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "validation").Inc()
		}
		return fmt.Errorf("%w: ids cannot be empty", ErrInvalidFilter)
	}

	if status == "" {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "validation").Inc()
		}
		return fmt.Errorf("%w: status cannot be empty", ErrInvalidFilter)
	}

	// Step 2: Build UPDATE query with ANY($1) for array matching
	query := `
		UPDATE silences
		SET status = $1,
		    updated_at = NOW()
		WHERE id = ANY($2)
	`

	r.logger.Debug("bulk updating silence statuses",
		"count", len(ids),
		"status", status,
	)

	// Step 3: Execute bulk update
	result, err := r.pool.Exec(ctx, query, status, ids)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return fmt.Errorf("bulk update status: %w", err)
	}

	affected := result.RowsAffected()

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	r.logger.Info("bulk status update completed",
		"requested", len(ids),
		"updated", affected,
		"status", status,
	)

	return nil
}

// GetSilenceStats implements SilenceRepository.GetSilenceStats.
//
// This method returns aggregate statistics about silences:
//   - Total silences count
//   - Count by status (active, pending, expired)
//   - Count by creator (top 10)
//
// Performance target: <30ms
//
// Example:
//
//	stats, err := repo.GetSilenceStats(ctx)
//	fmt.Printf("Active: %d, Expired: %d\n", stats.Active, stats.Expired)
func (r *PostgresSilenceRepository) GetSilenceStats(ctx context.Context) (*SilenceStats, error) {
	start := time.Now()
	operation := "get_stats"

	defer func() {
		if r.metrics != nil {
			duration := time.Since(start).Seconds()
			r.metrics.OperationDuration.WithLabelValues(operation, "success").Observe(duration)
		}
	}()

	stats := &SilenceStats{
		ByCreator: make(map[string]int64),
	}

	// Query 1: Total and by-status counts
	countQuery := `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = $1) AS active,
			COUNT(*) FILTER (WHERE status = $2) AS pending,
			COUNT(*) FILTER (WHERE status = $3) AS expired
		FROM silences
	`

	err := r.pool.QueryRow(ctx, countQuery,
		silencing.SilenceStatusActive,
		silencing.SilenceStatusPending,
		silencing.SilenceStatusExpired,
	).Scan(&stats.Total, &stats.Active, &stats.Pending, &stats.Expired)

	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return nil, fmt.Errorf("query silence counts: %w", err)
	}

	// Query 2: Top 10 creators
	creatorQuery := `
		SELECT created_by, COUNT(*) AS count
		FROM silences
		GROUP BY created_by
		ORDER BY count DESC
		LIMIT 10
	`

	rows, err := r.pool.Query(ctx, creatorQuery)
	if err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "query").Inc()
		}
		return nil, fmt.Errorf("query creators: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var creator string
		var count int64

		if err := rows.Scan(&creator, &count); err != nil {
			if r.metrics != nil {
				r.metrics.Errors.WithLabelValues(operation, "scan").Inc()
			}
			return nil, fmt.Errorf("scan creator stats: %w", err)
		}

		stats.ByCreator[creator] = count
	}

	if err := rows.Err(); err != nil {
		if r.metrics != nil {
			r.metrics.Errors.WithLabelValues(operation, "rows").Inc()
		}
		return nil, fmt.Errorf("iterate creator rows: %w", err)
	}

	if r.metrics != nil {
		r.metrics.Operations.WithLabelValues(operation, "success").Inc()
	}

	r.logger.Debug("silence stats retrieved",
		"total", stats.Total,
		"active", stats.Active,
		"pending", stats.Pending,
		"expired", stats.Expired,
	)

	return stats, nil
}
