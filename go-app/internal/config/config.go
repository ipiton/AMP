package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	// Deployment profile (TN-200)
	// Values: "lite" (embedded storage, single-node) or "standard" (Postgres+Redis, HA)
	Profile DeploymentProfile `mapstructure:"profile"`

	// Storage backend configuration (TN-201)
	Storage StorageConfig `mapstructure:"storage"`

	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Log      LogConfig      `mapstructure:"log"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Lock     LockConfig     `mapstructure:"lock"`
	App      AppConfig      `mapstructure:"app"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	Webhook  WebhookConfig  `mapstructure:"webhook"`
}

// DeploymentProfile represents the deployment profile type
type DeploymentProfile string

const (
	// ProfileLite is single-node deployment with embedded storage (SQLite/BadgerDB)
	// No external dependencies (no Postgres, no Redis required)
	// Persistent storage via PVC (Kubernetes) or local filesystem
	// Use case: Development, testing, small-scale production (<1K alerts/day)
	ProfileLite DeploymentProfile = "lite"

	// ProfileStandard is HA-ready deployment with external storage (Postgres+Redis)
	// Requires: PostgreSQL (required), Redis (optional)
	// Supports: 2-10 replicas, horizontal scaling, extended history
	// Use case: Production environments, high-volume (>1K alerts/day), HA requirements
	ProfileStandard DeploymentProfile = "standard"
)

// StorageConfig holds storage backend configuration
type StorageConfig struct {
	// Backend determines storage implementation
	// Values: "filesystem" (Lite), "postgres" (Standard)
	Backend StorageBackend `mapstructure:"backend"`

	// FilesystemPath is the path for embedded storage (Lite profile)
	// Default: /data/alerthistory.db (SQLite)
	FilesystemPath string `mapstructure:"filesystem_path"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port                    int           `mapstructure:"port"`
	Host                    string        `mapstructure:"host"`
	ReadTimeout             time.Duration `mapstructure:"read_timeout"`
	WriteTimeout            time.Duration `mapstructure:"write_timeout"`
	IdleTimeout             time.Duration `mapstructure:"idle_timeout"`
	GracefulShutdownTimeout time.Duration `mapstructure:"graceful_shutdown_timeout"`
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	Database        string        `mapstructure:"database"`
	Username        string        `mapstructure:"username"`
	Password        string        `mapstructure:"password"`
	SSLMode         string        `mapstructure:"ssl_mode"`
	MaxConnections  int           `mapstructure:"max_connections"`
	MinConnections  int           `mapstructure:"min_connections"`
	MaxConnLifetime time.Duration `mapstructure:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `mapstructure:"max_conn_idle_time"`
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout"`
	QueryTimeout    time.Duration `mapstructure:"query_timeout"`
	URL             string        `mapstructure:"url"`
}

// RedisConfig holds Redis-related configuration
type RedisConfig struct {
	Addr            string        `mapstructure:"addr"`
	Password        string        `mapstructure:"password"`
	DB              int           `mapstructure:"db"`
	PoolSize        int           `mapstructure:"pool_size"`
	MinIdleConns    int           `mapstructure:"min_idle_conns"`
	DialTimeout     time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	MaxRetries      int           `mapstructure:"max_retries"`
	MinRetryBackoff time.Duration `mapstructure:"min_retry_backoff"`
	MaxRetryBackoff time.Duration `mapstructure:"max_retry_backoff"`
}

// LLMConfig holds LLM-related configuration
type LLMConfig struct {
	Enabled     bool          `mapstructure:"enabled"`
	Provider    string        `mapstructure:"provider"`
	APIKey      string        `mapstructure:"api_key"`
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	MaxTokens   int           `mapstructure:"max_tokens"`
	Temperature float64       `mapstructure:"temperature"`
	Timeout     time.Duration `mapstructure:"timeout"`
	MaxRetries  int           `mapstructure:"max_retries"`
}

// LogConfig holds logging-related configuration
type LogConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

// CacheConfig holds cache-related configuration
type CacheConfig struct {
	DefaultTTL      time.Duration `mapstructure:"default_ttl"`
	MaxTTL          time.Duration `mapstructure:"max_ttl"`
	CleanupInterval time.Duration `mapstructure:"cleanup_interval"`
	MaxKeys         int64         `mapstructure:"max_keys"`
	EnableMetrics   bool          `mapstructure:"enable_metrics"`
}

// LockConfig holds distributed lock configuration
type LockConfig struct {
	TTL            time.Duration `mapstructure:"ttl"`
	MaxRetries     int           `mapstructure:"max_retries"`
	RetryInterval  time.Duration `mapstructure:"retry_interval"`
	AcquireTimeout time.Duration `mapstructure:"acquire_timeout"`
	ReleaseTimeout time.Duration `mapstructure:"release_timeout"`
	ValuePrefix    string        `mapstructure:"value_prefix"`
}

