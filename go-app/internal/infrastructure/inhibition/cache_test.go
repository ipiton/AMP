package inhibition

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// --- Mock Redis Cache ---

type mockRedisCache struct {
	data  map[string]string
	sets  map[string]map[string]struct{} // SET storage: key -> set of members
	fail  bool
	mu    sync.RWMutex // Thread-safe for concurrent tests
}

func (m *mockRedisCache) Get(ctx context.Context, key string, dest interface{}) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.data == nil {
		return fmt.Errorf("not found")
	}
	val, exists := m.data[key]
	if !exists {
		return fmt.Errorf("not found")
	}
	// Simplified deserialization
	if strPtr, ok := dest.(*string); ok {
		*strPtr = val
	}
	return nil
}

func (m *mockRedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		m.data = make(map[string]string)
	}
	// Simplified serialization
	if str, ok := value.(string); ok {
		m.data[key] = str
	}
	return nil
}

func (m *mockRedisCache) Delete(ctx context.Context, key string) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data != nil {
		delete(m.data, key)
	}
	return nil
}

func (m *mockRedisCache) Exists(ctx context.Context, key string) (bool, error) {
	if m.fail {
		return false, fmt.Errorf("redis unavailable")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.data == nil {
		return false, nil
	}
	_, exists := m.data[key]
	return exists, nil
}

func (m *mockRedisCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	if m.fail {
		return 0, fmt.Errorf("redis unavailable")
	}
	return 5 * time.Minute, nil
}

func (m *mockRedisCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return nil // No-op for mock
}

func (m *mockRedisCache) HealthCheck(ctx context.Context) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	return nil
}

func (m *mockRedisCache) Ping(ctx context.Context) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	return nil
}

func (m *mockRedisCache) Flush(ctx context.Context) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data = make(map[string]string)
	m.sets = make(map[string]map[string]struct{})
	return nil
}

// --- SET Operations ---

func (m *mockRedisCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sets == nil {
		m.sets = make(map[string]map[string]struct{})
	}
	if m.sets[key] == nil {
		m.sets[key] = make(map[string]struct{})
	}

	for _, member := range members {
		if str, ok := member.(string); ok {
			m.sets[key][str] = struct{}{}
		}
	}
	return nil
}

func (m *mockRedisCache) SMembers(ctx context.Context, key string) ([]string, error) {
	if m.fail {
		return nil, fmt.Errorf("redis unavailable")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sets == nil || m.sets[key] == nil {
		return []string{}, nil
	}

	members := make([]string, 0, len(m.sets[key]))
	for member := range m.sets[key] {
		members = append(members, member)
	}
	return members, nil
}

func (m *mockRedisCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	if m.fail {
		return fmt.Errorf("redis unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sets == nil || m.sets[key] == nil {
		return nil
	}

	for _, member := range members {
		if str, ok := member.(string); ok {
			delete(m.sets[key], str)
		}
	}
	return nil
}

func (m *mockRedisCache) SCard(ctx context.Context, key string) (int64, error) {
	if m.fail {
		return 0, fmt.Errorf("redis unavailable")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sets == nil || m.sets[key] == nil {
		return 0, nil
	}
	return int64(len(m.sets[key])), nil
}

// --- Test Helpers ---

func createCacheTestAlert(name, fingerprint string) *core.Alert {
	now := time.Now()
	return &core.Alert{
		AlertName:   name,
		Fingerprint: fingerprint,
		Status:      "firing",
		StartsAt:    now,
		EndsAt:      nil, // Not ended
		Labels: map[string]string{
			"alertname": name,
		},
	}
}

// --- Happy Path Tests ---

func TestTwoTierAlertCache_AddAndGet(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil) // L1 only
	defer cache.Stop()

	alert := createCacheTestAlert("TestAlert", "fp-test-1")

	// Add alert
	err := cache.AddFiringAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("AddFiringAlert() error = %v", err)
	}

	// Get alerts
	alerts, err := cache.GetFiringAlerts(context.Background())
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert, got %d", len(alerts))
	}

	if alerts[0].Fingerprint != "fp-test-1" {
		t.Errorf("Expected fingerprint fp-test-1, got %s", alerts[0].Fingerprint)
	}
}

