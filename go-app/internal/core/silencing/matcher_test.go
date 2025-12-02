package silencing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Helper function to create test alerts
func newTestAlert(labels map[string]string) Alert {
	return Alert{
		Labels:      labels,
		Annotations: map[string]string{},
	}
}

// Helper function to create test silences
func newTestSilence(id string, matchers []Matcher) *Silence {
	return &Silence{
		ID:        id,
		CreatedBy: "test@example.com",
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers:  matchers,
		Status:    SilenceStatusActive,
		CreatedAt: time.Now(),
	}
}

// ====================
// PHASE 5: Operator Tests (30 tests)
// ====================

// Equal Operator Tests (8 tests)

func TestMatcherEqual_Matched(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"severity":  "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match, got no match")
	}
}

func TestMatcherEqual_NotMatched(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "web-server",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match, got match")
	}
}

func TestMatcherEqual_MissingLabel(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (missing label), got match")
	}
}

func TestMatcherEqual_EmptyValue(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (empty equals empty), got no match")
	}
}

func TestMatcherEqual_CaseSensitive(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"severity": "Critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "severity", Value: "critical", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (case sensitive), got match")
	}
}

func TestMatcherEqual_Unicode(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"message": "CPU使用率が高い",
		"emoji":   "🚨",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "message", Value: "CPU使用率が高い", Type: MatcherTypeEqual},
		{Name: "emoji", Value: "🚨", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (unicode), got no match")
	}
}

func TestMatcherEqual_SpecialCharacters(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"path": "/var/log/app.log",
		"cmd":  "echo \"hello\nworld\"",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "path", Value: "/var/log/app.log", Type: MatcherTypeEqual},
		{Name: "cmd", Value: "echo \"hello\nworld\"", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (special chars), got no match")
	}
}

func TestMatcherEqual_MultipleMatchers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"instance":  "server-01",
		"severity":  "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
		{Name: "instance", Value: "server-01", Type: MatcherTypeEqual},
		{Name: "severity", Value: "critical", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (all 4 matchers), got no match")
	}
}

// NotEqual Operator Tests (6 tests)

func TestMatcherNotEqual_ValueDifferent(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"env": "dev",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "prod", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (dev != prod), got no match")
	}
}

func TestMatcherNotEqual_ValueSame(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"env": "prod",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "prod", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (prod == prod), got match")
	}
}

func TestMatcherNotEqual_MissingLabel(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "Test",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "prod", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (missing label != prod), got no match")
	}
}

func TestMatcherNotEqual_EmptyValue(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "label", Value: "", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (value != empty), got no match")
	}
}

func TestMatcherNotEqual_MultipleMatchers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"env":      "dev",
		"instance": "server-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "prod", Type: MatcherTypeNotEqual},
		{Name: "instance", Value: "server-02", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (both != checks pass), got no match")
	}
}

func TestMatcherNotEqual_UnicodeMatching(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"region": "日本",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "region", Value: "アメリカ", Type: MatcherTypeNotEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (unicode values different), got no match")
	}
}

// Regex Operator Tests (10 tests)

func TestMatcherRegex_SimplePattern(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"severity": "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "severity", Value: "critical", Type: MatcherTypeRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (simple regex), got no match")
	}
}

func TestMatcherRegex_Alternation(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	tests := []struct {
		name      string
		value     string
		wantMatch bool
	}{
		{"matches critical", "critical", true},
		{"matches warning", "warning", true},
		{"no match info", "info", false},
		{"no match debug", "debug", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := newTestAlert(map[string]string{
				"severity": tt.value,
			})

			silence := newTestSilence("s1", []Matcher{
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
			})

			matched, err := matcher.Matches(ctx, alert, silence)
			if err != nil {
				t.Fatalf("Matches() error: %v", err)
			}
			if matched != tt.wantMatch {
				t.Errorf("value=%q: got matched=%v, want %v", tt.value, matched, tt.wantMatch)
			}
		})
	}
}

func TestMatcherRegex_CharacterClass(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: "server-[0-9]+", Type: MatcherTypeRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (character class), got no match")
	}
}

