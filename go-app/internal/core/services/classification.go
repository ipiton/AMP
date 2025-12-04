// Package services provides core business logic for alert processing.
package services

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	"github.com/ipiton/AMP/pkg/metrics"
)

// ClassificationService manages alert classification with intelligent caching and fallback.
//
// Features:
//   - Two-tier caching (memory L1 + Redis L2) for optimal performance
//   - Circuit breaker protection through LLM client integration
//   - Intelligent fallback for high availability
//   - Comprehensive Prometheus metrics
//   - Thread-safe concurrent access
//
// Architecture:
//
//	Request → [L1 Cache Check] → [L2 Cache Check] → [LLM Call] → [Fallback] → Response
//
// Performance:
//   - Cache hit (L1): <5ms (target)
//   - Cache miss + LLM: <500ms (target)
//   - Fallback: <1ms (target)
type ClassificationService interface {
	// ClassifyAlert classifies a single alert with caching and fallback.
	ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)

	// GetCachedClassification retrieves cached classification if available.
	GetCachedClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error)

	// ClassifyBatch processes multiple alerts concurrently (150% enhancement).
	ClassifyBatch(ctx context.Context, alerts []*core.Alert) ([]*core.ClassificationResult, error)

	// InvalidateCache removes classification from cache.
	InvalidateCache(ctx context.Context, fingerprint string) error

	// WarmCache pre-populates cache for expected alerts (150% enhancement).
	WarmCache(ctx context.Context, alerts []*core.Alert) error

	// GetStats returns classification service statistics.
	GetStats() ClassificationStats

	// Health checks service health.
	Health(ctx context.Context) error
}

// classificationService implements ClassificationService interface.
type classificationService struct {
	// Dependencies
	llmClient       llm.LLMClient
	cache           cache.Cache
	storage         core.AlertStorage
	logger          *slog.Logger
	businessMetrics *metrics.BusinessMetrics

	// Configuration
	config ClassificationConfig

	// In-memory cache (L1) - 150% enhancement
	memCache    *sync.Map
	memCacheTTL time.Duration

	// Fallback strategy
	fallbackEnabled bool
	fallbackEngine  FallbackEngine

	// Statistics (thread-safe)
	stats *classificationStats
}

// classificationStats tracks in-memory statistics (thread-safe).
type classificationStats struct {
	mu sync.RWMutex

	// Request counters
	totalRequests int64
	cacheHits     int64
	cacheMisses   int64

	// LLM counters
	llmCalls     int64
	llmSuccesses int64
	llmFailures  int64
	fallbackUsed int64

	// Performance metrics
	totalDuration   time.Duration
	avgResponseTime time.Duration

	// Error tracking
	lastError     error
	lastErrorTime *time.Time
}

// cacheEntry represents an L1 cache entry with TTL.
type cacheEntry struct {
	Result    *core.ClassificationResult
	ExpiresAt time.Time
}

