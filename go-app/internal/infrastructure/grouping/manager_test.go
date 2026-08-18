package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

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

	_, _ = manager.AddAlertToGroup(ctx, alert1, GroupKey("alertname=HighCPU"))
	_, _ = manager.AddAlertToGroup(ctx, alert2, GroupKey("alertname=DiskFull"))

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

	_, _ = manager.AddAlertToGroup(ctx, firingAlert, GroupKey("alertname=Firing"))
	_, _ = manager.AddAlertToGroup(ctx, resolvedAlert, GroupKey("alertname=Resolved"))

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
		_, _ = manager.AddAlertToGroup(ctx, alert, GroupKey("group_"+string(rune(i))))
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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	// Manually set resolved time to 2 hours ago (TN-125: use storage)
	group, _ := manager.storage.Load(ctx, groupKey)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	group.Metadata.ResolvedAt = &twoHoursAgo
	_ = manager.storage.Store(ctx, group)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	// Manually set updated time to 2 hours ago (TN-125: use storage)
	group, _ := manager.storage.Load(ctx, groupKey)
	group.Metadata.UpdatedAt = time.Now().Add(-2 * time.Hour)
	_ = manager.storage.Store(ctx, group)

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
	_, _ = manager.AddAlertToGroup(ctx, alert, GroupKey("alertname=Fresh"))

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

	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

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

	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

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

	_, _ = manager.AddAlertToGroup(ctx, firingAlert, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, resolvedAlert, groupKey)

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
		_, _ = manager.AddAlertToGroup(ctx, alert, GroupKey("group_"+string(rune(i))))
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

	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)
	_, _ = manager.RemoveAlertFromGroup(ctx, alert.Fingerprint, groupKey)

	// Get stats
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalAdds)
	assert.Equal(t, int64(1), stats.TotalRemoves)
	assert.Equal(t, 0, stats.ActiveGroups)
}

// === Notification Triggering Tests (parity-a1-notification-triggering) ===

// mockPublisherTarget is the single synthetic target name mockPublisher
// reports outcomes for (task fwb) — it stands in for "the one downstream
// receiver endpoint" the way manager-level tests have always modeled
// GroupNotificationPublisher, without needing to know about real
// core.PublishingTarget fan-out (that lives below ApplicationPublishingAdapter/
// PublishingCoordinator, a different package these tests don't exercise).
const mockPublisherTarget = "mock"

// mockPublisher records PublishGroup calls for assertion in tests (task
// 2.4: GroupNotificationPublisher is a batch interface — one call per group
// notification, carrying every alert in that notification, not one call per
// alert). published has one entry per actual send (skipped calls per
// mockPublisherTarget's per-target dedup, task fwb, are not appended);
// receivers has the matching receiver argument for each entry. Thread-safe:
// timer callbacks run on their own goroutine.
type mockPublisher struct {
	mu          sync.Mutex
	published   [][]*core.Alert
	receivers   []string
	groupLabels []map[string]string
	err         error
}

// PublishGroup implements grouping.GroupNotificationPublisher. When err is
// set it always fails the whole call (mirrors a total/degraded-mode
// failure, e.g. metrics-only mode or every target down). Otherwise it
// consults skipTarget for its one synthetic target (task fwb's per-target
// nflog dedup, exercised the same way a real multi-target publisher would
// be) — a skip means this fire is fully deduped and nothing is appended or
// reported. groupLabels (review finding 1) is recorded per call so tests
// can assert the manager resolved it correctly from GroupMetadata.GroupBy.
func (p *mockPublisher) PublishGroup(_ context.Context, _ string, alerts []*core.Alert, receiver string, groupLabels map[string]string, skipTarget func(string) bool) ([]TargetPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		// Record the attempt even though it fails — calls() measures publish
		// ATTEMPTS (matching this mock's pre-task-fwb behavior), not confirmed
		// deliveries; tests rely on seeing a failed attempt counted so they can
		// tell "retried" apart from "wrongly deduped".
		p.published = append(p.published, alerts)
		p.receivers = append(p.receivers, receiver)
		p.groupLabels = append(p.groupLabels, groupLabels)
		return nil, p.err
	}
	if skipTarget != nil && skipTarget(mockPublisherTarget) {
		// Fully deduped: no real attempt was made against the (one synthetic)
		// target, so nothing is recorded — mirrors a real publisher that
		// skips a target's HTTP call entirely because it already delivered
		// this cycle.
		return nil, nil
	}
	p.published = append(p.published, alerts)
	p.receivers = append(p.receivers, receiver)
	p.groupLabels = append(p.groupLabels, groupLabels)
	return []TargetPublishOutcome{{Target: mockPublisherTarget, Success: true}}, nil
}

