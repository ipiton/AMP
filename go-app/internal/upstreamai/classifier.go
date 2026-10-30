// Package upstreamai hooks AMP's LLM classification into the upstream
// Alertmanager core wired by internal/upstreamcore (ADR-019, slice
// UPSTREAM-AI-CLASSIFY).
//
// The Classifier is an alert-store callback: PostStore queues every firing
// alert it has not seen for classification and returns at once, so ingest
// never waits for the LLM. Workers classify off that path; NotifyStage then
// waits, within a budget, for the classifications of the alerts it is about
// to notify and adds them as llm_* annotations to copies of those alerts.
package upstreamai

import (
	"context"
	"log/slog"
	"sync"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
)

// Defaults for Config.
const (
	DefaultWorkers   = 4
	DefaultQueueSize = 1024
)

// Service classifies one alert; services.ClassificationService satisfies it.
type Service interface {
	ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
}

// Config configures a Classifier. Zero Workers/QueueSize use the defaults.
type Config struct {
	Service    Service
	Logger     *slog.Logger
	Registerer prometheus.Registerer
	Workers    int
	QueueSize  int
}

// entry is one alert's classification. done is closed when it is final;
// result is written before that and is nil when the alert was skipped.
type entry struct {
	done   chan struct{}
	result *core.ClassificationResult
}

type job struct {
	fp     model.Fingerprint
	e      *entry
	labels model.LabelSet
	alert  *core.Alert
}

// Classifier classifies alerts off the ingest path. Create it with New,
// pass AlertCallback as upstreamcore.Options.NewAlertCallback and
// NotifyStage as Options.NotifyStage, and Stop it after the core.
type Classifier struct {
	svc    Service
	logger *slog.Logger
	jobs   chan job

	// mutes is set by AlertCallback before the store can call PostStore,
	// and read by workers only after receiving a job PostStore sent.
	mutes func(context.Context, model.LabelSet) bool

	mtx     sync.Mutex
	entries map[model.Fingerprint]*entry
	// queueFull logs one Warn per overflow episode instead of one per Put;
	// an episode ends when the queue drains below half.
	queueFull bool

	cancel context.CancelFunc
	wg     sync.WaitGroup

	classifications *prometheus.CounterVec
	waits           *prometheus.CounterVec
	noiseDropped    prometheus.Counter
}

// New starts the workers.
func New(cfg Config) *Classifier {
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	f := promauto.With(cfg.Registerer)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Classifier{
		svc:     cfg.Service,
		logger:  cfg.Logger.With("component", "upstreamai"),
		jobs:    make(chan job, cfg.QueueSize),
		entries: make(map[model.Fingerprint]*entry),
		cancel:  cancel,
		classifications: f.NewCounterVec(prometheus.CounterOpts{
			Name: "amp_upstream_ai_classifications_total",
			Help: "Alert classification attempts by outcome: classified (LLM), fallback (rule-based, LLM unavailable), error, skipped_silenced, dropped_queue_full (counted per Put, not per alert).",
		}, []string{"outcome"}),
		waits: f.NewCounterVec(prometheus.CounterOpts{
			Name: "amp_upstream_ai_notify_waits_total",
			Help: "Pending classifications a notification waited for, by outcome: ready, timeout.",
		}, []string{"outcome"}),
		noiseDropped: f.NewCounter(prometheus.CounterOpts{
			Name: "amp_upstream_ai_noise_dropped_total",
			Help: "Alerts left out of a notification because they were classified as noise (llm.drop_noise).",
		}),
	}
	for range cfg.Workers {
		c.wg.Go(func() { c.work(ctx) })
	}
	return c
}

// Stop cancels in-flight classifications and waits for the workers.
func (c *Classifier) Stop() {
	c.cancel()
	c.wg.Wait()
}

// AlertCallback matches upstreamcore.Options.NewAlertCallback.
func (c *Classifier) AlertCallback(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback {
	c.mutes = mutes
	return c
}

// PreStore accepts every alert; filtering belongs to slice 2b.
func (c *Classifier) PreStore(*alert.Alert, bool) error { return nil }

// PostStore queues a firing alert seen for the first time. It runs under
// the provider's lock, so it never blocks: with the queue full the alert is
// not recorded and the next Put of it tries again.
func (c *Classifier) PostStore(a *alert.Alert, _ bool) {
	if a.Resolved() {
		return
	}
	fp := a.Fingerprint()
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if _, ok := c.entries[fp]; ok {
		return
	}
	e := &entry{done: make(chan struct{})}
	select {
	case c.jobs <- job{fp: fp, e: e, labels: a.Labels.Clone(), alert: toCoreAlert(a)}:
		c.entries[fp] = e
		// Re-arm the Warn only once the queue has drained to half: at the
		// limit, every enqueue that slips through would log again.
		if len(c.jobs) < cap(c.jobs)/2 {
			c.queueFull = false
		}
	default:
		c.classifications.WithLabelValues("dropped_queue_full").Inc()
		if !c.queueFull {
			c.queueFull = true
			c.logger.Warn("classification queue full, alerts are not classified until it drains; the next Put of an alert retries",
				"queue_size", cap(c.jobs))
		}
	}
}

// PostDelete forgets a deleted alert.
func (c *Classifier) PostDelete(a *alert.Alert) {
	c.forget(a.Fingerprint())
}

// PostGC forgets garbage-collected alerts.
func (c *Classifier) PostGC(fps model.Fingerprints) {
	c.forget(fps...)
}

func (c *Classifier) forget(fps ...model.Fingerprint) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	for _, fp := range fps {
		delete(c.entries, fp)
	}
}

func (c *Classifier) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-c.jobs:
			c.classify(ctx, j)
		}
	}
}

func (c *Classifier) classify(ctx context.Context, j job) {
	defer close(j.e.done)
	if c.mutes != nil && c.mutes(ctx, j.labels) {
		c.classifications.WithLabelValues("skipped_silenced").Inc()
		return
	}
	res, err := c.svc.ClassifyAlert(ctx, j.alert)
	if err != nil {
		c.classifications.WithLabelValues("error").Inc()
		c.logger.Warn("alert classification failed", "alertname", j.alert.AlertName, "fingerprint", j.fp.String(), "err", err)
		// Forget the entry so the next Put of the alert retries.
		c.mtx.Lock()
		if c.entries[j.fp] == j.e {
			delete(c.entries, j.fp)
		}
		c.mtx.Unlock()
		return
	}
	j.e.result = res
	// With the LLM down the service answers from its rule-based fallback
	// without an error; the outcome keeps an outage visible.
	if fb, _ := res.Metadata["fallback"].(bool); fb {
		c.classifications.WithLabelValues("fallback").Inc()
		return
	}
	c.classifications.WithLabelValues("classified").Inc()
}

// lookup returns the alerts' entries, nil where there is none.
func (c *Classifier) lookup(alerts []*alert.Alert) []*entry {
	out := make([]*entry, len(alerts))
	c.mtx.Lock()
	defer c.mtx.Unlock()
	for i, a := range alerts {
		out[i] = c.entries[a.Fingerprint()]
	}
	return out
}
