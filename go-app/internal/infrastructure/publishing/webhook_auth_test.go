package publishing

import (
	"net/http"
	"testing"
)

// ==================== BearerAuthStrategy Tests ====================

func TestBearerAuthStrategy_Apply(t *testing.T) {
	strategy := &BearerAuthStrategy{}
	config := AuthConfig{
		Type:  AuthTypeBearer,
		Token: "test_bearer_token_123",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	authHeader := req.Header.Get("Authorization")
	expected := "Bearer test_bearer_token_123"
	if authHeader != expected {
		t.Errorf("Expected Authorization=%s, got %s", expected, authHeader)
	}
}

func TestBearerAuthStrategy_Apply_MissingToken(t *testing.T) {
	strategy := &BearerAuthStrategy{}
	config := AuthConfig{
		Type:  AuthTypeBearer,
		Token: "",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err == nil {
		t.Error("Expected error for missing token, got nil")
	}
}

// ==================== BasicAuthStrategy Tests ====================

func TestBasicAuthStrategy_Apply(t *testing.T) {
	strategy := &BasicAuthStrategy{}
	config := AuthConfig{
		Type:     AuthTypeBasic,
		Username: "admin",
		Password: "secret123",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		t.Error("Expected Authorization header, got empty")
	}
	if !containsPrefix(authHeader, "Basic ") {
		t.Errorf("Expected Basic auth header, got %s", authHeader)
	}
}

func TestBasicAuthStrategy_Apply_MissingCredentials(t *testing.T) {
	strategy := &BasicAuthStrategy{}
	config := AuthConfig{
		Type:     AuthTypeBasic,
		Username: "",
		Password: "",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err == nil {
		t.Error("Expected error for missing credentials, got nil")
	}
}

// ==================== APIKeyAuthStrategy Tests ====================

func TestAPIKeyAuthStrategy_Apply(t *testing.T) {
	strategy := &APIKeyAuthStrategy{}
	config := AuthConfig{
		Type:         AuthTypeAPIKey,
		APIKey:       "api_key_xyz",
		APIKeyHeader: "X-API-Key",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	apiKey := req.Header.Get("X-API-Key")
	if apiKey != "api_key_xyz" {
		t.Errorf("Expected X-API-Key=api_key_xyz, got %s", apiKey)
	}
}

func TestAPIKeyAuthStrategy_Apply_DefaultHeader(t *testing.T) {
	strategy := &APIKeyAuthStrategy{}
	config := AuthConfig{
		Type:         AuthTypeAPIKey,
		APIKey:       "api_key_xyz",
		APIKeyHeader: "", // Should default to "X-API-Key"
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check default header
	apiKey := req.Header.Get("X-API-Key")
	if apiKey != "api_key_xyz" {
		t.Errorf("Expected default X-API-Key=api_key_xyz, got %s", apiKey)
	}
}

func TestAPIKeyAuthStrategy_Apply_MissingAPIKey(t *testing.T) {
	strategy := &APIKeyAuthStrategy{}
	config := AuthConfig{
		Type:         AuthTypeAPIKey,
		APIKey:       "",
		APIKeyHeader: "X-API-Key",
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err == nil {
		t.Error("Expected error for missing API key, got nil")
	}
}

// ==================== CustomAuthStrategy Tests ====================

func TestCustomAuthStrategy_Apply(t *testing.T) {
	strategy := &CustomAuthStrategy{}
	config := AuthConfig{
		Type: AuthTypeCustom,
		CustomHeaders: map[string]string{
			"X-Custom-Auth": "custom_value",
			"X-Request-ID":  "req_123",
		},
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	customAuth := req.Header.Get("X-Custom-Auth")
	if customAuth != "custom_value" {
		t.Errorf("Expected X-Custom-Auth=custom_value, got %s", customAuth)
	}

	requestID := req.Header.Get("X-Request-ID")
	if requestID != "req_123" {
		t.Errorf("Expected X-Request-ID=req_123, got %s", requestID)
	}
}

func TestCustomAuthStrategy_Apply_NoHeaders(t *testing.T) {
	strategy := &CustomAuthStrategy{}
	config := AuthConfig{
		Type:          AuthTypeCustom,
		CustomHeaders: nil,
	}

	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	err := strategy.ApplyAuth(req, config)

	if err == nil {
		t.Error("Expected error for missing custom headers, got nil")
	}
}

// ==================== Helper Functions ====================

func containsPrefix(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
