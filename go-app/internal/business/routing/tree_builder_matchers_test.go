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
			// fix-round finding I-3: upstream errors on a trailing quote
			// with no leading quote (an unescaped '"' anywhere outside a
			// properly-opened quoted value is invalid) — the first pass
			// here silently kept it as a literal trailing quote instead.
			name:   "unmatched trailing quote now errors (upstream: unescaped double quote)",
			expr:   `service=files"`,
			wantOK: false,
		},
		{
			// fix-round finding I-3: upstream errors on a leading quote
			// with no matching trailing quote — the first pass here
			// silently kept the literal leading quote instead.
			name:   "unmatched leading quote now errors (upstream: unescaped double quote)",
			expr:   `service="files`,
			wantOK: false,
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
		// alertmanager-parity wave-5 item 5 (FU-PARSEARGUMENT-QUOTE-HANDLING):
		// escaped quotes, embedded spaces, and unicode inside a quoted value
		// — the previously-divergent cases vs pkg/configvalidator/matcher.Parse
		// (which didn't strip quotes at all before this task). See
		// pkg/configvalidator/matcher/matcher_test.go for the mirrored table.
		{
			name:   "escaped quote inside quoted value is unescaped",
			expr:   `team="front\"end"`,
			want:   Matcher{Name: "team", Value: `front"end`},
			wantOK: true,
		},
		{
			name:   "escaped backslash inside quoted value is unescaped",
			expr:   `path="C:\\temp"`,
			want:   Matcher{Name: "path", Value: `C:\temp`},
			wantOK: true,
		},
		{
			name:   "embedded space inside quoted value survives",
			expr:   `message="hello world"`,
			want:   Matcher{Name: "message", Value: "hello world"},
			wantOK: true,
		},
		{
			name:   "unicode inside quoted value survives",
			expr:   `city="Zürich 東京"`,
			want:   Matcher{Name: "city", Value: "Zürich 東京"},
			wantOK: true,
		},
		{
			name:   "explicit empty quoted value is preserved as empty string",
			expr:   `label=""`,
			want:   Matcher{Name: "label", Value: ""},
			wantOK: true,
		},
		{
			// fix-round 2, Minor #5: upstream accepts an empty value
			// outright ("The 3rd token may be the empty string"), and this
			// parser always did too — mirrored in
			// pkg/configvalidator/matcher's table after that parser's own
			// round-1 guard against this exact case was found to diverge.
			name:   "empty value (nothing after operator) is accepted, matching upstream",
			expr:   "label=",
			want:   Matcher{Name: "label", Value: ""},
			wantOK: true,
		},
		// fix-round finding I-3: the four verified divergences vs upstream
		// pkg/labels, now aligned. See matcher_test.go's mirrored table.
		{
			name:   "escaped LF inside a quoted value becomes a real line feed",
			expr:   "v=\"line\\nbreak\"",
			want:   Matcher{Name: "v", Value: "line\nbreak"},
			wantOK: true,
		},
		{
			name:   "escaping also applies to an UNQUOTED value (upstream applies it either way)",
			expr:   `v=C:\\temp`,
			want:   Matcher{Name: "v", Value: `C:\temp`},
			wantOK: true,
		},
		{
			name:   "unescaped inner quote inside a quoted value now errors",
			expr:   `v="a"b"`,
			wantOK: false,
		},
		{
			name:   "spurious escape (unrecognized \\x) keeps the backslash literal, not just the char",
			expr:   `v="a\tb"`,
			want:   Matcher{Name: "v", Value: `a\tb`},
			wantOK: true,
		},
		{
			name:   "a lone trailing backslash with nothing after it is a literal backslash",
			expr:   `v=foo\`,
			want:   Matcher{Name: "v", Value: `foo\`},
			wantOK: true,
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

// TestUnquoteMatcherValue_InvalidUTF8 exercises unquoteMatcherValue
// directly (fix-round finding I-3's "non-UTF-8 input" rule) rather than
// through parseMatcherExpr's regex: routing an invalid byte sequence
// through matcherExprPattern's `(.*)$` capture group first would make this
// a test of Go's regexp/UTF-8 handling as much as of unquoteMatcherValue
// itself.
func TestUnquoteMatcherValue_InvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe})

	if _, ok := unquoteMatcherValue(invalid); ok {
		t.Fatalf("unquoteMatcherValue(%q) ok = true, want false (invalid UTF-8 must be rejected)", invalid)
	}

	// A leading-quote variant, since the UTF-8 check runs on rawValue
	// AFTER stripping a leading quote (matching upstream's exact
	// placement) — both paths must reject it.
	if _, ok := unquoteMatcherValue(`"` + invalid + `"`); ok {
		t.Fatalf("unquoteMatcherValue with a quoted invalid UTF-8 value: ok = true, want false")
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
