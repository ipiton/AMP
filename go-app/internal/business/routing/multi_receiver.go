// Package routing provides multi-receiver support for parallel publishing.
package routing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// MultiReceiverPublisher publishes alerts to multiple receivers in parallel.
//
// Design:
//   - Goroutine per receiver (up to MaxConcurrent)
//   - sync.WaitGroup for coordination
//   - context.WithTimeout per receiver (10s default)
//   - Panic recovery in each goroutine
//   - Independent error handling (one failure doesn't affect others)
//
// Performance:
//   - Parallel speedup: ~5x vs sequential for 5 receivers
//   - Total duration: max(receiver_durations), not sum
//   - Example: 5 receivers × 100ms = 500ms sequential → 100ms parallel
//
// Thread Safety:
//
//	Safe for concurrent use (stateless per publish).
//	evaluator and publishers are immutable.
//
// Example:
//
//	publisher := NewMultiReceiverPublisher(evaluator, publishers, opts)
//	result, err := publisher.PublishMulti(ctx, alert)
//	if result.IsFullSuccess() {
//	    log.Info("all receivers succeeded")
//	} else if result.IsPartialSuccess() {
//	    log.Warn("partial success", "failed", result.FailedReceivers())
//	}
type MultiReceiverPublisher struct {
	// evaluator determines which receivers to use
	evaluator *RouteEvaluator

	// publishers maps receiver name to Publisher implementation
	// Example: {"pagerduty": PagerDutyPublisher{}, "slack": SlackPublisher{}}
	publishers map[string]Publisher

	// metrics tracks Prometheus metrics
	metrics *MultiReceiverMetrics

	// opts controls publisher behavior
	opts MultiReceiverOptions
}

// MultiReceiverOptions controls MultiReceiverPublisher behavior.
type MultiReceiverOptions struct {
	// EnableMetrics enables Prometheus metrics (default: true)
	EnableMetrics bool

	// EnableLogging enables debug logging (default: false)
	EnableLogging bool

	// PerReceiverTimeout is timeout per receiver (default: 10s)
	//
	// Each receiver gets its own context.WithTimeout.
	// If receiver exceeds timeout, it's marked as failed.
	PerReceiverTimeout time.Duration

	// MaxConcurrent is max concurrent goroutines (default: 10)
	//
	// Limits parallel receivers to avoid resource exhaustion.
	// Currently unused (no limit), but reserved for future use.
	MaxConcurrent int
}

// Publisher is the interface for publishing to a receiver.
//
// Each receiver (pagerduty, slack, webhook) implements this.
type Publisher interface {
	// Publish sends alert to the receiver.
	//
	// Must respect ctx cancellation and timeout.
	// Must be safe for concurrent use.
	//
	// Returns:
	// - nil: Success
	// - error: Failure (timeout, network, etc.)
	Publish(ctx context.Context, alert *Alert) error
}

// DefaultMultiReceiverOptions returns default options.
//
// Defaults:
//   - EnableMetrics: true
//   - EnableLogging: false (debug disabled)
//   - PerReceiverTimeout: 10s
//   - MaxConcurrent: 10
func DefaultMultiReceiverOptions() MultiReceiverOptions {
	return MultiReceiverOptions{
		EnableMetrics:      true,
		EnableLogging:      false,
		PerReceiverTimeout: 10 * time.Second,
		MaxConcurrent:      10,
	}
}

// NewMultiReceiverPublisher creates a new multi-receiver publisher.
//
// Parameters:
//   - evaluator: RouteEvaluator to determine receivers
//   - publishers: Map of receiver name → Publisher implementation
//   - opts: Configuration options
//
// Returns:
//   - *MultiReceiverPublisher: A new publisher instance
//
// The publisher is stateless and thread-safe.
// Multiple goroutines can call PublishMulti() concurrently.
//
// Example:
//
//	publishers := map[string]Publisher{
//	    "pagerduty": NewPagerDutyPublisher(...),
//	    "slack":     NewSlackPublisher(...),
//	}
//	multiPublisher := NewMultiReceiverPublisher(evaluator, publishers, opts)
func NewMultiReceiverPublisher(
	evaluator *RouteEvaluator,
	publishers map[string]Publisher,
	opts MultiReceiverOptions,
) *MultiReceiverPublisher {
	p := &MultiReceiverPublisher{
		evaluator:  evaluator,
		publishers: publishers,
		opts:       opts,
	}

	// Initialize metrics if enabled
	if opts.EnableMetrics {
		p.metrics = NewMultiReceiverMetrics()
	}

	if opts.EnableLogging {
		slog.Info("multi-receiver publisher initialized",
			"receivers", len(publishers),
			"timeout", opts.PerReceiverTimeout)
	}

	return p
}

