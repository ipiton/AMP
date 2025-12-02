package inhibition

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// CacheMetrics contains Prometheus metrics for TwoTierAlertCache.
type CacheMetrics struct {
	// CacheHits tracks cache hit rate by tier (l1, l2)
	CacheHits *prometheus.CounterVec

	// CacheMisses tracks cache miss rate by tier
	CacheMisses *prometheus.CounterVec

	// Evictions tracks number of alerts evicted from L1 cache
	Evictions prometheus.Counter

	// CacheSize tracks current number of alerts in L1 cache
	CacheSize prometheus.Gauge

	// OperationsTotal tracks total cache operations by type (add, get, remove)
	OperationsTotal *prometheus.CounterVec

	// OperationDuration tracks cache operation latency in seconds
	OperationDuration *prometheus.HistogramVec
}

var (
	cacheMetricsOnce     sync.Once
	cacheMetricsInstance *CacheMetrics
)

// GetCacheMetrics returns the singleton CacheMetrics instance.
// Metrics are registered only once globally to avoid duplicate registration.
func GetCacheMetrics() *CacheMetrics {
	cacheMetricsOnce.Do(func() {
		cacheMetricsInstance = &CacheMetrics{
			CacheHits: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "alert_history_inhibition_cache_hits_total",
					Help: "Total number of cache hits by tier (l1, l2)",
				},
				[]string{"tier"},
			),
			CacheMisses: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "alert_history_inhibition_cache_misses_total",
					Help: "Total number of cache misses by tier",
				},
				[]string{"tier"},
			),
			Evictions: promauto.NewCounter(
				prometheus.CounterOpts{
					Name: "alert_history_inhibition_cache_evictions_total",
					Help: "Total number of alerts evicted from L1 cache",
				},
			),
			CacheSize: promauto.NewGauge(
				prometheus.GaugeOpts{
					Name: "alert_history_inhibition_cache_size",
					Help: "Current number of alerts in L1 cache",
				},
			),
			OperationsTotal: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "alert_history_inhibition_cache_operations_total",
					Help: "Total number of cache operations by type",
				},
				[]string{"operation"},
			),
			OperationDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "alert_history_inhibition_cache_operation_duration_seconds",
					Help:    "Cache operation latency in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"operation"},
			),
		}
	})
	return cacheMetricsInstance
}

// NewCacheMetrics is deprecated. Use GetCacheMetrics() instead.
// Kept for backward compatibility.
func NewCacheMetrics() *CacheMetrics {
	return GetCacheMetrics()
}

// TwoTierAlertCache implements ActiveAlertCache with two-tier caching strategy:
//   - L1: In-memory cache (fast, limited capacity)
//   - L2: Redis cache (persistent, distributed)
//
// Fallback strategy: L1 → L2 → empty (graceful degradation)
//
// Thread-safety: Safe for concurrent use (protected by mutex).
// Performance: L1 <1ms, L2 <10ms.
//
// Example:
//
//	cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
//	defer cache.Stop()
//	_ = cache.AddFiringAlert(ctx, alert)
type TwoTierAlertCache struct {
	// L1: In-memory cache (map-based)
	l1Cache map[string]*core.Alert
	l1Mutex sync.RWMutex
	l1Max   int // Max entries in L1

	// L2: Redis cache
	redisCache cache.Cache
	keyPrefix  string
	ttl        time.Duration

	// Background cleanup
	stopCh          chan struct{}
	cleanupDone     chan struct{}
	cleanupInterval time.Duration

	// Observability
	metrics *CacheMetrics
	logger  *slog.Logger
}

// AlertCacheOptions contains optional configuration for TwoTierAlertCache.
type AlertCacheOptions struct {
	// CleanupInterval sets how often to run background cleanup (default: 1 minute)
	CleanupInterval time.Duration
	// L1Max sets maximum entries in L1 cache (default: 1000)
	L1Max int
	// TTL sets Redis TTL (default: 5 minutes)
	TTL time.Duration
	// Metrics enables Prometheus metrics (default: nil = auto-create)
	Metrics *CacheMetrics
}

