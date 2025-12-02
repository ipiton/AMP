package inhibition

import (
	"context"
	"testing"
	"time"

	"log/slog"
)

// Helper function to create a test state manager
// Note: We use nil metrics to avoid duplicate Prometheus registration issues in tests
func newTestStateManager(t *testing.T) *DefaultStateManager {
	t.Helper()

	logger := slog.Default()

	// Pass nil metrics to avoid duplicate registration errors
	// In production, metrics will be provided by the caller
	return NewDefaultStateManager(nil, logger, nil)
}

// Helper function to create a test state manager with a unique namespace (for metrics tests)
func newTestStateManagerWithMetrics(t *testing.T, namespace string) *DefaultStateManager {
	t.Helper()

	logger := slog.Default()

	// Note: We skip metrics here to avoid duplicate registration issues
	// In integration tests, we can test metrics properly
	_ = namespace // Prevent unused variable warning
	return NewDefaultStateManager(nil, logger, nil)
}

// Helper function to create a test inhibition state
func newTestState(targetFP, sourceFP, ruleName string) *InhibitionState {
	return &InhibitionState{
		TargetFingerprint: targetFP,
		SourceFingerprint: sourceFP,
		RuleName:          ruleName,
		InhibitedAt:       time.Now(),
		ExpiresAt:         nil,
	}
}

// Helper function to create an expired inhibition state
func newExpiredState(targetFP, sourceFP, ruleName string) *InhibitionState {
	expiresAt := time.Now().Add(-1 * time.Hour) // Expired 1 hour ago
	return &InhibitionState{
		TargetFingerprint: targetFP,
		SourceFingerprint: sourceFP,
		RuleName:          ruleName,
		InhibitedAt:       time.Now().Add(-2 * time.Hour),
		ExpiresAt:         &expiresAt,
	}
}

// ==================== Basic Operations Tests (4 tests) ====================

func TestRecordInhibition_Success(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state := newTestState("target-fp-1", "source-fp-1", "rule-1")

	err := sm.RecordInhibition(ctx, state)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	// Verify state was stored
	value, ok := sm.states.Load("target-fp-1")
	if !ok {
		t.Fatal("State not found in memory")
	}

	storedState, ok := value.(*InhibitionState)
	if !ok {
		t.Fatal("Stored value is not InhibitionState")
	}

	if storedState.TargetFingerprint != "target-fp-1" {
		t.Errorf("Expected target fingerprint 'target-fp-1', got %s", storedState.TargetFingerprint)
	}

	if storedState.SourceFingerprint != "source-fp-1" {
		t.Errorf("Expected source fingerprint 'source-fp-1', got %s", storedState.SourceFingerprint)
	}

	if storedState.RuleName != "rule-1" {
		t.Errorf("Expected rule name 'rule-1', got %s", storedState.RuleName)
	}
}

