package publishing

import (
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

// ==================== WebhookValidator Tests ====================

func TestWebhookValidator_ValidateURL_HTTPS(t *testing.T) {
	validator := NewWebhookValidator(nil)

	tests := []struct {
		url       string
		shouldErr bool
	}{
		{"https://api.example.com/webhook", false},
		{"https://example.com:8443/webhook", false},
		{"http://api.example.com/webhook", true},  // Not HTTPS
		{"ftp://api.example.com/webhook", true},   // Not HTTPS
		{"", true},                                 // Empty
		{"not-a-url", true},                        // Invalid
		{"https://localhost/webhook", true},        // Localhost blocked
		{"https://127.0.0.1/webhook", true},        // Loopback blocked
		{"https://192.168.1.1/webhook", true},      // Private IP blocked
		{"https://user:pass@api.com/webhook", true}, // Credentials in URL
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidateURL(%s): expected error=%v, got error=%v", tt.url, tt.shouldErr, err != nil)
			}
		})
	}
}

func TestWebhookValidator_ValidatePayloadSize(t *testing.T) {
	validator := NewWebhookValidator(nil)

	tests := []struct {
		name      string
		size      int
		shouldErr bool
	}{
		{"empty", 0, false},
		{"small", 1024, false},           // 1 KB
		{"medium", 512 * 1024, false},    // 512 KB
		{"max", 1024 * 1024, false},      // 1 MB (exactly at limit)
		{"too_large", 1024*1024 + 1, true}, // 1 MB + 1 byte
		{"huge", 10 * 1024 * 1024, true}, // 10 MB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := make([]byte, tt.size)
			err := validator.ValidatePayloadSize(payload)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidatePayloadSize(%d bytes): expected error=%v, got error=%v", tt.size, tt.shouldErr, err != nil)
			}
		})
	}
}

func TestWebhookValidator_ValidateHeaders(t *testing.T) {
	validator := NewWebhookValidator(nil)

	tests := []struct {
		name      string
		headers   map[string]string
		shouldErr bool
	}{
		{"empty", map[string]string{}, false},
		{"few_headers", map[string]string{
			"Content-Type": "application/json",
			"X-Custom":     "value",
		}, false},
		{"max_headers", generateHeaders(100), false}, // Exactly 100
		{"too_many", generateHeaders(101), true},     // 101 headers
		{"large_value", map[string]string{
			"X-Large": string(make([]byte, 5*1024)), // 5 KB value
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateHeaders(tt.headers)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidateHeaders(%s): expected error=%v, got error=%v", tt.name, tt.shouldErr, err != nil)
			}
		})
	}
}

func TestWebhookValidator_ValidateTarget(t *testing.T) {
	validator := NewWebhookValidator(nil)

	tests := []struct {
		name      string
		target    *core.PublishingTarget
		shouldErr bool
	}{
		{
			name: "valid_target",
			target: &core.PublishingTarget{
				Name: "test-webhook",
				Type: "webhook",
				URL:  "https://api.example.com/webhook",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid_url",
			target: &core.PublishingTarget{
				Name: "test-webhook",
				Type: "webhook",
				URL:  "http://api.example.com/webhook",
			},
			shouldErr: true,
		},
		{
			name: "empty_url",
			target: &core.PublishingTarget{
				Name: "test-webhook",
				Type: "webhook",
				URL:  "",
			},
			shouldErr: true,
		},
		{
			name: "too_many_headers",
			target: &core.PublishingTarget{
				Name:    "test-webhook",
				Type:    "webhook",
				URL:     "https://api.example.com/webhook",
				Headers: generateHeaders(101),
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateTarget(tt.target)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidateTarget(%s): expected error=%v, got error=%v", tt.name, tt.shouldErr, err != nil)
			}
		})
	}
}

func TestWebhookValidator_ValidateFormat(t *testing.T) {
	validator := NewWebhookValidator(nil)

	tests := []struct {
		name      string
		payload   map[string]interface{}
		shouldErr bool
	}{
		{
			name: "valid_simple",
			payload: map[string]interface{}{
				"alert": "test",
				"severity": "critical",
			},
			shouldErr: false,
		},
		{
			name: "valid_nested",
			payload: map[string]interface{}{
				"alert": "test",
				"labels": map[string]string{
					"env": "prod",
				},
			},
			shouldErr: false,
		},
		{
			name:      "empty",
			payload:   map[string]interface{}{},
			shouldErr: false,
		},
		{
			name:      "nil",
			payload:   nil,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateFormat(tt.payload)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidateFormat(%s): expected error=%v, got error=%v", tt.name, tt.shouldErr, err != nil)
			}
		})
	}
}

func TestWebhookValidatorWithConfig_CustomLimits(t *testing.T) {
	config := ValidationConfig{
		MaxPayloadSize: 512 * 1024, // 512 KB (lower than default 1 MB)
		MaxHeaders:     50,          // 50 (lower than default 100)
		MaxHeaderSize:  2 * 1024,    // 2 KB (lower than default 4 KB)
		AllowedSchemes: []string{"https"},
		BlockedHosts:   []string{"localhost", "127.0.0.1"},
		MinTimeout:     1000000000,  // 1s
		MaxTimeout:     60000000000, // 60s
		MaxRetries:     3,           // 3 (lower than default 5)
	}

	validator := NewWebhookValidatorWithConfig(config, nil)

	// Test custom max payload size (512 KB limit)
	payload600KB := make([]byte, 600*1024)
	if err := validator.ValidatePayloadSize(payload600KB); err == nil {
		t.Error("Expected error for 600 KB payload (limit 512 KB), got nil")
	}

	// Test custom max headers (50 limit)
	headers60 := generateHeaders(60)
	if err := validator.ValidateHeaders(headers60); err == nil {
		t.Error("Expected error for 60 headers (limit 50), got nil")
	}

	// Test valid within custom limits
	payload400KB := make([]byte, 400*1024)
	if err := validator.ValidatePayloadSize(payload400KB); err != nil {
		t.Errorf("Unexpected error for 400 KB payload: %v", err)
	}

	headers40 := generateHeaders(40)
	if err := validator.ValidateHeaders(headers40); err != nil {
		t.Errorf("Unexpected error for 40 headers: %v", err)
	}
}

// ==================== Helper Functions ====================

func generateHeaders(count int) map[string]string {
	headers := make(map[string]string, count)
	for i := 0; i < count; i++ {
		headers[string(rune('A'+i%26))+string(rune('0'+i/26))] = "value"
	}
	return headers
}
