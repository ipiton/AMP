package grouping

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Benchmark helpers

func createBenchmarkManager() *DefaultGroupManager {
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver: "default",
			GroupBy:  []string{"alertname", "namespace"},
		},
	}

	manager, _ := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
	})
	return manager
}

func createBenchmarkAlert(name string, status core.AlertStatus) *core.Alert {
	return &core.Alert{
		Fingerprint: "fp_" + name,
		AlertName:   name,
		Status:      status,
		Labels: map[string]string{
			"alertname": name,
			"namespace": "prod",
			"severity":  "critical",
		},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}
}

// === Core Operation Benchmarks ===

// BenchmarkAddAlertToGroup_NewGroup benchmarks adding an alert to a new group.
// Target: <500μs (150% quality), <1ms (baseline)
func BenchmarkAddAlertToGroup_NewGroup(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)
	}
}

// BenchmarkAddAlertToGroup_ExistingGroup benchmarks adding an alert to an existing group.
// Target: <500μs (150% quality)
func BenchmarkAddAlertToGroup_ExistingGroup(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-create a group
	firstAlert := createBenchmarkAlert("Base", core.StatusFiring)
	groupKey := GroupKey("alertname=Base,namespace=prod")
	manager.AddAlertToGroup(ctx, firstAlert, groupKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		alert.Fingerprint = fmt.Sprintf("fp_unique_%d", i)
		_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)
	}
}

// BenchmarkGetGroup benchmarks retrieving a group.
// Target: <100μs (150% quality), <500μs (baseline)
func BenchmarkGetGroup(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate groups
	for i := 0; i < 100; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	targetKey := GroupKey("alertname=Alert50,namespace=prod")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.GetGroup(ctx, targetKey)
	}
}

// BenchmarkListGroups_Small benchmarks listing groups (100 groups).
// Target: <1ms
func BenchmarkListGroups_Small(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 100 groups
	for i := 0; i < 100; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.ListGroups(ctx, nil)
	}
}

// BenchmarkListGroups_Large benchmarks listing groups (1000 groups).
// Target: <5ms (150% quality), <10ms (baseline)
func BenchmarkListGroups_Large(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 1000 groups
	for i := 0; i < 1000; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.ListGroups(ctx, nil)
	}
}

// BenchmarkListGroups_WithFilters benchmarks listing groups with filters.
// 150% Enhancement: Pagination and filtering performance
func BenchmarkListGroups_WithFilters(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 1000 groups (mix of firing and resolved)
	for i := 0; i < 1000; i++ {
		status := core.StatusFiring
		if i%2 == 0 {
			status = core.StatusResolved
		}
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), status)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	firingState := GroupStateFiring
	filters := &GroupFilters{
		State: &firingState,
		Limit: 100,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.ListGroups(ctx, filters)
	}
}

// BenchmarkRemoveAlertFromGroup benchmarks removing an alert from a group.
// Target: <500μs (150% quality), <1ms (baseline)
func BenchmarkRemoveAlertFromGroup(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate groups with multiple alerts
	groupKey := GroupKey("alertname=Base,namespace=prod")
	for i := 0; i < b.N; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		alert.Fingerprint = fmt.Sprintf("fp_remove_%d", i)
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fingerprint := fmt.Sprintf("fp_remove_%d", i)
		_, _ = manager.RemoveAlertFromGroup(ctx, fingerprint, groupKey)
	}
}

// BenchmarkCleanupExpiredGroups benchmarks cleanup operation.
// Target: <50ms (150% quality), <100ms (baseline)
func BenchmarkCleanupExpiredGroups(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 100 expired groups
	for i := 0; i < 100; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusResolved)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)

		// Mark as expired (TN-125: use storage)
		group, _ := manager.storage.Load(ctx, groupKey)
		twoHoursAgo := time.Now().Add(-2 * time.Hour)
		group.Metadata.ResolvedAt = &twoHoursAgo
		group.Metadata.UpdatedAt = twoHoursAgo
		manager.storage.Store(ctx, group)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.CleanupExpiredGroups(ctx, 1*time.Hour)
	}
}

// BenchmarkGetGroupByFingerprint benchmarks reverse lookup by fingerprint.
// 150% Enhancement: O(1) lookup performance
// Target: <100μs
func BenchmarkGetGroupByFingerprint(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 1000 groups
	var targetFingerprint string
	for i := 0; i < 1000; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)

		if i == 500 {
			targetFingerprint = alert.Fingerprint
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = manager.GetGroupByFingerprint(ctx, targetFingerprint)
	}
}

// BenchmarkGetMetrics benchmarks metrics collection.
// Target: <1ms for 1000 groups
func BenchmarkGetMetrics(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate 1000 groups with varying sizes
	for i := 0; i < 1000; i++ {
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))

		// Add 1-10 alerts per group
		alertsPerGroup := (i % 10) + 1
		for j := 0; j < alertsPerGroup; j++ {
			alert := createBenchmarkAlert(fmt.Sprintf("Alert%d-%d", i, j), core.StatusFiring)
			alert.Fingerprint = fmt.Sprintf("fp_%d_%d", i, j)
			manager.AddAlertToGroup(ctx, alert, groupKey)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.GetMetrics(ctx)
	}
}

// BenchmarkGetStats benchmarks statistics collection.
// 150% Enhancement: Extended statistics
// Target: <1ms
func BenchmarkGetStats(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate groups
	for i := 0; i < 500; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.GetStats(ctx)
	}
}

// === Concurrent Access Benchmarks ===

// BenchmarkConcurrentAdds benchmarks concurrent alert additions.
// Tests thread-safety and lock contention.
func BenchmarkConcurrentAdds_Parallel(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
			alert.Fingerprint = fmt.Sprintf("fp_concurrent_%d", i)
			groupKey := GroupKey("alertname=Concurrent,namespace=prod")
			_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)
			i++
		}
	})
}

// BenchmarkConcurrentReads benchmarks concurrent group reads.
// Tests RLock performance.
func BenchmarkConcurrentReads_Parallel(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	// Pre-populate groups
	for i := 0; i < 100; i++ {
		alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
		groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i))
		manager.AddAlertToGroup(ctx, alert, groupKey)
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i%100))
			_, _ = manager.GetGroup(ctx, groupKey)
			i++
		}
	})
}

// BenchmarkMixedOperations benchmarks a realistic mix of operations.
// 150% Enhancement: Real-world scenario testing
func BenchmarkMixedOperations(b *testing.B) {
	manager := createBenchmarkManager()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 70% adds, 20% gets, 10% removes
		op := i % 10

		if op < 7 {
			// Add operation
			alert := createBenchmarkAlert(fmt.Sprintf("Alert%d", i), core.StatusFiring)
			groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i%100))
			_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)
		} else if op < 9 {
			// Get operation
			groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", i%100))
			_, _ = manager.GetGroup(ctx, groupKey)
		} else {
			// Remove operation (if group exists)
			fingerprint := fmt.Sprintf("fp_Alert%d", i-10)
			groupKey := GroupKey(fmt.Sprintf("alertname=Alert%d,namespace=prod", (i-10)%100))
			_, _ = manager.RemoveAlertFromGroup(ctx, fingerprint, groupKey)
		}
	}
}
