package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixtures --------------------------------------------------------------

func groupingDuration(d time.Duration) *grouping.Duration {
	return &grouping.Duration{Duration: d}
}

// buildFixtureRouteConfig returns a small but non-trivial route tree:
// root -> default receiver, group_wait 10s; one child matching
// severity=critical -> "critical-pagerduty" receiver with its own group_by.
// Mirrors internal/business/routing/tree_builder_test.go's fixture style
// (task 1.1/1.2 regression tests) so this task reuses the same conventions.
func buildFixtureRouteConfig() *infraroute.RouteConfig {
	criticalChild := &grouping.Route{
		Receiver: "critical-pagerduty",
		Match:    map[string]string{"severity": "critical"},
		GroupBy:  []string{"alertname", "namespace"},
	}

	root := &grouping.Route{
		Receiver:  "default-webhook",
		GroupBy:   []string{"alertname"},
		GroupWait: groupingDuration(10 * time.Second),
		Routes:    []*grouping.Route{criticalChild},
	}

	receivers := []*infraroute.Receiver{
		{Name: "default-webhook"},
		{Name: "critical-pagerduty"},
	}

	return &infraroute.RouteConfig{Route: root, Receivers: receivers}
}

// buildAlternateFixtureRouteConfig returns a differently-shaped tree (single
// root, no children, different receiver/group_wait) used to prove hot
// reload actually swaps behavior rather than reusing the old tree.
func buildAlternateFixtureRouteConfig() *infraroute.RouteConfig {
	root := &grouping.Route{
		Receiver:  "reloaded-receiver",
		GroupBy:   []string{"cluster"},
		GroupWait: groupingDuration(99 * time.Second),
	}
	receivers := []*infraroute.Receiver{{Name: "reloaded-receiver"}}
	return &infraroute.RouteConfig{Route: root, Receivers: receivers}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- initializeRouting -----------------------------------------------------

func TestInitializeRouting_SkipsCleanlyWhenAbsent(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{}, // Routing left nil: lite/legacy mode
		logger: testLogger(),
	}

	err := registry.initializeRouting(context.Background())
	require.NoError(t, err)

	assert.Nil(t, registry.routeTreeManager)
	assert.Nil(t, registry.routeEvaluator)
}

func TestInitializeRouting_WiresTreeManagerAndEvaluatorWhenPresent(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}

	err := registry.initializeRouting(context.Background())
	require.NoError(t, err)

	require.NotNil(t, registry.routeTreeManager)
	require.NotNil(t, registry.routeEvaluator)

	decision, err := registry.routeEvaluator.Evaluate(map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
	})
	require.NoError(t, err)
	assert.Equal(t, "critical-pagerduty", decision.Receiver)
	assert.Equal(t, []string{"alertname", "namespace"}, decision.GroupBy)
}

func TestInitializeRouting_BuildFailurePropagatesAsError(t *testing.T) {
	// A RouteConfig with no root Route fails TreeBuilder.Build (see
	// business/routing.TreeBuilder.Build: "config has no root route").
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: &infraroute.RouteConfig{Route: nil}},
		logger: testLogger(),
	}

	err := registry.initializeRouting(context.Background())
	require.Error(t, err)
	assert.Nil(t, registry.routeTreeManager)
	assert.Nil(t, registry.routeEvaluator)
}

// --- routeTreeEvaluator: fixture-tree decisions -----------------------------

func TestRouteTreeEvaluator_ProducesExpectedDecisionForFixtureTree(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	t.Run("matches critical child route", func(t *testing.T) {
		decision, err := registry.routeEvaluator.Evaluate(map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		})
		require.NoError(t, err)
		assert.Equal(t, "critical-pagerduty", decision.Receiver)
		assert.Equal(t, []string{"alertname", "namespace"}, decision.GroupBy)
		// Child leaves group_wait unset: inherited from root.
		assert.Equal(t, 10*time.Second, decision.GroupWait)
	})

	t.Run("falls back to root default", func(t *testing.T) {
		decision, err := registry.routeEvaluator.Evaluate(map[string]string{
			"alertname": "LowDisk",
			"severity":  "warning",
		})
		require.NoError(t, err)
		assert.Equal(t, "default-webhook", decision.Receiver)
		assert.Equal(t, []string{"alertname"}, decision.GroupBy)
		assert.Equal(t, 10*time.Second, decision.GroupWait)
	})
}

// --- hot reload --------------------------------------------------------------

