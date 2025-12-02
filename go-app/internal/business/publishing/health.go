// Package publishing provides target health monitoring for publishing system.
//
// This package implements automatic health checks for publishing targets discovered
// from Kubernetes Secrets (TN-047), with support for:
//   - Periodic health checks (background worker, 2m interval)
//   - Manual health checks (HTTP API trigger)
//   - HTTP connectivity tests (TCP + HTTP GET)
//   - Status tracking (healthy/unhealthy/degraded/unknown)
//   - Failure detection (3 consecutive failures → unhealthy)
//   - Recovery detection (1 success → healthy)
//   - Prometheus metrics (6 metrics)
//   - Structured logging (slog)
//
// Key Components:
//   - HealthMonitor: Interface for health monitoring lifecycle
//   - DefaultHealthMonitor: Production implementation
//   - TargetHealthStatus: Health status data structure
//   - HealthCheckResult: Individual check result
//   - healthStatusCache: Thread-safe status cache (O(1) lookups)
//
// Example Usage:
//
//	// Create health monitor
//	config := publishing.DefaultHealthConfig()
//	healthMonitor, err := publishing.NewHealthMonitor(
//	    discoveryMgr,
//	    config,
//	    slog.Default(),
//	    metricsRegistry,
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Start background worker
//	if err := healthMonitor.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer healthMonitor.Stop(10 * time.Second)
//
//	// Get health status
//	health, err := healthMonitor.GetHealthByName(ctx, "rootly-prod")
//	if err != nil {
//	    log.Error("Target not found", err)
//	}
//
//	// Check target health before publishing
//	if health.Status.IsUnhealthy() {
//	    log.Warn("Skipping unhealthy target", "target", "rootly-prod")
//	    return nil // Don't publish
//	}
//
// See TN-049 for detailed design documentation.
package publishing

import (
	"context"
	"time"
)

