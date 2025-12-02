package config

import (
	"testing"
)

func TestDefaultConfigSanitizer_Sanitize(t *testing.T) {
	sanitizer := NewDefaultConfigSanitizer()

	cfg := &Config{
		Database: DatabaseConfig{
			Password: "secret123",
			URL:      "postgres://user:pass@host/db",
		},
		Redis: RedisConfig{
			Password: "redispass",
		},
		LLM: LLMConfig{
			APIKey: "sk-1234567890",
		},
		Webhook: WebhookConfig{
			Authentication: AuthenticationConfig{
				APIKey:    "webhook-key",
				JWTSecret: "jwt-secret",
			},
			Signature: SignatureConfig{
				Secret: "signature-secret",
			},
		},
		Server: ServerConfig{
			Port: 8080,
		},
	}

	sanitized := sanitizer.Sanitize(cfg)

	// Check that sensitive fields are redacted
	if sanitized.Database.Password != "***REDACTED***" {
		t.Errorf("Database.Password = %v, want ***REDACTED***", sanitized.Database.Password)
	}

	if sanitized.Redis.Password != "***REDACTED***" {
		t.Errorf("Redis.Password = %v, want ***REDACTED***", sanitized.Redis.Password)
	}

	if sanitized.LLM.APIKey != "***REDACTED***" {
		t.Errorf("LLM.APIKey = %v, want ***REDACTED***", sanitized.LLM.APIKey)
	}

	if sanitized.Webhook.Authentication.APIKey != "***REDACTED***" {
		t.Errorf("Webhook.Authentication.APIKey = %v, want ***REDACTED***", sanitized.Webhook.Authentication.APIKey)
	}

	if sanitized.Webhook.Authentication.JWTSecret != "***REDACTED***" {
		t.Errorf("Webhook.Authentication.JWTSecret = %v, want ***REDACTED***", sanitized.Webhook.Authentication.JWTSecret)
	}

	if sanitized.Webhook.Signature.Secret != "***REDACTED***" {
		t.Errorf("Webhook.Signature.Secret = %v, want ***REDACTED***", sanitized.Webhook.Signature.Secret)
	}

	// Check that non-sensitive fields are preserved
	if sanitized.Server.Port != cfg.Server.Port {
		t.Errorf("Server.Port = %v, want %v", sanitized.Server.Port, cfg.Server.Port)
	}
}

func TestDefaultConfigSanitizer_DeepCopy(t *testing.T) {
	sanitizer := NewDefaultConfigSanitizer()

	cfg := &Config{
		Database: DatabaseConfig{
			Password: "original",
		},
		Server: ServerConfig{
			Port: 8080,
		},
	}

	sanitized := sanitizer.Sanitize(cfg)

	// Original should not be mutated
	if cfg.Database.Password != "original" {
		t.Error("Sanitize() mutated original config")
	}

	// Sanitized should be different instance
	if sanitized == cfg {
		t.Error("Sanitize() did not create deep copy")
	}
}

func TestNewConfigSanitizer_CustomRedaction(t *testing.T) {
	customValue := "[HIDDEN]"
	sanitizer := NewConfigSanitizer(customValue)

	cfg := &Config{
		Database: DatabaseConfig{
			Password: "secret",
		},
	}

	sanitized := sanitizer.Sanitize(cfg)

	if sanitized.Database.Password != customValue {
		t.Errorf("Database.Password = %v, want %v", sanitized.Database.Password, customValue)
	}
}

func TestDefaultConfigSanitizer_EmptyConfig(t *testing.T) {
	sanitizer := NewDefaultConfigSanitizer()
	cfg := &Config{}

	sanitized := sanitizer.Sanitize(cfg)

	if sanitized == nil {
		t.Error("Sanitize() returned nil for empty config")
	}
}
