// This file is a port of the construction half of
// github.com/prometheus/alertmanager@v0.34.1/app/{app.go,metrics.go}
// without the UI, the gossip cluster and the network listeners (see doc.go):
//
//	Copyright The Prometheus Authors
//	Licensed under the Apache License, Version 2.0 (the "License");
//	you may not use this file except in compliance with the License.
//	You may obtain a copy of the License at
//
//	    http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//	See the License for the specific language governing permissions and
//	limitations under the License.

package upstreamcore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/api"
	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
	"github.com/prometheus/alertmanager/eventrecorder"
	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/httpserver"
	"github.com/prometheus/alertmanager/marker"
	"github.com/prometheus/alertmanager/nflog"
	"github.com/prometheus/alertmanager/notify"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/alertmanager/silence"
	"github.com/prometheus/alertmanager/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/model"
	"github.com/prometheus/common/route"
)

// Defaults match the upstream cmd/alertmanager flags.
const (
	DefaultRetention                   = 120 * time.Hour
	DefaultMaintenanceInterval         = 15 * time.Minute
	DefaultAlertGCInterval             = 30 * time.Minute
	DefaultDispatchMaintenanceInterval = 30 * time.Second
)

// Options configures the core. Zero durations fall back to the upstream
// defaults above.
type Options struct {
	ConfigFile string
	DataDir    string
	// ExternalURL is the URL Alertmanager is reachable under; its path is
	// the route prefix. It feeds templates (.ExternalURL) and links.
	ExternalURL *url.URL

	Retention                   time.Duration
	MaintenanceInterval         time.Duration
	AlertGCInterval             time.Duration
	DispatchMaintenanceInterval time.Duration

	Logger *slog.Logger
	// Registerer receives the upstream alertmanager_* metrics. /metrics
	// serves the default gatherer (upstream httpserver), so a binary passes
	// prometheus.DefaultRegisterer.
	Registerer prometheus.Registerer
}

// Core is a running upstream Alertmanager core without an HTTP server of
// its own: the caller serves Handler.
type Core struct {
	logger      *slog.Logger
	coordinator *config.Coordinator
	handler     http.Handler

	// reloadReqs serialises every reload — /-/reload and Reload — through
	// one goroutine, as upstream's reloadRouter does.
	reloadReqs chan chan error
	quit       chan struct{}

	// cleanups is drained in reverse by Stop, mirroring the creation order.
	cleanups []func()
	stopOnce sync.Once
}

// New builds the core from opts and applies the initial configuration. On
// error everything started so far is stopped again.
func New(opts Options) (*Core, error) {
	if err := opts.defaults(); err != nil {
		return nil, err
	}
	c := &Core{
		logger:     opts.Logger,
		reloadReqs: make(chan chan error),
		quit:       make(chan struct{}),
	}
	if err := c.setup(opts); err != nil {
		c.Stop()
		return nil, err
	}
	return c, nil
}

func (o *Options) defaults() error {
	if o.ConfigFile == "" {
		return errors.New("upstreamcore: Options.ConfigFile is required")
	}
	if o.DataDir == "" {
		return errors.New("upstreamcore: Options.DataDir is required")
	}
	if o.ExternalURL == nil {
		return errors.New("upstreamcore: Options.ExternalURL is required")
	}
	if o.Logger == nil {
		return errors.New("upstreamcore: Options.Logger is required")
	}
	if o.Registerer == nil {
		return errors.New("upstreamcore: Options.Registerer is required")
	}
	for _, d := range []struct {
		val *time.Duration
		def time.Duration
	}{
		{&o.Retention, DefaultRetention},
		{&o.MaintenanceInterval, DefaultMaintenanceInterval},
		{&o.AlertGCInterval, DefaultAlertGCInterval},
		{&o.DispatchMaintenanceInterval, DefaultDispatchMaintenanceInterval},
	} {
		if *d.val <= 0 {
			*d.val = d.def
		}
	}
	return nil
}

func (c *Core) onStop(fn func()) {
	c.cleanups = append(c.cleanups, fn)
}

