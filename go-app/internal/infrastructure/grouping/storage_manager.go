// Package grouping provides automatic fallback/recovery coordination for group storage.
//
// StorageManager coordinates between RedisGroupStorage (primary) and MemoryGroupStorage (fallback),
// providing seamless automatic switching when Redis becomes unhealthy or recovers.
//
// Features:
//   - Automatic fallback to MemoryGroupStorage on Redis failure
//   - Automatic recovery to RedisGroupStorage when Redis restored
//   - Health check polling every 30s
//   - Metrics for fallback/recovery events
//   - Graceful shutdown
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
package grouping

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/metrics"
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
//	manager := grouping.NewStorageManager(primary, fallback, logger, metrics)
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

	// mu protects current field
	mu sync.RWMutex

	// logger for structured logging
	logger *slog.Logger

	// metrics for observability
	metrics *metrics.BusinessMetrics

	// healthTicker for periodic health checks
	healthTicker *time.Ticker

	// stopChan signals health check goroutine to stop
	stopChan chan struct{}

	// stopped indicates if manager has been stopped
	stopped bool
}

// NewStorageManager creates a new StorageManager with automatic fallback/recovery.
//
// Parameters:
//   - primary: Primary storage (typically RedisGroupStorage)
//   - fallback: Fallback storage (typically MemoryGroupStorage)
//   - logger: Structured logger (optional, defaults to slog.Default)
//   - metrics: Business metrics for fallback/recovery tracking (optional)
//
// Returns:
//   - *StorageManager: Initialized manager with health check running
//
// The health check goroutine starts immediately and polls every 30s.
// Call Stop() to gracefully shutdown when done.
//
// Example:
//
//	manager := grouping.NewStorageManager(redisStorage, memoryStorage, logger, metrics)
//	defer manager.Stop()
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
func NewStorageManager(
	primary GroupStorage,
	fallback GroupStorage,
	logger *slog.Logger,
	metrics *metrics.BusinessMetrics,
) *StorageManager {
	if logger == nil {
		logger = slog.Default()
	}

	sm := &StorageManager{
		primary:  primary,
		fallback: fallback,
		current:  primary, // Start with primary (Redis)
		logger:   logger,
		metrics:  metrics,
		stopChan: make(chan struct{}),
	}

	// Start health check poller
	sm.startHealthCheck()

	logger.Info("Initialized storage manager with automatic fallback/recovery",
		"initial_storage", "primary")

	return sm
}

// startHealthCheck polls primary storage health every 30s and switches storage accordingly.
//
// Goroutine lifecycle:
//   - Starts immediately in NewStorageManager
//   - Polls every 30s
//   - Stops when Stop() is called
//
// Behavior:
//   - On primary.Ping() error: switch to fallback (if not already)
//   - On primary.Ping() success: switch back to primary (if was fallback)
//   - Records metrics for fallback/recovery events
func (sm *StorageManager) startHealthCheck() {
	sm.healthTicker = time.NewTicker(30 * time.Second)

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

	sm.logger.Debug("Started health check poller", "interval", "30s")
}

// checkHealthAndSwitch checks primary storage health and switches if needed.
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
				"error", err)
			sm.current = sm.fallback

			// Record fallback metric
			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("health_check_failed")
			}
		}
	} else {
		// Primary healthy → switch back to primary
		if sm.current == sm.fallback {
			sm.logger.Info("Primary storage recovered, switching back from fallback")
			sm.current = sm.primary

			// Record recovery metric
			if sm.metrics != nil {
				sm.metrics.IncStorageRecovery()
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
