package grouping

import (
	"context"
	"fmt"
	"time"
)

// restoreGroupsFromStorage loads all groups from storage on startup (TN-125).
//
// This method is called during initialization to restore distributed state
// after service restart. It rebuilds the fingerprintIndex from stored groups.
//
// Performance: <500ms for 10,000 groups (parallel loading via storage.LoadAll)
//
// Thread-safety: Called during initialization (before manager is returned),
// so no concurrent access possible.
func (m *DefaultGroupManager) restoreGroupsFromStorage(ctx context.Context) error {
	start := time.Now()
	m.logger.Info("Restoring groups from storage...")

	groups, err := m.storage.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load all groups: %w", err)
	}

	// Rebuild fingerprint index
	m.mu.Lock()
	defer m.mu.Unlock()

	fingerprintCount := 0
	for _, group := range groups {
		for fingerprint := range group.Alerts {
			m.fingerprintIndex[fingerprint] = group.Key
			fingerprintCount++
		}
	}

	m.logger.Info("Restored groups from storage",
		"groups_count", len(groups),
		"fingerprints_count", fingerprintCount,
		"duration_ms", time.Since(start).Milliseconds())

	if m.metrics != nil {
		m.metrics.RecordGroupsRestored(len(groups))
	}

	return nil
}