// setup is one straight-line function for the same reason as upstream's:
// almost every step consumes locals produced by the earlier ones.
func (c *Core) setup(opts Options) error {
	logger := opts.Logger
	reg := opts.Registerer
	ff := featurecontrol.NoopFlags{}
	m := newMetrics(reg)
	startTime := time.Now()

	if err := os.MkdirAll(opts.DataDir, 0o777); err != nil {
		return fmt.Errorf("unable to create data directory: %w", err)
	}

	initialConf, err := config.LoadFile(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("error loading configuration file: %w", err)
	}

	// The event recorder is behind a feature flag upstream and off by
	// default; the zero value discards events.
	var eventRec eventrecorder.Recorder

	stopc := make(chan struct{})
	var wg sync.WaitGroup

	notificationLog, err := nflog.New(nflog.Options{
		SnapshotFile: filepath.Join(opts.DataDir, "nflog"),
		Retention:    opts.Retention,
		Logger:       logger.With("component", "nflog"),
		Metrics:      reg,
	})
	if err != nil {
		return fmt.Errorf("error creating notification log: %w", err)
	}
	wg.Go(func() {
		notificationLog.Maintenance(opts.MaintenanceInterval, filepath.Join(opts.DataDir, "nflog"), stopc, nil)
	})
	// Closing stopc makes the maintenance goroutines write a final snapshot
	// and exit; this covers the silence maintenance started below as well.
	c.onStop(func() {
		close(stopc)
		wg.Wait()
	})

	groupMarker := marker.NewGroupMarker()

	silences, err := silence.New(silence.Options{
		SnapshotFile:  filepath.Join(opts.DataDir, "silences"),
		Retention:     opts.Retention,
		Logger:        logger.With("component", "silences"),
		Metrics:       reg,
		EventRecorder: eventRec,
	})
	if err != nil {
		return fmt.Errorf("error creating silence: %w", err)
	}
	wg.Go(func() {
		silences.Maintenance(opts.MaintenanceInterval, filepath.Join(opts.DataDir, "silences"), stopc, nil)
	})

	silencer := silence.NewSilencer(silences, logger, eventRec)

	// The silencer is the provider's AlertStoreCallback, exactly as
	// upstream wires it; an AMP callback has to compose with it.
	alerts, err := mem.NewAlerts(
		context.Background(),
		opts.AlertGCInterval,
		0,
		silencer,
		logger,
		eventRec,
		reg,
		ff,
	)
	if err != nil {
		return fmt.Errorf("error creating memory provider: %w", err)
	}
	c.onStop(alerts.Close)

	// The API's GroupFunc closes over r, which is assigned below; it is
	// only called once the handler is serving.
	var r *reloader
	groupFn := func(ctx context.Context, routeFilter func(*dispatch.Route) bool, alertFilter func(*alert.Alert, time.Time) bool) (dispatch.AlertGroups, map[model.Fingerprint][]string, error) {
		return r.groups(ctx, routeFilter, alertFilter)
	}

	apih, err := api.New(api.Options{
		Alerts:          alerts,
		Silences:        silences,
		GroupMutedFunc:  groupMarker.Muted,
		Logger:          logger.With("component", "api"),
		Registry:        reg,
		RequestDuration: m.requestDuration,
		GroupFunc:       groupFn,
	})
	if err != nil {
		return fmt.Errorf("failed to create API: %w", err)
	}

	// Without a cluster there is no peer position to wait for.
	waitFunc := func() time.Duration { return 0 }
	timeoutFunc := func(d time.Duration) time.Duration {
		if d < notify.MinTimeout {
			d = notify.MinTimeout
		}
		return d + waitFunc()
	}

	tracingManager := tracing.NewManager(logger.With("component", "tracing"))
	c.onStop(tracingManager.Stop)

	c.coordinator = config.NewCoordinator(opts.ConfigFile, reg, logger.With("component", "configuration"))

	r = &reloader{
		alerts:                      alerts,
		apih:                        apih,
		dispatcherMetrics:           dispatch.NewDispatcherMetrics(false, reg, ff),
		dispatchMaintenanceInterval: opts.DispatchMaintenanceInterval,
		eventRecorder:               eventRec,
		externalURL:                 opts.ExternalURL,
		groupMarker:                 groupMarker,
		logger:                      logger,
		metrics:                     m,
		notificationLog:             notificationLog,
		pipelineBuilder:             notify.NewPipelineBuilder(reg, ff, eventRec),
		retention:                   opts.Retention,
		silencer:                    silencer,
		startTime:                   startTime,
		timeoutFunc:                 timeoutFunc,
		tracingMgr:                  tracingManager,
		waitFunc:                    waitFunc,
	}
	c.onStop(func() { _ = r.stop() })

	c.coordinator.Subscribe(r.reload)
	if err := c.coordinator.ApplyConfig(initialConf); err != nil {
		return fmt.Errorf("failed to apply initial configuration: %w", err)
	}

	// Run the tracing manager once; ApplyConfig swaps the provider in place
	// on every reload.
	go tracingManager.Run()

	go c.serveReloads()

	routePrefix := "/" + strings.Trim(opts.ExternalURL.Path, "/")
	router := route.New().WithInstrumentation(m.instrumentHandler)
	if routePrefix != "/" {
		router.Get("/", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, routePrefix, http.StatusFound)
		})
		router = router.WithPrefix(routePrefix)
	}
	httpserver.Register(router, c.reloadReqs)
	c.handler = tracing.Middleware(apih.Register(router, routePrefix))

	return nil
}

