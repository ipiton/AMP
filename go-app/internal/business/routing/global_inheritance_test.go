package routing

// alertmanager-parity wave-5 item FU-GLOB-DEFAULT-VALUES.
//
// infraroute.GlobalConfig.GroupBy/GroupWait/GroupInterval/RepeatInterval
// were dropped by the TN-137 dedup (3f8d69d) when this package's local
// GlobalConfig (which had them) was deleted in favor of the canonical
// infrastructure/routing type (which didn't). This restores them as a
// fallback layer inheritGroupBy/inheritDuration consult BELOW parent-route
// inheritance and ABOVE the hardcoded upstream defaults — see
// infraroute.GlobalConfig's doc comment for why this is an AMP-only
// convenience, not literally upstream's `global:` schema.

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

func routingDuration(d time.Duration) *infraroute.Duration {
	v := infraroute.Duration(d)
	return &v
}

// --- inheritGroupBy -------------------------------------------------------

func TestInheritGroupBy_RouteOwnValueWinsOverGlobal(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupBy: []string{"from-global"}},
	}}

	got := b.inheritGroupBy(nil, &grouping.Route{GroupBy: []string{"from-route"}})

	if len(got) != 1 || got[0] != "from-route" {
		t.Fatalf("inheritGroupBy() = %v, want [\"from-route\"] (route's own value must win)", got)
	}
}

func TestInheritGroupBy_ParentValueWinsOverGlobal(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupBy: []string{"from-global"}},
	}}
	parent := &RouteNode{GroupBy: []string{"from-parent"}}

	got := b.inheritGroupBy(parent, &grouping.Route{})

	if len(got) != 1 || got[0] != "from-parent" {
		t.Fatalf("inheritGroupBy() = %v, want [\"from-parent\"] (parent must win over global)", got)
	}
}

func TestInheritGroupBy_FallsBackToGlobalWhenRouteAndParentUnset(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupBy: []string{"team", "service"}},
	}}

	got := b.inheritGroupBy(nil, &grouping.Route{})

	if len(got) != 2 || got[0] != "team" || got[1] != "service" {
		t.Fatalf("inheritGroupBy() = %v, want [\"team\" \"service\"] from global.group_by", got)
	}
}

// Upstream's DefaultRouteOpts leaves GroupBy EMPTY: a route without group_by
// aggregates everything it matches into ONE group with an empty label set. AMP
// used to substitute ["alertname"] here, which produced one group per alertname
// and broke `TestUpstreamParity_AlertGroupsWithoutGroupByHaveEmptyLabels` the
// moment a config without group_by became loadable (the blackhole-receiver
// change in FU-RECEIVERS-INTEGRATION). This assertion is inverted deliberately:
// the old expectation encoded AMP-only behaviour, not upstream's.
func TestInheritGroupBy_NoDefaultWhenRouteParentAndGlobalUnset(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{}} // no Global at all

	got := b.inheritGroupBy(nil, &grouping.Route{})

	if len(got) != 0 {
		t.Fatalf("inheritGroupBy() = %v, want empty (upstream: no group_by = one group per receiver with empty labels)", got)
	}
}

// --- inheritDuration -------------------------------------------------------

func TestInheritDuration_RouteOwnValueWinsOverGlobal(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupWait: routingDuration(99 * time.Second)},
	}}

	got := b.inheritDuration(nil, 10*time.Second, "group_wait")

	if got != 10*time.Second {
		t.Fatalf("inheritDuration() = %v, want 10s (route's own value must win)", got)
	}
}

func TestInheritDuration_ParentValueWinsOverGlobal(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupWait: routingDuration(99 * time.Second)},
	}}
	parent := &RouteNode{GroupWait: 20 * time.Second}

	got := b.inheritDuration(parent, 0, "group_wait")

	if got != 20*time.Second {
		t.Fatalf("inheritDuration() = %v, want 20s (parent must win over global)", got)
	}
}

