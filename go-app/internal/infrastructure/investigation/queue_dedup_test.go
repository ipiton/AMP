package investigation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/investigation"
)

// countingLLM counts investigations and always succeeds.
type countingLLM struct{ calls atomic.Int32 }

func (c *countingLLM) InvestigateAlert(_ context.Context, _ *core.Alert, _ *core.ClassificationResult) (*core.InvestigationResult, error) {
	c.calls.Add(1)
	return &core.InvestigationResult{Summary: "ok", Confidence: 1}, nil
}

func (r *mockRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dedupQueue(repo core.InvestigationRepository, llm investigation.InvestigationLLMClient, reg *prometheus.Registry) *investigation.InvestigationQueue {
	cfg := investigation.DefaultQueueConfig()
	cfg.WorkerCount = 1
	cfg.MaxRetries = 0
	cfg.LLMTimeout = time.Second
	return investigation.NewInvestigationQueue(repo, llm, cfg, nil, reg)
}

func dedupCount(t *testing.T, reg *prometheus.Registry, level string) float64 {
	return counterValue(t, reg, "amp_investigations_deduplicated_total", level)
}

// counterValue reads a counter from reg; level "" matches an unlabelled one.
func counterValue(t *testing.T, reg *prometheus.Registry, name, level string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if level == "" {
				return m.GetCounter().GetValue()
			}
			for _, l := range m.GetLabel() {
				if l.GetName() == "level" && l.GetValue() == level {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// failOnceRepo fails the first Create, then behaves like mockRepo.
type failOnceRepo struct {
	*mockRepo
	failed atomic.Bool
}

func (r *failOnceRepo) Create(ctx context.Context, inv *core.Investigation) error {
	if r.failed.CompareAndSwap(false, true) {
		return errors.New("db down")
	}
	return r.mockRepo.Create(ctx, inv)
}

func TestSubmit_DoesNotTouchRepository(t *testing.T) {
	repo := newMockRepo()
	q := dedupQueue(repo, &countingLLM{}, prometheus.NewRegistry())

	// Workers not started: whatever Submit does happens on this goroutine.
	q.Submit(makeAlert(), nil)

	if n := repo.count(); n != 0 {
		t.Errorf("Submit wrote %d records; the record belongs to the worker", n)
	}
	if q.QueueDepth() != 1 {
		t.Errorf("queue depth = %d, want 1", q.QueueDepth())
	}
	_ = q.Stop(100 * time.Millisecond)
}

func TestSubmit_ResendOfIncidentDeduplicated(t *testing.T) {
	repo := newMockRepo()
	llm := &countingLLM{}
	reg := prometheus.NewRegistry()
	q := dedupQueue(repo, llm, reg)
	q.Start()

	alert := makeAlert()
	for i := 0; i < 5; i++ {
		resend := *alert // same fingerprint and StartsAt, new object
		q.Submit(&resend, nil)
	}
	waitFor(t, func() bool { return llm.calls.Load() == 1 })

	refire := *alert
	refire.StartsAt = alert.StartsAt.Add(time.Hour)
	q.Submit(&refire, nil)
	waitFor(t, func() bool { return llm.calls.Load() == 2 })
	_ = q.Stop(2 * time.Second)

	if got := llm.calls.Load(); got != 2 {
		t.Errorf("investigations = %d, want 2 (one per incident)", got)
	}
	if got := dedupCount(t, reg, "local"); got != 4 {
		t.Errorf("local dedup count = %v, want 4", got)
	}
}

func TestSubmit_FullQueueReleasesClaim(t *testing.T) {
	repo := newMockRepo()
	reg := prometheus.NewRegistry()
	cfg := investigation.DefaultQueueConfig()
	cfg.QueueSize = 1
	q := investigation.NewInvestigationQueue(repo, &countingLLM{}, cfg, nil, reg)

	q.Submit(makeAlert(), nil) // fills the queue

	other := makeAlert()
	other.Fingerprint = "other"
	q.Submit(other, nil) // dropped: queue full
	if q.QueueDepth() != 1 {
		t.Fatalf("queue depth = %d, want 1", q.QueueDepth())
	}

	// The drop released the claim: once there is room, a resend gets in
	// instead of being deduplicated against a job that never ran.
	q.Start()
	waitFor(t, func() bool { return q.QueueDepth() == 0 })
	q.Submit(other, nil)
	waitFor(t, func() bool { return repo.count() == 2 })
	_ = q.Stop(2 * time.Second)

	if got := dedupCount(t, reg, "local"); got != 0 {
		t.Errorf("resend after a drop was deduplicated (%v)", got)
	}
}

func TestQueue_SharedDedupAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	repo := newMockRepo()
	llm := &countingLLM{}
	regA, regB := prometheus.NewRegistry(), prometheus.NewRegistry()

	a := dedupQueue(repo, llm, regA)
	b := dedupQueue(repo, llm, regB)
	a.SetSharedDedup(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	b.SetSharedDedup(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	a.Start()
	b.Start()

	alert := makeAlert()
	copyA, copyB := *alert, *alert
	a.Submit(&copyA, nil)
	waitFor(t, func() bool { return llm.calls.Load() == 1 })
	b.Submit(&copyB, nil)
	waitFor(t, func() bool { return dedupCount(t, regB, "shared") == 1 })
	_ = a.Stop(2 * time.Second)
	_ = b.Stop(2 * time.Second)

	if got := llm.calls.Load(); got != 1 {
		t.Errorf("investigations = %d, want 1 across replicas", got)
	}
	if repo.count() != 1 {
		t.Errorf("records = %d, want 1: the losing replica must not create one", repo.count())
	}
	keys := mr.Keys()
	if len(keys) != 1 {
		t.Fatalf("redis keys = %v, want one claim", keys)
	}
	// The finished job lifted its claim to the full TTL.
	if ttl := mr.TTL(keys[0]); ttl != 24*time.Hour {
		t.Errorf("claim TTL after the job = %v, want 24h", ttl)
	}
}

func TestQueue_RedisDownFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	repo := newMockRepo()
	llm := &countingLLM{}
	q := dedupQueue(repo, llm, prometheus.NewRegistry())
	q.SetSharedDedup(redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1}))
	mr.Close()
	q.Start()

	q.Submit(makeAlert(), nil)
	waitFor(t, func() bool { return llm.calls.Load() == 1 })
	_ = q.Stop(2 * time.Second)
}

func TestQueue_CreateErrorReleasesClaims(t *testing.T) {
	mr := miniredis.RunT(t)
	repo := &failOnceRepo{mockRepo: newMockRepo()}
	llm := &countingLLM{}
	reg := prometheus.NewRegistry()
	q := dedupQueue(repo, llm, reg)
	q.SetSharedDedup(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	q.Start()

	alert := makeAlert()
	first := *alert
	q.Submit(&first, nil)
	waitFor(t, func() bool { return counterValue(t, reg, "amp_investigations_dropped_total", "") == 1 })
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("shared claim kept after a failed Create: %v", keys)
	}

	// The database is back: the next resend investigates.
	resend := *alert
	q.Submit(&resend, nil)
	waitFor(t, func() bool { return llm.calls.Load() == 1 })
	_ = q.Stop(2 * time.Second)
}
