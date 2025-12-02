package publishing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// mockDiscoveryManagerForRefresh is a mock for testing refresh mechanism
type mockDiscoveryManagerForRefresh struct {
	discoverCalled int
	shouldFail     bool
	targetCount    int
}

func (m *mockDiscoveryManagerForRefresh) DiscoverTargets(ctx context.Context) error {
	m.discoverCalled++
	if m.shouldFail {
		return assert.AnError
	}
	return nil
}

func (m *mockDiscoveryManagerForRefresh) GetTarget(name string) (*core.PublishingTarget, error) {
	return nil, nil
}

func (m *mockDiscoveryManagerForRefresh) ListTargets() []*core.PublishingTarget {
	return nil
}

func (m *mockDiscoveryManagerForRefresh) GetTargetsByType(targetType string) []*core.PublishingTarget {
	return nil
}

func (m *mockDiscoveryManagerForRefresh) GetTargetCount() int {
	return m.targetCount
}

func (m *mockDiscoveryManagerForRefresh) GetEnabledTargetCount() int {
	return m.targetCount
}

func TestNewRefreshManager(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{targetCount: 5}
	interval := 5 * time.Minute

	manager := NewRefreshManager(mockDM, interval, nil)

	assert.NotNil(t, manager)
	assert.Equal(t, interval, manager.refreshInterval)
	assert.NotNil(t, manager.stopChan)
}

func TestRefreshNow_Success(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{targetCount: 3}

	manager := NewRefreshManager(mockDM, 5*time.Minute, nil)

	err := manager.RefreshNow(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, mockDM.discoverCalled)
}

func TestRefreshNow_Failure(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{shouldFail: true}

	manager := NewRefreshManager(mockDM, 5*time.Minute, nil)

	err := manager.RefreshNow(context.Background())

	require.Error(t, err)
	assert.Equal(t, 1, mockDM.discoverCalled)
}

func TestStart_Success(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{targetCount: 2}

	manager := NewRefreshManager(mockDM, 5*time.Minute, slog.Default())
	defer manager.Stop()

	err := manager.Start(context.Background())

	require.NoError(t, err)
	// Should have done initial discovery
	assert.Greater(t, mockDM.discoverCalled, 0)
}

func TestStart_InitialDiscoveryFailure(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{shouldFail: true}

	manager := NewRefreshManager(mockDM, 5*time.Minute, nil)

	err := manager.Start(context.Background())

	require.Error(t, err)
	assert.Equal(t, 1, mockDM.discoverCalled)
}

func TestPeriodicRefresh(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{targetCount: 2}
	interval := 100 * time.Millisecond // Short interval for testing

	manager := NewRefreshManager(mockDM, interval, slog.Default())

	err := manager.Start(context.Background())
	require.NoError(t, err)

	// Wait for at least 2 refresh cycles
	time.Sleep(250 * time.Millisecond)

	// Should have been called multiple times (initial + periodic)
	assert.Greater(t, mockDM.discoverCalled, 1)

	manager.Stop()
}

func TestStop(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{targetCount: 1}
	interval := 50 * time.Millisecond

	manager := NewRefreshManager(mockDM, interval, nil)

	err := manager.Start(context.Background())
	require.NoError(t, err)

	// Let it run for a bit
	time.Sleep(120 * time.Millisecond)
	initialCalls := mockDM.discoverCalled

	// Stop refresh
	manager.Stop()

	// Wait and check that no more calls happen
	time.Sleep(100 * time.Millisecond)
	finalCalls := mockDM.discoverCalled

	// Should have stopped (no new calls after Stop)
	assert.Equal(t, initialCalls, finalCalls)
}

func TestStop_WithoutStart(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{}

	manager := NewRefreshManager(mockDM, 5*time.Minute, nil)

	// Should not panic
	manager.Stop()

	assert.Equal(t, 0, mockDM.discoverCalled)
}

func TestRefreshWithContext_Cancellation(t *testing.T) {
	mockDM := &mockDiscoveryManagerForRefresh{}

	manager := NewRefreshManager(mockDM, 5*time.Minute, nil)

	// Create context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.RefreshNow(ctx)

	// Should still work (context is passed to DiscoverTargets)
	require.NoError(t, err)
	assert.Equal(t, 1, mockDM.discoverCalled)
}
