package routing

// Task 3.2 (alertmanager-parity): mute_time_intervals/active_time_intervals
// flow from grouping.Route (parsed config, task 3.1) through RouteNode,
// RouteTree.GetTimeInterval, and RoutingDecision. The central requirement
// under test here is non-inheritance: unlike GroupBy/timings, these two
// fields apply ONLY to the route node that declares them.

import (
	"testing"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
)

// buildTimeMuteFixtureConfig returns a RouteConfig where only one child
// route declares mute_time_intervals/active_time_intervals; its sibling and
// the root declare neither.
func buildTimeMuteFixtureConfig() *infraroute.RouteConfig {
	mutedChild := &grouping.Route{
		Receiver:            "muted-receiver",
		Match:               map[string]string{"team": "sre"},
		MuteTimeIntervals:   []string{"weekends"},
		ActiveTimeIntervals: []string{"business-hours"},
	}
	plainChild := &grouping.Route{
		Receiver: "plain-receiver",
		Match:    map[string]string{"team": "other"},
	}
	root := &grouping.Route{
		Receiver: "default",
		Routes:   []*grouping.Route{mutedChild, plainChild},
	}

	receivers := []*infraroute.Receiver{
		{Name: "default", WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/default"}}},
		{Name: "muted-receiver", WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/muted"}}},
		{Name: "plain-receiver", WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/plain"}}},
	}

	return &infraroute.RouteConfig{
		Route:     root,
		Receivers: receivers,
		TimeIntervalIndex: map[string]timeinterval.TimeInterval{
			"weekends": {
				Name: "weekends",
				TimeIntervals: []timeinterval.Interval{
					{Weekdays: []timeinterval.WeekdayRange{{Begin: 0, End: 6}}},
				},
			},
			"business-hours": {Name: "business-hours"},
		},
	}
}

func TestTreeBuilder_TimeIntervalNames_NotInherited(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	root := tree.Root
	if len(root.MuteTimeIntervals) != 0 || len(root.ActiveTimeIntervals) != 0 {
		t.Fatalf("root must not inherit a child's time intervals, got mute=%v active=%v",
			root.MuteTimeIntervals, root.ActiveTimeIntervals)
	}

	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	mutedNode, plainNode := root.Children[0], root.Children[1]

	if got := mutedNode.MuteTimeIntervals; len(got) != 1 || got[0] != "weekends" {
		t.Fatalf("mutedNode.MuteTimeIntervals = %v, want [weekends]", got)
	}
	if got := mutedNode.ActiveTimeIntervals; len(got) != 1 || got[0] != "business-hours" {
		t.Fatalf("mutedNode.ActiveTimeIntervals = %v, want [business-hours]", got)
	}

	if len(plainNode.MuteTimeIntervals) != 0 || len(plainNode.ActiveTimeIntervals) != 0 {
		t.Fatalf("sibling route must not inherit another route's time intervals, got mute=%v active=%v",
			plainNode.MuteTimeIntervals, plainNode.ActiveTimeIntervals)
	}
}

func TestRouteNode_Clone_PreservesTimeIntervalNames(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	cloneRoot := tree.Root.Clone()
	mutedClone := cloneRoot.Children[0]
	if len(mutedClone.MuteTimeIntervals) != 1 || mutedClone.MuteTimeIntervals[0] != "weekends" {
		t.Fatalf("cloned node lost MuteTimeIntervals: %v", mutedClone.MuteTimeIntervals)
	}
	if len(mutedClone.ActiveTimeIntervals) != 1 || mutedClone.ActiveTimeIntervals[0] != "business-hours" {
		t.Fatalf("cloned node lost ActiveTimeIntervals: %v", mutedClone.ActiveTimeIntervals)
	}
}

func TestRouteTree_GetTimeInterval(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	ti, ok := tree.GetTimeInterval("weekends")
	if !ok || ti.Name != "weekends" {
		t.Fatalf("GetTimeInterval(weekends) = %+v, ok=%v", ti, ok)
	}

	if _, ok := tree.GetTimeInterval("does-not-exist"); ok {
		t.Fatal("GetTimeInterval for an undefined name must report ok=false, not panic or fabricate a value")
	}
}

func TestRouteTree_GetTimeInterval_NilIndexIsSafe(t *testing.T) {
	// A config with no time_intervals: section at all (TimeIntervalIndex
	// stays nil) must not panic on lookup — it must just report ok=false.
	config := &infraroute.RouteConfig{
		Route:     &grouping.Route{Receiver: "default"},
		Receivers: []*infraroute.Receiver{{Name: "default"}},
	}
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if _, ok := tree.GetTimeInterval("anything"); ok {
		t.Fatal("expected ok=false against a nil time-interval index")
	}
}

func TestRouteTree_Clone_PreservesTimeIntervalIndex(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	clone := tree.Clone()
	if _, ok := clone.GetTimeInterval("weekends"); !ok {
		t.Fatal("Clone() must preserve the time interval index")
	}
}

// testEvaluatorOptions mirrors testMatcher's metrics-disabled posture
// (matcher_test.go): promauto is backed by the global registry, so
// constructing many metrics-enabled evaluators in one test binary panics
// on double registration.
func testEvaluatorOptions() EvaluatorOptions {
	opts := DefaultEvaluatorOptions()
	opts.EnableMetrics = false
	return opts
}

func TestRouteEvaluator_Evaluate_CarriesTimeIntervalNamesFromMatchedNodeOnly(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	evaluator := NewRouteEvaluator(tree, testMatcher(), testEvaluatorOptions())

	decision, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"team": "sre"}})
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if got := decision.MuteTimeIntervals; len(got) != 1 || got[0] != "weekends" {
		t.Fatalf("decision.MuteTimeIntervals = %v, want [weekends]", got)
	}
	if got := decision.ActiveTimeIntervals; len(got) != 1 || got[0] != "business-hours" {
		t.Fatalf("decision.ActiveTimeIntervals = %v, want [business-hours]", got)
	}

	// The sibling route (team=other) declares none of its own and must not
	// pick up the muted route's names.
	plainDecision, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"team": "other"}})
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if len(plainDecision.MuteTimeIntervals) != 0 || len(plainDecision.ActiveTimeIntervals) != 0 {
		t.Fatalf("plain route decision must carry no time intervals, got mute=%v active=%v",
			plainDecision.MuteTimeIntervals, plainDecision.ActiveTimeIntervals)
	}

	// No child matches: falls back to root, which declares none either.
	rootDecision, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"team": "unknown"}})
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if len(rootDecision.MuteTimeIntervals) != 0 || len(rootDecision.ActiveTimeIntervals) != 0 {
		t.Fatalf("root fallback decision must carry no time intervals, got mute=%v active=%v",
			rootDecision.MuteTimeIntervals, rootDecision.ActiveTimeIntervals)
	}
}

