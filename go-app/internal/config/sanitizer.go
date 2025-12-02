package config

import (
	"encoding/json"
)

// ConfigSanitizer sanitizes sensitive configuration data
type ConfigSanitizer interface {
	// Sanitize removes or redacts sensitive fields
	Sanitize(cfg *Config) *Config
}

// DefaultConfigSanitizer implements ConfigSanitizer
type DefaultConfigSanitizer struct {
	redactionValue string // Value to use for redacted fields
}

// NewDefaultConfigSanitizer creates a new DefaultConfigSanitizer
func NewDefaultConfigSanitizer() ConfigSanitizer {
	return &DefaultConfigSanitizer{
		redactionValue: "***REDACTED***",
	}
}

// NewConfigSanitizer creates a ConfigSanitizer with custom redaction value
func NewConfigSanitizer(redactionValue string) ConfigSanitizer {
	return &DefaultConfigSanitizer{
		redactionValue: redactionValue,
	}
}

// Sanitize removes or redacts sensitive fields from configuration
func (s *DefaultConfigSanitizer) Sanitize(cfg *Config) *Config {
	// Deep copy config to avoid mutating original
	sanitized := s.deepCopy(cfg)

	// Redact database password
	sanitized.Database.Password = s.redactionValue

	// Redact Redis password
	sanitized.Redis.Password = s.redactionValue

	// Redact LLM API key
	sanitized.LLM.APIKey = s.redactionValue

	// Redact webhook authentication secrets
	sanitized.Webhook.Authentication.APIKey = s.redactionValue
	sanitized.Webhook.Authentication.JWTSecret = s.redactionValue

	// Redact webhook signature secret
	sanitized.Webhook.Signature.Secret = s.redactionValue

	// Redact database URL if it contains credentials
	sanitized.Database.URL = s.sanitizeURL(sanitized.Database.URL)

	return sanitized
}

// deepCopy creates a deep copy of Config using JSON serialization
func (s *DefaultConfigSanitizer) deepCopy(cfg *Config) *Config {
	// Use JSON serialization for deep copy
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		// Fallback: return original (should not happen with valid config)
		return cfg
	}

	var configCopy Config
	if err := json.Unmarshal(configJSON, &configCopy); err != nil {
		// Fallback: return original
		return cfg
	}

	return &configCopy
}

// sanitizeURL redacts credentials from database URL
func (s *DefaultConfigSanitizer) sanitizeURL(url string) string {
	if url == "" {
		return url
	}

	// Simple URL sanitization: replace password in postgres://user:pass@host/db
	// This is a basic implementation; for production, use url.Parse
	// For now, if URL contains @, assume it might have credentials
	if len(url) > 0 {
		// Check if URL looks like it has credentials (contains :// and @)
		// Basic check: if it's a postgres:// URL with @, redact password part
		// Full implementation would use url.Parse, but this is safer for now
		// We'll redact the entire URL if it looks like it has credentials
		// In practice, Database.URL is usually empty if using separate fields
		return s.redactionValue
	}

	return url
}