// IsExpired checks if cache entry is expired.
func (e *cacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// ClassificationStats represents public statistics.
type ClassificationStats struct {
	TotalRequests   int64         `json:"total_requests"`
	CacheHitRate    float64       `json:"cache_hit_rate"`
	LLMSuccessRate  float64       `json:"llm_success_rate"`
	FallbackRate    float64       `json:"fallback_rate"`
	AvgResponseTime time.Duration `json:"avg_response_time"`
	LastError       string        `json:"last_error,omitempty"`
	LastErrorTime   *time.Time    `json:"last_error_time,omitempty"`
}

// NewClassificationService creates a new classification service.
func NewClassificationService(config ClassificationServiceConfig) (ClassificationService, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Apply defaults
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Create fallback engine if enabled
	var fallbackEngine FallbackEngine
	if config.Config.EnableFallback {
		fallbackEngine = NewRuleBasedFallback(config.Logger)
	}

	// Initialize in-memory cache
	memCache := &sync.Map{}
	memCacheTTL := config.Config.MemoryCacheTTL
	if memCacheTTL == 0 {
		memCacheTTL = 5 * time.Minute // Default
	}

	svc := &classificationService{
		llmClient:       config.LLMClient,
		cache:           config.Cache,
		storage:         config.Storage,
		logger:          config.Logger,
		businessMetrics: config.BusinessMetrics,
		config:          config.Config,
		memCache:        memCache,
		memCacheTTL:     memCacheTTL,
		fallbackEnabled: config.Config.EnableFallback,
		fallbackEngine:  fallbackEngine,
		stats:           &classificationStats{},
	}

	config.Logger.Info("Classification service initialized",
		"cache_ttl", config.Config.CacheTTL,
		"memory_cache_enabled", config.Config.EnableMemoryCache,
		"fallback_enabled", config.Config.EnableFallback,
		"max_batch_size", config.Config.MaxBatchSize)

	return svc, nil
}

// ClassifyAlert classifies a single alert with two-tier caching and fallback.
func (s *classificationService) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		s.updateStats(duration)
		if s.businessMetrics != nil {
			s.businessMetrics.RecordClassificationDuration("total", duration.Seconds())
		}
	}()

	// Increment request counter
	s.incrementTotalRequests()

	// Validate input
	if alert == nil {
		return nil, fmt.Errorf("alert cannot be nil")
	}
	if alert.Fingerprint == "" {
		return nil, fmt.Errorf("alert fingerprint is required")
	}

	s.logger.Debug("Classifying alert",
		"fingerprint", alert.Fingerprint,
		"alert_name", alert.AlertName)

	// Step 1: Check cache (two-tier)
	if cached, found := s.getFromCache(ctx, alert.Fingerprint); found {
		s.logger.Debug("Cache hit",
			"fingerprint", alert.Fingerprint,
			"severity", cached.Severity)
		if s.businessMetrics != nil {
			s.businessMetrics.RecordClassificationDuration("cache", time.Since(startTime).Seconds())
		}
		return cached, nil
	}

	// Step 2: Call LLM (if enabled and available)
	if s.config.EnableLLM && s.llmClient != nil {
		result, err := s.classifyWithLLM(ctx, alert)
		if err == nil {
			// Success - cache and return
			s.saveToCache(ctx, alert.Fingerprint, result)
			s.incrementLLMSuccess()

			if s.businessMetrics != nil {
				s.businessMetrics.LLMClassificationsTotal("llm_" + string(result.Severity))
				s.businessMetrics.RecordClassificationDuration("llm", time.Since(startTime).Seconds())
			}

			return result, nil
		}

		// LLM failed - log and continue to fallback
		s.incrementLLMFailure()
		s.recordError(err)
		s.logger.Warn("LLM classification failed, falling back",
			"fingerprint", alert.Fingerprint,
			"error", err)
	}

	// Step 3: Fallback classification
	if s.fallbackEnabled && s.fallbackEngine != nil {
		result := s.classifyWithFallback(alert)
		s.incrementFallbackUsed()

		// Cache fallback result
		// Note: Using standard TTL for fallback results
		s.saveToCache(ctx, alert.Fingerprint, result)

		if s.businessMetrics != nil {
			s.businessMetrics.LLMClassificationsTotal("fallback_" + string(result.Severity))
			s.businessMetrics.RecordClassificationDuration("fallback", time.Since(startTime).Seconds())
		}

		s.logger.Info("Using fallback classification",
			"fingerprint", alert.Fingerprint,
			"severity", result.Severity,
			"confidence", result.Confidence)

		return result, nil
	}

	// Step 4: No classification available
	err := fmt.Errorf("classification unavailable: LLM failed and fallback disabled")
	s.recordError(err)
	return nil, err
}

// GetCachedClassification retrieves cached classification if available.
func (s *classificationService) GetCachedClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error) {
	if fingerprint == "" {
		return nil, fmt.Errorf("fingerprint is required")
	}

	result, found := s.getFromCache(ctx, fingerprint)
	if !found {
		return nil, cache.ErrNotFound
	}

	return result, nil
}

