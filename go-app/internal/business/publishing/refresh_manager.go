// Package publishing provides target refresh mechanism for dynamic target updates.
//
// This package implements automatic and manual refresh of publishing targets
// discovered from Kubernetes Secrets (TN-047).
//
// Key Components:
//   - RefreshManager: Interface for refresh lifecycle management
//   - Background Worker: Periodic refresh with configurable interval (5m)
//   - Manual Trigger: HTTP API endpoint for immediate refresh
//   - Retry Logic: Exponential backoff for transient failures
//   - Observability: 5 Prometheus metrics, structured logging
//
// Example Usage:
//
//	// Create refresh manager
//	config := publishing.DefaultRefreshConfig()
//	refreshMgr, err := publishing.NewRefreshManager(
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
//	if err := refreshMgr.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer refreshMgr.Stop(30 * time.Second)
//
//	// Manual refresh (via API)
//	if err := refreshMgr.RefreshNow(); err != nil {
//	    if errors.Is(err, ErrRefreshInProgress) {
//	        log.Warn("Refresh already running")
//	    }
//	}
//
//	// Check status
//	status := refreshMgr.GetStatus()
//	log.Info("Refresh status",
//	    "state", status.State,
//	    "last_refresh", status.LastRefresh,
//	    "targets", status.TargetsDiscovered)
//
// See TN-048 for detailed design documentation.
package publishing

import (
	"time"
)

