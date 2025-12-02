package inhibition

import (
	"fmt"
	"strings"
	"testing"
)

// --- Extended Coverage Tests for Enterprise Grade (90%+ Coverage) ---
// This file contains tests for previously uncovered functions to reach 90%+ coverage target.

// TestGetConfig tests the GetConfig method.
func TestGetConfig(t *testing.T) {
	parser := NewParser()

	// Test 1: GetConfig returns empty config when nothing loaded
	config := parser.GetConfig()
	if config == nil {
		t.Fatal("Expected non-nil config")
	}
	if len(config.Rules) != 0 {
		t.Errorf("Expected empty rules, got %d", len(config.Rules))
	}

	// Test 2: GetConfig returns loaded config
	yamlData := generateValidConfig()
	loadedConfig, err := parser.Parse(yamlData)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	retrievedConfig := parser.GetConfig()
	if retrievedConfig == nil {
		t.Fatal("Expected non-nil config")
	}
	if len(retrievedConfig.Rules) != len(loadedConfig.Rules) {
		t.Errorf("Expected %d rules, got %d", len(loadedConfig.Rules), len(retrievedConfig.Rules))
	}
}

// TestInhibitionRule_Validate tests the Validate method on InhibitionRule.
func TestInhibitionRule_Validate(t *testing.T) {
	tests := []struct {
		name    string
		rule    InhibitionRule
		wantErr bool
	}{
		{
			name: "valid rule with source_match and target_match",
			rule: InhibitionRule{
				SourceMatch: map[string]string{"alertname": "NodeDown"},
				TargetMatch: map[string]string{"alertname": "InstanceDown"},
				Equal:       []string{"node", "cluster"},
			},
			wantErr: false,
		},
		{
			name: "valid rule with regex",
			rule: InhibitionRule{
				SourceMatchRE: map[string]string{"service": "^api.*"},
				TargetMatch:   map[string]string{"alertname": "InstanceDown"},
			},
			wantErr: false,
		},
		{
			name: "missing both source conditions",
			rule: InhibitionRule{
				TargetMatch: map[string]string{"alertname": "InstanceDown"},
			},
			wantErr: true,
		},
		{
			name: "missing both target conditions",
			rule: InhibitionRule{
				SourceMatch: map[string]string{"alertname": "NodeDown"},
			},
			wantErr: true,
		},
		{
			name: "invalid label in equal (starts with number)",
			rule: InhibitionRule{
				SourceMatch: map[string]string{"alertname": "NodeDown"},
				TargetMatch: map[string]string{"alertname": "InstanceDown"},
				Equal:       []string{"123invalid"},
			},
			wantErr: true,
		},
		{
			name: "invalid label name in source_match (hyphen)",
			rule: InhibitionRule{
				SourceMatch: map[string]string{"alert-name": "NodeDown"},
				TargetMatch: map[string]string{"alertname": "InstanceDown"},
			},
			wantErr: true,
		},
		{
			name: "invalid label name in target_match_re (starts with number)",
			rule: InhibitionRule{
				SourceMatch:   map[string]string{"alertname": "NodeDown"},
				TargetMatchRE: map[string]string{"123invalid": ".*"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				// Check that error is ValidationError
				if !IsValidationError(err) {
					t.Errorf("Expected ValidationError, got %T", err)
				}
			}
		})
	}
}

