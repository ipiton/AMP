package inhibition

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// --- Mock ActiveAlertCache for testing ---

type mockCache struct {
	firingAlerts []*core.Alert
	err          error
}

func (m *mockCache) GetFiringAlerts(ctx context.Context) ([]*core.Alert, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.firingAlerts, nil
}

func (m *mockCache) AddFiringAlert(ctx context.Context, alert *core.Alert) error {
	m.firingAlerts = append(m.firingAlerts, alert)
	return nil
}

func (m *mockCache) RemoveAlert(ctx context.Context, fingerprint string) error {
	return nil
}

// --- Test Helpers ---

func createTestAlert(name, severity, node, cluster string) *core.Alert {
	return &core.Alert{
		AlertName:   name,
		Fingerprint: fmt.Sprintf("fp-%s-%s", name, node),
		Labels: map[string]string{
			"alertname": name,
			"severity":  severity,
			"node":      node,
			"cluster":   cluster,
		},
		Status: "firing",
	}
}

func createTestRule(name string) InhibitionRule {
	rule := InhibitionRule{
		Name: name,
		SourceMatch: map[string]string{
			"alertname": "NodeDown",
			"severity":  "critical",
		},
		TargetMatch: map[string]string{
			"alertname": "InstanceDown",
		},
		Equal: []string{"node", "cluster"},
	}
	rule.compiledSourceRE = make(map[string]*regexp.Regexp)
	rule.compiledTargetRE = make(map[string]*regexp.Regexp)
	return rule
}

// --- Happy Path Tests ---

func TestShouldInhibit_BasicMatch(t *testing.T) {
	// Setup
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	// Execute
	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	// Assert
	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if !result.Matched {
		t.Error("Expected match, got no match")
	}

	if result.InhibitedBy == nil {
		t.Error("Expected InhibitedBy to be set")
	}

	if result.InhibitedBy.AlertName != "NodeDown" {
		t.Errorf("Expected inhibitor to be NodeDown, got %s", result.InhibitedBy.AlertName)
	}

	if result.Rule == nil {
		t.Error("Expected Rule to be set")
	}
}

func TestShouldInhibit_NoMatch_DifferentNode(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node2", "prod") // Different node

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if result.Matched {
		t.Error("Expected no match (different node), got match")
	}
}

func TestShouldInhibit_NoMatch_DifferentAlertName(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("DiskFull", "warning", "node1", "prod") // Different alertname

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if result.Matched {
		t.Error("Expected no match (different alertname), got match")
	}
}

func TestShouldInhibit_NoFiringAlerts(t *testing.T) {
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{}, // Empty
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if result.Matched {
		t.Error("Expected no match (no firing alerts), got match")
	}
}

func TestShouldInhibit_CacheError(t *testing.T) {
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		err: fmt.Errorf("cache error"),
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	_, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err == nil {
		t.Error("Expected error from cache, got nil")
	}
}

func TestShouldInhibit_MultipleRules_FirstMatch(t *testing.T) {
	sourceAlert1 := createTestAlert("NodeDown", "critical", "node1", "prod")
	sourceAlert2 := createTestAlert("ClusterDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert1, sourceAlert2},
	}

	rule1 := createTestRule("rule1")
	rule2 := createTestRule("rule2")
	rule2.SourceMatch = map[string]string{"alertname": "ClusterDown"}

	matcher := NewMatcher(cache, []InhibitionRule{rule1, rule2}, nil)

	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if !result.Matched {
		t.Error("Expected match, got no match")
	}

	// Should match first rule (NodeDown)
	if result.InhibitedBy.AlertName != "NodeDown" {
		t.Errorf("Expected inhibitor to be NodeDown (first match), got %s", result.InhibitedBy.AlertName)
	}
}

func TestFindInhibitors_MultipleMatches(t *testing.T) {
	sourceAlert1 := createTestAlert("NodeDown", "critical", "node1", "prod")
	sourceAlert2 := createTestAlert("NodeDown", "critical", "node1", "prod") // Duplicate
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert1, sourceAlert2},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	results, err := matcher.FindInhibitors(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("FindInhibitors() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 inhibitors, got %d", len(results))
	}
}

func TestFindInhibitors_NoMatches(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node2", "prod") // Different node

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	results, err := matcher.FindInhibitors(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("FindInhibitors() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 inhibitors, got %d", len(results))
	}
}

// --- MatchRule Tests ---

