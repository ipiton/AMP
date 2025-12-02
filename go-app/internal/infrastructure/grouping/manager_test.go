package grouping

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// Test helpers

func createTestAlert(name string, status core.AlertStatus, labels map[string]string) *core.Alert {
	now := time.Now()
	return &core.Alert{
		Fingerprint: "fp_" + name,
		AlertName:   name,
		Status:      status,
		Labels:      labels,
		Annotations: map[string]string{},
		StartsAt:    now,
	}
}

func createTestManager(t *testing.T) *DefaultGroupManager {
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver: "default",
			GroupBy:  []string{"alertname"},
		},
	}

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
	})
	require.NoError(t, err)
	return manager
}

// === Constructor Tests ===

func TestNewDefaultGroupManager(t *testing.T) {
	tests := []struct {
		name    string
		config  DefaultGroupManagerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: DefaultGroupManagerConfig{
				KeyGenerator: NewGroupKeyGenerator(),
				Config: &GroupingConfig{
					Route: &Route{
						Receiver: "default",
						GroupBy:  []string{"alertname"},
					},
				},
				Logger:  slog.Default(),
				Storage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
			},
			wantErr: false,
		},
		{
			name: "missing key generator",
			config: DefaultGroupManagerConfig{
				Config: &GroupingConfig{
					Route: &Route{},
				},
				Storage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
			},
			wantErr: true,
		},
		{
			name: "missing config",
			config: DefaultGroupManagerConfig{
				KeyGenerator: NewGroupKeyGenerator(),
				Storage:      NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewDefaultGroupManager(context.Background(), tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, manager)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, manager)
			}
		})
	}
}

// === AddAlertToGroup Tests ===

func TestAddAlertToGroup_NewGroup(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{
		"alertname": "HighCPU",
		"namespace": "prod",
	})
	groupKey := GroupKey("alertname=HighCPU")

	// Add alert to new group
	group, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)
	require.NotNil(t, group)

	// Verify group created
	assert.Equal(t, groupKey, group.Key)
	assert.Equal(t, 1, group.Size())
	assert.Equal(t, alert, group.Alerts[alert.Fingerprint])
	assert.Equal(t, GroupStateFiring, group.Metadata.State)
	assert.Equal(t, 1, group.Metadata.FiringCount)
	assert.Equal(t, 0, group.Metadata.ResolvedCount)
}

func TestAddAlertToGroup_ExistingGroup(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")

	// Add first alert
	alert1 := createTestAlert("HighCPU-1", core.StatusFiring, map[string]string{
		"alertname": "HighCPU",
	})
	_, err := manager.AddAlertToGroup(ctx, alert1, groupKey)
	require.NoError(t, err)

	// Add second alert to same group
	alert2 := createTestAlert("HighCPU-2", core.StatusFiring, map[string]string{
		"alertname": "HighCPU",
	})
	group, err := manager.AddAlertToGroup(ctx, alert2, groupKey)
	require.NoError(t, err)

	// Verify both alerts in group
	assert.Equal(t, 2, group.Size())
	assert.Equal(t, 2, group.Metadata.FiringCount)
	assert.Contains(t, group.Alerts, alert1.Fingerprint)
	assert.Contains(t, group.Alerts, alert2.Fingerprint)
}

func TestAddAlertToGroup_UpdateExisting(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")

	// Add firing alert
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{
		"alertname": "HighCPU",
	})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	// Update alert to resolved
	alertResolved := createTestAlert("HighCPU", core.StatusResolved, map[string]string{
		"alertname": "HighCPU",
	})
	group, err := manager.AddAlertToGroup(ctx, alertResolved, groupKey)
	require.NoError(t, err)

	// Verify alert updated
	assert.Equal(t, 1, group.Size())
	assert.Equal(t, core.StatusResolved, group.Alerts[alert.Fingerprint].Status)
	assert.Equal(t, GroupStateResolved, group.Metadata.State)
	assert.Equal(t, 0, group.Metadata.FiringCount)
	assert.Equal(t, 1, group.Metadata.ResolvedCount)
}

