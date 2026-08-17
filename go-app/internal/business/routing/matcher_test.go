package routing

// Fixture-style tests for RouteMatcher / FindMatchingRoutes.
//
// These are the first tests of this package. The tree fixture below
// mirrors the canonical routing example from the Prometheus Alertmanager
// configuration docs (https://prometheus.io/docs/alerting/latest/configuration/#route):
//
//	route:
//	  receiver: 'team-X-mails'
//	  routes:
//	  - match_re:
//	      service: ^(foo1|foo2|baz)$
//	    receiver: 'team-X-mails'
//	    routes:
//	    - match:
//	        severity: critical
//	      receiver: 'team-X-pager'
//	  - match:
//	      service: files
//	    receiver: 'team-Y-mails'
//	    routes:
//	    - match:
//	        severity: critical
//	      receiver: 'team-Y-pager'
//	  - match:
//	      service: database
//	    receiver: 'team-DB-pager'
//	    continue: true
//	  - match:
//	      owner: team-Z
//	    receiver: 'team-Z-pager'
//
// It is used to assert the expected receiver(s) for given alert label sets,
// exercising: root fallback, nested descent (parent receiver suppressed
// once a child matches), continue:true multi-match, sibling order /
// first-match-wins without continue, and regex anchoring.

import (
	"testing"
)

// testMatcher returns a RouteMatcher with metrics disabled, suitable for
// most tests. Metrics are backed by promauto (global Prometheus registry),
// so constructing many metrics-enabled matchers within one test binary
// would panic on duplicate registration.
func testMatcher() *RouteMatcher {
	return NewRouteMatcher(nil, MatcherOptions{
		EnableMetrics: false,
		CacheSize:     100,
	})
}

// buildAlertmanagerDocsFixtureTree builds the RouteTree described above.
func buildAlertmanagerDocsFixtureTree() *RouteTree {
	teamZPager := &RouteNode{
		Receiver: "team-Z-pager",
		Matchers: []Matcher{{Name: "owner", Value: "team-Z"}},
	}
	teamDBPager := &RouteNode{
		Receiver: "team-DB-pager",
		Matchers: []Matcher{{Name: "service", Value: "database"}},
		Continue: true,
	}
	teamYPager := &RouteNode{
		Receiver: "team-Y-pager",
		Matchers: []Matcher{{Name: "severity", Value: "critical"}},
	}
	teamYMails := &RouteNode{
		Receiver: "team-Y-mails",
		Matchers: []Matcher{{Name: "service", Value: "files"}},
		Children: []*RouteNode{teamYPager},
	}
	teamXPager := &RouteNode{
		Receiver: "team-X-pager",
		Matchers: []Matcher{{Name: "severity", Value: "critical"}},
	}
	teamXMailsNested := &RouteNode{
		Receiver: "team-X-mails",
		Matchers: []Matcher{{Name: "service", Value: "^(foo1|foo2|baz)$", IsRegex: true}},
		Children: []*RouteNode{teamXPager},
	}
	root := &RouteNode{
		Receiver: "team-X-mails",
		Children: []*RouteNode{teamXMailsNested, teamYMails, teamDBPager, teamZPager},
	}

	return &RouteTree{Root: root}
}

// receiverNames extracts, in order, the receiver of each matched node.
func receiverNames(nodes []*RouteNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Receiver
	}
	return names
}

func TestFindMatchingRoutes_RootFallback(t *testing.T) {
	// No route in the fixture matches an alert with no relevant labels at
	// all: none of the top-level branches (service=~.., service=files,
	// service=database, owner=team-Z) match. Per upstream semantics, the
	// root itself (which always "matches" — it has no matchers) is then
	// the result, and its own receiver is used.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	alert := &Alert{Labels: map[string]string{"alertname": "Unrelated"}}
	result := m.FindMatchingRoutes(tree, alert)

	if got := receiverNames(result.Matches); len(got) != 1 || got[0] != "team-X-mails" {
		t.Fatalf("expected root fallback to team-X-mails, got %v", got)
	}
}

func TestFindMatchingRoutes_NestedDescent_ParentReceiverSuppressed(t *testing.T) {
	// service=foo2 matches the team-X-mails branch (regex), and within it
	// severity=critical matches team-X-pager. The parent's own receiver
	// (team-X-mails) must NOT be returned once its child matched.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	alert := &Alert{Labels: map[string]string{"service": "foo2", "severity": "critical"}}
	result := m.FindMatchingRoutes(tree, alert)

	got := receiverNames(result.Matches)
	if len(got) != 1 || got[0] != "team-X-pager" {
		t.Fatalf("expected exactly [team-X-pager] (parent suppressed), got %v", got)
	}
}

func TestFindMatchingRoutes_NestedDescent_OneLevel_NoChildMatch(t *testing.T) {
	// service=foo1 matches the team-X-mails branch, but severity is unset
	// so the nested team-X-pager child does not match. The matched parent
	// itself becomes the result.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	alert := &Alert{Labels: map[string]string{"service": "foo1"}}
	result := m.FindMatchingRoutes(tree, alert)

	got := receiverNames(result.Matches)
	if len(got) != 1 || got[0] != "team-X-mails" {
		t.Fatalf("expected exactly [team-X-mails], got %v", got)
	}
}