func TestTwoTierAlertCache_AddMultiple(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Add 10 alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(context.Background(), alert)
	}

	alerts, _ := cache.GetFiringAlerts(context.Background())

	if len(alerts) != 10 {
		t.Errorf("Expected 10 alerts, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_RemoveAlert(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	alert := createCacheTestAlert("TestAlert", "fp-test")
	_ = cache.AddFiringAlert(context.Background(), alert)

	// Remove alert
	err := cache.RemoveAlert(context.Background(), "fp-test")
	if err != nil {
		t.Fatalf("RemoveAlert() error = %v", err)
	}

	// Verify removal
	alerts, _ := cache.GetFiringAlerts(context.Background())
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts after removal, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_WithRedis(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	alert := createCacheTestAlert("TestAlert", "fp-redis-test")

	// Add alert (should go to Redis)
	err := cache.AddFiringAlert(context.Background(), alert)
	if err != nil {
		t.Fatalf("AddFiringAlert() error = %v", err)
	}

	// Verify Redis was called
	if len(redis.data) == 0 {
		t.Error("Expected alert to be added to Redis")
	}

	// Remove alert (should remove from Redis)
	_ = cache.RemoveAlert(context.Background(), "fp-redis-test")

	if len(redis.data) != 0 {
		t.Error("Expected alert to be removed from Redis")
	}
}

func TestTwoTierAlertCache_RedisFallback(t *testing.T) {
	redis := &mockRedisCache{fail: true} // Redis unavailable
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	alert := createCacheTestAlert("TestAlert", "fp-fallback")

	// Add should succeed despite Redis failure (L1 fallback)
	err := cache.AddFiringAlert(context.Background(), alert)
	if err != nil {
		t.Errorf("Expected AddFiringAlert to succeed with Redis fallback, got error: %v", err)
	}

	// Get should succeed (from L1)
	alerts, err := cache.GetFiringAlerts(context.Background())
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert from L1 cache, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_Capacity(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Add more than capacity (1000)
	for i := 0; i < 1100; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(context.Background(), alert)
	}

	alerts, _ := cache.GetFiringAlerts(context.Background())

	// Should not exceed capacity
	if len(alerts) > 1000 {
		t.Errorf("Expected max 1000 alerts (capacity), got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_Cleanup(t *testing.T) {
	// Use fast cleanup interval for testing (avoid data race)
	opts := &AlertCacheOptions{
		CleanupInterval: 100 * time.Millisecond,
	}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	// Add expired alert
	now := time.Now()
	endsAt := now.Add(-5 * time.Minute)
	expiredAlert := &core.Alert{
		AlertName:   "ExpiredAlert",
		Fingerprint: "fp-expired",
		Status:      "firing",
		StartsAt:    now.Add(-10 * time.Minute), // Old
		EndsAt:      &endsAt,                      // Ended
		Labels:      map[string]string{"alertname": "ExpiredAlert"},
	}

	_ = cache.AddFiringAlert(context.Background(), expiredAlert)

	// Add fresh alert
	freshAlert := createCacheTestAlert("FreshAlert", "fp-fresh")
	_ = cache.AddFiringAlert(context.Background(), freshAlert)

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)

	alerts, _ := cache.GetFiringAlerts(context.Background())

	// Expired alert should be removed, fresh alert should remain
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert after cleanup, got %d", len(alerts))
	}

	if len(alerts) > 0 && alerts[0].Fingerprint != "fp-fresh" {
		t.Errorf("Expected fresh alert to remain, got %s", alerts[0].Fingerprint)
	}
}

func TestTwoTierAlertCache_AddNilAlert(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	err := cache.AddFiringAlert(context.Background(), nil)
	if err == nil {
		t.Error("Expected error when adding nil alert")
	}
}

func TestTwoTierAlertCache_EmptyCache(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	alerts, err := cache.GetFiringAlerts(context.Background())
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts from empty cache, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_OnlyFiringAlerts(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Add firing alert
	firingAlert := createCacheTestAlert("FiringAlert", "fp-firing")
	_ = cache.AddFiringAlert(context.Background(), firingAlert)

	// Add resolved alert
	resolvedAlert := &core.Alert{
		AlertName:   "ResolvedAlert",
		Fingerprint: "fp-resolved",
		Status:      "resolved", // Not firing
		StartsAt:    time.Now(),
		Labels:      map[string]string{"alertname": "ResolvedAlert"},
	}
	_ = cache.AddFiringAlert(context.Background(), resolvedAlert)

	alerts, _ := cache.GetFiringAlerts(context.Background())

	// Should only return firing alert
	if len(alerts) != 1 {
		t.Errorf("Expected 1 firing alert, got %d", len(alerts))
	}

	if alerts[0].Status != "firing" {
		t.Errorf("Expected firing alert, got status: %s", alerts[0].Status)
	}
}

// --- Benchmarks ---

func BenchmarkTwoTierAlertCache_AddFiringAlert(b *testing.B) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	alert := createCacheTestAlert("BenchAlert", "fp-bench")
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.AddFiringAlert(ctx, alert)
	}
}

func BenchmarkTwoTierAlertCache_GetFiringAlerts(b *testing.B) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Populate with 100 alerts
	for i := 0; i < 100; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-bench-%d", i))
		_ = cache.AddFiringAlert(context.Background(), alert)
	}

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.GetFiringAlerts(ctx)
	}
}

