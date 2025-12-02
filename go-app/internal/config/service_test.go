package config

import (
	"context"
	"testing"
	"time"
)

func TestDefaultConfigService_GetConfig(t *testing.T) {
	// Create test config
	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "localhost",
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			Database: "testdb",
			Username: "testuser",
			Password: "testpass",
		},
		App: AppConfig{
			Name:        "test-app",
			Version:     "1.0.0",
			Environment: "test",
		},
	}

	service := NewConfigService(cfg, "/test/config.yaml", time.Now(), ConfigSourceFile)

	tests := []struct {
		name    string
		opts    GetConfigOptions
		wantErr bool
	}{
		{
			name: "JSON format default",
			opts: GetConfigOptions{
				Format:   "json",
				Sanitize: true,
			},
			wantErr: false,
		},
		{
			name: "YAML format",
			opts: GetConfigOptions{
				Format:   "yaml",
				Sanitize: true,
			},
			wantErr: false,
		},
		{
			name: "Unsanitized config",
			opts: GetConfigOptions{
				Format:   "json",
				Sanitize: false,
			},
			wantErr: false,
		},
		{
			name: "Section filtering",
			opts: GetConfigOptions{
				Format:   "json",
				Sanitize: true,
				Sections: []string{"server", "app"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			resp, err := service.GetConfig(ctx, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && resp == nil {
				t.Error("GetConfig() returned nil response")
				return
			}
			if !tt.wantErr {
				// Validate response structure
				if resp.Version == "" {
					t.Error("GetConfig() version is empty")
				}
				if resp.Source != ConfigSourceFile {
					t.Errorf("GetConfig() source = %v, want %v", resp.Source, ConfigSourceFile)
				}
				if resp.Config == nil {
					t.Error("GetConfig() config is nil")
				}
			}
		})
	}
}

func TestDefaultConfigService_GetConfigVersion(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
		},
		App: AppConfig{
			Name: "test",
		},
	}

	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)

	version1 := service.GetConfigVersion()
	if version1 == "" {
		t.Error("GetConfigVersion() returned empty version")
	}

	// Version should be deterministic (same config = same version)
	version2 := service.GetConfigVersion()
	if version1 != version2 {
		t.Error("GetConfigVersion() is not deterministic")
	}

	// Different config should produce different version
	cfg2 := &Config{
		Server: ServerConfig{
			Port: 9090, // Different port
		},
		App: AppConfig{
			Name: "test",
		},
	}
	service2 := NewConfigService(cfg2, "", time.Now(), ConfigSourceDefaults)
	version3 := service2.GetConfigVersion()
	if version1 == version3 {
		t.Error("GetConfigVersion() should differ for different configs")
	}
}

func TestDefaultConfigService_GetConfigSource(t *testing.T) {
	tests := []struct {
		name   string
		source ConfigSource
	}{
		{"File source", ConfigSourceFile},
		{"Env source", ConfigSourceEnv},
		{"Defaults source", ConfigSourceDefaults},
		{"Mixed source", ConfigSourceMixed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{App: AppConfig{Name: "test"}}
			service := NewConfigService(cfg, "", time.Now(), tt.source)
			if got := service.GetConfigSource(); got != tt.source {
				t.Errorf("GetConfigSource() = %v, want %v", got, tt.source)
			}
		})
	}
}

func TestDefaultConfigService_Cache(t *testing.T) {
	cfg := &Config{App: AppConfig{Name: "test"}}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults).(*DefaultConfigService)

	opts := GetConfigOptions{Format: "json", Sanitize: true}
	ctx := context.Background()

	// First call - should populate cache
	resp1, err := service.GetConfig(ctx, opts)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	// Second call immediately - should use cache
	resp2, err := service.GetConfig(ctx, opts)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	// Responses should be identical (same pointer due to cache)
	if resp1 != resp2 {
		t.Error("GetConfig() cache not working - different responses")
	}
}

func TestDefaultConfigService_SectionFiltering(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080, Host: "localhost"},
		Database: DatabaseConfig{Host: "localhost", Port: 5432},
		App:      AppConfig{Name: "test"},
	}
	service := NewConfigService(cfg, "", time.Now(), ConfigSourceDefaults)

	ctx := context.Background()
	opts := GetConfigOptions{
		Format:   "json",
		Sanitize: true,
		Sections: []string{"server", "app"},
	}

	resp, err := service.GetConfig(ctx, opts)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	// Check that only requested sections are present
	// JSON marshaling uses struct field names (capitalized), but we convert to map
	// The map keys should match the struct field names (Server, App, not server, app)
	configMap := resp.Config

	// Check that Server section exists and has data
	server, ok := configMap["Server"].(map[string]interface{})
	if !ok || server == nil {
		t.Error("Section filtering: Server section missing")
	} else if server["Port"] == nil {
		t.Error("Section filtering: Server.Port missing")
	}

	// Check that App section exists
	app, ok := configMap["App"].(map[string]interface{})
	if !ok || app == nil {
		t.Error("Section filtering: App section missing")
	} else if app["Name"] == nil {
		t.Error("Section filtering: App.Name missing")
	}

	// Database should be filtered out (not in sections list)
	// After filtering, Database should be empty or nil
	if db, ok := configMap["Database"].(map[string]interface{}); ok && db != nil {
		// Check if database has any non-zero values
		if host, ok := db["Host"].(string); ok && host != "" {
			t.Errorf("Section filtering: Database.Host should be filtered out, got %v", host)
		}
	}
}