func TestMatchRule_ExactMatch(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if !matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected rule to match")
	}
}

func TestMatchRule_SourceMismatch(t *testing.T) {
	sourceAlert := createTestAlert("DiskFull", "warning", "node1", "prod") // Wrong alertname
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (source mismatch)")
	}
}

func TestMatchRule_TargetMismatch(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("DiskFull", "warning", "node1", "prod") // Wrong alertname

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (target mismatch)")
	}
}

func TestMatchRule_EqualLabelMismatch(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node2", "prod") // Different node

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (equal label mismatch)")
	}
}

func TestMatchRule_MissingEqualLabel(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := &core.Alert{
		AlertName:   "InstanceDown",
		Fingerprint: "fp-test",
		Labels: map[string]string{
			"alertname": "InstanceDown",
			// Missing "node" label
			"cluster": "prod",
		},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (missing equal label)")
	}
}

func TestMatchRule_RegexMatch(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname": "ServiceDown",
			"service":   "api-backend", // Matches ^api.*
			"cluster":   "prod",
		},
	}

	targetAlert := &core.Alert{
		AlertName:   "HighLatency",
		Fingerprint: "fp-latency",
		Labels: map[string]string{
			"alertname": "HighLatency",
			"severity":  "warning", // Matches warning|info
			"cluster":   "prod",
		},
	}

	rule := InhibitionRule{
		Name:          "regex-rule",
		SourceMatchRE: map[string]string{"service": "^api.*"},
		TargetMatchRE: map[string]string{"severity": "warning|info"},
		Equal:         []string{"cluster"},
	}

	// Compile regex
	rule.setCompiledSourceRE("service", regexp.MustCompile("^api.*"))
	rule.setCompiledTargetRE("severity", regexp.MustCompile("warning|info"))

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if !matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected regex rule to match")
	}
}

func TestMatchRule_RegexNoMatch(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname": "ServiceDown",
			"service":   "database", // Does NOT match ^api.*
			"cluster":   "prod",
		},
	}

	targetAlert := &core.Alert{
		AlertName:   "HighLatency",
		Fingerprint: "fp-latency",
		Labels: map[string]string{
			"alertname": "HighLatency",
			"severity":  "warning",
			"cluster":   "prod",
		},
	}

	rule := InhibitionRule{
		Name:          "regex-rule",
		SourceMatchRE: map[string]string{"service": "^api.*"},
		TargetMatchRE: map[string]string{"severity": "warning|info"},
		Equal:         []string{"cluster"},
	}

	rule.setCompiledSourceRE("service", regexp.MustCompile("^api.*"))
	rule.setCompiledTargetRE("severity", regexp.MustCompile("warning|info"))

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (regex mismatch)")
	}
}

// --- Performance Tests ---

func TestShouldInhibit_Performance(t *testing.T) {
	// Create 100 firing alerts
	firingAlerts := make([]*core.Alert, 100)
	for i := 0; i < 100; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod")
	}

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	// Create 10 rules
	rules := make([]InhibitionRule, 10)
	for i := 0; i < 10; i++ {
		rules[i] = createTestRule(fmt.Sprintf("rule%d", i))
	}

	matcher := NewMatcher(cache, rules, nil)

	targetAlert := createTestAlert("TargetAlert", "info", "node99", "prod")

	start := time.Now()
	_, err := matcher.ShouldInhibit(context.Background(), targetAlert)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	// Target: <5ms (realistic for 100 alerts x 10 rules = 1000 operations)
	// With optimizations: typically <2ms, but allow 5ms for slower CI environments
	target := 5 * time.Millisecond
	if duration > target {
		t.Errorf("Performance target missed: took %v (target < %v)", duration, target)
	}

	t.Logf("Performance: %v for 100 alerts x 10 rules (target < %v)", duration, target)
}

// --- Edge Cases & Coverage Tests ---

