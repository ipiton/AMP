package application

import (
	"context"
	"fmt"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// DiscoveryAdapter adapts business discovery to infrastructure delivery primitives.
type DiscoveryAdapter struct {
	manager businesspublishing.TargetDiscoveryManager
}

// NewDiscoveryAdapter creates an infrastructure-compatible discovery adapter.
func NewDiscoveryAdapter(manager businesspublishing.TargetDiscoveryManager) (*DiscoveryAdapter, error) {
	if manager == nil {
		return nil, fmt.Errorf("publishing discovery manager is required")
	}

	return &DiscoveryAdapter{manager: manager}, nil
}

var _ infrapublishing.TargetDiscoveryManager = (*DiscoveryAdapter)(nil)

func (a *DiscoveryAdapter) DiscoverTargets(ctx context.Context) error {
	return a.manager.DiscoverTargets(ctx)
}

func (a *DiscoveryAdapter) GetTarget(name string) (*core.PublishingTarget, error) {
	return a.manager.GetTarget(name)
}

func (a *DiscoveryAdapter) ListTargets() []*core.PublishingTarget {
	return a.manager.ListTargets()
}

func (a *DiscoveryAdapter) GetTargetsByType(targetType string) []*core.PublishingTarget {
	return a.manager.GetTargetsByType(targetType)
}

func (a *DiscoveryAdapter) GetTargetCount() int {
	return len(a.manager.ListTargets())
}
