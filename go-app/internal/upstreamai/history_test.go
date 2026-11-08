package upstreamai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// recorder collects the events History writes; with gate set, each write
// blocks until the gate is closed or the context ends.
type recorder struct {
	mu     sync.Mutex
	events []*core.Alert
	err    error
	gate   chan struct{}
}

func (r *recorder) record(ctx context.Context, a *core.Alert) error {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, a)
	return r.err
}

func (r *recorder) got() []*core.Alert {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*core.Alert(nil), r.events...)
}

func newTestHistory(t *testing.T, r *recorder, cfg HistoryConfig) *History {
	t.Helper()
	cfg.Record = r.record
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Registerer = prometheus.NewRegistry()
	h := NewHistory(cfg)
	t.Cleanup(h.Stop)
	return h
}

func writes(h *History, o string) float64 {
	return testutil.ToFloat64(h.writes.WithLabelValues(o))
}

func TestHistoryWritesEventsInPutOrderWithAMPKey(t *testing.T) {
	r := &recorder{}
	h := newTestHistory(t, r, HistoryConfig{})

	f, res := firing("DiskFull"), resolved("DiskFull")
	h.PostStore(f, false)
	h.PostStore(f, true)
	h.PostStore(res, true)

	require.Eventually(t, func() bool { return len(r.got()) == 3 }, 5*time.Second, 5*time.Millisecond)
	got := r.got()
	assert.Equal(t, []core.AlertStatus{core.StatusFiring, core.StatusFiring, core.StatusResolved},
		[]core.AlertStatus{got[0].Status, got[1].Status, got[2].Status}, "one alert's events in the order of its Puts")
	want := alertconv.Fingerprint(labelMap(f.Labels))
	for _, a := range got {
		assert.Equal(t, want, a.Fingerprint, "cmd/server's SHA-256 key")
	}
	require.NotNil(t, got[2].EndsAt)
	assert.Equal(t, 3.0, writes(h, "written"))
}

func TestHistoryPostStoreNeverBlocksOnFullShard(t *testing.T) {
	r := &recorder{gate: make(chan struct{})}
	defer close(r.gate)
	h := newTestHistory(t, r, HistoryConfig{Shards: 1, ShardSize: 1})

	start := time.Now()
	for i := range 50 {
		h.PostStore(firing(fmt.Sprintf("A%d", i)), false)
	}
	assert.Less(t, time.Since(start), time.Second, "PostStore runs under the provider lock and must not block")
	assert.GreaterOrEqual(t, writes(h, "dropped_queue_full"), 48.0, "one in flight, one queued, the rest dropped")
}

func TestHistoryWriteErrorAndTimeoutAreCounted(t *testing.T) {
	r := &recorder{err: errors.New("pg down")}
	h := newTestHistory(t, r, HistoryConfig{})
	h.PostStore(firing("DiskFull"), false)
	require.Eventually(t, func() bool { return writes(h, "error") == 1 }, 5*time.Second, 5*time.Millisecond)

	stuck := &recorder{gate: make(chan struct{})}
	defer close(stuck.gate)
	h = newTestHistory(t, stuck, HistoryConfig{WriteTimeout: 20 * time.Millisecond})
	h.PostStore(firing("DiskFull"), false)
	require.Eventually(t, func() bool { return writes(h, "error") == 1 }, 5*time.Second, 5*time.Millisecond,
		"a stuck write gives up at the write timeout")
}

func TestHistoryAcceptsEverythingAndOutlivesTheStore(t *testing.T) {
	r := &recorder{}
	h := newTestHistory(t, r, HistoryConfig{})

	assert.NoError(t, h.PreStore(firing("DiskFull"), false))
	h.PostGC(model.Fingerprints{firing("DiskFull").Fingerprint()})
	h.PostDelete(firing("StillFiring"))

	// An alert that resolved by its EndsAt passing gets its resolved event
	// when the store collects it.
	h.PostDelete(resolved("TimedOut"))
	require.Eventually(t, func() bool { return len(r.got()) == 1 }, 5*time.Second, 5*time.Millisecond)
	got := r.got()[0]
	assert.Equal(t, "TimedOut", got.AlertName)
	assert.Equal(t, core.StatusResolved, got.Status)
}

func TestHistoryStopLeavesLatePutsHarmless(t *testing.T) {
	r := &recorder{}
	h := NewHistory(HistoryConfig{Record: r.record, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	h.Stop()
	assert.NotPanics(t, func() { h.PostStore(firing("Late"), false) })
}