// ClassifyBatch processes multiple alerts concurrently (150% enhancement).
func (s *classificationService) ClassifyBatch(ctx context.Context, alerts []*core.Alert) ([]*core.ClassificationResult, error) {
	if len(alerts) == 0 {
		return nil, fmt.Errorf("alerts slice is empty")
	}

	if len(alerts) > s.config.MaxBatchSize {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(alerts), s.config.MaxBatchSize)
	}

	s.logger.Info("Processing batch classification",
		"batch_size", len(alerts),
		"max_concurrent", s.config.MaxConcurrentCalls)

	results := make([]*core.ClassificationResult, len(alerts))
	errors := make([]error, len(alerts))

	// Semaphore for concurrency control
	sem := make(chan struct{}, s.config.MaxConcurrentCalls)

	var wg sync.WaitGroup
	for i, alert := range alerts {
		wg.Add(1)
		go func(idx int, a *core.Alert) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }() // Release

			result, err := s.ClassifyAlert(ctx, a)
			if err != nil {
				errors[idx] = fmt.Errorf("alert %d (%s): %w", idx, a.Fingerprint, err)
			} else {
				results[idx] = result
			}
		}(i, alert)
	}

	wg.Wait()

	// Count errors
	var errCount int
	for _, err := range errors {
		if err != nil {
			errCount++
			s.logger.Error("Batch classification error", "error", err)
		}
	}

	if errCount > 0 {
		return results, fmt.Errorf("batch classification completed with %d errors", errCount)
	}

	s.logger.Info("Batch classification completed successfully",
		"batch_size", len(alerts),
		"success_count", len(alerts)-errCount)

	return results, nil
}

// InvalidateCache removes classification from cache.
func (s *classificationService) InvalidateCache(ctx context.Context, fingerprint string) error {
	if fingerprint == "" {
		return fmt.Errorf("fingerprint is required")
	}

	s.logger.Debug("Invalidating cache", "fingerprint", fingerprint)

	// Remove from L1 (memory)
	if s.config.EnableMemoryCache {
		s.memCache.Delete(fingerprint)
	}

	// Remove from L2 (Redis)
	key := s.getCacheKey(fingerprint)
	if err := s.cache.Delete(ctx, key); err != nil && err != cache.ErrNotFound {
		return fmt.Errorf("failed to invalidate cache: %w", err)
	}

	s.logger.Info("Cache invalidated", "fingerprint", fingerprint)
	return nil
}

// WarmCache pre-populates cache for expected alerts (150% enhancement).
func (s *classificationService) WarmCache(ctx context.Context, alerts []*core.Alert) error {
	s.logger.Info("Warming cache", "alert_count", len(alerts))

	successCount := 0
	for _, alert := range alerts {
		// Check if already cached
		if _, found := s.getFromCache(ctx, alert.Fingerprint); found {
			successCount++
			continue
		}

		// Classify and cache
		_, err := s.ClassifyAlert(ctx, alert)
		if err != nil {
			s.logger.Warn("Failed to warm cache for alert",
				"fingerprint", alert.Fingerprint,
				"error", err)
		} else {
			successCount++
		}
	}

	s.logger.Info("Cache warming completed",
		"total", len(alerts),
		"success", successCount,
		"failed", len(alerts)-successCount)

	return nil
}

// GetStats returns classification service statistics.
func (s *classificationService) GetStats() ClassificationStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()

	var cacheHitRate, llmSuccessRate, fallbackRate float64

	if s.stats.totalRequests > 0 {
		cacheHitRate = float64(s.stats.cacheHits) / float64(s.stats.totalRequests)
		fallbackRate = float64(s.stats.fallbackUsed) / float64(s.stats.totalRequests)
	}

	if s.stats.llmCalls > 0 {
		llmSuccessRate = float64(s.stats.llmSuccesses) / float64(s.stats.llmCalls)
	}

	var lastErrorStr string
	if s.stats.lastError != nil {
		lastErrorStr = s.stats.lastError.Error()
	}

	return ClassificationStats{
		TotalRequests:   s.stats.totalRequests,
		CacheHitRate:    cacheHitRate,
		LLMSuccessRate:  llmSuccessRate,
		FallbackRate:    fallbackRate,
		AvgResponseTime: s.stats.avgResponseTime,
		LastError:       lastErrorStr,
		LastErrorTime:   s.stats.lastErrorTime,
	}
}

// Health checks service health.
func (s *classificationService) Health(ctx context.Context) error {
	// Check LLM client health
	if s.llmClient != nil {
		if err := s.llmClient.Health(ctx); err != nil {
			s.logger.Warn("LLM client unhealthy (non-critical)", "error", err)
			// Not critical if fallback is enabled
			if !s.fallbackEnabled {
				return fmt.Errorf("LLM client unhealthy and fallback disabled: %w", err)
			}
		}
	}

	// Check cache health
	if s.cache != nil {
		if err := s.cache.HealthCheck(ctx); err != nil {
			s.logger.Warn("Cache unhealthy (non-critical)", "error", err)
			// Cache is optional - don't fail health check
		}
	}

	return nil
}

