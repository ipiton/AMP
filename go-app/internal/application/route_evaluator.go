package application

import (
	businessrouting "github.com/ipiton/AMP/internal/business/routing"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
)

// routeTreeEvaluator adapts a hot-reloadable business/routing route tree
// (RouteTreeManager + RouteMatcher) into the core/services.RouteEvaluator
// interface consumed by AlertProcessor (task 1.4: alertmanager-parity
// route wiring).
//
// Hot reload: RouteTreeManager.Reload (invoked from
// ServiceRegistry.reloadRoutingTree, called by ReloadConfig) atomically
// swaps the manager's tree. This adapter re-reads manager.GetTree() on
// every Evaluate call, so a reload takes effect on the very next alert
// with no extra wiring — there is nothing to "refresh" on the AlertProcessor
// side.
//
// Metrics note: business/routing.RouteEvaluator's metrics
// (EvaluatorMetrics) are registered via promauto against the default
// Prometheus registry, which panics on double-registration. This adapter
// builds a fresh RouteEvaluator on every single Evaluate call — see
// Evaluate below for why that's cheap — so EnableMetrics is forced off
// (opts.EnableMetrics = false in newRouteTreeEvaluator): constructing
// EvaluatorMetrics once per alert would panic on the second alert.
// Structured logging in AlertProcessor.evaluateRoute covers observability
// for task 1.4. Restoring per-evaluation Prometheus metrics is tracked as
// a follow-up: it needs a custom (non-default) registry scoped to this
// adapter's lifetime, not promauto's global one — out of scope here.
type routeTreeEvaluator struct {
	manager *businessrouting.RouteTreeManager
	matcher *businessrouting.RouteMatcher
	opts    businessrouting.EvaluatorOptions
}

// newRouteTreeEvaluator creates a routeTreeEvaluator wrapping the given
// manager and matcher. matcher may be shared/long-lived (it is
// tree-independent); manager owns the hot-reloadable tree.
func newRouteTreeEvaluator(manager *businessrouting.RouteTreeManager, matcher *businessrouting.RouteMatcher) *routeTreeEvaluator {
	opts := businessrouting.DefaultEvaluatorOptions()
	opts.EnableMetrics = false
	return &routeTreeEvaluator{manager: manager, matcher: matcher, opts: opts}
}

// Evaluate implements services.RouteEvaluator.
//
// Rebuilding a business/routing.RouteEvaluator on every call is a cheap
// pointer-wrap, not real work: NewRouteEvaluator just assigns e.tree/
// e.matcher/e.opts on a new struct (with EnableMetrics: false, it doesn't
// even touch promauto — see the type doc above). It does NOT rebuild the
// tree (manager.GetTree() is an atomic.Value load of the already-built
// tree) and does NOT recompile any regexes (matcher owns its own
// long-lived RegexCache across calls, untouched by this rebuild). This is
// what makes per-call reconstruction safe to do on every single alert.
func (e *routeTreeEvaluator) Evaluate(labels map[string]string) (*services.RoutingDecision, error) {
	tree := e.manager.GetTree()
	evaluator := businessrouting.NewRouteEvaluator(tree, e.matcher, e.opts)

	decision, err := evaluator.Evaluate(&businessrouting.Alert{Labels: labels})
	if err != nil {
		return nil, err
	}

	return &services.RoutingDecision{
		Receiver:            decision.Receiver,
		GroupBy:             decision.GroupBy,
		GroupWait:           decision.GroupWait,
		GroupInterval:       decision.GroupInterval,
		RepeatInterval:      decision.RepeatInterval,
		MuteTimeIntervals:   decision.MuteTimeIntervals,
		ActiveTimeIntervals: decision.ActiveTimeIntervals,
		MatchedRoute:        decision.MatchedRoute,
	}, nil
}

var _ services.RouteEvaluator = (*routeTreeEvaluator)(nil)

// routeTreeTimeIntervalLookup adapts the hot-reloadable RouteTreeManager
// into grouping.GroupTimeIntervalLookup (task 3.2) — the notify-chain's
// TimeMute step's send-time interval-definition read path.
//
// Hot reload: like routeTreeEvaluator above, this re-reads
// manager.GetTree() on every single GetTimeInterval call rather than
// caching the tree, so a RouteTreeManager.Reload (config hot reload) takes
// effect on the very next group-timer fire that calls it — never a
// construction-time snapshot.
type routeTreeTimeIntervalLookup struct {
	manager *businessrouting.RouteTreeManager
}

// newRouteTreeTimeIntervalLookup creates a routeTreeTimeIntervalLookup
// wrapping manager.
func newRouteTreeTimeIntervalLookup(manager *businessrouting.RouteTreeManager) *routeTreeTimeIntervalLookup {
	return &routeTreeTimeIntervalLookup{manager: manager}
}

// GetTimeInterval implements grouping.GroupTimeIntervalLookup.
func (l *routeTreeTimeIntervalLookup) GetTimeInterval(name string) (timeinterval.TimeInterval, bool) {
	return l.manager.GetTree().GetTimeInterval(name)
}

var _ grouping.GroupTimeIntervalLookup = (*routeTreeTimeIntervalLookup)(nil)