// AppConfig holds application-specific configuration
type AppConfig struct {
	Name          string        `mapstructure:"name"`
	Version       string        `mapstructure:"version"`
	Environment   string        `mapstructure:"environment"`
	Debug         bool          `mapstructure:"debug"`
	Timezone      string        `mapstructure:"timezone"`
	MaxWorkers    int           `mapstructure:"max_workers"`
	WorkerTimeout time.Duration `mapstructure:"worker_timeout"`
}

// MetricsConfig holds metrics-related configuration
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
	Port    int    `mapstructure:"port"`
}

// WebhookConfig holds webhook endpoint configuration
type WebhookConfig struct {
	MaxRequestSize  int64         `mapstructure:"max_request_size"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`
	MaxAlertsPerReq int           `mapstructure:"max_alerts_per_request"`
	RateLimiting    RateLimitingConfig `mapstructure:"rate_limiting"`
	Authentication  AuthenticationConfig `mapstructure:"authentication"`
	Signature       SignatureConfig `mapstructure:"signature"`
	CORS            CORSWebhookConfig `mapstructure:"cors"`
}

// RateLimitingConfig holds rate limiting configuration
type RateLimitingConfig struct {
	Enabled     bool `mapstructure:"enabled"`
	PerIPLimit  int  `mapstructure:"per_ip_limit"`
	GlobalLimit int  `mapstructure:"global_limit"`
}

// AuthenticationConfig holds authentication configuration
type AuthenticationConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	Type      string `mapstructure:"type"`
	APIKey    string `mapstructure:"api_key"`
	JWTSecret string `mapstructure:"jwt_secret"`
}

// SignatureConfig holds signature verification configuration
type SignatureConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Secret  string `mapstructure:"secret"`
}

// CORSWebhookConfig holds CORS configuration for webhook endpoint
type CORSWebhookConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	AllowedOrigins string `mapstructure:"allowed_origins"`
	AllowedMethods string `mapstructure:"allowed_methods"`
	AllowedHeaders string `mapstructure:"allowed_headers"`
}

// StorageBackend represents the storage implementation
type StorageBackend string

const (
	// StorageBackendFilesystem uses embedded storage (SQLite/BadgerDB)
	// Used by Lite profile
	StorageBackendFilesystem StorageBackend = "filesystem"

	// StorageBackendPostgres uses PostgreSQL external storage
	// Used by Standard profile
	StorageBackendPostgres StorageBackend = "postgres"
)

