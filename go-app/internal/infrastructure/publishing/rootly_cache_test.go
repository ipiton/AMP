package publishing

import (
	"runtime"
	"testing"
	"time"
)

// TestIncidentIDCache_StopStopsCleanupWorker is the regression test for item
// 2 (FU-WAVE3-RELIABILITY): inMemoryIncidentCache already implemented a
// working Stop() with a stopChan/select loop, but it was declared only on
// the concrete type, not on the IncidentIDCache interface every caller
// actually holds (PublisherFactory.rootlyCache is typed IncidentIDCache) —
// so nothing could ever call it, and PublisherFactory.Shutdown() never
// tried to. Every factory instance leaked this goroutine for the life of
// the process. Now that Stop() is on the interface and wired into
// Shutdown(), verify it actually terminates the goroutine.
//
// goleak isn't a direct dependency of this module (only pulled in
// transitively via go.sum), so this uses a before/after runtime.NumGoroutine
// bound with a short poll, per the task brief's documented fallback.
func TestIncidentIDCache_StopStopsCleanupWorker(t *testing.T) {
	runtime.Gosched()
	baseline := runtime.NumGoroutine()

	cache := NewIncidentIDCache(24 * time.Hour)

	var afterStart int
	for i := 0; i < 100; i++ {
		afterStart = runtime.NumGoroutine()
		if afterStart > baseline {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if afterStart <= baseline {
		t.Fatalf("expected goroutine count to rise above baseline %d after starting cache, got %d", baseline, afterStart)
	}

	cache.Stop()

	var afterStop int
	ok := false
	for i := 0; i < 200; i++ {
		afterStop = runtime.NumGoroutine()
		if afterStop <= baseline {
			ok = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("expected goroutine count to return to baseline %d after Stop, got %d", baseline, afterStop)
	}

	// Calling Stop again must not panic (double-close guard).
	cache.Stop()
}
