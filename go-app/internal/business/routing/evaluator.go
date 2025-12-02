// Package routing provides advanced Alertmanager-compatible routing.
//
// The RouteEvaluator orchestrates routing decisions by combining
// route matching (TN-139) with parameter inheritance (TN-138).
package routing

import (
	"fmt"
	"log/slog"
	"time"
)

// RouteEvaluator orchestrates routing decisions for alerts.
//
// Design:
//   - Lightweight wrapper around RouteMatcher
//   - Stateless (no caching, no state)
//   - Thread-safe for concurrent use
//   - Zero allocations in hot path (design goal: 1-2 max)
//
// Performance:
//   - Evaluate: <100µs typical (target: <50µs)
//   - EvaluateWithAlternatives: <200µs for 5 receivers
//   - Throughput: >10,000 evaluations/sec per core
//
// Thread Safety:
//
//	RouteEvaluator is safe for concurrent use.
//	tree and matcher are immutable after construction.
//
// Example:
//
//	evaluator := NewRouteEvaluator(tree, matcher, opts)
//	decision, err := evaluator.Evaluate(alert)
//	if err != nil {
//	    // Handle error (very rare)
//	}
//	// Use decision.Receiver, decision.GroupBy, etc.
type RouteEvaluator struct {
	// tree is the route tree (from TN-138)
	tree *RouteTree

	// matcher finds matching routes (from TN-139)
	matcher *RouteMatcher

	// metrics tracks evaluation statistics
	metrics *EvaluatorMetrics

	// opts controls evaluator behavior
	opts EvaluatorOptions
}

// EvaluatorOptions controls RouteEvaluator behavior.
type EvaluatorOptions struct {
	// EnableLogging enables debug logging (default: false)
	//
	// When enabled, logs routing decisions at DEBUG level.
	// Format: JSON with structured fields.
	EnableLogging bool

	// EnableMetrics enables Prometheus metrics (default: true)
	//
	// Tracks evaluations, duration, errors, etc.
	EnableMetrics bool

	// FallbackToRoot enables fallback to root on no match (default: true)
	//
	// When true: no matches → use root receiver
	// When false: no matches → return error
	FallbackToRoot bool
}

// DefaultEvaluatorOptions returns default evaluator options.
//
// Defaults:
//   - EnableLogging: false (debug disabled)
//   - EnableMetrics: true (metrics enabled)
//   - FallbackToRoot: true (graceful fallback)
func DefaultEvaluatorOptions() EvaluatorOptions {
	return EvaluatorOptions{
		EnableLogging:  false,
		EnableMetrics:  true,
		FallbackToRoot: true,
	}
}

// NewRouteEvaluator creates a new RouteEvaluator.
//
// Parameters:
//   - tree: The route tree (from TN-138 TreeBuilder)
//   - matcher: The route matcher (from TN-139)
//   - opts: Evaluator options (use DefaultEvaluatorOptions())
//
// Returns:
//   - *RouteEvaluator: A new evaluator instance
//
// The evaluator is stateless and thread-safe.
// Multiple goroutines can call Evaluate() concurrently.
//
// Example:
//
//	tree, _ := NewTreeBuilder(config, buildOpts).Build()
//	matcher := NewRouteMatcher(patterns, matcherOpts)
//	evaluator := NewRouteEvaluator(tree, matcher, DefaultEvaluatorOptions())
func NewRouteEvaluator(
	tree *RouteTree,
	matcher *RouteMatcher,
	opts EvaluatorOptions,
) *RouteEvaluator {
	e := &RouteEvaluator{
		tree:    tree,
		matcher: matcher,
		opts:    opts,
	}

	// Initialize metrics if enabled
	if opts.EnableMetrics {
		e.metrics = NewEvaluatorMetrics()
	}

	if opts.EnableLogging {
		slog.Info("route evaluator initialized",
			"fallback_to_root", opts.FallbackToRoot)
	}

	return e
}

