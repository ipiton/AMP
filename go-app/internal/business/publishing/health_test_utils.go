package publishing

import (
	"context"
	"fmt"
	"sync"

	"github.com/ipiton/AMP/internal/core"
)

// TestHealthDiscoveryManager is enhanced mock для health monitoring tests.
//
// Расширяет базовый MockTargetDiscoveryManager с поддержкой:
//   - Управление списком targets (SetTargets)
//   - GetTarget по имени
//   - ListTargets с реальными данными
//
// Thread-safe: Да (sync.RWMutex)
type TestHealthDiscoveryManager struct {
	mu      sync.RWMutex
	targets []*core.PublishingTarget
}

// NewTestHealthDiscoveryManager creates new test discovery manager.
func NewTestHealthDiscoveryManager() *TestHealthDiscoveryManager {
	return &TestHealthDiscoveryManager{
		targets: make([]*core.PublishingTarget, 0),
	}
}

// SetTargets sets targets for testing.
func (m *TestHealthDiscoveryManager) SetTargets(targets []*core.PublishingTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = targets
}

// AddTarget adds a single target for testing.
func (m *TestHealthDiscoveryManager) AddTarget(target *core.PublishingTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = append(m.targets, target)
}

// RemoveTarget removes a target by name.
func (m *TestHealthDiscoveryManager) RemoveTarget(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, t := range m.targets {
		if t.Name == name {
			m.targets = append(m.targets[:i], m.targets[i+1:]...)
			return
		}
	}
}

// DiscoverTargets mock implementation (always succeeds).
func (m *TestHealthDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	return nil
}

// ListTargets returns all targets.
func (m *TestHealthDiscoveryManager) ListTargets() []*core.PublishingTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return copy
	result := make([]*core.PublishingTarget, len(m.targets))
	copy(result, m.targets)
	return result
}

// GetTarget returns target by name.
func (m *TestHealthDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.targets {
		if t.Name == name {
			return t, nil
		}
	}

	return nil, fmt.Errorf("target not found: %s", name)
}

// GetTargetsByType returns targets by type.
func (m *TestHealthDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
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

// GetStats returns discovery statistics.
func (m *TestHealthDiscoveryManager) GetStats() DiscoveryStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return DiscoveryStats{
		TotalTargets:   len(m.targets),
		ValidTargets:   len(m.targets),
		InvalidTargets: 0,
	}
}

// Health checks manager health.
func (m *TestHealthDiscoveryManager) Health(ctx context.Context) error {
	return nil
}