func TestReloadRoutingTree_SwapsTreeAtomically(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	// Before reload: old tree answers.
	before, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	assert.Equal(t, "critical-pagerduty", before.Receiver)

	// Swap in a differently-shaped config and reload.
	registry.config.Routing = buildAlternateFixtureRouteConfig()
	require.NoError(t, registry.reloadRoutingTree())

	// After reload: new tree answers for every alert (single root receiver),
	// including the label set that used to match the old critical child.
	after, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	assert.Equal(t, "reloaded-receiver", after.Receiver)
	assert.Equal(t, []string{"cluster"}, after.GroupBy)
	assert.Equal(t, 99*time.Second, after.GroupWait)
}

func TestReloadRoutingTree_ErrorPropagatesAndKeepsOldTree(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	// Malformed reload config: no root route -> RouteTreeManager.Reload's
	// TreeBuilder.Build fails (task 1.4: "Reload errors must propagate").
	registry.config.Routing = &infraroute.RouteConfig{Route: nil}

	err := registry.reloadRoutingTree()
	require.Error(t, err)

	// Old tree must still answer: the manager keeps the last-known-good
	// tree on a failed Reload.
	decision, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	assert.Equal(t, "critical-pagerduty", decision.Receiver)
}

func TestReloadRoutingTree_NoManagerConfigured_NoOp(t *testing.T) {
	// routeTreeManager nil (never initialized: lite/legacy mode at
	// startup). A later config carrying a route tree without a restart is a
	// known limitation — reloadRoutingTree must warn, not panic or error.
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}

	err := registry.reloadRoutingTree()
	require.NoError(t, err)
	assert.Nil(t, registry.routeTreeManager)
}

func TestReloadRoutingTree_RouteSectionRemoved_NoOp(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	registry.config.Routing = nil // route: section removed at reload time

	err := registry.reloadRoutingTree()
	require.NoError(t, err)

	// Old tree keeps answering until restart.
	decision, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	assert.Equal(t, "critical-pagerduty", decision.Receiver)
}

// --- routeTreeTimeIntervalLookup (task 3.2) ---------------------------------

// buildFixtureRouteConfigWithTimeIntervals reuses buildFixtureRouteConfig's
// tree shape but adds a named time_intervals index, for the
// GroupTimeIntervalLookup adapter tests below.
func buildFixtureRouteConfigWithTimeIntervals() *infraroute.RouteConfig {
	config := buildFixtureRouteConfig()
	config.TimeIntervalIndex = map[string]timeinterval.TimeInterval{
		"weekends": {Name: "weekends"},
	}
	return config
}

func TestRouteTreeTimeIntervalLookup_ResolvesDefinedName(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfigWithTimeIntervals()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	lookup := newRouteTreeTimeIntervalLookup(registry.routeTreeManager)

	ti, ok := lookup.GetTimeInterval("weekends")
	require.True(t, ok)
	assert.Equal(t, "weekends", ti.Name)
}

func TestRouteTreeTimeIntervalLookup_UndefinedNameReturnsNotOk(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfigWithTimeIntervals()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	lookup := newRouteTreeTimeIntervalLookup(registry.routeTreeManager)

	_, ok := lookup.GetTimeInterval("does-not-exist")
	assert.False(t, ok)
}

// TestRouteTreeTimeIntervalLookup_HotReloadReflectsNewDefinition proves the
// adapter never caches: it re-reads manager.GetTree() on every single call,
// so a config hot-reload (RouteTreeManager.Reload) is visible on the very
// next GetTimeInterval call — mirroring routeTreeEvaluator's same posture
// for route decisions (TestReloadRoutingTree_SwapsTreeAtomically above).
func TestRouteTreeTimeIntervalLookup_HotReloadReflectsNewDefinition(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfigWithTimeIntervals()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	lookup := newRouteTreeTimeIntervalLookup(registry.routeTreeManager)

	_, ok := lookup.GetTimeInterval("weekends")
	require.True(t, ok, "initial config must define 'weekends'")

	reloaded := buildAlternateFixtureRouteConfig()
	reloaded.TimeIntervalIndex = map[string]timeinterval.TimeInterval{
		"renamed": {Name: "renamed"},
	}
	registry.config.Routing = reloaded
	require.NoError(t, registry.reloadRoutingTree())

	_, ok = lookup.GetTimeInterval("weekends")
	assert.False(t, ok, "after reload, the old name must no longer resolve")

	_, ok = lookup.GetTimeInterval("renamed")
	assert.True(t, ok, "after reload, the SAME lookup instance must see the new definition")
}

// --- routing metrics (follow-up to task 1.4's EnableMetrics:false blind
// spot) ------------------------------------------------------------------
//
// initializeRouting now injects routingMatcherMetricsOnce()/
// routingEvaluatorMetricsOnce() (route_evaluator.go) instead of leaving
// EnableMetrics off. Both are built via sync.OnceValue against the
// process-wide default Prometheus registry, so every test below shares
// them with every OTHER test in this file/binary that also calls
// initializeRouting — assertions here use deltas (before/after a specific
// Evaluate call) rather than absolute values, to stay correct regardless
// of test run order or `-run` filtering.

