package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ConfigService provides configuration export functionality
type ConfigService interface {
	// GetConfig exports current configuration with specified options
	GetConfig(ctx context.Context, opts GetConfigOptions) (*ConfigResponse, error)

	// GetConfigVersion returns version hash of current configuration
	GetConfigVersion() string

	// GetConfigSource returns source of configuration
	GetConfigSource() ConfigSource
}

// GetConfigOptions specifies export options
type GetConfigOptions struct {
	Format   string   // "json" or "yaml" (default: "json")
	Sanitize bool     // Whether to sanitize secrets (default: true)
	Sections []string // Filter to specific sections (empty = all)
}

// ConfigResponse contains exported configuration
type ConfigResponse struct {
	Version        string                 `json:"version"`                  // SHA256 hash of config
	Source         ConfigSource           `json:"source"`                   // Configuration source
	LoadedAt       time.Time              `json:"loaded_at"`                // When config was loaded
	ConfigFilePath string                 `json:"config_file_path,omitempty"` // Path if from file
	Config         map[string]interface{} `json:"config"`                    // Actual config data
}

// ConfigSource represents configuration source
type ConfigSource string

const (
	ConfigSourceFile     ConfigSource = "file"     // Loaded from YAML file
	ConfigSourceEnv      ConfigSource = "env"      // Loaded from environment variables only
	ConfigSourceDefaults ConfigSource = "defaults"  // Using default values only
	ConfigSourceMixed    ConfigSource = "mixed"    // File + environment variables
)

// DefaultConfigService implements ConfigService
type DefaultConfigService struct {
	config     *Config
	configPath string
	loadedAt   time.Time
	source     ConfigSource
	sanitizer  ConfigSanitizer

	// Cache for serialized responses (TTL: 1s)
	cacheMu     sync.RWMutex
	cachedResp  *ConfigResponse
	cacheKey    string
	cacheExpiry time.Time
}

// NewConfigService creates a new ConfigService instance
func NewConfigService(
	cfg *Config,
	configPath string,
	loadedAt time.Time,
	source ConfigSource,
) ConfigService {
	return &DefaultConfigService{
		config:     cfg,
		configPath: configPath,
		loadedAt:   loadedAt,
		source:     source,
		sanitizer:  NewDefaultConfigSanitizer(),
	}
}

// GetConfig exports current configuration with specified options
func (s *DefaultConfigService) GetConfig(
	ctx context.Context,
	opts GetConfigOptions,
) (*ConfigResponse, error) {
	// Set defaults
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Sanitize {
		// Default is true, so if not explicitly false, sanitize
	}

	// Check cache
	cacheKey := s.buildCacheKey(opts)
	if cached := s.getCachedResponse(cacheKey); cached != nil {
		return cached, nil
	}

	// Deep copy config to avoid mutations
	configCopy := s.deepCopyConfig()

	// Sanitize if requested
	if opts.Sanitize {
		configCopy = s.sanitizer.Sanitize(configCopy)
	}

	// Filter sections if requested
	if len(opts.Sections) > 0 {
		configCopy = s.filterSections(configCopy, opts.Sections)
	}

	// Convert to map for JSON/YAML serialization
	configMap, err := s.configToMap(configCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config to map: %w", err)
	}

	// Build response
	resp := &ConfigResponse{
		Version:        s.GetConfigVersion(),
		Source:         s.source,
		LoadedAt:       s.loadedAt,
		ConfigFilePath: s.configPath,
		Config:         configMap,
	}

	// Cache response (TTL: 1s)
	s.setCachedResponse(cacheKey, resp)

	return resp, nil
}

// GetConfigVersion returns SHA256 hash of current configuration
func (s *DefaultConfigService) GetConfigVersion() string {
	// Serialize config to JSON for hashing
	configJSON, err := json.Marshal(s.config)
	if err != nil {
		// Fallback to timestamp-based version if serialization fails
		return fmt.Sprintf("error-%d", time.Now().Unix())
	}

	// Calculate SHA256 hash
	hash := sha256.Sum256(configJSON)
	return hex.EncodeToString(hash[:])
}

// GetConfigSource returns source of configuration
func (s *DefaultConfigService) GetConfigSource() ConfigSource {
	return s.source
}

// buildCacheKey builds cache key from options
func (s *DefaultConfigService) buildCacheKey(opts GetConfigOptions) string {
	sectionsKey := ""
	if len(opts.Sections) > 0 {
		sectionsKey = fmt.Sprintf("-%v", opts.Sections)
	}
	return fmt.Sprintf("%s-%s-%t%s",
		s.GetConfigVersion(),
		opts.Format,
		opts.Sanitize,
		sectionsKey,
	)
}

// getCachedResponse retrieves cached response if valid
func (s *DefaultConfigService) getCachedResponse(cacheKey string) *ConfigResponse {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if s.cachedResp != nil &&
		s.cacheKey == cacheKey &&
		time.Now().Before(s.cacheExpiry) {
		return s.cachedResp
	}

	return nil
}

// setCachedResponse caches response with TTL
func (s *DefaultConfigService) setCachedResponse(cacheKey string, resp *ConfigResponse) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cachedResp = resp
	s.cacheKey = cacheKey
	s.cacheExpiry = time.Now().Add(1 * time.Second)
}

// deepCopyConfig creates a deep copy of configuration
func (s *DefaultConfigService) deepCopyConfig() *Config {
	// Use JSON serialization for deep copy (simple and reliable)
	configJSON, err := json.Marshal(s.config)
	if err != nil {
		// Fallback: return original (should not happen with valid config)
		return s.config
	}

	var configCopy Config
	if err := json.Unmarshal(configJSON, &configCopy); err != nil {
		// Fallback: return original
		return s.config
	}

	return &configCopy
}

// filterSections filters config to include only specified sections
func (s *DefaultConfigService) filterSections(
	cfg *Config,
	sections []string,
) *Config {
	// Create filtered config with only requested sections
	filtered := &Config{}

	for _, section := range sections {
		switch section {
		case "server":
			filtered.Server = cfg.Server
		case "database":
			filtered.Database = cfg.Database
		case "redis":
			filtered.Redis = cfg.Redis
		case "llm":
			filtered.LLM = cfg.LLM
		case "log":
			filtered.Log = cfg.Log
		case "cache":
			filtered.Cache = cfg.Cache
		case "lock":
			filtered.Lock = cfg.Lock
		case "app":
			filtered.App = cfg.App
		case "metrics":
			filtered.Metrics = cfg.Metrics
		case "webhook":
			filtered.Webhook = cfg.Webhook
		}
	}

	return filtered
}

// configToMap converts Config struct to map[string]interface{}
func (s *DefaultConfigService) configToMap(cfg *Config) (map[string]interface{}, error) {
	// Use JSON serialization for conversion
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(configJSON, &configMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config to map: %w", err)
	}

	return configMap, nil
}