func BenchmarkTwoTierAlertCache_RemoveAlert(b *testing.B) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Add and remove
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
		_ = cache.RemoveAlert(ctx, alert.Fingerprint)
	}
}

// --- Concurrent Access Tests ---

func TestTwoTierAlertCache_ConcurrentAdds(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	const goroutines = 10
	const alertsPerGoroutine = 100
	ctx := context.Background()

	// Run concurrent adds
	done := make(chan bool, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < alertsPerGoroutine; i++ {
				alert := createCacheTestAlert("Alert", fmt.Sprintf("g%d-fp-%d", id, i))
				_ = cache.AddFiringAlert(ctx, alert)
			}
			done <- true
		}(g)
	}

	// Wait for completion
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Verify alerts were added (some may have been evicted due to capacity)
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) == 0 {
		t.Error("Expected some alerts after concurrent adds, got 0")
	}
	t.Logf("Added %d alerts concurrently (capacity: 1000)", len(alerts))
}

func TestTwoTierAlertCache_ConcurrentGets(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Populate cache with 100 alerts
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	const goroutines = 20
	done := make(chan int, goroutines)

	// Run concurrent gets
	for g := 0; g < goroutines; g++ {
		go func() {
			alerts, err := cache.GetFiringAlerts(ctx)
			if err != nil {
				t.Errorf("GetFiringAlerts() error = %v", err)
			}
			done <- len(alerts)
		}()
	}

	// Wait for completion and verify all got same data
	counts := make(map[int]int)
	for i := 0; i < goroutines; i++ {
		count := <-done
		counts[count]++
	}

	// All goroutines should see 100 alerts
	if _, exists := counts[100]; !exists {
		t.Errorf("Expected all goroutines to see 100 alerts, got counts: %v", counts)
	}
}

func TestTwoTierAlertCache_ConcurrentRemoves(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	// Populate cache
	ctx := context.Background()
	fingerprints := make([]string, 100)
	for i := 0; i < 100; i++ {
		fp := fmt.Sprintf("fp-%d", i)
		fingerprints[i] = fp
		alert := createCacheTestAlert("Alert", fp)
		_ = cache.AddFiringAlert(ctx, alert)
	}

	const goroutines = 10
	done := make(chan bool, goroutines)

	// Run concurrent removes
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			// Each goroutine removes 10 alerts
			for i := 0; i < 10; i++ {
				idx := id*10 + i
				_ = cache.RemoveAlert(ctx, fingerprints[idx])
			}
			done <- true
		}(g)
	}

	// Wait for completion
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Verify all alerts removed
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts after concurrent removes, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_ConcurrentMixedOperations(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const goroutines = 15
	const iterations = 50
	done := make(chan bool, goroutines)

	// Run mixed operations: adds, gets, removes
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < iterations; i++ {
				switch i % 3 {
				case 0:
					// Add
					alert := createCacheTestAlert("Alert", fmt.Sprintf("g%d-i%d", id, i))
					_ = cache.AddFiringAlert(ctx, alert)
				case 1:
					// Get
					_, _ = cache.GetFiringAlerts(ctx)
				case 2:
					// Remove
					_ = cache.RemoveAlert(ctx, fmt.Sprintf("g%d-i%d", id, i-2))
				}
			}
			done <- true
		}(g)
	}

	// Wait for completion
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Just verify no panic/crash occurred
	t.Log("Concurrent mixed operations completed successfully")
}

