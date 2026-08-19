package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	// Deployment profile (TN-200)
	// Values: "lite" (embedded storage, single-node) or "standard" (Postgres+Redis, HA)
	Profile DeploymentProfile `mapstructure:"profile"`

	// Storage backend configuration (TN-201)
	Storage StorageConfig `mapstructure:"storage"`

	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Redis         RedisConfig         `mapstructure:"redis"`
	LLM           LLMConfig           `mapstructure:"llm"`
	Log           LogConfig           `mapstructure:"log"`
	Cache         CacheConfig         `mapstructure:"cache"`
	Lock          LockConfig          `mapstructure:"lock"`
	App           AppConfig           `mapstructure:"app"`
	Metrics       MetricsConfig       `mapstructure:"metrics"`
	Webhook       WebhookConfig       `mapstructure:"webhook"`
	HTTPClient    HTTPClientConfig    `mapstructure:"http_client"`
	Retry         RetryConfig         `mapstructure:"retry"`
	Telemetry     TelemetryConfig     `mapstructure:"telemetry"`
	Publishing    PublishingConfig    `mapstructure:"publishing"`
	Inhibition    InhibitionConfig    `mapstructure:"inhibition" yaml:"inhibition,omitempty"`
	Investigation InvestigationConfig `mapstructure:"investigation" yaml:"investigation,omitempty"`
	Grouping      GroupingConfig      `mapstructure:"grouping" yaml:"grouping,omitempty"`
	Silencing     SilencingConfig     `mapstructure:"silencing" yaml:"silencing,omitempty"`
	Receivers     []ReceiverConfig    `mapstructure:"receivers"`

	// Routing holds the full Alertmanager-compatible route tree and receiver
	// definitions (top-level `route:` + `receivers:` + `global:` YAML
	// sections), parsed via the existing infrastructure/routing.Parse()
	// (task 1.3: alertmanager-parity).
	//
	// It is populated only when the loaded config file has a `route:`
	// section. Absent `route:` keeps the legacy single-receiver behavior:
	// Routing stays nil and the Receivers field above (name-only) remains
	// authoritative — no error.
	//
	// Excluded from mapstructure/json/validator processing on purpose:
	//   - mapstructure:"-": populated manually by loadRouteConfig, not by
	//     viper's generic unmarshal (the nested types only carry yaml tags).
	//   - json:"-": RouteConfig carries internal fields (e.g. compiled
	//     regex keyed by *grouping.Route) that encoding/json cannot marshal;
	//     it also isn't meant to appear in the config-diff/update API.
	//   - validate:"-": already fully validated inside routing.Parse();
	//     re-validating via go-playground/validator here would recurse into
	//     custom tag names (alphanum_hyphen, https_production, ...) that are
	//     only registered on routing's own validator instance and would
	//     panic on cv.v.Struct(cfg).
	//
	// Wiring this into the routing/notification engine happens in task 1.4
	// (service_registry).
	Routing *infraroute.RouteConfig `mapstructure:"-" json:"-" validate:"-"`
}

// HasRouteTree reports whether the config loaded a full `route:` +
// `receivers:` tree (task 1.3). When false, callers should fall back to the
// legacy single-receiver Receivers field.
func (c *Config) HasRouteTree() bool {
	return c.Routing != nil
}

// InvestigationConfig controls the PHASE-5A async investigation pipeline.
// When disabled, no queue is started and AlertProcessor skips the Submit call.
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
}

// GroupingConfig controls the alert grouping subsystem (task 2.2,
// alertmanager-parity): GroupManager (group lifecycle) + TimerManager
// (group_wait/group_interval/repeat_interval timers).
//
// Grouping defaults — group_by, group_wait, group_interval, repeat_interval —
// are intentionally NOT duplicated here. They come from the `route:` tree
// (task 1.3/1.4, Config.Routing) via BuildGroupingConfig (grouping_adapter.go):
// infraroute.RouteConfig.Route already IS a *grouping.Route (TN-121 backward
// compatibility), so the adapter reuses it directly instead of re-parsing a
// second copy of the same fields.
type GroupingConfig struct {
	// Enabled turns on the grouping subsystem (group manager + timers).
	// Defaults to false: this task (2.2) only wires storage + timer
	// lifecycle (start/restore/shutdown); the alert ingest pipeline does not
	// consult the grouping subsystem yet — that lands in task 2.3, which
	// flips this flag's effect on the request path.
	Enabled bool `mapstructure:"enabled" yaml:"enabled,omitempty"`

	// ReconciliationInterval controls the standard profile's periodic
	// orphan-adoption loop (task 6.2, distributed timer liveness — see
	// grouping.TimerManagerConfig.ReconciliationInterval for the mechanism).
	// ServiceRegistry.initializeGrouping only forwards this value to the
	// TimerManager when running the standard profile with a live
	// Redis-backed TimerStorage; every other case (lite profile, or a
	// standard-profile Redis failure that fell back to in-memory storage)
	// leaves the TimerManager's loop disabled regardless of this setting —
	// InMemoryTimerStorage is never shared across replicas, so scanning it
	// for orphans left by ANOTHER replica is meaningless.
	//
	// Defaults to 45s (see setDefaults) in the standard profile with Redis;
	// 0 disables the loop.
	ReconciliationInterval time.Duration `mapstructure:"reconciliation_interval" yaml:"reconciliation_interval,omitempty"`

	// ReconciliationGrace is how far past a timer's ExpiresAt the
	// reconciliation loop waits before treating it as orphaned rather than
	// possibly still being processed by its owning replica. Only consulted
	// when ReconciliationInterval is positive; left at 0 here means "use
	// grouping.TimerManagerConfig's own default" (60s).
	ReconciliationGrace time.Duration `mapstructure:"reconciliation_grace" yaml:"reconciliation_grace,omitempty"`
}

