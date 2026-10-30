package upstreamai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// fakeService answers with res/err; with gate set, each call blocks until
// the gate is closed or the context ends.
type fakeService struct {
	mu    sync.Mutex
	calls []*core.Alert
	res   *core.ClassificationResult
	err   error
	gate  chan struct{}
}

func (f *fakeService) ClassifyAlert(ctx context.Context, a *core.Alert) (*core.ClassificationResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, a)
	gate, res, err := f.gate, f.res, f.err
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return res, err
}

func (f *fakeService) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func warning() *core.ClassificationResult {
	return &core.ClassificationResult{
		Severity:        core.SeverityWarning,
		Confidence:      0.87,
		Reasoning:       "disk filling up",
		Recommendations: []string{"clean logs", "grow volume", "page owner", "ignored"},
	}
}

func newTestClassifier(t *testing.T, svc Service, cfg Config) (*Classifier, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	cfg.Service = svc
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Registerer = reg
	c := New(cfg)
	t.Cleanup(c.Stop)
	return c, reg
}

func firing(name string) *alert.Alert {
	return &alert.Alert{Alert: model.Alert{
		Labels:       model.LabelSet{model.AlertNameLabel: model.LabelValue(name), "instance": "db-1"},
		Annotations:  model.LabelSet{"summary": "disk"},
		StartsAt:     time.Now().Add(-time.Minute),
		GeneratorURL: "http://prom/graph",
	}}
}

func resolved(name string) *alert.Alert {
	a := firing(name)
	a.EndsAt = time.Now().Add(-time.Second)
	return a
}

func outcome(c *Classifier, o string) float64 {
	return testutil.ToFloat64(c.classifications.WithLabelValues(o))
}

func waitDone(t *testing.T, c *Classifier, a *alert.Alert) *entry {
	t.Helper()
	e := c.lookup([]*alert.Alert{a})[0]
	require.NotNil(t, e, "alert was not queued")
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		t.Fatal("classification did not finish")
	}
	return e
}

func TestPostStoreQueuesFiringOnceAndConverts(t *testing.T) {
	svc := &fakeService{res: warning()}
	c, _ := newTestClassifier(t, svc, Config{})
	c.AlertCallback(nil)

	a := firing("DiskFull")
	c.PostStore(a, false)
	e := waitDone(t, c, a)
	c.PostStore(a, true) // resend: already known

	assert.Equal(t, warning(), e.result)
	assert.Equal(t, 1, svc.callCount())
	assert.Equal(t, 1.0, outcome(c, "classified"))

	got := svc.calls[0]
	assert.Equal(t, "DiskFull", got.AlertName)
	assert.Equal(t, core.StatusFiring, got.Status)
	assert.Len(t, got.Fingerprint, 64, "AMP's SHA-256 fingerprint, not upstream's FNV")
	assert.Equal(t, "disk", got.Annotations["summary"])
	require.NotNil(t, got.GeneratorURL)
	assert.Equal(t, "http://prom/graph", *got.GeneratorURL)
	assert.Nil(t, got.EndsAt)
}

func TestPostStoreSkipsResolved(t *testing.T) {
	svc := &fakeService{res: warning()}
	c, _ := newTestClassifier(t, svc, Config{})

	c.PostStore(resolved("DiskFull"), false)

	assert.Nil(t, c.lookup([]*alert.Alert{resolved("DiskFull")})[0])
	assert.Zero(t, svc.callCount())
}

func TestPostStoreNeverBlocksOnFullQueue(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	defer close(svc.gate)
	c, _ := newTestClassifier(t, svc, Config{Workers: 1, QueueSize: 1})

	start := time.Now()
	for i := range 50 {
		c.PostStore(firing(fmt.Sprintf("A%d", i)), false)
	}
	assert.Less(t, time.Since(start), time.Second, "PostStore runs under the provider lock and must not block")
	assert.Positive(t, outcome(c, "dropped_queue_full"))

	// A dropped alert is not recorded, so its next Put can try again.
	entries := 0
	for i := range 50 {
		if c.lookup([]*alert.Alert{firing(fmt.Sprintf("A%d", i))})[0] != nil {
			entries++
		}
	}
	assert.LessOrEqual(t, entries, 2, "one in flight plus one queued")
}

func TestSilencedAlertIsSkipped(t *testing.T) {
	svc := &fakeService{res: warning()}
	c, _ := newTestClassifier(t, svc, Config{})
	c.AlertCallback(func(context.Context, model.LabelSet) bool { return true })

	a := firing("DiskFull")
	c.PostStore(a, false)
	e := waitDone(t, c, a)

	assert.Nil(t, e.result)
	assert.Zero(t, svc.callCount(), "a silenced alert must not cost an LLM call")
	assert.Equal(t, 1.0, outcome(c, "skipped_silenced"))
}

func TestClassificationErrorIsRetriedOnNextPut(t *testing.T) {
	svc := &fakeService{err: errors.New("llm down, fallback off")}
	c, _ := newTestClassifier(t, svc, Config{})

	a := firing("DiskFull")
	c.PostStore(a, false)
	waitDone(t, c, a)
	require.Eventually(t, func() bool { return c.lookup([]*alert.Alert{a})[0] == nil }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1.0, outcome(c, "error"))

	svc.mu.Lock()
	svc.err, svc.res = nil, warning()
	svc.mu.Unlock()
	c.PostStore(a, true)
	e := waitDone(t, c, a)
	assert.Equal(t, warning(), e.result)
	assert.Equal(t, 2, svc.callCount())
}

func TestFallbackResultHasItsOwnOutcome(t *testing.T) {
	res := warning()
	res.Metadata = map[string]any{"fallback": true}
	c, _ := newTestClassifier(t, &fakeService{res: res}, Config{})

	a := firing("DiskFull")
	c.PostStore(a, false)
	e := waitDone(t, c, a)

	assert.Equal(t, res, e.result, "the rule-based result is still used")
	assert.Equal(t, 1.0, outcome(c, "fallback"))
	assert.Zero(t, outcome(c, "classified"))
}

func TestPostDeleteAndPostGCForget(t *testing.T) {
	c, _ := newTestClassifier(t, &fakeService{res: warning()}, Config{})
	a, b := firing("A"), firing("B")
	c.PostStore(a, false)
	c.PostStore(b, false)
	waitDone(t, c, a)
	waitDone(t, c, b)

	c.PostDelete(a)
	c.PostGC(model.Fingerprints{b.Fingerprint()})

	assert.Equal(t, []*entry{nil, nil}, c.lookup([]*alert.Alert{a, b}))
}

func TestStopCancelsInFlightClassification(t *testing.T) {
	svc := &fakeService{gate: make(chan struct{})}
	c := New(Config{Service: svc, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	c.PostStore(firing("DiskFull"), false)
	require.Eventually(t, func() bool { return svc.callCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	done := make(chan struct{})
	go func() { c.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel the in-flight classification")
	}
}
