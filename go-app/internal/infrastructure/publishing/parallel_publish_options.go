package publishing

import (
	"fmt"
	"time"
)

// ParallelPublishOptions configures the behavior of parallel publishing.
//
// This structure contains:
//   - Timeout configuration (max time for all publishes)
//   - Health check configuration (enable, strategy)
//   - Worker pool configuration (max concurrent goroutines, use worker pool)
//   - Circuit breaker configuration (respect circuit breakers)
//
// Example usage:
//
//	// Use default options
//	options := DefaultParallelPublishOptions()
//
//	// Customize options
//	options := ParallelPublishOptions{
//	    Timeout:                60 * time.Second,           // 60s timeout
//	    CheckHealth:            true,                       // Enable health checks
//	    HealthStrategy:         SkipUnhealthy,              // Skip unhealthy targets
//	    MaxConcurrent:          20,                         // Max 20 concurrent goroutines
//	    UseWorkerPool:          false,                      // Use direct goroutines (not worker pool)
//	    RespectCircuitBreakers: true,                       // Skip targets with open circuit breakers
//	}
//
//	parallelPublisher := NewDefaultParallelPublisher(
//	    factory,
//	    healthMonitor,
//	    discoveryMgr,
//	    metrics,
//	    logger,
//	    options,
//	)
type ParallelPublishOptions struct {
	// Timeout

	// Timeout is the maximum time to wait for all publishes to complete.
	// If this timeout is exceeded, the context is cancelled and remaining goroutines are stopped.
	//
	// Default: 30 seconds
	// Recommended: 30-60 seconds (depending on target count and network latency)
	//
	// Note: This is the total time for ALL publishes (parallel), not per-target.
	Timeout time.Duration

	// Health Checks

	// CheckHealth enables health checks before publishing.
	// If true, targets are filtered based on health status before publishing.
	// If false, all targets are published to (no health filtering).
	//
	// Default: true
	// Recommended: true (to skip unhealthy targets)
	CheckHealth bool

	// HealthStrategy defines the health check strategy.
	// This determines which targets are skipped based on health status.
	//
	// Possible values:
	//   - SkipUnhealthy: Skip unhealthy targets (default)
	//   - PublishToAll: Publish to all targets (ignore health)
	//   - SkipUnhealthyAndDegraded: Skip unhealthy and degraded targets
	//
	// Default: SkipUnhealthy
	HealthStrategy HealthCheckStrategy

	// Worker Pool

	// MaxConcurrent is the maximum number of concurrent goroutines.
	// This limits the number of targets published to in parallel.
	//
	// Default: 10
	// Recommended: 10-20 (depending on target count and resource availability)
	//
	// Note: This is only used if UseWorkerPool is true.
	MaxConcurrent int

	// UseWorkerPool enables goroutine pooling for parallel publishing.
	// If true, a worker pool is used to reuse goroutines (reduces spawn overhead).
	// If false, direct goroutines are spawned per target (simpler, no pooling).
	//
	// Default: false (direct goroutines)
	// Recommended: false (unless publishing to 20+ targets frequently)
	//
	// Trade-offs:
	//   - Worker pool: Lower overhead, better resource control, more complex
	//   - Direct goroutines: Simpler, easier to debug, slightly higher overhead
	UseWorkerPool bool

	// Circuit Breakers

	// RespectCircuitBreakers enables circuit breaker checks before publishing.
	// If true, targets with open circuit breakers are skipped.
	// If false, all targets are published to (no circuit breaker filtering).
	//
	// Default: true
	// Recommended: true (to avoid cascading failures)
	RespectCircuitBreakers bool
}

// DefaultParallelPublishOptions returns the default parallel publish options.
//
// Default values:
//   - Timeout: 30 seconds
//   - CheckHealth: true (enable health checks)
//   - HealthStrategy: SkipUnhealthy (skip unhealthy targets)
//   - MaxConcurrent: 10 goroutines
//   - UseWorkerPool: false (use direct goroutines)
//   - RespectCircuitBreakers: true (skip targets with open circuit breakers)
//
// Example:
//
//	options := DefaultParallelPublishOptions()
//	// Customize if needed
//	options.Timeout = 60 * time.Second
//	options.MaxConcurrent = 20
func DefaultParallelPublishOptions() ParallelPublishOptions {
	return ParallelPublishOptions{
		Timeout:                30 * time.Second,
		CheckHealth:            true,
		HealthStrategy:         SkipUnhealthy,
		MaxConcurrent:          10,
		UseWorkerPool:          false,
		RespectCircuitBreakers: true,
	}
}