// SilencingConfig holds the cross-replica silence cache sync knobs (task
// 6.3's RedisSilenceEventBus subscribe loop + periodic fallback resync in
// ServiceRegistry, alertmanager-parity wave-5 item FU-SILENCE-SYNC-INTERVALS).
// Both fields were hardcoded literals (runSilenceSubscribeLoop's retryDelay,
// runSilencePeriodicResync's fallbackInterval) before this task; the
// defaults below are unchanged from those literals.
type SilencingConfig struct {
	// SubscribeRetryBackoff is the fixed delay between resubscribe attempts
	// after RedisSilenceEventBus.Subscribe returns a non-nil error (a
	// dropped/failed Redis connection). Each successful (re)subscribe
	// triggers a full resync via onResync, so a shorter backoff only trades
	// faster reconnect attempts for more Redis connection churn while the
	// outage lasts — it does not affect steady-state load.
	//
	// Defaults to 2s (see setDefaults).
	SubscribeRetryBackoff time.Duration `mapstructure:"subscribe_retry_backoff" yaml:"subscribe_retry_backoff,omitempty"`

	// PeriodicResyncInterval is how often runSilencePeriodicResync forces a
	// full resync of memory.SilenceStore regardless of pub/sub health — the
	// backstop for a Publish call that fails on the writing replica without
	// surfacing as an HTTP error (publishSilenceEvent is deliberately
	// best-effort). A shorter interval bounds the staleness window tighter
	// at the cost of more frequent full-table resync reads.
	//
	// Defaults to 5m (see setDefaults) — the same order of magnitude as the
	// silence GC worker's default (DefaultSilenceManagerConfig.GCInterval).
	PeriodicResyncInterval time.Duration `mapstructure:"periodic_resync_interval" yaml:"periodic_resync_interval,omitempty"`
}

// InhibitionConfig holds inhibition rules configuration (Alertmanager parity, PARITY-A2)
type InhibitionConfig struct {
	// Rules is the list of inhibition rules (Alertmanager compatible format)
	Rules []InhibitionRuleConfig `mapstructure:"inhibit_rules" yaml:"inhibit_rules"`

	// ConfigFile is an optional path to a separate inhibition rules YAML file.
	// If specified, rules from the file are merged with inline Rules.
	ConfigFile string `mapstructure:"config_file" yaml:"config_file,omitempty"`
}

// InhibitionRuleConfig holds a single inhibition rule in config format
type InhibitionRuleConfig struct {
	SourceMatch   map[string]string `mapstructure:"source_match"    yaml:"source_match,omitempty"`
	SourceMatchRE map[string]string `mapstructure:"source_match_re" yaml:"source_match_re,omitempty"`
	TargetMatch   map[string]string `mapstructure:"target_match"    yaml:"target_match,omitempty"`
	TargetMatchRE map[string]string `mapstructure:"target_match_re" yaml:"target_match_re,omitempty"`
	Equal         []string          `mapstructure:"equal"           yaml:"equal,omitempty"`
	Name          string            `mapstructure:"name"            yaml:"name,omitempty"`

	// SourceMatchers/TargetMatchers are upstream Alertmanager's modern
	// `matchers:` list syntax for inhibit rules (e.g. ['severity="critical"'],
	// recommended since v0.22), converted into runtime rules by
	// ToInhibitionRules alongside the legacy map form — both may be present
	// on the same rule (upstream ANDs them together).
	//
	// Wave 7 (FU-INHIBIT-MATCHERS): previously captured here only so
	// ToInhibitionRules could log a loud per-rule Error naming them as
	// unimplemented (final review finding 10) — the inhibition engine had
	// no such fields and a rule using only this syntax inhibited nothing.
	// internal/infrastructure/inhibition.InhibitionRule now implements
	// them for real (CompileMatchers + matchRuleFast).
	SourceMatchers []string `mapstructure:"source_matchers" yaml:"source_matchers,omitempty"`
	TargetMatchers []string `mapstructure:"target_matchers" yaml:"target_matchers,omitempty"`
}

