package services

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"log/slog"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics"
)

// EnrichmentMode represents alert processing mode
type EnrichmentMode string

const (
	// EnrichmentModeTransparent - proxy alerts without LLM, WITH filtering
	EnrichmentModeTransparent EnrichmentMode = "transparent"

	// EnrichmentModeEnriched - classify with LLM, WITH filtering (default)
	EnrichmentModeEnriched EnrichmentMode = "enriched"

	// EnrichmentModeTransparentWithRecommendations - proxy without LLM, WITHOUT filtering
	EnrichmentModeTransparentWithRecommendations EnrichmentMode = "transparent_with_recommendations"
)

// Valid modes map
var validModes = map[EnrichmentMode]bool{
	EnrichmentModeTransparent:                    true,
	EnrichmentModeEnriched:                       true,
	EnrichmentModeTransparentWithRecommendations: true,
}

// IsValid checks if mode is valid
func (m EnrichmentMode) IsValid() bool {
	return validModes[m]
}

// String returns string representation
func (m EnrichmentMode) String() string {
	return string(m)
}

// ToMetricValue converts mode to metric gauge value (0, 1, 2)
func (m EnrichmentMode) ToMetricValue() float64 {
	switch m {
	case EnrichmentModeTransparent:
		return 0
	case EnrichmentModeEnriched:
		return 1
	case EnrichmentModeTransparentWithRecommendations:
		return 2
	default:
		return 1 // default to enriched
	}
}

// EnrichmentStats represents enrichment mode statistics
type EnrichmentStats struct {
	CurrentMode    EnrichmentMode `json:"current_mode"`
	Source         string         `json:"source"`
	LastSwitchTime *time.Time     `json:"last_switch_time,omitempty"`
	LastSwitchFrom EnrichmentMode `json:"last_switch_from,omitempty"`
	TotalSwitches  int64          `json:"total_switches"`
	RedisAvailable bool           `json:"redis_available"`
	CacheHitRate   float64        `json:"cache_hit_rate"`
}

// EnrichmentModeManager manages enrichment mode state
type EnrichmentModeManager interface {
	// GetMode returns current enrichment mode (uses in-memory cache)
	GetMode(ctx context.Context) (EnrichmentMode, error)

	// GetModeWithSource returns mode and source (redis/memory/env/default)
	GetModeWithSource(ctx context.Context) (EnrichmentMode, string, error)

	// SetMode sets new enrichment mode (saves to Redis + memory)
	SetMode(ctx context.Context, mode EnrichmentMode) error

	// ValidateMode validates if mode is supported
	ValidateMode(mode EnrichmentMode) error

	// GetStats returns enrichment statistics
	GetStats(ctx context.Context) (*EnrichmentStats, error)

	// RefreshCache forces cache refresh from Redis
	RefreshCache(ctx context.Context) error
}

const (
	redisKeyMode         = "enrichment:mode"
	redisKeyStats        = "enrichment:stats"
	defaultMode          = EnrichmentModeEnriched
	cacheRefreshInterval = 30 * time.Second
)

// enrichmentModeManager implements EnrichmentModeManager
type enrichmentModeManager struct {
	cache   cache.Cache
	logger  *slog.Logger
	metrics *metrics.MetricsManager

	// In-memory cache for fast access
	currentMode   EnrichmentMode
	currentSource string
	lastRefresh   time.Time

	// Stats
	totalSwitches  int64
	lastSwitchTime *time.Time
	lastSwitchFrom EnrichmentMode

	mu sync.RWMutex // protects in-memory state
}

// NewEnrichmentModeManager creates new enrichment mode manager
func NewEnrichmentModeManager(
	cache cache.Cache,
	logger *slog.Logger,
	metrics *metrics.MetricsManager,
) EnrichmentModeManager {
	if logger == nil {
		logger = slog.Default()
	}

	m := &enrichmentModeManager{
		cache:         cache,
		logger:        logger,
		metrics:       metrics,
		currentMode:   defaultMode,
		currentSource: "default",
		lastRefresh:   time.Now(),
	}

	// Initialize mode from storage
	ctx := context.Background()
	if err := m.RefreshCache(ctx); err != nil {
		logger.Warn("Failed to initialize mode from storage, using default",
			"error", err,
			"default_mode", defaultMode,
		)
	}

	return m
}

// GetMode returns current enrichment mode (uses in-memory cache)
func (m *enrichmentModeManager) GetMode(ctx context.Context) (EnrichmentMode, error) {
	m.mu.RLock()
	mode := m.currentMode
	lastRefresh := m.lastRefresh
	m.mu.RUnlock()

	// Auto-refresh if cache is stale
	if time.Since(lastRefresh) > cacheRefreshInterval {
		go func() {
			if err := m.RefreshCache(context.Background()); err != nil {
				m.logger.Debug("Background cache refresh failed", "error", err)
			}
		}()
	}

	return mode, nil
}

