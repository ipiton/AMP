package routing

// ROUTE-TIMINGS-BUNDLE: route timings resolve like upstream dispatch.newRoute
// — own value (0s included) → parent's resolved value → (root only)
// global.* → 30s/5m/4h. These tests go through the production parser
// (infraroute.NewRouteConfigParser) on purpose: the bug lived in the parser
// filling defaults into every route, which tests building grouping.Route
// values by hand never saw.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

func buildTreeFromYAML(t *testing.T, yaml string) *RouteTree {
	t.Helper()
	cfg, err := infraroute.NewRouteConfigParser().Parse([]byte(yaml))
	require.NoError(t, err)
	tree, err := NewTreeBuilder(cfg, DefaultBuildOptions()).Build()
	require.NoError(t, err)
	return tree
}

type nodeTimings struct{ wait, interval, repeat time.Duration }

func timingsOf(n *RouteNode) nodeTimings {
	return nodeTimings{n.GroupWait, n.GroupInterval, n.RepeatInterval}
}

func TestTreeTimings_NestedRoutesInheritNearestAncestor(t *testing.T) {
	tree := buildTreeFromYAML(t, `
route:
  receiver: root
  group_wait: 3s
  group_interval: 10s
  repeat_interval: 1h
  routes:
    - matchers: ['route="inherit"']
      receiver: child
      routes:
        - matchers: ['sub="grand"']
          receiver: grand
    - matchers: ['route="own"']
      receiver: child
      group_interval: 2m
      routes:
        - matchers: ['sub="grand"']
          receiver: grand
receivers:
  - name: root
  - name: child
  - name: grand
`)

	root := tree.Root
	assert.Equal(t, nodeTimings{3 * time.Second, 10 * time.Second, time.Hour}, timingsOf(root))

	inherit := root.Children[0]
	assert.Equal(t, timingsOf(root), timingsOf(inherit), "child without timings takes the root's")
	assert.Equal(t, timingsOf(root), timingsOf(inherit.Children[0]), "grandchild without timings takes the root's")

	own := root.Children[1]
	want := nodeTimings{3 * time.Second, 2 * time.Minute, time.Hour}
	assert.Equal(t, want, timingsOf(own), "only the field the route sets overrides the parent's")
	assert.Equal(t, want, timingsOf(own.Children[0]), "grandchild takes its nearest ancestor's timings")
}

func TestTreeTimings_ExplicitZeroGroupWaitIsKept(t *testing.T) {
	tree := buildTreeFromYAML(t, `
route:
  receiver: root
  group_wait: 10s
  routes:
    - matchers: ['signal="panic"']
      receiver: panic
      group_wait: 0s
      routes:
        - matchers: ['team="db"']
          receiver: panic
receivers:
  - name: root
  - name: panic
`)

	panicNode := tree.Root.Children[0]
	assert.Equal(t, time.Duration(0), panicNode.GroupWait, "group_wait: 0s means notify immediately, not inherit 10s")
	assert.Equal(t, time.Duration(0), panicNode.Children[0].GroupWait, "an explicit 0s is inherited like any value")

	evaluator := NewRouteEvaluator(tree, testMatcher(), EvaluatorOptions{FallbackToRoot: true})
	decision, err := evaluator.Evaluate(&Alert{Labels: map[string]string{"alertname": "PostgresDown", "signal": "panic"}})
	require.NoError(t, err)
	assert.Equal(t, "panic", decision.Receiver)
	assert.Equal(t, time.Duration(0), decision.GroupWait, "RoutingDecision carries the explicit 0 to grouping")
}

func TestTreeTimings_GlobalFallbackReachesNestedRoutes(t *testing.T) {
	tree := buildTreeFromYAML(t, `
global:
  group_wait: 7s
  group_interval: 1m
  repeat_interval: 2h
route:
  receiver: root
  repeat_interval: 3h
  routes:
    - matchers: ['a="b"']
      receiver: root
receivers:
  - name: root
`)

	want := nodeTimings{7 * time.Second, time.Minute, 3 * time.Hour}
	assert.Equal(t, want, timingsOf(tree.Root), "unset root timings come from global.*, set ones win")
	assert.Equal(t, want, timingsOf(tree.Root.Children[0]), "nested routes see global.* through the root")
}

func TestTreeTimings_UpstreamDefaultsAtRootOnly(t *testing.T) {
	tree := buildTreeFromYAML(t, `
route:
  receiver: root
  routes:
    - matchers: ['a="b"']
      receiver: root
      group_wait: 1s
receivers:
  - name: root
`)

	assert.Equal(t, nodeTimings{30 * time.Second, 5 * time.Minute, 4 * time.Hour}, timingsOf(tree.Root))
	assert.Equal(t, nodeTimings{time.Second, 5 * time.Minute, 4 * time.Hour}, timingsOf(tree.Root.Children[0]))
}

// Upstream has no group_interval >= group_wait rule; with inherited timings it
// would reject this valid upstream config (child lowers only group_interval).
func TestTreeTimings_GroupIntervalBelowInheritedGroupWaitLoads(t *testing.T) {
	tree := buildTreeFromYAML(t, `
route:
  receiver: root
  group_wait: 1m
  group_interval: 5m
  routes:
    - matchers: ['a="b"']
      receiver: root
      group_interval: 30s
receivers:
  - name: root
`)

	assert.Equal(t, nodeTimings{time.Minute, 30 * time.Second, 4 * time.Hour}, timingsOf(tree.Root.Children[0]))
}

func TestTreeTimings_ParserRejectsZeroIntervals(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"route group_interval": {`
route:
  receiver: root
  routes:
    - matchers: ['a="b"']
      receiver: root
      group_interval: 0s
receivers:
  - name: root
`, "group_interval cannot be zero"},
		"root repeat_interval": {`
route:
  receiver: root
  repeat_interval: 0s
receivers:
  - name: root
`, "repeat_interval cannot be zero"},
		"global group_interval": {`
global:
  group_interval: 0s
route:
  receiver: root
receivers:
  - name: root
`, "group_interval cannot be zero"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := infraroute.NewRouteConfigParser().Parse([]byte(tc.yaml))
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tc.want), "error %q must contain %q", err, tc.want)
		})
	}
}

// Defense in depth below the parser: a hand-built config reaching Build with
// zero intervals is still rejected, while group_wait 0 is not.
func TestTreeTimings_BuildValidatesDurations(t *testing.T) {
	zero := &grouping.Duration{}
	build := func(route *grouping.Route) error {
		_, err := NewTreeBuilder(&infraroute.RouteConfig{
			Route:     route,
			Receivers: []*infraroute.Receiver{{Name: "root"}},
		}, DefaultBuildOptions()).Build()
		return err
	}

	require.NoError(t, build(&grouping.Route{Receiver: "root", GroupWait: zero}))
	require.Error(t, build(&grouping.Route{Receiver: "root", GroupInterval: zero}))
	require.Error(t, build(&grouping.Route{Receiver: "root", RepeatInterval: zero}))
	require.Error(t, build(&grouping.Route{Receiver: "root", GroupWait: &grouping.Duration{Duration: -time.Second}}))
}