// TestInhibitionConfig_Validate tests the Validate method on InhibitionConfig.
func TestInhibitionConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  InhibitionConfig
		wantErr bool
	}{
		{
			name: "valid config with one rule",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{
						SourceMatch: map[string]string{"alertname": "NodeDown"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid config with multiple rules",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{
						SourceMatch: map[string]string{"alertname": "NodeDown"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
					},
					{
						SourceMatchRE: map[string]string{"service": "^api.*"},
						TargetMatch:   map[string]string{"alertname": "ServiceDown"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty rules list",
			config: InhibitionConfig{
				Rules: []InhibitionRule{},
			},
			wantErr: true,
		},
		{
			name: "invalid rule in config (missing source)",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid rule in config (invalid label name)",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{
						SourceMatch: map[string]string{"123invalid": "value"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestInhibitionConfig_RuleCount tests the RuleCount method.
func TestInhibitionConfig_RuleCount(t *testing.T) {
	tests := []struct {
		name     string
		config   InhibitionConfig
		expected int
	}{
		{
			name: "empty config",
			config: InhibitionConfig{
				Rules: []InhibitionRule{},
			},
			expected: 0,
		},
		{
			name: "config with 2 rules",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
					{SourceMatch: map[string]string{"e": "f"}, TargetMatch: map[string]string{"g": "h"}},
				},
			},
			expected: 2,
		},
		{
			name: "config with 10 rules",
			config: InhibitionConfig{
				Rules: make([]InhibitionRule, 10),
			},
			expected: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := tt.config.RuleCount()
			if count != tt.expected {
				t.Errorf("Expected %d rules, got %d", tt.expected, count)
			}
		})
	}
}

// TestInhibitionConfig_GetRuleByName tests the GetRuleByName method.
func TestInhibitionConfig_GetRuleByName(t *testing.T) {
	config := InhibitionConfig{
		Rules: []InhibitionRule{
			{Name: "rule1", SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
			{Name: "rule2", SourceMatch: map[string]string{"e": "f"}, TargetMatch: map[string]string{"g": "h"}},
			{Name: "", SourceMatch: map[string]string{"i": "j"}, TargetMatch: map[string]string{"k": "l"}},
		},
	}

	tests := []struct {
		name     string
		ruleName string
		want     bool
	}{
		{"found first rule", "rule1", true},
		{"found second rule", "rule2", true},
		{"not found", "nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := config.GetRuleByName(tt.ruleName)
			found := rule != nil
			if found != tt.want {
				t.Errorf("GetRuleByName(%q) found=%v, want=%v", tt.ruleName, found, tt.want)
			}
			if found && rule.Name != tt.ruleName {
				t.Errorf("Expected rule name %q, got %q", tt.ruleName, rule.Name)
			}
		})
	}
}

// TestInhibitionConfig_GetRuleByIndex tests the GetRuleByIndex method.
func TestInhibitionConfig_GetRuleByIndex(t *testing.T) {
	config := InhibitionConfig{
		Rules: []InhibitionRule{
			{Name: "rule0", SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
			{Name: "rule1", SourceMatch: map[string]string{"e": "f"}, TargetMatch: map[string]string{"g": "h"}},
		},
	}

	tests := []struct {
		name     string
		index    int
		want     bool
		ruleName string
	}{
		{"valid index 0", 0, true, "rule0"},
		{"valid index 1", 1, true, "rule1"},
		{"negative index", -1, false, ""},
		{"out of bounds (too large)", 100, false, ""},
		{"out of bounds (exactly at length)", 2, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := config.GetRuleByIndex(tt.index)
			found := rule != nil
			if found != tt.want {
				t.Errorf("GetRuleByIndex(%d) found=%v, want=%v", tt.index, found, tt.want)
			}
			if found && rule.Name != tt.ruleName {
				t.Errorf("Expected rule name %q, got %q", tt.ruleName, rule.Name)
			}
		})
	}
}

// TestInhibitionRule_String tests the String method on InhibitionRule.
func TestInhibitionRule_String(t *testing.T) {
	tests := []struct {
		name     string
		rule     InhibitionRule
		contains []string
	}{
		{
			name: "rule with name",
			rule: InhibitionRule{
				Name:        "test-rule",
				SourceMatch: map[string]string{"alertname": "NodeDown"},
				TargetMatch: map[string]string{"alertname": "InstanceDown"},
				Equal:       []string{"node", "cluster"},
			},
			contains: []string{"InhibitionRule", "test-rule"},
		},
		{
			name: "rule without name",
			rule: InhibitionRule{
				SourceMatch:   map[string]string{"severity": "critical"},
				TargetMatchRE: map[string]string{"severity": "warning|info"},
			},
			contains: []string{"InhibitionRule"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.rule.String()
			if str == "" {
				t.Error("Expected non-empty string representation")
			}
			for _, substr := range tt.contains {
				if !strings.Contains(str, substr) {
					t.Errorf("Expected string to contain %q, got: %s", substr, str)
				}
			}
		})
	}
}

// TestInhibitionConfig_String tests the String method on InhibitionConfig.
func TestInhibitionConfig_String(t *testing.T) {
	tests := []struct {
		name     string
		config   InhibitionConfig
		contains []string
	}{
		{
			name: "config with 1 rule",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{Name: "rule1", SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
				},
			},
			contains: []string{"InhibitionConfig", "rules=1"},
		},
		{
			name: "config with multiple rules",
			config: InhibitionConfig{
				Rules: []InhibitionRule{
					{Name: "rule1", SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
					{Name: "rule2", SourceMatch: map[string]string{"e": "f"}, TargetMatch: map[string]string{"g": "h"}},
					{Name: "rule3", SourceMatch: map[string]string{"i": "j"}, TargetMatch: map[string]string{"k": "l"}},
				},
			},
			contains: []string{"InhibitionConfig", "rules=3"},
		},
		{
			name: "empty config",
			config: InhibitionConfig{
				Rules: []InhibitionRule{},
			},
			contains: []string{"InhibitionConfig", "rules=0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.config.String()
			if str == "" {
				t.Error("Expected non-empty string representation")
			}
			for _, substr := range tt.contains {
				if !strings.Contains(str, substr) {
					t.Errorf("Expected string to contain %q, got: %s", substr, str)
				}
			}
		})
	}
}

// TestMatchResult_String tests the String method on MatchResult.
func TestMatchResult_String(t *testing.T) {
	tests := []struct {
		name     string
		result   MatchResult
		contains []string
	}{
		{
			name: "matched result",
			result: MatchResult{
				Matched: true,
				Rule:    &InhibitionRule{Name: "test-rule"},
			},
			contains: []string{"MatchResult", "matched=true", "test-rule"},
		},
		{
			name: "not matched result",
			result: MatchResult{
				Matched: false,
				Rule:    nil,
			},
			contains: []string{"MatchResult", "matched=false"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.result.String()
			if str == "" {
				t.Error("Expected non-empty string representation")
			}
			for _, substr := range tt.contains {
				if !strings.Contains(str, substr) {
					t.Errorf("Expected string to contain %q, got: %s", substr, str)
				}
			}
		})
	}
}

// TestErrorTypes_Error tests Error() methods on all error types.
func TestErrorTypes_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains []string
	}{
		{
			name:     "ParseError",
			err:      NewParseError("field1", "value1", fmt.Errorf("underlying error")),
			contains: []string{"field1", "value1", "underlying error"},
		},
		{
			name:     "ValidationError",
			err:      NewValidationError("field2", "rule2", "message2"),
			contains: []string{"field2", "rule2", "message2"},
		},
		{
			name:     "ConfigError with errors",
			err:      NewConfigError("config error", []error{fmt.Errorf("err1"), fmt.Errorf("err2")}),
			contains: []string{"config error", "2"},
		},
		{
			name:     "ConfigError without errors",
			err:      NewConfigError("config error", nil),
			contains: []string{"config error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			if errStr == "" {
				t.Error("Expected non-empty error string")
			}
			for _, substr := range tt.contains {
				if !strings.Contains(errStr, substr) {
					t.Errorf("Expected error string to contain %q, got: %s", substr, errStr)
				}
			}
		})
	}
}

// TestErrorTypes_Unwrap tests Unwrap() methods on error types.
func TestErrorTypes_Unwrap(t *testing.T) {
	t.Run("ParseError Unwrap", func(t *testing.T) {
		underlyingErr := fmt.Errorf("underlying error")
		parseErr := NewParseError("field", "value", underlyingErr)
		unwrapped := parseErr.Unwrap()
		if unwrapped != underlyingErr {
			t.Error("Expected Unwrap to return underlying error")
		}
	})

	t.Run("ConfigError Unwrap", func(t *testing.T) {
		err1 := fmt.Errorf("err1")
		err2 := fmt.Errorf("err2")
		configErr := NewConfigError("config error", []error{err1, err2})
		unwrappedSlice := configErr.Unwrap()
		if len(unwrappedSlice) != 2 {
			t.Errorf("Expected 2 unwrapped errors, got %d", len(unwrappedSlice))
		}
		if unwrappedSlice[0] != err1 || unwrappedSlice[1] != err2 {
			t.Error("Unwrapped errors don't match original errors")
		}
	})
}

// TestErrorTypes_Is tests Is() methods on error types.
func TestErrorTypes_Is(t *testing.T) {
	t.Run("ParseError Is", func(t *testing.T) {
		parseErr1 := NewParseError("field", "value", fmt.Errorf("err"))
		parseErr2 := NewParseError("field", "value", fmt.Errorf("err"))
		if !parseErr1.Is(parseErr2) {
			t.Error("Expected ParseError.Is to return true for same type")
		}

		otherErr := fmt.Errorf("other error")
		if parseErr1.Is(otherErr) {
			t.Error("Expected ParseError.Is to return false for different type")
		}
	})

	t.Run("ValidationError Is", func(t *testing.T) {
		valErr1 := NewValidationError("field", "rule", "msg")
		valErr2 := NewValidationError("field", "rule", "msg")
		if !valErr1.Is(valErr2) {
			t.Error("Expected ValidationError.Is to return true for same type")
		}

		otherErr := fmt.Errorf("other error")
		if valErr1.Is(otherErr) {
			t.Error("Expected ValidationError.Is to return false for different type")
		}
	})

	t.Run("ConfigError Is", func(t *testing.T) {
		configErr1 := NewConfigError("msg", nil)
		configErr2 := NewConfigError("msg", nil)
		if !configErr1.Is(configErr2) {
			t.Error("Expected ConfigError.Is to return true for same type")
		}

		otherErr := fmt.Errorf("other error")
		if configErr1.Is(otherErr) {
			t.Error("Expected ConfigError.Is to return false for different type")
		}
	})
}

// TestGetValidationError tests the GetValidationError helper function.
func TestGetValidationError(t *testing.T) {
	t.Run("Direct ValidationError", func(t *testing.T) {
		valErr := NewValidationError("field", "rule", "msg")
		retrieved := GetValidationError(valErr)
		if retrieved == nil {
			t.Fatal("Expected non-nil ValidationError")
		}
		if retrieved.Field != "field" {
			t.Errorf("Expected field 'field', got '%s'", retrieved.Field)
		}
	})

	t.Run("Not ValidationError", func(t *testing.T) {
		otherErr := fmt.Errorf("other error")
		retrieved := GetValidationError(otherErr)
		if retrieved != nil {
			t.Error("Expected nil ValidationError for non-ValidationError input")
		}
	})

	t.Run("Wrapped ValidationError", func(t *testing.T) {
		valErr := NewValidationError("field", "rule", "msg")
		wrappedErr := fmt.Errorf("wrapped: %w", valErr)
		retrieved := GetValidationError(wrappedErr)
		if retrieved == nil {
			t.Fatal("Expected non-nil ValidationError")
		}
		if retrieved.Field != "field" {
			t.Errorf("Expected field 'field', got '%s'", retrieved.Field)
		}
	})

	t.Run("Nil error", func(t *testing.T) {
		retrieved := GetValidationError(nil)
		if retrieved != nil {
			t.Error("Expected nil ValidationError for nil input")
		}
	})
}

// TestParseReader_IOError tests ParseReader with IO errors.
func TestParseReader_IOError(t *testing.T) {
	parser := NewParser()

	// Create a reader that always returns an error
	errorReader := &errorReaderMock{err: fmt.Errorf("mock IO error")}

	_, err := parser.ParseReader(errorReader)
	if err == nil {
		t.Error("Expected error from ParseReader with failing reader")
	}
	if !strings.Contains(err.Error(), "failed to read data") {
		t.Errorf("Expected error message about failed read, got: %v", err)
	}
}

// errorReaderMock is a mock reader that always returns an error.
type errorReaderMock struct {
	err error
}

func (e *errorReaderMock) Read(p []byte) (n int, err error) {
	return 0, e.err
}

// TestValidate_NilConfigExtraTests adds more coverage for Validate with nil config.
func TestValidate_NilConfigExtraTests(t *testing.T) {
	parser := NewParser()

	err := parser.Validate(nil)
	if err == nil {
		t.Error("Expected error when validating nil config")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("Expected error message about nil config, got: %v", err)
	}
}
