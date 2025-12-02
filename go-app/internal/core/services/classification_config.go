package services

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	"github.com/ipiton/AMP/pkg/metrics"
)

// ClassificationConfig holds classification service configuration.
type ClassificationConfig struct {
	// Cache settings
	CacheTTL          time.Duration // Default: 1 hour
	EnableMemoryCache bool          // Default: true (150% enhancement)
	MemoryCacheTTL    time.Duration // Default: 5 minutes
	CacheKeyPrefix    string        // Default: "classification:"

	// LLM settings
	EnableLLM  bool          // Default: true
	LLMTimeout time.Duration // Default: 30s

	// Fallback settings
	EnableFallback     bool    // Default: true
	FallbackConfidence float64 // Default: 0.5

	// Performance
	MaxBatchSize       int // Default: 50
	MaxConcurrentCalls int // Default: 10

	// Observability
	EnableMetrics      bool // Default: true
	EnableDetailedLogs bool // Default: false
}

// DefaultClassificationConfig returns default configuration.
func DefaultClassificationConfig() ClassificationConfig {
	return ClassificationConfig{
		CacheTTL:           1 * time.Hour,
		EnableMemoryCache:  true,
		MemoryCacheTTL:     5 * time.Minute,
		CacheKeyPrefix:     "classification:",
		EnableLLM:          true,
		LLMTimeout:         30 * time.Second,
		EnableFallback:     true,
		FallbackConfidence: 0.5,
		MaxBatchSize:       50,
		MaxConcurrentCalls: 10,
		EnableMetrics:      true,
		EnableDetailedLogs: false,
	}
}

// Validate checks configuration validity.
func (c *ClassificationConfig) Validate() error {
	if c.CacheTTL <= 0 {
		return fmt.Errorf("cache TTL must be positive")
	}

	if c.EnableMemoryCache && c.MemoryCacheTTL <= 0 {
		return fmt.Errorf("memory cache TTL must be positive when memory cache is enabled")
	}

	if c.MemoryCacheTTL > c.CacheTTL {
		return fmt.Errorf("memory cache TTL cannot exceed Redis cache TTL")
	}

	if c.CacheKeyPrefix == "" {
		return fmt.Errorf("cache key prefix cannot be empty")
	}

	if c.LLMTimeout <= 0 {
		return fmt.Errorf("LLM timeout must be positive")
	}

	if c.FallbackConfidence < 0 || c.FallbackConfidence > 1 {
		return fmt.Errorf("fallback confidence must be between 0 and 1")
	}

	if c.MaxBatchSize <= 0 {
		return fmt.Errorf("max batch size must be positive")
	}

	if c.MaxConcurrentCalls <= 0 {
		return fmt.Errorf("max concurrent calls must be positive")
	}

	return nil
}

// ClassificationServiceConfig holds all dependencies for creating ClassificationService.
type ClassificationServiceConfig struct {
	// Dependencies (required)
	LLMClient llm.LLMClient     // LLM client for classification
	Cache     cache.Cache       // Redis cache for L2 caching
	Storage   core.AlertStorage // Alert storage for persistence (optional)

	// Configuration
	Config ClassificationConfig

	// Observability (optional)
	Logger          *slog.Logger
	BusinessMetrics *metrics.BusinessMetrics
}

// Validate checks if all required dependencies are provided.
func (c *ClassificationServiceConfig) Validate() error {
	// LLM client is required if LLM is enabled
	if c.Config.EnableLLM && c.LLMClient == nil {
		return fmt.Errorf("LLM client is required when LLM is enabled")
	}

	// Cache is required
	if c.Cache == nil {
		return fmt.Errorf("cache is required")
	}

	// Validate config
	if err := c.Config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return nil
}
