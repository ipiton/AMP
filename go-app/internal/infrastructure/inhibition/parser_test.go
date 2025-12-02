package inhibition

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Test Helpers ---

// generateValidConfig generates a valid inhibition configuration for testing.
func generateValidConfig() []byte {
	return []byte(`
inhibit_rules:
  - name: "node-down-inhibits-instance-down"
    source_match:
      alertname: "NodeDown"
      severity: "critical"
    target_match:
      alertname: "InstanceDown"
    equal:
      - node
      - cluster
  - name: "critical-inhibits-warning"
    source_match:
      severity: "critical"
    target_match_re:
      severity: "warning|info"
    equal:
      - service
`)
}

// generateMinimalConfig generates a minimal valid configuration.
func generateMinimalConfig() []byte {
	return []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
`)
}

// generateInvalidYAML generates invalid YAML syntax.
func generateInvalidYAML() []byte {
	return []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown
    target_match:  # Missing closing quote above
      alertname: "InstanceDown"
`)
}

// generateLargeConfig generates a configuration with N rules.
func generateLargeConfig(numRules int) []byte {
	var sb strings.Builder
	sb.WriteString("inhibit_rules:\n")

	for i := 0; i < numRules; i++ {
		sb.WriteString(fmt.Sprintf(`  - name: "rule-%d"
    source_match:
      alertname: "Alert%d"
    target_match:
      alertname: "Target%d"
    equal:
      - cluster
`, i, i, i))
	}

	return []byte(sb.String())
}

// --- Happy Path Tests ---

func TestParse_ValidConfig(t *testing.T) {
	parser := NewParser()
	config, err := parser.Parse(generateValidConfig())

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if config == nil {
		t.Fatal("Config is nil")
	}

	if len(config.Rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(config.Rules))
	}

	// Check first rule
	rule1 := config.Rules[0]
	if rule1.Name != "node-down-inhibits-instance-down" {
		t.Errorf("Expected rule name 'node-down-inhibits-instance-down', got %q", rule1.Name)
	}

	if len(rule1.SourceMatch) != 2 {
		t.Errorf("Expected 2 source_match entries, got %d", len(rule1.SourceMatch))
	}

	if rule1.SourceMatch["alertname"] != "NodeDown" {
		t.Errorf("Expected alertname=NodeDown, got %s", rule1.SourceMatch["alertname"])
	}

	if len(rule1.Equal) != 2 {
		t.Errorf("Expected 2 equal labels, got %d", len(rule1.Equal))
	}
}

func TestParse_MultipleRules(t *testing.T) {
	parser := NewParser()
	config, err := parser.Parse(generateValidConfig())

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if len(config.Rules) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(config.Rules))
	}

	// Verify both rules are parsed
	names := []string{config.Rules[0].Name, config.Rules[1].Name}
	expectedNames := []string{"node-down-inhibits-instance-down", "critical-inhibits-warning"}

	for i, expected := range expectedNames {
		if names[i] != expected {
			t.Errorf("Rule %d: expected name %q, got %q", i, expected, names[i])
		}
	}
}

func TestParse_AllFields(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - name: "test-rule"
    source_match:
      alertname: "SourceAlert"
      severity: "critical"
    source_match_re:
      service: "^api.*"
    target_match:
      alertname: "TargetAlert"
    target_match_re:
      severity: "warning|info"
    equal:
      - cluster
      - namespace
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	rule := config.Rules[0]

	// Check all fields
	if rule.Name != "test-rule" {
		t.Errorf("Name: expected 'test-rule', got %q", rule.Name)
	}

	if len(rule.SourceMatch) != 2 {
		t.Errorf("SourceMatch: expected 2 entries, got %d", len(rule.SourceMatch))
	}

	if len(rule.SourceMatchRE) != 1 {
		t.Errorf("SourceMatchRE: expected 1 entry, got %d", len(rule.SourceMatchRE))
	}

	if len(rule.TargetMatch) != 1 {
		t.Errorf("TargetMatch: expected 1 entry, got %d", len(rule.TargetMatch))
	}

	if len(rule.TargetMatchRE) != 1 {
		t.Errorf("TargetMatchRE: expected 1 entry, got %d", len(rule.TargetMatchRE))
	}

	if len(rule.Equal) != 2 {
		t.Errorf("Equal: expected 2 entries, got %d", len(rule.Equal))
	}

	// Check compiled regex
	if rule.GetCompiledSourceRE("service") == nil {
		t.Error("Source regex not compiled")
	}

	if rule.GetCompiledTargetRE("severity") == nil {
		t.Error("Target regex not compiled")
	}
}