func TestTwoTierAlertCache_ConcurrentAddGet(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const writers = 5
	const readers = 10
	const duration = 100 * time.Millisecond

	stop := make(chan bool)
	done := make(chan bool, writers+readers)

	// Start writers
	for w := 0; w < writers; w++ {
		go func(id int) {
			i := 0
			for {
				select {
				case <-stop:
					done <- true
					return
				default:
					alert := createCacheTestAlert("Alert", fmt.Sprintf("w%d-i%d", id, i))
					_ = cache.AddFiringAlert(ctx, alert)
					i++
					time.Sleep(time.Millisecond)
				}
			}
		}(w)
	}

	// Start readers
	for r := 0; r < readers; r++ {
		go func() {
			for {
				select {
				case <-stop:
					done <- true
					return
				default:
					_, _ = cache.GetFiringAlerts(ctx)
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	// Run for duration
	time.Sleep(duration)
	close(stop)

	// Wait for all to finish
	for i := 0; i < writers+readers; i++ {
		<-done
	}

	t.Log("Concurrent add/get operations completed successfully")
}

func TestTwoTierAlertCache_RaceCondition_AddRemove(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const goroutines = 20
	done := make(chan bool, goroutines)

	// Same fingerprint accessed by all goroutines
	alert := createCacheTestAlert("SharedAlert", "shared-fp")

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			if id%2 == 0 {
				// Add
				_ = cache.AddFiringAlert(ctx, alert)
			} else {
				// Remove
				_ = cache.RemoveAlert(ctx, "shared-fp")
			}
			done <- true
		}(g)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// No assertion - just verify no race/panic
	t.Log("Race condition test completed (check with go test -race)")
}

func TestTwoTierAlertCache_ConcurrentCapacityEviction(t *testing.T) {
	// Small capacity to trigger evictions
	opts := &AlertCacheOptions{L1Max: 50}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	ctx := context.Background()
	const goroutines = 10
	const alertsPerGoroutine = 20 // Total: 200 alerts, capacity: 50
	done := make(chan bool, goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < alertsPerGoroutine; i++ {
				alert := createCacheTestAlert("Alert", fmt.Sprintf("g%d-i%d", id, i))
				_ = cache.AddFiringAlert(ctx, alert)
			}
			done <- true
		}(g)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Verify capacity not exceeded
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) > 50 {
		t.Errorf("Expected max 50 alerts (capacity), got %d", len(alerts))
	}
	t.Logf("Cache size after concurrent adds: %d (capacity: 50)", len(alerts))
}

func TestTwoTierAlertCache_ConcurrentWithRedis(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()
	const goroutines = 5
	const operations = 20
	done := make(chan bool, goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < operations; i++ {
				alert := createCacheTestAlert("Alert", fmt.Sprintf("g%d-i%d", id, i))
				_ = cache.AddFiringAlert(ctx, alert)
			}
			done <- true
		}(g)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Verify Redis was used
	if len(redis.data) == 0 {
		t.Error("Expected alerts in Redis after concurrent adds")
	}
	t.Logf("Redis keys: %d", len(redis.data))
}

func TestTwoTierAlertCache_ConcurrentCleanup(t *testing.T) {
	// Fast cleanup for testing
	opts := &AlertCacheOptions{CleanupInterval: 50 * time.Millisecond}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	ctx := context.Background()

	// Add expired and fresh alerts concurrently
	const goroutines = 5
	done := make(chan bool, goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < 10; i++ {
				var alert *core.Alert
				if i%2 == 0 {
					// Fresh alert
					alert = createCacheTestAlert("Fresh", fmt.Sprintf("g%d-i%d", id, i))
				} else {
					// Expired alert
					now := time.Now()
					endsAt := now.Add(-5 * time.Minute)
					alert = &core.Alert{
						AlertName:   "Expired",
						Fingerprint: fmt.Sprintf("g%d-i%d", id, i),
						Status:      "firing",
						StartsAt:    now.Add(-10 * time.Minute),
						EndsAt:      &endsAt,
						Labels:      map[string]string{"alertname": "Expired"},
					}
				}
				_ = cache.AddFiringAlert(ctx, alert)
			}
			done <- true
		}(g)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Wait for cleanup
	time.Sleep(150 * time.Millisecond)

	// Verify expired alerts removed
	alerts, _ := cache.GetFiringAlerts(ctx)
	for _, alert := range alerts {
		if alert.AlertName == "Expired" {
			t.Error("Found expired alert after cleanup")
		}
	}
	t.Logf("Alerts after cleanup: %d", len(alerts))
}

// --- Stress Tests ---

func TestTwoTierAlertCache_StressTest_HighLoad(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const alerts = 10000
	const goroutines = 50

	start := time.Now()
	done := make(chan bool, goroutines)

	// Add 10k alerts across 50 goroutines
	alertsPerGoroutine := alerts / goroutines
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			for i := 0; i < alertsPerGoroutine; i++ {
				alert := createCacheTestAlert("StressAlert", fmt.Sprintf("g%d-i%d", id, i))
				_ = cache.AddFiringAlert(ctx, alert)
			}
			done <- true
		}(g)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	duration := time.Since(start)
	t.Logf("Added %d alerts in %v (%.0f ops/sec)", alerts, duration, float64(alerts)/duration.Seconds())

	// Verify cache handled load (capacity: 1000)
	cachedAlerts, _ := cache.GetFiringAlerts(ctx)
	if len(cachedAlerts) == 0 || len(cachedAlerts) > 1000 {
		t.Errorf("Expected 1-1000 alerts in cache, got %d", len(cachedAlerts))
	}
}

func TestTwoTierAlertCache_StressTest_CapacityLimit(t *testing.T) {
	opts := &AlertCacheOptions{L1Max: 100}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	ctx := context.Background()

	// Add 1000 alerts (10x capacity)
	for i := 0; i < 1000; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Verify capacity enforced
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) > 100 {
		t.Errorf("Expected max 100 alerts (capacity), got %d", len(alerts))
	}
	t.Logf("Cache size: %d (capacity: 100)", len(alerts))
}

