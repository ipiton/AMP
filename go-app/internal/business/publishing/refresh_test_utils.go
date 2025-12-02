// Package publishing provides test utilities for refresh manager testing.
//
// This file contains:
//   - Mock implementations (MockTargetDiscoveryManager)
//   - Test helpers (assertRefreshStatus, waitForRefresh, etc.)
//   - Test factories (createTestConfig, createTestManager)
//
// Usage:
//
//	mock := &MockTargetDiscoveryManager{
//	    shouldFail: false,
//	    targetCount: 5,
//	}
//
//	config := createTestConfig(100 * time.Millisecond)  // Fast refresh for testing
//	manager, err := NewRefreshManager(mock, config, logger, reg)
//
//	waitForRefresh(t, manager, 5 * time.Second)
//	assertRefreshStatus(t, manager, RefreshStateSuccess, 5, 0)
package publishing

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/ipiton/AMP/internal/core"
)

// MockTargetDiscoveryManager is a mock implementation of TargetDiscoveryManager for testing.
//
// This mock provides full control over discovery behavior:
//   - Success/failure simulation
//   - Timeout simulation
//   - Context cancellation handling
//   - Target count configuration
//   - Call count tracking
//
// Thread-Safe: Yes (mutex-protected)
//
// Example:
//
//	mock := &MockTargetDiscoveryManager{
//	    shouldFail:   false,
//	    failureCount: 2,  // Fail first 2 attempts
//	    targetCount:  10,
//	    delayDuration: 100 * time.Millisecond,
//	}
//
//	err := mock.DiscoverTargets(ctx)  // Will fail first 2 times, then succeed
type MockTargetDiscoveryManager struct {
	// Configuration
	shouldFail     bool          // If true, DiscoverTargets returns error
	failureCount   int           // Number of failures before success (0 = always fail)
	targetCount    int           // Number of targets to report
	delayDuration  time.Duration // Artificial delay in DiscoverTargets
	ctxCancelCheck bool          // If true, check ctx.Done() before returning

	// State (protected by mu)
	discoverCalled int                    // Number of DiscoverTargets calls
	lastCtx        context.Context        // Last context passed to DiscoverTargets
	lastError      error                  // Last error returned
	mu             sync.Mutex

	// Errors to return (for error classification testing)
	errorToReturn error
}

// DiscoverTargets simulates target discovery.
//
// Behavior:
//   - If shouldFail=true and failureCount=0: Always fails
//   - If shouldFail=true and failureCount>0: Fails first N times, then succeeds
//   - If delayDuration>0: Sleeps before returning
//   - If ctxCancelCheck=true: Returns ctx.Err() if context cancelled
//   - If errorToReturn!=nil: Returns specific error (for error classification testing)
//
// Thread-Safe: Yes
func (m *MockTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.discoverCalled++
	m.lastCtx = ctx

	// Simulate delay (useful for timeout testing)
	if m.delayDuration > 0 {
		select {
		case <-time.After(m.delayDuration):
		case <-ctx.Done():
			if m.ctxCancelCheck {
				m.lastError = ctx.Err()
				return ctx.Err()
			}
		}
	}

	// Check context cancellation (if enabled)
	if m.ctxCancelCheck {
		select {
		case <-ctx.Done():
			m.lastError = ctx.Err()
			return ctx.Err()
		default:
		}
	}

	// Return specific error (for error classification testing)
	if m.errorToReturn != nil {
		m.lastError = m.errorToReturn
		return m.errorToReturn
	}

	// Simulate failures (for retry logic testing)
	if m.shouldFail {
		if m.failureCount == 0 {
			// Always fail
			m.lastError = errors.New("mock discovery failed")
			return m.lastError
		}
		if m.discoverCalled <= m.failureCount {
			// Fail first N times
			m.lastError = errors.New("mock discovery failed (transient)")
			return m.lastError
		}
		// After N failures, succeed
	}

	// Success
	m.lastError = nil
	return nil
}

// GetTarget returns target by name (mock implementation).
func (m *MockTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mock: always return error (not used in refresh tests)
	return nil, &ErrTargetNotFound{TargetName: name}
}

// ListTargets returns all targets (mock implementation).
func (m *MockTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mock: return empty slice (not used in refresh tests)
	return []*core.PublishingTarget{}
}

// GetTargetsByType returns targets by type (mock implementation).
func (m *MockTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mock: return empty slice (not used in refresh tests)
	return []*core.PublishingTarget{}
}