func TestMatcherRegex_Quantifiers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	tests := []struct {
		pattern   string
		value     string
		wantMatch bool
	}{
		{"a+", "aaa", true},
		{"a+", "bbb", false},
		{"a*b", "aaab", true},
		{"a*b", "b", true},
		{"a?b", "ab", true},
		{"a?b", "b", true},
		{"a{2,4}", "aaa", true},
		{"a{2,4}", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.value, func(t *testing.T) {
			alert := newTestAlert(map[string]string{
				"label": tt.value,
			})

			silence := newTestSilence("s1", []Matcher{
				{Name: "label", Value: tt.pattern, Type: MatcherTypeRegex},
			})

			matched, err := matcher.Matches(ctx, alert, silence)
			if err != nil {
				t.Fatalf("Matches() error: %v", err)
			}
			if matched != tt.wantMatch {
				t.Errorf("pattern=%q value=%q: got %v, want %v", tt.pattern, tt.value, matched, tt.wantMatch)
			}
		})
	}
}

func TestMatcherRegex_Anchors(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	tests := []struct {
		pattern   string
		value     string
		wantMatch bool
	}{
		{"^start", "start-middle-end", true},
		{"^start", "middle-start-end", false},
		{"end$", "start-middle-end", true},
		{"end$", "start-end-middle", false},
		{"^exact$", "exact", true},
		{"^exact$", "not-exact", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.value, func(t *testing.T) {
			alert := newTestAlert(map[string]string{
				"label": tt.value,
			})

			silence := newTestSilence("s1", []Matcher{
				{Name: "label", Value: tt.pattern, Type: MatcherTypeRegex},
			})

			matched, err := matcher.Matches(ctx, alert, silence)
			if err != nil {
				t.Fatalf("Matches() error: %v", err)
			}
			if matched != tt.wantMatch {
				t.Errorf("pattern=%q value=%q: got %v, want %v", tt.pattern, tt.value, matched, tt.wantMatch)
			}
		})
	}
}

func TestMatcherRegex_MissingLabel(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"other": "value",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "severity", Value: "critical", Type: MatcherTypeRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (missing label), got match")
	}
}

func TestMatcherRegex_InvalidPattern(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "label", Value: "[invalid", Type: MatcherTypeRegex},
	})

	_, err := matcher.Matches(ctx, alert, silence)
	if err == nil {
		t.Error("Expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "regex pattern compilation failed") {
		t.Errorf("Expected regex compilation error, got: %v", err)
	}
}

func TestMatcherRegex_CacheHit(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	pattern := ".*-prod-.*"

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: pattern, Type: MatcherTypeRegex},
	})

	// First call: compile and cache
	matched1, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("First Matches() error: %v", err)
	}
	if !matched1 {
		t.Error("First match failed")
	}

	// Second call: cache hit (should be faster)
	matched2, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Second Matches() error: %v", err)
	}
	if !matched2 {
		t.Error("Second match failed")
	}

	// Verify cache has the pattern
	if matcher.regexCache.Size() == 0 {
		t.Error("Cache is empty, expected cached pattern")
	}
}

func TestMatcherRegex_ComplexPattern(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"email": "user@example.com",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "email", Value: `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`, Type: MatcherTypeRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (email pattern), got no match")
	}
}

func TestMatcherRegex_WildcardPattern(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"path": "/var/log/app/error.log",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "path", Value: ".*/log/.*", Type: MatcherTypeRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (wildcard), got no match")
	}
}

// NotRegex Operator Tests (6 tests)

func TestMatcherNotRegex_NotMatched(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (not dev), got no match")
	}
}

func TestMatcherNotRegex_Matched(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-dev-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (is dev), got match")
	}
}

func TestMatcherNotRegex_MissingLabel(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"other": "value",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (missing label = not matched), got no match")
	}
}

func TestMatcherNotRegex_InvalidPattern(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "label", Value: "(unclosed", Type: MatcherTypeNotRegex},
	})

	_, err := matcher.Matches(ctx, alert, silence)
	if err == nil {
		t.Error("Expected error for invalid regex, got nil")
	}
}

func TestMatcherNotRegex_CacheHit(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	pattern := ".*-test-.*"
	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: pattern, Type: MatcherTypeNotRegex},
	})

	// First call
	matched1, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("First Matches() error: %v", err)
	}
	if !matched1 {
		t.Error("First match failed")
	}

	// Second call (cache hit)
	matched2, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Second Matches() error: %v", err)
	}
	if !matched2 {
		t.Error("Second match failed")
	}
}

