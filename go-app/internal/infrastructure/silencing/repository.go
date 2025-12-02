package silencing

import (
	"context"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// SilenceRepository provides persistence operations for silence rules.
// All methods are safe for concurrent use and support context cancellation.
//
// Thread-safety: All methods are safe for concurrent calls.
// Context handling: All methods respect ctx.Done() for cancellation.
// Error handling: Returns wrapped errors with context.
//
// Example usage:
//
//	repo := NewPostgresSilenceRepository(pool, logger)
//
//	// Create a silence
//	silence := &silencing.Silence{
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Maintenance window",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []silencing.Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: silencing.MatcherTypeEqual},
//	    },
//	}
//	created, err := repo.CreateSilence(ctx, silence)
type SilenceRepository interface {
	// CreateSilence creates a new silence in the database.
	// Generates a new UUID if silence.ID is empty.
	// Validates the silence before insertion.
	// Returns the created silence with ID, CreatedAt populated.
	//
	// Errors:
	//   - ErrSilenceExists if a silence with the same ID already exists
	//   - ErrValidation if silence.Validate() fails
	//   - ErrDatabaseConnection for database errors
	CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error)

	// GetSilenceByID retrieves a single silence by its UUID.
	//
	// Errors:
	//   - ErrSilenceNotFound if no silence with the given ID exists
	//   - ErrInvalidUUID if id is not a valid UUID
	//   - ErrDatabaseConnection for database errors
	GetSilenceByID(ctx context.Context, id string) (*silencing.Silence, error)

	// ListSilences retrieves silences matching the provided filter.
	// Returns an empty slice if no silences match.
	// Results are paginated according to filter.Limit and filter.Offset.
	//
	// Errors:
	//   - ErrInvalidFilter if filter parameters are invalid
	//   - ErrDatabaseConnection for database errors
	ListSilences(ctx context.Context, filter SilenceFilter) ([]*silencing.Silence, error)

	// UpdateSilence updates an existing silence.
	// Uses optimistic locking: compares silence.UpdatedAt before update.
	// Sets UpdatedAt to NOW() on successful update.
	//
	// Errors:
	//   - ErrSilenceNotFound if the silence does not exist
	//   - ErrSilenceConflict if optimistic locking fails (concurrent update)
	//   - ErrValidation if silence.Validate() fails
	//   - ErrDatabaseConnection for database errors
	UpdateSilence(ctx context.Context, silence *silencing.Silence) error

	// DeleteSilence deletes a silence by ID.
	// This is a hard delete (permanent removal).
	//
	// Errors:
	//   - ErrSilenceNotFound if the silence does not exist
	//   - ErrDatabaseConnection for database errors
	DeleteSilence(ctx context.Context, id string) error

	// CountSilences returns the total number of silences matching the filter.
	// Useful for pagination (total pages = count / limit).
	//
	// Errors:
	//   - ErrInvalidFilter if filter parameters are invalid
	//   - ErrDatabaseConnection for database errors
	CountSilences(ctx context.Context, filter SilenceFilter) (int64, error)

	// ExpireSilences marks silences with EndsAt < before as expired.
	// If deleteExpired is true, also deletes them from the database.
	// Returns the number of silences affected.
	//
	// Batch limit: processes max 1000 silences per call.
	//
	// Errors:
	//   - ErrDatabaseConnection for database errors
	ExpireSilences(ctx context.Context, before time.Time, deleteExpired bool) (int64, error)

	// GetExpiringSoon returns silences expiring within the specified window.
	// Example: GetExpiringSoon(ctx, 1*time.Hour) returns silences expiring in next hour.
	//
	// Errors:
	//   - ErrDatabaseConnection for database errors
	GetExpiringSoon(ctx context.Context, window time.Duration) ([]*silencing.Silence, error)

	// BulkUpdateStatus updates the status of multiple silences atomically.
	// Uses a transaction to ensure all-or-nothing semantics.
	//
	// Errors:
	//   - ErrTransactionFailed if the transaction fails
	//   - ErrDatabaseConnection for database errors
	BulkUpdateStatus(ctx context.Context, ids []string, status silencing.SilenceStatus) error

	// GetSilenceStats returns aggregate statistics about silences.
	//
	// Returns counts by status (active, pending, expired) and top creators.
	//
	// Errors:
	//   - ErrDatabaseConnection for database errors
	GetSilenceStats(ctx context.Context) (*SilenceStats, error)
}