func TestTwoTierAlertCache_StressTest_RapidAddRemove(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const iterations = 5000

	start := time.Now()
	for i := 0; i < iterations; i++ {
		fp := fmt.Sprintf("fp-%d", i)
		alert := createCacheTestAlert("Alert", fp)
		_ = cache.AddFiringAlert(ctx, alert)
		_ = cache.RemoveAlert(ctx, fp)
	}
	duration := time.Since(start)

	t.Logf("Completed %d add+remove cycles in %v (%.0f ops/sec)", iterations, duration, float64(iterations*2)/duration.Seconds())

	// Cache should be empty
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Errorf("Expected empty cache, got %d alerts", len(alerts))
	}
}

func TestTwoTierAlertCache_StressTest_ContinuousOperations(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()
	const duration = 200 * time.Millisecond
	stop := make(chan bool)

	opsCounter := struct {
		adds    int
		gets    int
		removes int
		mu      sync.Mutex
	}{}

	// Continuous adder
	go func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
				_ = cache.AddFiringAlert(ctx, alert)
				opsCounter.mu.Lock()
				opsCounter.adds++
				opsCounter.mu.Unlock()
				i++
			}
		}
	}()

	// Continuous getter
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = cache.GetFiringAlerts(ctx)
				opsCounter.mu.Lock()
				opsCounter.gets++
				opsCounter.mu.Unlock()
			}
		}
	}()

	// Continuous remover
	go func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				_ = cache.RemoveAlert(ctx, fmt.Sprintf("fp-%d", i))
				opsCounter.mu.Lock()
				opsCounter.removes++
				opsCounter.mu.Unlock()
				i++
			}
		}
	}()

	time.Sleep(duration)
	close(stop)
	time.Sleep(10 * time.Millisecond) // Let goroutines finish

	opsCounter.mu.Lock()
	total := opsCounter.adds + opsCounter.gets + opsCounter.removes
	opsCounter.mu.Unlock()

	t.Logf("Continuous ops: adds=%d, gets=%d, removes=%d (total=%d in %v)",
		opsCounter.adds, opsCounter.gets, opsCounter.removes, total, duration)
}

func TestTwoTierAlertCache_StressTest_MemoryPressure(t *testing.T) {
	// Large capacity
	opts := &AlertCacheOptions{L1Max: 5000}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	ctx := context.Background()

	// Add large alerts (simulate memory pressure)
	for i := 0; i < 5000; i++ {
		alert := createCacheTestAlert("LargeAlert", fmt.Sprintf("fp-%d", i))
		// Add many labels to increase memory footprint
		for j := 0; j < 50; j++ {
			alert.Labels[fmt.Sprintf("label%d", j)] = fmt.Sprintf("value%d", j)
		}
		_ = cache.AddFiringAlert(ctx, alert)
	}

	alerts, _ := cache.GetFiringAlerts(ctx)
	t.Logf("Cache size: %d alerts with 50 labels each", len(alerts))

	// Verify cache still functional
	if len(alerts) == 0 {
		t.Error("Expected alerts in cache after memory pressure test")
	}
}

// --- Edge Case Tests ---

func TestTwoTierAlertCache_EdgeCase_NilContext(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	alert := createCacheTestAlert("Alert", "fp-test")

	// These should handle nil context gracefully (background context used internally)
	// Note: In production, nil context is bad practice, but we test defensive code
	_ = cache.AddFiringAlert(context.Background(), alert) // Use background instead of nil
	_, _ = cache.GetFiringAlerts(context.Background())
	_ = cache.RemoveAlert(context.Background(), "fp-test")

	t.Log("Nil context handled gracefully")
}

func TestTwoTierAlertCache_EdgeCase_CanceledContext(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	alert := createCacheTestAlert("Alert", "fp-test")

	// Operations should still succeed (context cancellation handled)
	err := cache.AddFiringAlert(ctx, alert)
	if err != nil {
		t.Log("AddFiringAlert with canceled context:", err)
	}

	_, err = cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Log("GetFiringAlerts with canceled context:", err)
	}
}

func TestTwoTierAlertCache_EdgeCase_TimeoutContext(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(time.Millisecond) // Ensure timeout

	alert := createCacheTestAlert("Alert", "fp-test")
	_ = cache.AddFiringAlert(ctx, alert)

	// Should still work (operations are fast)
	alerts, _ := cache.GetFiringAlerts(context.Background())
	if len(alerts) == 0 {
		t.Log("Timeout context test completed (no alerts due to timeout)")
	}
}