// Evaluate makes a routing decision for an alert.
//
// Algorithm:
//  1. Validate input (tree != nil)
//  2. Find matching routes using matcher
//  3. If no matches: fallback to root (if enabled)
//  4. Extract first match
//  5. Build RoutingDecision from matched node
//  6. Record metrics
//  7. Return decision
//
// Parameters:
//   - alert: The alert to route
//
// Returns:
//   - *RoutingDecision: Complete routing decision
//   - error: Only on unrecoverable error (empty tree, no receiver)
//
// If no routes match and FallbackToRoot=true: returns root decision.
// If no routes match and FallbackToRoot=false: returns error.
//
// Complexity: O(N) where N = routes evaluated by matcher
//
// Performance:
//   - Typical: <50µs (matcher ~30µs + overhead ~20µs)
//   - No-match: <30µs (fast path)
//   - Deep tree: <100µs
//
// Example:
//
//	decision, err := evaluator.Evaluate(alert)
//	if err != nil {
//	    return fmt.Errorf("routing failed: %w", err)
//	}
//	// Use decision
//	receiver := decision.Receiver
//	groupBy := decision.GroupBy
func (e *RouteEvaluator) Evaluate(alert *Alert) (*RoutingDecision, error) {
	// Step 1: Validate input
	if e.tree == nil || e.tree.Root == nil {
		if e.metrics != nil {
			e.metrics.RecordError("empty_tree")
		}
		return nil, ErrEmptyTree
	}

	start := time.Now()

	// Step 2: Find matching routes
	matchResult := e.matcher.FindMatchingRoutes(e.tree, alert)

	// Step 3: Handle no matches
	var node *RouteNode
	var matchedPath string

	if matchResult.Empty() {
		if !e.opts.FallbackToRoot {
			// No fallback: return error
			if e.metrics != nil {
				e.metrics.RecordError("no_match")
			}
			return nil, ErrNoMatch
		}

		// Fallback to root
		node = e.tree.Root
		matchedPath = "/ (root default)"

		if e.metrics != nil {
			e.metrics.NoMatchTotal.Inc()
		}

		if e.opts.EnableLogging {
			slog.Debug("no matching route, using root default",
				"alert", alert.Labels["alertname"])
		}
	} else {
		// Step 4: Extract first match
		node = matchResult.First()
		matchedPath = node.Path
	}

	// Step 5: Build decision
	decision := &RoutingDecision{
		Receiver:        node.Receiver,
		GroupBy:         node.GroupBy,
		GroupWait:       node.GroupWait,
		GroupInterval:   node.GroupInterval,
		RepeatInterval:  node.RepeatInterval,
		MatchedRoute:    matchedPath,
		MatchDuration:   matchResult.Duration,
		RoutesEvaluated: matchResult.MatchersEvaluated,
		CacheHitRate:    matchResult.CacheHitRate(),
	}

	// Validate receiver is not empty
	if decision.Receiver == "" {
		if e.metrics != nil {
			e.metrics.RecordError("no_receiver")
		}
		return nil, fmt.Errorf("%w: matched route %s has no receiver",
			ErrNoReceiver, matchedPath)
	}

	// Step 6: Record metrics
	totalDuration := time.Since(start)
	if e.metrics != nil {
		e.metrics.RecordEvaluation(decision.Receiver, totalDuration)
	}

	// Step 7: Debug logging
	if e.opts.EnableLogging {
		slog.Debug("routing decision made",
			"alert", alert.Labels["alertname"],
			"receiver", decision.Receiver,
			"matched_route", decision.MatchedRoute,
			"duration_us", totalDuration.Microseconds(),
			"group_by", decision.GroupBy,
			"routes_evaluated", decision.RoutesEvaluated,
			"cache_hit_rate", fmt.Sprintf("%.2f%%", decision.CacheHitRate*100),
		)
	}

	return decision, nil
}

