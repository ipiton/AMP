package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DefaultRefreshManager is production implementation of RefreshManager.
//
// This implementation:
//   - Integrates with TargetDiscoveryManager (TN-047) for actual discovery
//   - Runs background worker for periodic refresh (5m interval)
//   - Handles manual refresh requests (via API)
//   - Implements retry logic with exponential backoff (30s → 5m)
//   - Tracks state (idle/in_progress/success/failed)
//   - Records Prometheus metrics (5 metrics)
//   - Logs structured events (slog)
//
// Thread Safety:
//   - All public methods safe for concurrent use
//   - Internal state protected by sync.RWMutex
//   - Single-flight pattern (only 1 refresh at a time)
//
// Performance:
//   - Start/Stop: <1ms (O(1))
//   - RefreshNow: <100ms (async trigger)
//   - GetStatus: <10ms (read-only)
//
// Observability:
//   - 5 Prometheus metrics (total, duration, errors, last_success, in_progress)
//   - Structured logging (DEBUG/INFO/WARN/ERROR)
//   - Request ID tracking
//
// Example:
//
//	// Create manager
//	config := DefaultRefreshConfig()
//	manager, err := NewRefreshManager(
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
//	if err := manager.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer manager.Stop(30 * time.Second)
type DefaultRefreshManager struct {
	// Dependencies
	discovery TargetDiscoveryManager // TN-047 (actual discovery logic)
	logger    *slog.Logger
	metrics   *RefreshMetrics

	// Configuration
	config RefreshConfig

	// State (protected by mu)
	state               RefreshState
	lastRefresh         time.Time
	lastError           error
	nextRefresh         time.Time
	inProgress          bool
	consecutiveFailures int
	targetStats         targetStats
	refreshDuration     time.Duration
	mu                  sync.RWMutex

	// Lifecycle (protected by lifecycleMu)
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	started     bool
	lifecycleMu sync.Mutex

	// Rate limiting (protected by rateMu)
	lastManualRefresh time.Time
	rateMu            sync.Mutex
}

// targetStats holds target discovery statistics.
type targetStats struct {
	Total   int // Total secrets discovered
	Valid   int // Valid targets in cache
	Invalid int // Invalid/skipped targets
}

// NewRefreshManager creates new refresh manager.
//
// Parameters:
//   - discovery: TargetDiscoveryManager from TN-047 (for actual discovery)
//   - config: RefreshConfig (intervals, retries, backoff)
//   - logger: Structured logger (slog)
//   - metricsReg: Prometheus metrics registry
//
// Returns:
//   - RefreshManager instance
//   - error if invalid config or nil dependencies
//
// Example:
//
//	config := DefaultRefreshConfig()
//	config.Interval = 10 * time.Minute // Custom interval
//
//	manager, err := NewRefreshManager(
//	    discoveryMgr,
//	    config,
//	    slog.Default(),
//	    prometheus.DefaultRegisterer,
//	)
//	if err != nil {
//	    log.Fatal("Failed to create refresh manager", err)
//	}
func NewRefreshManager(
	discovery TargetDiscoveryManager,
	config RefreshConfig,
	logger *slog.Logger,
	metricsReg prometheus.Registerer,
) (RefreshManager, error) {
	// Validate dependencies
	if discovery == nil {
		return nil, fmt.Errorf("discovery manager is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}
	if metricsReg == nil {
		return nil, fmt.Errorf("metrics registry is nil")
	}

	// Validate config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create metrics
	metrics := NewRefreshMetrics(metricsReg)

	// Create manager
	m := &DefaultRefreshManager{
		discovery: discovery,
		logger:    logger,
		metrics:   metrics,
		config:    config,
		state:     RefreshStateIdle,
	}

	return m, nil
}

// Start begins background refresh worker.
//
// This method:
//   1. Validates manager not already started
//   2. Creates context for lifecycle management
//   3. Spawns background goroutine (runBackgroundWorker)
//   4. Returns immediately (non-blocking)
//
// Returns:
//   - nil on success
//   - ErrAlreadyStarted if manager already running
//
// Thread-Safe: Yes (protected by lifecycleMu)
func (m *DefaultRefreshManager) Start() error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	if m.started {
		return ErrAlreadyStarted
	}

	// Create context for lifecycle
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// Spawn background worker
	m.wg.Add(1)
	go m.runBackgroundWorker()

	m.started = true

	m.logger.Info("Refresh manager started",
		"interval", m.config.Interval,
		"max_retries", m.config.MaxRetries,
		"base_backoff", m.config.BaseBackoff,
		"max_backoff", m.config.MaxBackoff,
		"warmup_period", m.config.WarmupPeriod)

	return nil
}

// Stop gracefully stops background refresh worker.
//
// This method:
//   1. Validates manager is started
//   2. Cancels context (stops new refreshes)
//   3. Waits for goroutine to exit (max timeout)
//   4. Cleans up resources
//
// Returns:
//   - nil if stopped cleanly
//   - ErrShutdownTimeout if timeout exceeded
//   - ErrNotStarted if manager not running
//
// Thread-Safe: Yes (protected by lifecycleMu)
func (m *DefaultRefreshManager) Stop(timeout time.Duration) error {
	m.lifecycleMu.Lock()
	if !m.started {
		m.lifecycleMu.Unlock()
		return ErrNotStarted
	}

	// Check if refresh in progress
	m.mu.RLock()
	inProgress := m.inProgress
	m.mu.RUnlock()

	m.lifecycleMu.Unlock()

	m.logger.Info("Refresh manager stopping",
		"in_progress", inProgress,
		"timeout", timeout)

	// Cancel context (stops new refreshes)
	m.cancel()

	// Wait for goroutine to exit (with timeout)
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("Refresh manager stopped gracefully")
		return nil
	case <-time.After(timeout):
		m.logger.Error("Refresh manager shutdown timeout exceeded",
			"timeout", timeout)
		return ErrShutdownTimeout
	}
}

