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
