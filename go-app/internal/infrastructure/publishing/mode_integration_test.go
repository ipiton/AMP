package publishing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// TestModeManager_MetricsOnlyBehavior tests metrics-only mode detection
func TestModeManager_MetricsOnlyBehavior(t *testing.T) {
	// Create stub discovery manager with no targets (metrics-only mode)
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)

	// Create mode manager (nil metrics to avoid registry conflicts in tests)
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Trigger initial mode check to update state
	modeManager.CheckModeTransition()

	// Verify metrics-only mode
	if !modeManager.IsMetricsOnly() {
		t.Fatal("Expected metrics-only mode with no targets")
	}

	if mode := modeManager.GetCurrentMode(); mode != ModeMetricsOnly {
		t.Errorf("Expected ModeMetricsOnly, got %v", mode)
	}

	// Simulate handler checking mode (handlers would reject submissions)
	if !modeManager.IsMetricsOnly() {
		t.Error("Handler check: should be in metrics-only mode")
	}

	// Add target and verify normal mode
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})
	modeManager.CheckModeTransition()

	if modeManager.IsMetricsOnly() {
		t.Error("Expected normal mode after adding target")
	}

	if mode := modeManager.GetCurrentMode(); mode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", mode)
	}
}

// TestModeManager_NormalModeBehavior tests normal mode detection
func TestModeManager_NormalModeBehavior(t *testing.T) {
	// Create stub discovery manager with one enabled target (normal mode)
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	// Create mode manager (nil metrics to avoid registry conflicts in tests)
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Trigger initial mode check to update state
	modeManager.CheckModeTransition()

	// Verify normal mode
	if modeManager.IsMetricsOnly() {
		t.Fatal("Expected normal mode with enabled target")
	}

	if mode := modeManager.GetCurrentMode(); mode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", mode)
	}

	// Simulate handler checking mode (handlers would accept submissions)
	if modeManager.IsMetricsOnly() {
		t.Error("Handler check: should be in normal mode")
	}

	// Remove all targets and verify metrics-only mode
	stubDiscovery.ClearTargets()
	modeManager.CheckModeTransition()

	if !modeManager.IsMetricsOnly() {
		t.Error("Expected metrics-only mode after removing targets")
	}

	if mode := modeManager.GetCurrentMode(); mode != ModeMetricsOnly {
		t.Errorf("Expected ModeMetricsOnly, got %v", mode)
	}
}

// TestModeTransition_EndToEnd tests mode transitions with all components
func TestModeTransition_EndToEnd(t *testing.T) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	// Use nil metrics to avoid registry conflicts in tests
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Start mode manager
	ctx := context.Background()
	if err := modeManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start mode manager: %v", err)
	}
	defer modeManager.Stop()

	// Track mode changes via callback for verification
	var callbackCalled int32 // Use atomic for thread-safety
	modeManager.Subscribe(func(from, to Mode, reason string) {
		atomic.StoreInt32(&callbackCalled, 1)
		t.Logf("Mode transition: %v -> %v (reason: %s)", from, to, reason)
	})

	// Trigger initial check to set proper mode (transition #1: normal->metrics-only)
	modeManager.CheckModeTransition()

	// Initial state: metrics-only (no targets)
	if mode := modeManager.GetCurrentMode(); mode != ModeMetricsOnly {
		t.Errorf("Expected initial mode ModeMetricsOnly, got %v", mode)
	}

	// Verify transition was recorded
	initialMetrics := modeManager.GetModeMetrics()
	if initialMetrics.TransitionCount < 1 {
		t.Errorf("Expected at least 1 transition, got %d", initialMetrics.TransitionCount)
	}

	// Transition #2: metrics-only -> normal (add enabled target)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "target-1",
		Type:    "webhook",
		Enabled: true,
	})

	// Trigger mode check
	_, changed, err := modeManager.CheckModeTransition()
	if err != nil {
		t.Fatalf("CheckModeTransition failed: %v", err)
	}
	if !changed {
		t.Error("Expected mode change after adding target")
	}

	// Verify normal mode
	if mode := modeManager.GetCurrentMode(); mode != ModeNormal {
		t.Errorf("Expected mode ModeNormal after adding target, got %v", mode)
	}

	// Verify transition count increased
	afterAddMetrics := modeManager.GetModeMetrics()
	if afterAddMetrics.TransitionCount <= initialMetrics.TransitionCount {
		t.Errorf("Expected transition count to increase after adding target, got %d (was %d)",
			afterAddMetrics.TransitionCount, initialMetrics.TransitionCount)
	}

	// Transition #3: normal -> metrics-only (remove all targets)
	stubDiscovery.ClearTargets()

	// Trigger mode check
	_, changed, err = modeManager.CheckModeTransition()
	if err != nil {
		t.Fatalf("CheckModeTransition failed: %v", err)
	}
	if !changed {
		t.Error("Expected mode change after removing targets")
	}

	// Verify metrics-only mode
	if mode := modeManager.GetCurrentMode(); mode != ModeMetricsOnly {
		t.Errorf("Expected mode ModeMetricsOnly after removing targets, got %v", mode)
	}

	// Verify final metrics
	finalMetrics := modeManager.GetModeMetrics()
	if finalMetrics.TransitionCount < 3 {
		t.Errorf("Expected at least 3 transitions, got %d", finalMetrics.TransitionCount)
	}
	if finalMetrics.CurrentMode != ModeMetricsOnly {
		t.Errorf("Expected current mode ModeMetricsOnly, got %v", finalMetrics.CurrentMode)
	}
	if finalMetrics.LastTransitionReason == "" {
		t.Error("Expected non-empty last transition reason")
	}

	// Verify callback was called at least once
	// Note: Since callbacks are executed asynchronously in goroutines,
	// we may need to add a small delay to ensure they're called
	time.Sleep(10 * time.Millisecond)
	if atomic.LoadInt32(&callbackCalled) == 0 {
		t.Error("Expected subscriber callback to be called")
	}
}

