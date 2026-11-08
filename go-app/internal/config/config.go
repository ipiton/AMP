package config

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the AMP configuration file read by amp-upstream
// (--amp.config.file): the AMP layer on top of the upstream Alertmanager core
// — alert history, AI classification and investigation, K8s-Secret target
// discovery. Routing, receivers, inhibition and templates live in the
// upstream alertmanager.yml (--config.file); route:/receivers: keys in this
// file are not read (ADR-019).
type Config struct {
	// Profile: "lite" (no PostgreSQL) or "standard" (PostgreSQL).
	Profile DeploymentProfile `mapstructure:"profile"`

	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	LLM           LLMConfig           `mapstructure:"llm"`
	App           AppConfig           `mapstructure:"app"`
	Publishing    PublishingConfig    `mapstructure:"publishing"`
	Investigation InvestigationConfig `mapstructure:"investigation" yaml:"investigation,omitempty"`
}

// InvestigationConfig controls the async investigation pipeline (PHASE-5A).
type InvestigationConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	WorkerCount   int           `mapstructure:"worker_count"`
	QueueSize     int           `mapstructure:"queue_size"`
	MaxRetries    int           `mapstructure:"max_retries"`
	RetryInterval time.Duration `mapstructure:"retry_interval"`
	LLMTimeout    time.Duration `mapstructure:"llm_timeout"`
	// OnlyFiring: when true, resolved alerts are not submitted for investigation.
	OnlyFiring bool `mapstructure:"only_firing"`
	// Tools configures built-in investigation tools (PHASE-6A).
	Tools InvestigationToolsConfig `mapstructure:"tools" yaml:"tools,omitempty"`
	// Runbooks configures the runbook engine (PHASE-6B).
	Runbooks InvestigationRunbooksConfig `mapstructure:"runbooks" yaml:"runbooks,omitempty"`
}

// DeploymentProfile represents the deployment profile type
type DeploymentProfile string

const (
	// ProfileLite runs without PostgreSQL: no alert history, no K8s-Secret
	// target discovery.
	ProfileLite DeploymentProfile = "lite"

	// ProfileStandard requires PostgreSQL for alert history.
	ProfileStandard DeploymentProfile = "standard"
)

// ServerConfig holds AMP's HTTP settings. Listen address and external URL
// are upstream flags of amp-upstream.
type ServerConfig struct {
	// WebConfigFile is the web config file for basic auth when
	// --web.config.file is not set (PROD-AUTH); SERVER_WEB_CONFIG_FILE is the
	// last fallback (cmd/amp-upstream resolveWebConfigFile).
	WebConfigFile string `mapstructure:"web_config_file"`
	// Auth holds AMP-specific authentication settings that deliberately
	// live outside the web config file, so that file stays portable to
	// upstream Alertmanager.
	Auth ServerAuthConfig `mapstructure:"auth"`
}

// DefaultUnauthenticatedPaths are the paths served without credentials by
// default when authentication is enabled: upstream's plain-text probe
// endpoints.
func DefaultUnauthenticatedPaths() []string {
	return []string{"/-/healthy", "/-/ready"}
}

// ServerAuthConfig holds AMP-specific HTTP authentication settings.
type ServerAuthConfig struct {
	// UnauthenticatedPaths are request paths (exact match, without the
	// route prefix) served without credentials when authentication is
	// enabled — for kubelet probes. Read at startup.
	UnauthenticatedPaths []string `mapstructure:"unauthenticated_paths"`
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
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

	// AllowInsecureSSLInProduction lifts the production ban on ssl_mode
	// "disable" and nothing else. The Helm chart sets it only for its bundled,
	// in-cluster PostgreSQL, which has no TLS (HELM-DEFAULTS-VALIDATE).
	AllowInsecureSSLInProduction bool `mapstructure:"allow_insecure_ssl_in_production"`
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
	// AgentMode enables the agentic investigation loop with tool calling.
	// When false, the pipeline uses the one-shot InvestigateAlert() call.
	AgentMode bool `mapstructure:"agent_mode"`
	// NotifyWait bounds how long the notify stage waits for a pending
	// classification of a group's alerts (0 = do not wait); DropNoise removes
	// alerts classified as noise from notifications (UPSTREAM-AI-CLASSIFY).
	NotifyWait time.Duration `mapstructure:"notify_wait"`
	DropNoise  bool          `mapstructure:"drop_noise"`
}

// AppConfig holds application-specific configuration
type AppConfig struct {
	// Environment "production" turns on the database credential checks.
	Environment string `mapstructure:"environment"`
}

// PublishingConfig holds K8s-Secret target discovery settings
// (UPSTREAM-TARGETS-DISCOVERY).
type PublishingConfig struct {
	Enabled   bool                      `mapstructure:"enabled"`
	Discovery PublishingDiscoveryConfig `mapstructure:"discovery"`
	Refresh   PublishingRefreshConfig   `mapstructure:"refresh"`
}

// PublishingDiscoveryConfig holds target discovery settings.
type PublishingDiscoveryConfig struct {
	Namespace     string `mapstructure:"namespace"`
	LabelSelector string `mapstructure:"label_selector"`
}

// PublishingRefreshConfig holds dynamic target refresh settings.
type PublishingRefreshConfig struct {
	Interval time.Duration `mapstructure:"interval"`
}

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

		// A missing file is not an error: continue with defaults and env
		// vars. With SetConfigFile viper does not search for the file, so it
		// reports absence as *fs.PathError, never ConfigFileNotFoundError.
		if err := viper.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// LoadConfigFromEnv loads configuration from environment variables only
func LoadConfigFromEnv() (*Config, error) {
	return LoadConfig("")
}