// NewTwoTierAlertCache creates a new two-tier alert cache.
//
// Parameters:
//   - redisCache: Redis cache for L2 (can be nil for L1-only mode)
//   - logger: structured logger
//
// Returns:
//   - *TwoTierAlertCache: initialized cache (starts background cleanup worker)
//
// Example:
//
//	cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
//	defer cache.Stop()
func NewTwoTierAlertCache(redisCache cache.Cache, logger *slog.Logger) *TwoTierAlertCache {
	return NewTwoTierAlertCacheWithOptions(redisCache, logger, nil)
}

// NewTwoTierAlertCacheWithOptions creates a new two-tier alert cache with custom options.
//
// Parameters:
//   - redisCache: Redis cache for L2 (can be nil for L1-only mode)
//   - logger: structured logger
//   - opts: optional configuration (can be nil for defaults)
//
// Returns:
//   - *TwoTierAlertCache: initialized cache (starts background cleanup worker)
//
// Example:
//
//	opts := &AlertCacheOptions{CleanupInterval: 30 * time.Second}
//	cache := inhibition.NewTwoTierAlertCacheWithOptions(redisCache, logger, opts)
//	defer cache.Stop()
func NewTwoTierAlertCacheWithOptions(redisCache cache.Cache, logger *slog.Logger, opts *AlertCacheOptions) *TwoTierAlertCache {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults
	cleanupInterval := 1 * time.Minute
	l1Max := 1000
	ttl := 5 * time.Minute
	var metrics *CacheMetrics

	if opts != nil {
		if opts.CleanupInterval > 0 {
			cleanupInterval = opts.CleanupInterval
		}
		if opts.L1Max > 0 {
			l1Max = opts.L1Max
		}
		if opts.TTL > 0 {
			ttl = opts.TTL
		}
		metrics = opts.Metrics
	}

	// Auto-create metrics if not provided (use singleton)
	if metrics == nil {
		metrics = GetCacheMetrics()
	}

	c := &TwoTierAlertCache{
		l1Cache:         make(map[string]*core.Alert),
		l1Max:           l1Max,
		redisCache:      redisCache,
		keyPrefix:       "inhibition:active_alerts:",
		ttl:             ttl,
		stopCh:          make(chan struct{}),
		cleanupDone:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		metrics:         metrics,
		logger:          logger,
	}

	// Start background cleanup worker
	go c.cleanupWorker()

	return c
}

// GetFiringAlerts implements ActiveAlertCache.GetFiringAlerts.
//
// Lookup strategy:
//  1. Try L1 cache (in-memory) → return if all found
//  2. Try L2 cache (Redis) → populate L1 → return
//  3. Return empty on error (graceful degradation)
func (c *TwoTierAlertCache) GetFiringAlerts(ctx context.Context) ([]*core.Alert, error) {
	start := time.Now()
	defer func() {
		c.metrics.OperationDuration.WithLabelValues("get").Observe(time.Since(start).Seconds())
		c.metrics.OperationsTotal.WithLabelValues("get").Inc()
	}()

	// Try L1 cache first
	c.l1Mutex.RLock()
	alerts := make([]*core.Alert, 0, len(c.l1Cache))
	for _, alert := range c.l1Cache {
		// Only return firing alerts
		if alert.Status == "firing" {
			alerts = append(alerts, alert)
		}
	}
	c.l1Mutex.RUnlock()

	// If L1 has alerts, return them
	if len(alerts) > 0 {
		c.metrics.CacheHits.WithLabelValues("l1").Inc()
		c.logger.Debug("L1 cache hit",
			"alerts_count", len(alerts))
		return alerts, nil
	}

	// L1 miss
	c.metrics.CacheMisses.WithLabelValues("l1").Inc()

	// Try L2 cache (Redis)
	if c.redisCache != nil {
		alerts, err := c.getFromRedis(ctx)
		if err != nil {
			c.metrics.CacheMisses.WithLabelValues("l2").Inc()
			c.logger.Warn("Failed to get alerts from Redis, returning empty",
				"error", err)
			return []*core.Alert{}, nil // Graceful degradation
		}

		if len(alerts) > 0 {
			c.metrics.CacheHits.WithLabelValues("l2").Inc()
		} else {
			c.metrics.CacheMisses.WithLabelValues("l2").Inc()
		}

		// Populate L1 cache
		c.populateL1(alerts)

		c.logger.Debug("L2 cache hit",
			"alerts_count", len(alerts))
		return alerts, nil
	}

	// No alerts found
	return []*core.Alert{}, nil
}