// RefreshManager manages target refresh lifecycle.
//
// This interface provides methods for:
//   - Starting/stopping background refresh worker (periodic refresh)
//   - Triggering immediate refresh (manual via API)
//   - Getting current refresh status (for monitoring/debugging)
//
// Thread Safety:
//   - All methods are safe for concurrent use
//   - Multiple goroutines can call methods simultaneously
//
// Performance:
//   - Start/Stop: <1ms (O(1), non-blocking)
//   - RefreshNow: <100ms (async trigger, immediate return)
//   - GetStatus: <10ms (read-only, O(1))
//
// Error Handling:
//   - Graceful degradation on K8s API failures
//   - Automatic retry with exponential backoff
//   - Stale cache retained until successful refresh
//
// Example:
//
//	manager, _ := NewRefreshManager(discovery, config, logger, metrics)
//
//	// Start background worker
//	if err := manager.Start(); err != nil {
//	    log.Fatal("Failed to start refresh manager", err)
//	}
//
//	// Graceful shutdown
//	defer manager.Stop(30 * time.Second)
//
//	// Manual refresh
//	if err := manager.RefreshNow(); err != nil {
//	    log.Error("Manual refresh failed", err)
//	}
//
//	// Check status
//	status := manager.GetStatus()
//	log.Info("Refresh status", "state", status.State)
type RefreshManager interface {
	// Start begins background refresh worker.
	//
	// This method:
	//   1. Validates manager not already started
	//   2. Creates background goroutine for periodic refresh
	//   3. Schedules first refresh (after 30s warmup period)
	//   4. Returns immediately (non-blocking)
	//
	// Returns:
	//   - nil on success
	//   - ErrAlreadyStarted if manager already running
	//
	// Performance: <1ms (O(1), spawns goroutine)
	//
	// Thread-Safe: Yes
	//
	// Example:
	//   if err := manager.Start(); err != nil {
	//       log.Fatal("Failed to start refresh manager", err)
	//   }
	Start() error

	// Stop gracefully stops background refresh worker.
	//
	// This method:
	//   1. Cancels context (stops new refreshes)
	//   2. Waits for current refresh to complete (max timeout)
	//   3. Cleans up goroutine resources
	//   4. Returns when fully stopped or timeout exceeded
	//
	// Parameters:
	//   - timeout: Max time to wait for graceful shutdown (e.g., 30s)
	//     If timeout exceeded, forces shutdown (may leak goroutine)
	//
	// Returns:
	//   - nil if stopped cleanly within timeout
	//   - ErrShutdownTimeout if timeout exceeded
	//   - ErrNotStarted if manager not running
	//
	// Performance:
	//   - Normal case: <5s (waits for current refresh)
	//   - Timeout case: ~timeout duration
	//
	// Thread-Safe: Yes
	//
	// Example:
	//   // Graceful shutdown with 30s timeout
	//   if err := manager.Stop(30 * time.Second); err != nil {
	//       if errors.Is(err, ErrShutdownTimeout) {
	//           log.Error("Force shutdown after timeout", err)
	//       }
	//   }
	Stop(timeout time.Duration) error

	// RefreshNow triggers immediate refresh (async).
	//
	// This method:
	//   1. Validates rate limit (max 1 refresh per minute)
	//   2. Checks if refresh already in progress
	//   3. Spawns goroutine for async execution
	//   4. Returns immediately (202 Accepted behavior)
	//
	// Returns:
	//   - nil if refresh triggered successfully
	//   - ErrRefreshInProgress if refresh already running
	//   - ErrRateLimitExceeded if called too frequently (<1m since last)
	//   - ErrNotStarted if manager not running
	//
	// Performance: <100ms (async trigger, immediate return)
	//
	// Thread-Safe: Yes (protected by mutex)
	//
	// Rate Limiting:
	//   - Max 1 refresh per minute (configurable)
	//   - Prevents DoS attacks on K8s API
	//
	// Example:
	//   // Manual refresh (typically via API)
	//   if err := manager.RefreshNow(); err != nil {
	//       switch {
	//       case errors.Is(err, ErrRefreshInProgress):
	//           // Return 503 Service Unavailable
	//           http.Error(w, "Refresh in progress", 503)
	//       case errors.Is(err, ErrRateLimitExceeded):
	//           // Return 429 Too Many Requests
	//           http.Error(w, "Rate limit exceeded", 429)
	//       default:
	//           http.Error(w, "Internal error", 500)
	//       }
	//   }
	RefreshNow() error

	// GetStatus returns current refresh state.
	//
	// Returns:
	//   - RefreshStatus with current state (copy, safe to modify)
	//   - Never returns error (always succeeds)
	//
	// Performance: <10ms (read-only, O(1))
	//
	// Thread-Safe: Yes (RLock during read)
	//
	// Example:
	//   status := manager.GetStatus()
	//   log.Info("Refresh status",
	//       "state", status.State,
	//       "last_refresh", status.LastRefresh,
	//       "next_refresh", status.NextRefresh,
	//       "targets_discovered", status.TargetsDiscovered,
	//       "targets_valid", status.TargetsValid,
	//       "consecutive_failures", status.ConsecutiveFailures)
	//
	//   // Check if cache is stale
	//   if time.Since(status.LastRefresh) > 10*time.Minute {
	//       log.Warn("Refresh cache is stale", "age", time.Since(status.LastRefresh))
	//   }
	GetStatus() RefreshStatus
}

