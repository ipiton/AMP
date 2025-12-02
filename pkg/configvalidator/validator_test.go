package configvalidator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidator_ValidateFile(t *testing.T) {
	// Create temp directory for test files
	tmpDir := t.TempDir()

	tests := []struct {
		name         string
		config       string
		filename     string
		mode         ValidationMode
		wantValid    bool
		wantErrors   int
		wantWarnings int
	}{
		{
			name: "valid minimal config",
			config: `
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: ['alertname']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`,
			filename:     "valid_minimal.yml",
			mode:         StrictMode,
			wantValid:    true,
			wantErrors:   0,
			wantWarnings: 0,
		},
		{
			name: "missing required receiver",
			config: `
route:
  receiver: default
  group_by: ['alertname']

receivers: []
`,
			filename:     "missing_receiver.yml",
			mode:         StrictMode,
			wantValid:    false,
			wantErrors:   1,
			wantWarnings: 0,
		},
		{
			name: "invalid yaml syntax",
			config: `
route:
  receiver: default
  group_by: ['alertname'
receivers:
  - name: default
`,
			filename:     "invalid_syntax.yml",
			mode:         StrictMode,
			wantValid:    false,
			wantErrors:   1,
			wantWarnings: 0,
		},
		{
			name: "insecure http webhook",
			config: `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: http://example.com/webhook
`,
			filename:     "insecure_http.yml",
			mode:         StrictMode,
			wantValid:    false,
			wantErrors:   0,
			wantWarnings: 1,
		},
		{
			name: "valid config with warnings in lenient mode",
			config: `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: http://example.com/webhook
`,
			filename:     "warnings_lenient.yml",
			mode:         LenientMode,
			wantValid:    true,
			wantErrors:   0,
			wantWarnings: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write test config to file
			filePath := filepath.Join(tmpDir, tt.filename)
			err := os.WriteFile(filePath, []byte(tt.config), 0644)
			if err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			// Create validator
			opts := DefaultOptions()
			opts.Mode = tt.mode
			validator := New(opts)

			// Validate file
			result, err := validator.ValidateFile(filePath)
			if err != nil {
				// Only expect error for file read failures, not validation failures
				if !os.IsNotExist(err) {
					t.Logf("ValidateFile() error = %v", err)
				}
			}

			if result != nil {
				if result.Valid != tt.wantValid {
					t.Errorf("ValidateFile() Valid = %v, want %v", result.Valid, tt.wantValid)
					t.Logf("Errors: %v", result.Errors)
					t.Logf("Warnings: %v", result.Warnings)
				}

				if len(result.Errors) < tt.wantErrors {
					t.Errorf("ValidateFile() got %d errors, want at least %d", len(result.Errors), tt.wantErrors)
				}

				if len(result.Warnings) < tt.wantWarnings {
					t.Errorf("ValidateFile() got %d warnings, want at least %d", len(result.Warnings), tt.wantWarnings)
				}
			}
		})
	}
}

func TestValidator_ValidateBytes(t *testing.T) {
	tests := []struct {
		name       string
		config     []byte
		wantValid  bool
		wantErrors bool
	}{
		{
			name: "valid YAML",
			config: []byte(`
route:
  receiver: default
receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`),
			wantValid:  true,
			wantErrors: false,
		},
		{
			name: "valid JSON",
			config: []byte(`{
  "route": {
    "receiver": "default"
  },
  "receivers": [
    {
      "name": "default",
      "webhook_configs": [
        {"url": "https://example.com/webhook"}
      ]
    }
  ]
}`),
			wantValid:  true,
			wantErrors: false,
		},
		{
			name:       "invalid YAML",
			config:     []byte("route:\n  receiver: [unclosed"),
			wantValid:  false,
			wantErrors: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := New(DefaultOptions())

			result, err := validator.ValidateBytes(tt.config)
			if err != nil && !tt.wantErrors {
				t.Errorf("ValidateBytes() unexpected error = %v", err)
			}

			if result != nil && result.Valid != tt.wantValid {
				t.Errorf("ValidateBytes() Valid = %v, want %v", result.Valid, tt.wantValid)
			}
		})
	}
}

