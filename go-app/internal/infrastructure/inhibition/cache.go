package inhibition

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
//   - L1: In-memory cache — the only tier read by GetFiringAlerts (hot path)
//   - L2: Redis — shared between replicas, merged into L1 by a background sync
//
// A cached alert is an inhibition source while it is firing and its EndsAt is
// in the future, checked at query time (upstream types.Alert.ResolvedAt). An
// alert received without endsAt gets EndsAt = now + resolve_timeout, and every
// AddFiringAlert (including an unchanged re-send) extends it — upstream
// api/v2 insertAlerts semantics. The age of an alert (StartsAt) never evicts it.
//
// Thread-safety: Safe for concurrent use (protected by mutex).
//
// Example:
//
//	cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
//	defer cache.Stop()
//	_ = cache.AddFiringAlert(ctx, alert)
type TwoTierAlertCache struct {
	// L1: In-memory cache (map-based)
	l1Cache map[string]*cacheEntry
	// tombstones holds fingerprints removed locally until the next sync pass
	// that started after the removal — so a pass that read Redis before the
	// removal cannot bring the alert back.
	tombstones map[string]time.Time
	l1Mutex    sync.RWMutex
	l1Max      int // Max entries in L1

	// L2: Redis cache
	redisCache cache.Cache
	keyPrefix  string

	// resolveTimeout supplies global.resolve_timeout for alerts without endsAt.
	resolveTimeout func() time.Duration
	now            func() time.Time

	// Background cleanup and L2 → L1 sync
	stopCh          chan struct{}
	cleanupDone     chan struct{}
	cleanupInterval time.Duration
	syncInterval    time.Duration

	// Observability
	metrics *CacheMetrics
	logger  *slog.Logger
}

// cacheEntry is one L1 record. alert is a copy of the ingested alert with
// EndsAt always set (see withExpiry); the caller's alert is never mutated.
type cacheEntry struct {
	alert *core.Alert
	// writtenAt orders local writes against a running sync pass: a write made
	// after the pass started wins over what the pass read from Redis.
	writtenAt time.Time
	// inRedis marks an entry known to be in the shared Redis SET. Only such
	// entries are dropped by a sync pass that no longer finds them there (the
	// alert was resolved or expired on another replica); an entry whose Redis
	// write failed lives until its own EndsAt.
	inRedis bool
}

// AlertCacheOptions contains optional configuration for TwoTierAlertCache.
type AlertCacheOptions struct {
	// CleanupInterval sets how often expired alerts are dropped from L1 (default: 1 minute)
	CleanupInterval time.Duration
	// SyncInterval sets how often L1 is merged with Redis, i.e. how soon a
	// source received by another replica becomes visible (default: 5 seconds)
	SyncInterval time.Duration
	// L1Max sets maximum entries in L1 cache (default: 1000)
	L1Max int
	// ResolveTimeout supplies global.resolve_timeout, read on every add
	// (default: alertconv.DefaultResolveTimeout)
	ResolveTimeout func() time.Duration
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
//   - *TwoTierAlertCache: initialized cache (starts background worker)
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
//   - *TwoTierAlertCache: initialized cache (starts background worker; with
//     Redis the first sync runs immediately, restoring sources after a restart)
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
	syncInterval := 5 * time.Second
	l1Max := 1000
	var resolveTimeout func() time.Duration
	var metrics *CacheMetrics

	if opts != nil {
		if opts.CleanupInterval > 0 {
			cleanupInterval = opts.CleanupInterval
		}
		if opts.SyncInterval > 0 {
			syncInterval = opts.SyncInterval
		}
		if opts.L1Max > 0 {
			l1Max = opts.L1Max
		}
		resolveTimeout = opts.ResolveTimeout
		metrics = opts.Metrics
	}

	// Auto-create metrics if not provided (use singleton)
	if metrics == nil {
		metrics = GetCacheMetrics()
	}

	c := &TwoTierAlertCache{
		l1Cache:         make(map[string]*cacheEntry),
		tombstones:      make(map[string]time.Time),
		l1Max:           l1Max,
		redisCache:      redisCache,
		keyPrefix:       "inhibition:active_alerts:",
		resolveTimeout:  resolveTimeout,
		now:             time.Now,
		stopCh:          make(chan struct{}),
		cleanupDone:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
		syncInterval:    syncInterval,
		metrics:         metrics,
		logger:          logger,
	}

	// Start background worker
	go c.backgroundWorker()

	return c
}

