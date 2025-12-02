package publishing

import (
	"testing"
	"time"
)

// ==================== WebhookRetryConfig Tests ====================

func TestWebhookRetryConfig_Defaults(t *testing.T) {
	config := DefaultWebhookRetryConfig

	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", config.MaxRetries)
	}
	if config.BaseBackoff != 100*time.Millisecond {
		t.Errorf("Expected BaseBackoff=100ms, got %v", config.BaseBackoff)
	}
	if config.MaxBackoff != 5*time.Second {
		t.Errorf("Expected MaxBackoff=5s, got %v", config.MaxBackoff)
	}
	if config.Multiplier != 2.0 {
		t.Errorf("Expected Multiplier=2.0, got %f", config.Multiplier)
	}
}

func TestWebhookRetryConfig_CalculateBackoff(t *testing.T) {
	config := WebhookRetryConfig{
		MaxRetries:  3,
		BaseBackoff: 100 * time.Millisecond,
		MaxBackoff:  5 * time.Second,
		Multiplier:  2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},  // 100ms * 2^0 = 100ms
		{1, 200 * time.Millisecond},  // 100ms * 2^1 = 200ms
		{2, 400 * time.Millisecond},  // 100ms * 2^2 = 400ms
		{3, 800 * time.Millisecond},  // 100ms * 2^3 = 800ms
		{4, 1600 * time.Millisecond}, // 100ms * 2^4 = 1600ms
		{5, 3200 * time.Millisecond}, // 100ms * 2^5 = 3200ms
		{6, 5 * time.Second},         // 100ms * 2^6 = 6400ms, capped at 5s
		{10, 5 * time.Second},        // Capped at MaxBackoff
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.attempt+'0')), func(t *testing.T) {
			result := config.CalculateBackoff(tt.attempt)
			if result != tt.expected {
				t.Errorf("Attempt %d: expected %v, got %v", tt.attempt, tt.expected, result)
			}
		})
	}
}

func TestWebhookRetryConfig_CalculateBackoff_ZeroMultiplier(t *testing.T) {
	config := WebhookRetryConfig{
		MaxRetries:  3,
		BaseBackoff: 100 * time.Millisecond,
		MaxBackoff:  5 * time.Second,
		Multiplier:  0, // Invalid, should default to 1.0
	}

	// With zero multiplier, backoff should remain constant at BaseBackoff
	for attempt := 0; attempt < 5; attempt++ {
		result := config.CalculateBackoff(attempt)
		if result != config.BaseBackoff {
			t.Errorf("Attempt %d: expected %v, got %v", attempt, config.BaseBackoff, result)
		}
	}
}

// ==================== AuthConfig Tests ====================

func TestAuthConfig_BearerToken(t *testing.T) {
	config := &AuthConfig{
		Type:  AuthTypeBearer,
		Token: "test_bearer_token_123",
	}

	if config.Type != AuthTypeBearer {
		t.Errorf("Expected type=bearer, got %s", config.Type)
	}
	if config.Token != "test_bearer_token_123" {
		t.Errorf("Expected token=test_bearer_token_123, got %s", config.Token)
	}
}

func TestAuthConfig_BasicAuth(t *testing.T) {
	config := &AuthConfig{
		Type:     AuthTypeBasic,
		Username: "admin",
		Password: "secret123",
	}

	if config.Type != AuthTypeBasic {
		t.Errorf("Expected type=basic, got %s", config.Type)
	}
	if config.Username != "admin" {
		t.Errorf("Expected username=admin, got %s", config.Username)
	}
	if config.Password != "secret123" {
		t.Errorf("Expected password=secret123, got %s", config.Password)
	}
}

func TestAuthConfig_APIKey(t *testing.T) {
	config := &AuthConfig{
		Type:         AuthTypeAPIKey,
		APIKey:       "api_key_xyz",
		APIKeyHeader: "X-API-Key",
	}

	if config.Type != AuthTypeAPIKey {
		t.Errorf("Expected type=apikey, got %s", config.Type)
	}
	if config.APIKey != "api_key_xyz" {
		t.Errorf("Expected apikey=api_key_xyz, got %s", config.APIKey)
	}
	if config.APIKeyHeader != "X-API-Key" {
		t.Errorf("Expected header=X-API-Key, got %s", config.APIKeyHeader)
	}
}

func TestAuthConfig_CustomHeaders(t *testing.T) {
	config := &AuthConfig{
		Type: AuthTypeCustom,
		CustomHeaders: map[string]string{
			"X-Custom-Auth": "custom_value",
			"X-Request-ID":  "req_123",
		},
	}

	if config.Type != AuthTypeCustom {
		t.Errorf("Expected type=custom, got %s", config.Type)
	}
	if len(config.CustomHeaders) != 2 {
		t.Errorf("Expected 2 custom headers, got %d", len(config.CustomHeaders))
	}
	if config.CustomHeaders["X-Custom-Auth"] != "custom_value" {
		t.Errorf("Expected X-Custom-Auth=custom_value, got %s", config.CustomHeaders["X-Custom-Auth"])
	}
}

// ==================== WebhookRequest Tests ====================

func TestWebhookRequest_Creation(t *testing.T) {
	payload := map[string]interface{}{
		"alert": "test",
		"severity": "critical",
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"X-Custom-Header": "value",
	}

	req := &WebhookRequest{
		URL:     "https://api.example.com/webhooks",
		Payload: payload,
		Headers: headers,
	}

	if req.URL != "https://api.example.com/webhooks" {
		t.Errorf("Expected URL=https://api.example.com/webhooks, got %s", req.URL)
	}
	if req.Payload["alert"] != "test" {
		t.Errorf("Expected payload alert=test, got %v", req.Payload["alert"])
	}
	if len(req.Headers) != 2 {
		t.Errorf("Expected 2 headers, got %d", len(req.Headers))
	}
}

// ==================== WebhookResponse Tests ====================

func TestWebhookResponse_Success(t *testing.T) {
	resp := &WebhookResponse{
		StatusCode: 200,
		Body:       []byte(`{"status":"ok"}`),
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}

	if resp.StatusCode != 200 {
		t.Errorf("Expected StatusCode=200, got %d", resp.StatusCode)
	}
	if string(resp.Body) != `{"status":"ok"}` {
		t.Errorf("Expected body={\"status\":\"ok\"}, got %s", string(resp.Body))
	}
}

func TestWebhookResponse_Error(t *testing.T) {
	resp := &WebhookResponse{
		StatusCode: 500,
		Body:       []byte(`{"error":"internal server error"}`),
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}

	if resp.StatusCode != 500 {
		t.Errorf("Expected StatusCode=500, got %d", resp.StatusCode)
	}
	if string(resp.Body) != `{"error":"internal server error"}` {
		t.Errorf("Unexpected body: %s", string(resp.Body))
	}
}

// ==================== AuthType Tests ====================

func TestAuthType_Constants(t *testing.T) {
	tests := []struct {
		authType AuthType
		expected string
	}{
		{AuthTypeBearer, "bearer"},
		{AuthTypeBasic, "basic"},
		{AuthTypeAPIKey, "apikey"},
		{AuthTypeCustom, "custom"},
	}

	for _, tt := range tests {
		t.Run(string(tt.authType), func(t *testing.T) {
			if string(tt.authType) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.authType))
			}
		})
	}
}