func TestAddAlertToGroup_NilAlert(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	_, err := manager.AddAlertToGroup(ctx, nil, "test")
	require.Error(t, err)
	assert.IsType(t, &InvalidAlertError{}, err)
}

func TestAddAlertToGroup_EmptyFingerprint(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	alert := &core.Alert{
		Fingerprint: "", // Empty fingerprint
		AlertName:   "Test",
		Status:      core.StatusFiring,
	}

	_, err := manager.AddAlertToGroup(ctx, alert, "test")
	require.Error(t, err)
	assert.IsType(t, &InvalidAlertError{}, err)
}

func TestAddAlertToGroup_ContextCancellation(t *testing.T) {
	manager := createTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	alert := createTestAlert("Test", core.StatusFiring, map[string]string{})

	_, err := manager.AddAlertToGroup(ctx, alert, "test")
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// === RemoveAlertFromGroup Tests ===

func TestRemoveAlertFromGroup_Success(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")

	// Add two alerts
	alert1 := createTestAlert("HighCPU-1", core.StatusFiring, map[string]string{})
	alert2 := createTestAlert("HighCPU-2", core.StatusFiring, map[string]string{})

	manager.AddAlertToGroup(ctx, alert1, groupKey)
	manager.AddAlertToGroup(ctx, alert2, groupKey)

	// Remove first alert
	removed, err := manager.RemoveAlertFromGroup(ctx, alert1.Fingerprint, groupKey)
	require.NoError(t, err)
	assert.True(t, removed)

	// Verify group still exists with one alert
	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, 1, group.Size())
	assert.NotContains(t, group.Alerts, alert1.Fingerprint)
	assert.Contains(t, group.Alerts, alert2.Fingerprint)
}

func TestRemoveAlertFromGroup_DeletesEmptyGroup(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")

	// Add one alert
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Remove alert
	removed, err := manager.RemoveAlertFromGroup(ctx, alert.Fingerprint, groupKey)
	require.NoError(t, err)
	assert.True(t, removed)

	// Verify group deleted
	_, err = manager.GetGroup(ctx, groupKey)
	assert.Error(t, err)
	assert.IsType(t, &GroupNotFoundError{}, err)
}

func TestRemoveAlertFromGroup_NotFound(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")

	// Add alert
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Try to remove non-existent alert
	removed, err := manager.RemoveAlertFromGroup(ctx, "nonexistent", groupKey)
	require.NoError(t, err)
	assert.False(t, removed)

	// Verify group still exists
	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, 1, group.Size())
}

func TestRemoveAlertFromGroup_GroupNotFound(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	_, err := manager.RemoveAlertFromGroup(ctx, "fp_test", GroupKey("nonexistent"))
	require.Error(t, err)
	assert.IsType(t, &GroupNotFoundError{}, err)
}

// === GetGroup Tests ===

func TestGetGroup_Success(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})

	// Add alert
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Get group
	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, groupKey, group.Key)
	assert.Equal(t, 1, group.Size())
}

func TestGetGroup_NotFound(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	_, err := manager.GetGroup(ctx, GroupKey("nonexistent"))
	require.Error(t, err)
	assert.IsType(t, &GroupNotFoundError{}, err)
}

func TestGetGroup_ReturnsCopy(t *testing.T) {
	// 150% Enhancement: Verify that GetGroup returns a copy
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})

	// Add alert
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Get group twice
	group1, err1 := manager.GetGroup(ctx, groupKey)
	group2, err2 := manager.GetGroup(ctx, groupKey)

	require.NoError(t, err1)
	require.NoError(t, err2)

	// Verify different instances (shallow copy) - use pointer comparison
	assert.False(t, group1 == group2, "groups should be different instances")

	// Verify contents are the same
	assert.Equal(t, group1.Key, group2.Key)
	assert.Equal(t, group1.Size(), group2.Size())

	// But same alert pointers (shallow copy)
	for fp := range group1.Alerts {
		assert.Same(t, group1.Alerts[fp], group2.Alerts[fp])
	}
}

// === ListGroups Tests ===