func TestParse_MinimalRule(t *testing.T) {
	parser := NewParser()
	config, err := parser.Parse(generateMinimalConfig())

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if len(config.Rules) != 1 {
		t.Fatalf("Expected 1 rule, got %d", len(config.Rules))
	}

	rule := config.Rules[0]

	// Minimal rule should have source_match and target_match
	if len(rule.SourceMatch) == 0 {
		t.Error("SourceMatch is empty")
	}

	if len(rule.TargetMatch) == 0 {
		t.Error("TargetMatch is empty")
	}

	// Equal can be nil or empty (YAML didn't specify it)
	// This is expected behavior - no initialization needed if not in YAML
}

func TestParseFile_Success(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "inhibition.yaml")

	err := os.WriteFile(tmpFile, generateValidConfig(), 0644)
	if err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// Parse file
	parser := NewParser()
	config, err := parser.ParseFile(tmpFile)

	if err != nil {
		t.Fatalf("ParseFile() failed: %v", err)
	}

	if config.SourceFile != tmpFile {
		t.Errorf("SourceFile: expected %q, got %q", tmpFile, config.SourceFile)
	}

	if len(config.Rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(config.Rules))
	}
}

func TestParseString_Success(t *testing.T) {
	parser := NewParser()
	yamlStr := string(generateValidConfig())

	config, err := parser.ParseString(yamlStr)

	if err != nil {
		t.Fatalf("ParseString() failed: %v", err)
	}

	if len(config.Rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(config.Rules))
	}
}

func TestParseReader_Success(t *testing.T) {
	parser := NewParser()
	reader := bytes.NewReader(generateValidConfig())

	config, err := parser.ParseReader(reader)

	if err != nil {
		t.Fatalf("ParseReader() failed: %v", err)
	}

	if len(config.Rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(config.Rules))
	}
}

func TestParse_EmptyMatchers(t *testing.T) {
	// Empty matchers are valid (maps can be empty)
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match_re:
      alertname: ".*"
    equal: []
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	rule := config.Rules[0]

	if len(rule.Equal) != 0 {
		t.Errorf("Equal should be empty, got %d entries", len(rule.Equal))
	}
}

func TestParse_RegexPatterns(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match_re:
      service: "^(api|web).*"
      environment: "prod.*"
    target_match:
      alertname: "TargetAlert"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	rule := config.Rules[0]

	// Check regex compilation
	serviceRE := rule.GetCompiledSourceRE("service")
	if serviceRE == nil {
		t.Fatal("service regex not compiled")
	}

	// Test regex matching
	if !serviceRE.MatchString("api-server") {
		t.Error("Regex should match 'api-server'")
	}

	if !serviceRE.MatchString("web-frontend") {
		t.Error("Regex should match 'web-frontend'")
	}

	if serviceRE.MatchString("database") {
		t.Error("Regex should not match 'database'")
	}
}

func TestParse_EqualLabels(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
    equal:
      - cluster
      - datacenter
      - environment
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	rule := config.Rules[0]

	expectedEqual := []string{"cluster", "datacenter", "environment"}
	if len(rule.Equal) != len(expectedEqual) {
		t.Fatalf("Expected %d equal labels, got %d", len(expectedEqual), len(rule.Equal))
	}

	for i, expected := range expectedEqual {
		if rule.Equal[i] != expected {
			t.Errorf("Equal[%d]: expected %q, got %q", i, expected, rule.Equal[i])
		}
	}
}

// --- Error Handling Tests ---

func TestParse_InvalidYAML(t *testing.T) {
	parser := NewParser()
	_, err := parser.Parse(generateInvalidYAML())

	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}

	if !IsParseError(err) {
		t.Errorf("Expected ParseError, got %T", err)
	}

	parseErr := GetParseError(err)
	if parseErr == nil {
		t.Fatal("GetParseError returned nil")
	}

	if parseErr.Field != "config" {
		t.Errorf("Expected field 'config', got %q", parseErr.Field)
	}
}

func TestParse_MissingSourceMatch(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - target_match:
      alertname: "TargetAlert"
`)

	parser := NewParser()
	_, err := parser.Parse(yaml)

	if err == nil {
		t.Fatal("Expected error for missing source conditions, got nil")
	}

	if !IsConfigError(err) {
		t.Errorf("Expected ConfigError, got %T", err)
	}
}

func TestParse_MissingTargetMatch(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "SourceAlert"
`)

	parser := NewParser()
	_, err := parser.Parse(yaml)

	if err == nil {
		t.Fatal("Expected error for missing target conditions, got nil")
	}

	if !IsConfigError(err) {
		t.Errorf("Expected ConfigError, got %T", err)
	}
}