func TestRouteTreeEvaluator_MetricsIncrementOnEvaluate(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	adapter, ok := registry.routeEvaluator.(*routeTreeEvaluator)
	require.True(t, ok, "routeEvaluator must be a *routeTreeEvaluator")
	require.NotNil(t, adapter.opts.Metrics, "metrics must be injected by initializeRouting")

	before := testutil.ToFloat64(adapter.opts.Metrics.EvaluationsTotal.WithLabelValues("critical-pagerduty"))

	_, err := registry.routeEvaluator.Evaluate(map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
	})
	require.NoError(t, err)

	after := testutil.ToFloat64(adapter.opts.Metrics.EvaluationsTotal.WithLabelValues("critical-pagerduty"))
	assert.Greater(t, after, before, "EvaluationsTotal must increment on a real Evaluate call")
}

// TestReloadRoutingTree_MetricsSurviveReloadAndKeepCounting proves the two
// hot-reload guarantees the follow-up required: reloading the tree must
// not panic (it would, pre-fix, if metrics were constructed per reload),
// and the SAME metrics instance — not a fresh one — keeps counting after
// the swap.
func TestReloadRoutingTree_MetricsSurviveReloadAndKeepCounting(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	require.NoError(t, registry.initializeRouting(context.Background()))

	adapter := registry.routeEvaluator.(*routeTreeEvaluator)
	metricsBeforeReload := adapter.opts.Metrics
	require.NotNil(t, metricsBeforeReload)

	_, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)

	registry.config.Routing = buildAlternateFixtureRouteConfig()
	require.NoError(t, registry.reloadRoutingTree())

	// Tree swap must not touch the adapter's metrics: same instance, no
	// re-registration.
	assert.Same(t, metricsBeforeReload, adapter.opts.Metrics,
		"reload must not rebuild/replace the metrics instance")

	before := testutil.ToFloat64(metricsBeforeReload.EvaluationsTotal.WithLabelValues("reloaded-receiver"))

	decision, err := registry.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	require.Equal(t, "reloaded-receiver", decision.Receiver)

	after := testutil.ToFloat64(metricsBeforeReload.EvaluationsTotal.WithLabelValues("reloaded-receiver"))
	assert.Greater(t, after, before, "the same metrics instance must keep counting after reload")
}

// TestInitializeRouting_MultipleRegistries_ShareMetricsNoDoubleRegistrationPanic
// constructs and initializes TWO independent ServiceRegistry instances in
// one test (mirroring what already happens across this whole test file,
// just made explicit): pre-fix, giving both a metrics-enabled matcher/
// evaluator would panic on the second initializeRouting call because
// NewMatcherMetrics/NewEvaluatorMetrics promauto-register against the
// shared default registry. routingMatcherMetricsOnce/
// routingEvaluatorMetricsOnce (sync.OnceValue) make both registries share
// one metrics instance instead.
func TestInitializeRouting_MultipleRegistries_ShareMetricsNoDoubleRegistrationPanic(t *testing.T) {
	regA := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}
	regB := &ServiceRegistry{
		config: &appconfig.Config{Routing: buildFixtureRouteConfig()},
		logger: testLogger(),
	}

	require.NotPanics(t, func() {
		require.NoError(t, regA.initializeRouting(context.Background()))
		require.NoError(t, regB.initializeRouting(context.Background()))
	})

	adapterA := regA.routeEvaluator.(*routeTreeEvaluator)
	adapterB := regB.routeEvaluator.(*routeTreeEvaluator)
	assert.Same(t, adapterA.opts.Metrics, adapterB.opts.Metrics,
		"both registries must share the single process-wide EvaluatorMetrics instance")

	_, err := regA.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
	_, err = regB.routeEvaluator.Evaluate(map[string]string{"severity": "critical"})
	require.NoError(t, err)
}

// TestRoutingMetricsSingletons_ReturnSameInstanceAcrossCalls is a direct
// proof that routingMatcherMetricsOnce/routingEvaluatorMetricsOnce
// construct their promauto-registered metrics exactly once: repeated calls
// return the identical pointer, so NewMatcherMetrics()/NewEvaluatorMetrics()
// (and therefore promauto registration) run at most once per process.
func TestRoutingMetricsSingletons_ReturnSameInstanceAcrossCalls(t *testing.T) {
	m1 := routingMatcherMetricsOnce()
	m2 := routingMatcherMetricsOnce()
	assert.Same(t, m1, m2)

	e1 := routingEvaluatorMetricsOnce()
	e2 := routingEvaluatorMetricsOnce()
	assert.Same(t, e1, e2)
}
