package publishing

import (
	"sync"
	"testing"
	"time"
)

// TestHealthStatusCache_GetSet tests basic Get/Set operations.
func TestHealthStatusCache_GetSet(t *testing.T) {
	cache := newHealthStatusCache()

	status := &TargetHealthStatus{
		TargetName:   "test-target",
		TargetType:   "rootly",
		Enabled:      true,
		Status:       HealthStatusHealthy,
		LatencyMs:    ptr(int64(123)),
		LastCheck:    time.Now(),
		TotalChecks:  100,
		SuccessRate:  99.5,
	}

	// Test Set
	cache.Set(status)

	// Test Get
	retrieved, ok := cache.Get("test-target")
	if !ok {
		t.Fatal("Failed to retrieve status from cache")
	}

	// Verify fields
	if retrieved.TargetName != "test-target" {
		t.Errorf("Expected target name 'test-target', got '%s'", retrieved.TargetName)
	}
	if retrieved.Status != HealthStatusHealthy {
		t.Errorf("Expected status 'healthy', got '%s'", retrieved.Status)
	}
	if *retrieved.LatencyMs != 123 {
		t.Errorf("Expected latency 123ms, got %dms", *retrieved.LatencyMs)
	}
}

// TestHealthStatusCache_GetNonExistent tests Get for non-existent target.
func TestHealthStatusCache_GetNonExistent(t *testing.T) {
	cache := newHealthStatusCache()

	_, ok := cache.Get("non-existent")
	if ok {
		t.Error("Expected Get to return false for non-existent target")
	}
}

// TestHealthStatusCache_GetStale tests stale entry detection.
func TestHealthStatusCache_GetStale(t *testing.T) {
	cache := newHealthStatusCache()
	cache.maxAge = 100 * time.Millisecond // Very short for testing

	status := &TargetHealthStatus{
		TargetName: "stale-target",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now().Add(-200 * time.Millisecond), // Old check
	}

	cache.Set(status)

	// Try to get stale entry
	_, ok := cache.Get("stale-target")
	if ok {
		t.Error("Expected Get to return false for stale entry")
	}
}