// TestShouldInhibit_ContextCancellation tests early exit on cancelled context
func TestShouldInhibit_ContextCancellation(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Execute
	_, err := matcher.ShouldInhibit(ctx, targetAlert)

	// Assert - should return context error
	if err == nil {
		t.Error("Expected context error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

// TestFindInhibitors_ContextCancellation tests early exit on cancelled context
func TestFindInhibitors_ContextCancellation(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Execute
	_, err := matcher.FindInhibitors(ctx, targetAlert)

	// Assert
	if err == nil {
		t.Error("Expected context error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

// TestShouldInhibit_EmptyFiringAlerts_FastPath tests the empty alerts optimization
func TestShouldInhibit_EmptyFiringAlerts_FastPath(t *testing.T) {
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{}, // Empty
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	start := time.Now()
	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if result.Matched {
		t.Error("Expected no match (empty firing alerts)")
	}

	// Should be very fast (fast path)
	if duration > 100*time.Microsecond {
		t.Errorf("Fast path too slow: %v (expected <100µs)", duration)
	}
}

// TestFindInhibitors_EmptyFiringAlerts tests empty alerts path
func TestFindInhibitors_EmptyFiringAlerts(t *testing.T) {
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	results, err := matcher.FindInhibitors(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("FindInhibitors() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 inhibitors (empty alerts), got %d", len(results))
	}
}

// TestShouldInhibit_PrefilterOptimization tests alertname pre-filtering
func TestShouldInhibit_PrefilterOptimization(t *testing.T) {
	// Create 50 alerts with different alertnames
	firingAlerts := make([]*core.Alert, 50)
	for i := 0; i < 50; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod")
	}

	// Add ONE NodeDown alert (the one we're looking for)
	nodeDownAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	firingAlerts = append(firingAlerts, nodeDownAlert)

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("prefilter-test")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	start := time.Now()
	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if !result.Matched {
		t.Error("Expected match (should find NodeDown alert)")
	}

	// Should be fast due to pre-filtering (not checking all 50 irrelevant alerts)
	if duration > 500*time.Microsecond {
		t.Errorf("Pre-filter optimization not working: took %v", duration)
	}

	t.Logf("Pre-filter performance: %v for 51 alerts (only 1 relevant)", duration)
}

// TestShouldInhibit_NoAlertnameFilter tests path without alertname pre-filtering
func TestShouldInhibit_NoAlertnameFilter(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	// Rule WITHOUT alertname in source_match (no pre-filtering)
	rule := InhibitionRule{
		Name: "no-alertname-filter",
		SourceMatch: map[string]string{
			"severity": "critical", // No alertname!
		},
		TargetMatch: map[string]string{
			"alertname": "InstanceDown",
		},
		Equal: []string{"node", "cluster"},
	}
	rule.compiledSourceRE = make(map[string]*regexp.Regexp)
	rule.compiledTargetRE = make(map[string]*regexp.Regexp)

	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	result, err := matcher.ShouldInhibit(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("ShouldInhibit() error = %v", err)
	}

	if !result.Matched {
		t.Error("Expected match (rule without alertname filter)")
	}
}

// TestMatchRuleFast_AllConditions tests all conditions in matchRuleFast
func TestMatchRuleFast_AllConditions(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname":   "ServiceDown",
			"severity":    "critical",
			"service":     "api-backend", // Matches ^api.*
			"environment": "production",  // Matches prod.*
			"cluster":     "prod-eu",
		},
	}

	targetAlert := &core.Alert{
		AlertName:   "HighLatency",
		Fingerprint: "fp-latency",
		Labels: map[string]string{
			"alertname":   "HighLatency",
			"severity":    "warning",      // Matches warning|info
			"component":   "api-gateway",  // Matches .*gateway
			"environment": "production",
			"cluster":     "prod-eu",
		},
	}

	// Rule with ALL types of conditions
	rule := InhibitionRule{
		Name: "complex-rule",
		SourceMatch: map[string]string{
			"alertname": "ServiceDown",
			"severity":  "critical",
		},
		SourceMatchRE: map[string]string{
			"service":     "^api.*",
			"environment": "prod.*",
		},
		TargetMatch: map[string]string{
			"alertname": "HighLatency",
		},
		TargetMatchRE: map[string]string{
			"severity":  "warning|info",
			"component": ".*gateway",
		},
		Equal: []string{"cluster", "environment"},
	}

	// Compile regex
	rule.setCompiledSourceRE("service", regexp.MustCompile("^api.*"))
	rule.setCompiledSourceRE("environment", regexp.MustCompile("prod.*"))
	rule.setCompiledTargetRE("severity", regexp.MustCompile("warning|info"))
	rule.setCompiledTargetRE("component", regexp.MustCompile(".*gateway"))

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Test match
	if !matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected complex rule to match (all conditions satisfied)")
	}

	// Test mismatch - change cluster
	targetAlert.Labels["cluster"] = "prod-us" // Different cluster
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (cluster mismatch)")
	}
}

// TestFindInhibitors_PrefilterOptimization tests pre-filtering in FindInhibitors
func TestFindInhibitors_PrefilterOptimization(t *testing.T) {
	// Create 30 alerts with different alertnames
	firingAlerts := make([]*core.Alert, 30)
	for i := 0; i < 30; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", "node1", "prod")
	}

	// Add 3 NodeDown alerts (matching ones)
	for i := 0; i < 3; i++ {
		nodeDownAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
		nodeDownAlert.Fingerprint = fmt.Sprintf("fp-NodeDown-node1-%d", i) // Unique fingerprints
		firingAlerts = append(firingAlerts, nodeDownAlert)
	}

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("prefilter-find")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	results, err := matcher.FindInhibitors(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("FindInhibitors() error = %v", err)
	}

	// Should find all 3 NodeDown alerts
	if len(results) != 3 {
		t.Errorf("Expected 3 inhibitors, got %d", len(results))
	}

	// All should be matched
	for i, result := range results {
		if !result.Matched {
			t.Errorf("Result %d: Expected matched=true", i)
		}
		if result.InhibitedBy.AlertName != "NodeDown" {
			t.Errorf("Result %d: Expected NodeDown alert, got %s", i, result.InhibitedBy.AlertName)
		}
	}
}