// RefreshStatus represents current refresh state.
//
// This struct provides comprehensive status information for monitoring and debugging:
//   - Current state (idle/in_progress/success/failed)
//   - Timestamps (last refresh, next scheduled)
//   - Performance metrics (refresh duration)
//   - Discovery statistics (targets discovered/valid/invalid)
//   - Error information (last error, consecutive failures)
//
// Thread Safety:
//   - Returned by GetStatus() as a copy (safe to modify)
//   - Internal state protected by RWMutex
//
// Example:
//
//	status := manager.GetStatus()
//
//	// Check current state
//	switch status.State {
//	case RefreshStateSuccess:
//	    log.Info("Targets refreshed successfully")
//	case RefreshStateFailed:
//	    log.Error("Refresh failed", "error", status.Error)
//	case RefreshStateInProgress:
//	    log.Info("Refresh in progress")
//	}
//
//	// Check cache freshness
//	if time.Since(status.LastRefresh) > 10*time.Minute {
//	    log.Warn("Cache stale", "age", time.Since(status.LastRefresh))
//	}
//
//	// Check error rate
//	if status.ConsecutiveFailures >= 3 {
//	    log.Error("Multiple consecutive failures", "count", status.ConsecutiveFailures)
//	    // Trigger alert
//	}
type RefreshStatus struct {
	// State is current refresh state.
	// Values: idle, in_progress, success, failed
	State RefreshState

	// LastRefresh is timestamp of last successful refresh.
	// Zero value if no successful refresh yet.
	LastRefresh time.Time

	// NextRefresh is timestamp of next scheduled refresh.
	// Zero value if no refresh scheduled.
	NextRefresh time.Time

	// RefreshDuration is duration of last refresh (success or failed).
	// Zero value if no refresh completed yet.
	RefreshDuration time.Duration

	// TargetsDiscovered is total secrets discovered from K8s API.
	// Includes both valid and invalid targets.
	TargetsDiscovered int

	// TargetsValid is number of valid targets in cache.
	// These are targets that passed parsing + validation.
	TargetsValid int

	// TargetsInvalid is number of invalid/skipped targets.
	// Reasons: parse errors (bad base64/JSON), validation failures.
	TargetsInvalid int

	// Error is last error message (empty string if success).
	// Contains human-readable error description.
	Error string

	// ConsecutiveFailures is count of consecutive failures.
	// Reset to 0 on successful refresh.
	// Alert if >= 3 (persistent issue).
	ConsecutiveFailures int
}

// RefreshState represents refresh state.
type RefreshState string

const (
	// RefreshStateIdle means no refresh running, no scheduled.
	// Initial state after creation (before Start()).
	RefreshStateIdle RefreshState = "idle"

	// RefreshStateInProgress means refresh currently running.
	// Transition: idle/success/failed → in_progress
	RefreshStateInProgress RefreshState = "in_progress"

	// RefreshStateSuccess means last refresh succeeded.
	// Transition: in_progress → success
	RefreshStateSuccess RefreshState = "success"

	// RefreshStateFailed means last refresh failed.
	// Transition: in_progress → failed
	RefreshStateFailed RefreshState = "failed"
)

// String returns string representation of RefreshState.
func (s RefreshState) String() string {
	return string(s)
}

// RefreshConfig configures refresh behavior.
//
// This struct provides configuration for:
//   - Refresh interval (periodic refresh frequency)
//   - Retry behavior (max retries, backoff schedule)
//   - Rate limiting (prevent excessive API calls)
//   - Timeouts (refresh operation timeout)
//   - Warmup period (delay before first refresh)
//
// All durations are configurable via environment variables.
//
// Example:
//
//	// Use defaults
//	config := DefaultRefreshConfig()
//
//	// Or customize
//	config := RefreshConfig{
//	    Interval:       10 * time.Minute,  // More frequent
//	    MaxRetries:     10,                 // More retries
//	    BaseBackoff:    1 * time.Minute,    // Longer initial backoff
//	    MaxBackoff:     10 * time.Minute,   // Higher backoff cap
//	    RateLimitPer:   2 * time.Minute,    // More relaxed rate limit
//	    RefreshTimeout: 60 * time.Second,   // Longer timeout
//	    WarmupPeriod:   10 * time.Second,   // Shorter warmup
//	}
type RefreshConfig struct {
	// Interval is periodic refresh interval.
	// Default: 5m
	// Environment: TARGET_REFRESH_INTERVAL
	Interval time.Duration

	// MaxRetries is max retry attempts on transient errors.
	// Default: 5
	// Environment: TARGET_REFRESH_MAX_RETRIES
	MaxRetries int

	// BaseBackoff is initial backoff duration for retry.
	// Default: 30s
	// Environment: TARGET_REFRESH_BASE_BACKOFF
	BaseBackoff time.Duration

	// MaxBackoff is maximum backoff duration (cap for exponential backoff).
	// Default: 5m
	// Environment: TARGET_REFRESH_MAX_BACKOFF
	MaxBackoff time.Duration

	// RateLimitPer is minimum time between manual refreshes.
	// Default: 1m (max 1 refresh per minute)
	// Environment: TARGET_REFRESH_RATE_LIMIT
	RateLimitPer time.Duration

	// RefreshTimeout is max time for single refresh operation.
	// Default: 30s
	// Environment: TARGET_REFRESH_TIMEOUT
	RefreshTimeout time.Duration

	// WarmupPeriod is delay before first refresh after Start().
	// Default: 30s (allows K8s to stabilize)
	// Environment: TARGET_REFRESH_WARMUP
	WarmupPeriod time.Duration
}

