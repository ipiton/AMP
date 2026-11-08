package upstreamai

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
)

// Defaults for HistoryConfig.
const (
	DefaultHistoryShards       = 4
	DefaultHistoryShardSize    = 256
	DefaultHistoryWriteTimeout = 5 * time.Second
)

// HistoryConfig configures a History. Zero Shards/ShardSize/WriteTimeout use
// the defaults.
type HistoryConfig struct {
	// Record writes one alert event to the history. In amp-upstream it is
	// services.DeduplicationService.ProcessAlert, the path cmd/server takes.
	Record       func(context.Context, *core.Alert) error
	Logger       *slog.Logger
	Registerer   prometheus.Registerer
	Shards       int
	ShardSize    int
	WriteTimeout time.Duration
}

// History writes every alert the store accepts — firing and resolved,
// silenced and inhibited ones included — to AMP's alert history off the
// ingest path (ADR-019, slice UPSTREAM-AI-HISTORY-INVESTIGATION). It is an
// alert-store callback: PostStore hands the alert to the worker of its
// fingerprint's shard and returns at once, so one alert's events are written
// in the order of the Puts and none of them waits for the database.
//
// History is best-effort, like cmd/server's: an event that finds its shard
// full or fails to write is counted and lost, and the next Put of the alert
// (a Prometheus resend) brings the row up to date.
type History struct {
	record  func(context.Context, *core.Alert) error
	logger  *slog.Logger
	timeout time.Duration
	shards  []chan *core.Alert

	// queueFull logs one Warn per overflow episode, as Classifier does; an
	// episode ends once an event is queued to a shard less than half full.
	queueFull atomic.Bool

	cancel context.CancelFunc
	wg     sync.WaitGroup

	writes *prometheus.CounterVec
}

var _ mem.AlertStoreCallback = (*History)(nil)

// NewHistory starts one worker per shard.
func NewHistory(cfg HistoryConfig) *History {
	if cfg.Shards <= 0 {
		cfg.Shards = DefaultHistoryShards
	}
	if cfg.ShardSize <= 0 {
		cfg.ShardSize = DefaultHistoryShardSize
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = DefaultHistoryWriteTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &History{
		record:  cfg.Record,
		logger:  cfg.Logger.With("component", "upstreamai-history"),
		timeout: cfg.WriteTimeout,
		shards:  make([]chan *core.Alert, cfg.Shards),
		cancel:  cancel,
		writes: promauto.With(cfg.Registerer).NewCounterVec(prometheus.CounterOpts{
			Name: "amp_upstream_history_writes_total",
			Help: "Alert history writes by outcome: written, error, dropped_queue_full.",
		}, []string{"outcome"}),
	}
	for i := range h.shards {
		ch := make(chan *core.Alert, cfg.ShardSize)
		h.shards[i] = ch
		h.wg.Go(func() { h.work(ctx, ch) })
	}
	return h
}

// Stop cancels the write in flight and waits for the workers; events still
// queued are not written.
func (h *History) Stop() {
	h.cancel()
	h.wg.Wait()
}

// PreStore accepts every alert: the history rejects nothing.
func (h *History) PreStore(*alert.Alert, bool) error { return nil }

// PostStore queues the alert's event. It runs under the provider's lock, so
// it never blocks: with the shard full the event is dropped.
func (h *History) PostStore(a *alert.Alert, _ bool) {
	h.enqueue(a)
}

// PostDelete records the resolution of an alert the store garbage-collects
// once it has resolved: one that resolved by its EndsAt passing, without a
// resolved Put, has no resolved event otherwise. The history itself outlives
// the store.
func (h *History) PostDelete(a *alert.Alert) {
	if a.Resolved() {
		h.enqueue(a)
	}
}

// PostGC keeps the history: it outlives the store.
func (h *History) PostGC(model.Fingerprints) {}

func (h *History) enqueue(a *alert.Alert) {
	fp := a.Fingerprint()
	ca := toCoreAlert(a)
	shard := h.shards[uint64(fp)%uint64(len(h.shards))]
	select {
	case shard <- ca:
		// Re-arm the Warn only once the shard has drained to half: at the
		// limit, every event that slips through would log again.
		if len(shard) < cap(shard)/2 {
			h.queueFull.Store(false)
		}
	default:
		h.writes.WithLabelValues("dropped_queue_full").Inc()
		if h.queueFull.CompareAndSwap(false, true) {
			h.logger.Warn("history queue full, alert events are dropped until it drains; the next Put of an alert updates its row",
				"shard_size", cap(h.shards[0]))
		}
	}
}

func (h *History) work(ctx context.Context, ch <-chan *core.Alert) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-ch:
			h.write(ctx, a)
		}
	}
}

func (h *History) write(ctx context.Context, a *core.Alert) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	if err := h.record(ctx, a); err != nil {
		h.writes.WithLabelValues("error").Inc()
		h.logger.Warn("alert history write failed",
			"alertname", a.AlertName, "fingerprint", a.Fingerprint, "status", a.Status, "err", err)
		return
	}
	h.writes.WithLabelValues("written").Inc()
}
