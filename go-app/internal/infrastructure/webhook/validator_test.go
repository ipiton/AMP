package webhook

import (
	"testing"
	"time"
)

func TestNewWebhookValidator(t *testing.T) {
	validator := NewWebhookValidator()
	if validator == nil {
		t.Fatal("NewWebhookValidator should not return nil")
	}
}

func TestValidateAlertmanager_ValidPayload(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "{}:{alertname=\"TestAlert\"}",
		Status:   "firing",
		Receiver: "default",
		GroupLabels: map[string]string{
			"alertname": "TestAlert",
		},
		CommonLabels: map[string]string{
			"alertname": "TestAlert",
			"severity":  "critical",
		},
		ExternalURL: "http://alertmanager.example.com",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "critical",
				},
				Annotations: map[string]string{
					"summary": "Test alert",
				},
				StartsAt:     time.Now().Add(-5 * time.Minute),
				EndsAt:       time.Time{}, // Ongoing alert
				GeneratorURL: "http://prometheus.example.com/graph",
				Fingerprint:  "abc123",
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if !result.Valid {
		t.Errorf("Expected valid webhook, got errors: %+v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors, got %d errors", len(result.Errors))
	}
}

func TestValidateAlertmanager_NilWebhook(t *testing.T) {
	validator := NewWebhookValidator()

	result := validator.ValidateAlertmanager(nil)
	if result.Valid {
		t.Error("Expected invalid result for nil webhook")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected validation errors for nil webhook")
	}
	if result.Errors[0].Field != "webhook" {
		t.Errorf("Expected field 'webhook', got '%s'", result.Errors[0].Field)
	}
}

func TestValidateAlertmanager_MissingRequiredFields(t *testing.T) {
	validator := NewWebhookValidator()

	tests := []struct {
		name          string
		webhook       *AlertmanagerWebhook
		expectedField string
	}{
		{
			name: "missing version",
			webhook: &AlertmanagerWebhook{
				GroupKey: "test",
				Status:   "firing",
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{"alertname": "test"},
					},
				},
			},
			expectedField: "version",
		},
		{
			name: "missing status",
			webhook: &AlertmanagerWebhook{
				Version:  "4",
				GroupKey: "test",
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{"alertname": "test"},
					},
				},
			},
			expectedField: "status",
		},
		{
			name: "missing groupKey",
			webhook: &AlertmanagerWebhook{
				Version: "4",
				Status:  "firing",
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{"alertname": "test"},
					},
				},
			},
			expectedField: "groupKey",
		},
		{
			name: "empty alerts",
			webhook: &AlertmanagerWebhook{
				Version:  "4",
				GroupKey: "test",
				Status:   "firing",
				Alerts:   []AlertmanagerAlert{},
			},
			expectedField: "alerts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validator.ValidateAlertmanager(tt.webhook)
			if result.Valid {
				t.Error("Expected invalid result")
			}
			if len(result.Errors) == 0 {
				t.Fatal("Expected validation errors")
			}

			found := false
			for _, err := range result.Errors {
				if err.Field == tt.expectedField {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected error for field '%s', got errors: %+v", tt.expectedField, result.Errors)
			}
		})
	}
}

func TestValidateAlertmanager_InvalidStatus(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "invalid_status",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "test"},
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for invalid status")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "status" && err.Tag == "webhook_status" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected status validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlertmanager_InvalidURL(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:     "4",
		GroupKey:    "test",
		Status:      "firing",
		ExternalURL: "not-a-valid-url",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "test"},
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for invalid URL")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "externalURL" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected externalURL validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlert_MissingAlertname(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{}, // Missing alertname
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for missing alertname")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].labels.alertname" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected alertname validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlert_InvalidSeverity(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "test",
					"severity":  "invalid_severity",
				},
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for invalid severity")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].labels.severity" && err.Tag == "severity" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected severity validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlert_ValidSeverities(t *testing.T) {
	validator := NewWebhookValidator()

	validSeverities := []string{"critical", "warning", "info", "debug"}

	for _, severity := range validSeverities {
		t.Run(severity, func(t *testing.T) {
			webhook := &AlertmanagerWebhook{
				Version:  "4",
				GroupKey: "test",
				Status:   "firing",
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{
							"alertname": "test",
							"severity":  severity,
						},
					},
				},
			}

			result := validator.ValidateAlertmanager(webhook)
			if !result.Valid {
				t.Errorf("Expected valid result for severity '%s', got errors: %+v", severity, result.Errors)
			}
		})
	}
}