// EvaluateWithAlternatives returns primary + alternative routing decisions.
//
// Use this when you need all matching receivers (continue=true scenario)
// or want detailed statistics for debugging.
//
// Algorithm:
//  1. Validate input
//  2. Find matching routes using matcher
//  3. If no matches: return root decision only
//  4. Build primary decision (first match)
//  5. Build alternative decisions (remaining matches)
//  6. Aggregate statistics
//  7. Return EvaluationResult
//
// Parameters:
//   - alert: The alert to route
//
// Returns:
//   - *EvaluationResult: Complete evaluation result with alternatives
//
// EvaluationResult always includes Primary decision.
// Alternatives are populated only if multiple routes matched.
//
// Complexity: O(M) where M = number of matches
//
// Performance:
//   - 1 match: <50µs
//   - 5 matches: <200µs
//   - 10 matches: <400µs
//
// Example:
//
//	result := evaluator.EvaluateWithAlternatives(alert)
//	if result.Error != nil {
//	    // Handle error
//	}
//
//	// Use primary decision
//	publishTo(result.Primary.Receiver, alert)
//
//	// Use alternatives (if continue=true)
//	for _, alt := range result.Alternatives {
//	    publishTo(alt.Receiver, alert)
//	}
func (e *RouteEvaluator) EvaluateWithAlternatives(
	alert *Alert,
) *EvaluationResult {
	start := time.Now()

	result := &EvaluationResult{
		Alternatives: make([]*RoutingDecision, 0, 4), // Pre-allocate typical size
	}

	// Step 1: Validate input
	if e.tree == nil || e.tree.Root == nil {
		result.Error = ErrEmptyTree
		if e.metrics != nil {
			e.metrics.RecordError("empty_tree")
		}
		return result
	}

	// Step 2: Find matching routes
	matchResult := e.matcher.FindMatchingRoutes(e.tree, alert)

	// Step 3: Handle no matches
	if matchResult.Empty() {
		if !e.opts.FallbackToRoot {
			result.Error = ErrNoMatch
			if e.metrics != nil {
				e.metrics.RecordError("no_match")
			}
			return result
		}

		// Fallback to root
		result.Primary = e.buildDecision(
			e.tree.Root,
			"/ (root default)",
			matchResult,
		)

		if e.metrics != nil {
			e.metrics.NoMatchTotal.Inc()
		}

		result.TotalDuration = time.Since(start)
		result.RoutesEvaluated = matchResult.MatchersEvaluated
		result.CacheHitRate = matchResult.CacheHitRate()

		return result
	}

	// Step 4: Build primary decision
	result.Primary = e.buildDecision(
		matchResult.Matches[0],
		matchResult.Matches[0].Path,
		matchResult,
	)

	// Validate primary receiver
	if result.Primary.Receiver == "" {
		result.Error = fmt.Errorf("%w: matched route %s has no receiver",
			ErrNoReceiver, result.Primary.MatchedRoute)
		if e.metrics != nil {
			e.metrics.RecordError("no_receiver")
		}
		return result
	}

	// Step 5: Build alternative decisions
	for _, node := range matchResult.Matches[1:] {
		decision := e.buildDecision(node, node.Path, matchResult)

		// Skip alternatives with no receiver
		if decision.Receiver == "" {
			if e.opts.EnableLogging {
				slog.Warn("skipping alternative with no receiver",
					"route", node.Path)
			}
			continue
		}

		result.Alternatives = append(result.Alternatives, decision)
	}

	// Step 6: Aggregate statistics
	result.TotalDuration = time.Since(start)
	result.RoutesEvaluated = matchResult.MatchersEvaluated
	result.CacheHitRate = matchResult.CacheHitRate()

	// Step 7: Record metrics
	if e.metrics != nil {
		e.metrics.RecordEvaluation(result.Primary.Receiver, result.TotalDuration)

		if len(result.Alternatives) > 0 {
			e.metrics.MultiReceiverTotal.Inc()
		}
	}

	// Debug logging
	if e.opts.EnableLogging {
		slog.Debug("evaluation with alternatives complete",
			"alert", alert.Labels["alertname"],
			"primary_receiver", result.Primary.Receiver,
			"alternatives", len(result.Alternatives),
			"duration_us", result.TotalDuration.Microseconds(),
		)
	}

	return result
}

// buildDecision builds a RoutingDecision from a matched node.
//
// Helper function to avoid code duplication between
// Evaluate() and EvaluateWithAlternatives().
//
// Parameters:
//   - node: The matched route node
//   - path: The route path (for debugging)
//   - matchResult: The match result (for statistics)
//
// Returns:
//   - *RoutingDecision: The complete routing decision
func (e *RouteEvaluator) buildDecision(
	node *RouteNode,
	path string,
	matchResult *MatchResult,
) *RoutingDecision {
	return &RoutingDecision{
		Receiver:        node.Receiver,
		GroupBy:         node.GroupBy,
		GroupWait:       node.GroupWait,
		GroupInterval:   node.GroupInterval,
		RepeatInterval:  node.RepeatInterval,
		MatchedRoute:    path,
		MatchDuration:   matchResult.Duration,
		RoutesEvaluated: matchResult.MatchersEvaluated,
		CacheHitRate:    matchResult.CacheHitRate(),
	}
}

// GetMetrics returns the evaluator's metrics instance.
//
// Returns nil if metrics are disabled (opts.EnableMetrics=false).
func (e *RouteEvaluator) GetMetrics() *EvaluatorMetrics {
	return e.metrics
}
