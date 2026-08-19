package routing

// Regression test for the task 1.2 type-unification migration: TreeBuilder
// must build a correct RouteTree directly from the canonical
// infrastructure/routing.RouteConfig (route tree = grouping.Route,
// receivers = infrastructure/routing.Receiver), with no local
// RouteConfig/Route/GlobalConfig/Receiver types involved.
//
// In particular this must prove that match/match_re maps, the `matchers:`
// list syntax, and negative matchers (added in task 1.1) survive the type
// migration all the way through to RouteNode.Matchers.

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

func groupingDuration(d time.Duration) *grouping.Duration {
	return &grouping.Duration{Duration: d}
}

// buildMigrationFixtureConfig returns an infraroute.RouteConfig exercising:
//   - match (equality, never negative)
//   - match_re (regex, never negative)
//   - matchers: list syntax, including a negative equality (!=) and a
//     regex (=~) entry
//   - group_wait inheritance (child leaves it unset, must inherit root's)
//   - receiver inheritance (child leaves it empty, must inherit root's)
func buildMigrationFixtureConfig() *infraroute.RouteConfig {
	criticalChild := &grouping.Route{
		Receiver: "critical",
		Match:    map[string]string{"severity": "critical"},
		MatchRE:  map[string]string{"service": "^db.*"},
		Matchers: []string{
			"team != frontend",
			`region =~ "us-.*"`,
		},
	}

	inheritingChild := &grouping.Route{
		// No Receiver, no GroupWait: both must inherit from root.
	}

	root := &grouping.Route{
		Receiver:  "default",
		GroupBy:   []string{"alertname"},
		GroupWait: groupingDuration(10 * time.Second),
		Routes:    []*grouping.Route{criticalChild, inheritingChild},
	}

	receivers := []*infraroute.Receiver{
		{
			Name:           "default",
			WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/default"}},
		},
		{
			Name:           "critical",
			WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://example.com/critical"}},
		},
	}

	return &infraroute.RouteConfig{Route: root, Receivers: receivers}
}

func TestTreeBuilder_BuildsFromCanonicalRouteConfig(t *testing.T) {
	config := buildMigrationFixtureConfig()
	builder := NewTreeBuilder(config, BuildOptions{ValidateOnBuild: false})

	tree, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	if tree == nil || tree.Root == nil {
		t.Fatal("Build() returned nil tree or nil root")
	}

	root := tree.Root
	if root.Receiver != "default" {
		t.Fatalf("root.Receiver = %q, want %q", root.Receiver, "default")
	}
	if root.GroupWait != 10*time.Second {
		t.Fatalf("root.GroupWait = %v, want 10s", root.GroupWait)
	}
	// GroupInterval/RepeatInterval were left unset on the fixture root and
	// have no parent to inherit from; this fixture's RouteConfig.Global is
	// also nil, so both must fall back to the hardcoded defaults. See
	// global_inheritance_test.go for the global.group_interval/
	// repeat_interval fallback layer restored by task fu5-cfg item 3
	// (FU-GLOB-DEFAULT-VALUES).
	if root.GroupInterval != 5*time.Minute {
		t.Fatalf("root.GroupInterval = %v, want 5m default", root.GroupInterval)
	}
	if root.RepeatInterval != 4*time.Hour {
		t.Fatalf("root.RepeatInterval = %v, want 4h default", root.RepeatInterval)
	}

	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	criticalNode, inheritingNode := root.Children[0], root.Children[1]

	t.Run("match/match_re/matchers all reach RouteNode.Matchers", func(t *testing.T) {
		if len(criticalNode.Matchers) != 4 {
			t.Fatalf("expected 4 matchers, got %d: %+v", len(criticalNode.Matchers), criticalNode.Matchers)
		}

		var gotSeverity, gotService, gotTeam, gotRegion bool
		for _, m := range criticalNode.Matchers {
			switch m.Name {
			case "severity":
				gotSeverity = true
				if m.Value != "critical" || m.IsRegex || m.IsNegative {
					t.Fatalf("severity matcher wrong: %+v", m)
				}
			case "service":
				gotService = true
				if m.Value != "^db.*" || !m.IsRegex || m.IsNegative {
					t.Fatalf("service matcher wrong: %+v", m)
				}
			case "team":
				gotTeam = true
				if m.Value != "frontend" || m.IsRegex || !m.IsNegative {
					t.Fatalf("team matcher wrong (expected negative equality): %+v", m)
				}
			case "region":
				gotRegion = true
				if m.Value != "us-.*" || !m.IsRegex || m.IsNegative {
					t.Fatalf("region matcher wrong (expected positive regex): %+v", m)
				}
			}
		}
		if !gotSeverity || !gotService || !gotTeam || !gotRegion {
			t.Fatalf("missing expected matcher(s), got: %+v", criticalNode.Matchers)
		}
	})

	t.Run("receiver config resolves to the canonical infraroute.Receiver", func(t *testing.T) {
		if criticalNode.ReceiverConfig == nil {
			t.Fatal("criticalNode.ReceiverConfig is nil")
		}
		if criticalNode.ReceiverConfig.Name != "critical" {
			t.Fatalf("ReceiverConfig.Name = %q, want %q", criticalNode.ReceiverConfig.Name, "critical")
		}
		if len(criticalNode.ReceiverConfig.WebhookConfigs) != 1 ||
			criticalNode.ReceiverConfig.WebhookConfigs[0].URL != "https://example.com/critical" {
			t.Fatalf("ReceiverConfig.WebhookConfigs unexpected: %+v", criticalNode.ReceiverConfig.WebhookConfigs)
		}

		got := tree.GetReceiver("default")
		if got == nil || got.Name != "default" {
			t.Fatalf("tree.GetReceiver(%q) = %+v, want receiver named %q", "default", got, "default")
		}
	})

	t.Run("receiver and group_wait inherit from parent when unset", func(t *testing.T) {
		if inheritingNode.Receiver != "default" {
			t.Fatalf("inheritingNode.Receiver = %q, want inherited %q", inheritingNode.Receiver, "default")
		}
		if inheritingNode.GroupWait != 10*time.Second {
			t.Fatalf("inheritingNode.GroupWait = %v, want inherited 10s", inheritingNode.GroupWait)
		}
		if len(inheritingNode.Matchers) != 0 {
			t.Fatalf("inheritingNode.Matchers = %+v, want empty (matches all)", inheritingNode.Matchers)
		}
	})
}
