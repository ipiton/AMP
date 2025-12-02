package routing

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

// RouteTreeManager manages hot reload of routing trees with zero downtime.
//
// Features:
// - Atomic swap of trees (via atomic.Value)
// - Thread-safe concurrent reads during reload
// - Backup mechanism for rollback on error
// - Graceful transition (old requests complete on old tree, new use new tree)
//
// Usage:
//
//	// Initialize with tree
//	manager := routing.NewRouteTreeManager(tree)
//
//	// Hot reload with new config
//	if err := manager.Reload(newConfig); err != nil {
//	    log.Error("reload failed", "error", err)
//	    manager.Rollback()
//	}
//
//	// Get current tree (zero-cost, thread-safe)
//	tree := manager.GetTree()
//
// Thread Safety:
// - GetTree(): Lock-free reads (atomic.Value.Load)
// - Reload(): Serialized writes (sync.RWMutex)
// - Concurrent reads + single writer pattern
type RouteTreeManager struct {
	// current holds the active RouteTree (atomic swap)
	// Use atomic.Value for lock-free reads
	current atomic.Value // *RouteTree

	// mu serializes write operations (Reload, Rollback)
	// Reads (GetTree) do not acquire lock
	mu sync.RWMutex

	// backup stores previous tree for rollback
	// Only valid after Reload() or Rollback()
	backup *RouteTree

	// stats tracks reload history
	stats ManagerStats
}

// ManagerStats tracks RouteTreeManager statistics.
type ManagerStats struct {
	// ReloadCount is the total number of successful reloads
	ReloadCount int

	// RollbackCount is the total number of rollbacks
	RollbackCount int

	// FailedReloadCount is the number of failed reload attempts
	FailedReloadCount int

	// LastReloadError is the most recent reload error (if any)
	LastReloadError string
}

// NewRouteTreeManager creates a new manager with the given initial tree.
//
// The tree must not be nil.
//
// Returns an error if tree is nil.
func NewRouteTreeManager(tree *RouteTree) (*RouteTreeManager, error) {
	if tree == nil {
		return nil, fmt.Errorf("initial tree cannot be nil")
	}

	manager := &RouteTreeManager{
		backup: nil,
		stats:  ManagerStats{},
	}

	// Store initial tree
	manager.current.Store(tree)

	slog.Info("route tree manager initialized",
		"nodes", tree.stats.NodeCount,
		"depth", tree.stats.MaxDepth,
		"receivers", tree.stats.ReceiverCount)

	return manager, nil
}

// GetTree returns the current active RouteTree.
//
// This is a lock-free operation (uses atomic.Value.Load).
// Safe for concurrent access by unlimited readers.
//
// Complexity: O(1)
//
// Returns:
// - Current active tree (never nil)
func (m *RouteTreeManager) GetTree() *RouteTree {
	return m.current.Load().(*RouteTree)
}

// Reload replaces the current tree with a new tree built from config.
//
// Process:
// 1. Acquire write lock (serializes concurrent Reload calls)
// 2. Backup current tree (for rollback)
// 3. Build new tree from config
// 4. Validate new tree
// 5. Atomic swap: current → new tree
// 6. Update stats
// 7. Log reload event
//
// Error Handling:
// - If build fails: return error, keep current tree
// - If validation fails: return error, keep current tree
// - On success: atomic swap, backup old tree
//
// Thread Safety:
// - Serialized writes (only one Reload at a time)
// - Concurrent reads continue during reload (on old tree)
// - Atomic swap ensures readers see consistent tree
//
// Complexity: O(N) where N is nodes in new config
//
// Returns:
// - nil on success
// - error if build or validation fails
func (m *RouteTreeManager) Reload(config *RouteConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	slog.Info("reloading route tree", "receivers", len(config.Receivers))

	// Step 1: Backup current tree
	m.backup = m.GetTree()

	// Step 2: Build new tree
	builder := NewTreeBuilder(config, DefaultBuildOptions())
	newTree, err := builder.Build()
	if err != nil {
		m.stats.FailedReloadCount++
		m.stats.LastReloadError = err.Error()

		slog.Error("tree build failed", "error", err)
		return fmt.Errorf("build failed: %w", err)
	}

	// Step 3: Validate new tree
	validationErrors := newTree.Validate()
	if len(validationErrors) > 0 {
		m.stats.FailedReloadCount++
		m.stats.LastReloadError = fmt.Sprintf("%d validation errors", len(validationErrors))

		slog.Error("tree validation failed",
			"errors", len(validationErrors),
			"first_error", validationErrors[0].Message)

		return fmt.Errorf("validation failed: %d errors (first: %s)",
			len(validationErrors), validationErrors[0].Message)
	}

	// Step 4: Atomic swap (zero downtime)
	m.current.Store(newTree)

	// Step 5: Update stats
	m.stats.ReloadCount++
	m.stats.LastReloadError = ""

	slog.Info("tree reloaded successfully",
		"nodes", newTree.stats.NodeCount,
		"depth", newTree.stats.MaxDepth,
		"receivers", newTree.stats.ReceiverCount,
		"reload_count", m.stats.ReloadCount)

	return nil
}

// Rollback reverts to the backup tree (previous version).
//
// Use this when Reload() fails or you need to undo a reload.
//
// Process:
// 1. Acquire write lock
// 2. Check backup exists
// 3. Atomic swap: current → backup
// 4. Update stats
// 5. Log rollback event
//
// Error Handling:
// - Returns error if no backup available
// - Otherwise always succeeds
//
// Thread Safety:
// - Serialized with Reload() (write lock)
// - Atomic swap for readers
//
// Returns:
// - nil on success
// - error if no backup available
func (m *RouteTreeManager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.backup == nil {
		return fmt.Errorf("no backup tree available for rollback")
	}

	// Atomic swap to backup
	m.current.Store(m.backup)

	// Update stats
	m.stats.RollbackCount++

	slog.Warn("tree rolled back to backup",
		"nodes", m.backup.stats.NodeCount,
		"rollback_count", m.stats.RollbackCount)

	return nil
}

// GetStats returns current manager statistics.
//
// Thread-safe (read-only access to stats).
//
// Returns:
// - Copy of current stats
func (m *RouteTreeManager) GetStats() ManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.stats
}

// GetBackup returns the backup tree (if any).
//
// Returns:
// - Backup tree (may be nil if no reload has occurred)
func (m *RouteTreeManager) GetBackup() *RouteTree {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.backup
}

// HasBackup returns true if a backup tree is available.
func (m *RouteTreeManager) HasBackup() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.backup != nil
}

// ClearBackup clears the backup tree (releases memory).
//
// Use this after a successful reload to free old tree memory.
// Rollback will not be possible after clearing backup.
func (m *RouteTreeManager) ClearBackup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backup = nil

	slog.Debug("backup tree cleared")
}

// String returns a human-readable representation of the manager.
//
// Format: "RouteTreeManager{reloads=<count> rollbacks=<count> tree=<tree>}"
func (m *RouteTreeManager) String() string {
	tree := m.GetTree()
	stats := m.GetStats()

	return fmt.Sprintf(
		"RouteTreeManager{reloads=%d rollbacks=%d failed=%d tree=%s}",
		stats.ReloadCount,
		stats.RollbackCount,
		stats.FailedReloadCount,
		tree.String(),
	)
}