// TestFindInhibitors_MultipleRulesMatching tests multiple rules with multiple matches
func TestFindInhibitors_MultipleRulesMatching(t *testing.T) {
	sourceAlert1 := createTestAlert("NodeDown", "critical", "node1", "prod")
	sourceAlert2 := createTestAlert("ClusterDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert1, sourceAlert2},
	}

	// Two rules that both match
	rule1 := createTestRule("rule1") // Matches NodeDown
	rule2 := InhibitionRule{
		Name: "rule2",
		SourceMatch: map[string]string{
			"alertname": "ClusterDown",
			"severity":  "critical",
		},
		TargetMatch: map[string]string{
			"alertname": "InstanceDown",
		},
		Equal: []string{"node", "cluster"},
	}
	rule2.compiledSourceRE = make(map[string]*regexp.Regexp)
	rule2.compiledTargetRE = make(map[string]*regexp.Regexp)

	matcher := NewMatcher(cache, []InhibitionRule{rule1, rule2}, nil)

	results, err := matcher.FindInhibitors(context.Background(), targetAlert)

	if err != nil {
		t.Fatalf("FindInhibitors() error = %v", err)
	}

	// Should find both inhibitors
	if len(results) != 2 {
		t.Errorf("Expected 2 inhibitors (one per rule), got %d", len(results))
	}
}

// TestMatchRuleFast_MissingLabelInSource tests missing label in source alert
func TestMatchRuleFast_MissingLabelInSource(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "NodeDown",
		Fingerprint: "fp-node",
		Labels: map[string]string{
			"alertname": "NodeDown",
			// Missing "severity" label!
		},
	}

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	rule := createTestRule("missing-source-label")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Should NOT match (source missing required label)
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (source missing severity label)")
	}
}

// TestMatchRuleFast_MissingLabelInTarget tests missing label in target alert
func TestMatchRuleFast_MissingLabelInTarget(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")

	targetAlert := &core.Alert{
		AlertName:   "InstanceDown",
		Fingerprint: "fp-instance",
		Labels: map[string]string{
			// Missing "alertname" label!
			"node":    "node1",
			"cluster": "prod",
		},
	}

	rule := createTestRule("missing-target-label")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Should NOT match (target missing alertname)
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (target missing alertname)")
	}
}

// TestMatchRuleFast_MissingRegexLabel tests missing label for regex check
func TestMatchRuleFast_MissingRegexLabel(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname": "ServiceDown",
			// Missing "service" label for regex check!
		},
	}

	targetAlert := createTestAlert("HighLatency", "warning", "node1", "prod")

	rule := InhibitionRule{
		Name:          "missing-regex-label",
		SourceMatchRE: map[string]string{"service": "^api.*"},
		TargetMatch:   map[string]string{"alertname": "HighLatency"},
	}
	rule.setCompiledSourceRE("service", regexp.MustCompile("^api.*"))

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Should NOT match (source missing label for regex)
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (source missing service label for regex)")
	}
}