func TestValidateAlert_InvalidTimestamps(t *testing.T) {
	validator := NewWebhookValidator()

	startsAt := time.Now()
	endsAt := startsAt.Add(-1 * time.Hour) // EndsAt before StartsAt

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "resolved",
				Labels: map[string]string{"alertname": "test"},
				StartsAt: startsAt,
				EndsAt:   endsAt,
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for endsAt before startsAt")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].endsAt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected endsAt validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlert_InvalidGeneratorURL(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:       "firing",
				Labels:       map[string]string{"alertname": "test"},
				GeneratorURL: "not-a-valid-url",
			},
		},
	}

	result := validator.ValidateAlertmanager(webhook)
	if result.Valid {
		t.Error("Expected invalid result for invalid generatorURL")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].generatorURL" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected generatorURL validation error, got: %+v", result.Errors)
	}
}

func TestValidateAlertmanager_TruncatedAlerts(t *testing.T) {
	validator := NewWebhookValidator()

	tests := []struct {
		name              string
		truncatedAlerts   int
		expectValid       bool
	}{
		{"positive truncated", 5, true},
		{"zero truncated", 0, true},
		{"negative truncated", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhook := &AlertmanagerWebhook{
				Version:         "4",
				GroupKey:        "test",
				Status:          "firing",
				TruncatedAlerts: tt.truncatedAlerts,
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{"alertname": "test"},
					},
				},
			}

			result := validator.ValidateAlertmanager(webhook)
			if result.Valid != tt.expectValid {
				t.Errorf("Expected valid=%v for truncatedAlerts=%d, got valid=%v, errors: %+v",
					tt.expectValid, tt.truncatedAlerts, result.Valid, result.Errors)
			}
		})
	}
}

func TestValidateGeneric_ValidPayload(t *testing.T) {
	validator := NewWebhookValidator()

	data := map[string]interface{}{
		"alertname": "TestAlert",
		"status":    "firing",
		"severity":  "critical",
		"message":   "Test message",
	}

	result := validator.ValidateGeneric(data)
	if !result.Valid {
		t.Errorf("Expected valid result, got errors: %+v", result.Errors)
	}
}

func TestValidateGeneric_NilData(t *testing.T) {
	validator := NewWebhookValidator()

	result := validator.ValidateGeneric(nil)
	if result.Valid {
		t.Error("Expected invalid result for nil data")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected validation errors for nil data")
	}
}

func TestValidateGeneric_MissingRequiredFields(t *testing.T) {
	validator := NewWebhookValidator()

	tests := []struct {
		name          string
		data          map[string]interface{}
		missingField  string
	}{
		{
			name: "missing alertname",
			data: map[string]interface{}{
				"status": "firing",
			},
			missingField: "alertname",
		},
		{
			name: "missing status",
			data: map[string]interface{}{
				"alertname": "test",
			},
			missingField: "status",
		},
		{
			name: "empty alertname",
			data: map[string]interface{}{
				"alertname": "",
				"status":    "firing",
			},
			missingField: "alertname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validator.ValidateGeneric(tt.data)
			if result.Valid {
				t.Error("Expected invalid result")
			}

			found := false
			for _, err := range result.Errors {
				if err.Field == tt.missingField {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected error for field '%s', got: %+v", tt.missingField, result.Errors)
			}
		})
	}
}

