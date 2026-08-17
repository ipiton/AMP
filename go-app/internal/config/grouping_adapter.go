package config

import (
	"errors"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// ErrGroupingRequiresRouteTree is returned by BuildGroupingConfig when
// grouping is enabled but no `route:` tree was loaded (Config.Routing ==
// nil). The grouping package has no configuration of its own for
// group_by/group_wait/group_interval/repeat_interval — GroupingConfig (this
// file's Config.Grouping) only toggles the subsystem — so there is nothing
// to feed grouping.GroupingConfig.Route without a route tree.
var ErrGroupingRequiresRouteTree = errors.New("grouping.enabled requires a route: tree to be configured (see routing config)")

// BuildGroupingConfig builds a grouping.GroupingConfig for the grouping
// subsystem (task 2.2, alertmanager-parity) from the app's route tree.
//
// infraroute.RouteConfig.Route (Config.Routing.Route) already IS a
// *grouping.Route — RouteConfig inherits it directly from TN-121 for
// backward compatibility — so this adapter reuses the pointer as-is instead
// of re-mapping/duplicating group_by/group_wait/group_interval/
// repeat_interval into a second copy.
//
// Returns ErrGroupingRequiresRouteTree if Config.Routing is nil (no `route:`
// section loaded). Callers (ServiceRegistry.initializeGrouping) treat this
// as a clean skip, not a fatal error — mirrors HasRouteTree()'s treatment of
// the same absent-route-tree case for the routing engine.
func (c *Config) BuildGroupingConfig() (*grouping.GroupingConfig, error) {
	if c.Routing == nil {
		return nil, ErrGroupingRequiresRouteTree
	}
	return &grouping.GroupingConfig{Route: c.Routing.Route}, nil
}