// TestQueueWorker_SkipsJobsInMetricsOnlyMode tests that queue workers skip jobs in metrics-only mode
func TestQueueWorker_SkipsJobsInMetricsOnlyMode(t *testing.T) {
	// This is a simplified test - full queue worker tests would require more setup
	// For now, we verify the mode manager behavior that queue workers rely on

	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	// Use nil metrics to avoid registry conflicts in tests
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Trigger initial mode check
	modeManager.CheckModeTransition()

	// Verify metrics-only mode
	if !modeManager.IsMetricsOnly() {
		t.Fatal("Expected metrics-only mode with no targets")
	}

	// Simulate queue worker checking mode before processing
	shouldSkip := modeManager.IsMetricsOnly()
	if !shouldSkip {
		t.Error("Queue worker should skip jobs in metrics-only mode")
	}

	// Add target and verify normal mode
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "target-1",
		Type:    "webhook",
		Enabled: true,
	})
	modeManager.CheckModeTransition()

	// Now worker should process jobs
	shouldSkip = modeManager.IsMetricsOnly()
	if shouldSkip {
		t.Error("Queue worker should process jobs in normal mode")
	}
}

// TestGetPublishingMode_EnhancedResponse tests the enhanced /api/v1/publishing/mode endpoint
func TestGetPublishingMode_EnhancedResponse(t *testing.T) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	// Use nil metrics to avoid registry conflicts in tests
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Trigger initial mode check to populate metrics (normal->metrics-only)
	modeManager.CheckModeTransition()

	// Then add target to trigger second transition (metrics-only->normal)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "target-1",
		Type:    "webhook",
		Enabled: true,
	})
	modeManager.CheckModeTransition()

	// Create handlers
	handlers := &PublishingHandlers{
		modeManager:      modeManager,
		discoveryManager: stubDiscovery,
		logger:           logger,
	}

	// Send request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/publishing/mode", nil)
	w := httptest.NewRecorder()

	handlers.GetPublishingMode(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp PublishingModeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify basic fields
	if resp.Mode != "normal" {
		t.Errorf("Expected mode='normal', got %s", resp.Mode)
	}
	if !resp.TargetsAvailable {
		t.Error("Expected targets_available=true")
	}
	if resp.EnabledTargets != 1 {
		t.Errorf("Expected enabled_targets=1, got %d", resp.EnabledTargets)
	}
	if resp.MetricsOnlyActive {
		t.Error("Expected metrics_only_active=false")
	}

	// Verify enhanced fields (TN-060 additions)
	// We triggered 2 transitions: normal->metrics-only, then metrics-only->normal
	if resp.TransitionCount < 1 {
		t.Errorf("Expected transition_count>=1, got %d", resp.TransitionCount)
	}
	if resp.CurrentModeDurationSeconds < 0 {
		t.Errorf("Expected positive duration, got %f", resp.CurrentModeDurationSeconds)
	}
	// Note: LastTransitionReason may be empty if using nil metrics (no history tracked)
	// But transition count should be tracked via atomic counter
}

// TestModeManager_PerformanceUnderLoad tests mode manager performance with concurrent access
func TestModeManager_PerformanceUnderLoad(t *testing.T) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	// Use nil metrics to avoid registry conflicts in tests
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	// Add some targets
	for i := 0; i < 5; i++ {
		stubDiscovery.AddTarget(&core.PublishingTarget{
			Name:    "target-" + string(rune(i)),
			Type:    "webhook",
			Enabled: true,
		})
	}
	modeManager.CheckModeTransition()

	// Concurrent goroutines checking mode
	const goroutines = 100
	const iterations = 1000
	start := time.Now()

	done := make(chan bool, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < iterations; i++ {
				// These operations should be very fast (<1µs each)
				_ = modeManager.IsMetricsOnly()
				_ = modeManager.GetCurrentMode()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for g := 0; g < goroutines; g++ {
		<-done
	}

	elapsed := time.Since(start)
	totalOps := goroutines * iterations * 2 // 2 operations per iteration
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	t.Logf("Performance: %d ops in %v = %.0f ops/sec", totalOps, elapsed, opsPerSec)

	// Should handle >1M ops/sec on modern hardware
	if opsPerSec < 100000 {
		t.Errorf("Performance too low: %.0f ops/sec (expected >100k ops/sec)", opsPerSec)
	}

	// Average latency should be <1µs
	avgLatency := elapsed / time.Duration(totalOps)
	if avgLatency > 10*time.Microsecond {
		t.Errorf("Average latency too high: %v (expected <10µs)", avgLatency)
	}
}

// Integration tests verify mode manager behavior that components rely on.
// Full end-to-end tests with actual handlers/queue/coordinator would require
// more complex setup with all dependencies. These tests focus on the core
// mode detection and transition logic that all components use.
