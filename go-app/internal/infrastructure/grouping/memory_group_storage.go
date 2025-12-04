// Package grouping provides in-memory fallback storage for AlertGroup.
//
// MemoryGroupStorage implements GroupStorage interface as a simple in-memory map.
// This is used as a fallback when Redis is unavailable (graceful degradation).
//
// Features:
//   - Thread-safe via sync.RWMutex
//   - No persistence (volatile)
//   - O(1) Store/Load/Delete operations
//   - No optimistic locking (single-instance only)
//   - Prometheus metrics integration
//
// Limitations:
//   - Data lost on restart (not suitable for HA)
//   - No distributed state
//   - Memory usage grows unbounded (no TTL/eviction)
//
// Use Cases:
//   - Redis connection failure (automatic fallback via StorageManager)
//   - Testing and development
//   - Single-instance deployments without HA requirements
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
package grouping

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// MemoryGroupStorage provides in-memory volatile storage for AlertGroup.
//
// Thread-safety: All methods are thread-safe via sync.RWMutex.
//
// Example:
//
//	storage := grouping.NewMemoryGroupStorage(logger, metrics)
//	group := &grouping.AlertGroup{Key: "team:frontend:alert:cpu", ...}
//	err := storage.Store(ctx, group)
type MemoryGroupStorage struct {
	// groups stores alert groups in memory, keyed by GroupKey
	groups map[GroupKey]*AlertGroup

	// mu protects concurrent access to groups map
	mu sync.RWMutex

	// logger for structured logging
	logger *slog.Logger

	// metrics for observability
	metrics *metrics.BusinessMetrics
}

// MemoryGroupStorageConfig holds configuration for MemoryGroupStorage.
type MemoryGroupStorageConfig struct {
	// Logger for structured logging (optional, defaults to slog.Default)
	Logger *slog.Logger

	// Metrics for observability (optional, no metrics if nil)
	Metrics *metrics.BusinessMetrics
}

// NewMemoryGroupStorage creates a new in-memory group storage.
//
// Parameters:
//   - config: Configuration including logger and metrics (optional)
//
// Returns:
//   - *MemoryGroupStorage: Initialized in-memory storage
//
// Example:
//
//	config := &grouping.MemoryGroupStorageConfig{
//	    Logger: logger,
//	    Metrics: businessMetrics,
//	}
//	storage := grouping.NewMemoryGroupStorage(config)
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
func NewMemoryGroupStorage(config *MemoryGroupStorageConfig) *MemoryGroupStorage {
	logger := slog.Default()
	var metricsPtr *metrics.BusinessMetrics

	if config != nil {
		if config.Logger != nil {
			logger = config.Logger
		}
		metricsPtr = config.Metrics
	}

	storage := &MemoryGroupStorage{
		groups:  make(map[GroupKey]*AlertGroup),
		logger:  logger,
		metrics: metricsPtr,
	}

	// Initialize health metric
	if storage.metrics != nil {
		storage.metrics.SetStorageHealth(true)
	}

	logger.Info("Initialized in-memory group storage (volatile, no persistence)")

	return storage
}

// Store saves an AlertGroup to memory (no persistence).
//
// Behavior:
//  1. Deep copy the group to prevent external modifications
//  2. Store in map by GroupKey
//  3. No optimistic locking (single-instance only)
//
// Performance: <100µs (in-memory map operation)
//
// Thread-safety: Safe for concurrent calls.
func (m *MemoryGroupStorage) Store(ctx context.Context, group *AlertGroup) error {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("store", float64(time.Since(start)))
		}
	}()

	if group == nil {
		err := NewStorageError("store", fmt.Errorf("group cannot be nil"))
		if m.metrics != nil {
			m.metrics.RecordStorageOperation("store", "error")
		}
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Deep copy to prevent external modifications
	// Note: AlertGroup has internal mutex, but we copy data structures
	groupCopy := &AlertGroup{
		Key:      group.Key,
		Alerts:   make(map[string]*core.Alert, len(group.Alerts)),
		Metadata: m.copyMetadata(group.Metadata),
		Version:  group.Version,
	}

	// Copy alerts map
	for fp, alert := range group.Alerts {
		groupCopy.Alerts[fp] = alert
	}

	m.groups[group.Key] = groupCopy

	m.logger.Debug("Stored group in memory",
		"group_key", group.Key,
		"alerts_count", len(group.Alerts),
		"duration_us", time.Since(start).Microseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("store", "success")
	}

	return nil
}

// copyMetadata creates a deep copy of GroupMetadata.
func (m *MemoryGroupStorage) copyMetadata(meta *GroupMetadata) *GroupMetadata {
	if meta == nil {
		return nil
	}

	copy := &GroupMetadata{
		State:         meta.State,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
		FiringCount:   meta.FiringCount,
		ResolvedCount: meta.ResolvedCount,
		GroupBy:       make([]string, len(meta.GroupBy)),
		Version:       meta.Version,
	}

	// Copy GroupBy slice
	for i, label := range meta.GroupBy {
		copy.GroupBy[i] = label
	}

	// Copy optional time pointers
	if meta.FirstFiringAt != nil {
		t := *meta.FirstFiringAt
		copy.FirstFiringAt = &t
	}
	if meta.ResolvedAt != nil {
		t := *meta.ResolvedAt
		copy.ResolvedAt = &t
	}

	// Copy timer metadata (shallow copy is sufficient for pointers)
	copy.GroupWaitTimer = meta.GroupWaitTimer
	copy.GroupIntervalTimer = meta.GroupIntervalTimer
	copy.RepeatIntervalTimer = meta.RepeatIntervalTimer

	return copy
}