func TestValidateGeneric_InvalidStatus(t *testing.T) {
	validator := NewWebhookValidator()

	data := map[string]interface{}{
		"alertname": "test",
		"status":    "invalid_status",
	}

	result := validator.ValidateGeneric(data)
	if result.Valid {
		t.Error("Expected invalid result for invalid status")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "status" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected status validation error, got: %+v", result.Errors)
	}
}

func TestValidateGeneric_InvalidSeverity(t *testing.T) {
	validator := NewWebhookValidator()

	data := map[string]interface{}{
		"alertname": "test",
		"status":    "firing",
		"severity":  "invalid_severity",
	}

	result := validator.ValidateGeneric(data)
	if result.Valid {
		t.Error("Expected invalid result for invalid severity")
	}

	found := false
	for _, err := range result.Errors {
		if err.Field == "severity" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected severity validation error, got: %+v", result.Errors)
	}
}

func TestIsValidSeverity(t *testing.T) {
	tests := []struct {
		severity string
		expected bool
	}{
		{"critical", true},
		{"warning", true},
		{"info", true},
		{"debug", true},
		{"CRITICAL", true}, // Case insensitive
		{"Warning", true},
		{"invalid", false},
		{"error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			result := isValidSeverity(tt.severity)
			if result != tt.expected {
				t.Errorf("isValidSeverity(%q) = %v, expected %v", tt.severity, result, tt.expected)
			}
		})
	}
}

func TestIsValidWebhookStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"firing", true},
		{"resolved", true},
		{"FIRING", false},   // Case sensitive
		{"Resolved", false},
		{"pending", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := isValidWebhookStatus(tt.status)
			if result != tt.expected {
				t.Errorf("isValidWebhookStatus(%q) = %v, expected %v", tt.status, result, tt.expected)
			}
		})
	}
}

// =======================================================================================
// TN-146 Phase 4: Prometheus Validation Tests (10 comprehensive tests, ~500 LOC)
// =======================================================================================

// TestValidatePrometheusRequiredFields tests validation of all required fields
func TestValidatePrometheusRequiredFields(t *testing.T) {
	validator := NewWebhookValidator()

	// Valid webhook with all required fields
	validWebhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "TestAlert", "job": "api"},
				Annotations:  map[string]string{"summary": "Test"},
				State:        "firing",
				ActiveAt:     time.Now().Add(-1 * time.Minute),
				GeneratorURL: "http://prometheus:9090/graph",
			},
		},
	}

	result := validator.ValidatePrometheus(validWebhook)
	if !result.Valid {
		t.Errorf("Valid webhook failed validation: %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected 0 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestValidateMissingAlertname tests validation error when alertname is missing
func TestValidateMissingAlertname(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"job": "api"}, // Missing alertname
				State:        "firing",
				ActiveAt:     time.Now(),
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for missing alertname")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected validation errors, got none")
	}

	// Check error message
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].labels.alertname" && err.Tag == "required" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for missing alertname, got: %v", result.Errors)
	}
}

// TestValidateMissingActiveAt tests validation error when activeAt is missing
func TestValidateMissingActiveAt(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test"},
				State:        "firing",
				ActiveAt:     time.Time{}, // Zero time (missing)
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for missing activeAt")
	}

	// Check error field
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].activeAt" && err.Tag == "required" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for missing activeAt, got: %v", result.Errors)
	}
}

// TestValidateMissingGeneratorURL tests validation error when generatorURL is missing
func TestValidateMissingGeneratorURL(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test"},
				State:        "firing",
				ActiveAt:     time.Now(),
				GeneratorURL: "", // Missing
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for missing generatorURL")
	}

	// Check error field
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].generatorURL" && err.Tag == "required" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for missing generatorURL, got: %v", result.Errors)
	}
}

// TestValidateInvalidState tests validation error for invalid state value
func TestValidateInvalidState(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test"},
				State:        "unknown", // Invalid state
				ActiveAt:     time.Now(),
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for invalid state")
	}

	// Check error contains state validation message
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].state" && err.Tag == "enum" {
			if !contains(err.Message, "firing") || !contains(err.Message, "pending") || !contains(err.Message, "inactive") {
				t.Errorf("Error message should list valid states, got: %s", err.Message)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for invalid state, got: %v", result.Errors)
	}
}

