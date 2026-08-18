package publishing

import (
	"context"

	"github.com/ipiton/AMP/internal/core"
)

// TargetDiscoveryManager is the publishing side's read-only view of the
// discovered publishing targets.
//
// The live implementation is NOT in this package: targets are discovered from
// Kubernetes Secrets by businesspublishing.DefaultTargetDiscoveryManager and
// reach this package through application.DiscoveryAdapter, which implements
// this interface. In-process consumers are ParallelAlertPublisher (target
// enumeration) and the dashboard health handler; StubTargetDiscoveryManager
// serves tests.
//
// Final review finding 14: this file also carried a SECOND, fully dead
// implementation of the same interface — a `DefaultTargetDiscoveryManager` with
// its own K8s client, TargetDiscoveryConfig and secret parser, and zero
// constructors anywhere in the codebase. It shared its type name with the live
// business-layer one, so a reader (or a future wiring change) could easily
// reach for the wrong one and get a manager that discovers nothing. Deleted; if
// discovery ever needs to move into this package, do it by moving the live
// implementation rather than by reviving a parallel copy.
type TargetDiscoveryManager interface {
	// DiscoverTargets discovers all publishing targets from K8s secrets
	DiscoverTargets(ctx context.Context) error

	// GetTarget returns a specific target by name
	GetTarget(name string) (*core.PublishingTarget, error)

	// ListTargets returns all discovered targets
	ListTargets() []*core.PublishingTarget

	// GetTargetsByType returns targets filtered by type
	GetTargetsByType(targetType string) []*core.PublishingTarget

	// GetTargetCount returns the number of discovered targets
	GetTargetCount() int
}
