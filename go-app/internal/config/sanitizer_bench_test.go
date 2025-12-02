package config

import (
	"testing"
)

func BenchmarkDefaultConfigSanitizer_Sanitize(b *testing.B) {
	sanitizer := NewDefaultConfigSanitizer()
	cfg := &Config{
		Database: DatabaseConfig{
			Password: "secret123",
			Host:     "localhost",
			Port:     5432,
		},
		Redis: RedisConfig{
			Password: "redispass",
			Addr:     "localhost:6379",
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
			Host: "localhost",
		},
		App: AppConfig{
			Name: "test-app",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sanitizer.Sanitize(cfg)
	}
}