// ReceiverConfig holds configuration for a notification receiver.
// The json tag matters: /api/v2/receivers marshals this directly and the
// Alertmanager API v2 schema requires lowercase "name".
type ReceiverConfig struct {
	Name string `mapstructure:"name" json:"name"`
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

	// SnapshotPath is the directory where the lite profile persists file
	// snapshots of the in-memory silence store and notification log (wave 6,
	// FU-LITE-FILE-SNAPSHOT), mirroring upstream Alertmanager's
	// --storage.path. Empty (the default) disables snapshotting entirely —
	// this must be an explicit operator opt-in, not a default-on side effect
	// of upgrading, so upstream's own non-empty default ("data/") is
	// deliberately NOT copied here. Only consulted for the lite profile;
	// ServiceRegistry.initializeSnapshotting logs Info and skips wiring when
	// this is set under the standard profile, where Postgres/Redis already
	// own durability.
	SnapshotPath string `mapstructure:"path"`

	// SnapshotInterval is how often the lite profile's periodic snapshot
	// writer flushes silences + nflog state to SnapshotPath, on top of the
	// always-on final write on graceful shutdown. Only consulted when
	// SnapshotPath is non-empty.
	SnapshotInterval time.Duration `mapstructure:"snapshot_interval"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port                    int           `mapstructure:"port"`
	Host                    string        `mapstructure:"host"`
	ReadTimeout             time.Duration `mapstructure:"read_timeout"`
	WriteTimeout            time.Duration `mapstructure:"write_timeout"`
	IdleTimeout             time.Duration `mapstructure:"idle_timeout"`
	GracefulShutdownTimeout time.Duration `mapstructure:"graceful_shutdown_timeout"`
	// ExternalURL is the public URL of this AMP instance (env: AMP_SERVER_EXTERNAL_URL).
	// Used in notification callbacks: email footer, silence links, webhook externalURL field.
	// Empty string disables callback links (graceful degradation).
	ExternalURL string `mapstructure:"external_url"`
	// RoutePrefix mounts all HTTP routes under this path prefix, mirroring
	// upstream Alertmanager's --web.route-prefix (PARITY-B6). Empty string
	// (the default) or "/" means no prefix. Overridable via the
	// -web.route-prefix CLI flag (see cmd/server/main.go); the flag takes
	// precedence over this config value when set.
	RoutePrefix string                `mapstructure:"route_prefix"`
	WebSocket   WebSocketServerConfig `mapstructure:"websocket"`
	// CORS controls cross-origin headers for the whole HTTP API
	// (reuses the same config shape as webhook.cors).
	CORS CORSWebhookConfig `mapstructure:"cors"`
}

// WebSocketServerConfig holds WebSocket endpoint configuration
type WebSocketServerConfig struct {
	// AllowedOrigins is a comma-separated list of origins allowed to open
	// WebSocket connections (e.g. "https://amp.example.com, https://grafana.example.com").
	// Empty string restricts to same-origin requests; "*" allows any origin.
	AllowedOrigins string `mapstructure:"allowed_origins"`
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
	// AgentMode enables the Phase 5B agentic investigation loop with tool calling.
	// When false, the pipeline uses the Phase 5A one-shot InvestigateAlert() call.
	AgentMode bool `mapstructure:"agent_mode"`
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
	MaxRequestSize  int64                `mapstructure:"max_request_size"`
	RequestTimeout  time.Duration        `mapstructure:"request_timeout"`
	MaxAlertsPerReq int                  `mapstructure:"max_alerts_per_request"`
	RateLimiting    RateLimitingConfig   `mapstructure:"rate_limiting"`
	Authentication  AuthenticationConfig `mapstructure:"authentication"`
	Signature       SignatureConfig      `mapstructure:"signature"`
	CORS            CORSWebhookConfig    `mapstructure:"cors"`
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

// RetryConfig holds global retry configuration
type RetryConfig struct {
	// MaxAttempts is the maximum number of retry attempts (default: 4, which is 3 retries + 1 initial attempt)
	MaxAttempts int `mapstructure:"max_attempts"`

	// BaseDelay is the base delay for exponential backoff (default: 100ms)
	BaseDelay time.Duration `mapstructure:"base_delay"`

	// MaxDelay is the maximum delay between retries (default: 30s)
	MaxDelay time.Duration `mapstructure:"max_delay"`

	// Multiplier is the backoff multiplier (default: 2.0 for exponential backoff)
	Multiplier float64 `mapstructure:"multiplier"`

	// JitterRatio is the jitter ratio (0.0-1.0, default: 0.15 = 15% jitter)
	JitterRatio float64 `mapstructure:"jitter_ratio"`
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 4,                      // 3 retries + 1 initial attempt
		BaseDelay:   100 * time.Millisecond, // 100ms
		MaxDelay:    30 * time.Second,       // 30s
		Multiplier:  2.0,                    // Exponential backoff
		JitterRatio: 0.15,                   // 15% jitter
	}
}

// TelemetryConfig holds OpenTelemetry configuration
type TelemetryConfig struct {
	// Enabled controls whether tracing is enabled
	Enabled bool `mapstructure:"enabled"`

	// Endpoint is the OTLP collector endpoint (e.g., "localhost:4317")
	Endpoint string `mapstructure:"endpoint"`

	// SamplingRatio is the sampling ratio (0.0 to 1.0)
	SamplingRatio float64 `mapstructure:"sampling_ratio"`

	// Insecure disables transport security for the OTLP exporter (plaintext gRPC).
	// Default false = TLS.
	Insecure bool `mapstructure:"insecure"`

	// CACertFile is an optional PEM CA certificate path for verifying the OTLP
	// collector. Empty uses system roots. Ignored when Insecure is true.
	CACertFile string `mapstructure:"ca_cert_file"`
}

// PublishingConfig holds runtime publishing configuration.
type PublishingConfig struct {
	Enabled   bool                      `mapstructure:"enabled"`
	Discovery PublishingDiscoveryConfig `mapstructure:"discovery"`
	Queue     PublishingQueueConfig     `mapstructure:"queue"`
	Refresh   PublishingRefreshConfig   `mapstructure:"refresh"`
	Health    PublishingHealthConfig    `mapstructure:"health"`
}

// PublishingDiscoveryConfig holds target discovery settings.
type PublishingDiscoveryConfig struct {
	Namespace     string `mapstructure:"namespace"`
	LabelSelector string `mapstructure:"label_selector"`
}

// PublishingQueueConfig holds publishing queue settings.
type PublishingQueueConfig struct {
	MaxConcurrent           int           `mapstructure:"max_concurrent"`
	WorkerCount             int           `mapstructure:"worker_count"`
	HighPriorityQueueSize   int           `mapstructure:"high_priority_queue_size"`
	MediumPriorityQueueSize int           `mapstructure:"medium_priority_queue_size"`
	LowPriorityQueueSize    int           `mapstructure:"low_priority_queue_size"`
	MaxRetries              int           `mapstructure:"max_retries"`
	RetryInterval           time.Duration `mapstructure:"retry_interval"`
	StopTimeout             time.Duration `mapstructure:"stop_timeout"`
	JobTrackingCapacity     int           `mapstructure:"job_tracking_capacity"`

	// DeliveryConfirmationTimeout bounds how long a group notification waits
	// for ONE target to confirm delivery before that target is reported
	// unconfirmed (task rec, alertmanager-parity wave 3; exposed as a knob in
	// fix round 1, review finding I3).
	//
	// An unconfirmed target gets no notification-log entry and is retried on
	// the group's next scheduled fire, so raising this trades a longer-held
	// per-group publish lock for fewer duplicate notifications from slow
	// endpoints. Two grouping-side durations are DERIVED from it at startup —
	// the timer-callback deadline and the cross-replica publish-claim TTL (see
	// grouping/notify_budget.go) — so it is the single knob for the whole
	// notify-fire time budget.
	DeliveryConfirmationTimeout time.Duration `mapstructure:"delivery_confirmation_timeout"`
}

// PublishingRefreshConfig holds dynamic target refresh settings.
type PublishingRefreshConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	Interval     time.Duration `mapstructure:"interval"`
	MaxRetries   int           `mapstructure:"max_retries"`
	BaseBackoff  time.Duration `mapstructure:"base_backoff"`
	MaxBackoff   time.Duration `mapstructure:"max_backoff"`
	RateLimitPer time.Duration `mapstructure:"rate_limit_per"`
	Timeout      time.Duration `mapstructure:"timeout"`
	WarmupPeriod time.Duration `mapstructure:"warmup_period"`
}

// PublishingHealthConfig holds publishing target health settings.
type PublishingHealthConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	CheckInterval       time.Duration `mapstructure:"check_interval"`
	HTTPTimeout         time.Duration `mapstructure:"http_timeout"`
	WarmupDelay         time.Duration `mapstructure:"warmup_delay"`
	FailureThreshold    int           `mapstructure:"failure_threshold"`
	DegradedThreshold   time.Duration `mapstructure:"degraded_threshold"`
	MaxConcurrentChecks int           `mapstructure:"max_concurrent_checks"`
	MaxIdleConns        int           `mapstructure:"max_idle_conns"`
	TLSSkipVerify       bool          `mapstructure:"tls_skip_verify"`
	FollowRedirects     bool          `mapstructure:"follow_redirects"`
	MaxRedirects        int           `mapstructure:"max_redirects"`
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

	// Task 5.4 (carried fix): fail fast if the external inhibition rules
	// file (inhibition.config_file) is missing or malformed, instead of
	// silently dropping all file-based inhibition rules at startup/reload
	// time (see internal/config/inhibition_adapter.go).
	if _, err := cfg.Inhibition.ToInhibitionRules(); err != nil {
		return nil, fmt.Errorf("inhibition config validation failed: %w", err)
	}

	// Parse optional route:/receivers:/global: sections (task 1.3).
	// No-op (cfg.Routing stays nil) when the file has no route: section.
	if err := loadRouteConfig(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("route config validation failed: %w", err)
	}

	return &cfg, nil
}

// loadRouteConfig parses the optional `route:` + `receivers:` + `global:`
// top-level YAML sections via the existing infrastructure/routing.Parse()
// (task 1.3: alertmanager-parity).
//
// It intentionally re-reads the raw file bytes: the nested route/receiver
// types only carry `yaml` tags (no `mapstructure`), so they cannot be
// populated by viper.Unmarshal — they need their own gopkg.in/yaml.v3 pass,
// which is exactly what routing.Parse() does.
//
// Absent `route:` section is not an error: it means the config still uses
// the legacy single-receiver model, and cfg.Routing is left nil.
//
// Task 5.4: runs pkg/configvalidator's broader Alertmanager-parity checks
// (receiver integration shapes, inhibition, global, security - see
// alertmanager_validation.go) on the same raw bytes BEFORE
// infraroute.Parse() below, so a config failing both surfaces
// configvalidator's more detailed message first; routing.Parse() remains
// a backstop for anything configvalidator does not (yet) check.
func loadRouteConfig(configPath string, cfg *Config) error {
	if configPath == "" || !viper.IsSet("route") {
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Consistent with LoadConfig's tolerant handling of a missing file.
			return nil
		}
		return fmt.Errorf("failed to read config file for route parsing: %w", err)
	}

	if err := validateAlertmanagerSubset(data, cfg); err != nil {
		return err
	}

	parsed, err := infraroute.NewRouteConfigParser().Parse(data)
	if err != nil {
		return fmt.Errorf("invalid route/receivers configuration: %w", err)
	}

	cfg.Routing = parsed
	return nil
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

	// Task 5.4 (carried fix): same fail-fast as LoadConfig - see there.
	if _, err := cfg.Inhibition.ToInhibitionRules(); err != nil {
		return nil, fmt.Errorf("inhibition config validation failed: %w", err)
	}

	return &cfg, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// Deployment profile defaults (TN-200)
	viper.SetDefault("profile", "standard")                              // Default to standard profile
	viper.SetDefault("storage.backend", "postgres")                      // Default to Postgres
	viper.SetDefault("storage.filesystem_path", "/data/alerthistory.db") // SQLite path for Lite
	viper.SetDefault("storage.path", "")                                 // Empty = file snapshots DISABLED (wave 6)
	viper.SetDefault("storage.snapshot_interval", "5m")

	// Server defaults
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "120s")
	viper.SetDefault("server.graceful_shutdown_timeout", "30s")
	viper.SetDefault("server.external_url", "")
	viper.SetDefault("server.route_prefix", "")
	viper.SetDefault("server.websocket.allowed_origins", "")
	viper.SetDefault("server.cors.enabled", false)
	viper.SetDefault("server.cors.allowed_origins", "")
	viper.SetDefault("server.cors.allowed_methods", "GET, POST, PUT, DELETE, OPTIONS")
	viper.SetDefault("server.cors.allowed_headers", "Content-Type, Authorization, X-Request-ID")

	// Database defaults
	viper.SetDefault("database.driver", "postgres")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.database", "alerthistory")
	viper.SetDefault("database.username", "")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.ssl_mode", "require") // Secure by default
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

	// Silence cross-replica sync defaults (task fu5-cfg item 1,
	// alertmanager-parity wave-5): unchanged from the pre-config-knob
	// hardcoded literals in ServiceRegistry's runSilenceSubscribeLoop /
	// runSilencePeriodicResync.
	viper.SetDefault("silencing.subscribe_retry_backoff", "2s")
	viper.SetDefault("silencing.periodic_resync_interval", "5m")

	// Grouping subsystem defaults (task 2.2, alertmanager-parity)
	viper.SetDefault("grouping.enabled", false)
	// Distributed timer reconciliation defaults (task 6.2). Only takes
	// effect in the standard profile with a live Redis-backed TimerStorage
	// — see ServiceRegistry.initializeGrouping and GroupingConfig's doc
	// comment (config.go) for why the lite profile ignores this.
	viper.SetDefault("grouping.reconciliation_interval", "45s")
	// grouping.reconciliation_grace deliberately has NO viper default (wave-4
	// hygiene item 3, review finding M-a): a hardcoded literal here duplicated
	// grouping.ReconciliationGraceFor(deliveryConfirmationTimeout) with
	// nothing tying the two together, so raising
	// publishing.queue.delivery_confirmation_timeout alone (without also
	// raising this knob) failed startup validation for no reason a config
	// diff would explain. When this key is left unset, the field decodes to
	// zero and ServiceRegistry.initializeGrouping derives the effective grace
	// from the ACTUAL configured delivery-confirmation timeout via
	// ReconciliationGraceFor — see reconciliationGraceFor in
	// service_registry.go. An operator-supplied value (any positive
	// duration here) always wins over that derivation, and
	// validateNotifyTimingBudget still rejects the end result if it violates
	// the budget invariant.
	//
	// Two invariants meet at the DERIVED value — kept in sync with
	// grouping.defaultReconciliationGracePeriod, which computes the same
	// formula and is checked against reconciliation_interval at compile time:
	//
	//  1. Well BELOW the shared timer record's own Redis TTL grace
	//     (grouping.timerTTLGracePeriod, 10m): the difference between the two
	//     IS the adoption window. Equal values collapsed it to ~0s, so a timer
	//     became adoptable exactly when its key expired and a dead replica's
	//     groups never notified again (final review finding 2).
	//  2. ABOVE a whole notify fire, publish claim included (90s at the default
	//     delivery-confirmation timeout — task rec fix round 2, review finding
	//     R4). Since task rec a fire blocks until delivery is confirmed; a
	//     shorter grace makes a LIVE fire look orphaned, and the adopting
	//     replica — correctly blocked from double-notifying by the publish
	//     claim — still deletes the shared timer record, racing the
	//     publisher's continuation. Was 20s, i.e. shorter than the fire.
	//
	// ServiceRegistry.validateNotifyTimingBudget rechecks (2) at startup
	// against the actual publishing.queue.delivery_confirmation_timeout.

	// Investigation pipeline defaults (PHASE-5A)
	viper.SetDefault("investigation.enabled", false)
	viper.SetDefault("investigation.worker_count", 3)
	viper.SetDefault("investigation.queue_size", 500)
	viper.SetDefault("investigation.max_retries", 3)
	viper.SetDefault("investigation.retry_interval", "5s")
	viper.SetDefault("investigation.llm_timeout", "60s")
	viper.SetDefault("investigation.only_firing", true)

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

	// HTTP Client defaults (TN-204: Configurable Timeouts)
	viper.SetDefault("http_client.timeout", "30s")
	viper.SetDefault("http_client.dial_timeout", "5s")
	viper.SetDefault("http_client.tls_handshake_timeout", "5s")
	viper.SetDefault("http_client.response_header_timeout", "10s")
	viper.SetDefault("http_client.expect_continue_timeout", "1s")
	viper.SetDefault("http_client.keep_alive", "30s")
	viper.SetDefault("http_client.idle_conn_timeout", "90s")
	viper.SetDefault("http_client.max_idle_conns", 100)
	viper.SetDefault("http_client.max_idle_conns_per_host", 10)
	viper.SetDefault("http_client.max_conns_per_host", 0) // 0 = unlimited
	viper.SetDefault("http_client.min_tls_version", "1.2")
	viper.SetDefault("http_client.disable_http2", false)
	viper.SetDefault("http_client.insecure_skip_verify", false)

	// Publishing defaults
	viper.SetDefault("publishing.enabled", true)
	viper.SetDefault("publishing.discovery.namespace", "")
	viper.SetDefault("publishing.discovery.label_selector", "publishing-target=true")

	viper.SetDefault("publishing.queue.max_concurrent", 5)
	viper.SetDefault("publishing.queue.worker_count", 10)
	viper.SetDefault("publishing.queue.high_priority_queue_size", 500)
	viper.SetDefault("publishing.queue.medium_priority_queue_size", 1000)
	viper.SetDefault("publishing.queue.low_priority_queue_size", 500)
	viper.SetDefault("publishing.queue.max_retries", 3)
	viper.SetDefault("publishing.queue.retry_interval", "2s")
	viper.SetDefault("publishing.queue.stop_timeout", "10s")
	viper.SetDefault("publishing.queue.job_tracking_capacity", 10000)
	viper.SetDefault("publishing.queue.delivery_confirmation_timeout", "45s")

	viper.SetDefault("publishing.refresh.enabled", true)
	viper.SetDefault("publishing.refresh.interval", "5m")
	viper.SetDefault("publishing.refresh.max_retries", 5)
	viper.SetDefault("publishing.refresh.base_backoff", "30s")
	viper.SetDefault("publishing.refresh.max_backoff", "5m")
	viper.SetDefault("publishing.refresh.rate_limit_per", "1m")
	viper.SetDefault("publishing.refresh.timeout", "30s")
	viper.SetDefault("publishing.refresh.warmup_period", "30s")

	viper.SetDefault("publishing.health.enabled", true)
	viper.SetDefault("publishing.health.check_interval", "2m")
	viper.SetDefault("publishing.health.http_timeout", "5s")
	viper.SetDefault("publishing.health.warmup_delay", "10s")
	viper.SetDefault("publishing.health.failure_threshold", 3)
	viper.SetDefault("publishing.health.degraded_threshold", "5s")
	viper.SetDefault("publishing.health.max_concurrent_checks", 10)
	viper.SetDefault("publishing.health.max_idle_conns", 100)
	viper.SetDefault("publishing.health.tls_skip_verify", false)
	viper.SetDefault("publishing.health.follow_redirects", true)
	viper.SetDefault("publishing.health.max_redirects", 3)

	// Default receivers
	viper.SetDefault("receivers", []map[string]string{
		{"name": "default"},
	})
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

	if c.Server.ExternalURL != "" {
		if u, err := url.ParseRequestURI(c.Server.ExternalURL); err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("server.external_url must be a valid absolute URL, got %q", c.Server.ExternalURL)
		}
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

		// TN-205: Validate database credentials for production
		if err := c.validateDatabaseCredentials(); err != nil {
			return fmt.Errorf("database credentials validation failed: %w", err)
		}
	}

	// Redis is optional for both profiles (TN-202)
	// Validation only if Redis addr is provided.
	// Note: Redis is not recommended for Lite profile,
	// but it is allowed for testing/development.

	if c.Log.Level == "" {
		return fmt.Errorf("log level cannot be empty")
	}

	if c.App.Name == "" {
		return fmt.Errorf("app name cannot be empty")
	}

	if err := c.validatePublishing(); err != nil {
		return fmt.Errorf("publishing validation failed: %w", err)
	}

	if err := c.validateGrouping(); err != nil {
		return fmt.Errorf("grouping validation failed: %w", err)
	}

	if err := c.validateSilencing(); err != nil {
		return fmt.Errorf("silencing validation failed: %w", err)
	}

	if err := c.validateStorageSnapshot(); err != nil {
		return fmt.Errorf("storage snapshot validation failed: %w", err)
	}

	return nil
}

// validateStorageSnapshot checks storage.snapshot_interval (wave 6,
// FU-LITE-FILE-SNAPSHOT) only when storage.path is set — an empty path
// disables snapshotting entirely, so the interval is moot and left
// unvalidated (its default is still positive; this just avoids rejecting a
// config that never engages the feature). No profile check here: a
// standard-profile operator setting storage.path anyway is logged and
// ignored by ServiceRegistry.initializeSnapshotting, not a config error —
// see that method's doc comment.
func (c *Config) validateStorageSnapshot() error {
	if c.Storage.SnapshotPath == "" {
		return nil
	}
	if c.Storage.SnapshotInterval <= 0 {
		return fmt.Errorf("storage.snapshot_interval must be positive when storage.path is set")
	}
	return nil
}

// validateSilencing checks silencing.subscribe_retry_backoff/
// periodic_resync_interval (task fu5-cfg item 1): both must be positive, and
// the backoff must stay below the resync interval — a backoff at or past
// the resync period would make the periodic resync fire no more often (or
// less often) than a single resubscribe retry cycle, defeating its purpose
// as an independent-of-pub/sub backstop.
func (c *Config) validateSilencing() error {
	if c.Silencing.SubscribeRetryBackoff <= 0 {
		return fmt.Errorf("silencing.subscribe_retry_backoff must be positive")
	}
	if c.Silencing.PeriodicResyncInterval <= 0 {
		return fmt.Errorf("silencing.periodic_resync_interval must be positive")
	}
	if c.Silencing.SubscribeRetryBackoff >= c.Silencing.PeriodicResyncInterval {
		return fmt.Errorf(
			"silencing.subscribe_retry_backoff=%s must be less than silencing.periodic_resync_interval=%s",
			c.Silencing.SubscribeRetryBackoff, c.Silencing.PeriodicResyncInterval,
		)
	}
	return nil
}

// validateGrouping checks grouping.reconciliation_interval/reconciliation_grace
// against the adoption-window invariant (task fu2-d item 9): an operator
// value pair that leaves no room for the reconciliation loop to actually
// discover an orphaned timer before its Redis record's TTL grace period
// expires reopens the zero-adoption-window bug (final review finding 2),
// which grouping.timerTTLGracePeriod's compile-time guard only prevents for
// the hardcoded defaults, not for values that only exist at config-load
// time.
func (c *Config) validateGrouping() error {
	if err := grouping.ValidateReconciliationGrace(
		c.Grouping.ReconciliationInterval,
		c.Grouping.ReconciliationGrace,
	); err != nil {
		return fmt.Errorf(
			"grouping.reconciliation_grace=%s with grouping.reconciliation_interval=%s: %w",
			c.Grouping.ReconciliationGrace, c.Grouping.ReconciliationInterval, err,
		)
	}
	return nil
}

func (c *Config) validatePublishing() error {
	if !c.Publishing.Enabled {
		return nil
	}

	if c.Publishing.Queue.MaxConcurrent <= 0 {
		return fmt.Errorf("publishing.queue.max_concurrent must be positive")
	}
	if c.Publishing.Queue.WorkerCount <= 0 {
		return fmt.Errorf("publishing.queue.worker_count must be positive")
	}
	if c.Publishing.Queue.HighPriorityQueueSize <= 0 {
		return fmt.Errorf("publishing.queue.high_priority_queue_size must be positive")
	}
	if c.Publishing.Queue.MediumPriorityQueueSize <= 0 {
		return fmt.Errorf("publishing.queue.medium_priority_queue_size must be positive")
	}
	if c.Publishing.Queue.LowPriorityQueueSize <= 0 {
		return fmt.Errorf("publishing.queue.low_priority_queue_size must be positive")
	}
	if c.Publishing.Queue.MaxRetries < 0 {
		return fmt.Errorf("publishing.queue.max_retries must be non-negative")
	}
	if c.Publishing.Queue.RetryInterval <= 0 {
		return fmt.Errorf("publishing.queue.retry_interval must be positive")
	}
	if c.Publishing.Queue.StopTimeout <= 0 {
		return fmt.Errorf("publishing.queue.stop_timeout must be positive")
	}
	if c.Publishing.Queue.JobTrackingCapacity <= 0 {
		return fmt.Errorf("publishing.queue.job_tracking_capacity must be positive")
	}
	if c.Publishing.Queue.DeliveryConfirmationTimeout <= 0 {
		return fmt.Errorf("publishing.queue.delivery_confirmation_timeout must be positive")
	}
	// Upper bound (task rec fix round 2, review finding R9): this knob is not
	// just a timeout — the notify chain holds a group's publish lock and its
	// cross-replica claim for the whole wait, and the timer-callback deadline
	// and orphan-adoption grace are derived from it. A multi-minute value would
	// hold those for minutes and collide with group_interval/reconciliation
	// assumptions, so refuse it here rather than degrade quietly.
	if c.Publishing.Queue.DeliveryConfirmationTimeout > core.MaxDeliveryConfirmationTimeout {
		return fmt.Errorf(
			"publishing.queue.delivery_confirmation_timeout=%s exceeds the supported maximum %s "+
				"(the notify chain holds each group's publish lock and cross-replica claim for this long, and both the timer-callback deadline and the orphan-adoption grace are derived from it)",
			c.Publishing.Queue.DeliveryConfirmationTimeout, core.MaxDeliveryConfirmationTimeout)
	}

	if c.Publishing.Refresh.Enabled {
		if c.Publishing.Refresh.Interval <= 0 {
			return fmt.Errorf("publishing.refresh.interval must be positive")
		}
		if c.Publishing.Refresh.MaxRetries < 0 {
			return fmt.Errorf("publishing.refresh.max_retries must be non-negative")
		}
		if c.Publishing.Refresh.BaseBackoff <= 0 {
			return fmt.Errorf("publishing.refresh.base_backoff must be positive")
		}
		if c.Publishing.Refresh.MaxBackoff < c.Publishing.Refresh.BaseBackoff {
			return fmt.Errorf("publishing.refresh.max_backoff must be >= base_backoff")
		}
		if c.Publishing.Refresh.RateLimitPer <= 0 {
			return fmt.Errorf("publishing.refresh.rate_limit_per must be positive")
		}
		if c.Publishing.Refresh.Timeout <= 0 {
			return fmt.Errorf("publishing.refresh.timeout must be positive")
		}
		if c.Publishing.Refresh.WarmupPeriod < 0 {
			return fmt.Errorf("publishing.refresh.warmup_period must be non-negative")
		}
	}

	if c.Publishing.Health.Enabled {
		if c.Publishing.Health.CheckInterval <= 0 {
			return fmt.Errorf("publishing.health.check_interval must be positive")
		}
		if c.Publishing.Health.HTTPTimeout <= 0 {
			return fmt.Errorf("publishing.health.http_timeout must be positive")
		}
		if c.Publishing.Health.WarmupDelay < 0 {
			return fmt.Errorf("publishing.health.warmup_delay must be non-negative")
		}
		if c.Publishing.Health.FailureThreshold <= 0 {
			return fmt.Errorf("publishing.health.failure_threshold must be positive")
		}
		if c.Publishing.Health.DegradedThreshold <= 0 {
			return fmt.Errorf("publishing.health.degraded_threshold must be positive")
		}
		if c.Publishing.Health.MaxConcurrentChecks <= 0 {
			return fmt.Errorf("publishing.health.max_concurrent_checks must be positive")
		}
		if c.Publishing.Health.MaxIdleConns <= 0 {
			return fmt.Errorf("publishing.health.max_idle_conns must be positive")
		}
		if c.Publishing.Health.MaxRedirects < 0 {
			return fmt.Errorf("publishing.health.max_redirects must be non-negative")
		}
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

		// Note: Postgres is not used in Lite profile; a non-local
		// database host is tolerated to allow testing.

	case ProfileStandard:
		// Standard profile: require postgres storage
		if c.Storage.Backend != StorageBackendPostgres {
			return fmt.Errorf("standard profile requires storage.backend='postgres' (got '%s')", c.Storage.Backend)
		}

		// Postgres configuration is required (validated in main Validate())
	}

	return nil
}

// validateDatabaseCredentials validates database credentials for production (TN-205)
func (c *Config) validateDatabaseCredentials() error {
	// Check if credentials are provided
	if c.Database.Username == "" || c.Database.Password == "" {
		// Check environment
		if c.App.Environment == "production" {
			return fmt.Errorf("database credentials (username/password) are required in production environment")
		}

		// Development/testing: warn but allow
		if c.Database.Username == "" && c.Database.Password == "" {
			// Both empty - likely intentional for local development
			// Log warning (caller should log this)
			return nil
		}

		// One is set but not the other - likely misconfiguration
		return fmt.Errorf("database username and password must both be set or both be empty")
	}

	// Validate weak credentials in production
	if c.App.Environment == "production" {
		weakPasswords := []string{"password", "admin", "root", "dev", "test", "123456", "postgres"}
		for _, weak := range weakPasswords {
			if c.Database.Password == weak {
				return fmt.Errorf("weak database password detected: '%s' is not allowed in production", weak)
			}
		}

		// Check password length
		if len(c.Database.Password) < 12 {
			return fmt.Errorf("database password must be at least 12 characters in production (got %d)", len(c.Database.Password))
		}
	}

	// Validate SSL mode for production
	if c.App.Environment == "production" && c.Database.SSLMode == "disable" {
		return fmt.Errorf("database SSL mode 'disable' is not allowed in production (use 'require' or 'verify-full')")
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
