package webhook

import (
	"strings"
	"testing"
)

func TestEnhancedValidator_ValidateLabelKey(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid alphanumeric", "alertname", false},
		{"valid with underscore", "alert_name", false},
		{"valid starting with underscore", "_private", false},
		{"valid uppercase", "AlertName", false},
		{"valid mixed case", "Alert_Name_123", false},
		{"empty key", "", true},
		{"starts with number", "123alert", true},
		{"contains hyphen", "alert-name", true},
		{"contains dot", "alert.name", true},
		{"contains special char", "alert@name", true},
		{"too long", strings.Repeat("a", 256), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateLabelKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLabelKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateLabelValue(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid simple", "value", false},
		{"valid with spaces", "value with spaces", false},
		{"valid with newline", "line1\nline2", false},
		{"valid with tab", "col1\tcol2", false},
		{"valid empty", "", false},
		{"valid long", strings.Repeat("a", 1024), false},
		{"too long", strings.Repeat("a", 1025), true},
		{"control character", "value\x00end", true},
		{"control character 2", "value\x07end", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateLabelValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLabelValue() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateLabels(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		labels  map[string]string
		wantErr bool
	}{
		{
			name: "valid labels",
			labels: map[string]string{
				"alertname": "HighCPU",
				"severity":  "critical",
				"instance":  "server1",
			},
			wantErr: false,
		},
		{
			name: "invalid key",
			labels: map[string]string{
				"alert-name": "HighCPU",
			},
			wantErr: true,
		},
		{
			name: "invalid value",
			labels: map[string]string{
				"alertname": strings.Repeat("a", 1025),
			},
			wantErr: true,
		},
		{
			name:    "empty labels",
			labels:  map[string]string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateLabels(tt.labels)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLabels() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateStatus(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{"firing", "firing", false},
		{"resolved", "resolved", false},
		{"empty", "", true},
		{"invalid", "pending", true},
		{"uppercase", "FIRING", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateStatus(tt.status)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateURL(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://example.com", false},
		{"valid http", "http://example.com/path", false},
		{"valid with port", "https://example.com:8080", false},
		{"valid with query", "https://example.com?key=value", false},
		{"empty", "", false}, // Empty is allowed
		{"localhost", "http://localhost", true},
		{"localhost with port", "http://localhost:8080", true},
		{"127.0.0.1", "http://127.0.0.1", true},
		{"192.168.x", "http://192.168.1.1", true},
		{"10.x", "http://10.0.0.1", true},
		{"172.16.x", "http://172.16.0.1", true},
		{"private hostname", "http://server.local", true},
		{"ipv6 loopback", "http://[::1]", true},
		{"ftp scheme", "ftp://example.com", true},
		{"file scheme", "file:///etc/passwd", true},
		{"javascript", "javascript:alert(1)", true},
		{"data uri", "data:text/html,<script>alert(1)</script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateURL_AllowPrivate(t *testing.T) {
	config := DefaultValidationConfig()
	config.BlockPrivateIPs = false

	validator, err := NewEnhancedValidator(config)
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	// Private IPs should be allowed when BlockPrivateIPs is false
	urls := []string{
		"http://192.168.1.1",
		"http://10.0.0.1",
		"http://172.16.0.1",
	}

	for _, urlStr := range urls {
		err := validator.ValidateURL(urlStr)
		if err != nil {
			t.Errorf("ValidateURL(%q) with BlockPrivateIPs=false should not error, got: %v", urlStr, err)
		}
	}
}

func TestEnhancedValidator_ValidateAnnotations(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{
			name: "valid annotations",
			annotations: map[string]string{
				"summary":     "High CPU usage",
				"description": "CPU usage is above 90%",
			},
			wantErr: false,
		},
		{
			name: "valid with URL",
			annotations: map[string]string{
				"summary":     "Alert",
				"runbook_url": "https://docs.example.com/runbook",
			},
			wantErr: false,
		},
		{
			name: "invalid URL",
			annotations: map[string]string{
				"runbook_url": "http://localhost/runbook",
			},
			wantErr: true,
		},
		{
			name: "invalid URL in custom field",
			annotations: map[string]string{
				"dashboard_url": "http://192.168.1.1",
			},
			wantErr: true,
		},
		{
			name: "too long value",
			annotations: map[string]string{
				"description": strings.Repeat("a", 4097),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateAnnotations(tt.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAnnotations() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateAlert(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		alert   *AlertmanagerAlert
		wantErr bool
	}{
		{
			name: "valid alert",
			alert: &AlertmanagerAlert{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "HighCPU",
					"severity":  "critical",
				},
				Annotations: map[string]string{
					"summary": "High CPU usage",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid status",
			alert: &AlertmanagerAlert{
				Status: "pending",
				Labels: map[string]string{
					"alertname": "HighCPU",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid label key",
			alert: &AlertmanagerAlert{
				Status: "firing",
				Labels: map[string]string{
					"alert-name": "HighCPU",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid annotation URL",
			alert: &AlertmanagerAlert{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "HighCPU",
				},
				Annotations: map[string]string{
					"runbook_url": "http://localhost",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateAlert(tt.alert)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAlert() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnhancedValidator_ValidateWebhook(t *testing.T) {
	validator, err := NewEnhancedValidator(DefaultValidationConfig())
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		webhook *AlertmanagerWebhook
		wantErr bool
	}{
		{
			name: "valid webhook",
			webhook: &AlertmanagerWebhook{
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{
							"alertname": "HighCPU",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "empty alerts",
			webhook: &AlertmanagerWebhook{Alerts: []AlertmanagerAlert{}},
			wantErr: true,
		},
		{
			name: "invalid alert in list",
			webhook: &AlertmanagerWebhook{
				Alerts: []AlertmanagerAlert{
					{
						Status: "firing",
						Labels: map[string]string{
							"alertname": "Valid",
						},
					},
					{
						Status: "invalid",
						Labels: map[string]string{
							"alertname": "Invalid",
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateWebhook(tt.webhook)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func BenchmarkValidateLabelKey(b *testing.B) {
	validator, _ := NewEnhancedValidator(DefaultValidationConfig())
	key := "alertname"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validator.ValidateLabelKey(key)
	}
}

func BenchmarkValidateLabels(b *testing.B) {
	validator, _ := NewEnhancedValidator(DefaultValidationConfig())
	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server1",
		"namespace": "production",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validator.ValidateLabels(labels)
	}
}

func BenchmarkValidateURL(b *testing.B) {
	validator, _ := NewEnhancedValidator(DefaultValidationConfig())
	url := "https://docs.example.com/runbook"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}

func BenchmarkValidateAlert(b *testing.B) {
	validator, _ := NewEnhancedValidator(DefaultValidationConfig())
	alert := &AlertmanagerAlert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		Annotations: map[string]string{
			"summary":     "High CPU usage",
			"runbook_url": "https://docs.example.com/runbook",
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validator.ValidateAlert(alert)
	}
}