// GetModeWithSource returns mode and source
func (m *enrichmentModeManager) GetModeWithSource(ctx context.Context) (EnrichmentMode, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.currentMode, m.currentSource, nil
}

// SetMode sets new enrichment mode
func (m *enrichmentModeManager) SetMode(ctx context.Context, mode EnrichmentMode) error {
	// Validate mode
	if err := m.ValidateMode(mode); err != nil {
		return err
	}

	m.mu.Lock()
	oldMode := m.currentMode
	m.mu.Unlock()

	// Save to Redis first
	saved := false
	if m.cache != nil {
		data := map[string]any{
			"mode":      string(mode),
			"timestamp": time.Now().Unix(),
		}

		if err := m.cache.Set(ctx, redisKeyMode, data, 0); err != nil {
			m.logger.Warn("Failed to save mode to Redis, using memory fallback",
				"error", err,
				"mode", mode,
			)
		} else {
			saved = true
		}
	}

	// Update in-memory state
	m.mu.Lock()
	m.currentMode = mode
	m.currentSource = "memory"
	if saved {
		m.currentSource = "redis"
	}
	m.lastRefresh = time.Now()

	// Track switch if mode changed
	if oldMode != mode {
		m.totalSwitches++
		m.lastSwitchFrom = oldMode
		now := time.Now()
		m.lastSwitchTime = &now
	}
	m.mu.Unlock()

	// Update metrics
	if m.metrics != nil {
		m.updateMetrics(oldMode, mode)
	}

	m.logger.Info("Enrichment mode updated",
		"old_mode", oldMode,
		"new_mode", mode,
		"source", m.currentSource,
	)

	return nil
}

// ValidateMode validates if mode is supported
func (m *enrichmentModeManager) ValidateMode(mode EnrichmentMode) error {
	if !mode.IsValid() {
		return fmt.Errorf("invalid enrichment mode: %s (valid: transparent, enriched, transparent_with_recommendations)", mode)
	}
	return nil
}

// GetStats returns enrichment statistics
func (m *enrichmentModeManager) GetStats(ctx context.Context) (*EnrichmentStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := &EnrichmentStats{
		CurrentMode:    m.currentMode,
		Source:         m.currentSource,
		TotalSwitches:  m.totalSwitches,
		LastSwitchTime: m.lastSwitchTime,
		LastSwitchFrom: m.lastSwitchFrom,
		RedisAvailable: m.cache != nil,
	}

	return stats, nil
}

// RefreshCache forces cache refresh from Redis
func (m *enrichmentModeManager) RefreshCache(ctx context.Context) error {
	var mode EnrichmentMode
	var source string

	// 1. Try Redis
	if m.cache != nil {
		var data map[string]any
		err := m.cache.Get(ctx, redisKeyMode, &data)
		if err == nil && data != nil {
			if modeStr, ok := data["mode"].(string); ok {
				mode = EnrichmentMode(modeStr)
				if mode.IsValid() {
					source = "redis"
					goto found
				}
			}
		}

		// Check if error is not "not found"
		if err != nil && !cache.IsNotFound(err) {
			m.logger.Debug("Redis get failed", "error", err)
		}
	}

	// 2. Try ENV variable
	if envMode := os.Getenv("ENRICHMENT_MODE"); envMode != "" {
		mode = EnrichmentMode(envMode)
		if mode.IsValid() {
			source = "env"
			goto found
		}
		m.logger.Warn("Invalid ENRICHMENT_MODE env variable", "value", envMode)
	}

	// 3. Use default
	mode = defaultMode
	source = "default"

found:
	// Update in-memory cache
	m.mu.Lock()
	oldMode := m.currentMode
	m.currentMode = mode
	m.currentSource = source
	m.lastRefresh = time.Now()
	m.mu.Unlock()

	// Update metrics if mode changed
	if oldMode != mode && m.metrics != nil {
		m.updateMetrics(oldMode, mode)
	}

	m.logger.Debug("Cache refreshed",
		"mode", mode,
		"source", source,
	)

	return nil
}

// updateMetrics updates Prometheus metrics
func (m *enrichmentModeManager) updateMetrics(oldMode, newMode EnrichmentMode) {
	if m.metrics == nil {
		return
	}

	enrichmentMetrics := m.metrics.Enrichment()
	if enrichmentMetrics == nil {
		return
	}

	// Record mode switch
	if oldMode != newMode {
		enrichmentMetrics.RecordModeSwitch(oldMode.String(), newMode.String())
	}

	// Update mode status gauge
	enrichmentMetrics.SetModeStatus(newMode.ToMetricValue())
}
