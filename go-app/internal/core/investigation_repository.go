package core

import "context"

// InvestigationRepository defines persistence operations for alert investigations.
type InvestigationRepository interface {
	// Create inserts a new investigation record with status=queued.
	Create(ctx context.Context, inv *Investigation) error

	// UpdateStatus sets the status (and started_at for processing).
	UpdateStatus(ctx context.Context, id string, status InvestigationStatus) error

	// SaveResult stores the LLM findings and sets status=completed.
	SaveResult(ctx context.Context, id string, result *InvestigationResult) error

	// SaveError records the failure and increments retry_count.
	SaveError(ctx context.Context, id string, errMsg string, errType InvestigationErrorType) error

	// GetLatestByFingerprint retrieves the most recent investigation for a fingerprint.
	// Returns nil, nil if no record exists.
	GetLatestByFingerprint(ctx context.Context, fingerprint string) (*Investigation, error)

	// MoveToDLQ sets status=dlq for a given investigation.
	MoveToDLQ(ctx context.Context, id string) error
}
