package silencing

import "errors"

// Silence repository error types provide structured error handling for storage operations.
// These errors can be used for appropriate HTTP status codes and error responses.

var (
	// ErrSilenceNotFound is returned when a silence does not exist in the database.
	// This typically maps to HTTP 404 Not Found.
	ErrSilenceNotFound = errors.New("silence not found")

	// ErrSilenceExists is returned when trying to create a silence with a duplicate ID.
	// This typically maps to HTTP 409 Conflict.
	ErrSilenceExists = errors.New("silence already exists")

	// ErrSilenceConflict is returned when optimistic locking fails during an update.
	// This occurs when another transaction has modified the silence between read and write.
	// The client should retry with fresh data.
	// This typically maps to HTTP 409 Conflict.
	ErrSilenceConflict = errors.New("silence was modified by another transaction")

	// ErrInvalidFilter is returned when filter parameters are invalid or malformed.
	// Examples: negative limit, invalid order_by field, limit > 1000.
	// This typically maps to HTTP 400 Bad Request.
	ErrInvalidFilter = errors.New("invalid filter parameters")

	// ErrInvalidUUID is returned when a UUID string is malformed or cannot be parsed.
	// This typically maps to HTTP 400 Bad Request.
	ErrInvalidUUID = errors.New("invalid UUID format")

	// ErrDatabaseConnection is returned when the database connection fails.
	// This could be due to network issues, authentication failures, or database unavailability.
	// This typically maps to HTTP 503 Service Unavailable.
	ErrDatabaseConnection = errors.New("database connection error")

	// ErrTransactionFailed is returned when a database transaction fails or cannot be committed.
	// This typically maps to HTTP 500 Internal Server Error.
	ErrTransactionFailed = errors.New("database transaction failed")

	// ErrValidation is returned when silence validation fails.
	// This wraps validation errors from silencing.Silence.Validate().
	// This typically maps to HTTP 400 Bad Request.
	ErrValidation = errors.New("validation failed")
)