// TestValidateInvalidTimestamp tests validation error for future timestamp
func TestValidateInvalidTimestamp(t *testing.T) {
	validator := NewWebhookValidator()

	// Timestamp in the future (beyond 5m tolerance)
	futureTime := time.Now().Add(10 * time.Minute)

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test"},
				State:        "firing",
				ActiveAt:     futureTime,
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for future timestamp")
	}

	// Check error message contains "future"
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].activeAt" && err.Tag == "timestamp" {
			if !contains(err.Message, "future") {
				t.Errorf("Error message should mention 'future', got: %s", err.Message)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for future timestamp, got: %v", result.Errors)
	}
}

// TestValidateInvalidLabelName tests validation error for invalid label name
func TestValidateInvalidLabelName(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels: map[string]string{
					"alertname":   "Test",
					"123invalid":  "value", // Invalid: starts with digit
					"invalid-key": "value", // Invalid: contains hyphen
				},
				State:        "firing",
				ActiveAt:     time.Now(),
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for invalid label names")
	}

	// Should have errors for both invalid labels
	invalidLabelsCount := 0
	for _, err := range result.Errors {
		if err.Tag == "format" && contains(err.Field, ".labels.") {
			invalidLabelsCount++
		}
	}
	if invalidLabelsCount < 2 {
		t.Errorf("Expected 2+ invalid label errors, got %d: %v", invalidLabelsCount, result.Errors)
	}
}

// TestValidateInvalidURL tests validation error for invalid URL format
func TestValidateInvalidURL(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test"},
				State:        "firing",
				ActiveAt:     time.Now(),
				GeneratorURL: "not a valid url ://", // Invalid URL
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for invalid URL")
	}

	// Check error field
	found := false
	for _, err := range result.Errors {
		if err.Field == "alerts[0].generatorURL" && err.Tag == "url" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for invalid URL, got: %v", result.Errors)
	}
}