// AddFiringAlert implements ActiveAlertCache.AddFiringAlert.
//
// Adds alert to both L1 and L2 caches.
// L2 failures are logged but don't fail the operation (best-effort).
func (c *TwoTierAlertCache) AddFiringAlert(ctx context.Context, alert *core.Alert) error {
	start := time.Now()
	defer func() {
		c.metrics.OperationDuration.WithLabelValues("add").Observe(time.Since(start).Seconds())
		c.metrics.OperationsTotal.WithLabelValues("add").Inc()
	}()

	if alert == nil {
		return fmt.Errorf("alert is nil")
	}

	// Add to L1 cache
	c.l1Mutex.Lock()

	// Check capacity
	if len(c.l1Cache) >= c.l1Max {
		// Evict oldest alert (simple FIFO for now)
		var oldestKey string
		var oldestTime time.Time
		for key, a := range c.l1Cache {
			if oldestKey == "" || a.StartsAt.Before(oldestTime) {
				oldestKey = key
				oldestTime = a.StartsAt
			}
		}
		if oldestKey != "" {
			delete(c.l1Cache, oldestKey)
			c.metrics.Evictions.Inc()
		}
	}

	c.l1Cache[alert.Fingerprint] = alert
	c.metrics.CacheSize.Set(float64(len(c.l1Cache)))
	c.l1Mutex.Unlock()

	c.logger.Debug("Added alert to L1 cache",
		"fingerprint", alert.Fingerprint,
		"alert_name", alert.AlertName)

	// Add to L2 cache (Redis) - best effort
	if c.redisCache != nil {
		if err := c.addToRedis(ctx, alert); err != nil {
			c.logger.Warn("Failed to add alert to Redis (continuing with L1 only)",
				"error", err,
				"fingerprint", alert.Fingerprint)
			// Don't return error - L1 cache is sufficient
		} else {
			// Add fingerprint to SET for tracking
			setKey := c.keyPrefix + "set"
			if err := c.redisCache.SAdd(ctx, setKey, alert.Fingerprint); err != nil {
				c.logger.Warn("Failed to add fingerprint to Redis SET",
					"error", err,
					"fingerprint", alert.Fingerprint,
					"set_key", setKey)
				// Non-critical - alert data is in Redis
			}
		}
	}

	return nil
}

// RemoveAlert implements ActiveAlertCache.RemoveAlert.
//
// Removes alert from both L1 and L2 caches.
func (c *TwoTierAlertCache) RemoveAlert(ctx context.Context, fingerprint string) error {
	start := time.Now()
	defer func() {
		c.metrics.OperationDuration.WithLabelValues("remove").Observe(time.Since(start).Seconds())
		c.metrics.OperationsTotal.WithLabelValues("remove").Inc()
	}()

	// Remove from L1
	c.l1Mutex.Lock()
	delete(c.l1Cache, fingerprint)
	c.metrics.CacheSize.Set(float64(len(c.l1Cache)))
	c.l1Mutex.Unlock()

	c.logger.Debug("Removed alert from L1 cache",
		"fingerprint", fingerprint)

	// Remove from L2 (Redis) - best effort
	if c.redisCache != nil {
		key := c.redisKey(fingerprint)
		if err := c.redisCache.Delete(ctx, key); err != nil {
			c.logger.Warn("Failed to remove alert from Redis",
				"error", err,
				"fingerprint", fingerprint)
			// Don't return error - L1 removal is sufficient
		}

		// Remove fingerprint from SET
		setKey := c.keyPrefix + "set"
		if err := c.redisCache.SRem(ctx, setKey, fingerprint); err != nil {
			c.logger.Warn("Failed to remove fingerprint from Redis SET",
				"error", err,
				"fingerprint", fingerprint,
				"set_key", setKey)
			// Non-critical - alert data removed
		}
	}

	return nil
}

// Stop stops the background cleanup worker.
// Should be called when shutting down the cache.
func (c *TwoTierAlertCache) Stop() {
	close(c.stopCh)
	<-c.cleanupDone // Wait for cleanup worker to finish
}

// --- Private methods ---