// DefaultRefreshConfig returns sensible defaults.
//
// These defaults are production-tested and work well for most deployments:
//   - Refresh every 5m (balance freshness vs K8s API load)
//   - 5 retry attempts with exponential backoff (30s → 5m)
//   - Rate limit 1/min (prevent DoS)
//   - 30s timeout (fail fast on K8s API issues)
//   - 30s warmup (let service stabilize after startup)
//
// Override defaults via environment variables:
//   - TARGET_REFRESH_INTERVAL=10m
//   - TARGET_REFRESH_MAX_RETRIES=10
//   - TARGET_REFRESH_BASE_BACKOFF=1m
//   - TARGET_REFRESH_MAX_BACKOFF=10m
//   - TARGET_REFRESH_RATE_LIMIT=2m
//   - TARGET_REFRESH_TIMEOUT=60s
//   - TARGET_REFRESH_WARMUP=10s
//
// Example:
//
//	config := DefaultRefreshConfig()
//	log.Info("Refresh config",
//	    "interval", config.Interval,
//	    "max_retries", config.MaxRetries,
//	    "base_backoff", config.BaseBackoff)
func DefaultRefreshConfig() RefreshConfig {
	return RefreshConfig{
		Interval:       5 * time.Minute,
		MaxRetries:     5,
		BaseBackoff:    30 * time.Second,
		MaxBackoff:     5 * time.Minute,
		RateLimitPer:   1 * time.Minute,
		RefreshTimeout: 30 * time.Second,
		WarmupPeriod:   30 * time.Second,
	}
}

// Validate validates refresh configuration.
//
// Returns error if:
//   - Interval <= 0 (must be positive)
//   - MaxRetries < 0 (must be non-negative)
//   - BaseBackoff <= 0 (must be positive)
//   - MaxBackoff < BaseBackoff (invalid backoff range)
//   - RateLimitPer <= 0 (must be positive)
//   - RefreshTimeout <= 0 (must be positive)
//   - WarmupPeriod < 0 (can be zero but not negative)
//
// Example:
//
//	config := RefreshConfig{
//	    Interval: 10 * time.Minute,
//	    MaxRetries: 5,
//	    // ... other fields
//	}
//	if err := config.Validate(); err != nil {
//	    log.Fatal("Invalid config", err)
//	}
func (c RefreshConfig) Validate() error {
	if c.Interval <= 0 {
		return &ConfigError{Field: "Interval", Value: c.Interval, Reason: "must be positive"}
	}
	if c.MaxRetries < 0 {
		return &ConfigError{Field: "MaxRetries", Value: c.MaxRetries, Reason: "must be non-negative"}
	}
	if c.BaseBackoff <= 0 {
		return &ConfigError{Field: "BaseBackoff", Value: c.BaseBackoff, Reason: "must be positive"}
	}
	if c.MaxBackoff < c.BaseBackoff {
		return &ConfigError{Field: "MaxBackoff", Value: c.MaxBackoff, Reason: "must be >= BaseBackoff"}
	}
	if c.RateLimitPer <= 0 {
		return &ConfigError{Field: "RateLimitPer", Value: c.RateLimitPer, Reason: "must be positive"}
	}
	if c.RefreshTimeout <= 0 {
		return &ConfigError{Field: "RefreshTimeout", Value: c.RefreshTimeout, Reason: "must be positive"}
	}
	if c.WarmupPeriod < 0 {
		return &ConfigError{Field: "WarmupPeriod", Value: c.WarmupPeriod, Reason: "must be non-negative"}
	}
	return nil
}
