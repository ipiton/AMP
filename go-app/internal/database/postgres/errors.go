package postgres

import (
	"errors"
	"fmt"
)

// Common errors
var (
	// ErrNotConnected indicates that the pool is not connected to the database
	ErrNotConnected = errors.New("database pool is not connected")

	// ErrAlreadyConnected indicates that the pool is already connected
	ErrAlreadyConnected = errors.New("database pool is already connected")

	// ErrConnectionFailed indicates that connection to database failed
	ErrConnectionFailed = errors.New("failed to connect to database")

	// ErrConnectionClosed indicates that the connection pool is closed
	ErrConnectionClosed = errors.New("database connection pool is closed")

	// ErrHealthCheckFailed indicates that health check failed
	ErrHealthCheckFailed = errors.New("database health check failed")

	// ErrCircuitBreakerOpen indicates that circuit breaker is open
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")

	// ErrInvalidConfig indicates that configuration is invalid
	ErrInvalidConfig = errors.New("invalid database configuration")

	// ErrQueryTimeout indicates that query execution timed out
	ErrQueryTimeout = errors.New("query execution timed out")

	// ErrTransactionFailed indicates that transaction failed
	ErrTransactionFailed = errors.New("database transaction failed")

	// ErrPreparedStatementFailed indicates that prepared statement creation failed
	ErrPreparedStatementFailed = errors.New("prepared statement creation failed")
)

// DatabaseError wraps database-specific errors with additional context
type DatabaseError struct {
	Code      string
	Message   string
	Severity  string
	Detail    string
	Hint      string
	Position  string
	Query     string
	Args      []interface{}
	Operation string
	Timestamp string
}

// Error implements the error interface
func (e *DatabaseError) Error() string {
	if e.Operation != "" {
		return fmt.Sprintf("database error in %s [%s]: %s", e.Operation, e.Code, e.Message)
	}
	return fmt.Sprintf("database error [%s]: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error
func (e *DatabaseError) Unwrap() error {
	return fmt.Errorf("%s: %s", e.Code, e.Message)
}

// NewDatabaseError creates a new database error
func NewDatabaseError(code, message string) *DatabaseError {
	return &DatabaseError{
		Code:    code,
		Message: message,
	}
}

// WithOperation adds operation context to the error
func (e *DatabaseError) WithOperation(operation string) *DatabaseError {
	e.Operation = operation
	return e
}

// WithQuery adds query context to the error
func (e *DatabaseError) WithQuery(query string, args ...interface{}) *DatabaseError {
	e.Query = query
	e.Args = args
	return e
}

// WithDetails adds additional details to the error
func (e *DatabaseError) WithDetails(severity, detail, hint, position string) *DatabaseError {
	e.Severity = severity
	e.Detail = detail
	e.Hint = hint
	e.Position = position
	return e
}

// IsRetryable determines if the error is retryable
func (e *DatabaseError) IsRetryable() bool {
	// List of PostgreSQL error codes that are retryable
	retryableCodes := map[string]bool{
		"08006": true, // connection_failure
		"40001": true, // serialization_failure
		"40P01": true, // deadlock_detected
		"53300": true, // too_many_connections
		"57P01": true, // admin_shutdown
		"57P02": true, // crash_shutdown
		"57P03": true, // cannot_connect_now
	}

	return retryableCodes[e.Code]
}

// IsConnectionError determines if the error is connection-related
func (e *DatabaseError) IsConnectionError() bool {
	// List of PostgreSQL error codes that are connection-related
	connectionCodes := map[string]bool{
		"08000": true, // connection_exception
		"08003": true, // connection_does_not_exist
		"08006": true, // connection_failure
		"08001": true, // sqlclient_unable_to_establish_sqlconnection
		"08004": true, // sqlserver_rejected_establishment_of_sqlconnection
		"08007": true, // transaction_resolution_unknown
		"53300": true, // too_many_connections
	}

	return connectionCodes[e.Code]
}

// ConnectionError wraps connection-specific errors
type ConnectionError struct {
	Operation string
	Reason    string
	Duration  string
}

// Error implements the error interface
func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection error during %s: %s", e.Operation, e.Reason)
}

// NewConnectionError creates a new connection error
func NewConnectionError(operation, reason string) *ConnectionError {
	return &ConnectionError{
		Operation: operation,
		Reason:    reason,
	}
}

// WithDuration adds duration context to the error
func (e *ConnectionError) WithDuration(duration string) *ConnectionError {
	e.Duration = duration
	return e
}

// QueryError wraps query execution errors
type QueryError struct {
	Query     string
	Args      []interface{}
	Duration  string
	Operation string
}

// Error implements the error interface
func (e *QueryError) Error() string {
	return fmt.Sprintf("query error in %s after %s: %s", e.Operation, e.Duration, e.Query)
}

// NewQueryError creates a new query error
func NewQueryError(query string, args []interface{}, operation string) *QueryError {
	return &QueryError{
		Query:     query,
		Args:      args,
		Operation: operation,
	}
}

// WithDuration adds duration context to the error
func (e *QueryError) WithDuration(duration string) *QueryError {
	return &QueryError{
		Query:     e.Query,
		Args:      e.Args,
		Duration:  duration,
		Operation: e.Operation,
	}
}

// TimeoutError wraps timeout errors
type TimeoutError struct {
	Operation string
	Timeout   string
	Query     string
}

// Error implements the error interface
func (e *TimeoutError) Error() string {
	if e.Query != "" {
		return fmt.Sprintf("timeout error in %s after %s: %s", e.Operation, e.Timeout, e.Query)
	}
	return fmt.Sprintf("timeout error in %s after %s", e.Operation, e.Timeout)
}

// NewTimeoutError creates a new timeout error
func NewTimeoutError(operation, timeout string) *TimeoutError {
	return &TimeoutError{
		Operation: operation,
		Timeout:   timeout,
	}
}

// WithQuery adds query context to the error
func (e *TimeoutError) WithQuery(query string) *TimeoutError {
	e.Query = query
	return e
}

// IsTimeout checks if the error is a timeout error
func IsTimeout(err error) bool {
	var timeoutErr *TimeoutError
	return errors.As(err, &timeoutErr)
}

// IsConnectionError checks if the error is a connection error
func IsConnectionError(err error) bool {
	var connErr *ConnectionError
	var dbErr *DatabaseError
	return errors.As(err, &connErr) || (errors.As(err, &dbErr) && dbErr.IsConnectionError())
}

// IsRetryable checks if the error is retryable
func IsRetryable(err error) bool {
	var dbErr *DatabaseError
	if errors.As(err, &dbErr) {
		return dbErr.IsRetryable()
	}

	// Connection errors are generally retryable
	if IsConnectionError(err) {
		return true
	}

	// Timeout errors are retryable
	if IsTimeout(err) {
		return true
	}

	return false
}