// serveReloads runs every reload request in turn until Stop.
func (c *Core) serveReloads() {
	for {
		select {
		case <-c.quit:
			return
		case errc := <-c.reloadReqs:
			errc <- c.coordinator.Reload()
		}
	}
}

// Handler serves api/v2, /-/healthy, /-/ready, /-/reload, /metrics and
// /debug/* under the external URL's path.
func (c *Core) Handler() http.Handler {
	return c.handler
}

// Reload re-reads the configuration file. A failed reload keeps the active
// configuration and returns the error.
func (c *Core) Reload() error {
	errc := make(chan error)
	select {
	case c.reloadReqs <- errc:
		return <-errc
	case <-c.quit:
		return errors.New("upstreamcore: stopped")
	}
}

// Stop stops the dispatcher and inhibitor, then the remaining components in
// reverse creation order, writing the final silence and nflog snapshots.
// The caller shuts its HTTP server down first. Stop is idempotent.
func (c *Core) Stop() {
	c.stopOnce.Do(func() {
		close(c.quit)
		for i := len(c.cleanups) - 1; i >= 0; i-- {
			c.cleanups[i]()
		}
	})
}

// metrics are the process-level metrics upstream's app package owns.
type metrics struct {
	requestDuration           *prometheus.HistogramVec
	responseSize              *prometheus.HistogramVec
	configuredReceivers       prometheus.Gauge
	configuredIntegrations    prometheus.Gauge
	configuredInhibitionRules prometheus.Gauge
}

func newMetrics(reg prometheus.Registerer) *metrics {
	f := promauto.With(reg)
	return &metrics{
		requestDuration: f.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:                            "alertmanager_http_request_duration_seconds",
				Help:                            "Histogram of latencies for HTTP requests.",
				Buckets:                         prometheus.DefBuckets,
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: 1 * time.Hour,
			},
			[]string{"handler", "method", "code"},
		),
		responseSize: f.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "alertmanager_http_response_size_bytes",
				Help:    "Histogram of response size for HTTP requests.",
				Buckets: prometheus.ExponentialBuckets(100, 10, 7),
			},
			[]string{"handler", "method"},
		),
		configuredReceivers: f.NewGauge(
			prometheus.GaugeOpts{
				Name: "alertmanager_receivers",
				Help: "Number of configured receivers.",
			},
		),
		configuredIntegrations: f.NewGauge(
			prometheus.GaugeOpts{
				Name: "alertmanager_integrations",
				Help: "Number of configured integrations.",
			},
		),
		configuredInhibitionRules: f.NewGauge(
			prometheus.GaugeOpts{
				Name: "alertmanager_inhibition_rules",
				Help: "Number of configured inhibition rules.",
			},
		),
	}
}

func (m *metrics) instrumentHandler(handlerName string, handler http.HandlerFunc) http.HandlerFunc {
	handlerLabel := prometheus.Labels{"handler": handlerName}
	return promhttp.InstrumentHandlerDuration(
		m.requestDuration.MustCurryWith(handlerLabel),
		promhttp.InstrumentHandlerResponseSize(
			m.responseSize.MustCurryWith(handlerLabel),
			handler,
		),
	)
}
