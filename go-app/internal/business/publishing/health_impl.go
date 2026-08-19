package publishing

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// DefaultHealthMonitor is production implementation of HealthMonitor.
//
// This implementation provides:
//   - Background worker for periodic health checks (2m interval)
//   - HTTP connectivity tests (TCP + HTTP GET)
//   - Thread-safe status cache (O(1) lookups)
//   - Prometheus metrics recording (6 metrics)
//   - Graceful lifecycle management (Start/Stop)
//   - Context-aware cancellation
//
// Architecture:
//   - Single background goroutine for periodic checks
//   - Goroutine pool (10 workers) for parallel target checks
//   - RWMutex-based status cache for thread safety
//   - HTTP client with connection pooling
//
// Example Usage:
//
//	config := publishing.DefaultHealthConfig()
//	monitor, err := publishing.NewHealthMonitor(
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
//	if err := monitor.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer monitor.Stop(10 * time.Second)
//
//	// Get health status
//	health, err := monitor.GetHealth(context.Background())
type DefaultHealthMonitor struct {
	// Dependencies
	discoveryMgr TargetDiscoveryManager // Get targets
	httpClient   *http.Client           // HTTP connectivity tests
	config       HealthConfig           // Configuration

	// State
	statusCache *healthStatusCache // Thread-safe cache
	running     atomic.Bool        // Is worker running?
	cancel      context.CancelFunc // Cancel worker
	wg          sync.WaitGroup     // Track worker goroutine

	// Observability
	logger  *slog.Logger
	metrics *v2.PublishingMetrics
}

// NewHealthMonitor creates DefaultHealthMonitor.
//
// This function:
//  1. Validates dependencies (discoveryMgr not nil)
//  2. Creates HTTP client with timeout & connection pooling
//  3. Initializes status cache
//  4. Creates Prometheus metrics
//  5. Returns DefaultHealthMonitor instance
//
// Parameters:
//   - discoveryMgr: Target discovery manager (required)
//   - config: Health configuration (use DefaultHealthConfig() for defaults)
//   - logger: Structured logger (nil = slog.Default())
//   - metricsRegistry: Prometheus metrics registry (required)
//
// Returns:
//   - *DefaultHealthMonitor: Health monitor instance
//   - error: ErrNilDiscoveryManager if discoveryMgr is nil
//
// Example:
//
//	config := publishing.DefaultHealthConfig()
//	config.CheckInterval = 5 * time.Minute // Override interval
//
//	monitor, err := publishing.NewHealthMonitor(
//	    discoveryMgr,
//	    config,
//	    slog.Default(),
//	    metricsRegistry,
//	)
//	if err != nil {
//	    return fmt.Errorf("failed to create health monitor: %w", err)
//	}
func NewHealthMonitor(
	discoveryMgr TargetDiscoveryManager,
	config HealthConfig,
	logger *slog.Logger,
	metrics *v2.PublishingMetrics,
) (*DefaultHealthMonitor, error) {
	// Validation
	if discoveryMgr == nil {
		return nil, ErrNilDiscoveryManager
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Create HTTP client with timeout & connection pooling
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: config.TLSSkipVerify, // #nosec G402
		},
	}

	// Configure redirect policy
	var checkRedirect func(req *http.Request, via []*http.Request) error
	if config.FollowRedirects {
		checkRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= config.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", config.MaxRedirects)
			}
			return nil
		}
	} else {
		checkRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	httpClient := &http.Client{
		Timeout:       config.HTTPTimeout,
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}

	return &DefaultHealthMonitor{
		discoveryMgr: discoveryMgr,
		httpClient:   httpClient,
		config:       config,
		statusCache:  newHealthStatusCache(),
		logger:       logger,
		metrics:      metrics,
	}, nil
}

// Start begins background health check worker.
func (m *DefaultHealthMonitor) Start() error {
	// Atomically check-and-set the single-flight start gate. This MUST be a
	// single CompareAndSwap, not a separate Load()-then-Store(true): two
	// concurrent Start() calls could otherwise both observe running==false
	// (the Load), and both proceed to spawn a worker and write m.cancel —
	// the second write races the first (caught by -race) and leaks the
	// first worker goroutine since only the last-written m.cancel is ever
	// reachable from Stop(). CompareAndSwap makes "was it false, and is it
	// now true" one atomic operation, so exactly one caller among any
	// number of concurrent Start()s wins the race.
	if !m.running.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// Start background worker
	m.wg.Add(1)
	go m.runHealthCheckWorker(ctx)

	m.logger.Info("Health check worker started",
		"check_interval", m.config.CheckInterval,
		"warmup_delay", m.config.WarmupDelay,
		"failure_threshold", m.config.FailureThreshold)

	return nil
}