// cleanupWorker runs periodic cleanup of expired alerts.
func (c *TwoTierAlertCache) cleanupWorker() {
	defer close(c.cleanupDone)

	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			c.logger.Info("Cleanup worker stopped")
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

// cleanup removes expired alerts from L1 cache.
func (c *TwoTierAlertCache) cleanup() {
	now := time.Now()
	removed := 0

	c.l1Mutex.Lock()
	for fingerprint, alert := range c.l1Cache {
		// Remove if alert has ended
		if alert.EndsAt != nil && alert.EndsAt.Before(now) {
			delete(c.l1Cache, fingerprint)
			removed++
			c.metrics.Evictions.Inc()
		}
		// Remove if alert is too old (TTL)
		if alert.StartsAt.Add(c.ttl).Before(now) {
			delete(c.l1Cache, fingerprint)
			removed++
			c.metrics.Evictions.Inc()
		}
	}
	c.metrics.CacheSize.Set(float64(len(c.l1Cache)))
	c.l1Mutex.Unlock()

	if removed > 0 {
		c.logger.Info("Cleanup completed",
			"removed_alerts", removed,
			"remaining_alerts", len(c.l1Cache))
	}
}

// getFromRedis retrieves all firing alerts from Redis using SET tracking.
//
// Implementation strategy:
//  1. Get all fingerprints from Redis SET "inhibition:active_alerts:set"
//  2. For each fingerprint, GET the JSON alert data
//  3. Deserialize and filter firing alerts only
//  4. Return all firing alerts
//
// Performance: O(N) where N is number of active alerts.
// Uses Redis SET for O(1) membership tracking.
//
// Enterprise-grade: Full recovery after pod restart!
func (c *TwoTierAlertCache) getFromRedis(ctx context.Context) ([]*core.Alert, error) {
	setKey := c.keyPrefix + "set"

	// Get all fingerprints from SET
	fingerprints, err := c.redisCache.SMembers(ctx, setKey)
	if err != nil {
		c.logger.Warn("Failed to get alert fingerprints from Redis SET",
			"error", err,
			"set_key", setKey)
		return []*core.Alert{}, nil // Graceful degradation
	}

	if len(fingerprints) == 0 {
		return []*core.Alert{}, nil
	}

	c.logger.Debug("Retrieving alerts from Redis",
		"fingerprint_count", len(fingerprints),
		"set_key", setKey)

	// Get alerts by fingerprints
	alerts := make([]*core.Alert, 0, len(fingerprints))
	retrieved := 0
	skipped := 0

	for _, fp := range fingerprints {
		key := c.redisKey(fp)
		var alertJSON string

		if err := c.redisCache.Get(ctx, key, &alertJSON); err != nil {
			// Alert expired or deleted - remove from SET
			_ = c.redisCache.SRem(ctx, setKey, fp)
			skipped++
			continue
		}

		var alert core.Alert
		if err := json.Unmarshal([]byte(alertJSON), &alert); err != nil {
			c.logger.Warn("Failed to unmarshal alert from Redis",
				"error", err,
				"fingerprint", fp)
			skipped++
			continue
		}

		// Only return firing alerts
		if alert.Status == "firing" {
			alerts = append(alerts, &alert)
			retrieved++
		} else {
			skipped++
		}
	}

	c.logger.Info("Retrieved alerts from Redis L2 cache",
		"retrieved", retrieved,
		"skipped", skipped,
		"total_fingerprints", len(fingerprints))

	return alerts, nil
}

// addToRedis adds an alert to Redis with TTL.
func (c *TwoTierAlertCache) addToRedis(ctx context.Context, alert *core.Alert) error {
	key := c.redisKey(alert.Fingerprint)

	// Serialize alert to JSON
	data, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	// Set with TTL
	return c.redisCache.Set(ctx, key, string(data), c.ttl)
}

// populateL1 populates L1 cache from a list of alerts.
func (c *TwoTierAlertCache) populateL1(alerts []*core.Alert) {
	c.l1Mutex.Lock()
	defer c.l1Mutex.Unlock()

	for _, alert := range alerts {
		// Don't exceed capacity
		if len(c.l1Cache) >= c.l1Max {
			break
		}
		c.l1Cache[alert.Fingerprint] = alert
	}
}

// redisKey generates Redis key for an alert fingerprint.
func (c *TwoTierAlertCache) redisKey(fingerprint string) string {
	return c.keyPrefix + fingerprint
}
