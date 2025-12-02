package config

import (
	"context"
	"testing"
	"time"
)

func BenchmarkDefaultConfigService_GetConfig(b *testing.B) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080, Host: "localhost"},
		Database: DatabaseConfig{Host: "localhost", Port: 5432, Database: "testdb"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		App:      AppConfig{Name: "test-app", Version: "1.0.0"},
	}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)
	ctx := context.Background()
	opts := GetConfigOptions{Format: "json", Sanitize: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetConfig(ctx, opts)
	}
}

func BenchmarkDefaultConfigService_GetConfig_CacheHit(b *testing.B) {
	cfg := &Config{App: AppConfig{Name: "test"}}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)
	ctx := context.Background()
	opts := GetConfigOptions{Format: "json", Sanitize: true}

	// Warm up cache
	_, _ = service.GetConfig(ctx, opts)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetConfig(ctx, opts)
	}
}

func BenchmarkDefaultConfigService_GetConfig_YAML(b *testing.B) {
	cfg := &Config{
		Server: ServerConfig{Port: 8080},
		App:    AppConfig{Name: "test"},
	}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)
	ctx := context.Background()
	opts := GetConfigOptions{Format: "yaml", Sanitize: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetConfig(ctx, opts)
	}
}

func BenchmarkDefaultConfigService_GetConfig_SectionFilter(b *testing.B) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		Database: DatabaseConfig{Host: "localhost"},
		App:      AppConfig{Name: "test"},
	}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)
	ctx := context.Background()
	opts := GetConfigOptions{
		Format:   "json",
		Sanitize: true,
		Sections: []string{"server", "app"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetConfig(ctx, opts)
	}
}

func BenchmarkDefaultConfigService_GetConfigVersion(b *testing.B) {
	cfg := &Config{
		Server: ServerConfig{Port: 8080},
		App:    AppConfig{Name: "test"},
	}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.GetConfigVersion()
	}
}
