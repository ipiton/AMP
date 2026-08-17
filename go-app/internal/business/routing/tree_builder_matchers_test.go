package routing

// Tests for parseMatchers / parseMatcherExpr: the legacy match/match_re
// map syntax, and the `matchers:` free-form list syntax that is the only
// way to express negative matchers (!=, !~), since match/match_re have no
// way to encode negation.
//
// These tests exercise parseMatchers directly (via a zero-value
// TreeBuilder) and never construct the tree_builder-local Route /
// RouteConfig types, since those are slated for deletion in a later task.

import (
	"reflect"
	"testing"
)

func TestParseMatcherExpr(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		want   Matcher
		wantOK bool
	}{
		{
			name:   "equality unquoted",
			expr:   "service=files",
			want:   Matcher{Name: "service", Value: "files"},
			wantOK: true,
		},
		{
			name:   "equality quoted",
			expr:   `service="files"`,
			want:   Matcher{Name: "service", Value: "files"},
			wantOK: true,
		},
		{
			name:   "inequality with spaces",
			expr:   "severity != critical",
			want:   Matcher{Name: "severity", Value: "critical", IsNegative: true},
			wantOK: true,
		},
		{
			name:   "regex quoted alternation",
			expr:   `sev =~ "a|b"`,
			want:   Matcher{Name: "sev", Value: "a|b", IsRegex: true},
			wantOK: true,
		},
		{
			name:   "negative regex",
			expr:   `namespace !~ "dev.*"`,
			want:   Matcher{Name: "namespace", Value: "dev.*", IsRegex: true, IsNegative: true},
			wantOK: true,
		},
		{
			name:   "unmatched trailing quote is preserved, not stripped",
			expr:   `service=files"`,
			want:   Matcher{Name: "service", Value: `files"`},
			wantOK: true,
		},
		{
			name:   "unmatched leading quote is preserved, not stripped",
			expr:   `service="files`,
			want:   Matcher{Name: "service", Value: `"files`},
			wantOK: true,
		},
		{
			name:   "malformed: no operator",
			expr:   "not-a-matcher",
			wantOK: false,
		},
		{
			name:   "malformed: empty",
			expr:   "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseMatcherExpr(tc.expr)
			if ok != tc.wantOK {
				t.Fatalf("parseMatcherExpr(%q) ok = %v, want %v", tc.expr, ok, tc.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseMatcherExpr(%q) = %+v, want %+v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParseMatchers_LegacyMapsNeverNegative(t *testing.T) {
	b := &TreeBuilder{}

	matchers := b.parseMatchers(
		map[string]string{"team": "frontend"},
		map[string]string{"service": "mysql|cassandra"},
		nil,
	)

	if len(matchers) != 2 {
		t.Fatalf("expected 2 matchers, got %d: %+v", len(matchers), matchers)
	}

	for _, m := range matchers {
		if m.IsNegative {
			t.Fatalf("legacy match/match_re syntax must never produce IsNegative=true, got %+v", m)
		}
	}
}

func TestParseMatchers_ListSyntaxSetsIsNegative(t *testing.T) {
	// This is the core regression: before the fix, IsNegative was never
	// set by parseMatchers, making != and !~ unreachable from config.
	b := &TreeBuilder{}

	matchers := b.parseMatchers(nil, nil, []string{
		"severity != critical",
		`namespace !~ "dev.*"`,
		"team = frontend",
	})

	if len(matchers) != 3 {
		t.Fatalf("expected 3 matchers, got %d: %+v", len(matchers), matchers)
	}

	want := []Matcher{
		{Name: "severity", Value: "critical", IsNegative: true},
		{Name: "namespace", Value: "dev.*", IsRegex: true, IsNegative: true},
		{Name: "team", Value: "frontend"},
	}

	for i, m := range matchers {
		if !reflect.DeepEqual(m, want[i]) {
			t.Fatalf("matcher[%d] = %+v, want %+v", i, m, want[i])
		}
	}
}

func TestParseMatchers_CombinesAllThreeSyntaxes(t *testing.T) {
	b := &TreeBuilder{}

	matchers := b.parseMatchers(
		map[string]string{"team": "frontend"},
		map[string]string{"service": "mysql|cassandra"},
		[]string{"severity != critical"},
	)

	if len(matchers) != 3 {
		t.Fatalf("expected 3 combined matchers, got %d: %+v", len(matchers), matchers)
	}

	var negativeCount int
	for _, m := range matchers {
		if m.IsNegative {
			negativeCount++
		}
	}
	if negativeCount != 1 {
		t.Fatalf("expected exactly 1 negative matcher (from the list syntax), got %d in %+v", negativeCount, matchers)
	}
}

func TestParseMatchers_MalformedListEntrySkipped(t *testing.T) {
	b := &TreeBuilder{}

	matchers := b.parseMatchers(nil, nil, []string{
		"severity != critical",
		"garbage-no-operator",
	})

	if len(matchers) != 1 {
		t.Fatalf("expected malformed entry to be skipped, got %+v", matchers)
	}
	if matchers[0].Name != "severity" {
		t.Fatalf("expected the valid entry to survive, got %+v", matchers)
	}
}