// GetFiringAlerts implements ActiveAlertCache.GetFiringAlerts.
//
// Reads L1 only: alerts from other replicas arrive through the background
// sync, so the hot path (every ingest and every alert of a flushed group)
// never waits on Redis. Returns alerts that are firing with EndsAt after now.
func (c *TwoTierAlertCache) GetFiringAlerts(ctx context.Context) ([]*core.Alert, error) {
	start := time.Now()
	defer func() {
		c.metrics.OperationDuration.WithLabelValues("get").Observe(time.Since(start).Seconds())
		c.metrics.OperationsTotal.WithLabelValues("get").Inc()
	}()

	now := c.now()
	c.l1Mutex.RLock()
	alerts := make([]*core.Alert, 0, len(c.l1Cache))
	for _, entry := range c.l1Cache {
		if isActive(entry.alert, now) {
			alerts = append(alerts, entry.alert)
		}
	}
	c.l1Mutex.RUnlock()

	if len(alerts) > 0 {
		c.metrics.CacheHits.WithLabelValues("l1").Inc()
	} else {
		c.metrics.CacheMisses.WithLabelValues("l1").Inc()
	}

	return alerts, nil
}

// AddFiringAlert implements ActiveAlertCache.AddFiringAlert.
//
// Adds or refreshes the alert in L1 and L2. Must be called on every ingest of
// a firing alert, unchanged re-sends included: that is what extends the
// resolve_timeout window of an alert without endsAt.
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

	now := c.now()
	stamped := c.withExpiry(alert, now)
	entry := &cacheEntry{alert: stamped, writtenAt: now}

	c.l1Mutex.Lock()
	if _, exists := c.l1Cache[alert.Fingerprint]; !exists && len(c.l1Cache) >= c.l1Max {
		c.evictLocked(now)
	}
	c.l1Cache[alert.Fingerprint] = entry
	delete(c.tombstones, alert.Fingerprint)
	c.metrics.CacheSize.Set(float64(len(c.l1Cache)))
	c.l1Mutex.Unlock()

	c.logger.Debug("Added alert to L1 cache",
		"fingerprint", alert.Fingerprint,
		"alert_name", alert.AlertName,
		"ends_at", stamped.EndsAt)

	// Add to L2 cache (Redis) - best effort. An alert that has already ended
	// is not shared: it is no inhibition source for anyone.
	ttl := stamped.EndsAt.Sub(now)
	if c.redisCache == nil || ttl <= 0 {
		return nil
	}
	if err := c.addToRedis(ctx, stamped, ttl); err != nil {
		c.logger.Warn("Failed to add alert to Redis (continuing with L1 only)",
			"error", err,
			"fingerprint", alert.Fingerprint)
		return nil
	}
	setKey := c.keyPrefix + "set"
	if err := c.redisCache.SAdd(ctx, setKey, alert.Fingerprint); err != nil {
		c.logger.Warn("Failed to add fingerprint to Redis SET",
			"error", err,
			"fingerprint", alert.Fingerprint,
			"set_key", setKey)
		return nil
	}

	c.l1Mutex.Lock()
	if c.l1Cache[alert.Fingerprint] == entry {
		entry.inRedis = true
	}
	c.l1Mutex.Unlock()

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
	if c.redisCache != nil {
		c.tombstones[fingerprint] = c.now()
	}
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

// Stop stops the background worker.
// Should be called when shutting down the cache.
func (c *TwoTierAlertCache) Stop() {
	close(c.stopCh)
	<-c.cleanupDone // Wait for background worker to finish
}

// --- Private methods ---

// isActive reports whether a cached alert is an inhibition source at now.
func isActive(alert *core.Alert, now time.Time) bool {
	return alert.Status == core.StatusFiring && alert.EndsAt != nil && alert.EndsAt.After(now)
}

