package routing

import "errors"

// Evaluator-specific errors
//
// Note: ErrEmptyTree is defined in matcher_errors.go and shared
// between RouteMatcher and RouteEvaluator.
var (
	// ErrNoReceiver indicates matched route has no receiver.
	//
	// This should be caught at config validation time (TN-137),
	// but we check at runtime as a safety measure.
	//
	// If this occurs, it indicates an invalid route config
	// where a route has matchers but no receiver.
	ErrNoReceiver = errors.New("no receiver in matched route")

	// ErrNoMatch indicates no routes matched the alert.
	//
	// This is only returned when FallbackToRoot=false.
	//
	// By default (FallbackToRoot=true), the evaluator
	// gracefully falls back to the root receiver instead
	// of returning an error.
	ErrNoMatch = errors.New("no matching routes")
)
