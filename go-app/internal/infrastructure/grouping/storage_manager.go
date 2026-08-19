// Package grouping provides automatic fallback/recovery coordination for group storage.
//
// StorageManager coordinates between RedisGroupStorage (primary) and MemoryGroupStorage (fallback),
// providing seamless automatic switching when Redis becomes unhealthy or recovers.
//
// Features:
//   - Automatic fallback to MemoryGroupStorage on Redis failure
//   - Automatic recovery to RedisGroupStorage when Redis restored
//   - Health check polling every 30s (configurable, see StorageManagerConfig)
//   - Metrics for fallback/recovery events, plus a backend-active gauge
//     (BusinessMetrics.SetActiveGroupStorageBackend)
//   - Graceful shutdown
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
//
// # Runtime failback/failforward (alertmanager-parity wave-5,
// FU-STORAGEMANAGER-FAILBACK)
//
// This type existed fully built — health probe, per-call fallback, recovery
// switch, fallback/recovery counters — since 2025-11-04, but
// ServiceRegistry.newGroupingStorage (added by the alertmanager-parity epic,
// 2026-08-18) never actually constructed one: it picked RedisGroupStorage or
// MemoryGroupStorage ONCE at startup and returned that value directly. A
// Redis outage after startup therefore had no runtime detection at all — the
// per-call fallback in GroupManager's callers (none; GroupManager itself
// holds no fallback logic) never engaged, and requests just got the
// underlying Redis error. wave-5 wires this type into newGroupingStorage's
// standard-profile Redis path (service_registry.go) to close that gap.
//
// # What recovery does NOT do, and why
//
// On primary recovery, checkHealthAndSwitch flips sm.current back to primary
// immediately — it does NOT read back and re-write whatever accumulated in
// the fallback store while primary was down. This is a deliberate MINIMUM
// VIABLE scope boundary (full state-merge machinery — reconciling two stores
// that may both have live writes for overlapping keys, with no vector clock
// or last-writer-wins policy defined anywhere in this codebase — is a
// separate, much larger piece of work than "detect + make visible + resume
// safely"):
//
//   - Any AlertGroup created or updated in the fallback store during the
//     outage is NOT copied into the primary on recovery. The next Load for
//     that GroupKey goes to the (now current again) primary and gets a
//     GroupNotFoundError, even though the group is sitting right there in
//     the fallback store — DefaultGroupManager.AddAlertToGroup treats that
//     as "no group yet" and starts a fresh one (see manager_impl.go), so
//     this fails safe rather than fails silent, but the fallback group's
//     accumulated alerts are not merged into the new one automatically.
//   - This is NOT the same convergence story as wave-4's nflog/delivered-state
//     (see manager_impl.go's RecordPartialDelivery / redis_notify_log.go):
//     that data converges FOR FREE because it is keyed by
//     (group, target, alert fingerprint) with a bounded TTL — an entry that
//     never made it to Redis simply expires and the next fire re-sends,
//     which is safe because notify-log entries are idempotent dedup markers,
//     not the group's actual alert membership. A GroupStorage entry has no
//     such TTL-based "worst case is a harmless resend" property: it IS the
//     alert membership, so losing track of it (rather than re-deriving it)
//     is the actual risk this boundary accepts.
//   - Mitigations already in place, not new to this task: (1) the fallback
//     store is exactly as durable as the process — a crash during the outage
//     loses it either way, same as before this task; (2) the alert ingest
//     pipeline re-adds any alert still firing on its next classification
//     pass, so an orphaned fallback-store group's alerts reappear as a NEW
//     group on the primary rather than vanishing — duplicate notifications
//     for the outage window are possible, silent data loss is not; (3) the
//     backend-active gauge and the loud Warn/Info log lines on every switch
//     (see checkHealthAndSwitch) make the window operationally visible
//     instead of a silent gap, which is this task's actual deliverable.
package grouping

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics kept until v2 migration (v2 lacks BusinessMetrics)
)

// StorageManager coordinates between primary (Redis) and fallback (Memory) storage.
//
// Thread-safety: All methods are thread-safe via sync.RWMutex.
//
// Behavior:
//   - Starts with primary storage (Redis)
//   - Polls health every 30s via Ping()
//   - On Redis failure: switches to fallback (Memory), records metric
//   - On Redis recovery: switches back to primary (Redis), records metric
//   - All storage operations automatically use current active storage
//
// Example:
//
//	primary, _ := grouping.NewRedisGroupStorage(redisConfig)
//	fallback := grouping.NewMemoryGroupStorage(logger)
//
//	manager := grouping.NewStorageManager(grouping.StorageManagerConfig{
//		Primary:  primary,
//		Fallback: fallback,
//		Logger:   logger,
//		Metrics:  metrics,
//	})
//	defer manager.Stop()
//
//	// Automatically uses Redis (or Memory if Redis fails)
//	err := manager.Store(ctx, group)
type StorageManager struct {
	// primary storage (typically RedisGroupStorage)
	primary GroupStorage

	// fallback storage (typically MemoryGroupStorage)
	fallback GroupStorage

	// current active storage (primary or fallback)
	current GroupStorage

	// primaryLabel/fallbackLabel name the two backends for the
	// backend-active gauge (task fu5-cfg item 2) and log lines — e.g.
	// "redis"/"memory". Defaulted by NewStorageManager when left empty.
	primaryLabel  string
	fallbackLabel string

	// mu protects current field
	mu sync.RWMutex

	// logger for structured logging
	logger *slog.Logger

	// metrics for observability
	metrics *metrics.BusinessMetrics

	// healthCheckInterval controls how often checkHealthAndSwitch polls
	// primary.Ping (task fu5-cfg item 2: made configurable, default
	// unchanged at 30s, so tests can inject a short interval instead of
	// sleeping through a real 30s tick).
	healthCheckInterval time.Duration

	// healthTicker for periodic health checks
	healthTicker *time.Ticker

	// stopChan signals health check goroutine to stop
	stopChan chan struct{}

	// stopped indicates if manager has been stopped
	stopped bool
}