func TestListGroups_Empty(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groups, err := manager.ListGroups(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestListGroups_MultipleGroups(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	// Add alerts to different groups
	alert1 := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})
	alert2 := createTestAlert("DiskFull", core.StatusFiring, map[string]string{})

	manager.AddAlertToGroup(ctx, alert1, GroupKey("alertname=HighCPU"))
	manager.AddAlertToGroup(ctx, alert2, GroupKey("alertname=DiskFull"))

	// List all groups
	groups, err := manager.ListGroups(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, len(groups))
}

func TestListGroups_WithStateFilter(t *testing.T) {
	// 150% Enhancement: Test filtering by state
	manager := createTestManager(t)
	ctx := context.Background()

	// Add firing and resolved groups
	firingAlert := createTestAlert("Firing", core.StatusFiring, map[string]string{})
	resolvedAlert := createTestAlert("Resolved", core.StatusResolved, map[string]string{})

	manager.AddAlertToGroup(ctx, firingAlert, GroupKey("alertname=Firing"))
	manager.AddAlertToGroup(ctx, resolvedAlert, GroupKey("alertname=Resolved"))

	// Filter for firing groups
	firingState := GroupStateFiring
	filters := &GroupFilters{
		State: &firingState,
	}

	groups, err := manager.ListGroups(ctx, filters)
	require.NoError(t, err)
	assert.Equal(t, 1, len(groups))
	assert.Equal(t, GroupStateFiring, groups[0].Metadata.State)
}

func TestListGroups_WithPagination(t *testing.T) {
	// 150% Enhancement: Test pagination
	manager := createTestManager(t)
	ctx := context.Background()

	// Add 5 groups
	for i := 0; i < 5; i++ {
		alert := createTestAlert("Alert"+string(rune(i)), core.StatusFiring, map[string]string{})
		manager.AddAlertToGroup(ctx, alert, GroupKey("group_"+string(rune(i))))
	}

	// Get first page (limit 2)
	filters := &GroupFilters{
		Limit: 2,
	}

	groups, err := manager.ListGroups(ctx, filters)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(groups), 2)

	// Get second page (offset 2, limit 2)
	filters = &GroupFilters{
		Offset: 2,
		Limit:  2,
	}

	groups, err = manager.ListGroups(ctx, filters)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(groups), 2)
}

// === GetGroupByFingerprint Tests ===

func TestGetGroupByFingerprint_Success(t *testing.T) {
	// 150% Enhancement: Reverse lookup test
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=HighCPU")
	alert := createTestAlert("HighCPU", core.StatusFiring, map[string]string{})

	// Add alert
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Find group by fingerprint
	foundKey, group, err := manager.GetGroupByFingerprint(ctx, alert.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, groupKey, foundKey)
	assert.Equal(t, 1, group.Size())
}

func TestGetGroupByFingerprint_NotFound(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	_, _, err := manager.GetGroupByFingerprint(ctx, "nonexistent")
	require.Error(t, err)
	assert.IsType(t, &GroupNotFoundError{}, err)
}

// === CleanupExpiredGroups Tests ===