func TestMatcherNotRegex_MultiplePatterns(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"env":      "prod",
		"instance": "server-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "(dev|test)", Type: MatcherTypeNotRegex},
		{Name: "instance", Value: ".*-staging-.*", Type: MatcherTypeNotRegex},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (both !~ checks pass), got no match")
	}
}

// ====================
// PHASE 6: Integration Tests (14 tests)
// ====================

// Multi-Matcher Tests (8 tests)

func TestMultiMatcher_AllMatch(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"severity":  "critical",
		"env":       "prod",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
		{Name: "severity", Value: "critical", Type: MatcherTypeEqual},
		{Name: "env", Value: "prod", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (all 4 matchers), got no match")
	}
}

func TestMultiMatcher_OneFailsAllFail(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "web-server", // Different!
		"severity":  "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual}, // This fails
		{Name: "severity", Value: "critical", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if matched {
		t.Error("Expected no match (one matcher failed), got match")
	}
}

func TestMultiMatcher_MixedTypes(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"severity":  "critical",
		"env":       "prod",
		"instance":  "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},              // =
		{Name: "env", Value: "dev", Type: MatcherTypeNotEqual},                     // !=
		{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},    // =~
		{Name: "instance", Value: ".*-staging-.*", Type: MatcherTypeNotRegex},      // !~
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (mixed operators), got no match")
	}
}

func TestMultiMatcher_TenMatchers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"l1": "v1", "l2": "v2", "l3": "v3", "l4": "v4", "l5": "v5",
		"l6": "v6", "l7": "v7", "l8": "v8", "l9": "v9", "l10": "v10",
	})

	matchers := make([]Matcher, 10)
	for i := 0; i < 10; i++ {
		matchers[i] = Matcher{
			Name:  fmt.Sprintf("l%d", i+1),
			Value: fmt.Sprintf("v%d", i+1),
			Type:  MatcherTypeEqual,
		}
	}

	silence := newTestSilence("s1", matchers)

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (10 matchers), got no match")
	}
}

func TestMultiMatcher_OrderIndependent(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	})

	// Test with different matcher orders
	orders := [][]Matcher{
		{{Name: "a", Value: "1", Type: MatcherTypeEqual}, {Name: "b", Value: "2", Type: MatcherTypeEqual}, {Name: "c", Value: "3", Type: MatcherTypeEqual}},
		{{Name: "c", Value: "3", Type: MatcherTypeEqual}, {Name: "a", Value: "1", Type: MatcherTypeEqual}, {Name: "b", Value: "2", Type: MatcherTypeEqual}},
		{{Name: "b", Value: "2", Type: MatcherTypeEqual}, {Name: "c", Value: "3", Type: MatcherTypeEqual}, {Name: "a", Value: "1", Type: MatcherTypeEqual}},
	}

	for i, matchers := range orders {
		silence := newTestSilence("s"+string(rune('1'+i)), matchers)

		matched, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			t.Fatalf("Order %d: Matches() error: %v", i, err)
		}
		if !matched {
			t.Errorf("Order %d: Expected match, got no match", i)
		}
	}
}

func TestMultiMatcher_ShortCircuit(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"first": "wrong",
	})

	// First matcher fails, should not evaluate second (which has invalid regex)
	silence := newTestSilence("s1", []Matcher{
		{Name: "first", Value: "correct", Type: MatcherTypeEqual},  // This fails
		{Name: "second", Value: "[invalid", Type: MatcherTypeRegex}, // Should not be evaluated
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error (should short-circuit before regex): %v", err)
	}
	if matched {
		t.Error("Expected no match (first matcher failed), got match")
	}
}

func TestMultiMatcher_Performance(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"l1": "v1", "l2": "v2", "l3": "v3", "l4": "v4", "l5": "v5",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "l1", Value: "v1", Type: MatcherTypeEqual},
		{Name: "l2", Value: "v2", Type: MatcherTypeEqual},
		{Name: "l3", Value: "v3", Type: MatcherTypeEqual},
		{Name: "l4", Value: "v4", Type: MatcherTypeEqual},
		{Name: "l5", Value: "v5", Type: MatcherTypeEqual},
	})

	start := time.Now()
	matched, err := matcher.Matches(ctx, alert, silence)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match, got no match")
	}

	// Target: <500µs for 5 matchers
	if duration > 500*time.Microsecond {
		t.Errorf("Performance target missed: %v > 500µs", duration)
	}
}