func TestInheritDuration_FallsBackToGlobalWhenNoRouteOrParentValue(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{GroupWait: routingDuration(90 * time.Second)},
	}}

	got := b.inheritDuration(nil, 0, "group_wait")

	if got != 90*time.Second {
		t.Fatalf("inheritDuration() = %v, want 90s from global.group_wait", got)
	}
}

func TestInheritDuration_FallsBackToHardcodedDefaultWhenGlobalAlsoUnset(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{}} // no Global at all

	if got := b.inheritDuration(nil, 0, "group_wait"); got != 30*time.Second {
		t.Fatalf("inheritDuration(group_wait) = %v, want 30s hardcoded default", got)
	}
	if got := b.inheritDuration(nil, 0, "group_interval"); got != 5*time.Minute {
		t.Fatalf("inheritDuration(group_interval) = %v, want 5m hardcoded default", got)
	}
	if got := b.inheritDuration(nil, 0, "repeat_interval"); got != 4*time.Hour {
		t.Fatalf("inheritDuration(repeat_interval) = %v, want 4h hardcoded default", got)
	}
}

func TestInheritDuration_GroupIntervalAndRepeatIntervalAlsoFallBackToGlobal(t *testing.T) {
	b := &TreeBuilder{config: &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			GroupInterval:  routingDuration(2 * time.Minute),
			RepeatInterval: routingDuration(6 * time.Hour),
		},
	}}

	if got := b.inheritDuration(nil, 0, "group_interval"); got != 2*time.Minute {
		t.Fatalf("inheritDuration(group_interval) = %v, want 2m from global", got)
	}
	if got := b.inheritDuration(nil, 0, "repeat_interval"); got != 6*time.Hour {
		t.Fatalf("inheritDuration(repeat_interval) = %v, want 6h from global", got)
	}
}

func TestInheritDuration_NilConfigOrNilGlobalIsSafe(t *testing.T) {
	var b TreeBuilder // config is nil
	if got := b.inheritDuration(nil, 0, "group_wait"); got != 30*time.Second {
		t.Fatalf("inheritDuration() with nil config = %v, want the 30s hardcoded default, not a panic", got)
	}

	b2 := &TreeBuilder{config: &infraroute.RouteConfig{}} // Global is nil
	if got := b2.inheritDuration(nil, 0, "group_wait"); got != 30*time.Second {
		t.Fatalf("inheritDuration() with nil Global = %v, want the 30s hardcoded default, not a panic", got)
	}
}

// --- End-to-end via Build() -----------------------------------------------

// TestTreeBuilder_Build_RootInheritsUnsetDurationsAndGroupByFromGlobal is the
// brief's literal test ask: a route (here, the root — the only route with no
// parent to inherit from) that leaves group_wait (and friends) unset must
// inherit the global fallback, while a field it DOES set locally must win.
func TestTreeBuilder_Build_RootInheritsUnsetDurationsAndGroupByFromGlobal(t *testing.T) {
	root := &grouping.Route{
		Receiver: "default",
		// GroupBy/GroupInterval/RepeatInterval left unset: must come from Global.
		GroupWait: groupingDuration(15 * time.Second), // explicit: must win over Global
	}

	config := &infraroute.RouteConfig{
		Route: root,
		Global: &infraroute.GlobalConfig{
			GroupBy:        []string{"cluster", "alertname"},
			GroupWait:      routingDuration(99 * time.Second), // must lose to root's explicit 15s
			GroupInterval:  routingDuration(90 * time.Second),
			RepeatInterval: routingDuration(12 * time.Hour),
		},
		Receivers: []*infraroute.Receiver{{Name: "default"}},
	}

	builder := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false})
	tree, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	root2 := tree.Root
	if len(root2.GroupBy) != 2 || root2.GroupBy[0] != "cluster" || root2.GroupBy[1] != "alertname" {
		t.Fatalf("root.GroupBy = %v, want [\"cluster\" \"alertname\"] from global.group_by", root2.GroupBy)
	}
	if root2.GroupWait != 15*time.Second {
		t.Fatalf("root.GroupWait = %v, want 15s (the route's own explicit value must win over global)", root2.GroupWait)
	}
	if root2.GroupInterval != 90*time.Second {
		t.Fatalf("root.GroupInterval = %v, want 90s from global.group_interval", root2.GroupInterval)
	}
	if root2.RepeatInterval != 12*time.Hour {
		t.Fatalf("root.RepeatInterval = %v, want 12h from global.repeat_interval", root2.RepeatInterval)
	}
}