func TestParse_InvalidRegex(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match_re:
      service: "^(unclosed"
    target_match:
      alertname: "TargetAlert"
`)

	parser := NewParser()
	_, err := parser.Parse(yaml)

	if err == nil {
		t.Fatal("Expected error for invalid regex, got nil")
	}

	if !IsParseError(err) {
		t.Errorf("Expected ParseError, got %T", err)
	}

	parseErr := GetParseError(err)
	if !strings.Contains(parseErr.Field, "source_match_re") {
		t.Errorf("Expected field to contain 'source_match_re', got %q", parseErr.Field)
	}
}

func TestParse_InvalidLabelName(t *testing.T) {
	testCases := []struct {
		name     string
		yaml     string
		wantErr  bool
		errField string
	}{
		{
			name: "invalid equal label (starts with number)",
			yaml: `
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
    equal:
      - 123invalid
`,
			wantErr:  true,
			errField: "equal",
		},
		{
			name: "invalid source_match label (special chars)",
			yaml: `
inhibit_rules:
  - source_match:
      alert-name: "NodeDown"
    target_match:
      alertname: "InstanceDown"
`,
			wantErr:  true,
			errField: "source_match",
		},
	}

	parser := NewParser()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.Parse([]byte(tc.yaml))

			if tc.wantErr && err == nil {
				t.Fatal("Expected error, got nil")
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			if tc.wantErr && err != nil {
				// For ConfigError, check the detailed error message
				if configErr := GetConfigError(err); configErr != nil {
					errStr := configErr.DetailedError()
					if !strings.Contains(errStr, tc.errField) {
						t.Errorf("Expected error to mention %q, got: %v", tc.errField, errStr)
					}
				} else {
					t.Errorf("Expected ConfigError, got %T: %v", err, err)
				}
			}
		})
	}
}

func TestParse_EmptyConfig(t *testing.T) {
	yaml := []byte(`
inhibit_rules: []
`)

	parser := NewParser()
	_, err := parser.Parse(yaml)

	if err == nil {
		t.Fatal("Expected error for empty config, got nil")
	}

	if !IsConfigError(err) {
		t.Errorf("Expected ConfigError, got %T", err)
	}
}

func TestParseFile_FileNotFound(t *testing.T) {
	parser := NewParser()
	_, err := parser.ParseFile("/nonexistent/file.yaml")

	if err == nil {
		t.Fatal("Expected error for nonexistent file, got nil")
	}

	if !strings.Contains(err.Error(), "failed to read file") {
		t.Errorf("Expected 'failed to read file' error, got: %v", err)
	}
}

func TestValidate_NilConfig(t *testing.T) {
	parser := NewParser()
	err := parser.Validate(nil)

	if err == nil {
		t.Fatal("Expected error for nil config, got nil")
	}

	if !IsValidationError(err) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestValidate_EmptyRules(t *testing.T) {
	parser := NewParser()
	config := &InhibitionConfig{Rules: []InhibitionRule{}}

	err := parser.Validate(config)

	if err == nil {
		t.Fatal("Expected error for empty rules, got nil")
	}

	if !IsConfigError(err) {
		t.Errorf("Expected ConfigError, got %T", err)
	}
}

func TestParse_LargeConfig(t *testing.T) {
	parser := NewParser()
	config, err := parser.Parse(generateLargeConfig(100))

	if err != nil {
		t.Fatalf("Parse() failed for large config: %v", err)
	}

	if len(config.Rules) != 100 {
		t.Errorf("Expected 100 rules, got %d", len(config.Rules))
	}

	// Check first and last rule
	if config.Rules[0].Name != "rule-0" {
		t.Errorf("First rule name: expected 'rule-0', got %q", config.Rules[0].Name)
	}

	if config.Rules[99].Name != "rule-99" {
		t.Errorf("Last rule name: expected 'rule-99', got %q", config.Rules[99].Name)
	}
}

// --- Edge Cases Tests ---

func TestParse_UnicodeLabels(t *testing.T) {
	// Unicode in label values is allowed, but not in label names
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
      description: "Узел недоступен"
    target_match:
      alertname: "InstanceDown"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if config.Rules[0].SourceMatch["description"] != "Узел недоступен" {
		t.Errorf("Unicode label value not preserved")
	}
}

func TestParse_SpecialCharactersRegex(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match_re:
      alertname: "Test[0-9]+"
      service: "api-.*\\.(prod|staging)"
    target_match:
      alertname: "Target"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// Test regex matching
	alertnameRE := config.Rules[0].GetCompiledSourceRE("alertname")
	if alertnameRE == nil {
		t.Fatal("alertname regex not compiled")
	}

	if !alertnameRE.MatchString("Test123") {
		t.Error("Regex should match 'Test123'")
	}
}

func TestParse_VeryLongLabelName(t *testing.T) {
	// Very long but valid label name
	longName := "very_long_label_name_" + strings.Repeat("a", 100)

	yaml := []byte(fmt.Sprintf(`
inhibit_rules:
  - source_match:
      %s: "value"
    target_match:
      alertname: "Target"
`, longName))

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if config.Rules[0].SourceMatch[longName] != "value" {
		t.Error("Long label name not preserved")
	}
}

func TestParse_DuplicateRules(t *testing.T) {
	// Duplicate rules are allowed (no uniqueness constraint)
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() should allow duplicate rules, got error: %v", err)
	}

	if len(config.Rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(config.Rules))
	}
}

func TestParse_ComplexRegex(t *testing.T) {
	yaml := []byte(`
inhibit_rules:
  - source_match_re:
      service: "^(api|web)-([a-z]+)-v([0-9]+\\.[0-9]+)$"
    target_match:
      alertname: "Target"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	re := config.Rules[0].GetCompiledSourceRE("service")
	if re == nil {
		t.Fatal("Complex regex not compiled")
	}

	// Test matching
	if !re.MatchString("api-backend-v1.2") {
		t.Error("Complex regex should match 'api-backend-v1.2'")
	}

	if re.MatchString("db-backend-v1.2") {
		t.Error("Complex regex should not match 'db-backend-v1.2'")
	}
}

