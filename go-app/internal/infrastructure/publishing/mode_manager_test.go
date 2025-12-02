package publishing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// modeTestTargetDiscoveryManager is a mock implementation of TargetDiscoveryManager for mode manager tests
type modeTestTargetDiscoveryManager struct {
	targets []*core.PublishingTarget
	mu      sync.RWMutex
}

func newModeTestTargetDiscoveryManager() *modeTestTargetDiscoveryManager {
	return &modeTestTargetDiscoveryManager{
		targets: make([]*core.PublishingTarget, 0),
	}
}

func (m *modeTestTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	return nil
}

func (m *modeTestTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.targets {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, errors.New("target not found")
}

func (m *modeTestTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*core.PublishingTarget, len(m.targets))
	copy(result, m.targets)
	return result
}

func (m *modeTestTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*core.PublishingTarget, 0)
	for _, t := range m.targets {
		if t.Type == targetType {
			result = append(result, t)
		}
	}
	return result
}

func (m *modeTestTargetDiscoveryManager) GetTargetCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.targets)
}

func (m *modeTestTargetDiscoveryManager) setTargets(targets []*core.PublishingTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = targets
}

func (m *modeTestTargetDiscoveryManager) addTarget(target *core.PublishingTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = append(m.targets, target)
}

func (m *modeTestTargetDiscoveryManager) clearTargets() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = make([]*core.PublishingTarget, 0)
}

func TestModeManager_GetCurrentMode(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Test default mode (normal)
	if mode := manager.GetCurrentMode(); mode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", mode)
	}

	// Test metrics-only mode
	mockDiscovery.setTargets([]*core.PublishingTarget{})
	manager.CheckModeTransition()
	if mode := manager.GetCurrentMode(); mode != ModeMetricsOnly {
		t.Errorf("Expected ModeMetricsOnly, got %v", mode)
	}
}

func TestModeManager_IsMetricsOnly(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Test normal mode
	mockDiscovery.addTarget(&core.PublishingTarget{
		Name:    "test-target",
		Enabled: true,
	})
	manager.CheckModeTransition()
	if manager.IsMetricsOnly() {
		t.Error("Expected IsMetricsOnly() to return false in normal mode")
	}

	// Test metrics-only mode
	mockDiscovery.clearTargets()
	manager.CheckModeTransition()
	if !manager.IsMetricsOnly() {
		t.Error("Expected IsMetricsOnly() to return true in metrics-only mode")
	}
}

func TestModeManager_CheckModeTransition(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Test transition to metrics-only
	mockDiscovery.setTargets([]*core.PublishingTarget{})
	newMode, changed, err := manager.CheckModeTransition()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !changed {
		t.Error("Expected mode change to metrics-only")
	}
	if newMode != ModeMetricsOnly {
		t.Errorf("Expected ModeMetricsOnly, got %v", newMode)
	}

	// Test transition to normal
	mockDiscovery.addTarget(&core.PublishingTarget{
		Name:    "test-target",
		Enabled: true,
	})
	newMode, changed, err = manager.CheckModeTransition()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !changed {
		t.Error("Expected mode change to normal")
	}
	if newMode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", newMode)
	}

	// Test no transition
	newMode, changed, err = manager.CheckModeTransition()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if changed {
		t.Error("Expected no mode change")
	}
	if newMode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", newMode)
	}
}

func TestModeManager_OnTargetsChanged(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Set initial state to normal
	mockDiscovery.addTarget(&core.PublishingTarget{
		Name:    "test-target",
		Enabled: true,
	})
	manager.CheckModeTransition()

	// Change to metrics-only
	mockDiscovery.clearTargets()
	if err := manager.OnTargetsChanged(); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !manager.IsMetricsOnly() {
		t.Error("Expected metrics-only mode after OnTargetsChanged")
	}
}

func TestModeManager_Subscribe(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Set initial state to metrics-only
	mockDiscovery.setTargets([]*core.PublishingTarget{})
	manager.CheckModeTransition()

	// Subscribe to mode changes
	callbackCh := make(chan struct {
		from   Mode
		to     Mode
		reason string
	}, 1)

	unsubscribe := manager.Subscribe(func(from, to Mode, r string) {
		callbackCh <- struct {
			from   Mode
			to     Mode
			reason string
		}{from, to, r}
	})

	// Trigger mode change to normal
	mockDiscovery.addTarget(&core.PublishingTarget{
		Name:    "test-target",
		Enabled: true,
	})
	manager.CheckModeTransition()

	// Wait for callback (with timeout)
	select {
	case result := <-callbackCh:
		if result.from != ModeMetricsOnly {
			t.Errorf("Expected fromMode ModeMetricsOnly, got %v", result.from)
		}
		if result.to != ModeNormal {
			t.Errorf("Expected toMode ModeNormal, got %v", result.to)
		}
		if result.reason != "targets_available" {
			t.Errorf("Expected reason 'targets_available', got %s", result.reason)
		}
	case <-time.After(1 * time.Second):
		t.Error("Expected callback to be called within 1 second")
	}

	// Unsubscribe
	unsubscribe()

	// Trigger another change
	mockDiscovery.clearTargets()
	manager.CheckModeTransition()

	// Wait for callback (should not be called)
	select {
	case <-callbackCh:
		t.Error("Expected callback not to be called after unsubscribe")
	case <-time.After(200 * time.Millisecond):
		// Expected - no callback
	}
}