// --- GlobalConfig.Clone() --------------------------------------------------

func TestGlobalConfig_Clone_CopiesGroupingFallbackFields(t *testing.T) {
	original := &infraroute.GlobalConfig{
		GroupBy:        []string{"a", "b"},
		GroupWait:      routingDuration(1 * time.Second),
		GroupInterval:  routingDuration(2 * time.Second),
		RepeatInterval: routingDuration(3 * time.Second),
	}

	clone := original.Clone()

	if len(clone.GroupBy) != 2 || clone.GroupBy[0] != "a" || clone.GroupBy[1] != "b" {
		t.Fatalf("Clone().GroupBy = %v, want [\"a\" \"b\"]", clone.GroupBy)
	}
	// Mutating the clone's slice must not affect the original (deep copy).
	clone.GroupBy[0] = "mutated"
	if original.GroupBy[0] != "a" {
		t.Fatal("Clone() must deep-copy GroupBy, not alias the original slice")
	}

	if clone.GroupWait == nil || *clone.GroupWait != *original.GroupWait {
		t.Fatalf("Clone().GroupWait = %v, want a copy of %v", clone.GroupWait, original.GroupWait)
	}
	if clone.GroupInterval == nil || *clone.GroupInterval != *original.GroupInterval {
		t.Fatalf("Clone().GroupInterval = %v, want a copy of %v", clone.GroupInterval, original.GroupInterval)
	}
	if clone.RepeatInterval == nil || *clone.RepeatInterval != *original.RepeatInterval {
		t.Fatalf("Clone().RepeatInterval = %v, want a copy of %v", clone.RepeatInterval, original.RepeatInterval)
	}

	// Clone's pointer must be independent of the original's.
	clone.GroupWait = routingDuration(999 * time.Second)
	if *original.GroupWait == *clone.GroupWait {
		t.Fatal("Clone() must not alias the original's GroupWait pointer")
	}
}

// TestTreeBuilder_RootWithoutGroupBy_BuildsEmptyGroupBy is the regression guard
// at the surface that actually broke: a config shaped exactly like upstream's
// minimal `route: {receiver: default}` must produce a root node with NO grouping
// labels, and every child that does not set its own must inherit that.
//
// The post-merge failure (futureparity
// TestUpstreamParity_AlertGroupsWithoutGroupByHaveEmptyLabels, "expected 1 group,
// got 2") came from this path returning ["alertname"] instead.
func TestTreeBuilder_RootWithoutGroupBy_BuildsEmptyGroupBy(t *testing.T) {
	child := &grouping.Route{Match: map[string]string{"severity": "critical"}}
	config := &infraroute.RouteConfig{
		Route: &grouping.Route{
			Receiver: "default",
			Routes:   []*grouping.Route{child},
		},
		Receivers: []*infraroute.Receiver{{
			Name:           "default",
			WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/default"}},
		}},
	}

	tree, err := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	root := tree.Root
	if len(root.GroupBy) != 0 {
		t.Fatalf("root.GroupBy = %v, want empty (upstream: no group_by = one group per receiver, labels {})", root.GroupBy)
	}
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}
	if len(root.Children[0].GroupBy) != 0 {
		t.Fatalf("child.GroupBy = %v, want empty (inherited)", root.Children[0].GroupBy)
	}
}