// TestValidateValidPrometheusAlert tests validation passes for fully valid alert
func TestValidateValidPrometheusAlert(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels: map[string]string{
					"alertname": "HighCPU",
					"instance":  "server-1:9100",
					"job":       "node-exporter",
					"severity":  "warning",
					"__meta_foo": "bar", // Valid: starts with underscore
				},
				Annotations: map[string]string{
					"summary":     "CPU is high",
					"description": "CPU > 80% for 5m",
					"runbook_url": "https://example.com/runbook",
				},
				State:        "firing",
				ActiveAt:     time.Now().Add(-5 * time.Minute),
				GeneratorURL: "http://prometheus:9090/graph?g0.expr=node_cpu_usage",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if !result.Valid {
		t.Errorf("Expected validation to pass, got errors: %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected 0 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestValidatePartialAnnotations tests validation passes with empty annotations
func TestValidatePartialAnnotations(t *testing.T) {
	validator := NewWebhookValidator()

	// Annotations are optional - empty should be valid
	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{"alertname": "Test", "job": "api"},
				Annotations:  map[string]string{}, // Empty (optional)
				State:        "pending",
				ActiveAt:     time.Now().Add(-30 * time.Second),
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if !result.Valid {
		t.Errorf("Expected validation to pass with empty annotations, got errors: %v", result.Errors)
	}
}

// TestValidatePrometheusV2Grouped tests validation works for v2 grouped format
func TestValidatePrometheusV2Grouped(t *testing.T) {
	validator := NewWebhookValidator()

	// v2 format with groups
	webhook := &PrometheusWebhook{
		Groups: []PrometheusAlertGroup{
			{
				Labels: map[string]string{"job": "api", "severity": "warning"},
				Alerts: []PrometheusAlert{
					{
						Labels:       map[string]string{"alertname": "Alert1", "instance": "server-1"},
						State:        "firing",
						ActiveAt:     time.Now().Add(-1 * time.Minute),
						GeneratorURL: "http://prometheus:9090",
					},
					{
						Labels:       map[string]string{"alertname": "Alert2", "instance": "server-2"},
						State:        "inactive",
						ActiveAt:     time.Now().Add(-10 * time.Minute),
						GeneratorURL: "http://prometheus:9090",
					},
				},
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if !result.Valid {
		t.Errorf("Expected validation to pass for v2 grouped format, got errors: %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected 0 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestValidateMultipleErrors tests validation captures multiple errors
func TestValidateMultipleErrors(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels:       map[string]string{}, // Missing alertname
				State:        "invalid",           // Invalid state
				ActiveAt:     time.Time{},         // Missing timestamp
				GeneratorURL: "",                  // Missing URL
			},
		},
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail with multiple errors")
	}

	// Should have at least 4 errors (alertname, state, timestamp, URL)
	if len(result.Errors) < 4 {
		t.Errorf("Expected at least 4 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestValidateNilWebhook tests validation handles nil webhook
func TestValidateNilWebhook(t *testing.T) {
	validator := NewWebhookValidator()

	result := validator.ValidatePrometheus(nil)
	if result.Valid {
		t.Error("Expected validation to fail for nil webhook")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected error for nil webhook")
	}
	if result.Errors[0].Field != "webhook" {
		t.Errorf("Expected error field 'webhook', got '%s'", result.Errors[0].Field)
	}
}

// TestValidateEmptyAlerts tests validation fails for webhook with no alerts
func TestValidateEmptyAlerts(t *testing.T) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{}, // Empty
	}

	result := validator.ValidatePrometheus(webhook)
	if result.Valid {
		t.Error("Expected validation to fail for empty alerts")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected error for empty alerts")
	}
}

// TestIsValidPrometheusLabelName tests label name validation helper
func TestIsValidPrometheusLabelName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"alertname", true},      // Valid: starts with letter
		{"_private", true},       // Valid: starts with underscore
		{"job_name", true},       // Valid: contains underscore
		{"http_requests_total", true}, // Valid: multiple underscores
		{"123invalid", false},    // Invalid: starts with digit
		{"invalid-name", false},  // Invalid: contains hyphen
		{"invalid.name", false},  // Invalid: contains dot
		{"", false},              // Invalid: empty
		{"a", true},              // Valid: single letter
		{"_", true},              // Valid: single underscore
		{"a1", true},             // Valid: letter + digit
		{"1a", false},            // Invalid: digit + letter
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidPrometheusLabelName(tt.name)
			if result != tt.expected {
				t.Errorf("isValidPrometheusLabelName(%q) = %v, expected %v", tt.name, result, tt.expected)
			}
		})
	}
}

// TestValidatePrometheusState tests state validation helper
func TestValidatePrometheusState(t *testing.T) {
	tests := []struct {
		state   string
		wantErr bool
	}{
		{"firing", false},
		{"pending", false},
		{"inactive", false},
		{"resolved", true},    // Invalid: not Prometheus state
		{"FIRING", true},      // Invalid: case sensitive
		{"unknown", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			err := validatePrometheusState(tt.state)
			hasErr := (err != nil)
			if hasErr != tt.wantErr {
				t.Errorf("validatePrometheusState(%q) error = %v, wantErr %v", tt.state, err, tt.wantErr)
			}
		})
	}
}

// TestValidatePrometheusTimestamp tests timestamp validation helper
func TestValidatePrometheusTimestamp(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		timestamp time.Time
		wantErr   bool
	}{
		{"past time", now.Add(-1 * time.Hour), false},
		{"recent past", now.Add(-1 * time.Minute), false},
		{"within tolerance", now.Add(3 * time.Minute), false}, // Within 5m tolerance
		{"future beyond tolerance", now.Add(10 * time.Minute), true},
		{"far future", now.Add(1 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrometheusTimestamp(tt.timestamp)
			hasErr := (err != nil)
			if hasErr != tt.wantErr {
				t.Errorf("validatePrometheusTimestamp(%s) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