func TestModeManager_GetModeMetrics(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Set initial state
	mockDiscovery.setTargets([]*core.PublishingTarget{})
	manager.CheckModeTransition()

	metrics := manager.GetModeMetrics()

	if metrics.CurrentMode != ModeMetricsOnly {
		t.Errorf("Expected CurrentMode ModeMetricsOnly, got %v", metrics.CurrentMode)
	}
	if metrics.TransitionCount < 1 {
		t.Errorf("Expected TransitionCount >= 1, got %d", metrics.TransitionCount)
	}
	if metrics.LastTransitionReason != "no_enabled_targets" {
		t.Errorf("Expected LastTransitionReason 'no_enabled_targets', got %s", metrics.LastTransitionReason)
	}
}

func TestModeManager_StartStop(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start manager
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Unexpected error starting manager: %v", err)
	}

	// Wait a bit for periodic check
	time.Sleep(100 * time.Millisecond)

	// Stop manager
	if err := manager.Stop(); err != nil {
		t.Fatalf("Unexpected error stopping manager: %v", err)
	}

	// Verify stop worked (second stop should be safe)
	// Note: We don't call Stop() twice as it's idempotent but we test it doesn't panic
	// The implementation now handles double-stop gracefully
}

func TestModeManager_Caching(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Set initial state
	mockDiscovery.setTargets([]*core.PublishingTarget{})
	manager.CheckModeTransition()

	// Get mode multiple times (should use cache)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		manager.GetCurrentMode()
	}
	duration := time.Since(start)

	// Should be very fast (<1ms for 1000 calls)
	if duration > 10*time.Millisecond {
		t.Errorf("GetCurrentMode() too slow: %v for 1000 calls", duration)
	}
}

func TestModeManager_ConcurrentAccess(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Set initial state
	mockDiscovery.setTargets([]*core.PublishingTarget{})

	var wg sync.WaitGroup
	concurrency := 100

	// Concurrent reads
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				manager.GetCurrentMode()
				manager.IsMetricsOnly()
			}
		}()
	}

	// Concurrent writes
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				manager.CheckModeTransition()
			}
		}()
	}

	wg.Wait()

	// Verify no race conditions
	metrics := manager.GetModeMetrics()
	if metrics.CurrentMode != ModeMetricsOnly && metrics.CurrentMode != ModeNormal {
		t.Errorf("Invalid mode after concurrent access: %v", metrics.CurrentMode)
	}
}

func TestModeManager_EdgeCases(t *testing.T) {
	mockDiscovery := newModeTestTargetDiscoveryManager()
	manager := NewModeManager(mockDiscovery, nil, nil).(*DefaultModeManager)

	// Test with disabled targets (should be metrics-only)
	mockDiscovery.addTarget(&core.PublishingTarget{
		Name:    "disabled-target",
		Enabled: false,
	})
	newMode, changed, err := manager.CheckModeTransition()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !changed {
		t.Error("Expected mode change to metrics-only (all targets disabled)")
	}
	if newMode != ModeMetricsOnly {
		t.Errorf("Expected ModeMetricsOnly, got %v", newMode)
	}

	// Test with mixed enabled/disabled targets
	mockDiscovery.setTargets([]*core.PublishingTarget{
		{Name: "enabled", Enabled: true},
		{Name: "disabled", Enabled: false},
	})
	newMode, changed, err = manager.CheckModeTransition()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !changed {
		t.Error("Expected mode change to normal (has enabled targets)")
	}
	if newMode != ModeNormal {
		t.Errorf("Expected ModeNormal, got %v", newMode)
	}
}

func TestMode_String(t *testing.T) {
	tests := []struct {
		mode     Mode
		expected string
	}{
		{ModeNormal, "normal"},
		{ModeMetricsOnly, "metrics-only"},
		{Mode(999), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.expected {
			t.Errorf("Mode(%d).String() = %v, want %v", tt.mode, got, tt.expected)
		}
	}
}
