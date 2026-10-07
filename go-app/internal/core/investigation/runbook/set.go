package runbook

import (
	"sort"

	"github.com/ipiton/AMP/internal/core"
)

// alertnameLabel is resolved from core.Alert.AlertName when the alert's label
// set does not carry it: AlertName is a separate field of the alert model.
const alertnameLabel = "alertname"

// Set is an immutable, deterministically ordered collection of runbooks.
type Set struct {
	runbooks []Runbook
}

// NewSet copies rbs and sorts them: more match labels first (more specific),
// then by Name, then by Source.
func NewSet(rbs []Runbook) *Set {
	sorted := make([]Runbook, len(rbs))
	copy(sorted, rbs)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if len(a.Match) != len(b.Match) {
			return len(a.Match) > len(b.Match)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Source < b.Source
	})
	return &Set{runbooks: sorted}
}

// Len returns the number of runbooks in the set. A nil set is empty.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.runbooks)
}

// Match returns the runbooks whose every match label equals the alert's label
// value, in set order, capped at limit (limit <= 0 means no cap).
func (s *Set) Match(alert *core.Alert, limit int) []Runbook {
	if s == nil || alert == nil {
		return nil
	}
	var out []Runbook
	for _, rb := range s.runbooks {
		if !matches(rb, alert) {
			continue
		}
		out = append(out, rb)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func matches(rb Runbook, alert *core.Alert) bool {
	for name, want := range rb.Match {
		got, ok := alert.Labels[name]
		if !ok && name == alertnameLabel && alert.AlertName != "" {
			got, ok = alert.AlertName, true
		}
		if !ok || got != want {
			return false
		}
	}
	return true
}