func TestValidationModes(t *testing.T) {
	config := []byte(`
route:
  receiver: default
receivers:
  - name: default
    webhook_configs:
      - url: http://example.com/webhook
`)

	tests := []struct {
		name      string
		mode      ValidationMode
		wantValid bool
	}{
		{
			name:      "strict mode blocks warnings",
			mode:      StrictMode,
			wantValid: false,
		},
		{
			name:      "lenient mode allows warnings",
			mode:      LenientMode,
			wantValid: true,
		},
		{
			name:      "permissive mode always valid",
			mode:      PermissiveMode,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Mode = tt.mode
			validator := New(opts)

			result, _ := validator.ValidateBytes(config)
			if result.Valid != tt.wantValid {
				t.Errorf("Mode %s: Valid = %v, want %v", tt.mode, result.Valid, tt.wantValid)
			}
		})
	}
}

func TestOptions(t *testing.T) {
	tests := []struct {
		name          string
		setupOpts     func(*Options)
		config        []byte
		checkResult   func(*testing.T, *Result)
	}{
		{
			name: "security checks disabled",
			setupOpts: func(opts *Options) {
				opts.EnableSecurityChecks = false
			},
			config: []byte(`
route:
  receiver: default
receivers:
  - name: default
    webhook_configs:
      - url: http://example.com/webhook
`),
			checkResult: func(t *testing.T, r *Result) {
				// Should have fewer warnings with security checks disabled
				securityWarnings := 0
				for _, w := range r.Warnings {
					if w.Code >= "W300" && w.Code <= "W399" {
						securityWarnings++
					}
				}
				if securityWarnings > 0 {
					t.Errorf("Expected no security warnings with security checks disabled, got %d", securityWarnings)
				}
			},
		},
		{
			name: "best practices disabled",
			setupOpts: func(opts *Options) {
				opts.EnableBestPractices = false
			},
			config: []byte(`
route:
  receiver: default
receivers:
  - name: default
`),
			checkResult: func(t *testing.T, r *Result) {
				// Should have fewer suggestions with best practices disabled
				if len(r.Suggestions) > 0 {
					t.Errorf("Expected no suggestions with best practices disabled, got %d", len(r.Suggestions))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			if tt.setupOpts != nil {
				tt.setupOpts(&opts)
			}

			validator := New(opts)
			result, _ := validator.ValidateBytes(tt.config)

			if tt.checkResult != nil {
				tt.checkResult(t, result)
			}
		})
	}
}

func TestResult_ExitCode(t *testing.T) {
	tests := []struct {
		name     string
		result   *Result
		mode     ValidationMode
		wantCode int
	}{
		{
			name: "no issues",
			result: &Result{
				Valid:    true,
				Errors:   []*Issue{},
				Warnings: []*Issue{},
			},
			mode:     StrictMode,
			wantCode: 0,
		},
		{
			name: "errors present",
			result: &Result{
				Valid: false,
				Errors: []*Issue{
					{Code: "E001", Message: "test error"},
				},
			},
			mode:     StrictMode,
			wantCode: 1,
		},
		{
			name: "warnings in strict mode",
			result: &Result{
				Valid: false,
				Warnings: []*Issue{
					{Code: "W001", Message: "test warning"},
				},
			},
			mode:     StrictMode,
			wantCode: 2,
		},
		{
			name: "warnings in lenient mode",
			result: &Result{
				Valid: true,
				Warnings: []*Issue{
					{Code: "W001", Message: "test warning"},
				},
			},
			mode:     LenientMode,
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.ExitCode(tt.mode)
			if got != tt.wantCode {
				t.Errorf("ExitCode() = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func BenchmarkValidator_ValidateBytes(b *testing.B) {
	config := []byte(`
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: ['alertname', 'cluster']
  routes:
    - receiver: team-a
      matchers:
        - team=a
        - severity=~critical|warning
    - receiver: team-b
      matchers:
        - team=b

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/default
  - name: team-a
    slack_configs:
      - api_url: https://hooks.slack.com/services/XXX/YYY/ZZZ
        channel: '#team-a-alerts'
  - name: team-b
    email_configs:
      - to: team-b@example.com
        from: alertmanager@example.com

inhibit_rules:
  - source_matchers:
      - severity=critical
    target_matchers:
      - severity=warning
    equal: ['alertname', 'instance']
`)

	validator := New(DefaultOptions())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = validator.ValidateBytes(config)
	}
}

func BenchmarkValidator_ValidateFile(b *testing.B) {
	// Create temp file
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "benchmark.yml")

	config := []byte(`
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: ['alertname']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`)

	err := os.WriteFile(filePath, config, 0644)
	if err != nil {
		b.Fatalf("Failed to write test file: %v", err)
	}

	validator := New(DefaultOptions())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = validator.ValidateFile(filePath)
	}
}

// Helper to create context with timeout
func testContext() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	return ctx
}