// StorageManagerConfig configures NewStorageManager.
type StorageManagerConfig struct {
	// Primary is the primary storage (typically RedisGroupStorage). Required.
	Primary GroupStorage

	// Fallback is the fallback storage (typically MemoryGroupStorage). Required.
	Fallback GroupStorage

	// Logger for structured logging. Optional, defaults to slog.Default().
	Logger *slog.Logger

	// Metrics for fallback/recovery/backend-active tracking. Optional (nil
	// disables metrics, same posture as the rest of this package).
	Metrics *metrics.BusinessMetrics

	// HealthCheckInterval overrides the default 30s primary.Ping poll
	// interval (task fu5-cfg item 2). Optional; <= 0 uses the default.
	HealthCheckInterval time.Duration

	// PrimaryLabel/FallbackLabel name the two backends for the
	// backend-active gauge and log lines (task fu5-cfg item 2). Optional;
	// empty defaults to "redis"/"memory" — this type's only real-world
	// pairing (newGroupingStorage in service_registry.go).
	PrimaryLabel  string
	FallbackLabel string
}

// defaultStorageManagerHealthCheckInterval is the pre-task-fu5-cfg-item-2
// hardcoded literal, kept as the default when StorageManagerConfig leaves
// HealthCheckInterval unset.
const defaultStorageManagerHealthCheckInterval = 30 * time.Second

// NewStorageManager creates a new StorageManager with automatic fallback/recovery.
//
// The health check goroutine starts immediately and polls every
// cfg.HealthCheckInterval (default 30s). Call Stop() to gracefully shutdown
// when done.
//
// Example:
//
//	manager := grouping.NewStorageManager(grouping.StorageManagerConfig{
//		Primary: redisStorage, Fallback: memoryStorage, Logger: logger, Metrics: metrics,
//	})
//	defer manager.Stop()
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
func NewStorageManager(cfg StorageManagerConfig) *StorageManager {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	interval := cfg.HealthCheckInterval
	if interval <= 0 {
		interval = defaultStorageManagerHealthCheckInterval
	}

	primaryLabel := cfg.PrimaryLabel
	if primaryLabel == "" {
		primaryLabel = "redis"
	}
	fallbackLabel := cfg.FallbackLabel
	if fallbackLabel == "" {
		fallbackLabel = "memory"
	}

	sm := &StorageManager{
		primary:             cfg.Primary,
		fallback:            cfg.Fallback,
		current:             cfg.Primary, // Start with primary (Redis)
		primaryLabel:        primaryLabel,
		fallbackLabel:       fallbackLabel,
		logger:              logger,
		metrics:             cfg.Metrics,
		healthCheckInterval: interval,
		stopChan:            make(chan struct{}),
	}

	if sm.metrics != nil {
		sm.metrics.SetActiveGroupStorageBackend(primaryLabel)
	}

	// Start health check poller
	sm.startHealthCheck()

	logger.Info("Initialized storage manager with automatic fallback/recovery",
		"initial_storage", primaryLabel, "health_check_interval", interval)

	return sm
}

// startHealthCheck polls primary storage health every healthCheckInterval
// and switches storage accordingly.
//
// Goroutine lifecycle:
//   - Starts immediately in NewStorageManager
//   - Polls every healthCheckInterval
//   - Stops when Stop() is called
//
// Behavior:
//   - On primary.Ping() error: switch to fallback (if not already)
//   - On primary.Ping() success: switch back to primary (if was fallback)
//   - Records metrics for fallback/recovery events
func (sm *StorageManager) startHealthCheck() {
	sm.healthTicker = time.NewTicker(sm.healthCheckInterval)

	go func() {
		for {
			select {
			case <-sm.healthTicker.C:
				sm.checkHealthAndSwitch()
			case <-sm.stopChan:
				sm.logger.Debug("Health check goroutine stopped")
				return
			}
		}
	}()

	sm.logger.Debug("Started health check poller", "interval", sm.healthCheckInterval)
}

