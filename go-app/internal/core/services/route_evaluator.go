package services

import "time"

// RoutingDecision is the publish-relevant subset of a routing decision made
// against the full Alertmanager-compatible route tree (task 1.3: route:/
// receivers: parsing, task 1.4: service runtime wiring).
//
// It intentionally mirrors internal/business/routing.RoutingDecision's
// fields rather than importing that type directly: internal/business/*
// depends on internal/core, so core/services importing business/routing
// would create an import cycle. internal/application adapts the business
// type into this one (see route_evaluator.go in that package).
type RoutingDecision struct {
	// Receiver is the target receiver name for the matched route.
	Receiver string

	// GroupBy are the labels to group alerts by (inherited from the
	// matched route, or the tree default).
	GroupBy []string

	// GroupWait is the initial delay before the first notification.
	GroupWait time.Duration

	// GroupInterval is the delay between notifications for the same group.
	GroupInterval time.Duration

	// RepeatInterval is the delay before re-sending an unchanged group.
	RepeatInterval time.Duration

	// MatchedRoute is the route path that matched (debugging only).
	MatchedRoute string
}

// RouteEvaluator computes a RoutingDecision for an alert's labels against
// the configured route tree.
//
// Optional on AlertProcessorConfig: when nil (no `route:` section in
// config — lite/legacy single-receiver mode, task 1.3), AlertProcessor
// skips route evaluation entirely and today's publish behavior is
// unchanged. When set, AlertProcessor computes and logs a decision on
// every alert but does NOT change the publish path itself (task 1.4 scope;
// wiring GroupBy/timers/receiver into grouping and publishing is task 2.3).
type RouteEvaluator interface {
	Evaluate(labels map[string]string) (*RoutingDecision, error)
}