// withExpiry returns a copy of alert with EndsAt set: the alert's own endsAt,
// or now + resolve_timeout when it came without one.
func (c *TwoTierAlertCache) withExpiry(alert *core.Alert, now time.Time) *core.Alert {
	stamped := *alert
	var endsAt time.Time
	if alert.EndsAt != nil {
		endsAt = *alert.EndsAt
	} else {
		endsAt = now.Add(c.currentResolveTimeout())
	}
	stamped.EndsAt = &endsAt
	return &stamped
}

// currentResolveTimeout returns the configured resolve_timeout, or
// alertconv.DefaultResolveTimeout when none (or a non-positive one) is set.
func (c *TwoTierAlertCache) currentResolveTimeout() time.Duration {
	if c.resolveTimeout != nil {
		if d := c.resolveTimeout(); d > 0 {
			return d
		}
	}
	return alertconv.DefaultResolveTimeout
}

// evictLocked makes room for one more L1 entry: expired entries go first, then
// the entry closest to its EndsAt. Caller holds l1Mutex.
func (c *TwoTierAlertCache) evictLocked(now time.Time) {
	if c.removeExpiredLocked(now) > 0 && len(c.l1Cache) < c.l1Max {
		return
	}

	var victim string
	var victimEnds time.Time
	for fp, entry := range c.l1Cache {
		if victim == "" || entry.alert.EndsAt.Before(victimEnds) {
			victim = fp
			victimEnds = *entry.alert.EndsAt
		}
	}
	if victim != "" {
		delete(c.l1Cache, victim)
		c.metrics.Evictions.Inc()
		c.logger.Warn("Inhibition cache full, evicted an active alert",
			"fingerprint", victim,
			"l1_max", c.l1Max)
	}
}

// removeExpiredLocked drops entries that are no longer inhibition sources.
// Caller holds l1Mutex.
func (c *TwoTierAlertCache) removeExpiredLocked(now time.Time) int {
	removed := 0
	for fp, entry := range c.l1Cache {
		if !isActive(entry.alert, now) {
			delete(c.l1Cache, fp)
			removed++
			c.metrics.Evictions.Inc()
		}
	}
	return removed
}

// backgroundWorker runs periodic cleanup of expired alerts and, with Redis,
// the L2 → L1 sync (first pass immediately).
func (c *TwoTierAlertCache) backgroundWorker() {
	defer close(c.cleanupDone)

	cleanupTicker := time.NewTicker(c.cleanupInterval)
	defer cleanupTicker.Stop()

	var syncC <-chan time.Time
	if c.redisCache != nil {
		syncTicker := time.NewTicker(c.syncInterval)
		defer syncTicker.Stop()
		syncC = syncTicker.C
		c.syncFromRedis()
	}

	for {
		select {
		case <-c.stopCh:
			c.logger.Info("Cleanup worker stopped")
			return
		case <-cleanupTicker.C:
			c.cleanup()
		case <-syncC:
			c.syncFromRedis()
		}
	}
}

// cleanup removes expired alerts from L1 cache.
func (c *TwoTierAlertCache) cleanup() {
	c.l1Mutex.Lock()
	removed := c.removeExpiredLocked(c.now())
	remaining := len(c.l1Cache)
	c.metrics.CacheSize.Set(float64(remaining))
	c.l1Mutex.Unlock()

	if removed > 0 {
		c.logger.Info("Cleanup completed",
			"removed_alerts", removed,
			"remaining_alerts", remaining)
	}
}