func TestRecordInhibition_NilState(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	err := sm.RecordInhibition(ctx, nil)
	if err == nil {
		t.Fatal("Expected error for nil state, got nil")
	}

	expectedError := "state cannot be nil"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestRecordInhibition_EmptyTargetFingerprint(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state := &InhibitionState{
		TargetFingerprint: "", // Empty
		SourceFingerprint: "source-fp",
		RuleName:          "rule-1",
		InhibitedAt:       time.Now(),
	}

	err := sm.RecordInhibition(ctx, state)
	if err == nil {
		t.Fatal("Expected error for empty target fingerprint, got nil")
	}

	expectedError := "target fingerprint cannot be empty"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestRecordInhibition_EmptySourceFingerprint(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state := &InhibitionState{
		TargetFingerprint: "target-fp",
		SourceFingerprint: "", // Empty
		RuleName:          "rule-1",
		InhibitedAt:       time.Now(),
	}

	err := sm.RecordInhibition(ctx, state)
	if err == nil {
		t.Fatal("Expected error for empty source fingerprint, got nil")
	}

	expectedError := "source fingerprint cannot be empty"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

// ==================== Removal Tests (3 tests) ====================

func TestRemoveInhibition_Success(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// First, record an inhibition
	state := newTestState("target-fp-1", "source-fp-1", "rule-1")
	err := sm.RecordInhibition(ctx, state)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	// Verify it exists
	_, ok := sm.states.Load("target-fp-1")
	if !ok {
		t.Fatal("State not found after recording")
	}

	// Remove it
	err = sm.RemoveInhibition(ctx, "target-fp-1")
	if err != nil {
		t.Fatalf("RemoveInhibition() failed: %v", err)
	}

	// Verify it's gone
	_, ok = sm.states.Load("target-fp-1")
	if ok {
		t.Fatal("State still exists after removal")
	}
}

func TestRemoveInhibition_EmptyFingerprint(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	err := sm.RemoveInhibition(ctx, "")
	if err == nil {
		t.Fatal("Expected error for empty fingerprint, got nil")
	}

	expectedError := "target fingerprint cannot be empty"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestRemoveInhibition_NonExistent(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Remove non-existent state (should be idempotent)
	err := sm.RemoveInhibition(ctx, "non-existent-fp")
	if err != nil {
		t.Fatalf("RemoveInhibition() should be idempotent, got error: %v", err)
	}
}

// ==================== Query Tests (8 tests) ====================

func TestGetActiveInhibitions_MultipleStates(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Record 5 inhibitions
	for i := 1; i <= 5; i++ {
		state := newTestState(
			"target-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		err := sm.RecordInhibition(ctx, state)
		if err != nil {
			t.Fatalf("RecordInhibition(%d) failed: %v", i, err)
		}
	}

	// Get active inhibitions
	states, err := sm.GetActiveInhibitions(ctx)
	if err != nil {
		t.Fatalf("GetActiveInhibitions() failed: %v", err)
	}

	if len(states) != 5 {
		t.Errorf("Expected 5 active states, got %d", len(states))
	}
}

func TestGetActiveInhibitions_FiltersExpired(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Record 2 expired states
	for i := 1; i <= 2; i++ {
		state := newExpiredState(
			"expired-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		sm.states.Store(state.TargetFingerprint, state)
	}

	// Record 1 active state
	activeState := newTestState("active-fp", "source-fp", "rule-1")
	err := sm.RecordInhibition(ctx, activeState)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	// Get active inhibitions (should filter out expired)
	states, err := sm.GetActiveInhibitions(ctx)
	if err != nil {
		t.Fatalf("GetActiveInhibitions() failed: %v", err)
	}

	if len(states) != 1 {
		t.Errorf("Expected 1 active state (expired filtered out), got %d", len(states))
	}

	if states[0].TargetFingerprint != "active-fp" {
		t.Errorf("Expected active-fp, got %s", states[0].TargetFingerprint)
	}
}

func TestGetActiveInhibitions_Empty(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	states, err := sm.GetActiveInhibitions(ctx)
	if err != nil {
		t.Fatalf("GetActiveInhibitions() failed: %v", err)
	}

	if len(states) != 0 {
		t.Errorf("Expected 0 states, got %d", len(states))
	}
}

func TestGetInhibitedAlerts_ReturnsFingerprints(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Record 3 inhibitions
	fingerprints := []string{"target-fp-1", "target-fp-2", "target-fp-3"}
	for i, fp := range fingerprints {
		state := newTestState(fp, "source-fp-"+string(rune('0'+i)), "rule-1")
		err := sm.RecordInhibition(ctx, state)
		if err != nil {
			t.Fatalf("RecordInhibition(%s) failed: %v", fp, err)
		}
	}

	// Get inhibited alerts
	inhibitedFPs, err := sm.GetInhibitedAlerts(ctx)
	if err != nil {
		t.Fatalf("GetInhibitedAlerts() failed: %v", err)
	}

	if len(inhibitedFPs) != 3 {
		t.Errorf("Expected 3 fingerprints, got %d", len(inhibitedFPs))
	}

	// Verify all fingerprints are present (order doesn't matter)
	fpMap := make(map[string]bool)
	for _, fp := range inhibitedFPs {
		fpMap[fp] = true
	}

	for _, expected := range fingerprints {
		if !fpMap[expected] {
			t.Errorf("Expected fingerprint %s not found", expected)
		}
	}
}

func TestIsInhibited_True(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state := newTestState("target-fp", "source-fp", "rule-1")
	err := sm.RecordInhibition(ctx, state)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	inhibited, err := sm.IsInhibited(ctx, "target-fp")
	if err != nil {
		t.Fatalf("IsInhibited() failed: %v", err)
	}

	if !inhibited {
		t.Error("Expected IsInhibited to return true")
	}
}

func TestIsInhibited_False(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	inhibited, err := sm.IsInhibited(ctx, "non-existent-fp")
	if err != nil {
		t.Fatalf("IsInhibited() failed: %v", err)
	}

	if inhibited {
		t.Error("Expected IsInhibited to return false")
	}
}

func TestIsInhibited_Expired(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Store expired state directly
	expiredState := newExpiredState("expired-fp", "source-fp", "rule-1")
	sm.states.Store("expired-fp", expiredState)

	// Check if inhibited (should auto-cleanup and return false)
	inhibited, err := sm.IsInhibited(ctx, "expired-fp")
	if err != nil {
		t.Fatalf("IsInhibited() failed: %v", err)
	}

	if inhibited {
		t.Error("Expected IsInhibited to return false for expired state")
	}

	// Verify state was auto-cleaned
	_, ok := sm.states.Load("expired-fp")
	if ok {
		t.Error("Expired state should have been auto-cleaned")
	}
}

func TestGetInhibitionState_Found(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state := newTestState("target-fp", "source-fp", "rule-1")
	err := sm.RecordInhibition(ctx, state)
	if err != nil {
		t.Fatalf("RecordInhibition() failed: %v", err)
	}

	retrievedState, err := sm.GetInhibitionState(ctx, "target-fp")
	if err != nil {
		t.Fatalf("GetInhibitionState() failed: %v", err)
	}

	if retrievedState == nil {
		t.Fatal("Expected state to be found, got nil")
	}

	if retrievedState.TargetFingerprint != "target-fp" {
		t.Errorf("Expected target-fp, got %s", retrievedState.TargetFingerprint)
	}

	if retrievedState.SourceFingerprint != "source-fp" {
		t.Errorf("Expected source-fp, got %s", retrievedState.SourceFingerprint)
	}

	if retrievedState.RuleName != "rule-1" {
		t.Errorf("Expected rule-1, got %s", retrievedState.RuleName)
	}
}

func TestGetInhibitionState_NotFound(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	state, err := sm.GetInhibitionState(ctx, "non-existent-fp")
	if err != nil {
		t.Fatalf("GetInhibitionState() should not error on not found, got: %v", err)
	}

	if state != nil {
		t.Errorf("Expected nil state for non-existent fingerprint, got %+v", state)
	}
}

// ==================== Helper Method Tests ====================

func TestCountActiveStates(t *testing.T) {
	sm := newTestStateManager(t)
	ctx := context.Background()

	// Initially 0
	count := sm.countActiveStates()
	if count != 0 {
		t.Errorf("Expected 0 active states initially, got %d", count)
	}

	// Add 3 active states
	for i := 1; i <= 3; i++ {
		state := newTestState(
			"target-fp-"+string(rune('0'+i)),
			"source-fp-"+string(rune('0'+i)),
			"rule-1",
		)
		err := sm.RecordInhibition(ctx, state)
		if err != nil {
			t.Fatalf("RecordInhibition(%d) failed: %v", i, err)
		}
	}

	count = sm.countActiveStates()
	if count != 3 {
		t.Errorf("Expected 3 active states, got %d", count)
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

	// Should still count only 3 (expired not counted)
	count = sm.countActiveStates()
	if count != 3 {
		t.Errorf("Expected 3 active states (expired not counted), got %d", count)
	}
}