func TestFindMatchingRoutes_ContinueTrue_MultiMatch(t *testing.T) {
	// service=database matches team-DB-pager (continue:true), so sibling
	// evaluation keeps going; owner=team-Z then also matches team-Z-pager.
	// Both must be collected, in order.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	alert := &Alert{Labels: map[string]string{"service": "database", "owner": "team-Z"}}
	result := m.FindMatchingRoutes(tree, alert)

	got := receiverNames(result.Matches)
	want := []string{"team-DB-pager", "team-Z-pager"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %v (continue:true fan-out), got %v", want, got)
	}
}

func TestFindMatchingRoutes_SiblingOrder_FirstMatchWinsWithoutContinue(t *testing.T) {
	// service=files matches team-Y-mails (continue defaults to false), so
	// sibling evaluation must stop there — even though owner=team-Z would
	// otherwise also match the team-Z-pager sibling further along.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	alert := &Alert{Labels: map[string]string{"service": "files", "owner": "team-Z"}}
	result := m.FindMatchingRoutes(tree, alert)

	got := receiverNames(result.Matches)
	if len(got) != 1 || got[0] != "team-Y-mails" {
		t.Fatalf("expected exactly [team-Y-mails] (sibling short-circuit), got %v", got)
	}
}

func TestFindMatchingRoutes_RegexAnchoring_SubstringMustNotMatch(t *testing.T) {
	// The team-X-mails branch matcher is service=~"^(foo1|foo2|baz)$".
	// Without anchoring, "foo10" or "xfoo1" would incorrectly match as a
	// substring. With anchoring (^(?:...)$), only exact "foo1"/"foo2"/"baz"
	// match.
	tree := buildAlertmanagerDocsFixtureTree()
	m := testMatcher()

	for _, service := range []string{"foo10", "xfoo1", "foo1x", "barbaz"} {
		alert := &Alert{Labels: map[string]string{"service": service}}
		result := m.FindMatchingRoutes(tree, alert)

		got := receiverNames(result.Matches)
		if len(got) != 1 || got[0] != "team-X-mails" {
			t.Fatalf("service=%q: expected root fallback [team-X-mails] (regex must not match as substring), got %v", service, got)
		}
	}

	// Sanity check: the exact values still match the nested branch.
	for _, service := range []string{"foo1", "foo2", "baz"} {
		alert := &Alert{Labels: map[string]string{"service": service}}
		result := m.FindMatchingRoutes(tree, alert)

		got := receiverNames(result.Matches)
		if len(got) != 1 || got[0] != "team-X-mails" {
			t.Fatalf("service=%q: expected [team-X-mails] via exact regex match, got %v", service, got)
		}
	}
}

func TestMatchesNode_RegexAnchoring_NegativeRegex(t *testing.T) {
	// !~ must also be anchored: "production" must NOT be treated as
	// matching pattern "prod" via unanchored substring search.
	m := testMatcher()
	node := &RouteNode{
		Matchers: []Matcher{{Name: "namespace", Value: "prod", IsRegex: true, IsNegative: true}},
	}

	alert := &Alert{Labels: map[string]string{"namespace": "production"}}
	if !m.MatchesNode(node, alert) {
		t.Fatalf("namespace=production should match !~ \"prod\" once anchored (not an exact match)")
	}

	alert2 := &Alert{Labels: map[string]string{"namespace": "prod"}}
	if m.MatchesNode(node, alert2) {
		t.Fatalf("namespace=prod should NOT match !~ \"prod\" (exact match, negated)")
	}
}

func TestMatchesNode_NegativeMatchers(t *testing.T) {
	m := testMatcher()

	t.Run("!= matches when label differs", func(t *testing.T) {
		node := &RouteNode{Matchers: []Matcher{{Name: "severity", Value: "critical", IsNegative: true}}}
		alert := &Alert{Labels: map[string]string{"severity": "warning"}}
		if !m.MatchesNode(node, alert) {
			t.Fatal("expected match: severity=warning != critical")
		}
	})

	t.Run("!= does not match when label equals", func(t *testing.T) {
		node := &RouteNode{Matchers: []Matcher{{Name: "severity", Value: "critical", IsNegative: true}}}
		alert := &Alert{Labels: map[string]string{"severity": "critical"}}
		if m.MatchesNode(node, alert) {
			t.Fatal("expected no match: severity=critical != critical is false")
		}
	})

	t.Run("!= matches when label missing", func(t *testing.T) {
		node := &RouteNode{Matchers: []Matcher{{Name: "severity", Value: "critical", IsNegative: true}}}
		alert := &Alert{Labels: map[string]string{}}
		if !m.MatchesNode(node, alert) {
			t.Fatal("expected match: missing label matches !=")
		}
	})

	t.Run("!~ matches when label missing", func(t *testing.T) {
		node := &RouteNode{Matchers: []Matcher{{Name: "namespace", Value: "dev.*", IsRegex: true, IsNegative: true}}}
		alert := &Alert{Labels: map[string]string{}}
		if !m.MatchesNode(node, alert) {
			t.Fatal("expected match: missing label matches !~")
		}
	})
}

func TestFindMatchingRoutes_NilTree(t *testing.T) {
	m := testMatcher()
	alert := &Alert{Labels: map[string]string{"alertname": "X"}}

	result := m.FindMatchingRoutes(nil, alert)
	if !result.Empty() {
		t.Fatalf("expected no matches for nil tree, got %v", receiverNames(result.Matches))
	}

	result = m.FindMatchingRoutes(&RouteTree{}, alert)
	if !result.Empty() {
		t.Fatalf("expected no matches for tree with nil root, got %v", receiverNames(result.Matches))
	}
}