// HealthMonitor manages target health checks lifecycle.
//
// This interface provides methods for:
//   - Starting/stopping background health worker (periodic checks)
//   - Getting current health status (for API/publishing)
//   - Triggering immediate health check (manual via API)
//   - Getting aggregate statistics (for monitoring)
//
// Thread Safety:
//   - All methods are safe for concurrent use
//   - Multiple goroutines can call methods simultaneously
//
// Performance:
//   - Start/Stop: <1ms (O(1), non-blocking)
//   - GetHealth: <50ms (O(n) for n targets, cached)
//   - GetHealthByName: <10ms (O(1) cache lookup)
//   - CheckNow: <500ms (includes HTTP request)
//   - GetStats: <20ms (O(n) aggregation)
//
// Error Handling:
//   - Graceful degradation on K8s API failures
//   - Health check failures don't crash service
//   - Stale cache retained until successful refresh
//
// Example:
//
//	manager, _ := NewHealthMonitor(discovery, config, logger, metrics)
//
//	// Start background worker
//	if err := manager.Start(); err != nil {
//	    log.Fatal("Failed to start health monitor", err)
//	}
//
//	// Graceful shutdown
//	defer manager.Stop(10 * time.Second)
//
//	// Manual health check
//	status, err := manager.CheckNow(ctx, "rootly-prod")
//	if err != nil {
//	    log.Error("Health check failed", err)
//	}
//
//	// Get all health statuses
//	allHealth, err := manager.GetHealth(ctx)
//	for _, h := range allHealth {
//	    log.Info("Target health", "name", h.TargetName, "status", h.Status)
//	}
type HealthMonitor interface {
	// Start begins background health check worker.
	//
	// This method:
	//   1. Validates monitor not already started
	//   2. Creates background goroutine for periodic checks
	//   3. Schedules first check (after 10s warmup period)
	//   4. Returns immediately (non-blocking)
	//
	// Returns:
	//   - nil on success
	//   - ErrAlreadyStarted if monitor already running
	//
	// Performance: <1ms (O(1), spawns goroutine)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	if err := manager.Start(); err != nil {
	//	    if errors.Is(err, ErrAlreadyStarted) {
	//	        log.Warn("Health monitor already running")
	//	    } else {
	//	        log.Fatal("Failed to start health monitor", err)
	//	    }
	//	}
	Start() error

	// Stop gracefully stops background health check worker.
	//
	// This method:
	//   1. Cancels context (stops new checks)
	//   2. Waits for current check to complete (max timeout)
	//   3. Cleans up goroutine resources
	//   4. Returns when fully stopped or timeout exceeded
	//
	// Parameters:
	//   - timeout: Max time to wait for graceful shutdown (e.g., 10s)
	//     If timeout exceeded, forces shutdown (may leak goroutine)
	//
	// Returns:
	//   - nil if stopped cleanly within timeout
	//   - ErrShutdownTimeout if timeout exceeded
	//   - ErrNotStarted if monitor not running
	//
	// Performance:
	//   - Normal case: <5s (waits for current check)
	//   - Timeout case: ~timeout duration
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	// Graceful shutdown with 10s timeout
	//	if err := manager.Stop(10 * time.Second); err != nil {
	//	    if errors.Is(err, ErrShutdownTimeout) {
	//	        log.Error("Force shutdown after timeout", err)
	//	    }
	//	}
	Stop(timeout time.Duration) error

	// GetHealth returns current health status for all targets.
	//
	// This method:
	//   1. Retrieves all targets from TargetDiscoveryManager
	//   2. Looks up health status from cache (O(1) per target)
	//   3. Returns array of TargetHealthStatus (enabled + disabled)
	//
	// Returns:
	//   - []TargetHealthStatus: Health status for all targets
	//   - error: If failed to get targets from discovery manager
	//
	// Performance: <50ms (O(n) for n targets, cached data)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	allHealth, err := manager.GetHealth(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("failed to get health status: %w", err)
	//	}
	//
	//	for _, h := range allHealth {
	//	    if h.Status.IsUnhealthy() {
	//	        log.Warn("Unhealthy target", "name", h.TargetName)
	//	    }
	//	}
	GetHealth(ctx context.Context) ([]TargetHealthStatus, error)

	// GetHealthByName returns health status for single target.
	//
	// This method:
	//   1. Validates target exists in TargetDiscoveryManager
	//   2. Looks up health status from cache (O(1))
	//   3. Returns TargetHealthStatus or error if not found
	//
	// Parameters:
	//   - targetName: Name of target (e.g., "rootly-prod")
	//
	// Returns:
	//   - *TargetHealthStatus: Health status (never nil if no error)
	//   - error: ErrTargetNotFound if target doesn't exist
	//
	// Performance: <10ms (O(1) lookup)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	health, err := manager.GetHealthByName(ctx, "rootly-prod")
	//	if err != nil {
	//	    if errors.Is(err, ErrTargetNotFound) {
	//	        return fmt.Errorf("target not found: %w", err)
	//	    }
	//	    return err
	//	}
	//
	//	if health.Status.IsHealthy() {
	//	    // Publish to target
	//	    publishToTarget(alert, target)
	//	}
	GetHealthByName(ctx context.Context, targetName string) (*TargetHealthStatus, error)

	// CheckNow triggers immediate health check for target.
	//
	// This method:
	//   1. Validates target exists
	//   2. Performs immediate HTTP connectivity test
	//   3. Updates health status (bypasses failure threshold)
	//   4. Returns updated health status
	//
	// Note: Manual checks bypass the 3-failure threshold (immediate status update).
	//
	// Parameters:
	//   - targetName: Name of target to check
	//
	// Returns:
	//   - *TargetHealthStatus: Updated health status (never nil if no error)
	//   - error: ErrTargetNotFound if target doesn't exist
	//
	// Performance: <500ms (includes HTTP request to target)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	// Manual health check (via API)
	//	status, err := manager.CheckNow(ctx, "rootly-prod")
	//	if err != nil {
	//	    return fmt.Errorf("health check failed: %w", err)
	//	}
	//
	//	log.Info("Manual health check completed",
	//	    "target", status.TargetName,
	//	    "status", status.Status,
	//	    "latency_ms", status.LatencyMs)
	CheckNow(ctx context.Context, targetName string) (*TargetHealthStatus, error)

	// GetStats returns aggregate health statistics.
	//
	// This method:
	//   1. Retrieves all health statuses from cache
	//   2. Calculates aggregate counts (healthy/unhealthy/degraded/unknown)
	//   3. Calculates overall success rate
	//   4. Returns HealthStats
	//
	// Returns:
	//   - *HealthStats: Aggregate statistics (never nil if no error)
	//   - error: If failed to retrieve statistics
	//
	// Performance: <20ms (O(n) aggregation for n targets)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//
	//	stats, err := manager.GetStats(ctx)
	//	if err != nil {
	//	    return err
	//	}
	//
	//	log.Info("Health statistics",
	//	    "total_targets", stats.TotalTargets,
	//	    "healthy", stats.HealthyCount,
	//	    "unhealthy", stats.UnhealthyCount,
	//	    "overall_success_rate", stats.OverallSuccessRate)
	GetStats(ctx context.Context) (*HealthStats, error)
}

