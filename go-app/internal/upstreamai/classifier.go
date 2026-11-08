// Package upstreamai hooks AMP's LLM classification into the upstream
// Alertmanager core wired by internal/upstreamcore (ADR-019, slice
// UPSTREAM-AI-CLASSIFY).
//
// The Classifier is an alert-store callback: PostStore queues every firing
// alert it has not seen for classification and returns at once, so ingest
// never waits for the LLM. Workers classify off that path; NotifyStage then
// waits, within a budget, for the classifications of the alerts it is about
// to notify and adds them as llm_* annotations to copies of those alerts.
// A classified alert is handed to the investigation queue; a silenced or
// inhibited one is not classified, and a Put of it after a recheck interval
// checks again.
//
// History, a second alert-store callback, writes every alert to AMP's
// PostgreSQL history (slice UPSTREAM-AI-HISTORY-INVESTIGATION). History rows,
// investigations and the classification cache share one key, AMP's SHA-256
// fingerprint of the labels (toCoreAlert), as on cmd/server.
package upstreamai

import (
	"context"
	"log/slog"
	"sync"
	"time"

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
	// DefaultMuteRecheck is how long a silenced or inhibited alert waits
	// before a Put of it checks its mutes again: about one Prometheus
	// resend, without a queue slot per resend of every muted alert.
	DefaultMuteRecheck = time.Minute
)

// Service classifies one alert; services.ClassificationService satisfies it.
type Service interface {
	ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
}

// Submitter queues an investigation of a classified alert without blocking;
// investigation.InvestigationQueue satisfies it.
type Submitter interface {
	Submit(alert *core.Alert, classification *core.ClassificationResult)
}

// Config configures a Classifier. Zero Workers/QueueSize use the defaults.
type Config struct {
	Service Service
	// Investigations, when set, receives every classified alert, with the
	// same *core.Alert (and so the same fingerprint) the history gets.
	Investigations Submitter
	Logger         *slog.Logger
	Registerer     prometheus.Registerer
	Workers        int
	QueueSize      int
	MuteRecheck    time.Duration
}

// entry is one alert's classification. done is closed when it is final;
// result is written before that and is nil when the alert was skipped.
// recheckAt, guarded by Classifier.mtx, is set when the alert was muted:
// from then on a Put of it classifies it anew.
type entry struct {
	done      chan struct{}
	result    *core.ClassificationResult
	recheckAt time.Time
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
	inv    Submitter
	logger *slog.Logger
	jobs   chan job

	muteRecheck time.Duration
	now         func() time.Time

	// mutes is set by AlertCallback before the store can call PostStore,
	// and read by workers only after receiving a job PostStore sent. It
	// covers silences and the active configuration's inhibition.
	mutes func(context.Context, model.LabelSet) bool

	mtx     sync.Mutex
	entries map[model.Fingerprint]*entry
	// queueFull logs one Warn per overflow episode instead of one per Put;
	// an episode ends when the queue drains below half.
	queueFull bool
	// stopped ends the resubmits from PostStore, so the investigation queue
	// can be stopped after Stop even if the store still takes a late Put.
	stopped bool

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
	if cfg.MuteRecheck <= 0 {
		cfg.MuteRecheck = DefaultMuteRecheck
	}
	f := promauto.With(cfg.Registerer)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Classifier{
		svc:     cfg.Service,
		inv:     cfg.Investigations,
		logger:  cfg.Logger.With("component", "upstreamai"),
		jobs:    make(chan job, cfg.QueueSize),
		entries: make(map[model.Fingerprint]*entry),
		cancel:  cancel,

		muteRecheck: cfg.MuteRecheck,
		now:         time.Now,
		classifications: f.NewCounterVec(prometheus.CounterOpts{
			Name: "amp_upstream_ai_classifications_total",
			Help: "Alert classification attempts by outcome: classified (LLM), fallback (rule-based, LLM unavailable), error, skipped_muted (silenced or inhibited; once per mute recheck), dropped_queue_full (counted per Put, not per alert).",
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

// Stop cancels in-flight classifications and waits for the workers. After
// it, the Classifier no longer submits investigations.
func (c *Classifier) Stop() {
	c.mtx.Lock()
	c.stopped = true
	c.mtx.Unlock()
	c.cancel()
	c.wg.Wait()
}

// AlertCallback matches upstreamcore.Options.NewAlertCallback.
func (c *Classifier) AlertCallback(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback {
	c.mutes = mutes
	return c
}

// PreStore accepts every alert. A rejection would take the alert out of the
// store — no notification, silence or inhibition, API — and every filter rule
// AMP has either needs the LLM or drops what upstream keeps; noise is left
// out at notification time instead (NotifyStage, llm.drop_noise).
func (c *Classifier) PreStore(*alert.Alert, bool) error { return nil }

// PostStore queues a firing alert seen for the first time, or muted when
// last checked longer than the mute recheck ago. A Put of a classified alert
// hands it to the investigation queue again, as every ingest does on
// cmd/server: the queue keeps one investigation per incident, retries one it
// had to drop, and starts one for an alert that fires anew (a new StartsAt)
// before the store forgets it. PostStore runs under the provider's lock, so
// it never blocks: with the queue full the alert is not recorded and the next
// Put of it tries again.
func (c *Classifier) PostStore(a *alert.Alert, _ bool) {
	if a.Resolved() {
		return
	}
	fp := a.Fingerprint()
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if e, ok := c.entries[fp]; ok {
		switch {
		case !e.recheckAt.IsZero():
			if c.now().Before(e.recheckAt) {
				return
			}
			// Muted when last checked: classify anew below.
		case c.inv != nil && !c.stopped && isDone(e) && e.result != nil:
			c.inv.Submit(toCoreAlert(a), e.result)
			return
		default:
			return
		}
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
	// mutes is safe off the store's path: the silencer touches only its own
	// locked cache, and the alert's silenced/inhibited status is set only
	// through a marker in the context, which a worker's context has none of.
	if c.mutes != nil && c.mutes(ctx, j.labels) {
		c.classifications.WithLabelValues("skipped_muted").Inc()
		// A Put after the recheck checks again, so the alert is classified
		// once its silence expires or its inhibitor resolves.
		c.mtx.Lock()
		j.e.recheckAt = c.now().Add(c.muteRecheck)
		c.mtx.Unlock()
		return
	}
	res, err := c.svc.ClassifyAlert(ctx, j.alert)
	if err != nil {
		c.classifications.WithLabelValues("error").Inc()
		c.logger.Warn("alert classification failed", "alertname", j.alert.AlertName, "fingerprint", j.fp.String(), "err", err)
		// Forget the entry so the next Put of the alert retries.
		c.forgetEntry(j)
		return
	}
	j.e.result = res
	if c.inv != nil {
		c.inv.Submit(j.alert, res)
	}
	// With the LLM down the service answers from its rule-based fallback
	// without an error; the outcome keeps an outage visible.
	if fb, _ := res.Metadata["fallback"].(bool); fb {
		c.classifications.WithLabelValues("fallback").Inc()
		return
	}
	c.classifications.WithLabelValues("classified").Inc()
}

// forgetEntry removes j's entry unless a newer one replaced it.
func (c *Classifier) forgetEntry(j job) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.entries[j.fp] == j.e {
		delete(c.entries, j.fp)
	}
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