func TestMultiMatcher_EmptyMatcherList(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	silence := newTestSilence("s1", []Matcher{}) // Empty!

	_, err := matcher.Matches(ctx, alert, silence)
	if err == nil {
		t.Error("Expected error for empty matchers, got nil")
	}
	if err != ErrInvalidSilence {
		t.Errorf("Expected ErrInvalidSilence, got: %v", err)
	}
}

// MatchesAny Tests (6 tests)

func TestMatchesAny_NoSilences(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "Test",
	})

	matchedIDs, err := matcher.MatchesAny(ctx, alert, []*Silence{})
	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matchedIDs))
	}
}

func TestMatchesAny_NoMatches(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "api-server",
	})

	silences := []*Silence{
		newTestSilence("s1", []Matcher{{Name: "job", Value: "web-server", Type: MatcherTypeEqual}}),
		newTestSilence("s2", []Matcher{{Name: "job", Value: "db-server", Type: MatcherTypeEqual}}),
	}

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 0 {
		t.Errorf("Expected 0 matches, got %d: %v", len(matchedIDs), matchedIDs)
	}
}

func TestMatchesAny_SingleMatch(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "api-server",
	})

	silences := []*Silence{
		newTestSilence("s1", []Matcher{{Name: "job", Value: "web-server", Type: MatcherTypeEqual}}),
		newTestSilence("s2", []Matcher{{Name: "job", Value: "api-server", Type: MatcherTypeEqual}}),
		newTestSilence("s3", []Matcher{{Name: "job", Value: "db-server", Type: MatcherTypeEqual}}),
	}

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 1 {
		t.Errorf("Expected 1 match, got %d: %v", len(matchedIDs), matchedIDs)
	}
	if len(matchedIDs) > 0 && matchedIDs[0] != "s2" {
		t.Errorf("Expected match s2, got %s", matchedIDs[0])
	}
}

func TestMatchesAny_MultipleMatches(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silences := []*Silence{
		newTestSilence("s1", []Matcher{{Name: "job", Value: "api-server", Type: MatcherTypeEqual}}),
		newTestSilence("s2", []Matcher{{Name: "severity", Value: "critical", Type: MatcherTypeEqual}}),
		newTestSilence("s3", []Matcher{
			{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
			{Name: "severity", Value: "critical", Type: MatcherTypeEqual},
		}),
		newTestSilence("s4", []Matcher{{Name: "job", Value: "web-server", Type: MatcherTypeEqual}}),
	}

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 3 {
		t.Errorf("Expected 3 matches, got %d: %v", len(matchedIDs), matchedIDs)
	}

	// Verify expected IDs
	expectedIDs := map[string]bool{"s1": true, "s2": true, "s3": true}
	for _, id := range matchedIDs {
		if !expectedIDs[id] {
			t.Errorf("Unexpected match ID: %s", id)
		}
	}
}

func TestMatchesAny_100Silences_Performance(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	// Create 100 silences
	silences := make([]*Silence, 100)
	for i := 0; i < 100; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
			},
		)
	}

	start := time.Now()
	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 100 {
		t.Errorf("Expected 100 matches, got %d", len(matchedIDs))
	}

	// Target: <1ms for 100 silences
	if duration > 1*time.Millisecond {
		t.Errorf("Performance target missed: %v > 1ms", duration)
	} else {
		t.Logf("Performance: %v for 100 silences (target: <1ms) ✅", duration)
	}
}

func TestMatchesAny_ContextCancellation(t *testing.T) {
	matcher := NewSilenceMatcher()

	alert := newTestAlert(map[string]string{
		"job": "api-server",
	})

	// Create many silences to increase iteration time
	silences := make([]*Silence, 1000)
	for i := 0; i < 1000; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{{Name: "job", Value: "api-server", Type: MatcherTypeEqual}},
		)
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	if err != ErrContextCancelled {
		t.Errorf("Expected ErrContextCancelled, got: %v", err)
	}

	// Should return partial results
	t.Logf("Partial results before cancellation: %d matches", len(matchedIDs))
}

// ====================
// PHASE 6: Error Handling Tests (8 tests)
// ====================

func TestMatches_NilAlert(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	var alert Alert // Zero value, Labels will be nil

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api", Type: MatcherTypeEqual},
	})

	_, err := matcher.Matches(ctx, alert, silence)
	if err != ErrInvalidAlert {
		t.Errorf("Expected ErrInvalidAlert, got: %v", err)
	}
}

