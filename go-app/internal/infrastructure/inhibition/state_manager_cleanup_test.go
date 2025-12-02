package inhibition

import (
	"context"
	"testing"
	"time"
)

// TestCleanupWorker_RemovesExpiredStates verifies that the cleanup worker
// automatically removes expired states.
func TestCleanupWorker_RemovesExpiredStates(t *testing.T) {
	sm := newTestStateManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set short cleanup interval for testing
	sm.cleanupInterval = 100 * time.Millisecond

	// Add 3 active states
	for i := 1; i <= 3; i++ {
		state := newTestState(
			"active-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		err := sm.RecordInhibition(ctx, state)
		if err != nil {
			t.Fatalf("RecordInhibition(%d) failed: %v", i, err)
		}
	}

	// Add 2 expired states
	for i := 1; i <= 2; i++ {
		expiredState := newExpiredState(
			"expired-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		sm.states.Store(expiredState.TargetFingerprint, expiredState)
	}

	// Verify we have 5 states total
	count := 0
	sm.states.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 5 {
		t.Errorf("Expected 5 total states before cleanup, got %d", count)
	}

	// Start cleanup worker
	sm.StartCleanupWorker(ctx)

	// Wait for cleanup to run (2 intervals to be safe)
	time.Sleep(250 * time.Millisecond)

	// Stop cleanup worker
	sm.StopCleanupWorker()

	// Verify only 3 active states remain
	count = 0
	sm.states.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 3 {
		t.Errorf("Expected 3 active states after cleanup, got %d", count)
	}

	// Verify expired states were removed
	for i := 1; i <= 2; i++ {
		fp := "expired-fp-" + string(rune('0'+i))
		_, ok := sm.states.Load(fp)
		if ok {
			t.Errorf("Expired state %s should have been cleaned up", fp)
		}
	}

	// Verify active states still exist
	for i := 1; i <= 3; i++ {
		fp := "active-fp-" + string(rune('0'+i))
		_, ok := sm.states.Load(fp)
		if !ok {
			t.Errorf("Active state %s should still exist", fp)
		}
	}
}

// TestCleanupWorker_GracefulShutdown verifies that the cleanup worker
// stops gracefully when context is cancelled or StopCleanupWorker is called.
func TestCleanupWorker_GracefulShutdown(t *testing.T) {
	sm := newTestStateManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sm.cleanupInterval = 100 * time.Millisecond

	// Start cleanup worker
	sm.StartCleanupWorker(ctx)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Stop via explicit call
	sm.StopCleanupWorker()

	// Should complete without hanging
	// (if it hangs, test will timeout)

	t.Log("Cleanup worker stopped gracefully")
}

// TestCleanupWorker_ContextCancellation verifies that the cleanup worker
// stops when context is cancelled.
func TestCleanupWorker_ContextCancellation(t *testing.T) {
	sm := newTestStateManager(t)
	ctx, cancel := context.WithCancel(context.Background())

	sm.cleanupInterval = 100 * time.Millisecond

	// Start cleanup worker
	sm.StartCleanupWorker(ctx)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for cleanup worker to stop
	sm.cleanupDone.Wait()

	t.Log("Cleanup worker stopped via context cancellation")
}

// TestCleanupExpiredStates_DirectCall verifies the cleanupExpiredStates
// method can be called directly and works correctly.
func TestCleanupExpiredStates_DirectCall(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Add 2 expired states
	for i := 1; i <= 2; i++ {
		expiredState := newExpiredState(
			"expired-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		sm.states.Store(expiredState.TargetFingerprint, expiredState)
	}

	// Add 1 active state
	activeState := newTestState("active-fp", "source-fp", "rule-1")
	err := sm.RecordInhibition(ctx, activeState)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	// Verify 3 states exist
	count := 0
	sm.states.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 3 {
		t.Errorf("Expected 3 states before cleanup, got %d", count)
	}

	// Call cleanupExpiredStates directly
	sm.cleanupExpiredStates(ctx)

	// Verify only 1 state remains
	count = 0
	sm.states.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("Expected 1 state after cleanup, got %d", count)
	}

	// Verify active state still exists
	_, ok := sm.states.Load("active-fp")
	if !ok {
		t.Error("Active state should still exist")
	}
}

// TestStopCleanupWorker_MultipleCallsSafe verifies that calling
// StopCleanupWorker multiple times is safe.
func TestStopCleanupWorker_MultipleCallsSafe(t *testing.T) {
	sm := newTestStateManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sm.cleanupInterval = 100 * time.Millisecond

	// Start cleanup worker
	sm.StartCleanupWorker(ctx)

	// Stop multiple times (should not panic)
	sm.StopCleanupWorker()
	sm.StopCleanupWorker()
	sm.StopCleanupWorker()

	t.Log("Multiple StopCleanupWorker calls handled safely")
}