func (p *mockPublisher) calls() [][]*core.Alert {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]*core.Alert, len(p.published))
	copy(out, p.published)
	return out
}

// lastGroupLabels returns the groupLabels argument from the most recent
// PublishGroup call (review finding 1), or nil if PublishGroup was never
// called.
func (p *mockPublisher) lastGroupLabels() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.groupLabels) == 0 {
		return nil
	}
	return p.groupLabels[len(p.groupLabels)-1]
}

// === groupLabelsFor unit tests (review finding 1, fwb fix round 1) ===

func TestGroupLabelsFor_ResolvesGroupByNamesFromFirstAlert(t *testing.T) {
	group := &AlertGroup{Metadata: &GroupMetadata{GroupBy: []string{"alertname", "cluster"}}}
	alerts := []*core.Alert{
		createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU", "cluster": "prod", "instance": "host-1"}),
	}

	got := groupLabelsFor(group, alerts)
	assert.Equal(t, map[string]string{"alertname": "HighCPU", "cluster": "prod"}, got)
}

func TestGroupLabelsFor_EmptyGroupByYieldsEmptyMap(t *testing.T) {
	group := &AlertGroup{Metadata: &GroupMetadata{GroupBy: nil}}
	alerts := []*core.Alert{createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU"})}

	got := groupLabelsFor(group, alerts)
	assert.Equal(t, map[string]string{}, got)
	assert.NotNil(t, got)
}

func TestGroupLabelsFor_NilGroupOrMetadataYieldsEmptyMap(t *testing.T) {
	alerts := []*core.Alert{createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU"})}

	assert.Equal(t, map[string]string{}, groupLabelsFor(nil, alerts))
	assert.Equal(t, map[string]string{}, groupLabelsFor(&AlertGroup{Metadata: nil}, alerts))
}

func TestGroupLabelsFor_EmptyAlertsYieldsEmptyMap(t *testing.T) {
	group := &AlertGroup{Metadata: &GroupMetadata{GroupBy: []string{"alertname"}}}
	assert.Equal(t, map[string]string{}, groupLabelsFor(group, nil))
}

func TestGroupLabelsFor_MissingLabelOnSourceAlertIsOmitted(t *testing.T) {
	group := &AlertGroup{Metadata: &GroupMetadata{GroupBy: []string{"alertname", "missing"}}}
	alerts := []*core.Alert{createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU"})}

	got := groupLabelsFor(group, alerts)
	assert.Equal(t, map[string]string{"alertname": "HighCPU"}, got)
}

// createTestManagerWithPublisher creates a manager with a publisher and timer manager wired up.
func createTestManagerWithPublisher(t *testing.T, pub GroupNotificationPublisher) (*DefaultGroupManager, *InMemoryTimerStorage) {
	t.Helper()
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{10 * time.Millisecond},
			GroupInterval:  &Duration{10 * time.Millisecond},
			RepeatInterval: &Duration{10 * time.Millisecond},
		},
	}

	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})
	timerStorage := NewInMemoryTimerStorage(nil)

	// Create a stub group manager for the timer manager (required by TimerManagerConfig).
	// The real group manager will be wired after construction.
	stubGroupMgr := &DefaultGroupManager{
		storage:          storage,
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	timerMgr, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:               timerStorage,
		GroupManager:          stubGroupMgr,
		DefaultGroupWait:      10 * time.Millisecond,
		DefaultGroupInterval:  10 * time.Millisecond,
		DefaultRepeatInterval: 10 * time.Millisecond,
		Logger:                slog.Default(),
	})
	require.NoError(t, err)

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      storage,
		TimerManager: timerMgr,
		Publisher:    pub,
	})
	require.NoError(t, err)

	// Wire the real manager into the timer manager so timer callbacks can load groups.
	timerMgr.groupManager = manager

	return manager, timerStorage
}