// TestHealthStatusCache_GetAll tests GetAll operation.
func TestHealthStatusCache_GetAll(t *testing.T) {
	cache := newHealthStatusCache()

	// Add multiple targets
	targets := []string{"target1", "target2", "target3"}
	for _, name := range targets {
		status := &TargetHealthStatus{
			TargetName: name,
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	// Get all
	all := cache.GetAll()

	if len(all) != 3 {
		t.Errorf("Expected 3 targets, got %d", len(all))
	}

	// Verify all target names present
	names := make(map[string]bool)
	for _, status := range all {
		names[status.TargetName] = true
	}

	for _, target := range targets {
		if !names[target] {
			t.Errorf("Target %s not in GetAll result", target)
		}
	}
}

// TestHealthStatusCache_GetAllExcludesStale tests stale entry exclusion in GetAll.
func TestHealthStatusCache_GetAllExcludesStale(t *testing.T) {
	cache := newHealthStatusCache()
	cache.maxAge = 100 * time.Millisecond

	// Add fresh entry
	fresh := &TargetHealthStatus{
		TargetName: "fresh",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now(),
	}
	cache.Set(fresh)

	// Add stale entry
	stale := &TargetHealthStatus{
		TargetName: "stale",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now().Add(-200 * time.Millisecond),
	}
	cache.Set(stale)

	// Get all (should exclude stale)
	all := cache.GetAll()

	if len(all) != 1 {
		t.Errorf("Expected 1 target (fresh only), got %d", len(all))
	}

	if all[0].TargetName != "fresh" {
		t.Errorf("Expected 'fresh' target, got '%s'", all[0].TargetName)
	}
}

// TestHealthStatusCache_Delete tests Delete operation.
func TestHealthStatusCache_Delete(t *testing.T) {
	cache := newHealthStatusCache()

	status := &TargetHealthStatus{
		TargetName: "to-delete",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now(),
	}
	cache.Set(status)

	// Verify it exists
	_, ok := cache.Get("to-delete")
	if !ok {
		t.Fatal("Failed to set initial status")
	}

	// Delete
	cache.Delete("to-delete")

	// Verify deleted
	_, ok = cache.Get("to-delete")
	if ok {
		t.Error("Status still exists after Delete")
	}
}

// TestHealthStatusCache_DeleteNonExistent tests Delete for non-existent target.
func TestHealthStatusCache_DeleteNonExistent(t *testing.T) {
	cache := newHealthStatusCache()

	// Should not panic
	cache.Delete("non-existent")
}

// TestHealthStatusCache_Clear tests Clear operation.
func TestHealthStatusCache_Clear(t *testing.T) {
	cache := newHealthStatusCache()

	// Add multiple targets
	for i := 0; i < 5; i++ {
		status := &TargetHealthStatus{
			TargetName: "target" + string(rune('0'+i)),
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	// Verify size
	if cache.Size() != 5 {
		t.Errorf("Expected size 5, got %d", cache.Size())
	}

	// Clear
	cache.Clear()

	// Verify empty
	if cache.Size() != 0 {
		t.Errorf("Expected size 0 after Clear, got %d", cache.Size())
	}

	all := cache.GetAll()
	if len(all) != 0 {
		t.Errorf("Expected 0 targets after Clear, got %d", len(all))
	}
}

// TestHealthStatusCache_Size tests Size method.
func TestHealthStatusCache_Size(t *testing.T) {
	cache := newHealthStatusCache()

	if cache.Size() != 0 {
		t.Errorf("Expected size 0 for empty cache, got %d", cache.Size())
	}

	// Add targets
	for i := 0; i < 10; i++ {
		status := &TargetHealthStatus{
			TargetName: "target" + string(rune('0'+i)),
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	if cache.Size() != 10 {
		t.Errorf("Expected size 10, got %d", cache.Size())
	}
}

// TestHealthStatusCache_GetAllNames tests GetAllNames method.
func TestHealthStatusCache_GetAllNames(t *testing.T) {
	cache := newHealthStatusCache()

	// Add targets
	expectedNames := []string{"target1", "target2", "target3"}
	for _, name := range expectedNames {
		status := &TargetHealthStatus{
			TargetName: name,
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	// Get all names
	names := cache.GetAllNames()

	if len(names) != 3 {
		t.Errorf("Expected 3 names, got %d", len(names))
	}

	// Verify all names present
	nameMap := make(map[string]bool)
	for _, name := range names {
		nameMap[name] = true
	}

	for _, expected := range expectedNames {
		if !nameMap[expected] {
			t.Errorf("Name %s not in GetAllNames result", expected)
		}
	}
}

// TestHealthStatusCache_Concurrent tests concurrent access.
func TestHealthStatusCache_Concurrent(t *testing.T) {
	cache := newHealthStatusCache()

	var wg sync.WaitGroup

	// Launch 10 concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				status := &TargetHealthStatus{
					TargetName: "target" + string(rune('0'+id)),
					Status:     HealthStatusHealthy,
					LastCheck:  time.Now(),
					TotalChecks: int64(j),
				}
				cache.Set(status)
			}
		}(i)
	}

	// Launch 10 concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				_ = cache.GetAll()
				_, _ = cache.Get("target" + string(rune('0'+id)))
			}
		}(i)
	}

	wg.Wait()

	// Verify final state (should have 10 targets)
	if cache.Size() != 10 {
		t.Errorf("Expected 10 targets after concurrent access, got %d", cache.Size())
	}
}

// TestHealthStatusCache_SetNil tests Set with nil status.
func TestHealthStatusCache_SetNil(t *testing.T) {
	cache := newHealthStatusCache()

	// Should not panic
	cache.Set(nil)

	// Verify cache still empty
	if cache.Size() != 0 {
		t.Error("Cache should remain empty after Set(nil)")
	}
}

// TestHealthStatusCache_Update tests atomic update method.
func TestHealthStatusCache_Update(t *testing.T) {
	cache := newHealthStatusCache()

	// Atomic update (should create new entry)
	status := cache.Update("test-target", func(s *TargetHealthStatus) {
		s.TotalChecks = 1
		s.TotalSuccesses = 1
		s.Status = HealthStatusHealthy
		s.LastCheck = time.Now()
	})

	// Verify status created
	if status.TargetName != "test-target" {
		t.Errorf("Expected target name 'test-target', got '%s'", status.TargetName)
	}
	if status.TotalChecks != 1 {
		t.Errorf("Expected 1 total check, got %d", status.TotalChecks)
	}

	// Second update (should modify existing)
	status2 := cache.Update("test-target", func(s *TargetHealthStatus) {
		s.TotalChecks++
		s.TotalSuccesses++
	})

	// Verify incremented
	if status2.TotalChecks != 2 {
		t.Errorf("Expected 2 total checks, got %d", status2.TotalChecks)
	}
	if status2.TotalSuccesses != 2 {
		t.Errorf("Expected 2 successes, got %d", status2.TotalSuccesses)
	}

	// Verify only one entry
	if cache.Size() != 1 {
		t.Errorf("Expected 1 entry, got %d", cache.Size())
	}
}

// TestHealthStatusCache_UpdateConcurrent tests concurrent atomic updates.
func TestHealthStatusCache_UpdateConcurrent(t *testing.T) {
	cache := newHealthStatusCache()

	var wg sync.WaitGroup

	// Launch 100 concurrent updates
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			cache.Update("concurrent-target", func(s *TargetHealthStatus) {
				s.TotalChecks++
			})
		}()
	}

	wg.Wait()

	// Verify all 100 increments were applied
	status, ok := cache.Get("concurrent-target")
	if !ok {
		t.Fatal("Status not found after concurrent updates")
	}

	if status.TotalChecks != 100 {
		t.Errorf("Expected 100 total checks, got %d (lost updates due to race)", status.TotalChecks)
	}
}

// TestHealthStatusCache_SetUpdate tests Set vs Update.
func TestHealthStatusCache_SetUpdate(t *testing.T) {
	cache := newHealthStatusCache()

	// Set initial status
	initial := &TargetHealthStatus{
		TargetName:  "set-update-target",
		Status:      HealthStatusHealthy,
		LastCheck:   time.Now(),
		TotalChecks: 1,
	}
	cache.Set(initial)

	// Update via atomic Update
	updated := cache.Update("set-update-target", func(s *TargetHealthStatus) {
		s.Status = HealthStatusUnhealthy
		s.TotalChecks = 5
	})

	// Verify updated
	if updated.Status != HealthStatusUnhealthy {
		t.Errorf("Expected status 'unhealthy', got '%s'", updated.Status)
	}
	if updated.TotalChecks != 5 {
		t.Errorf("Expected 5 total checks, got %d", updated.TotalChecks)
	}

	// Verify only one entry (not duplicated)
	if cache.Size() != 1 {
		t.Errorf("Expected 1 entry, got %d", cache.Size())
	}
}