// GetStats returns discovery statistics.
func (m *MockTargetDiscoveryManager) GetStats() DiscoveryStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	return DiscoveryStats{
		TotalTargets:   m.targetCount,
		ValidTargets:   m.targetCount,
		InvalidTargets: 0,
		// LastDiscovery and DiscoveryErrors can be zero for mock
	}
}

// Health checks manager health (mock implementation).
func (m *MockTargetDiscoveryManager) Health(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mock: always healthy
	return nil
}

// GetDiscoverCallCount returns number of DiscoverTargets calls.
func (m *MockTargetDiscoveryManager) GetDiscoverCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.discoverCalled
}

// GetLastError returns last error returned by DiscoverTargets.
func (m *MockTargetDiscoveryManager) GetLastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// Reset resets mock state (call count, last error).
func (m *MockTargetDiscoveryManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discoverCalled = 0
	m.lastError = nil
	m.lastCtx = nil
}

// Ensure MockTargetDiscoveryManager implements TargetDiscoveryManager interface
var _ TargetDiscoveryManager = (*MockTargetDiscoveryManager)(nil)

// MockPrometheusRegisterer is a mock implementation of prometheus.Registerer.
//
// This mock tracks registered metrics without actually registering them
// with Prometheus (useful for testing).
//
// Thread-Safe: Yes (mutex-protected)
//
// Example:
//
//	mockReg := &MockPrometheusRegisterer{}
//	metrics := NewRefreshMetrics(mockReg)
//
//	// Verify metrics registered
//	if mockReg.RegisteredCount() != 5 {
//	    t.Errorf("Expected 5 metrics registered, got %d", mockReg.RegisteredCount())
//	}
type MockPrometheusRegisterer struct {
	registered []prometheus.Collector
	mu         sync.Mutex
}

// Register registers collector.
func (m *MockPrometheusRegisterer) Register(c prometheus.Collector) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered = append(m.registered, c)
	return nil
}

// MustRegister registers collector (panics on error).
func (m *MockPrometheusRegisterer) MustRegister(cs ...prometheus.Collector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered = append(m.registered, cs...)
}

// Unregister unregisters collector.
func (m *MockPrometheusRegisterer) Unregister(c prometheus.Collector) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, col := range m.registered {
		if col == c {
			m.registered = append(m.registered[:i], m.registered[i+1:]...)
			return true
		}
	}
	return false
}

// RegisteredCount returns number of registered collectors.
func (m *MockPrometheusRegisterer) RegisteredCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registered)
}

// Ensure MockPrometheusRegisterer implements prometheus.Registerer
var _ prometheus.Registerer = (*MockPrometheusRegisterer)(nil)

// createTestConfig creates RefreshConfig optimized for testing.
//
// This config uses short intervals to speed up tests:
//   - Interval: 100ms (vs 5m in production)
//   - MaxRetries: 3 (vs 5 in production)
//   - BaseBackoff: 10ms (vs 30s in production)
//   - MaxBackoff: 50ms (vs 5m in production)
//   - RateLimitPer: 50ms (vs 1m in production)
//   - RefreshTimeout: 1s (vs 30s in production)
//   - WarmupPeriod: 10ms (vs 30s in production)
//
// Example:
//
//	config := createTestConfig()
//	manager, err := NewRefreshManager(mock, config, logger, reg)
func createTestConfig() RefreshConfig {
	return RefreshConfig{
		Interval:       100 * time.Millisecond,
		MaxRetries:     3,
		BaseBackoff:    10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		RateLimitPer:   50 * time.Millisecond,
		RefreshTimeout: 1 * time.Second,
		WarmupPeriod:   10 * time.Millisecond,
	}
}

// createTestManager creates DefaultRefreshManager for testing.
//
// This helper simplifies manager creation with sensible defaults:
//   - Mock TargetDiscoveryManager
//   - Test-optimized config (fast intervals)
//   - slog.Default() logger
//   - Mock Prometheus registerer
//
// Supports both *testing.T and *testing.B (via testing.TB interface).
//
// Example:
//
//	mock := &MockTargetDiscoveryManager{targetCount: 5}
//	manager, mockReg := createTestManager(t, mock)
//
//	// Use manager in tests
//	err := manager.Start()
//	require.NoError(t, err)
func createTestManager(tb testing.TB, mock *MockTargetDiscoveryManager) (RefreshManager, *MockPrometheusRegisterer) {
	tb.Helper()

	mockReg := &MockPrometheusRegisterer{}
	config := createTestConfig()

	manager, err := NewRefreshManager(
		mock,
		config,
		slog.Default(),
		mockReg,
	)
	if err != nil {
		tb.Fatalf("Failed to create test manager: %v", err)
	}

	return manager, mockReg
}

