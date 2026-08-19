package metrics

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// sharedTestBusinessMetrics returns one *BusinessMetrics for the whole test
// binary run: NewBusinessMetrics registers its collectors into the default
// Prometheus registry via promauto, so a second construction in the same
// process panics with "duplicate metrics collector registration attempted".
var (
	sharedTestMetricsOnce sync.Once
	sharedTestMetrics     *BusinessMetrics
)

func sharedTestBusinessMetrics() *BusinessMetrics {
	sharedTestMetricsOnce.Do(func() {
		sharedTestMetrics = NewBusinessMetrics()
	})
	return sharedTestMetrics
}

// TestSetActiveGroupStorageBackend_CustomLabelSwitchClearsThePreviousOne is
// the fix-round 2 Minor #7 regression test: StorageManagerConfig.PrimaryLabel/
// FallbackLabel accept any string, and a caller switching between two
// DIFFERENT custom (non-redis/memory) label pairs must not leave a stale
// "1" behind under the label that is no longer in use.
func TestSetActiveGroupStorageBackend_CustomLabelSwitchClearsThePreviousOne(t *testing.T) {
	m := sharedTestBusinessMetrics()

	m.SetActiveGroupStorageBackend("postgres")
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("postgres")); got != 1 {
		t.Fatalf("postgres gauge = %v, want 1", got)
	}

	m.SetActiveGroupStorageBackend("disk")
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("disk")); got != 1 {
		t.Fatalf("disk gauge = %v, want 1", got)
	}

	// The previous custom label's series must be gone entirely (not just
	// zeroed) rather than reading a stale 1 forever.
	metric, err := m.GroupStorageBackendGauge().GetMetricWithLabelValues("postgres")
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(postgres): %v", err)
	}
	if got := testutil.ToFloat64(metric); got != 0 {
		t.Fatalf("postgres gauge after switching to disk = %v, want 0 (DeleteLabelValues should have removed/reset it)", got)
	}
}

// TestSetActiveGroupStorageBackend_KnownLabelsUnaffectedByCustomLabelTracking
// guards against the fix-round 2 Minor #7 change regressing the ordinary
// redis/memory path: switching between the two known labels must not
// trip the "clear the previous custom label" logic at all.
func TestSetActiveGroupStorageBackend_KnownLabelsUnaffectedByCustomLabelTracking(t *testing.T) {
	m := sharedTestBusinessMetrics()

	m.SetActiveGroupStorageBackend("redis")
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("redis")); got != 1 {
		t.Fatalf("redis gauge = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("memory")); got != 0 {
		t.Fatalf("memory gauge = %v, want 0", got)
	}

	m.SetActiveGroupStorageBackend("memory")
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("redis")); got != 0 {
		t.Fatalf("redis gauge after switching to memory = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.GroupStorageBackendGauge().WithLabelValues("memory")); got != 1 {
		t.Fatalf("memory gauge = %v, want 1", got)
	}
}