// setDefaults sets default configuration values
func setDefaults() {
	viper.SetDefault("profile", "standard")

	viper.SetDefault("server.auth.unauthenticated_paths", DefaultUnauthenticatedPaths())

	// Database defaults
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.database", "alerthistory")
	viper.SetDefault("database.username", "")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.ssl_mode", "require") // Secure by default
	viper.SetDefault("database.allow_insecure_ssl_in_production", false)
	viper.SetDefault("database.max_connections", 25)
	viper.SetDefault("database.min_connections", 5)
	viper.SetDefault("database.max_conn_lifetime", "1h")
	viper.SetDefault("database.max_conn_idle_time", "30m")
	viper.SetDefault("database.connect_timeout", "10s")

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
	viper.SetDefault("llm.agent_mode", false)
	viper.SetDefault("llm.notify_wait", "5s")
	viper.SetDefault("llm.drop_noise", false)

	// BindEnv, not SetDefault: AutomaticEnv only resolves keys viper already
	// knows. Lets the database_query DSN, which holds a password, come from
	// INVESTIGATION_TOOLS_DATABASE_DSN (a Secret) instead of the YAML.
	_ = viper.BindEnv("investigation.tools.database.dsn")

	// Investigation pipeline defaults (PHASE-5A)
	viper.SetDefault("investigation.enabled", false)
	viper.SetDefault("investigation.worker_count", 3)
	viper.SetDefault("investigation.queue_size", 500)
	viper.SetDefault("investigation.max_retries", 3)
	viper.SetDefault("investigation.retry_interval", "5s")
	viper.SetDefault("investigation.llm_timeout", "60s")
	viper.SetDefault("investigation.only_firing", true)
	viper.SetDefault("investigation.runbooks.enabled", false)
	viper.SetDefault("investigation.runbooks.path", DefaultRunbooksPath)
	viper.SetDefault("investigation.runbooks.max_runbooks", DefaultRunbooksMaxRunbooks)
	viper.SetDefault("investigation.runbooks.max_chars", DefaultRunbooksMaxChars)

	viper.SetDefault("app.environment", "development")

	// Publishing (target discovery) defaults
	viper.SetDefault("publishing.enabled", true)
	viper.SetDefault("publishing.discovery.namespace", "")
	viper.SetDefault("publishing.discovery.label_selector", "publishing-target=true")
	viper.SetDefault("publishing.refresh.interval", "5m")
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Profile != ProfileLite && c.Profile != ProfileStandard {
		return fmt.Errorf("profile validation failed: invalid deployment profile: %s (must be 'lite' or 'standard')", c.Profile)
	}

	if c.Profile == ProfileStandard {
		if c.Database.Host == "" {
			return fmt.Errorf("database host cannot be empty (required for standard profile)")
		}

		if c.Database.Database == "" {
			return fmt.Errorf("database name cannot be empty (required for standard profile)")
		}

		// TN-205: Validate database credentials for production
		if err := c.validateDatabaseCredentials(); err != nil {
			return fmt.Errorf("database credentials validation failed: %w", err)
		}
	}

	if c.LLM.NotifyWait < 0 {
		return fmt.Errorf("llm.notify_wait cannot be negative")
	}

	return nil
}

// validateDatabaseCredentials validates database credentials for production (TN-205)
func (c *Config) validateDatabaseCredentials() error {
	if c.Database.Username == "" || c.Database.Password == "" {
		if c.App.Environment == "production" {
			return fmt.Errorf("database credentials (username/password) are required in production environment")
		}

		// Both empty: likely intentional for local development.
		if c.Database.Username == "" && c.Database.Password == "" {
			return nil
		}

		return fmt.Errorf("database username and password must both be set or both be empty")
	}

	if c.App.Environment == "production" {
		weakPasswords := []string{"password", "admin", "root", "dev", "test", "123456", "postgres"}
		for _, weak := range weakPasswords {
			if c.Database.Password == weak {
				// The value is not echoed: the error is logged, and the password
				// is the operator's real one (PUBLISH-SECRETS-IN-LOGS, L-2).
				return fmt.Errorf("weak database password detected: a common default password is not allowed in production")
			}
		}

		if len(c.Database.Password) < 12 {
			return fmt.Errorf("database password must be at least 12 characters in production (got %d)", len(c.Database.Password))
		}
	}

	if c.App.Environment == "production" && c.Database.SSLMode == "disable" && !c.Database.AllowInsecureSSLInProduction {
		return fmt.Errorf("database SSL mode 'disable' is not allowed in production (use 'require' or 'verify-full')")
	}

	return nil
}

// knownKeys are the sections LoadConfig reads, with the server keys: what
// else a file sets is ignored. Keys of the removed cmd/server (route,
// receivers, grouping, redis, log, server.port, ...) are in old files, and
// before UPSTREAM-CORE-REMOVAL they worked.
var knownKeys = map[string]map[string]bool{
	"profile": nil, "database": nil, "llm": nil, "app": nil, "publishing": nil, "investigation": nil,
	"server": {"auth": true, "web_config_file": true},
}

// IgnoredKeys lists the top-level keys and server.* keys of the config file
// at path that LoadConfig does not read, sorted. A file that cannot be read
// yields none: LoadConfig reports that.
func IgnoredKeys(path string) []string {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	var ignored []string
	for key, value := range v.AllSettings() {
		sub, known := knownKeys[key]
		switch {
		case !known:
			ignored = append(ignored, key)
		case sub != nil:
			if m, ok := value.(map[string]any); ok {
				for k := range m {
					if !sub[k] {
						ignored = append(ignored, key+"."+k)
					}
				}
			}
		}
	}
	sort.Strings(ignored)
	return ignored
}
