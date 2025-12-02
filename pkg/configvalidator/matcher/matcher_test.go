package matcher

import (
	"regexp"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantLabel   string
		wantType    MatchType
		wantValue   string
		wantErr     bool
		errContains string
	}{
		// Valid matchers
		{
			name:      "exact match",
			input:     "severity=critical",
			wantLabel: "severity",
			wantType:  MatchEqual,
			wantValue: "critical",
			wantErr:   false,
		},
		{
			name:      "not equal match",
			input:     "severity!=info",
			wantLabel: "severity",
			wantType:  MatchNotEqual,
			wantValue: "info",
			wantErr:   false,
		},
		{
			name:      "regex match",
			input:     "instance=~prod-.*",
			wantLabel: "instance",
			wantType:  MatchRegexp,
			wantValue: "prod-.*",
			wantErr:   false,
		},
		{
			name:      "negative regex match",
			input:     "alertname!~Test.*",
			wantLabel: "alertname",
			wantType:  MatchNotRegexp,
			wantValue: "Test.*",
			wantErr:   false,
		},
		{
			name:      "label with underscore",
			input:     "alert_name=HighCPU",
			wantLabel: "alert_name",
			wantType:  MatchEqual,
			wantValue: "HighCPU",
			wantErr:   false,
		},
		{
			name:      "label with numbers",
			input:     "status5xx=500",
			wantLabel: "status5xx",
			wantType:  MatchEqual,
			wantValue: "500",
			wantErr:   false,
		},
		{
			name:      "empty value",
			input:     "label=",
			wantLabel: "label",
			wantType:  MatchEqual,
			wantValue: "",
			wantErr:   false,
		},
		{
			name:      "value with spaces",
			input:     "message=high cpu usage",
			wantLabel: "message",
			wantType:  MatchEqual,
			wantValue: "high cpu usage",
			wantErr:   false,
		},
		{
			name:      "complex regex",
			input:     "instance=~(prod|staging)-.*",
			wantLabel: "instance",
			wantType:  MatchRegexp,
			wantValue: "(prod|staging)-.*",
			wantErr:   false,
		},

		// Invalid matchers
		{
			name:        "no operator",
			input:       "severity",
			wantErr:     true,
			errContains: "no operator found",
		},
		{
			name:        "invalid label name (starts with number)",
			input:       "123abc=value",
			wantErr:     true,
			errContains: "invalid label name",
		},
		{
			name:        "invalid label name (special chars)",
			input:       "label-name=value",
			wantErr:     true,
			errContains: "invalid label name",
		},
		{
			name:        "empty input",
			input:       "",
			wantErr:     true,
			errContains: "empty matcher",
		},
		{
			name:        "only operator",
			input:       "=value",
			wantErr:     true,
			errContains: "invalid label name",
		},
		{
			name:        "invalid regex",
			input:       "label=~(unclosed",
			wantErr:     true,
			errContains: "invalid regex",
		},
		{
			name:        "double equals",
			input:       "label==value",
			wantErr:     true,
			errContains: "no operator found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse() expected error, got nil")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Parse() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Parse() unexpected error = %v", err)
				return
			}

			if got.Label != tt.wantLabel {
				t.Errorf("Parse() Label = %v, want %v", got.Label, tt.wantLabel)
			}
			if got.Type != tt.wantType {
				t.Errorf("Parse() Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.Value != tt.wantValue {
				t.Errorf("Parse() Value = %v, want %v", got.Value, tt.wantValue)
			}

			// For regex matchers, check that regex compiled
			if tt.wantType == MatchRegexp || tt.wantType == MatchNotRegexp {
				if got.CompiledRegex == nil {
					t.Errorf("Parse() CompiledRegex is nil for regex matcher")
				}
			}
		})
	}
}