// TestMatchRuleFast_EmptyConditions tests empty source/target conditions
func TestMatchRuleFast_EmptyConditions(t *testing.T) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	// Rule with ONLY equal labels (empty source/target match)
	rule := InhibitionRule{
		Name:        "empty-conditions",
		SourceMatch: map[string]string{}, // Empty
		TargetMatch: map[string]string{}, // Empty
		Equal:       []string{"cluster"},
	}
	rule.compiledSourceRE = make(map[string]*regexp.Regexp)
	rule.compiledTargetRE = make(map[string]*regexp.Regexp)

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Should match (empty conditions = always true, only equal matters)
	if !matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected match (empty source/target conditions with matching equal labels)")
	}

	// Test mismatch in equal labels
	targetAlert.Labels["cluster"] = "staging" // Different cluster
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (equal label mismatch)")
	}
}

// TestMatchRuleFast_MissingRegexCompilation tests handling of missing compiled regex
func TestMatchRuleFast_MissingRegexCompilation(t *testing.T) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname": "ServiceDown",
			"service":   "api-backend",
		},
	}

	targetAlert := &core.Alert{
		AlertName:   "HighLatency",
		Fingerprint: "fp-latency",
		Labels: map[string]string{
			"alertname": "HighLatency",
		},
	}

	rule := InhibitionRule{
		Name:          "missing-regex",
		SourceMatchRE: map[string]string{"service": "^api.*"},
		TargetMatch:   map[string]string{"alertname": "HighLatency"},
	}
	// Deliberately NOT compiling the regex to test error path
	rule.compiledSourceRE = make(map[string]*regexp.Regexp)
	rule.compiledTargetRE = make(map[string]*regexp.Regexp)
	// compiledSourceRE["service"] is missing!

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	// Should NOT match (regex not compiled)
	if matcher.matchRuleFast(&rule, sourceAlert, targetAlert) {
		t.Error("Expected no match (regex not compiled)")
	}
}

// --- Benchmarks ---

func BenchmarkShouldInhibit_SingleRule(b *testing.B) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

func BenchmarkShouldInhibit_100Alerts_10Rules(b *testing.B) {
	// Create 100 firing alerts
	firingAlerts := make([]*core.Alert, 100)
	for i := 0; i < 100; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod")
	}

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	// Create 10 rules
	rules := make([]InhibitionRule, 10)
	for i := 0; i < 10; i++ {
		rules[i] = createTestRule(fmt.Sprintf("rule%d", i))
	}

	matcher := NewMatcher(cache, rules, nil)
	targetAlert := createTestAlert("TargetAlert", "info", "node99", "prod")

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

func BenchmarkMatchRule(b *testing.B) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = matcher.MatchRule(&rule, sourceAlert, targetAlert)
	}
}

func BenchmarkFindInhibitors(b *testing.B) {
	sourceAlert1 := createTestAlert("NodeDown", "critical", "node1", "prod")
	sourceAlert2 := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{sourceAlert1, sourceAlert2},
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.FindInhibitors(ctx, targetAlert)
	}
}

// --- Additional Advanced Benchmarks for 150% Quality ---

// BenchmarkShouldInhibit_NoMatch measures worst-case performance (no match found)
func BenchmarkShouldInhibit_NoMatch(b *testing.B) {
	// Create 50 alerts that DON'T match
	firingAlerts := make([]*core.Alert, 50)
	for i := 0; i < 50; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod")
	}

	targetAlert := createTestAlert("InstanceDown", "warning", "node999", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

// BenchmarkShouldInhibit_EarlyMatch measures best-case performance (first alert matches)
func BenchmarkShouldInhibit_EarlyMatch(b *testing.B) {
	// First alert matches, rest don't
	matchingAlert := createTestAlert("NodeDown", "critical", "node1", "prod")

	firingAlerts := []*core.Alert{matchingAlert}
	for i := 0; i < 49; i++ {
		firingAlerts = append(firingAlerts, createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod"))
	}

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

// BenchmarkShouldInhibit_1000Alerts_100Rules measures extreme stress test
func BenchmarkShouldInhibit_1000Alerts_100Rules(b *testing.B) {
	// Create 1000 firing alerts
	firingAlerts := make([]*core.Alert, 1000)
	for i := 0; i < 1000; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i%100), "prod")
	}

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	// Create 100 rules
	rules := make([]InhibitionRule, 100)
	for i := 0; i < 100; i++ {
		rules[i] = createTestRule(fmt.Sprintf("rule%d", i))
	}

	matcher := NewMatcher(cache, rules, nil)
	targetAlert := createTestAlert("TargetAlert", "info", "node50", "prod")

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

// BenchmarkMatchRuleFast benchmarks the optimized matchRuleFast method
func BenchmarkMatchRuleFast(b *testing.B) {
	sourceAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	rule := createTestRule("test-rule")
	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = matcher.matchRuleFast(&rule, sourceAlert, targetAlert)
	}
}