// createTestManagerWithLongRootTimings is like createTestManagerWithPublisher
// but the grouping config's ROOT Route.* timings are deliberately long
// (1 hour) — used by TestAddAlertToGroup_PerRouteTimingsOverrideRootDefaults
// below to prove a per-call WithGroupTimings override actually takes effect
// (a group_wait firing observed within the test's timeout is only possible
// via the override; the 1-hour root default would never fire in time).
func createTestManagerWithLongRootTimings(t *testing.T, pub GroupNotificationPublisher) *DefaultGroupManager {
	t.Helper()
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{time.Hour},
			GroupInterval:  &Duration{time.Hour},
			RepeatInterval: &Duration{time.Hour},
		},
	}

	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})
	timerStorage := NewInMemoryTimerStorage(nil)

	stubGroupMgr := &DefaultGroupManager{
		storage:          storage,
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	timerMgr, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:               timerStorage,
		GroupManager:          stubGroupMgr,
		DefaultGroupWait:      time.Hour,
		DefaultGroupInterval:  time.Hour,
		DefaultRepeatInterval: time.Hour,
		Logger:                slog.Default(),
	})
	require.NoError(t, err)

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      storage,
		TimerManager: timerMgr,
		Publisher:    pub,
	})
	require.NoError(t, err)

	timerMgr.groupManager = manager

	return manager
}

// TestAddAlertToGroup_PerRouteTimingsOverrideRootDefaults is the red->green
// test for the task 2.3 carry-over gap fixed in task 2.4: without
// WithGroupTimings actually being honored by startGroupWaitTimer, this
// group's group_wait timer would use the grouping config's root Route.*
// default (1 hour, see createTestManagerWithLongRootTimings) and never fire
// within this test's short deadline. Passing WithGroupTimings with a 20ms
// group_wait must make it fire almost immediately instead.
func TestAddAlertToGroup_PerRouteTimingsOverrideRootDefaults(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithLongRootTimings(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})

	_, err := manager.AddAlertToGroup(ctx, alert, groupKey,
		WithGroupTimings(20*time.Millisecond, time.Hour, time.Hour))
	require.NoError(t, err)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(pub.calls()) < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	require.Len(t, pub.calls(), 1,
		"a 20ms per-route group_wait override must fire well within 2s — the 1-hour root default never would")

	// Confirms the override was actually captured on the group, not just
	// coincidentally timed right.
	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	require.NotNil(t, group.Metadata.Timings)
	assert.Equal(t, 20*time.Millisecond, group.Metadata.Timings.GroupWait)
}