func TestParse_ReservedLabelNames(t *testing.T) {
	// __name__ is a reserved Prometheus label but should be allowed
	yaml := []byte(`
inhibit_rules:
  - source_match:
      __name__: "metric_name"
    target_match:
      alertname: "Target"
`)

	parser := NewParser()
	_, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() should allow __name__ label, got error: %v", err)
	}
}

func TestParse_WhitespaceHandling(t *testing.T) {
	// YAML should handle whitespace correctly
	yaml := []byte(`
inhibit_rules:
  - source_match:
      alertname: "  NodeDown  "
      severity: "critical"
    target_match:
      alertname: "InstanceDown"
`)

	parser := NewParser()
	config, err := parser.Parse(yaml)

	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// YAML trims whitespace from unquoted strings
	alertname := config.Rules[0].SourceMatch["alertname"]
	if alertname != "  NodeDown  " {
		t.Errorf("Whitespace not preserved correctly, got %q", alertname)
	}
}

// --- Benchmarks ---

func BenchmarkParse_SingleRule(b *testing.B) {
	data := generateMinimalConfig()
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(data)
	}
}

func BenchmarkParse_10Rules(b *testing.B) {
	data := generateLargeConfig(10)
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(data)
	}
}

func BenchmarkParse_100Rules(b *testing.B) {
	data := generateLargeConfig(100)
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(data)
	}
}

func BenchmarkParse_1000Rules(b *testing.B) {
	data := generateLargeConfig(1000)
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(data)
	}
}

func BenchmarkParseFile_SingleRule(b *testing.B) {
	// Create temp file
	tmpDir := b.TempDir()
	tmpFile := filepath.Join(tmpDir, "inhibition.yaml")
	_ = os.WriteFile(tmpFile, generateMinimalConfig(), 0644)

	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.ParseFile(tmpFile)
	}
}

func BenchmarkValidate_100Rules(b *testing.B) {
	parser := NewParser()
	config, _ := parser.Parse(generateLargeConfig(100))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.Validate(config)
	}
}

func BenchmarkCompileRegex_10Patterns(b *testing.B) {
	yaml := []byte(`
inhibit_rules:
  - source_match_re:
      p1: "^api.*"
      p2: "^web.*"
      p3: "^db.*"
    target_match_re:
      p4: ".*prod$"
      p5: ".*staging$"
    equal:
      - cluster
`)

	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(yaml)
	}
}

func BenchmarkIsValidLabelName(b *testing.B) {
	validNames := []string{
		"alertname",
		"severity",
		"cluster",
		"__name__",
		"label_with_underscore",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range validNames {
			_ = isValidLabelName(name)
		}
	}
}