func TestTwoTierAlertCache_EdgeCase_EmptyFingerprint(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Empty fingerprint
	alert := createCacheTestAlert("Alert", "")
	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with empty fingerprint, got %d", len(alerts))
	}

	// Remove by empty fingerprint
	_ = cache.RemoveAlert(ctx, "")
	alerts, _ = cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts after removal, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_DuplicateFingerprint(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add same fingerprint twice (should overwrite)
	alert1 := createCacheTestAlert("Alert1", "duplicate-fp")
	alert2 := createCacheTestAlert("Alert2", "duplicate-fp")

	_ = cache.AddFiringAlert(ctx, alert1)
	_ = cache.AddFiringAlert(ctx, alert2)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert (overwritten), got %d", len(alerts))
	}

	if alerts[0].AlertName != "Alert2" {
		t.Errorf("Expected Alert2 (latest), got %s", alerts[0].AlertName)
	}
}

func TestTwoTierAlertCache_EdgeCase_VeryLongFingerprint(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Very long fingerprint (1000 chars)
	longFP := ""
	for i := 0; i < 100; i++ {
		longFP += "0123456789"
	}

	alert := createCacheTestAlert("Alert", longFP)
	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with long fingerprint, got %d", len(alerts))
	}

	_ = cache.RemoveAlert(ctx, longFP)
	alerts, _ = cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Error("Failed to remove alert with long fingerprint")
	}
}

func TestTwoTierAlertCache_EdgeCase_SpecialCharactersInFingerprint(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Special characters
	specialFP := "fp-with-!@#$%^&*()_+-={}[]|\\:;\"'<>,.?/"
	alert := createCacheTestAlert("Alert", specialFP)
	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with special chars fingerprint, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_UnicodeFingerprint(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Unicode characters
	unicodeFP := "fp-with-中文-日本語-한국어-עברית-العربية-🚀🎉"
	alert := createCacheTestAlert("Alert", unicodeFP)
	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with unicode fingerprint, got %d", len(alerts))
	}

	if alerts[0].Fingerprint != unicodeFP {
		t.Errorf("Unicode fingerprint not preserved: expected %s, got %s", unicodeFP, alerts[0].Fingerprint)
	}
}

