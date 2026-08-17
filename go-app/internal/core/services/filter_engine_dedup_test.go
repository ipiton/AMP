package services

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
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