// LoadConfig loads configuration from file and environment variables
func LoadConfig(configPath string) (*Config, error) {
	// Set default values first
	setDefaults()

	// Enable automatic environment variable binding
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Try to read configuration file if it exists
	if configPath != "" {
		viper.SetConfigFile(configPath)
		viper.SetConfigType("yaml")

		if err := viper.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
			// Config file not found, continue with defaults and env vars
		}
	}

	// Unmarshal configuration
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// LoadConfigFromEnv loads configuration from environment variables only
func LoadConfigFromEnv() (*Config, error) {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Set default values
	setDefaults()

	// Unmarshal configuration
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// Deployment profile defaults (TN-200)
	viper.SetDefault("profile", "standard") // Default to standard profile
	viper.SetDefault("storage.backend", "postgres") // Default to Postgres
	viper.SetDefault("storage.filesystem_path", "/data/alerthistory.db") // SQLite path for Lite

	// Server defaults
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "120s")
	viper.SetDefault("server.graceful_shutdown_timeout", "30s")

	// Database defaults
	viper.SetDefault("database.driver", "postgres")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.database", "alerthistory")
	viper.SetDefault("database.username", "dev")
	viper.SetDefault("database.password", "dev")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_connections", 25)
	viper.SetDefault("database.min_connections", 5)
	viper.SetDefault("database.max_conn_lifetime", "1h")
	viper.SetDefault("database.max_conn_idle_time", "30m")
	viper.SetDefault("database.connect_timeout", "10s")
	viper.SetDefault("database.query_timeout", "30s")

	// Redis defaults
	viper.SetDefault("redis.addr", "localhost:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.pool_size", 10)
	viper.SetDefault("redis.min_idle_conns", 5)
	viper.SetDefault("redis.dial_timeout", "5s")
	viper.SetDefault("redis.read_timeout", "3s")
	viper.SetDefault("redis.write_timeout", "3s")
	viper.SetDefault("redis.max_retries", 3)
	viper.SetDefault("redis.min_retry_backoff", "100ms")
	viper.SetDefault("redis.max_retry_backoff", "500ms")

	// LLM defaults
	viper.SetDefault("llm.enabled", false)
	viper.SetDefault("llm.provider", "openai")
	viper.SetDefault("llm.api_key", "")
	viper.SetDefault("llm.base_url", "https://api.openai.com/v1")
	viper.SetDefault("llm.model", "gpt-3.5-turbo")
	viper.SetDefault("llm.max_tokens", 1000)
	viper.SetDefault("llm.temperature", 0.7)
	viper.SetDefault("llm.timeout", "30s")
	viper.SetDefault("llm.max_retries", 3)

	// Log defaults
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.format", "json")
	viper.SetDefault("log.output", "stdout")
	viper.SetDefault("log.filename", "")
	viper.SetDefault("log.max_size", 100)
	viper.SetDefault("log.max_backups", 3)
	viper.SetDefault("log.max_age", 28)
	viper.SetDefault("log.compress", true)

	// Cache defaults
	viper.SetDefault("cache.default_ttl", "1h")
	viper.SetDefault("cache.max_ttl", "24h")
	viper.SetDefault("cache.cleanup_interval", "10m")
	viper.SetDefault("cache.max_keys", 10000)
	viper.SetDefault("cache.enable_metrics", true)

	// Lock defaults
	viper.SetDefault("lock.ttl", "30s")
	viper.SetDefault("lock.max_retries", 3)
	viper.SetDefault("lock.retry_interval", "100ms")
	viper.SetDefault("lock.acquire_timeout", "5s")
	viper.SetDefault("lock.release_timeout", "2s")
	viper.SetDefault("lock.value_prefix", "lock")

	// App defaults
	viper.SetDefault("app.name", "alert-history")
	viper.SetDefault("app.version", "1.0.0")
	viper.SetDefault("app.environment", "development")
	viper.SetDefault("app.debug", false)
	viper.SetDefault("app.timezone", "UTC")
	viper.SetDefault("app.max_workers", 10)
	viper.SetDefault("app.worker_timeout", "5m")

	// Metrics defaults
	viper.SetDefault("metrics.enabled", true)
	viper.SetDefault("metrics.path", "/metrics")
	viper.SetDefault("metrics.port", 8080)

	// Webhook defaults
	viper.SetDefault("webhook.max_request_size", 10485760) // 10MB
	viper.SetDefault("webhook.request_timeout", "30s")
	viper.SetDefault("webhook.max_alerts_per_request", 1000)

	// Webhook rate limiting defaults
	viper.SetDefault("webhook.rate_limiting.enabled", true)
	viper.SetDefault("webhook.rate_limiting.per_ip_limit", 100)   // requests per minute
	viper.SetDefault("webhook.rate_limiting.global_limit", 10000) // requests per minute

	// Webhook authentication defaults
	viper.SetDefault("webhook.authentication.enabled", false)
	viper.SetDefault("webhook.authentication.type", "api_key")
	viper.SetDefault("webhook.authentication.api_key", "")
	viper.SetDefault("webhook.authentication.jwt_secret", "")

	// Webhook signature verification defaults
	viper.SetDefault("webhook.signature.enabled", false)
	viper.SetDefault("webhook.signature.secret", "")

	// Webhook CORS defaults
	viper.SetDefault("webhook.cors.enabled", false)
	viper.SetDefault("webhook.cors.allowed_origins", "*")
	viper.SetDefault("webhook.cors.allowed_methods", "POST, OPTIONS")
	viper.SetDefault("webhook.cors.allowed_headers", "Content-Type, X-Request-ID, X-API-Key, Authorization")
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate deployment profile (TN-200/TN-204)
	if err := c.validateProfile(); err != nil {
		return fmt.Errorf("profile validation failed: %w", err)
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Server.Host == "" {
		return fmt.Errorf("server host cannot be empty")
	}

	// Skip database validation for Lite profile (TN-204)
	if c.Profile == ProfileStandard {
		if c.Database.Driver == "" {
			return fmt.Errorf("database driver cannot be empty (required for standard profile)")
		}

		if c.Database.Host == "" {
			return fmt.Errorf("database host cannot be empty (required for standard profile)")
		}

		if c.Database.Database == "" {
			return fmt.Errorf("database name cannot be empty (required for standard profile)")
		}
	}

	// Redis is optional for both profiles (TN-202)
	// Validation only if Redis addr is provided
	if c.Redis.Addr != "" {
		// Redis config provided, validate it
		if c.Profile == ProfileLite {
			// Warning: Redis not recommended for Lite profile
			// but allow it for testing/development
		}
	}

	if c.Log.Level == "" {
		return fmt.Errorf("log level cannot be empty")
	}

	if c.App.Name == "" {
		return fmt.Errorf("app name cannot be empty")
	}

	return nil
}

