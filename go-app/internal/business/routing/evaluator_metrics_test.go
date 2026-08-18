package routing

// Tests for RouteEvaluator metrics injection (alertmanager-parity
// follow-up to task 1.4: EnableMetrics was hardcoded off for
// RouteMatcher/RouteEvaluator because NewEvaluatorMetrics/NewMatcherMetrics
// promauto-register against the default Prometheus registry, which panics
// on double registration, and application.routeTreeEvaluator.Evaluate
// builds a fresh RouteEvaluator on every single call).
//
// EvaluatorOptions.Metrics (and MatcherOptions.Metrics) let a caller build
// the metrics ONCE and inject the same instance into every RouteEvaluator/
// RouteMatcher it constructs, so promauto registration happens once while
// the shared counters keep incrementing on every call — see
// application/route_evaluator.go's routingEvaluatorMetricsOnce /
// routingMatcherMetricsOnce for the production wiring.

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRouteEvaluator_InjectedMetrics_RecordOnCustomRegistry proves that
// an EvaluatorMetrics instance built against a private (non-default)
// registry and injected via EvaluatorOptions.Metrics actually records real
// counts on Evaluate — this is the restored observability, not just an
// absence of panics.
func TestNewRouteEvaluator_InjectedMetrics_RecordOnCustomRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewEvaluatorMetricsWithRegisterer(reg)

	tree := buildAlertmanagerDocsFixtureTree()
	matcher := testMatcher()
	opts := EvaluatorOptions{
		EnableMetrics:  true,
		FallbackToRoot: true,
		Metrics:        metrics,
	}
	evaluator := NewRouteEvaluator(tree, matcher, opts)

	before := testutil.ToFloat64(metrics.EvaluationsTotal.WithLabelValues("team-Z-pager"))

	decision, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"owner": "team-Z"}})
	require.NoError(t, err)
	require.Equal(t, "team-Z-pager", decision.Receiver)

	after := testutil.ToFloat64(metrics.EvaluationsTotal.WithLabelValues("team-Z-pager"))
	assert.Greater(t, after, before, "EvaluationsTotal must increment for the matched receiver")
}

// TestNewRouteEvaluator_InjectedMetrics_SharedAcrossInstances_NoDoubleRegistrationPanic
// mirrors what application.routeTreeEvaluator.Evaluate does on every single
// alert: build a *fresh* RouteEvaluator (cheap pointer-wrap) reusing the
// SAME injected metrics instance. Metrics are only constructed once
// (outside the loop); constructing N RouteEvaluators against that one
// instance must never panic, and every one of them must still contribute
// to the shared counters.
func TestNewRouteEvaluator_InjectedMetrics_SharedAcrossInstances_NoDoubleRegistrationPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewEvaluatorMetricsWithRegisterer(reg)

	tree := buildAlertmanagerDocsFixtureTree()
	matcher := testMatcher()
	opts := EvaluatorOptions{
		EnableMetrics:  true,
		FallbackToRoot: true,
		Metrics:        metrics,
	}

	const calls = 5
	for i := 0; i < calls; i++ {
		evaluator := NewRouteEvaluator(tree, matcher, opts)
		_, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"owner": "team-Z"}})
		require.NoError(t, err)
	}

	assert.Equal(t, float64(calls),
		testutil.ToFloat64(metrics.EvaluationsTotal.WithLabelValues("team-Z-pager")))
}

// TestNewEvaluatorMetricsWithRegisterer_SameRegistererTwice_Panics proves
// the double-registration failure mode this whole design works around: two
// independently-constructed EvaluatorMetrics against the SAME registerer
// collide on their metric names.
func TestNewEvaluatorMetricsWithRegisterer_SameRegistererTwice_Panics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewEvaluatorMetricsWithRegisterer(reg)

	assert.Panics(t, func() {
		NewEvaluatorMetricsWithRegisterer(reg)
	})
}

// TestNewMatcherMetricsWithRegisterer_InjectedIntoMatcher_RecordsMatches is
// the RouteMatcher-side counterpart: MatcherOptions.Metrics lets a matcher
// built against a custom registry record real match counts.
func TestNewMatcherMetricsWithRegisterer_InjectedIntoMatcher_RecordsMatches(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMatcherMetricsWithRegisterer(reg)

	matcher := NewRouteMatcher(nil, MatcherOptions{
		EnableMetrics: true,
		CacheSize:     100,
		Metrics:       metrics,
	})
	tree := buildAlertmanagerDocsFixtureTree()

	// buildAlertmanagerDocsFixtureTree's nodes leave Path unset ("");
	// MatchesTotal is keyed by that node.Path, so the matched node's
	// samples land under the "" route_path label.
	before := testutil.ToFloat64(metrics.MatchesTotal.WithLabelValues(""))

	result := matcher.FindMatchingRoutes(tree, &Alert{Labels: map[string]string{"owner": "team-Z"}})
	require.False(t, result.Empty())
	require.Equal(t, "team-Z-pager", result.First().Receiver)

	after := testutil.ToFloat64(metrics.MatchesTotal.WithLabelValues(""))
	assert.Greater(t, after, before, "MatchesTotal must increment on a real match")
}