// Stop gracefully stops background health check worker.
func (m *DefaultHealthMonitor) Stop(timeout time.Duration) error {
	// Atomically check-and-clear the single-flight running gate — the same
	// CompareAndSwap pattern used in Start(), replacing the previous
	// Load()-then-eventual-Store(false) pair. Without this, concurrent
	// Stop() calls could all observe running==true (the Load) and all
	// proceed to call m.cancel(), each spawn their own wg.Wait() goroutine,
	// and race the final Store(false) — the same TOCTOU class fixed in
	// Start(). CompareAndSwap makes "was it true, and is it now false" one
	// atomic operation, so exactly one caller among any number of
	// concurrent Stop()s runs the stop sequence below; the rest get
	// ErrNotStarted immediately, mirroring Start()'s ErrAlreadyStarted.
	//
	// This does NOT close the narrow timeout-path reentrancy gap disclosed
	// separately (out of scope here, same as for item 3's Start() fix): if
	// this call's stop sequence times out while the old worker is still
	// draining, running is already false (flipped by the CompareAndSwap
	// below, before the wait completes), so a subsequent Start() can still
	// win and spawn a second worker while the first winds down. That needs
	// state distinct from "is a Stop() currently in flight" and is not
	// addressed by this gate.
	if !m.running.CompareAndSwap(true, false) {
		return ErrNotStarted
	}

	m.logger.Info("Stopping health check worker", "timeout", timeout)

	// Cancel context (stops new checks)
	m.cancel()

	// Wait for worker to stop (with timeout)
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("Health check worker stopped gracefully")
		return nil
	case <-time.After(timeout):
		m.logger.Error("Health check worker stop timeout exceeded")
		return ErrShutdownTimeout
	}
}

// GetHealth returns current health status for all targets.
func (m *DefaultHealthMonitor) GetHealth(ctx context.Context) ([]TargetHealthStatus, error) {
	// Get all targets from discovery manager
	targets := m.discoveryMgr.ListTargets()

	// Retrieve health status from cache
	statuses := make([]TargetHealthStatus, 0, len(targets))
	for _, target := range targets {
		// Try to get from cache
		if status, ok := m.statusCache.Get(target.Name); ok {
			statuses = append(statuses, *status)
		} else {
			// Initialize new status if not in cache
			status := initializeHealthStatus(target.Name, target.Type, target.Enabled)
			statuses = append(statuses, *status)
		}
	}

	return statuses, nil
}

// GetHealthByName returns health status for single target.
func (m *DefaultHealthMonitor) GetHealthByName(ctx context.Context, targetName string) (*TargetHealthStatus, error) {
	// Validate target exists
	target, err := m.discoveryMgr.GetTarget(targetName)
	if err != nil {
		return nil, err // ErrTargetNotFound from discovery manager
	}

	// Try to get from cache
	if status, ok := m.statusCache.Get(targetName); ok {
		return status, nil
	}

	// Target exists but no health check yet
	// Return default status (unknown)
	status := initializeHealthStatus(targetName, target.Type, target.Enabled)
	return status, nil
}

// CheckNow triggers immediate health check for target.
func (m *DefaultHealthMonitor) CheckNow(ctx context.Context, targetName string) (*TargetHealthStatus, error) {
	// Get target from discovery manager
	target, err := m.discoveryMgr.GetTarget(targetName)
	if err != nil {
		return nil, err // ErrTargetNotFound from discovery manager
	}

	m.logger.Info("Manual health check triggered", "target_name", targetName)

	// Perform immediate health check (with retry)
	result := checkTargetWithRetry(ctx, target, CheckTypeManual, m.httpClient, m.config)

	// Process result
	processHealthCheckResult(m.statusCache, m.metrics, m.logger, m.config, result)

	// Retrieve updated status
	status, ok := m.statusCache.Get(targetName)
	if !ok {
		return nil, fmt.Errorf("failed to retrieve health status after check")
	}

	return status, nil
}

// GetStats returns aggregate health statistics.
func (m *DefaultHealthMonitor) GetStats(ctx context.Context) (*HealthStats, error) {
	// Filter by current discovery state (same as GetHealth) to exclude orphaned cache entries
	targets := m.discoveryMgr.ListTargets()
	statuses := make([]TargetHealthStatus, 0, len(targets))
	for _, target := range targets {
		if status, ok := m.statusCache.Get(target.Name); ok {
			statuses = append(statuses, *status)
		} else {
			status := initializeHealthStatus(target.Name, target.Type, target.Enabled)
			statuses = append(statuses, *status)
		}
	}

	stats := calculateAggregateStats(statuses)
	return stats, nil
}

// runHealthCheckWorker is background goroutine for periodic checks.
//
// This worker:
//  1. Waits warmup period (10s)
//  2. Performs initial health check
//  3. Enters periodic loop (ticker 2m)
//  4. Checks all enabled targets in parallel
//  5. Continues until context cancelled
func (m *DefaultHealthMonitor) runHealthCheckWorker(ctx context.Context) {
	defer m.wg.Done()

	m.logger.Debug("Health check worker starting",
		"check_interval", m.config.CheckInterval,
		"warmup_delay", m.config.WarmupDelay)

	// Wait warmup period before first check
	select {
	case <-time.After(m.config.WarmupDelay):
		// Continue to first check
	case <-ctx.Done():
		m.logger.Info("Health check worker cancelled during warmup")
		return
	}

	// Perform initial check immediately
	if err := m.checkAllTargets(ctx, CheckTypePeriodic); err != nil {
		m.logger.Error("Initial health check failed", "error", err)
	}

	// Create ticker for periodic checks
	ticker := time.NewTicker(m.config.CheckInterval)
	defer ticker.Stop()

	// Periodic check loop
	for {
		select {
		case <-ticker.C:
			// Perform health check
			if err := m.checkAllTargets(ctx, CheckTypePeriodic); err != nil {
				m.logger.Error("Periodic health check failed", "error", err)
			}

		case <-ctx.Done():
			m.logger.Info("Health check worker stopped")
			return
		}
	}
}