// TestPublishGroupAlerts_OneCallCarriesAllAlerts verifies that
// publishGroupAlerts makes exactly ONE PublishGroup call for the whole
// group, carrying every alert in it (task 2.4: one grouped notification
// instead of N single ones — this replaces the pre-2.4 "once per alert"
// PublishToAll loop).
func TestPublishGroupAlerts_OneCallCarriesAllAlerts(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert1 := createTestAlert("A1", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	alert2 := createTestAlert("A2", core.StatusFiring, map[string]string{"alertname": "TestAlert"})

	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	calls := pub.calls()
	require.Len(t, calls, 1, "expected exactly one PublishGroup call for the group")
	assert.Len(t, calls[0], 2, "expected both alerts carried in that one call")
	assert.Equal(t, "default", pub.receivers[0], "receiver should be parsed from the group key prefix")
}

// TestPublishGroupAlerts_NilPublisher verifies that publishGroupAlerts is a no-op
// when no publisher is configured (backwards compatibility).
func TestPublishGroupAlerts_NilPublisher(t *testing.T) {
	manager := createTestManager(t) // no publisher
	ctx := context.Background()

	groupKey := GroupKey("alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	// Must not panic
	require.NotPanics(t, func() {
		manager.publishGroupAlerts(ctx, group)
	})
}

// TestOnGroupWaitExpired_TriggersNotification verifies that when the group_wait timer
// fires the publisher is called and the group_interval timer is scheduled.
func TestOnGroupWaitExpired_TriggersNotification(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	err = manager.onGroupWaitExpired(ctx, groupKey, GroupWaitTimer, group)
	require.NoError(t, err)

	assert.Len(t, pub.calls(), 1, "expected one PublishGroup call on group_wait expiry")
}

// TestOnGroupIntervalExpired_TriggersNotification verifies that when the group_interval
// timer fires the publisher is called and the repeat_interval timer is scheduled.
func TestOnGroupIntervalExpired_TriggersNotification(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("alertname=TestAlert")
	alert := createTestAlert("B", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	err = manager.onGroupIntervalExpired(ctx, groupKey, GroupIntervalTimer, group)
	require.NoError(t, err)

	assert.Len(t, pub.calls(), 1, "expected one PublishGroup call on group_interval expiry")
}

// TestOnRepeatIntervalExpired_TriggersNotification verifies that when the repeat_interval
// timer fires the publisher is called and the repeat_interval timer is restarted.
func TestOnRepeatIntervalExpired_TriggersNotification(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("alertname=TestAlert")
	alert := createTestAlert("C", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	err = manager.onRepeatIntervalExpired(ctx, groupKey, RepeatIntervalTimer, group)
	require.NoError(t, err)

	assert.Len(t, pub.calls(), 1, "expected one PublishGroup call on repeat_interval expiry")
}

// TestOnRepeatIntervalExpired_EmptyGroup verifies that when a group is empty the
// repeat_interval callback is a no-op (no publish, no new timer).
func TestOnRepeatIntervalExpired_EmptyGroup(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	// Non-existent group — storage.Load will return not-found error
	groupKey := GroupKey("nonexistent-group")
	group := &AlertGroup{
		Key:    groupKey,
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	err := manager.onRepeatIntervalExpired(ctx, groupKey, RepeatIntervalTimer, group)
	require.NoError(t, err)

	assert.Empty(t, pub.calls(), "no notification should be published for non-existent group")
}

// TestTimerChain_GroupWaitToRepeatInterval is an integration test that starts
// a group, waits for the group_wait timer to fire, and verifies the chain
// continues into group_interval.
//
// Task 2.4 note: with the notify-chain's Dedup step now active (TTL =
// repeat_interval, same 10ms as group_interval in this config), a SECOND
// firing carrying the EXACT SAME unchanged alert set is correctly
// suppressed — that is the point of Dedup, and matches upstream
// Alertmanager's own DedupStage (a flush with an unchanged alert set within
// repeat_interval does not re-send either). So this test adds a second
// alert before the group_interval timer is expected to fire, changing the
// alert set's signature, to verify the chain still delivers a fresh
// notification when there IS something new — not merely that timers fire.
func TestTimerChain_GroupWaitToRepeatInterval(t *testing.T) {
	pub := &mockPublisher{}
	manager, _ := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=Chain")
	alert := createTestAlert("Chain", core.StatusFiring, map[string]string{"alertname": "Chain"})

	// AddAlertToGroup triggers group_wait timer (configured to 10ms)
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	// Wait for the group_wait notification.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(pub.calls()) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	require.Len(t, pub.calls(), 1, "expected exactly one notification from group_wait (dedup: unchanged alert set)")

	// Add a second alert — changes the alert set's dedup signature, so the
	// next firing (group_interval) must NOT be suppressed by Dedup.
	alert2 := createTestAlert("Chain2", core.StatusFiring, map[string]string{"alertname": "Chain"})
	_, err = manager.AddAlertToGroup(ctx, alert2, groupKey)
	require.NoError(t, err)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(pub.calls()) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, len(pub.calls()), 2,
		"expected a second notification once the alert set changed")
}
