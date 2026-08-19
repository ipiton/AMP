package publishing

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
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

	// workerDone holds a pointer to the current/most-recently-started
	// worker's completion channel, closed by the worker goroutine itself
	// right before it returns. Start() blocks on the PREVIOUS generation's
	// channel before spawning a new worker — this closes the timeout-path
	// reentrancy gap disclosed in the fu3-rel review (Minor #1): Stop()
	// flips `running` back to false via CompareAndSwap BEFORE its wait
	// completes, so if that wait then times out (ErrShutdownTimeout) while
	// the old worker is still draining, a subsequent Start() could otherwise
	// see running==false and spawn a second worker while the first is still
	// winding down, racing shared state such as m.cancel. Waiting on
	// workerDone guarantees at most one live worker goroutine at a time
	// regardless of how Stop() returned.
	//
	// atomic.Pointer (rather than a plain field) keeps Start()'s read of the
	// previous generation's channel and Stop()'s read of the current one
	// race-detector-clean without needing an extra mutex: only Start() ever
	// writes it, exactly once per successful call, strictly after winning the
	// running CompareAndSwap gate.
	workerDone atomic.Pointer[chan struct{}]

	// testAfterCancelObserved, if set, is invoked by the worker goroutine
	// immediately after it observes ctx.Done(), before it returns. Used only
	// by TestHealthMonitor_RestartAfterStopTimeoutWaitsForDrain to create a
	// deterministic "still draining" window without depending on real HTTP
	// timing; nil (no-op) in production and every other test.
	testAfterCancelObserved func()

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

	monitor := &DefaultHealthMonitor{
		discoveryMgr: discoveryMgr,
		httpClient:   httpClient,
		config:       config,
		statusCache:  newHealthStatusCache(),
		logger:       logger,
		metrics:      metrics,
	}

	// Pre-closed so the FIRST Start() call's drain-wait (see Start) is a
	// non-blocking receive from an already-closed channel — there is no
	// previous worker to wait for.
	noPriorWorker := make(chan struct{})
	close(noPriorWorker)
	monitor.workerDone.Store(&noPriorWorker)

	return monitor, nil
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

	// Block until the PREVIOUS worker generation has actually exited (fu3-rel
	// review, Minor #1: Stop()'s timeout-path reentrancy gap). Without this,
	// a Stop() that returned ErrShutdownTimeout has already flipped `running`
	// to false before its wait completed, so this CompareAndSwap could
	// succeed while the old worker is still draining — spawning a second
	// worker that runs concurrently with the first, racing shared state such
	// as m.cancel below. workerDone is only ever written by Start() (never
	// by Stop()), and only after winning the gate above, so this load can't
	// race a concurrent writer.
	if prev := m.workerDone.Load(); prev != nil {
		<-*prev
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// New completion signal for THIS worker generation — closed by the
	// worker itself right before it returns (see runHealthCheckWorker).
	done := make(chan struct{})
	m.workerDone.Store(&done)

	// Start background worker
	go m.runHealthCheckWorker(ctx, done)

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
	// proceed to call m.cancel(), each spawn their own wait goroutine,
	// and race the final Store(false) — the same TOCTOU class fixed in
	// Start(). CompareAndSwap makes "was it true, and is it now false" one
	// atomic operation, so exactly one caller among any number of
	// concurrent Stop()s runs the stop sequence below; the rest get
	// ErrNotStarted immediately, mirroring Start()'s ErrAlreadyStarted.
	//
	// This flips `running` to false BEFORE the wait below completes, so a
	// concurrent/subsequent Start() could in principle win its own
	// CompareAndSwap while this call is still waiting on (or has already
	// timed out on) the worker below — that used to be exactly the
	// reentrancy gap this fix closes; see Start()'s workerDone wait for why
	// it's now safe regardless.
	if !m.running.CompareAndSwap(true, false) {
		return ErrNotStarted
	}

	m.logger.Info("Stopping health check worker", "timeout", timeout)

	// Cancel context (stops new checks)
	m.cancel()

	// Wait for the current worker generation to signal it has exited (with
	// timeout). workerDone is guaranteed non-nil here: running was true,
	// which only Start() sets, and Start() always stores a channel before
	// returning.
	donePtr := m.workerDone.Load()

	select {
	case <-*donePtr:
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
func (m *DefaultHealthMonitor) runHealthCheckWorker(ctx context.Context, done chan struct{}) {
	// Signals this worker generation has fully exited — see workerDone's
	// doc comment on DefaultHealthMonitor and Start()'s wait on it.
	defer close(done)

	m.logger.Debug("Health check worker starting",
		"check_interval", m.config.CheckInterval,
		"warmup_delay", m.config.WarmupDelay)

	// Wait warmup period before first check
	select {
	case <-time.After(m.config.WarmupDelay):
		// Continue to first check
	case <-ctx.Done():
		m.logger.Info("Health check worker cancelled during warmup")
		m.observeCancelForTest()
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
			m.observeCancelForTest()
			return
		}
	}
}

// observeCancelForTest invokes testAfterCancelObserved when a test has set
// one. Always a no-op in production. See DefaultHealthMonitor.workerDone and
// testAfterCancelObserved's doc comments.
func (m *DefaultHealthMonitor) observeCancelForTest() {
	if m.testAfterCancelObserved != nil {
		m.testAfterCancelObserved()
	}
}