func TestMatches_NilAlertLabels(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := Alert{
		Labels:      nil, // Explicitly nil
		Annotations: map[string]string{},
	}

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api", Type: MatcherTypeEqual},
	})

	_, err := matcher.Matches(ctx, alert, silence)
	if err != ErrInvalidAlert {
		t.Errorf("Expected ErrInvalidAlert, got: %v", err)
	}
}

func TestMatches_NilSilence(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "api",
	})

	_, err := matcher.Matches(ctx, alert, nil)
	if err != ErrInvalidSilence {
		t.Errorf("Expected ErrInvalidSilence, got: %v", err)
	}
}

func TestMatches_EmptyMatchers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "api",
	})

	silence := newTestSilence("s1", []Matcher{}) // Empty matchers

	_, err := matcher.Matches(ctx, alert, silence)
	if err != ErrInvalidSilence {
		t.Errorf("Expected ErrInvalidSilence, got: %v", err)
	}
}

func TestMatches_InvalidRegex(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	invalidPatterns := []string{
		"[unclosed",
		"(unclosed",
		"**",
		"(?P<invalid",
	}

	for _, pattern := range invalidPatterns {
		silence := newTestSilence("s1", []Matcher{
			{Name: "label", Value: pattern, Type: MatcherTypeRegex},
		})

		_, err := matcher.Matches(ctx, alert, silence)
		if err == nil {
			t.Errorf("Pattern %q: Expected error, got nil", pattern)
		}
		if !strings.Contains(err.Error(), "regex pattern compilation failed") {
			t.Errorf("Pattern %q: Expected regex compilation error, got: %v", pattern, err)
		}
	}
}

func TestMatches_ContextCancelled(t *testing.T) {
	matcher := NewSilenceMatcher()

	alert := newTestAlert(map[string]string{
		"job": "api",
	})

	// Create many matchers to increase iteration time
	matchers := make([]Matcher, 100)
	for i := 0; i < 100; i++ {
		matchers[i] = Matcher{
			Name:  string(rune('a' + (i % 26))),
			Value: "value",
			Type:  MatcherTypeEqual,
		}
	}

	silence := newTestSilence("s1", matchers)

	// Cancel context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := matcher.Matches(ctx, alert, silence)
	if err != ErrContextCancelled {
		t.Errorf("Expected ErrContextCancelled, got: %v", err)
	}
}

func TestMatchesAny_ContextCancelledDuringIteration(t *testing.T) {
	matcher := NewSilenceMatcher()

	alert := newTestAlert(map[string]string{
		"job": "api",
	})

	silences := make([]*Silence, 1000)
	for i := 0; i < 1000; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{{Name: "job", Value: "api", Type: MatcherTypeEqual}},
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Nanosecond)
	defer cancel()

	// Wait a tiny bit to ensure timeout triggers during iteration
	time.Sleep(100 * time.Nanosecond)

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)

	// Should get either ErrContextCancelled or partial results
	// (timing may vary, both are acceptable outcomes)
	if err != nil && err != ErrContextCancelled {
		t.Errorf("Expected nil or ErrContextCancelled, got: %v", err)
	}

	// Log results for debugging
	if err == ErrContextCancelled {
		t.Logf("Got ErrContextCancelled with %d partial matches (expected behavior)", len(matchedIDs))
	} else {
		t.Logf("Completed before timeout with %d matches (also acceptable)", len(matchedIDs))
	}
}

func TestMatchesAny_PartialErrors(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job": "api",
	})

	silences := []*Silence{
		newTestSilence("s1", []Matcher{{Name: "job", Value: "api", Type: MatcherTypeEqual}}),
		newTestSilence("s2", []Matcher{{Name: "label", Value: "[invalid", Type: MatcherTypeRegex}}), // Invalid!
		newTestSilence("s3", []Matcher{{Name: "job", Value: "api", Type: MatcherTypeEqual}}),
	}

	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}

	// Should skip invalid silence and continue
	if len(matchedIDs) != 2 {
		t.Errorf("Expected 2 matches (skipping invalid), got %d: %v", len(matchedIDs), matchedIDs)
	}
}

// ====================
// PHASE 7: Edge Cases (8 tests)
// ====================