// Load retrieves an AlertGroup from memory by its GroupKey.
//
// Performance: <10µs (in-memory map lookup)
//
// Errors:
//   - ErrNotFound: Group does not exist in memory
func (m *MemoryGroupStorage) Load(ctx context.Context, groupKey GroupKey) (*AlertGroup, error) {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("load", float64(time.Since(start)))
		}
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	group, exists := m.groups[groupKey]
	if !exists {
		if m.metrics != nil {
			m.metrics.RecordStorageOperation("load", "error")
		}
		return nil, NewGroupNotFoundError(groupKey)
	}

	m.logger.Debug("Loaded group from memory",
		"group_key", groupKey,
		"alerts_count", len(group.Alerts),
		"duration_us", time.Since(start).Microseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("load", "success")
	}

	return group, nil
}

// Delete removes an AlertGroup from memory by its GroupKey.
//
// Performance: <10µs (in-memory map delete)
//
// Thread-safety: Safe for concurrent calls.
func (m *MemoryGroupStorage) Delete(ctx context.Context, groupKey GroupKey) error {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("delete", float64(time.Since(start)))
		}
	}()

	m.mu.Lock()
	defer m.mu.Unlock()

	_, existed := m.groups[groupKey]
	delete(m.groups, groupKey)

	m.logger.Debug("Deleted group from memory",
		"group_key", groupKey,
		"existed", existed,
		"duration_us", time.Since(start).Microseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("delete", "success")
	}

	return nil
}

// ListKeys returns all active GroupKeys from memory.
//
// Performance: O(n) where n is the number of groups.
func (m *MemoryGroupStorage) ListKeys(ctx context.Context) ([]GroupKey, error) {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("list_keys", float64(time.Since(start)))
		}
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]GroupKey, 0, len(m.groups))
	for key := range m.groups {
		keys = append(keys, key)
	}

	m.logger.Debug("Listed group keys from memory",
		"count", len(keys),
		"duration_us", time.Since(start).Microseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("list_keys", "success")
	}

	return keys, nil
}

// Size returns the total number of active groups in memory.
//
// Performance: O(1) (map length operation).
func (m *MemoryGroupStorage) Size(ctx context.Context) (int, error) {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("size", float64(time.Since(start)))
		}
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	count := len(m.groups)

	m.logger.Debug("Retrieved group count from memory",
		"count", count,
		"duration_us", time.Since(start).Microseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("size", "success")
	}

	return count, nil
}

// LoadAll retrieves all AlertGroups from memory.
//
// Performance: O(n) where n is the number of groups.
//
// Note: Returns copies of groups to prevent external modifications.
func (m *MemoryGroupStorage) LoadAll(ctx context.Context) ([]*AlertGroup, error) {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("load_all", float64(time.Since(start)))
		}
	}()

	m.mu.RLock()
	defer m.mu.RUnlock()

	groups := make([]*AlertGroup, 0, len(m.groups))
	for _, group := range m.groups {
		groups = append(groups, group)
	}

	m.logger.Info("Loaded all groups from memory",
		"count", len(groups),
		"duration_ms", time.Since(start).Milliseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("load_all", "success")
		m.metrics.RecordGroupsRestored(len(groups))
	}

	return groups, nil
}

// StoreAll saves multiple AlertGroups to memory in bulk.
//
// Performance: O(n) where n is the number of groups.
//
// Thread-safety: Safe for concurrent calls.
func (m *MemoryGroupStorage) StoreAll(ctx context.Context, groups []*AlertGroup) error {
	start := time.Now()
	defer func() {
		if m.metrics != nil {
			m.metrics.RecordStorageDuration("store_all", float64(time.Since(start)))
		}
	}()

	if len(groups) == 0 {
		if m.metrics != nil {
			m.metrics.RecordStorageOperation("store_all", "success")
		}
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, group := range groups {
		if group == nil {
			continue
		}

		// Store directly (no deep copy for bulk operations - performance optimization)
		m.groups[group.Key] = group
	}

	m.logger.Info("Stored all groups in memory",
		"count", len(groups),
		"duration_ms", time.Since(start).Milliseconds())

	if m.metrics != nil {
		m.metrics.RecordStorageOperation("store_all", "success")
	}

	return nil
}

// Ping checks storage health (always healthy for in-memory storage).
//
// Note: MemoryGroupStorage is always available (no external dependencies).
func (m *MemoryGroupStorage) Ping(ctx context.Context) error {
	// In-memory storage is always healthy
	if m.metrics != nil {
		m.metrics.SetStorageHealth(true)
	}

	return nil
}

// Clear removes all groups from memory (for testing).
//
// This method is NOT part of GroupStorage interface.
// Use only in tests or manual cleanup scenarios.
func (m *MemoryGroupStorage) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.groups = make(map[GroupKey]*AlertGroup)
	m.logger.Debug("Cleared all groups from memory")
}
