package handlers

import (
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

func TestParseLabelMatcher_Valid(t *testing.T) {
	cases := []struct {
		raw     string
		name    string
		op      MatcherOp
		value   string
		hasRe   bool
	}{
		{`alertname="Watchdog"`, "alertname", MatcherOpEqual, "Watchdog", false},
		{`severity!="critical"`, "severity", MatcherOpNotEqual, "critical", false},
		{`alertname=~"Watch.*"`, "alertname", MatcherOpRegex, "Watch.*", true},
		{`severity!~"crit.*"`, "severity", MatcherOpNotRegex, "crit.*", true},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			m, err := ParseLabelMatcher(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Name != tc.name {
				t.Errorf("Name = %q, want %q", m.Name, tc.name)
			}
			if m.Op != tc.op {
				t.Errorf("Op = %q, want %q", m.Op, tc.op)
			}
			if m.Value != tc.value {
				t.Errorf("Value = %q, want %q", m.Value, tc.value)
			}
			if tc.hasRe && m.re == nil {
				t.Error("expected compiled regexp, got nil")
			}
			if !tc.hasRe && m.re != nil {
				t.Error("expected nil regexp, got non-nil")
			}
		})
	}
}

func TestParseLabelMatcher_Invalid(t *testing.T) {
	cases := []string{
		`bad`,
		`bad:syntax`,
		`name=value`,     // missing quotes
		`=~"value"`,      // missing name
		`name=~"[invalid"`, // invalid regex
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseLabelMatcher(raw)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", raw)
			}
		})
	}
}

func TestMatchesLabels(t *testing.T) {
	labels := map[string]string{
		"alertname": "Watchdog",
		"severity":  "critical",
	}

	cases := []struct {
		name    string
		raw     string
		labels  map[string]string
		want    bool
	}{
		{
			name:   "equal match",
			raw:    `alertname="Watchdog"`,
			labels: labels,
			want:   true,
		},
		{
			name:   "equal no match",
			raw:    `alertname="Other"`,
			labels: labels,
			want:   false,
		},
		{
			name:   "regex match",
			raw:    `severity=~"crit.*"`,
			labels: labels,
			want:   true,
		},
		{
			name:   "regex no match",
			raw:    `severity=~"warn.*"`,
			labels: labels,
			want:   false,
		},
		{
			name:   "not equal match",
			raw:    `alertname!="Other"`,
			labels: labels,
			want:   true,
		},
		{
			name:   "not regex match",
			raw:    `severity!~"warn.*"`,
			labels: labels,
			want:   true,
		},
		{
			name:   "missing label treated as empty",
			raw:    `missing="value"`,
			labels: labels,
			want:   false,
		},
		{
			name:   "missing label not-equal matches",
			raw:    `missing!="something"`,
			labels: labels,
			want:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseLabelMatcher(tc.raw)
			if err != nil {
				t.Fatalf("ParseLabelMatcher error: %v", err)
			}
			got := MatchesLabels([]*LabelMatcher{m}, tc.labels)
			if got != tc.want {
				t.Errorf("MatchesLabels = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchesLabels_MultipleAND(t *testing.T) {
	labels := map[string]string{"alertname": "Watchdog", "severity": "critical"}

	matchers, err := ParseLabelMatchers([]string{`alertname="Watchdog"`, `severity="critical"`})
	if err != nil {
		t.Fatal(err)
	}
	if !MatchesLabels(matchers, labels) {
		t.Error("expected match for all labels")
	}

	matchers2, err := ParseLabelMatchers([]string{`alertname="Watchdog"`, `severity="warning"`})
	if err != nil {
		t.Fatal(err)
	}
	if MatchesLabels(matchers2, labels) {
		t.Error("expected no match when one matcher fails")
	}
}

func TestMatchesLabels_EmptyFilters(t *testing.T) {
	labels := map[string]string{"alertname": "Watchdog"}
	if !MatchesLabels(nil, labels) {
		t.Error("empty filter slice should match everything")
	}
	if !MatchesLabels([]*LabelMatcher{}, labels) {
		t.Error("empty filter slice should match everything")
	}
}

func TestMatchesSilenceMatchers(t *testing.T) {
	silenceMatchers := []core.APISilenceMatcher{
		{Name: "alertname", Value: "Watchdog", IsRegex: false, IsEqual: true},
		{Name: "severity", Value: "critical", IsRegex: false, IsEqual: true},
	}

	t.Run("filter present in silence matchers", func(t *testing.T) {
		filters, err := ParseLabelMatchers([]string{`alertname="Watchdog"`})
		if err != nil {
			t.Fatal(err)
		}
		if !MatchesSilenceMatchers(filters, silenceMatchers) {
			t.Error("expected match")
		}
	})

	t.Run("filter not present in silence matchers", func(t *testing.T) {
		filters, err := ParseLabelMatchers([]string{`nonexistent="x"`})
		if err != nil {
			t.Fatal(err)
		}
		if MatchesSilenceMatchers(filters, silenceMatchers) {
			t.Error("expected no match")
		}
	})

	t.Run("all filters must match", func(t *testing.T) {
		filters, err := ParseLabelMatchers([]string{`alertname="Watchdog"`, `nonexistent="x"`})
		if err != nil {
			t.Fatal(err)
		}
		if MatchesSilenceMatchers(filters, silenceMatchers) {
			t.Error("expected no match when one filter has no corresponding silence matcher")
		}
	})

	t.Run("empty filters match all silences", func(t *testing.T) {
		if !MatchesSilenceMatchers(nil, silenceMatchers) {
			t.Error("empty filter should match all silences")
		}
	})
}
