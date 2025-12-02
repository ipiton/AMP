package publishing

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
)

// AuthStrategy defines authentication strategy interface (Strategy pattern)
type AuthStrategy interface {
	// ApplyAuth applies authentication to HTTP request
	ApplyAuth(req *http.Request, config AuthConfig) error

	// Name returns the strategy name
	Name() string
}

// AuthManager orchestrates authentication strategies
type AuthManager struct {
	strategies map[AuthType]AuthStrategy
	logger     *slog.Logger
}

// NewAuthManager creates a new authentication manager
func NewAuthManager(logger *slog.Logger) *AuthManager {
	return &AuthManager{
		strategies: map[AuthType]AuthStrategy{
			AuthTypeBearer: &BearerAuthStrategy{},
			AuthTypeBasic:  &BasicAuthStrategy{},
			AuthTypeAPIKey: &APIKeyAuthStrategy{},
			AuthTypeCustom: &CustomAuthStrategy{},
		},
		logger: logger,
	}
}

// ApplyAuth applies authentication to HTTP request using configured strategy
func (m *AuthManager) ApplyAuth(req *http.Request, config AuthConfig) error {
	strategy, exists := m.strategies[config.Type]
	if !exists {
		return fmt.Errorf("unsupported auth type: %s", config.Type)
	}

	m.logger.Debug("Applying authentication",
		slog.String("auth_type", string(config.Type)),
		slog.String("strategy", strategy.Name()),
		slog.String("url", maskURL(req.URL.String())))

	if err := strategy.ApplyAuth(req, config); err != nil {
		m.logger.Error("Authentication failed",
			slog.String("auth_type", string(config.Type)),
			slog.String("error", err.Error()))
		return err
	}

	m.logger.Debug("Authentication applied successfully",
		slog.String("auth_type", string(config.Type)))

	return nil
}

// ==================== AUTH STRATEGIES ====================

// BearerAuthStrategy implements Bearer Token authentication
// Authorization: Bearer <token>
type BearerAuthStrategy struct{}

// ApplyAuth applies Bearer token authentication
func (s *BearerAuthStrategy) ApplyAuth(req *http.Request, config AuthConfig) error {
	if config.Token == "" {
		return ErrMissingAuthToken
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.Token))
	return nil
}

// Name returns strategy name
func (s *BearerAuthStrategy) Name() string {
	return "BearerAuth"
}

// BasicAuthStrategy implements HTTP Basic Authentication
// Authorization: Basic <base64(username:password)>
type BasicAuthStrategy struct{}

// ApplyAuth applies Basic authentication
func (s *BasicAuthStrategy) ApplyAuth(req *http.Request, config AuthConfig) error {
	if config.Username == "" || config.Password == "" {
		return ErrMissingBasicAuthCredentials
	}

	// Use net/http's built-in Basic Auth
	req.SetBasicAuth(config.Username, config.Password)
	return nil
}

// Name returns strategy name
func (s *BasicAuthStrategy) Name() string {
	return "BasicAuth"
}

// APIKeyAuthStrategy implements API Key header authentication
// X-API-Key: <key> (or custom header name)
type APIKeyAuthStrategy struct{}

// ApplyAuth applies API Key authentication
func (s *APIKeyAuthStrategy) ApplyAuth(req *http.Request, config AuthConfig) error {
	if config.APIKey == "" {
		return ErrMissingAPIKey
	}

	// Use custom header name if specified, otherwise default to "X-API-Key"
	headerName := config.APIKeyHeader
	if headerName == "" {
		headerName = "X-API-Key"
	}

	req.Header.Set(headerName, config.APIKey)
	return nil
}

// Name returns strategy name
func (s *APIKeyAuthStrategy) Name() string {
	return "APIKeyAuth"
}

// CustomAuthStrategy implements custom header authentication
// Allows setting any arbitrary headers
type CustomAuthStrategy struct{}

// ApplyAuth applies custom headers authentication
func (s *CustomAuthStrategy) ApplyAuth(req *http.Request, config AuthConfig) error {
	if len(config.CustomHeaders) == 0 {
		return ErrNoCustomHeaders
	}

	for key, value := range config.CustomHeaders {
		req.Header.Set(key, value)
	}

	return nil
}

// Name returns strategy name
func (s *CustomAuthStrategy) Name() string {
	return "CustomAuth"
}

// ==================== HELPER FUNCTIONS ====================

// maskURL masks sensitive parts of URL for logging
func maskURL(urlStr string) string {
	// Simple masking: show only scheme and host
	// Example: https://api.example.com/webhook?key=secret → https://api.example.com/***
	if len(urlStr) > 30 {
		// Find the position after "://"
		schemeEnd := 0
		for i := 0; i < len(urlStr)-3; i++ {
			if urlStr[i:i+3] == "://" {
				schemeEnd = i + 3
				break
			}
		}

		// Find the first "/" after scheme
		pathStart := schemeEnd
		for i := schemeEnd; i < len(urlStr); i++ {
			if urlStr[i] == '/' || urlStr[i] == '?' {
				pathStart = i
				break
			}
		}

		// Return scheme + host + masked path
		if pathStart > schemeEnd {
			return urlStr[:pathStart] + "/***"
		}
	}

	return urlStr
}

// maskToken masks authentication token for logging
func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	// Show first 4 and last 4 characters
	return fmt.Sprintf("%s...%s", token[:4], token[len(token)-4:])
}

// maskCredentials masks username/password for logging
func maskCredentials(username, password string) string {
	maskedPassword := "***"
	if len(password) > 0 {
		maskedPassword = fmt.Sprintf("%d chars", len(password))
	}
	return fmt.Sprintf("%s:%s", username, maskedPassword)
}

// encodeBasicAuth encodes username:password to base64 (for logging/debugging)
func encodeBasicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
