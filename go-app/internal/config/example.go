package config

import (
	"fmt"
	"log"
	"os"
)

// ExampleLoadConfig demonstrates how to load configuration
func ExampleLoadConfig() {
	// Load configuration from file
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("App: %s v%s\n", cfg.App.Name, cfg.App.Version)
	fmt.Printf("Server: %s:%d\n", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf("Database: %s://%s:%d/%s\n",
		cfg.Database.Driver,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.Database)
	fmt.Printf("Redis: %s\n", cfg.Redis.Addr)
	fmt.Printf("Environment: %s\n", cfg.App.Environment)
	fmt.Printf("Debug: %t\n", cfg.IsDebug())
}

// ExampleLoadConfigFromEnv demonstrates loading config from environment only
func ExampleLoadConfigFromEnv() {
	// Set some environment variables
	os.Setenv("SERVER_PORT", "9090")
	os.Setenv("DATABASE_HOST", "prod-db.example.com")
	os.Setenv("APP_ENVIRONMENT", "production")
	os.Setenv("APP_DEBUG", "false")

	// Load configuration from environment
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("Failed to load config from env: %v", err)
	}

	fmt.Printf("Server port from env: %d\n", cfg.Server.Port)
	fmt.Printf("Database host from env: %s\n", cfg.Database.Host)
	fmt.Printf("Environment from env: %s\n", cfg.App.Environment)
	fmt.Printf("Debug from env: %t\n", cfg.App.Debug)
}

// ExampleConfigValidation demonstrates config validation
func ExampleConfigValidation() {
	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "localhost",
		},
		Database: DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Database: "alerthistory",
		},
		Redis: RedisConfig{
			Addr: "localhost:6379",
		},
		Log: LogConfig{
			Level: "info",
		},
		App: AppConfig{
			Name: "alert-history",
		},
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Config validation failed: %v", err)
	}

	fmt.Println("Configuration is valid!")
}

// ExampleGetDatabaseURL demonstrates database URL construction
func ExampleGetDatabaseURL() {
	cfg := &Config{
		Database: DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Port:     5432,
			Database: "alerthistory",
			Username: "dev",
			Password: "dev",
			SSLMode:  "disable",
		},
	}

	url := cfg.GetDatabaseURL()
	fmt.Printf("Database URL: %s\n", url)
}

// ExampleEnvironmentHelpers demonstrates environment helper methods
func ExampleEnvironmentHelpers() {
	// Development config
	devCfg := &Config{
		App: AppConfig{
			Environment: "development",
			Debug:       false,
		},
	}

	fmt.Printf("Is Development: %t\n", devCfg.IsDevelopment())
	fmt.Printf("Is Production: %t\n", devCfg.IsProduction())
	fmt.Printf("Is Debug: %t\n", devCfg.IsDebug())

	// Production config
	prodCfg := &Config{
		App: AppConfig{
			Environment: "production",
			Debug:       false,
		},
	}

	fmt.Printf("Is Development: %t\n", prodCfg.IsDevelopment())
	fmt.Printf("Is Production: %t\n", prodCfg.IsProduction())
	fmt.Printf("Is Debug: %t\n", prodCfg.IsDebug())
}

// ExampleConfigWithDefaults demonstrates loading config with defaults
func ExampleConfigWithDefaults() {
	// Load config with defaults (no file)
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Default server port: %d\n", cfg.Server.Port)
	fmt.Printf("Default database host: %s\n", cfg.Database.Host)
	fmt.Printf("Default redis addr: %s\n", cfg.Redis.Addr)
	fmt.Printf("Default app name: %s\n", cfg.App.Name)
}

// ExampleConfigOverride demonstrates how environment variables override file values
func ExampleConfigOverride() {
	// Set environment variables
	os.Setenv("SERVER_PORT", "9090")
	os.Setenv("DATABASE_HOST", "env-override.example.com")
	os.Setenv("REDIS_ADDR", "env-redis.example.com:6380")

	// Load config from file (env vars will override)
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Server port (env override): %d\n", cfg.Server.Port)
	fmt.Printf("Database host (env override): %s\n", cfg.Database.Host)
	fmt.Printf("Redis addr (env override): %s\n", cfg.Redis.Addr)
	fmt.Printf("App name (from file): %s\n", cfg.App.Name)
}
