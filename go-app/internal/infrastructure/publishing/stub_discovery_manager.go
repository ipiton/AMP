// Package publishing provides stub implementations for testing
package publishing

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/ipiton/AMP/internal/core"
)

var (
	// ErrTargetNotFound is returned when target is not found in stub manager
	ErrTargetNotFound = errors.New("target not found")
)

// StubTargetDiscoveryManager is a simple in-memory implementation of TargetDiscoveryManager for testing.
// It allows manual control over the targets list to test mode transitions.
//
// TN-060: Used for testing Metrics-Only Mode when K8s discovery is not available.
type StubTargetDiscoveryManager struct {
	targets []*core.PublishingTarget
	mu      sync.RWMutex
	logger  *slog.Logger
}

// NewStubTargetDiscoveryManager creates a new stub discovery manager.
// By default, it starts with no targets (metrics-only mode).
func NewStubTargetDiscoveryManager(logger *slog.Logger) *StubTargetDiscoveryManager {
	return &StubTargetDiscoveryManager{
		targets: make([]*core.PublishingTarget, 0),
		logger:  logger,
	}
}

// ListTargets returns the current list of targets.
func (s *StubTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to avoid external modification
	result := make([]*core.PublishingTarget, len(s.targets))
	copy(result, s.targets)
	return result
}

// GetTargetByName finds a target by name.
func (s *StubTargetDiscoveryManager) GetTargetByName(name string) (*core.PublishingTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, target := range s.targets {
		if target.Name == name {
			return target, nil
		}
	}
	return nil, ErrTargetNotFound
}

// GetTarget is an alias for GetTargetByName (for interface compatibility).
func (s *StubTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	return s.GetTargetByName(name)
}

// GetTargetCount returns the total number of targets.
func (s *StubTargetDiscoveryManager) GetTargetCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.targets)
}

// GetTargetsByType returns targets filtered by type (for interface compatibility).
func (s *StubTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*core.PublishingTarget, 0)
	for _, target := range s.targets {
		if target.Type == targetType {
			result = append(result, target)
		}
	}
	return result
}

// GetEnabledTargets returns only enabled targets (for interface compatibility).
func (s *StubTargetDiscoveryManager) GetEnabledTargets() []*core.PublishingTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*core.PublishingTarget, 0)
	for _, target := range s.targets {
		if target.Enabled {
			result = append(result, target)
		}
	}
	return result
}

// GetTargetStats returns statistics about discovered targets (for interface compatibility).
func (s *StubTargetDiscoveryManager) GetTargetStats() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	enabled := 0
	disabled := 0
	for _, target := range s.targets {
		if target.Enabled {
			enabled++
		} else {
			disabled++
		}
	}

	return map[string]int{
		"total":    len(s.targets),
		"enabled":  enabled,
		"disabled": disabled,
	}
}

// DiscoverTargets is a no-op for the stub (targets are set manually).
func (s *StubTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	// No-op: targets are managed manually for testing
	return nil
}

// AddTarget adds a target to the stub manager (for testing).
func (s *StubTargetDiscoveryManager) AddTarget(target *core.PublishingTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.targets = append(s.targets, target)
	s.logger.Debug("Stub: Added target", "name", target.Name, "enabled", target.Enabled)
}

// RemoveTarget removes a target from the stub manager (for testing).
func (s *StubTargetDiscoveryManager) RemoveTarget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, target := range s.targets {
		if target.Name == name {
			s.targets = append(s.targets[:i], s.targets[i+1:]...)
			s.logger.Debug("Stub: Removed target", "name", name)
			return
		}
	}
}

// ClearTargets removes all targets (for testing metrics-only mode).
func (s *StubTargetDiscoveryManager) ClearTargets() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.targets = make([]*core.PublishingTarget, 0)
	s.logger.Debug("Stub: Cleared all targets")
}

// SetTargets replaces the entire target list (for testing).
func (s *StubTargetDiscoveryManager) SetTargets(targets []*core.PublishingTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.targets = targets
	s.logger.Debug("Stub: Set targets", "count", len(targets))
}
