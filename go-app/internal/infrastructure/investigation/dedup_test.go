package investigation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ipiton/AMP/internal/core"
)

func TestDedupKey_SecondsAndStatus(t *testing.T) {
	start := time.Date(2026, 10, 28, 10, 0, 0, 123456789, time.UTC)
	firing := &core.Alert{Fingerprint: "fp", Status: core.StatusFiring, StartsAt: start}

	// Storage keeps microseconds: a resend read back from it is the same incident.
	readBack := &core.Alert{Fingerprint: "fp", Status: core.StatusFiring, StartsAt: start.Truncate(time.Microsecond)}
	if dedupKey(firing) != dedupKey(readBack) {
		t.Errorf("sub-second precision split one incident: %q vs %q", dedupKey(firing), dedupKey(readBack))
	}

	resolved := &core.Alert{Fingerprint: "fp", Status: core.StatusResolved, StartsAt: start}
	if dedupKey(firing) == dedupKey(resolved) {
		t.Error("the resolve of an incident must not share the firing claim")
	}

	refire := &core.Alert{Fingerprint: "fp", Status: core.StatusFiring, StartsAt: start.Add(time.Hour)}
	if dedupKey(firing) == dedupKey(refire) {
		t.Error("a new StartsAt is a new incident")
	}
}

func TestDedup_ClaimLocalExpiresAndReleases(t *testing.T) {
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	d := newDedup()
	d.now = func() time.Time { return now }

	if !d.claimLocal("k") {
		t.Fatal("first claim must succeed")
	}
	if d.claimLocal("k") {
		t.Fatal("second claim within the TTL must fail")
	}

	d.releaseLocal("k")
	if !d.claimLocal("k") {
		t.Fatal("claim after release must succeed")
	}

	now = now.Add(dedupTTL)
	if !d.claimLocal("k") {
		t.Fatal("claim after the TTL must succeed")
	}
}

func TestDedup_ClaimLocalSweepsExpired(t *testing.T) {
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	d := newDedup()
	d.now = func() time.Time { return now }

	for _, k := range []string{"a", "b", "c"} {
		d.claimLocal(k)
	}
	now = now.Add(dedupTTL + dedupSweepInterval)
	d.claimLocal("d")

	if len(d.claimed) != 1 {
		t.Errorf("expired claims kept: %d entries, want 1", len(d.claimed))
	}
}

func TestDedup_SharedClaimTTLs(t *testing.T) {
	mr := miniredis.RunT(t)
	d := newDedup()
	d.rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d.inFlight = 10 * time.Minute
	ctx := context.Background()

	ok, err := d.claimShared(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("first shared claim: ok=%v err=%v", ok, err)
	}
	if got := mr.TTL(dedupKeyPrefix + "k"); got != 10*time.Minute {
		t.Errorf("in-flight TTL = %v, want 10m", got)
	}
	if ok, _ := d.claimShared(ctx, "k"); ok {
		t.Error("second shared claim must fail")
	}

	if err := d.extendShared(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if got := mr.TTL(dedupKeyPrefix + "k"); got != dedupTTL {
		t.Errorf("finished TTL = %v, want %v", got, dedupTTL)
	}

	// A job that outlived its in-flight claim restores it on extend.
	mr.Del(dedupKeyPrefix + "k")
	if err := d.extendShared(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists(dedupKeyPrefix + "k") {
		t.Error("extend after expiry must recreate the claim")
	}

	if err := d.releaseShared(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(dedupKeyPrefix + "k") {
		t.Error("release must drop the claim")
	}
}

func TestDedup_SharedClaimFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	d := newDedup()
	d.rdb = redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	d.inFlight = time.Minute
	mr.Close()

	ok, err := d.claimShared(context.Background(), "k")
	if err == nil {
		t.Fatal("expected the Redis error to be reported")
	}
	if !ok {
		t.Error("a Redis error must not block the investigation (fail-open)")
	}
}

func TestInFlightTTL_CoversWorstCaseJob(t *testing.T) {
	cfg := DefaultQueueConfig()
	got := inFlightTTL(cfg)
	if got < 15*time.Minute || got > 20*time.Minute {
		t.Errorf("default in-flight TTL = %v, want about 15m", got)
	}

	cfg.MaxRetries = 5
	cfg.LLMTimeout = 120 * time.Second
	worst := time.Duration(cfg.MaxRetries+1)*(cfg.LLMTimeout+60*time.Second) + 5*time.Minute
	if got := inFlightTTL(cfg); got <= worst {
		t.Errorf("in-flight TTL %v does not cover the worst-case job %v", got, worst)
	}
}