func TestMatcher_VeryLongValue(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	// Create 1024-char value (max allowed by TN-131 validation)
	longValue := strings.Repeat("a", 1024)

	alert := newTestAlert(map[string]string{
		"long": longValue,
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "long", Value: longValue, Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (long value), got no match")
	}
}

func TestMatcher_SpecialCharactersInLabels(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"message": "Line 1\nLine 2\tTabbed",
		"path":    "C:\\Windows\\System32",
		"quote":   `He said "hello"`,
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "message", Value: "Line 1\nLine 2\tTabbed", Type: MatcherTypeEqual},
		{Name: "path", Value: "C:\\Windows\\System32", Type: MatcherTypeEqual},
		{Name: "quote", Value: `He said "hello"`, Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (special chars), got no match")
	}
}

func TestMatcher_UnicodeLabels(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"japanese": "日本語",
		"russian":  "Русский",
		"emoji":    "🚨🔥💻",
		"arabic":   "العربية",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "japanese", Value: "日本語", Type: MatcherTypeEqual},
		{Name: "russian", Value: "Русский", Type: MatcherTypeEqual},
		{Name: "emoji", Value: "🚨🔥💻", Type: MatcherTypeEqual},
		{Name: "arabic", Value: "العربية", Type: MatcherTypeEqual},
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (unicode), got no match")
	}
}

func TestMultiMatcher_100Matchers(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	// Create alert with 100 labels
	labels := make(map[string]string, 100)
	matchers := make([]Matcher, 100)
	for i := 0; i < 100; i++ {
		key := string(rune('a'+(i%26))) + string(rune('0'+(i/26)))
		value := "value" + string(rune('0'+i))
		labels[key] = value
		matchers[i] = Matcher{
			Name:  key,
			Value: value,
			Type:  MatcherTypeEqual,
		}
	}

	alert := newTestAlert(labels)
	silence := newTestSilence("s1", matchers)

	start := time.Now()
	matched, err := matcher.Matches(ctx, alert, silence)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (100 matchers), got no match")
	}

	// Target: <500µs for 100 matchers
	t.Logf("Performance: %v for 100 matchers", duration)
}

func TestMatchesAny_1000Silences_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	// Create 1000 silences (stress test)
	silences := make([]*Silence, 1000)
	for i := 0; i < 1000; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
			},
		)
	}

	start := time.Now()
	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("MatchesAny() error: %v", err)
	}
	if len(matchedIDs) != 1000 {
		t.Errorf("Expected 1000 matches, got %d", len(matchedIDs))
	}

	// Target: <10ms for 1000 silences
	if duration > 10*time.Millisecond {
		t.Errorf("Performance target missed: %v > 10ms", duration)
	} else {
		t.Logf("Performance: %v for 1000 silences (target: <10ms) ✅", duration)
	}
}

func TestMatcher_AllOperatorsInOneSilence(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"env":       "prod",
		"severity":  "critical",
		"instance":  "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},           // =
		{Name: "env", Value: "dev", Type: MatcherTypeNotEqual},                  // !=
		{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex}, // =~
		{Name: "instance", Value: ".*-test-.*", Type: MatcherTypeNotRegex},      // !~
	})

	matched, err := matcher.Matches(ctx, alert, silence)
	if err != nil {
		t.Fatalf("Matches() error: %v", err)
	}
	if !matched {
		t.Error("Expected match (all 4 operator types), got no match")
	}
}

func TestRegexCache_MaxSizeEviction(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	// Fill cache to max size (1000 patterns)
	for i := 0; i < 1050; i++ {
		pattern := "pattern" + string(rune('0'+i))
		silence := newTestSilence("s1", []Matcher{
			{Name: "label", Value: pattern, Type: MatcherTypeRegex},
		})

		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			// Expected for non-matching patterns
			continue
		}
	}

	// Cache should have been evicted at some point
	cacheSize := matcher.regexCache.Size()
	t.Logf("Cache size after 1050 patterns: %d", cacheSize)

	if cacheSize > 1000 {
		t.Errorf("Cache size exceeded max: %d > 1000", cacheSize)
	}
}

func TestMatcher_ConcurrentMatchingRaceCondition(t *testing.T) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
		{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
	})

	// Run 100 goroutines concurrently
	const goroutines = 100
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- true }()

			for j := 0; j < 10; j++ {
				matched, err := matcher.Matches(ctx, alert, silence)
				if err != nil {
					t.Errorf("Concurrent Matches() error: %v", err)
					return
				}
				if !matched {
					t.Error("Concurrent match failed")
					return
				}
			}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < goroutines; i++ {
		<-done
	}
}