// validateProfile validates deployment profile configuration (TN-200/TN-204)
func (c *Config) validateProfile() error {
	// Validate profile value
	if c.Profile != ProfileLite && c.Profile != ProfileStandard {
		return fmt.Errorf("invalid deployment profile: %s (must be 'lite' or 'standard')", c.Profile)
	}

	// Validate storage backend
	if c.Storage.Backend != StorageBackendFilesystem && c.Storage.Backend != StorageBackendPostgres {
		return fmt.Errorf("invalid storage backend: %s (must be 'filesystem' or 'postgres')", c.Storage.Backend)
	}

	// Profile-specific validation
	switch c.Profile {
	case ProfileLite:
		// Lite profile: require filesystem storage
		if c.Storage.Backend != StorageBackendFilesystem {
			return fmt.Errorf("lite profile requires storage.backend='filesystem' (got '%s')", c.Storage.Backend)
		}

		// Validate filesystem path
		if c.Storage.FilesystemPath == "" {
			return fmt.Errorf("lite profile requires storage.filesystem_path (e.g., /data/alerthistory.db)")
		}

		// Warning: Postgres not used in Lite (but don't fail)
		if c.Database.Host != "" && c.Database.Host != "localhost" {
			// Log warning but don't fail (allows testing)
		}

	case ProfileStandard:
		// Standard profile: require postgres storage
		if c.Storage.Backend != StorageBackendPostgres {
			return fmt.Errorf("standard profile requires storage.backend='postgres' (got '%s')", c.Storage.Backend)
		}

		// Postgres configuration is required (validated in main Validate())
	}

	return nil
}

// GetDatabaseURL constructs database URL from configuration
func (c *Config) GetDatabaseURL() string {
	if c.Database.URL != "" {
		return c.Database.URL
	}

	sslMode := c.Database.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	return fmt.Sprintf("%s://%s:%s@%s:%d/%s?sslmode=%s",
		c.Database.Driver,
		c.Database.Username,
		c.Database.Password,
		c.Database.Host,
		c.Database.Port,
		c.Database.Database,
		sslMode,
	)
}

// IsDevelopment returns true if the application is running in development mode
func (c *Config) IsDevelopment() bool {
	return c.App.Environment == "development"
}

// IsProduction returns true if the application is running in production mode
func (c *Config) IsProduction() bool {
	return c.App.Environment == "production"
}

// IsDebug returns true if debug mode is enabled
func (c *Config) IsDebug() bool {
	return c.App.Debug || c.IsDevelopment()
}

// IsLiteProfile returns true if running in Lite deployment profile (TN-200)
func (c *Config) IsLiteProfile() bool {
	return c.Profile == ProfileLite
}

// IsStandardProfile returns true if running in Standard deployment profile (TN-200)
func (c *Config) IsStandardProfile() bool {
	return c.Profile == ProfileStandard
}

// RequiresPostgres returns true if Postgres is required for this profile (TN-201)
func (c *Config) RequiresPostgres() bool {
	return c.Profile == ProfileStandard
}

// RequiresRedis returns true if Redis is required for this profile (TN-202)
// Note: Redis is optional for both profiles
func (c *Config) RequiresRedis() bool {
	// Redis is optional for both profiles
	// Only required if explicitly configured
	return false
}

// UsesEmbeddedStorage returns true if using embedded storage (SQLite/BadgerDB) (TN-201)
func (c *Config) UsesEmbeddedStorage() bool {
	return c.Storage.Backend == StorageBackendFilesystem
}

// UsesPostgresStorage returns true if using PostgreSQL storage (TN-201)
func (c *Config) UsesPostgresStorage() bool {
	return c.Storage.Backend == StorageBackendPostgres
}

// GetProfileName returns human-readable profile name (TN-200)
func (c *Config) GetProfileName() string {
	switch c.Profile {
	case ProfileLite:
		return "Lite (Embedded Storage)"
	case ProfileStandard:
		return "Standard (HA-Ready)"
	default:
		return string(c.Profile)
	}
}

// GetProfileDescription returns detailed profile description (TN-200)
func (c *Config) GetProfileDescription() string {
	switch c.Profile {
	case ProfileLite:
		return "Single-node deployment with embedded storage (SQLite). No external dependencies. Persistent via PVC."
	case ProfileStandard:
		return "HA-ready deployment with PostgreSQL and optional Redis. Supports 2-10 replicas and horizontal scaling."
	default:
		return "Unknown profile"
	}
}