func TestRouteTreeManager_Reload_TimeIntervalDefinitionSwapsOnNextGetTree(t *testing.T) {
	config := buildTimeMuteFixtureConfig()
	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	manager, err := NewRouteTreeManager(tree)
	if err != nil {
		t.Fatalf("NewRouteTreeManager() error: %v", err)
	}

	if _, ok := manager.GetTree().GetTimeInterval("weekends"); !ok {
		t.Fatal("expected initial tree to define 'weekends'")
	}

	// Hot-reload with a config that redefines "weekends" under a
	// completely different name set (renamed away) — simulates a config
	// change removing the interval the matched route still references by
	// name.
	newConfig := buildTimeMuteFixtureConfig()
	newConfig.TimeIntervalIndex = map[string]timeinterval.TimeInterval{
		"renamed-weekends": {Name: "renamed-weekends"},
	}

	if err := manager.Reload(newConfig); err != nil {
		t.Fatalf("Reload() error: %v", err)
	}

	if _, ok := manager.GetTree().GetTimeInterval("weekends"); ok {
		t.Fatal("after reload, the old 'weekends' name must no longer resolve")
	}
	if _, ok := manager.GetTree().GetTimeInterval("renamed-weekends"); !ok {
		t.Fatal("after reload, GetTree() must reflect the newly reloaded time-interval index")
	}
}