// waitForRefresh waits for async refresh to complete.
//
// This helper polls GetStatus() until refresh completes or timeout.
//
// Parameters:
//   - tb: Test/Benchmark instance
//   - manager: RefreshManager to monitor
//   - timeout: Max wait time (e.g., 5*time.Second)
//
// Behavior:
//   - Polls every 10ms
//   - Returns when state != in_progress
//   - Fails test if timeout exceeded
//
// Supports both *testing.T and *testing.B (via testing.TB interface).
//
// Example:
//
//	err := manager.RefreshNow()
//	require.NoError(t, err)
//
//	waitForRefresh(t, manager, 5 * time.Second)
//
//	status := manager.GetStatus()
//	assert.Equal(t, RefreshStateSuccess, status.State)
func waitForRefresh(tb testing.TB, manager RefreshManager, timeout time.Duration) {
	tb.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := manager.GetStatus()
		if status.State != RefreshStateInProgress {
			return // Refresh completed
		}
		time.Sleep(10 * time.Millisecond)
	}

	tb.Fatalf("Refresh did not complete within %v", timeout)
}

// assertRefreshStatus asserts refresh status fields.
//
// This helper validates RefreshStatus returned by GetStatus().
//
// Parameters:
//   - tb: Test/Benchmark instance
//   - manager: RefreshManager to check
//   - expectedState: Expected RefreshState
//   - expectedValid: Expected number of valid targets (0 = skip check)
//   - expectedInvalid: Expected number of invalid targets (0 = skip check)
//
// Supports both *testing.T and *testing.B (via testing.TB interface).
//
// Example:
//
//	// Assert successful refresh with 10 valid targets
//	assertRefreshStatus(t, manager, RefreshStateSuccess, 10, 0)
//
//	// Assert failed refresh (skip target counts)
//	assertRefreshStatus(t, manager, RefreshStateFailed, 0, 0)
func assertRefreshStatus(
	tb testing.TB,
	manager RefreshManager,
	expectedState RefreshState,
	expectedValid int,
	expectedInvalid int,
) {
	tb.Helper()

	status := manager.GetStatus()

	if status.State != expectedState {
		tb.Errorf("Expected state %s, got %s", expectedState, status.State)
	}

	if expectedValid > 0 && status.TargetsValid != expectedValid {
		tb.Errorf("Expected %d valid targets, got %d", expectedValid, status.TargetsValid)
	}

	if expectedInvalid > 0 && status.TargetsInvalid != expectedInvalid {
		tb.Errorf("Expected %d invalid targets, got %d", expectedInvalid, status.TargetsInvalid)
	}

	// Additional validations based on state
	switch expectedState {
	case RefreshStateSuccess:
		if status.Error != "" {
			tb.Errorf("Expected no error for success state, got: %s", status.Error)
		}
		if status.LastRefresh.IsZero() {
			tb.Errorf("Expected non-zero LastRefresh for success state")
		}
		if status.ConsecutiveFailures != 0 {
			tb.Errorf("Expected 0 consecutive failures for success, got %d", status.ConsecutiveFailures)
		}
	case RefreshStateFailed:
		if status.Error == "" {
			tb.Errorf("Expected error message for failed state")
		}
		if status.ConsecutiveFailures == 0 {
			tb.Errorf("Expected >0 consecutive failures for failed state")
		}
	}
}

// assertMetrics asserts Prometheus metrics values.
//
// This helper validates metric values after operations.
//
// Parameters:
//   - t: Test instance
//   - mockReg: MockPrometheusRegisterer used during manager creation
//   - expectedRegistered: Expected number of registered metrics (5 for RefreshMetrics)
//
// Example:
//
//	manager, mockReg := createTestManager(t, mock)
//	assertMetrics(t, mockReg, 5)  // 5 metrics registered
func assertMetrics(t *testing.T, mockReg *MockPrometheusRegisterer, expectedRegistered int) {
	t.Helper()

	if mockReg.RegisteredCount() != expectedRegistered {
		t.Errorf("Expected %d metrics registered, got %d", expectedRegistered, mockReg.RegisteredCount())
	}
}
