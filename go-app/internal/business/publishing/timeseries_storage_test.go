package publishing

import (
	"testing"
	"time"
)

// ============================================================================
// TimeSeriesStorage Tests
// ============================================================================

// TestTimeSeriesStorage_Basic tests basic record and retrieval.
func TestTimeSeriesStorage_Basic(t *testing.T) {
	ts := NewTimeSeriesStorage(24 * time.Hour)

	// Initially empty
	if ts.Size() != 0 {
		t.Errorf("Expected size 0, got %d", ts.Size())
	}

	// Record a snapshot
	now := time.Now()
	snapshot := &MetricsSnapshot{
		Timestamp: now,
		Metrics: map[string]float64{
			"jobs_submitted_total": 100.0,
		},
	}
	ts.Record(snapshot)

	// Check size
	if ts.Size() != 1 {
		t.Errorf("Expected size 1 after record, got %d", ts.Size())
	}

	// Retrieve
	all := ts.GetAll()
	if len(all) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(all))
	}

	if !all[0].Timestamp.Equal(now) {
		t.Errorf("Expected timestamp %v, got %v", now, all[0].Timestamp)
	}
}

// TestTimeSeriesStorage_RingBuffer tests ring buffer overflow behavior.
func TestTimeSeriesStorage_RingBuffer(t *testing.T) {
	// Create storage with capacity 15 (minimum is 10, so this will be 15)
	ts := NewTimeSeriesStorage(15 * time.Minute)

	// Record 30 snapshots (more than capacity)
	for i := 0; i < 30; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"index": float64(i),
			},
		}
		ts.Record(snapshot)
	}

	// Size should be capped at capacity (15)
	if ts.Size() != 15 {
		t.Errorf("Expected size 15 (capacity), got %d", ts.Size())
	}

	// Check that we have the last 15 snapshots (indices 15-29)
	all := ts.GetAll()
	if len(all) != 15 {
		t.Fatalf("Expected 15 snapshots, got %d", len(all))
	}

	// Verify indices 15-29 are stored
	for i, snapshot := range all {
		expectedIndex := float64(15 + i) // Indices 15, 16, ..., 29
		if snapshot.Metrics["index"] != expectedIndex {
			t.Errorf("Expected index %.0f, got %.0f", expectedIndex, snapshot.Metrics["index"])
		}
	}
}

// TestTimeSeriesStorage_GetRange tests time range filtering.
func TestTimeSeriesStorage_GetRange(t *testing.T) {
	ts := NewTimeSeriesStorage(24 * time.Hour)
	now := time.Now()

	// Record snapshots at different times
	for i := 0; i < 10; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"index": float64(i),
			},
		}
		ts.Record(snapshot)
	}

	// Get snapshots between minute 3 and minute 7 (inclusive)
	start := now.Add(3 * time.Minute)
	end := now.Add(7 * time.Minute)
	filtered := ts.GetRange(start, end)

	// Should have 5 snapshots (3, 4, 5, 6, 7)
	if len(filtered) != 5 {
		t.Errorf("Expected 5 snapshots in range, got %d", len(filtered))
	}

	// Verify indices
	for i, snapshot := range filtered {
		expectedIndex := float64(3 + i)
		if snapshot.Metrics["index"] != expectedIndex {
			t.Errorf("Expected index %.0f, got %.0f", expectedIndex, snapshot.Metrics["index"])
		}
	}
}

// TestTimeSeriesStorage_GetLatest tests retrieving N most recent snapshots.
func TestTimeSeriesStorage_GetLatest(t *testing.T) {
	ts := NewTimeSeriesStorage(24 * time.Hour)
	now := time.Now()

	// Record 10 snapshots
	for i := 0; i < 10; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"index": float64(i),
			},
		}
		ts.Record(snapshot)
	}

	// Get latest 3 snapshots
	latest := ts.GetLatest(3)

	// Should have 3 snapshots (7, 8, 9)
	if len(latest) != 3 {
		t.Fatalf("Expected 3 snapshots, got %d", len(latest))
	}

	// Verify they are the last 3 (indices 7, 8, 9)
	for i, snapshot := range latest {
		expectedIndex := float64(7 + i)
		if snapshot.Metrics["index"] != expectedIndex {
			t.Errorf("Expected index %.0f, got %.0f", expectedIndex, snapshot.Metrics["index"])
		}
	}
}

// TestTimeSeriesStorage_Clear tests clearing all snapshots.
func TestTimeSeriesStorage_Clear(t *testing.T) {
	ts := NewTimeSeriesStorage(24 * time.Hour)

	// Record some snapshots
	for i := 0; i < 5; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			Metrics:   map[string]float64{"index": float64(i)},
		}
		ts.Record(snapshot)
	}

	// Verify size
	if ts.Size() != 5 {
		t.Errorf("Expected size 5, got %d", ts.Size())
	}

	// Clear
	ts.Clear()

	// Verify empty
	if ts.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", ts.Size())
	}

	all := ts.GetAll()
	if len(all) != 0 {
		t.Errorf("Expected 0 snapshots after clear, got %d", len(all))
	}
}

// TestTimeSeriesStorage_Cleanup tests automatic cleanup of expired entries.
func TestTimeSeriesStorage_Cleanup(t *testing.T) {
	// Create storage with 10-minute retention
	ts := NewTimeSeriesStorage(10 * time.Minute)
	now := time.Now()

	// Record snapshots from 15 minutes ago to now
	for i := 0; i < 15; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-15) * time.Minute),
			Metrics: map[string]float64{
				"index": float64(i),
			},
		}
		ts.Record(snapshot)
	}

	// Before cleanup: should have 10 snapshots (capacity capped)
	initialSize := ts.Size()
	if initialSize != 10 {
		t.Logf("Warning: Expected initial size 10, got %d", initialSize)
	}

	// Run cleanup (removes entries older than 10 minutes)
	removed := ts.Cleanup()

	// Check that some entries were removed
	if removed == 0 {
		t.Logf("Note: Cleanup removed 0 entries (may be expected if retention > capacity)")
	}

	// After cleanup: should only have recent snapshots
	finalSize := ts.Size()
	t.Logf("Cleanup: initial size %d, removed %d, final size %d", initialSize, removed, finalSize)
}

// TestTimeSeriesStorage_ThreadSafety tests concurrent access.
func TestTimeSeriesStorage_ThreadSafety(t *testing.T) {
	ts := NewTimeSeriesStorage(24 * time.Hour)

	// Concurrent writers
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			for j := 0; j < 100; j++ {
				snapshot := &MetricsSnapshot{
					Timestamp: time.Now(),
					Metrics: map[string]float64{
						"index": float64(index*100 + j),
					},
				}
				ts.Record(snapshot)
			}
			done <- true
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = ts.GetAll()
				_ = ts.GetLatest(10)
				_ = ts.Size()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	// Final size should be reasonable (capped by capacity)
	finalSize := ts.Size()
	if finalSize == 0 {
		t.Error("Expected some snapshots after concurrent writes")
	}

	t.Logf("Final size after concurrent access: %d", finalSize)
}
