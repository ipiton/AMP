package runbook_test

import (
	"reflect"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/investigation/runbook"
)

func rb(name, source string, match map[string]string) runbook.Runbook {
	return runbook.Runbook{Name: name, Source: source, Match: match, Body: name + " body"}
}

func names(rbs []runbook.Runbook) []string {
	out := make([]string, 0, len(rbs))
	for _, r := range rbs {
		out = append(out, r.Name)
	}
	return out
}

func TestSet_Match(t *testing.T) {
	set := runbook.NewSet([]runbook.Runbook{
		rb("generic-memory", "a.md", map[string]string{"alertname": "HighMemoryUsage"}),
		rb("critical-memory", "b.md", map[string]string{"alertname": "HighMemoryUsage", "severity": "critical"}),
		rb("prod-critical-memory", "c.md", map[string]string{"alertname": "HighMemoryUsage", "severity": "critical", "namespace": "prod"}),
		rb("disk", "d.md", map[string]string{"alertname": "DiskFull"}),
	})

	tests := []struct {
		name  string
		alert *core.Alert
		limit int
		want  []string
	}{
		{
			name:  "alertname only in AlertName field, specificity order",
			alert: &core.Alert{AlertName: "HighMemoryUsage", Labels: map[string]string{"severity": "critical", "namespace": "prod", "pod": "x"}},
			want:  []string{"prod-critical-memory", "critical-memory", "generic-memory"},
		},
		{
			name:  "partial match excluded",
			alert: &core.Alert{AlertName: "HighMemoryUsage", Labels: map[string]string{"severity": "warning"}},
			want:  []string{"generic-memory"},
		},
		{
			name:  "labels alertname takes precedence over AlertName",
			alert: &core.Alert{AlertName: "HighMemoryUsage", Labels: map[string]string{"alertname": "DiskFull"}},
			want:  []string{"disk"},
		},
		{
			name:  "limit caps result keeping most specific",
			alert: &core.Alert{AlertName: "HighMemoryUsage", Labels: map[string]string{"severity": "critical", "namespace": "prod"}},
			limit: 2,
			want:  []string{"prod-critical-memory", "critical-memory"},
		},
		{
			name:  "no match",
			alert: &core.Alert{AlertName: "Other", Labels: map[string]string{"severity": "critical"}},
			want:  []string{},
		},
		{
			name:  "nil labels",
			alert: &core.Alert{AlertName: "DiskFull"},
			want:  []string{"disk"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := names(set.Match(tt.alert, tt.limit))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_TieBreakByNameThenSource(t *testing.T) {
	m := map[string]string{"alertname": "A"}
	set := runbook.NewSet([]runbook.Runbook{
		rb("beta", "z.md", m),
		rb("alpha", "y.md", m),
		rb("alpha", "x.md", m),
	})
	got := set.Match(&core.Alert{AlertName: "A"}, 0)
	var sources []string
	for _, r := range got {
		sources = append(sources, r.Source)
	}
	if want := []string{"x.md", "y.md", "z.md"}; !reflect.DeepEqual(sources, want) {
		t.Errorf("order = %v, want %v", sources, want)
	}
}

func TestSet_NilAndEmpty(t *testing.T) {
	var nilSet *runbook.Set
	if nilSet.Len() != 0 || nilSet.Match(&core.Alert{AlertName: "A"}, 0) != nil {
		t.Error("nil set must be empty and match nothing")
	}
	set := runbook.NewSet(nil)
	if set.Len() != 0 || len(set.Match(&core.Alert{AlertName: "A"}, 0)) != 0 {
		t.Error("empty set must match nothing")
	}
	full := runbook.NewSet([]runbook.Runbook{rb("a", "a.md", map[string]string{"alertname": "A"})})
	if full.Match(nil, 0) != nil {
		t.Error("nil alert must match nothing")
	}
}

func TestNewSet_DoesNotAliasInput(t *testing.T) {
	in := []runbook.Runbook{
		rb("b", "b.md", map[string]string{"alertname": "A"}),
		rb("a", "a.md", map[string]string{"alertname": "A"}),
	}
	runbook.NewSet(in)
	if in[0].Name != "b" {
		t.Error("NewSet reordered the caller's slice")
	}
}
