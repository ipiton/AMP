package publishing

import (
	"testing"
	"time"
)

// BenchmarkStart benchmarks Start() operation latency.
//
// Target: <500µs (150% goal)
// Expected: ~500ns (goroutine spawn overhead)
func BenchmarkStart(b *testing.B) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager, _ := createTestManager(b, mock)

		manager.Start()

		// Immediate stop (don't let it run)
		manager.Stop(100 * time.Millisecond)
	}
}

// BenchmarkStop benchmarks Stop() operation latency.
//
// Target: <3s (150% goal)
// Expected: ~2-5s (depends on active refresh)
func BenchmarkStop(b *testing.B) {
	b.Skip("Skipping slow benchmark (takes ~2-5s per iteration)")

	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager, _ := createTestManager(b, mock)
		manager.Start()

		time.Sleep(20 * time.Millisecond) // Let warmup + first refresh start

		b.StartTimer()
		manager.Stop(5 * time.Second)
		b.StopTimer()
	}
}

// BenchmarkRefreshNow benchmarks manual refresh trigger latency.
//
// Target: <50ms (150% goal)
// Expected: ~100ms (async trigger, immediate return)
func BenchmarkRefreshNow(b *testing.B) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(b, mock)
	manager.Start()
	defer manager.Stop(1 * time.Second)

	// Wait for warmup
	time.Sleep(20 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rate limited to 1/50ms in test config
		if i > 0 {
			time.Sleep(60 * time.Millisecond) // Wait for rate limit
		}

		manager.RefreshNow()

		// Wait for refresh to complete
		time.Sleep(10 * time.Millisecond)
	}
}

// BenchmarkGetStatus benchmarks status query latency.
//
// Target: <5ms (150% goal)
// Expected: ~5µs (read-only, O(1))
func BenchmarkGetStatus(b *testing.B) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(b, mock)
	manager.Start()
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh
	time.Sleep(50 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetStatus()
	}
}

// BenchmarkFullRefresh benchmarks end-to-end refresh operation.
//
// Target: <2s (150% goal)
// Expected: ~2s (depends on K8s API latency)
func BenchmarkFullRefresh(b *testing.B) {
	b.Skip("Skipping slow benchmark (takes ~2s per iteration)")

	mock := &MockTargetDiscoveryManager{
		targetCount: 10,
		shouldFail:  false,
		delayDuration: 100 * time.Millisecond, // Simulate K8s API latency
	}

	manager, _ := createTestManager(b, mock)
	manager.Start()
	defer manager.Stop(1 * time.Second)

	// Wait for warmup
	time.Sleep(20 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rate limited
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		b.StartTimer()
		manager.RefreshNow()

		// Wait for completion
		waitForRefresh(b, manager, 5*time.Second)
		b.StopTimer()
	}
}

// BenchmarkConcurrentGetStatus benchmarks concurrent GetStatus reads.
//
// Target: <100ns/op (150% goal)
// Expected: ~50ns/op (in-memory read with RLock)
func BenchmarkConcurrentGetStatus(b *testing.B) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(b, mock)
	manager.Start()
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh
	time.Sleep(50 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = manager.GetStatus()
		}
	})
}