// BenchmarkMatchRule_Regex benchmarks regex-heavy rule matching
func BenchmarkMatchRule_Regex(b *testing.B) {
	sourceAlert := &core.Alert{
		AlertName:   "ServiceDown",
		Fingerprint: "fp-service",
		Labels: map[string]string{
			"alertname":   "ServiceDown",
			"service":     "api-backend-v2",
			"environment": "production-eu-west-1",
			"cluster":     "prod-k8s-01",
		},
	}

	targetAlert := &core.Alert{
		AlertName:   "HighLatency",
		Fingerprint: "fp-latency",
		Labels: map[string]string{
			"alertname":   "HighLatency",
			"severity":    "warning",
			"component":   "api-gateway-v2",
			"environment": "production-eu-west-1",
			"cluster":     "prod-k8s-01",
		},
	}

	// Regex-heavy rule
	rule := InhibitionRule{
		Name: "regex-heavy",
		SourceMatchRE: map[string]string{
			"service":     "^api-.*-v[0-9]+$",
			"environment": "^production-[a-z]+-[a-z]+-[0-9]+$",
		},
		TargetMatchRE: map[string]string{
			"severity":  "warning|info|critical",
			"component": "^api-.*-v[0-9]+$",
		},
		Equal: []string{"cluster", "environment"},
	}
	rule.setCompiledSourceRE("service", regexp.MustCompile("^api-.*-v[0-9]+$"))
	rule.setCompiledSourceRE("environment", regexp.MustCompile("^production-[a-z]+-[a-z]+-[0-9]+$"))
	rule.setCompiledTargetRE("severity", regexp.MustCompile("warning|info|critical"))
	rule.setCompiledTargetRE("component", regexp.MustCompile("^api-.*-v[0-9]+$"))

	matcher := NewMatcher(&mockCache{}, []InhibitionRule{rule}, nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = matcher.MatchRule(&rule, sourceAlert, targetAlert)
	}
}

// BenchmarkShouldInhibit_PrefilterOptimization benchmarks pre-filtering efficiency
func BenchmarkShouldInhibit_PrefilterOptimization(b *testing.B) {
	// Create 200 alerts with different alertnames
	firingAlerts := make([]*core.Alert, 200)
	for i := 0; i < 200; i++ {
		firingAlerts[i] = createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod")
	}

	// Add ONE matching NodeDown alert
	matchingAlert := createTestAlert("NodeDown", "critical", "node1", "prod")
	firingAlerts = append(firingAlerts, matchingAlert)

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("prefilter-bench")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}

// BenchmarkFindInhibitors_MultipleMatches benchmarks finding all inhibitors
func BenchmarkFindInhibitors_MultipleMatches(b *testing.B) {
	// Create 10 matching NodeDown alerts
	firingAlerts := make([]*core.Alert, 10)
	for i := 0; i < 10; i++ {
		alert := createTestAlert("NodeDown", "critical", "node1", "prod")
		alert.Fingerprint = fmt.Sprintf("fp-NodeDown-node1-%d", i)
		firingAlerts[i] = alert
	}

	// Add 90 non-matching alerts
	for i := 0; i < 90; i++ {
		firingAlerts = append(firingAlerts, createTestAlert(fmt.Sprintf("Alert%d", i), "warning", fmt.Sprintf("node%d", i), "prod"))
	}

	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: firingAlerts,
	}

	rule := createTestRule("find-all")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.FindInhibitors(ctx, targetAlert)
	}
}

// BenchmarkShouldInhibit_EmptyCache benchmarks fast path with no firing alerts
func BenchmarkShouldInhibit_EmptyCache(b *testing.B) {
	targetAlert := createTestAlert("InstanceDown", "warning", "node1", "prod")

	cache := &mockCache{
		firingAlerts: []*core.Alert{}, // Empty
	}

	rule := createTestRule("test-rule")
	matcher := NewMatcher(cache, []InhibitionRule{rule}, nil)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = matcher.ShouldInhibit(ctx, targetAlert)
	}
}