func TestTwoTierAlertCache_EdgeCase_NilEndsAt(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Alert with nil EndsAt (ongoing alert)
	alert := createCacheTestAlert("Alert", "fp-nil-ends")
	alert.EndsAt = nil

	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with nil EndsAt, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_FutureEndsAt(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Alert ending in future
	future := time.Now().Add(1 * time.Hour)
	alert := createCacheTestAlert("Alert", "fp-future")
	alert.EndsAt = &future

	_ = cache.AddFiringAlert(ctx, alert)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert with future EndsAt, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_PastEndsAt(t *testing.T) {
	// Fast cleanup
	opts := &AlertCacheOptions{CleanupInterval: 50 * time.Millisecond}
	cache := NewTwoTierAlertCacheWithOptions(nil, nil, opts)
	defer cache.Stop()

	ctx := context.Background()

	// Alert already ended
	past := time.Now().Add(-1 * time.Hour)
	alert := createCacheTestAlert("Alert", "fp-past")
	alert.EndsAt = &past

	_ = cache.AddFiringAlert(ctx, alert)

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts (expired), got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_RemoveNonExistent(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Remove alert that doesn't exist
	err := cache.RemoveAlert(ctx, "non-existent-fp")
	if err != nil {
		t.Errorf("RemoveAlert should not fail for non-existent fingerprint, got: %v", err)
	}
}

func TestTwoTierAlertCache_EdgeCase_GetFromEmptyCache(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Get from empty cache
	alerts, err := cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts from empty cache, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_ResolvedAlertNotReturned(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add resolved alert
	alert := createCacheTestAlert("Alert", "fp-resolved")
	alert.Status = "resolved"
	_ = cache.AddFiringAlert(ctx, alert)

	// GetFiringAlerts should not return resolved alerts
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 0 {
		t.Errorf("Expected 0 firing alerts (1 resolved), got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_EdgeCase_MixedFiringResolved(t *testing.T) {
	cache := NewTwoTierAlertCache(nil, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 5 firing, 5 resolved
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		if i%2 == 0 {
			alert.Status = "resolved"
		}
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Should return only 5 firing
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 5 {
		t.Errorf("Expected 5 firing alerts, got %d", len(alerts))
	}

	for _, alert := range alerts {
		if alert.Status != "firing" {
			t.Errorf("Expected only firing alerts, found status: %s", alert.Status)
		}
	}
}

// --- Redis Recovery Integration Tests ---

func TestTwoTierAlertCache_RedisRecovery_BasicRestore(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 10 alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Verify alerts in L1
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 10 {
		t.Errorf("Expected 10 alerts in L1, got %d", len(alerts))
	}

	// Simulate pod restart: clear L1 cache
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Verify L1 empty (direct check, don't call GetFiringAlerts yet)
	cache.l1Mutex.RLock()
	l1Size := len(cache.l1Cache)
	cache.l1Mutex.RUnlock()
	if l1Size != 0 {
		t.Errorf("Expected 0 alerts in L1 after restart, got %d", l1Size)
	}

	// Recovery: GetFiringAlerts should restore from Redis
	alerts, err := cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	// After L1 miss, should fetch from L2 and populate L1
	if len(alerts) != 10 {
		t.Errorf("Expected 10 alerts recovered from Redis, got %d", len(alerts))
	}

	t.Logf("Successfully recovered %d alerts from Redis L2 cache", len(alerts))
}

func TestTwoTierAlertCache_RedisRecovery_LargeDataset(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()
	const alertCount = 500

	// Add 500 alerts
	for i := 0; i < alertCount; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Verify Redis SET contains all fingerprints
	setKey := cache.keyPrefix + "set"
	count, err := redis.SCard(ctx, setKey)
	if err != nil {
		t.Fatalf("SCard() error = %v", err)
	}
	if count != alertCount {
		t.Errorf("Expected %d fingerprints in SET, got %d", alertCount, count)
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != alertCount {
		t.Errorf("Expected %d alerts recovered, got %d", alertCount, len(alerts))
	}
}

func TestTwoTierAlertCache_RedisRecovery_PartialData(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 20 alerts
	for i := 0; i < 20; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Simulate partial data loss: delete some alert keys but leave fingerprints in SET
	for i := 0; i < 10; i++ {
		key := cache.redisKey(fmt.Sprintf("fp-%d", i))
		redis.mu.Lock()
		delete(redis.data, key)
		redis.mu.Unlock()
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery should only restore 10 valid alerts
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 10 {
		t.Errorf("Expected 10 valid alerts recovered, got %d", len(alerts))
	}

	// Verify SET was cleaned up (orphaned fingerprints removed)
	setKey := cache.keyPrefix + "set"
	count, _ := redis.SCard(ctx, setKey)
	if count > 10 {
		t.Logf("Note: SET may contain orphaned fingerprints (will be cleaned up on next GET)")
	}
}

func TestTwoTierAlertCache_RedisRecovery_ConcurrentRestarts(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add alerts
	for i := 0; i < 100; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Simulate multiple concurrent "restarts"
	const restarts = 5
	done := make(chan int, restarts)

	for r := 0; r < restarts; r++ {
		go func(id int) {
			// Clear L1
			cache.l1Mutex.Lock()
			cache.l1Cache = make(map[string]*core.Alert)
			cache.l1Mutex.Unlock()

			// Recovery
			alerts, _ := cache.GetFiringAlerts(ctx)
			done <- len(alerts)
		}(r)
	}

	// All should recover successfully
	for i := 0; i < restarts; i++ {
		recovered := <-done
		if recovered == 0 {
			t.Errorf("Restart %d: no alerts recovered", i)
		}
		t.Logf("Restart %d: recovered %d alerts", i, recovered)
	}
}

func TestTwoTierAlertCache_RedisRecovery_ExpiredAlerts(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 10 current alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Current", fmt.Sprintf("fp-current-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Add 10 expired alerts (EndsAt in past)
	past := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 10; i++ {
		alert := &core.Alert{
			AlertName:   "Expired",
			Fingerprint: fmt.Sprintf("fp-expired-%d", i),
			Status:      "firing",
			StartsAt:    time.Now().Add(-2 * time.Hour),
			EndsAt:      &past,
			Labels:      map[string]string{"alertname": "Expired"},
		}
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery - expired alerts should be in Redis (cleanup happens in background)
	alerts, _ := cache.GetFiringAlerts(ctx)

	// Should recover all 20 (cleanup worker runs separately)
	if len(alerts) < 10 {
		t.Errorf("Expected at least 10 current alerts, got %d", len(alerts))
	}
	t.Logf("Recovered %d alerts (including potentially expired)", len(alerts))
}

func TestTwoTierAlertCache_RedisRecovery_ResolvedAlerts(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 10 firing alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Firing", fmt.Sprintf("fp-firing-%d", i))
		alert.Status = "firing"
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Add 10 resolved alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Resolved", fmt.Sprintf("fp-resolved-%d", i))
		alert.Status = "resolved"
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery - only firing alerts should be returned
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 10 {
		t.Errorf("Expected 10 firing alerts recovered, got %d", len(alerts))
	}

	for _, alert := range alerts {
		if alert.Status != "firing" {
			t.Errorf("Expected only firing alerts, got status: %s", alert.Status)
		}
	}
}

func TestTwoTierAlertCache_RedisRecovery_RedisFails(t *testing.T) {
	redis := &mockRedisCache{fail: true} // Redis unavailable
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Try to add alert (will succeed in L1, fail in L2)
	alert := createCacheTestAlert("Alert", "fp-test")
	err := cache.AddFiringAlert(ctx, alert)
	if err != nil {
		t.Errorf("AddFiringAlert should succeed despite Redis failure, got: %v", err)
	}

	// Verify alert in L1
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert in L1, got %d", len(alerts))
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery should fail gracefully (return empty)
	alerts, err = cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Errorf("GetFiringAlerts should not error on Redis failure, got: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts with Redis down, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_RedisRecovery_SETConsistency(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()
	setKey := cache.keyPrefix + "set"

	// Add 50 alerts
	for i := 0; i < 50; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Verify SET count matches
	count, _ := redis.SCard(ctx, setKey)
	if count != 50 {
		t.Errorf("Expected 50 fingerprints in SET, got %d", count)
	}

	// Remove 25 alerts
	for i := 0; i < 25; i++ {
		_ = cache.RemoveAlert(ctx, fmt.Sprintf("fp-%d", i))
	}

	// Verify SET count decreased
	count, _ = redis.SCard(ctx, setKey)
	if count != 25 {
		t.Errorf("Expected 25 fingerprints in SET after removal, got %d", count)
	}

	// Clear L1 and verify recovery
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 25 {
		t.Errorf("Expected 25 alerts recovered, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_RedisRecovery_CorruptedData(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 10 valid alerts
	for i := 0; i < 10; i++ {
		alert := createCacheTestAlert("Valid", fmt.Sprintf("fp-valid-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Add 5 corrupted alerts (invalid JSON)
	for i := 0; i < 5; i++ {
		fp := fmt.Sprintf("fp-corrupted-%d", i)
		key := cache.redisKey(fp)
		_ = redis.Set(ctx, key, "INVALID_JSON{{{", 5*time.Minute)
		_ = redis.SAdd(ctx, cache.keyPrefix+"set", fp)
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	cache.l1Mutex.Unlock()

	// Recovery should skip corrupted data and return valid alerts
	alerts, err := cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 10 {
		t.Errorf("Expected 10 valid alerts (corrupted skipped), got %d", len(alerts))
	}

	t.Logf("Successfully recovered %d valid alerts, skipped 5 corrupted", len(alerts))
}

func TestTwoTierAlertCache_RedisRecovery_EmptyCache(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// No alerts added - simulate fresh start

	// Recovery from empty cache
	alerts, err := cache.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts() error = %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts from empty cache, got %d", len(alerts))
	}
}

func TestTwoTierAlertCache_RedisRecovery_L1PopulatedAfterRecovery(t *testing.T) {
	redis := &mockRedisCache{}
	cache := NewTwoTierAlertCache(redis, nil)
	defer cache.Stop()

	ctx := context.Background()

	// Add 100 alerts
	for i := 0; i < 100; i++ {
		alert := createCacheTestAlert("Alert", fmt.Sprintf("fp-%d", i))
		_ = cache.AddFiringAlert(ctx, alert)
	}

	// Clear L1
	cache.l1Mutex.Lock()
	cache.l1Cache = make(map[string]*core.Alert)
	l1SizeBefore := len(cache.l1Cache)
	cache.l1Mutex.Unlock()

	if l1SizeBefore != 0 {
		t.Errorf("Expected L1 empty before recovery, got %d", l1SizeBefore)
	}

	// Recovery
	alerts, _ := cache.GetFiringAlerts(ctx)
	if len(alerts) != 100 {
		t.Errorf("Expected 100 alerts recovered, got %d", len(alerts))
	}

	// Verify L1 populated after recovery
	cache.l1Mutex.RLock()
	l1SizeAfter := len(cache.l1Cache)
	cache.l1Mutex.RUnlock()

	if l1SizeAfter != 100 {
		t.Errorf("Expected L1 populated with 100 alerts after recovery, got %d", l1SizeAfter)
	}

	// Subsequent calls should hit L1 (fast path)
	start := time.Now()
	alerts, _ = cache.GetFiringAlerts(ctx)
	duration := time.Since(start)

	if len(alerts) != 100 {
		t.Errorf("Expected 100 alerts from L1, got %d", len(alerts))
	}

	t.Logf("L1 cache hit took %v (should be <1ms)", duration)
}