// TargetHealthStatus represents health status of a publishing target.
type TargetHealthStatus struct {
	// Target Info
	TargetName string `json:"target_name"`            // Target name (e.g., "rootly-prod")
	TargetType string `json:"target_type"`            // Target type (rootly/pagerduty/slack/webhook)
	Enabled    bool   `json:"enabled"`                // Is target enabled?

	// Health Status
	Status       HealthStatus `json:"status"`                 // Health status (healthy/unhealthy/degraded/unknown)
	LatencyMs    *int64       `json:"latency_ms,omitempty"`   // Response time in ms (null if error)
	ErrorMessage *string      `json:"error_message,omitempty"` // Error details (null if healthy)

	// Timestamps
	LastCheck   time.Time  `json:"last_check"`             // Last health check time
	LastSuccess *time.Time `json:"last_success,omitempty"` // Last successful check (null if never)
	LastFailure *time.Time `json:"last_failure,omitempty"` // Last failed check (null if never)

	// Statistics
	ConsecutiveFailures int     `json:"consecutive_failures"` // Current failure streak
	TotalChecks         int64   `json:"total_checks"`         // Lifetime checks
	TotalSuccesses      int64   `json:"total_successes"`      // Lifetime successes
	TotalFailures       int64   `json:"total_failures"`       // Lifetime failures
	SuccessRate         float64 `json:"success_rate"`         // (successes / total_checks) * 100
}

// HealthStatus represents health state.
type HealthStatus string

const (
	// HealthStatusUnknown indicates no checks performed yet (initial state).
	HealthStatusUnknown HealthStatus = "unknown"

	// HealthStatusHealthy indicates target is working normally (last check succeeded, latency < 5s).
	HealthStatusHealthy HealthStatus = "healthy"

	// HealthStatusUnhealthy indicates target is failing (3+ consecutive failures).
	HealthStatusUnhealthy HealthStatus = "unhealthy"

	// HealthStatusDegraded indicates target is slow (latency >= 5s).
	HealthStatusDegraded HealthStatus = "degraded"
)

// IsHealthy returns true if status is healthy.
func (s HealthStatus) IsHealthy() bool {
	return s == HealthStatusHealthy
}

// IsUnhealthy returns true if status is unhealthy.
func (s HealthStatus) IsUnhealthy() bool {
	return s == HealthStatusUnhealthy
}

// IsDegraded returns true if status is degraded.
func (s HealthStatus) IsDegraded() bool {
	return s == HealthStatusDegraded
}

// IsUnknown returns true if status is unknown.
func (s HealthStatus) IsUnknown() bool {
	return s == HealthStatusUnknown
}

// IsHealthy returns true if target health status is healthy.
func (s *TargetHealthStatus) IsHealthy() bool {
	return s.Status.IsHealthy()
}

// IsUnhealthy returns true if target health status is unhealthy.
func (s *TargetHealthStatus) IsUnhealthy() bool {
	return s.Status.IsUnhealthy()
}

// IsDegraded returns true if target health status is degraded.
func (s *TargetHealthStatus) IsDegraded() bool {
	return s.Status.IsDegraded()
}