func TestMatcher_Validate(t *testing.T) {
	tests := []struct {
		name        string
		matcher     *Matcher
		wantErr     bool
		errContains string
	}{
		{
			name: "valid exact match",
			matcher: &Matcher{
				Label: "severity",
				Type:  MatchEqual,
				Value: "critical",
			},
			wantErr: false,
		},
		{
			name: "valid regex match with compiled regex",
			matcher: &Matcher{
				Label:         "instance",
				Type:          MatchRegexp,
				Value:         "prod-.*",
				CompiledRegex: mustCompile("prod-.*"),
			},
			wantErr: false,
		},
		{
			name: "invalid label name (empty)",
			matcher: &Matcher{
				Label: "",
				Type:  MatchEqual,
				Value: "value",
			},
			wantErr:     true,
			errContains: "label name is required",
		},
		{
			name: "invalid label name (starts with number)",
			matcher: &Matcher{
				Label: "123abc",
				Type:  MatchEqual,
				Value: "value",
			},
			wantErr:     true,
			errContains: "invalid label name",
		},
		{
			name: "invalid label name (special chars)",
			matcher: &Matcher{
				Label: "label-name",
				Type:  MatchEqual,
				Value: "value",
			},
			wantErr:     true,
			errContains: "invalid label name",
		},
		{
			name: "regex matcher without compiled regex",
			matcher: &Matcher{
				Label:         "instance",
				Type:          MatchRegexp,
				Value:         "prod-.*",
				CompiledRegex: nil,
			},
			wantErr:     true,
			errContains: "compiled regex is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.matcher.Validate()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Validate() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

func TestMatcher_Matches(t *testing.T) {
	tests := []struct {
		name     string
		matcher  string
		labels   map[string]string
		expected bool
	}{
		// Exact match
		{
			name:    "exact match - present and equal",
			matcher: "severity=critical",
			labels:  map[string]string{"severity": "critical"},
			expected: true,
		},
		{
			name:    "exact match - present but not equal",
			matcher: "severity=critical",
			labels:  map[string]string{"severity": "warning"},
			expected: false,
		},
		{
			name:    "exact match - label not present",
			matcher: "severity=critical",
			labels:  map[string]string{"alertname": "HighCPU"},
			expected: false,
		},

		// Not equal match
		{
			name:    "not equal - present and different",
			matcher: "severity!=info",
			labels:  map[string]string{"severity": "critical"},
			expected: true,
		},
		{
			name:    "not equal - present and equal",
			matcher: "severity!=info",
			labels:  map[string]string{"severity": "info"},
			expected: false,
		},
		{
			name:    "not equal - label not present",
			matcher: "severity!=info",
			labels:  map[string]string{"alertname": "HighCPU"},
			expected: true,
		},

		// Regex match
		{
			name:    "regex match - matches",
			matcher: "instance=~prod-.*",
			labels:  map[string]string{"instance": "prod-server-01"},
			expected: true,
		},
		{
			name:    "regex match - doesn't match",
			matcher: "instance=~prod-.*",
			labels:  map[string]string{"instance": "staging-server-01"},
			expected: false,
		},
		{
			name:    "regex match - label not present",
			matcher: "instance=~prod-.*",
			labels:  map[string]string{"alertname": "HighCPU"},
			expected: false,
		},
		{
			name:    "regex match - complex pattern",
			matcher: "env=~(prod|staging)",
			labels:  map[string]string{"env": "prod"},
			expected: true,
		},

		// Negative regex match
		{
			name:    "negative regex - doesn't match pattern",
			matcher: "instance!~test-.*",
			labels:  map[string]string{"instance": "prod-server-01"},
			expected: true,
		},
		{
			name:    "negative regex - matches pattern",
			matcher: "instance!~test-.*",
			labels:  map[string]string{"instance": "test-server-01"},
			expected: false,
		},
		{
			name:    "negative regex - label not present",
			matcher: "instance!~test-.*",
			labels:  map[string]string{"alertname": "HighCPU"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.matcher)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			got := m.Matches(tt.labels)
			if got != tt.expected {
				t.Errorf("Matches() = %v, want %v (matcher: %s, labels: %v)", got, tt.expected, tt.matcher, tt.labels)
			}
		})
	}
}

func TestParseMatchers(t *testing.T) {
	tests := []struct {
		name         string
		input        []string
		wantCount    int
		wantErrCount int
	}{
		{
			name:         "all valid",
			input:        []string{"severity=critical", "instance=~prod-.*", "team!=platform"},
			wantCount:    3,
			wantErrCount: 0,
		},
		{
			name:         "some invalid",
			input:        []string{"severity=critical", "invalid", "team!=platform"},
			wantCount:    2,
			wantErrCount: 1,
		},
		{
			name:         "all invalid",
			input:        []string{"invalid1", "invalid2", ""},
			wantCount:    0,
			wantErrCount: 3,
		},
		{
			name:         "empty input",
			input:        []string{},
			wantCount:    0,
			wantErrCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchers, errors := ParseMatchers(tt.input)

			if len(matchers) != tt.wantCount {
				t.Errorf("ParseMatchers() got %d matchers, want %d", len(matchers), tt.wantCount)
			}

			if len(errors) != tt.wantErrCount {
				t.Errorf("ParseMatchers() got %d errors, want %d", len(errors), tt.wantErrCount)
			}
		})
	}
}

func TestMatcher_String(t *testing.T) {
	tests := []struct {
		name     string
		matcher  string
		expected string
	}{
		{
			name:     "exact match",
			matcher:  "severity=critical",
			expected: "severity=critical",
		},
		{
			name:     "not equal",
			matcher:  "severity!=info",
			expected: "severity!=info",
		},
		{
			name:     "regex match",
			matcher:  "instance=~prod-.*",
			expected: "instance=~prod-.*",
		},
		{
			name:     "negative regex",
			matcher:  "instance!~test-.*",
			expected: "instance!~test-.*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.matcher)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			got := m.String()
			if got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Benchmark tests
func BenchmarkParse(b *testing.B) {
	matchers := []string{
		"severity=critical",
		"instance=~prod-.*",
		"team!=platform",
		"alertname!~Test.*",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, m := range matchers {
			_, _ = Parse(m)
		}
	}
}

func BenchmarkMatcher_Matches(b *testing.B) {
	m, _ := Parse("instance=~prod-.*")
	labels := map[string]string{
		"instance":  "prod-server-01",
		"severity":  "critical",
		"alertname": "HighCPU",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Matches(labels)
	}
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func mustCompile(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		panic(err)
	}
	return re
}