func TestCleanupExpiredGroups_ExpiredByResolvedTime(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=Expired")
	alert := createTestAlert("Expired", core.StatusResolved, map[string]string{})

	// Add resolved alert
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Manually set resolved time to 2 hours ago (TN-125: use storage)
	group, _ := manager.storage.Load(ctx, groupKey)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	group.Metadata.ResolvedAt = &twoHoursAgo
	manager.storage.Store(ctx, group)

	// Cleanup with maxAge=1 hour
	deleted, err := manager.CleanupExpiredGroups(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	// Verify group deleted
	_, err = manager.GetGroup(ctx, groupKey)
	assert.Error(t, err)
}

func TestCleanupExpiredGroups_ExpiredByUpdateTime(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=Stale")
	alert := createTestAlert("Stale", core.StatusFiring, map[string]string{})

	// Add alert
	manager.AddAlertToGroup(ctx, alert, groupKey)

	// Manually set updated time to 2 hours ago (TN-125: use storage)
	group, _ := manager.storage.Load(ctx, groupKey)
	group.Metadata.UpdatedAt = time.Now().Add(-2 * time.Hour)
	manager.storage.Store(ctx, group)

	// Cleanup with maxAge=1 hour
	deleted, err := manager.CleanupExpiredGroups(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
}

func TestCleanupExpiredGroups_NoExpiredGroups(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	// Add fresh group
	alert := createTestAlert("Fresh", core.StatusFiring, map[string]string{})
	manager.AddAlertToGroup(ctx, alert, GroupKey("alertname=Fresh"))

	// Cleanup with maxAge=1 hour
	deleted, err := manager.CleanupExpiredGroups(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)

	// Verify group still exists
	_, err = manager.GetGroup(ctx, GroupKey("alertname=Fresh"))
	require.NoError(t, err)
}

// === UpdateGroupState Tests ===

func TestUpdateGroupState_AllFiring(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=Test")

	// Add firing alerts
	alert1 := createTestAlert("Test-1", core.StatusFiring, map[string]string{})
	alert2 := createTestAlert("Test-2", core.StatusFiring, map[string]string{})

	manager.AddAlertToGroup(ctx, alert1, groupKey)
	manager.AddAlertToGroup(ctx, alert2, groupKey)

	// Update state
	group, err := manager.UpdateGroupState(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, GroupStateFiring, group.Metadata.State)
	assert.Equal(t, 2, group.Metadata.FiringCount)
	assert.Equal(t, 0, group.Metadata.ResolvedCount)
}

func TestUpdateGroupState_AllResolved(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=Test")

	// Add resolved alerts
	alert1 := createTestAlert("Test-1", core.StatusResolved, map[string]string{})
	alert2 := createTestAlert("Test-2", core.StatusResolved, map[string]string{})

	manager.AddAlertToGroup(ctx, alert1, groupKey)
	manager.AddAlertToGroup(ctx, alert2, groupKey)

	// Update state
	group, err := manager.UpdateGroupState(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, GroupStateResolved, group.Metadata.State)
	assert.Equal(t, 0, group.Metadata.FiringCount)
	assert.Equal(t, 2, group.Metadata.ResolvedCount)
}

func TestUpdateGroupState_Mixed(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	groupKey := GroupKey("alertname=Test")

	// Add firing and resolved alerts
	firingAlert := createTestAlert("Firing", core.StatusFiring, map[string]string{})
	resolvedAlert := createTestAlert("Resolved", core.StatusResolved, map[string]string{})

	manager.AddAlertToGroup(ctx, firingAlert, groupKey)
	manager.AddAlertToGroup(ctx, resolvedAlert, groupKey)

	// Update state
	group, err := manager.UpdateGroupState(ctx, groupKey)
	require.NoError(t, err)
	assert.Equal(t, GroupStateMixed, group.Metadata.State)
	assert.Equal(t, 1, group.Metadata.FiringCount)
	assert.Equal(t, 1, group.Metadata.ResolvedCount)
}

// === GetMetrics Tests ===

func TestGetMetrics_Empty(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	metrics, err := manager.GetMetrics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, metrics.ActiveGroups)
	assert.Empty(t, metrics.AlertsPerGroup)
}

func TestGetMetrics_WithGroups(t *testing.T) {
	manager := createTestManager(t)
	ctx := context.Background()

	// Add groups with different sizes
	for i := 0; i < 3; i++ {
		alert := createTestAlert("Alert"+string(rune(i)), core.StatusFiring, map[string]string{})
		manager.AddAlertToGroup(ctx, alert, GroupKey("group_"+string(rune(i))))
	}

	metrics, err := manager.GetMetrics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, metrics.ActiveGroups)
	assert.Equal(t, 3, len(metrics.AlertsPerGroup))
}

// === GetStats Tests ===

func TestGetStats_WithOperations(t *testing.T) {
	// 150% Enhancement: Test extended statistics
	manager := createTestManager(t)
	ctx := context.Background()

	// Perform operations
	alert := createTestAlert("Test", core.StatusFiring, map[string]string{})
	groupKey := GroupKey("alertname=Test")

	manager.AddAlertToGroup(ctx, alert, groupKey)
	manager.RemoveAlertFromGroup(ctx, alert.Fingerprint, groupKey)

	// Get stats
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalAdds)
	assert.Equal(t, int64(1), stats.TotalRemoves)
	assert.Equal(t, 0, stats.ActiveGroups)
}