// PublishMulti publishes alert to all matching receivers in parallel.
//
// Algorithm:
//  1. Evaluate routes (get all matching receivers)
//  2. Collect receivers (primary + alternatives)
//  3. Create result collector (slice + WaitGroup)
//  4. Launch goroutines (one per receiver)
//     - Per-receiver timeout context
//     - Panic recovery
//     - Publish to receiver
//     - Record result
//  5. Wait for all goroutines
//  6. Aggregate results
//  7. Record metrics
//  8. Return result + error (if all failed)
//
// Parameters:
//   - ctx: Context for cancellation
//   - alert: Alert to publish
//
// Returns:
//   - *MultiReceiverResult: Aggregate result with per-receiver details
//   - error: Only if all receivers failed or no receivers found
//
// Partial success is considered success (no error returned).
//
// Complexity: O(1) with respect to receivers (parallel execution)
//
// Performance:
//   - 1 receiver: ~100ms (single publish)
//   - 5 receivers: ~100ms (parallel, not 500ms!)
//   - 10 receivers: ~100ms (parallel, not 1000ms!)
//
// Example:
//
//	result, err := publisher.PublishMulti(ctx, alert)
//	if err != nil {
//	    // All receivers failed or no receivers
//	    return err
//	}
//	// Use result
//	log.Info("published",
//	    "success", result.SuccessCount,
//	    "failure", result.FailureCount)
func (p *MultiReceiverPublisher) PublishMulti(
	ctx context.Context,
	alert *Alert,
) (*MultiReceiverResult, error) {
	start := time.Now()

	// Step 1: Evaluate routes
	evalResult := p.evaluator.EvaluateWithAlternatives(alert)
	if evalResult.Error != nil {
		if p.metrics != nil {
			p.metrics.RecordError("evaluation_failed")
		}
		return nil, fmt.Errorf("route evaluation failed: %w", evalResult.Error)
	}

	// Step 2: Collect all receivers
	receivers := p.collectReceivers(evalResult)
	if len(receivers) == 0 {
		if p.metrics != nil {
			p.metrics.RecordError("no_receivers")
		}
		return nil, ErrNoReceivers
	}

	if p.opts.EnableLogging {
		slog.Info("multi-receiver publish started",
			"alert", alert.Labels["alertname"],
			"receivers", len(receivers))
	}

	// Step 3: Create result collector
	results := make([]*ReceiverResult, len(receivers))
	var wg sync.WaitGroup

	// Step 4: Launch goroutines
	for i, receiver := range receivers {
		wg.Add(1)
		go p.publishToReceiver(ctx, alert, receiver, i, results, &wg)
	}

	// Step 5: Wait for all
	wg.Wait()

	// Step 6: Aggregate results
	result := p.aggregateResults(results, time.Since(start))

	// Step 7: Record metrics
	if p.metrics != nil {
		p.metrics.RecordPublish(result)
	}

	// Logging
	if p.opts.EnableLogging {
		slog.Info("multi-receiver publish complete",
			"success", result.SuccessCount,
			"failure", result.FailureCount,
			"duration_ms", result.TotalDuration.Milliseconds())
	}

	// Step 8: Return result
	if result.FailureCount == result.TotalReceivers {
		// All failed
		return result, ErrAllReceiversFailed
	}

	return result, nil
}

// collectReceivers collects all receiver names from evaluation result.
func (p *MultiReceiverPublisher) collectReceivers(
	evalResult *EvaluationResult,
) []string {
	if evalResult.Primary == nil {
		return []string{}
	}

	receivers := make([]string, 0, 1+len(evalResult.Alternatives))
	receivers = append(receivers, evalResult.Primary.Receiver)

	for _, alt := range evalResult.Alternatives {
		receivers = append(receivers, alt.Receiver)
	}

	return receivers
}

// publishToReceiver publishes to a single receiver (goroutine).
//
// Design:
// - Per-receiver timeout context
// - Panic recovery (one panic doesn't crash others)
// - Error captured in result
// - WaitGroup.Done() guaranteed (defer)
func (p *MultiReceiverPublisher) publishToReceiver(
	parentCtx context.Context,
	alert *Alert,
	receiver string,
	index int,
	results []*ReceiverResult,
	wg *sync.WaitGroup,
) {
	// Ensure WaitGroup.Done() called
	defer wg.Done()

	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			results[index] = &ReceiverResult{
				Receiver: receiver,
				Success:  false,
				Error:    fmt.Errorf("panic: %v", r),
			}

			if p.opts.EnableLogging {
				slog.Error("receiver publish panicked",
					"receiver", receiver,
					"panic", r)
			}
		}
	}()

	// Per-receiver timeout
	ctx, cancel := context.WithTimeout(
		parentCtx,
		p.opts.PerReceiverTimeout,
	)
	defer cancel()

	// Find publisher
	publisher, ok := p.publishers[receiver]
	if !ok {
		results[index] = &ReceiverResult{
			Receiver: receiver,
			Success:  false,
			Duration: 0,
			Error:    fmt.Errorf("no publisher for receiver: %s", receiver),
		}
		return
	}

	// Publish
	start := time.Now()
	err := publisher.Publish(ctx, alert)
	duration := time.Since(start)

	// Record result
	results[index] = &ReceiverResult{
		Receiver: receiver,
		Success:  err == nil,
		Duration: duration,
		Error:    err,
	}

	// Log
	if p.opts.EnableLogging {
		if err != nil {
			slog.Warn("receiver publish failed",
				"receiver", receiver,
				"duration_ms", duration.Milliseconds(),
				"error", err)
		} else {
			slog.Debug("receiver publish succeeded",
				"receiver", receiver,
				"duration_ms", duration.Milliseconds())
		}
	}
}

// aggregateResults aggregates per-receiver results.
func (p *MultiReceiverPublisher) aggregateResults(
	results []*ReceiverResult,
	totalDuration time.Duration,
) *MultiReceiverResult {
	successCount := 0
	failureCount := 0

	for _, r := range results {
		if r.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	return &MultiReceiverResult{
		TotalReceivers: len(results),
		SuccessCount:   successCount,
		FailureCount:   failureCount,
		Results:        results,
		TotalDuration:  totalDuration,
	}
}

// GetMetrics returns the publisher's metrics instance.
//
// Returns nil if metrics are disabled (opts.EnableMetrics=false).
func (p *MultiReceiverPublisher) GetMetrics() *MultiReceiverMetrics {
	return p.metrics
}
