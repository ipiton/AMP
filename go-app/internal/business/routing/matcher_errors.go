package routing

import "errors"

// Matcher errors
var (
	// ErrInvalidPattern indicates an invalid regex pattern.
	// This should be caught at config parse time (TN-137).
	ErrInvalidPattern = errors.New("invalid regex pattern")

	// ErrEmptyTree indicates an empty route tree was provided.
	ErrEmptyTree = errors.New("empty route tree")

	// ErrNoMatches indicates no routes matched the alert.
	// Caller should use root default receiver.
	ErrNoMatches = errors.New("no matching routes")

	// ErrContextCancelled indicates matching was cancelled by context.
	// This can occur with FindMatchingRoutesWithContext() when
	// the context times out or is cancelled.
	ErrContextCancelled = errors.New("matching cancelled by context")
)
