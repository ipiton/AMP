package services

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // FilterMetrics has no pkg/metrics/v2 equivalent yet
)

func dedupTestAlert(fp string, status core.AlertStatus) *core.Alert {
	return &core.Alert{
		Fingerprint: fp,
		AlertName:   "ProdAlert",
		Status:      status,
		Labels:      map[string]string{},
		StartsAt:    time.Now(),
	}
}

func TestSimpleFilterEngine_Dedup_BlocksRepeatWithinWindow(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	first := dedupTestAlert("dup-fp", core.StatusFiring)
	if blocked, _ := engine.ShouldBlock(first, nil); blocked {
		t.Fatal("first delivery must pass")
	}

	repeat := dedupTestAlert("dup-fp", core.StatusFiring)
	blocked, reason := engine.ShouldBlock(repeat, nil)
	if !blocked {
		t.Fatal("repeat within window must be blocked")
	}
	if reason != "duplicate" {
		t.Fatalf("expected reason=duplicate, got %q", reason)
	}
}

func TestSimpleFilterEngine_Dedup_StatusChangePasses(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-status", core.StatusFiring), nil); blocked {
		t.Fatal("firing must pass")
	}
	if blocked, reason := engine.ShouldBlock(dedupTestAlert("fp-status", core.StatusResolved), nil); blocked {
		t.Fatalf("firing -> resolved transition must pass, blocked with reason %q", reason)
	}
}

func TestSimpleFilterEngine_Dedup_ExpiresAfterWindow(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)
	engine.SetDedupWindow(50 * time.Millisecond)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-exp", core.StatusFiring), nil); blocked {
		t.Fatal("first delivery must pass")
	}

	time.Sleep(60 * time.Millisecond)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-exp", core.StatusFiring), nil); blocked {
		t.Fatal("delivery after window expiry must pass")
	}
}

func TestSimpleFilterEngine_Dedup_DisabledWithNonPositiveWindow(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)
	engine.SetDedupWindow(0)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-off", core.StatusFiring), nil); blocked {
		t.Fatal("first delivery must pass")
	}
	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-off", core.StatusFiring), nil); blocked {
		t.Fatal("dedup disabled: repeat must pass")
	}
}

func TestSimpleFilterEngine_Dedup_EmptyFingerprintNeverDeduped(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("", core.StatusFiring), nil); blocked {
		t.Fatal("empty fingerprint must not be deduped")
	}
	if blocked, _ := engine.ShouldBlock(dedupTestAlert("", core.StatusFiring), nil); blocked {
		t.Fatal("empty fingerprint repeat must not be deduped")
	}
}

// A flapping alert (firing -> resolved -> firing inside the window) must reach
// the receiver every time its status changes (PROD-HARDCODED-FILTER). Keying
// on fingerprint+status blocked the second firing against the first one and
// left the receiver on "resolved" while the alert fired.
func TestSimpleFilterEngine_Dedup_FlapWithinWindowPasses(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	for i, status := range []core.AlertStatus{core.StatusFiring, core.StatusResolved, core.StatusFiring} {
		if blocked, reason := engine.ShouldBlock(dedupTestAlert("fp-flap", status), nil); blocked {
			t.Fatalf("step %d (%s) must pass, blocked with reason %q", i, status, reason)
		}
	}

	// The last status is firing again: a plain repeat of it is still a duplicate.
	blocked, reason := engine.ShouldBlock(dedupTestAlert("fp-flap", core.StatusFiring), nil)
	if !blocked || reason != "duplicate" {
		t.Fatalf("repeat of the last status within window: want blocked/duplicate, got %v/%q", blocked, reason)
	}
}

// The window runs from the last delivery, not from the last attempt: a blocked
// repeat does not extend it.
func TestSimpleFilterEngine_Dedup_BlockedRepeatDoesNotExtendWindow(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)
	engine.SetDedupWindow(100 * time.Millisecond)

	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-ext", core.StatusFiring), nil); blocked {
		t.Fatal("first delivery must pass")
	}
	time.Sleep(60 * time.Millisecond)
	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-ext", core.StatusFiring), nil); !blocked {
		t.Fatal("repeat within window must be blocked")
	}
	time.Sleep(60 * time.Millisecond) // 120ms after the delivery, 60ms after the blocked repeat
	if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-ext", core.StatusFiring), nil); blocked {
		t.Fatal("window must expire 100ms after the delivery, not after the blocked repeat")
	}
}

// Concurrent deliveries of one fingerprint with alternating statuses: no race
// (run under -race), and the first call of each status always passes because
// nothing blocks a status change.
func TestSimpleFilterEngine_Dedup_ConcurrentStatusChanges(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		passed int
	)
	for i := 0; i < 64; i++ {
		status := core.StatusFiring
		if i%2 == 1 {
			status = core.StatusResolved
		}
		wg.Add(1)
		go func(status core.AlertStatus) {
			defer wg.Done()
			if blocked, _ := engine.ShouldBlock(dedupTestAlert("fp-conc", status), nil); !blocked {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}(status)
	}
	wg.Wait()

	if passed < 2 {
		t.Fatalf("at least the first firing and the first resolved must pass, got %d", passed)
	}
}

// Every block is visible as alert_history_filter_blocked_total{reason}.
func TestSimpleFilterEngine_Dedup_RecordsBlockedMetric(t *testing.T) {
	fm := &metrics.FilterMetrics{
		BlockedTotal:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "blocked_total"}, []string{"reason"}),
		FilteredTotal:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "filtered_total"}, []string{"result"}),
		FilterDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "duration_seconds"}, []string{"filter"}),
	}
	engine := NewSimpleFilterEngineWithMetrics(nil, fm)

	engine.ShouldBlock(dedupTestAlert("fp-metric", core.StatusFiring), nil)
	engine.ShouldBlock(dedupTestAlert("fp-metric", core.StatusFiring), nil)

	if got := testutil.ToFloat64(fm.BlockedTotal.WithLabelValues("duplicate")); got != 1 {
		t.Fatalf("blocked_total{reason=duplicate}: want 1, got %v", got)
	}
	if got := testutil.ToFloat64(fm.FilteredTotal.WithLabelValues("allowed")); got != 1 {
		t.Fatalf("filtered_total{result=allowed}: want 1, got %v", got)
	}
	if got := testutil.ToFloat64(fm.FilteredTotal.WithLabelValues("blocked")); got != 1 {
		t.Fatalf("filtered_total{result=blocked}: want 1, got %v", got)
	}
}
