package handlers

import (
	"fmt"
	"regexp"

	"github.com/ipiton/AMP/internal/core"
)

// MatcherOp is the operator in a label matcher.
type MatcherOp string

const (
	MatcherOpEqual    MatcherOp = "="
	MatcherOpNotEqual MatcherOp = "!="
	MatcherOpRegex    MatcherOp = "=~"
	MatcherOpNotRegex MatcherOp = "!~"
)

// LabelMatcher is a parsed label matcher from a query param.
type LabelMatcher struct {
	Name  string
	Op    MatcherOp
	Value string
	re    *regexp.Regexp // non-nil only for =~ and !~
}

var matcherRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)(=~|!~|!=|=)"(.*)"$`)

// ParseLabelMatcher parses a single filter string: name="v", name!="v", name=~"r", name!~"r".
func ParseLabelMatcher(raw string) (*LabelMatcher, error) {
	m := matcherRe.FindStringSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("invalid matcher syntax: %q", raw)
	}

	lm := &LabelMatcher{
		Name:  m[1],
		Op:    MatcherOp(m[2]),
		Value: m[3],
	}

	if lm.Op == MatcherOpRegex || lm.Op == MatcherOpNotRegex {
		re, err := regexp.Compile("^(?:" + lm.Value + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid matcher syntax: %q", raw)
		}
		lm.re = re
	}

	return lm, nil
}

// ParseLabelMatchers parses a slice of filter strings. Returns the first error encountered.
func ParseLabelMatchers(rawFilters []string) ([]*LabelMatcher, error) {
	matchers := make([]*LabelMatcher, 0, len(rawFilters))
	for _, raw := range rawFilters {
		lm, err := ParseLabelMatcher(raw)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, lm)
	}
	return matchers, nil
}

// MatchesLabels returns true if all matchers match the given labels (AND logic).
// A missing label is treated as an empty string.
func MatchesLabels(matchers []*LabelMatcher, labels map[string]string) bool {
	for _, m := range matchers {
		val := labels[m.Name]
		if !matchOne(m, val) {
			return false
		}
	}
	return true
}

func matchOne(m *LabelMatcher, val string) bool {
	switch m.Op {
	case MatcherOpEqual:
		return val == m.Value
	case MatcherOpNotEqual:
		return val != m.Value
	case MatcherOpRegex:
		return m.re != nil && m.re.MatchString(val)
	case MatcherOpNotRegex:
		return m.re != nil && !m.re.MatchString(val)
	}
	return false
}

// MatchesSilenceMatchers returns true if every filter matcher matches the
// VALUE of at least one silence matcher with the same name (upstream
// Alertmanager semantics). A name absent from the silence is treated as the
// empty value, so negative filters like service!="api" match silences that
// don't mention service at all.
func MatchesSilenceMatchers(filters []*LabelMatcher, silenceMatchers []core.APISilenceMatcher) bool {
	for _, f := range filters {
		matched := false
		nameSeen := false
		for _, sm := range silenceMatchers {
			if sm.Name != f.Name {
				continue
			}
			nameSeen = true
			if matchOne(f, sm.Value) {
				matched = true
				break
			}
		}
		if !nameSeen && matchOne(f, "") {
			matched = true
		}
		if !matched {
			return false
		}
	}
	return true
}