// syncFromRedis merges the shared Redis set into L1, so sources ingested by
// another replica become visible here and sources resolved there disappear.
//
// Ordering against local writes: an entry written or removed locally after the
// pass started is newer than what the pass read and is left alone. A Redis
// error leaves L1 untouched.
func (c *TwoTierAlertCache) syncFromRedis() {
	start := time.Now()
	defer func() {
		c.metrics.OperationDuration.WithLabelValues("sync").Observe(time.Since(start).Seconds())
		c.metrics.OperationsTotal.WithLabelValues("sync").Inc()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.syncInterval)
	defer cancel()

	passStart := c.now()
	remote, err := c.getFromRedis(ctx)
	if err != nil {
		c.metrics.CacheMisses.WithLabelValues("l2").Inc()
		c.logger.Warn("Failed to sync inhibition cache from Redis, keeping local state",
			"error", err)
		return
	}
	c.metrics.CacheHits.WithLabelValues("l2").Inc()

	c.l1Mutex.Lock()
	defer c.l1Mutex.Unlock()

	seen := make(map[string]struct{}, len(remote))
	for _, alert := range remote {
		fp := alert.Fingerprint
		seen[fp] = struct{}{}

		if removedAt, ok := c.tombstones[fp]; ok && !removedAt.Before(passStart) {
			continue
		}
		local, exists := c.l1Cache[fp]
		if exists && !local.writtenAt.Before(passStart) {
			continue
		}
		stamped := c.withExpiry(alert, passStart)
		if exists && !stamped.EndsAt.After(*local.alert.EndsAt) {
			local.inRedis = true
			continue
		}
		if !exists && len(c.l1Cache) >= c.l1Max {
			c.evictLocked(passStart)
		}
		c.l1Cache[fp] = &cacheEntry{alert: stamped, writtenAt: passStart, inRedis: true}
	}

	for fp, entry := range c.l1Cache {
		if _, ok := seen[fp]; !ok && entry.inRedis && entry.writtenAt.Before(passStart) {
			delete(c.l1Cache, fp)
		}
	}
	for fp, removedAt := range c.tombstones {
		if removedAt.Before(passStart) {
			delete(c.tombstones, fp)
		}
	}
	c.metrics.CacheSize.Set(float64(len(c.l1Cache)))

	c.logger.Debug("Synced inhibition cache from Redis",
		"remote_alerts", len(remote),
		"l1_alerts", len(c.l1Cache))
}

// getFromRedis retrieves all firing alerts from Redis using SET tracking.
//
// Implementation strategy:
//  1. Get all fingerprints from Redis SET "inhibition:active_alerts:set"
//  2. For each fingerprint, GET the JSON alert data; a key that expired
//     (TTL = until EndsAt) is removed from the SET
//  3. Deserialize and filter firing alerts only
//
// Any Redis error other than a missing key is returned: the caller must not
// mistake an unreadable set for an empty one.
func (c *TwoTierAlertCache) getFromRedis(ctx context.Context) ([]*core.Alert, error) {
	setKey := c.keyPrefix + "set"

	// Get all fingerprints from SET
	fingerprints, err := c.redisCache.SMembers(ctx, setKey)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", setKey, err)
	}

	alerts := make([]*core.Alert, 0, len(fingerprints))
	skipped := 0

	for _, fp := range fingerprints {
		key := c.redisKey(fp)
		var alertJSON string

		if err := c.redisCache.Get(ctx, key, &alertJSON); err != nil {
			if !cache.IsNotFound(err) {
				return nil, fmt.Errorf("read %s: %w", key, err)
			}
			// Alert expired or deleted - remove from SET
			if err := c.redisCache.SRem(ctx, setKey, fp); err != nil {
				c.logger.Debug("Failed to remove stale fingerprint from Redis SET",
					"error", err,
					"fingerprint", fp)
			}
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
		if alert.Status == core.StatusFiring {
			alerts = append(alerts, &alert)
		} else {
			skipped++
		}
	}

	c.logger.Debug("Retrieved alerts from Redis L2 cache",
		"retrieved", len(alerts),
		"skipped", skipped,
		"total_fingerprints", len(fingerprints))

	return alerts, nil
}

// addToRedis stores an alert in Redis until its EndsAt.
func (c *TwoTierAlertCache) addToRedis(ctx context.Context, alert *core.Alert, ttl time.Duration) error {
	key := c.redisKey(alert.Fingerprint)

	// Serialize alert to JSON
	data, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	return c.redisCache.Set(ctx, key, string(data), ttl)
}

// redisKey generates Redis key for an alert fingerprint.
func (c *TwoTierAlertCache) redisKey(fingerprint string) string {
	return c.keyPrefix + fingerprint
}