// checkHealthAndSwitch checks primary storage health and switches if needed.
//
// See this file's package doc ("What recovery does NOT do, and why") for why
// the primary-recovered branch is a clean cutover, not a merge of whatever
// accumulated in the fallback store while primary was down.
func (sm *StorageManager) checkHealthAndSwitch() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := sm.primary.Ping(ctx)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err != nil {
		// Primary unhealthy → switch to fallback
		if sm.current == sm.primary {
			sm.logger.Warn("Primary storage unhealthy, switching to fallback",
				"error", err, "backend", sm.fallbackLabel)
			sm.current = sm.fallback

			// Record fallback metric
			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("health_check_failed")
				sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
			}
		}
	} else {
		// Primary healthy → switch back to primary
		if sm.current == sm.fallback {
			sm.logger.Info("Primary storage recovered, switching back from fallback",
				"backend", sm.primaryLabel)
			sm.current = sm.primary

			// Record recovery metric
			if sm.metrics != nil {
				sm.metrics.IncStorageRecovery()
				sm.metrics.SetActiveGroupStorageBackend(sm.primaryLabel)
			}
		}
	}
}

// Stop gracefully stops the health check goroutine.
//
// Thread-safe: Can be called multiple times safely.
//
// Example:
//
//	manager := grouping.NewStorageManager(...)
//	defer manager.Stop()
func (sm *StorageManager) Stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.stopped {
		return
	}

	// Stop ticker
	if sm.healthTicker != nil {
		sm.healthTicker.Stop()
	}

	// Signal goroutine to stop
	close(sm.stopChan)

	sm.stopped = true
	sm.logger.Info("Stopped storage manager")
}

// === Storage Interface Implementation with Automatic Fallback ===

// Store delegates to current storage with automatic fallback on error.
//
// Behavior:
//   - Try current storage (primary or fallback)
//   - On error AND using primary: switch to fallback, retry
//   - Record fallback metric on switch
func (sm *StorageManager) Store(ctx context.Context, group *AlertGroup) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.Store(ctx, group)
	if err != nil {
		// On error, try fallback if we were using primary
		sm.mu.Lock()
		if sm.current == sm.primary {
			sm.logger.Warn("Primary storage Store failed, falling back to memory",
				"group_key", group.Key,
				"error", err)
			sm.current = sm.fallback

			// Record fallback metric
			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("store_error")
				sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
			}
		}
		sm.mu.Unlock()

		// Retry with fallback
		return sm.fallback.Store(ctx, group)
	}

	return nil
}

// Load delegates to current storage (no automatic fallback for reads).
//
// Rationale: Load failures typically indicate group doesn't exist (ErrNotFound),
// not storage failure. Fallback would return inconsistent data.
func (sm *StorageManager) Load(ctx context.Context, groupKey GroupKey) (*AlertGroup, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Load(ctx, groupKey)
}

// Delete delegates to current storage with automatic fallback on error.
func (sm *StorageManager) Delete(ctx context.Context, groupKey GroupKey) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.Delete(ctx, groupKey)
	if err != nil {
		// On error, try fallback if we were using primary
		sm.mu.Lock()
		if sm.current == sm.primary {
			sm.logger.Warn("Primary storage Delete failed, falling back to memory",
				"group_key", groupKey,
				"error", err)
			sm.current = sm.fallback

			// Record fallback metric
			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("delete_error")
				sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
			}
		}
		sm.mu.Unlock()

		// Retry with fallback
		return sm.fallback.Delete(ctx, groupKey)
	}

	return nil
}

// ListKeys delegates to current storage.
func (sm *StorageManager) ListKeys(ctx context.Context) ([]GroupKey, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.ListKeys(ctx)
}

// Size delegates to current storage.
func (sm *StorageManager) Size(ctx context.Context) (int, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Size(ctx)
}

// LoadAll delegates to current storage.
//
// Important: Typically called on startup before manager is initialized,
// or when explicitly loading from primary storage for recovery.
func (sm *StorageManager) LoadAll(ctx context.Context) ([]*AlertGroup, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.LoadAll(ctx)
}

// StoreAll delegates to current storage with automatic fallback on error.
func (sm *StorageManager) StoreAll(ctx context.Context, groups []*AlertGroup) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.StoreAll(ctx, groups)
	if err != nil {
		// On error, try fallback if we were using primary
		sm.mu.Lock()
		if sm.current == sm.primary {
			sm.logger.Warn("Primary storage StoreAll failed, falling back to memory",
				"count", len(groups),
				"error", err)
			sm.current = sm.fallback

			// Record fallback metric
			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("store_all_error")
				sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
			}
		}
		sm.mu.Unlock()

		// Retry with fallback
		return sm.fallback.StoreAll(ctx, groups)
	}

	return nil
}

// Ping checks current storage health.
//
// Returns the health status of the currently active storage (primary or fallback).
func (sm *StorageManager) Ping(ctx context.Context) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Ping(ctx)
}

// GetCurrentStorage returns which storage is currently active (for monitoring).
//
// Returns:
//   - "primary" if using RedisGroupStorage
//   - "fallback" if using MemoryGroupStorage
func (sm *StorageManager) GetCurrentStorage() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.current == sm.primary {
		return "primary"
	}
	return "fallback"
}