// RefreshNow triggers immediate refresh (async).
//
// This method:
//   1. Validates manager is started
//   2. Checks rate limit (max 1/min)
//   3. Checks if refresh already in progress
//   4. Spawns goroutine for async execution
//   5. Returns immediately (202 Accepted behavior)
//
// Returns:
//   - nil if triggered successfully
//   - ErrNotStarted if manager not running
//   - ErrRateLimitExceeded if called too frequently
//   - ErrRefreshInProgress if refresh already running
//
// Thread-Safe: Yes (protected by rateMu and mu)
func (m *DefaultRefreshManager) RefreshNow() error {
	// Check if started
	m.lifecycleMu.Lock()
	if !m.started {
		m.lifecycleMu.Unlock()
		return ErrNotStarted
	}
	m.lifecycleMu.Unlock()

	// Check rate limit
	m.rateMu.Lock()
	if time.Since(m.lastManualRefresh) < m.config.RateLimitPer {
		m.rateMu.Unlock()
		m.logger.Warn("Manual refresh rate limit exceeded",
			"last_refresh", m.lastManualRefresh,
			"rate_limit", m.config.RateLimitPer)
		return ErrRateLimitExceeded
	}
	m.lastManualRefresh = time.Now()
	m.rateMu.Unlock()

	// Check if refresh in progress
	m.mu.RLock()
	if m.inProgress {
		m.mu.RUnlock()
		m.logger.Debug("Manual refresh skipped (refresh in progress)")
		return ErrRefreshInProgress
	}
	m.mu.RUnlock()

	m.logger.Info("Manual refresh triggered")

	// Trigger async refresh (spawn goroutine)
	go m.executeRefresh(true) // isManual=true

	return nil
}

// GetStatus returns current refresh state.
//
// Returns copy of internal state (safe to modify).
//
// Thread-Safe: Yes (RLock during read)
func (m *DefaultRefreshManager) GetStatus() RefreshStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return RefreshStatus{
		State:               m.state,
		LastRefresh:         m.lastRefresh,
		NextRefresh:         m.nextRefresh,
		RefreshDuration:     m.refreshDuration,
		TargetsDiscovered:   m.targetStats.Total,
		TargetsValid:        m.targetStats.Valid,
		TargetsInvalid:      m.targetStats.Invalid,
		Error:               errorString(m.lastError),
		ConsecutiveFailures: m.consecutiveFailures,
	}
}

// updateState updates internal state (thread-safe).
func (m *DefaultRefreshManager) updateState(
	state RefreshState,
	lastRefresh time.Time,
	lastError error,
	targetStats targetStats,
	duration time.Duration,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state = state
	m.refreshDuration = duration

	if state == RefreshStateSuccess {
		m.lastRefresh = lastRefresh
		m.lastError = nil
		m.consecutiveFailures = 0
		m.targetStats = targetStats
		m.nextRefresh = lastRefresh.Add(m.config.Interval)
	} else if state == RefreshStateFailed {
		m.lastError = lastError
		m.consecutiveFailures++
		// Next refresh scheduled at regular interval (no exponential backoff for scheduled refreshes)
		m.nextRefresh = time.Now().Add(m.config.Interval)
	}
}