// Validate validates the parallel publish options.
//
// This method checks:
//   - Timeout > 0
//   - MaxConcurrent > 0
//   - HealthStrategy is valid
//
// Returns:
//   - nil if valid
//   - error if invalid
//
// Example:
//
//	options := ParallelPublishOptions{
//	    Timeout:       0, // Invalid
//	    MaxConcurrent: 0, // Invalid
//	}
//	if err := options.Validate(); err != nil {
//	    log.Fatal("Invalid options", "error", err)
//	}
func (o *ParallelPublishOptions) Validate() error {
	if o.Timeout <= 0 {
		return fmt.Errorf("timeout must be > 0 (got %v)", o.Timeout)
	}

	if o.MaxConcurrent <= 0 {
		return fmt.Errorf("max_concurrent must be > 0 (got %d)", o.MaxConcurrent)
	}

	// Validate health strategy
	switch o.HealthStrategy {
	case SkipUnhealthy, PublishToAll, SkipUnhealthyAndDegraded:
		// Valid
	default:
		return fmt.Errorf("invalid health_strategy: %d", o.HealthStrategy)
	}

	return nil
}

// HealthCheckStrategy defines the health check strategy for parallel publishing.
//
// This enum determines which targets are skipped based on health status:
//   - SkipUnhealthy: Skip unhealthy targets (publish to healthy, degraded, unknown)
//   - PublishToAll: Publish to all targets (ignore health status)
//   - SkipUnhealthyAndDegraded: Skip unhealthy and degraded targets (publish to healthy, unknown)
//
// Example usage:
//
//	options := ParallelPublishOptions{
//	    CheckHealth:    true,
//	    HealthStrategy: SkipUnhealthy, // Skip unhealthy targets
//	}
//
//	// Or use PublishToAll to ignore health
//	options.HealthStrategy = PublishToAll
type HealthCheckStrategy int

const (
	// SkipUnhealthy skips unhealthy targets (default).
	//
	// Publishes to:
	//   - healthy targets (last check succeeded, latency < 5s)
	//   - degraded targets (last check succeeded, latency >= 5s)
	//   - unknown targets (no checks performed yet)
	//
	// Skips:
	//   - unhealthy targets (3+ consecutive failures)
	//
	// This is the recommended strategy for most use cases.
	SkipUnhealthy HealthCheckStrategy = iota

	// PublishToAll publishes to all targets (ignore health status).
	//
	// Publishes to:
	//   - healthy targets
	//   - unhealthy targets
	//   - degraded targets
	//   - unknown targets
	//
	// Skips: None
	//
	// This strategy is useful when health checks are unreliable or when
	// you want to publish to all targets regardless of health status.
	PublishToAll

	// SkipUnhealthyAndDegraded skips unhealthy and degraded targets.
	//
	// Publishes to:
	//   - healthy targets (last check succeeded, latency < 5s)
	//   - unknown targets (no checks performed yet)
	//
	// Skips:
	//   - unhealthy targets (3+ consecutive failures)
	//   - degraded targets (last check succeeded, latency >= 5s)
	//
	// This strategy is useful when you want to publish only to fast, healthy targets.
	SkipUnhealthyAndDegraded
)

// String returns the string representation of the health check strategy.
//
// Returns:
//   - "skip_unhealthy" for SkipUnhealthy
//   - "publish_to_all" for PublishToAll
//   - "skip_unhealthy_and_degraded" for SkipUnhealthyAndDegraded
//   - "unknown" for invalid values
//
// Example:
//
//	strategy := SkipUnhealthy
//	log.Info("Health strategy", "strategy", strategy.String())
//	// Output: Health strategy strategy=skip_unhealthy
func (s HealthCheckStrategy) String() string {
	switch s {
	case SkipUnhealthy:
		return "skip_unhealthy"
	case PublishToAll:
		return "publish_to_all"
	case SkipUnhealthyAndDegraded:
		return "skip_unhealthy_and_degraded"
	default:
		return "unknown"
	}
}