// classifyWithLLM calls LLM client for classification.
func (s *classificationService) classifyWithLLM(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	s.incrementLLMCalls()

	// Apply timeout if configured
	if s.config.LLMTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.config.LLMTimeout)
		defer cancel()
	}

	result, err := s.llmClient.ClassifyAlert(ctx, alert)
	if err != nil {
		return nil, fmt.Errorf("LLM classification failed: %w", err)
	}

	// Validate result
	if result == nil {
		return nil, fmt.Errorf("LLM returned nil result")
	}

	return result, nil
}

// classifyWithFallback uses fallback engine for classification.
func (s *classificationService) classifyWithFallback(alert *core.Alert) *core.ClassificationResult {
	return s.fallbackEngine.Classify(alert)
}

// getFromCache retrieves classification from two-tier cache.
func (s *classificationService) getFromCache(ctx context.Context, fingerprint string) (*core.ClassificationResult, bool) {
	// Check L1 (memory) first
	if s.config.EnableMemoryCache {
		if cached, ok := s.memCache.Load(fingerprint); ok {
			if entry, ok := cached.(*cacheEntry); ok && !entry.IsExpired() {
				s.incrementCacheHit()
				s.logger.Debug("L1 cache hit", "fingerprint", fingerprint)
				if s.businessMetrics != nil {
					s.businessMetrics.RecordClassificationL1CacheHit()
				}
				return entry.Result, true
			}
			// Expired - remove from cache
			s.memCache.Delete(fingerprint)
		}
	}

	// Check L2 (Redis)
	var result core.ClassificationResult
	key := s.getCacheKey(fingerprint)
	if err := s.cache.Get(ctx, key, &result); err == nil {
		// Populate L1 cache
		if s.config.EnableMemoryCache {
			s.memCache.Store(fingerprint, &cacheEntry{
				Result:    &result,
				ExpiresAt: time.Now().Add(s.memCacheTTL),
			})
		}
		s.incrementCacheHit()
		s.logger.Debug("L2 cache hit", "fingerprint", fingerprint)
		if s.businessMetrics != nil {
			s.businessMetrics.RecordClassificationL2CacheHit()
		}
		return &result, true
	}

	s.incrementCacheMiss()
	s.logger.Debug("Cache miss", "fingerprint", fingerprint)
	return nil, false
}

// saveToCache saves classification to two-tier cache.
func (s *classificationService) saveToCache(ctx context.Context, fingerprint string, result *core.ClassificationResult) {
	// Save to L1 (memory)
	if s.config.EnableMemoryCache {
		s.memCache.Store(fingerprint, &cacheEntry{
			Result:    result,
			ExpiresAt: time.Now().Add(s.memCacheTTL),
		})
	}

	// Save to L2 (Redis)
	key := s.getCacheKey(fingerprint)
	if err := s.cache.Set(ctx, key, result, s.config.CacheTTL); err != nil {
		s.logger.Error("Failed to save to cache",
			"fingerprint", fingerprint,
			"error", err)
	}
}

// getCacheKey generates Redis cache key.
func (s *classificationService) getCacheKey(fingerprint string) string {
	return s.config.CacheKeyPrefix + fingerprint
}

// Statistics helper methods (thread-safe)

func (s *classificationService) incrementTotalRequests() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.totalRequests++
}

func (s *classificationService) incrementCacheHit() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.cacheHits++
}

func (s *classificationService) incrementCacheMiss() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.cacheMisses++
}

func (s *classificationService) incrementLLMCalls() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.llmCalls++
}

func (s *classificationService) incrementLLMSuccess() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.llmSuccesses++
}

func (s *classificationService) incrementLLMFailure() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.llmFailures++
}

func (s *classificationService) incrementFallbackUsed() {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.fallbackUsed++
}

func (s *classificationService) updateStats(duration time.Duration) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	s.stats.totalDuration += duration
	if s.stats.totalRequests > 0 {
		s.stats.avgResponseTime = time.Duration(int64(s.stats.totalDuration) / s.stats.totalRequests)
	}
}

func (s *classificationService) recordError(err error) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	s.stats.lastError = err
	now := time.Now()
	s.stats.lastErrorTime = &now
}
