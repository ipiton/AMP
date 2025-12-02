package silencing

import "errors"

// Manager lifecycle errors

// ErrManagerNotStarted is returned when operations are called before Start().
//
// This error indicates that the manager hasn't been initialized yet.
// Call manager.Start(ctx) before any CRUD or filtering operations.
//
// Example:
//
//	_, err := manager.CreateSilence(ctx, silence)
//	if errors.Is(err, ErrManagerNotStarted) {
//	    log.Error("Manager not started, call Start() first")
//	}
var ErrManagerNotStarted = errors.New("silence manager not started")

// ErrManagerShutdown is returned when operations are called during/after shutdown.
//
// This error indicates that the manager is shutting down or already stopped.
// No new operations are accepted during shutdown.
//
// Example:
//
//	_, err := manager.CreateSilence(ctx, silence)
//	if errors.Is(err, ErrManagerShutdown) {
//	    log.Info("Manager shutting down, operation rejected")
//	}
var ErrManagerShutdown = errors.New("silence manager is shutting down")

// Operation errors

// ErrInvalidAlert is returned when alert validation fails.
//
// This typically means the alert is nil or has nil Labels.
//
// Example:
//
//	silenced, _, err := manager.IsAlertSilenced(ctx, nil)
//	if errors.Is(err, ErrInvalidAlert) {
//	    log.Error("Invalid alert provided")
//	}
var ErrInvalidAlert = errors.New("invalid alert")

// ErrCacheUnavailable is returned when cache operations fail.
//
// This is an internal error that shouldn't normally be returned to clients.
// The manager gracefully degrades to database queries on cache failures.
//
// Note: This error is currently not used externally (internal only).
var ErrCacheUnavailable = errors.New("cache unavailable")
