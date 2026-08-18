package application

import (
	"sync"

	businessrouting "github.com/ipiton/AMP/internal/business/routing"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
)

// routingMatcherMetricsOnce / routingEvaluatorMetricsOnce build the
// process-wide RouteMatcher / RouteEvaluator Prometheus metrics exactly
// once, no matter how many times initializeRouting runs in this process
// (production: once per process; tests: once per test *binary* — many
// ServiceRegistry instances across many test functions share the same
// metrics objects and default registry, see route_evaluator_test.go).
//
// This is what makes EnableMetrics:true safe again (fixes the follow-up
// flagged in task 1.4): each of NewMatcherMetrics/NewEvaluatorMetrics calls
// promauto against the default Prometheus registry, which panics on double
// registration. Calling them through sync.OnceValue guarantees that
// registration happens at most once; every RouteMatcher/RouteEvaluator
// constructed afterwards (including the fresh RouteEvaluator
// routeTreeEvaluator.Evaluate builds per call, and any RouteMatcher/
// RouteEvaluator built by a later initializeRouting call, e.g. on retry)
// is injected with the SAME metrics instance via MatcherOptions.Metrics /
// EvaluatorOptions.Metrics, so counts simply keep accumulating instead of
// re-registering.
var (
	routingMatcherMetricsOnce   = sync.OnceValue(businessrouting.NewMatcherMetrics)
	routingEvaluatorMetricsOnce = sync.OnceValue(businessrouting.NewEvaluatorMetrics)
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
// Metrics: business/routing.RouteEvaluator's metrics (EvaluatorMetrics)
// register via promauto against the default Prometheus registry, which
// panics on double-registration — and this adapter builds a fresh
// RouteEvaluator on every single Evaluate call (see Evaluate below for why
// that's cheap), so constructing metrics inside that per-call
// NewRouteEvaluator would panic on the second alert. newRouteTreeEvaluator
// avoids that by accepting an already-built *EvaluatorMetrics (see
// routingEvaluatorMetricsOnce, built exactly once per process) and stashing
// it in e.opts.Metrics: every per-call RouteEvaluator reuses that SAME
// metrics instance via EvaluatorOptions.Metrics, so promauto registration
// happens once while counts still increment on every call. A tree swap
// (hot reload, RouteTreeManager.Reload) only replaces the tree the manager
// hands back from GetTree() — it never touches e.opts or its Metrics
// pointer, so metrics keep counting across reloads with no re-registration.
type routeTreeEvaluator struct {
	manager *businessrouting.RouteTreeManager
	matcher *businessrouting.RouteMatcher
	opts    businessrouting.EvaluatorOptions
}

// newRouteTreeEvaluator creates a routeTreeEvaluator wrapping the given
// manager and matcher. matcher may be shared/long-lived (it is
// tree-independent); manager owns the hot-reloadable tree.
//
// metrics is optional: pass nil to disable metrics entirely (e.g. ad-hoc
// test construction that doesn't care about observability); pass an
// *EvaluatorMetrics built once per process (routingEvaluatorMetricsOnce in
// production) to enable them without risking promauto double-registration.
func newRouteTreeEvaluator(manager *businessrouting.RouteTreeManager, matcher *businessrouting.RouteMatcher, metrics *businessrouting.EvaluatorMetrics) *routeTreeEvaluator {
	opts := businessrouting.DefaultEvaluatorOptions()
	opts.EnableMetrics = metrics != nil
	opts.Metrics = metrics
	return &routeTreeEvaluator{manager: manager, matcher: matcher, opts: opts}
}

// Evaluate implements services.RouteEvaluator.
//
// Rebuilding a business/routing.RouteEvaluator on every call is a cheap
// pointer-wrap, not real work: NewRouteEvaluator just assigns e.tree/
// e.matcher/e.opts on a new struct. When metrics are enabled, it does NOT
// construct a new *EvaluatorMetrics (promauto, panics on double
// registration) — opts.Metrics already holds the one shared instance, so
// NewRouteEvaluator just copies that pointer onto the new struct (see
// EvaluatorOptions.Metrics). It does NOT rebuild the tree (manager.GetTree()
// is an atomic.Value load of the already-built tree) and does NOT
// recompile any regexes (matcher owns its own long-lived RegexCache across
// calls, untouched by this rebuild). This is what makes per-call
// reconstruction safe to do on every single alert.
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
