package matcher

// Tests for Parse's quote handling (alertmanager-parity wave-5 item 5,
// FU-PARSEARGUMENT-QUOTE-HANDLING). Before this task Parse never stripped
// quotes at all — Parse(`severity="critical"`) returned Value ==
// `"critical"` literally. The table below mirrors
// internal/business/routing/tree_builder_matchers_test.go's
// TestParseMatcherExpr cases so the two previously-divergent parsers can be
// eyeballed side by side.

import (
	"testing"
)

func TestParse_QuoteHandling(t *testing.T) {
	tests := []struct {
		name      string
		matcher   string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "equality unquoted",
			matcher:   "service=files",
			wantValue: "files",
		},
		{
			name:      "equality quoted",
			matcher:   `service="files"`,
			wantValue: "files",
		},
		{
			name:      "regex quoted alternation",
			matcher:   `sev=~"a|b"`,
			wantValue: "a|b",
		},
		{
			name:      "escaped quote inside quoted value is unescaped",
			matcher:   `team="front\"end"`,
			wantValue: `front"end`,
		},
		{
			name:      "escaped backslash inside quoted value is unescaped",
			matcher:   `path="C:\\temp"`,
			wantValue: `C:\temp`,
		},
		{
			name:      "embedded space inside quoted value survives",
			matcher:   `message="hello world"`,
			wantValue: "hello world",
		},
		{
			name:      "unicode inside quoted value survives",
			matcher:   `city="Zürich 東京"`,
			wantValue: "Zürich 東京",
		},
		{
			name:      "explicit empty quoted value is preserved as empty string",
			matcher:   `label=""`,
			wantValue: "",
		},
		{
			// fix-round 2, Minor #5: upstream accepts an empty value
			// outright ("The 3rd token may be the empty string" —
			// ParseMatcher's own doc comment), and
			// business/routing.parseMatcherExpr always did too; a round-1
			// guard here rejected this specific case, contradicting the
			// "verbatim upstream grammar" claim. Aligned instead of kept
			// as a silent deviation.
			name:      "empty value (nothing after operator) is accepted, matching upstream and parseMatcherExpr",
			matcher:   "label=",
			wantValue: "",
		},
		{
			// fix-round finding I-3: upstream errors on a trailing quote
			// with no leading quote — the first pass here silently kept
			// it as a literal trailing quote instead.
			name:    "unmatched trailing quote now errors (upstream: unescaped double quote)",
			matcher: `service=files"`,
			wantErr: true,
		},
		{
			// fix-round finding I-3: upstream errors on a leading quote
			// with no matching trailing quote.
			name:    "unmatched leading quote now errors (upstream: unescaped double quote)",
			matcher: `service="files`,
			wantErr: true,
		},
		// fix-round finding I-3: the four verified divergences vs upstream
		// pkg/labels, now aligned. Mirrors
		// tree_builder_matchers_test.go's TestParseMatcherExpr table.
		{
			name:      "escaped LF inside a quoted value becomes a real line feed",
			matcher:   "v=\"line\\nbreak\"",
			wantValue: "line\nbreak",
		},
		{
			name:      "escaping also applies to an UNQUOTED value (upstream applies it either way)",
			matcher:   `v=C:\\temp`,
			wantValue: `C:\temp`,
		},
		{
			name:    "unescaped inner quote inside a quoted value now errors",
			matcher: `v="a"b"`,
			wantErr: true,
		},
		{
			name:      "spurious escape (unrecognized \\x) keeps the backslash literal, not just the char",
			matcher:   `v="a\tb"`,
			wantValue: `a\tb`,
		},
		{
			name:      "a lone trailing backslash with nothing after it is a literal backslash",
			matcher:   `v=foo\`,
			wantValue: `foo\`,
		},
		// fix-round finding I-4: a quoted value containing an operator
		// token used to hard-error here (E104's "invalid label name") while
		// business/routing.parseMatcherExpr parsed the identical YAML
		// entry fine, because the old operator search was a plain
		// strings.Index over the whole string and matched INSIDE the
		// quotes.
		{
			name:      "operator token inside a quoted value no longer splits the matcher (I-4)",
			matcher:   `summary="a!=b"`,
			wantValue: "a!=b",
		},
		{
			name:      "regex operator token inside a quoted value no longer splits the matcher (I-4)",
			matcher:   `summary="a=~b"`,
			wantValue: "a=~b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse(tc.matcher)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) error = nil, want an error", tc.matcher)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.matcher, err)
			}
			if m.Value != tc.wantValue {
				t.Fatalf("Parse(%q).Value = %q, want %q", tc.matcher, m.Value, tc.wantValue)
			}
		})
	}
}

// TestParse_QuotedRegexCompilesTheUnquotedPattern is the concrete bug this
// task closes: before quote-stripping was added, a quoted regex matcher fed
// its literal (quote-included) value straight into regexp.Compile, so
// `instance=~"crit.*"` compiled the pattern `"crit.*"` — requiring the
// label value to literally contain quote characters, which the actual
// route tree (built via business/routing.parseMatcherExpr, which already
// stripped quotes) never required for the identical YAML entry.
func TestParse_QuotedRegexCompilesTheUnquotedPattern(t *testing.T) {
	m, err := Parse(`instance=~"crit.*"`)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if m.Value != "crit.*" {
		t.Fatalf("Value = %q, want %q (quotes must not be part of the compiled pattern)", m.Value, "crit.*")
	}
	if m.CompiledRegex == nil {
		t.Fatal("CompiledRegex is nil for a regex matcher")
	}
	if !m.CompiledRegex.MatchString("critical") {
		t.Fatal(`compiled regex must match "critical" — it was compiled from the quote-included literal instead of "crit.*"`)
	}

	// Before quote-stripping, the compiled pattern was the LITERAL
	// `"crit.*"` (quotes included) — anchoring proves the difference,
	// since `^crit.*$` only matches a value with no surrounding quotes.
	anchored, err := Parse(`instance=~"^crit\.$"`)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if anchored.Value != `^crit\.$` {
		t.Fatalf("Value = %q, want %q — quotes must not leak into the compiled pattern", anchored.Value, `^crit\.$`)
	}
	if !anchored.CompiledRegex.MatchString("crit.") {
		t.Fatal(`^crit\.$ compiled from the unquoted value must match the literal string "crit."`)
	}
	if anchored.CompiledRegex.MatchString(`"crit."`) {
		t.Fatal(`^crit\.$ must NOT match a value with literal surrounding quotes — that would mean the pattern was compiled from the quote-included string`)
	}
}

func TestParse_UnquotedValuesUnaffected(t *testing.T) {
	m, err := Parse("alertname!=NodeDown")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if m.Value != "NodeDown" {
		t.Fatalf("Value = %q, want %q", m.Value, "NodeDown")
	}
}