// IsUnknown returns true if target health status is unknown.
func (s *TargetHealthStatus) IsUnknown() bool {
	return s.Status.IsUnknown()
}

// HealthCheckResult represents result of single health check.
type HealthCheckResult struct {
	// Target Info
	TargetName string `json:"target_name"`
	TargetURL  string `json:"target_url"`

	// Check Result
	Success      bool       `json:"success"`                  // Did check succeed?
	LatencyMs    *int64     `json:"latency_ms,omitempty"`     // Response time (null if error)
	StatusCode   *int       `json:"status_code,omitempty"`    // HTTP status code (null if TCP error)
	ErrorMessage *string    `json:"error_message,omitempty"`  // Error details (null if success)
	ErrorType    *ErrorType `json:"error_type,omitempty"`     // Error classification

	// Metadata
	CheckedAt time.Time `json:"checked_at"` // When check was performed
	CheckType CheckType `json:"check_type"` // periodic/manual
}

// CheckType represents health check trigger type.
type CheckType string

const (
	// CheckTypePeriodic indicates background worker check.
	CheckTypePeriodic CheckType = "periodic"

	// CheckTypeManual indicates HTTP API manual trigger.
	CheckTypeManual CheckType = "manual"
)

// HealthConfig configures health monitoring.
type HealthConfig struct {
	// Timing
	CheckInterval time.Duration // Interval between checks (default: 2m)
	HTTPTimeout   time.Duration // HTTP request timeout (default: 5s)
	WarmupDelay   time.Duration // Delay before first check (default: 10s)

	// Thresholds
	FailureThreshold  int           // Consecutive failures → unhealthy (default: 3)
	DegradedThreshold time.Duration // Latency threshold for degraded (default: 5s)

	// Parallelism
	MaxConcurrentChecks int // Max parallel health checks (default: 10)

	// HTTP Client
	MaxIdleConns    int  // HTTP client connection pool (default: 100)
	TLSSkipVerify   bool // Skip TLS verification (default: false)
	FollowRedirects bool // Follow HTTP redirects (default: true)
	MaxRedirects    int  // Max redirect hops (default: 3)
}

// DefaultHealthConfig returns default health configuration.
//
// Default values:
//   - CheckInterval: 2m (120 seconds)
//   - HTTPTimeout: 5s
//   - WarmupDelay: 10s
//   - FailureThreshold: 3 consecutive failures
//   - DegradedThreshold: 5s latency
//   - MaxConcurrentChecks: 10 goroutines
//   - MaxIdleConns: 100 connections
//   - TLSSkipVerify: false (validate certificates)
//   - FollowRedirects: true (max 3 hops)
//
// Example:
//
//	config := publishing.DefaultHealthConfig()
//	config.CheckInterval = 5 * time.Minute // Override interval
//	manager, err := publishing.NewHealthMonitor(discovery, config, logger, metrics)
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		CheckInterval:       2 * time.Minute,
		HTTPTimeout:         5 * time.Second,
		WarmupDelay:         10 * time.Second,
		FailureThreshold:    3,
		DegradedThreshold:   5 * time.Second,
		MaxConcurrentChecks: 10,
		MaxIdleConns:        100,
		TLSSkipVerify:       false,
		FollowRedirects:     true,
		MaxRedirects:        3,
	}
}

// HealthStats represents aggregate health statistics.
type HealthStats struct {
	// Counts
	TotalTargets   int `json:"total_targets"`   // Total number of targets
	HealthyCount   int `json:"healthy_count"`   // Number of healthy targets
	UnhealthyCount int `json:"unhealthy_count"` // Number of unhealthy targets
	DegradedCount  int `json:"degraded_count"`  // Number of degraded targets
	UnknownCount   int `json:"unknown_count"`   // Number of unknown targets

	// Last Check
	LastCheckTime *time.Time `json:"last_check_time,omitempty"` // Most recent check time

	// Success Rate
	OverallSuccessRate float64 `json:"overall_success_rate"` // Across all targets (0-100)
}