// SilenceStats contains aggregate statistics about silences.
type SilenceStats struct {
	Total     int64            `json:"total"`              // Total number of silences
	Active    int64            `json:"active"`             // Number of active silences
	Pending   int64            `json:"pending"`            // Number of pending silences
	Expired   int64            `json:"expired"`            // Number of expired silences
	ByCreator map[string]int64 `json:"by_creator"`         // Top 10 creators by silence count
}

// SilenceFilter defines filtering and pagination options for ListSilences.
//
// Example usage:
//
//	// Get active silences
//	filter := SilenceFilter{
//	    Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
//	    Limit:    100,
//	}
//	silences, err := repo.ListSilences(ctx, filter)
//
//	// Get silences by creator
//	filter := SilenceFilter{
//	    CreatedBy: "ops@example.com",
//	    OrderBy:   "created_at",
//	    OrderDesc: true,
//	    Limit:     50,
//	}
//	silences, err := repo.ListSilences(ctx, filter)
//
//	// Search by matcher name
//	filter := SilenceFilter{
//	    MatcherName: "alertname",
//	    Limit:       100,
//	}
//	silences, err := repo.ListSilences(ctx, filter)
type SilenceFilter struct {
	// Statuses filters by one or more status values (pending, active, expired).
	// Empty slice matches all statuses.
	// Example: []silencing.SilenceStatus{silencing.SilenceStatusActive}
	Statuses []silencing.SilenceStatus

	// CreatedBy filters by creator email/username (exact match).
	// Empty string matches all creators.
	// Example: "ops@example.com"
	CreatedBy string

	// MatcherName searches for silences with this matcher name in JSONB.
	// Uses JSONB containment operator: matchers @> '[{"name":"..."}]'
	// Empty string skips this filter.
	// Example: "alertname"
	MatcherName string

	// MatcherValue searches for silences with this matcher value in JSONB.
	// Uses JSONB containment operator: matchers @> '[{"value":"..."}]'
	// Empty string skips this filter.
	// Example: "HighCPU"
	MatcherValue string

	// Time range filters
	// StartsAfter filters silences where starts_at >= value
	StartsAfter *time.Time
	// StartsBefore filters silences where starts_at <= value
	StartsBefore *time.Time
	// EndsAfter filters silences where ends_at >= value
	EndsAfter *time.Time
	// EndsBefore filters silences where ends_at <= value
	EndsBefore *time.Time

	// Pagination

	// Limit is the maximum number of results to return.
	// Default: 100, Max: 1000
	Limit int

	// Offset is the number of results to skip (for pagination).
	// Default: 0
	Offset int

	// Sorting

	// OrderBy specifies the field to sort by.
	// Valid values: created_at|starts_at|ends_at|updated_at
	// Default: created_at
	OrderBy string

	// OrderDesc specifies the sort direction.
	// true: descending (newest first), false: ascending (oldest first)
	// Default: true (newest first)
	OrderDesc bool
}

// Validate validates the filter parameters and returns an error if invalid.
//
// Validation rules:
//   - Limit must be >= 0 and <= 1000
//   - Offset must be >= 0
//   - OrderBy must be one of: created_at, starts_at, ends_at, updated_at
//
// Returns:
//   - nil if valid
//   - ErrInvalidFilter with details if invalid
func (f *SilenceFilter) Validate() error {
	if f.Limit < 0 {
		return fmt.Errorf("%w: limit must be >= 0, got %d", ErrInvalidFilter, f.Limit)
	}
	if f.Limit > 1000 {
		return fmt.Errorf("%w: limit must be <= 1000, got %d", ErrInvalidFilter, f.Limit)
	}

	if f.Offset < 0 {
		return fmt.Errorf("%w: offset must be >= 0, got %d", ErrInvalidFilter, f.Offset)
	}

	validOrderBy := map[string]bool{
		"created_at": true,
		"starts_at":  true,
		"ends_at":    true,
		"updated_at": true,
	}
	if f.OrderBy != "" && !validOrderBy[f.OrderBy] {
		return fmt.Errorf("%w: invalid order_by field: %s (must be one of: created_at, starts_at, ends_at, updated_at)", ErrInvalidFilter, f.OrderBy)
	}

	return nil
}

// ApplyDefaults sets default values for empty fields.
// This is typically called before Validate() to ensure sensible defaults.
//
// Defaults:
//   - Limit: 100
//   - OrderBy: "created_at"
//   - OrderDesc: true (newest first)
func (f *SilenceFilter) ApplyDefaults() {
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.OrderBy == "" {
		f.OrderBy = "created_at"
	}
	// OrderDesc defaults to true (handled by caller if needed)
}
