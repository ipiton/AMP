// This file is a port of the construction half of
// github.com/prometheus/alertmanager@v0.34.1/app/{app.go,cluster.go,metrics.go,lifecycle.go}
// without the UI and the network listeners (see doc.go):
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
	"github.com/prometheus/alertmanager/cluster"
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

	// Cluster defaults, as in upstream app.DefaultOptions.
	DefaultClusterAddr   = "0.0.0.0:9094"
	DefaultPeerTimeout   = 15 * time.Second
	DefaultSettleTimeout = cluster.DefaultPushPullInterval
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

	// Cluster, as in upstream app.Options. An empty ClusterBindAddr leaves
	// the gossip cluster off; zero durations fall back to the upstream
	// defaults (DefaultPeerTimeout, DefaultSettleTimeout, cluster.Default*).
	ClusterBindAddr        string
	ClusterAdvertiseAddr   string
	ClusterPeerName        string
	Peers                  []string
	PeerTimeout            time.Duration
	PeersResolveTimeout    time.Duration
	GossipInterval         time.Duration
	PushPullInterval       time.Duration
	TCPTimeout             time.Duration
	ProbeTimeout           time.Duration
	ProbeInterval          time.Duration
	SettleTimeout          time.Duration
	ReconnectInterval      time.Duration
	PeerReconnectTimeout   time.Duration
	TLSConfigFile          string
	AllowInsecureAdvertise bool
	Label                  string

	Logger *slog.Logger
	// Registerer receives the upstream alertmanager_* metrics. /metrics
	// serves the default gatherer (upstream httpserver), so a binary passes
	// prometheus.DefaultRegisterer.
	Registerer prometheus.Registerer

	// AMP extensions; both optional, nil keeps the upstream behaviour.
	//
	// NewAlertCallback builds an alert-store callback that runs after the
	// silencer's (provider/mem calls it under its lock: it must not block).
	// It receives the silencer's Mutes to check silences off that path.
	NewAlertCallback func(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback
	// NotifyStage runs ahead of every receiver's notification pipeline.
	NotifyStage notify.Stage
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

	// mtx makes a reload and Stop mutually exclusive: a reload that ran
	// after the teardown would start a new dispatcher and inhibitor over the
	// stopped alert provider and leak them (upstream guards the same with
	// App.mtx and routerDone). stopped is guarded by mtx.
	mtx     sync.Mutex
	stopped bool

	// cleanups is drained in reverse by Stop, mirroring the creation order.
	cleanups []func()
}

var errStopped = errors.New("upstreamcore: stopped")

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
		{&o.PeerTimeout, DefaultPeerTimeout},
		{&o.PeersResolveTimeout, cluster.DefaultResolvePeersTimeout},
		{&o.GossipInterval, cluster.DefaultGossipInterval},
		{&o.PushPullInterval, cluster.DefaultPushPullInterval},
		{&o.TCPTimeout, cluster.DefaultTCPTimeout},
		{&o.ProbeTimeout, cluster.DefaultProbeTimeout},
		{&o.ProbeInterval, cluster.DefaultProbeInterval},
		{&o.SettleTimeout, DefaultSettleTimeout},
		{&o.ReconnectInterval, cluster.DefaultReconnectInterval},
		{&o.PeerReconnectTimeout, cluster.DefaultReconnectTimeout},
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

	tlsTransportConfig, err := cluster.GetTLSTransportConfig(opts.TLSConfigFile)
	if err != nil {
		return fmt.Errorf("unable to initialize TLS transport configuration for gossip mesh: %w", err)
	}

	var (
		peer *cluster.Peer
		// settleCancel cancels the settle context once Settle starts; it is
		// a no-op until then so the teardown below can be registered right
		// after the peer exists.
		settleCancel = func() {}
	)
	if opts.ClusterBindAddr != "" {
		peer, err = cluster.Create(
			logger.With("component", "cluster"),
			reg,
			opts.ClusterBindAddr,
			opts.ClusterAdvertiseAddr,
			opts.Peers,
			true,
			opts.PushPullInterval,
			opts.GossipInterval,
			opts.TCPTimeout,
			opts.PeersResolveTimeout,
			opts.ProbeTimeout,
			opts.ProbeInterval,
			tlsTransportConfig,
			opts.AllowInsecureAdvertise,
			opts.Label,
			opts.ClusterPeerName,
		)
		if err != nil {
			return fmt.Errorf("unable to initialize gossip mesh: %w", err)
		}
		// Registered first, so it runs last: the peer leaves after the
		// dispatcher and the final snapshots, as upstream's does.
		c.onStop(func() {
			settleCancel()
			if err := peer.Leave(10 * time.Second); err != nil {
				logger.Error("unable to leave gossip mesh", "err", err)
			}
		})
		m.clusterEnabled.Set(1)
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
	if peer != nil {
		ch := peer.AddState("nfl", notificationLog, reg)
		notificationLog.SetBroadcast(ch.Broadcast)
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
	if peer != nil {
		ch := peer.AddState("sil", silences, reg)
		silences.SetBroadcast(ch.Broadcast)
	}
	wg.Go(func() {
		silences.Maintenance(opts.MaintenanceInterval, filepath.Join(opts.DataDir, "silences"), stopc, nil)
	})

	silencer := silence.NewSilencer(silences, logger, eventRec)

	// Both states are registered, so join and pull the initial state. The
	// event recorder's SetClusterPeer is skipped: the port has no recorder.
	if peer != nil {
		if err := peer.Join(opts.ReconnectInterval, opts.PeerReconnectTimeout); err != nil {
			logger.Warn("unable to join gossip mesh", "err", err)
		}
		settleCtx, cancel := context.WithTimeout(context.Background(), opts.SettleTimeout)
		settleCancel = cancel // observed by the teardown registered above.
		go peer.Settle(settleCtx, opts.GossipInterval*10)
	}

	// The silencer is the provider's AlertStoreCallback, exactly as
	// upstream wires it; an AMP callback composes after it.
	var alertCallback mem.AlertStoreCallback = silencer
	if opts.NewAlertCallback != nil {
		alertCallback = chainCallback{silencer, opts.NewAlertCallback(silencer.Mutes)}
	}
	alerts, err := mem.NewAlerts(
		context.Background(),
		opts.AlertGCInterval,
		0,
		alertCallback,
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

	// An interface holding a nil pointer is non-nil, so pass an empty
	// interface when the cluster is off; the API detects that.
	var clusterPeer cluster.ClusterPeer
	if peer != nil {
		clusterPeer = peer
	}

	apih, err := api.New(api.Options{
		Alerts:          alerts,
		Silences:        silences,
		GroupMutedFunc:  groupMarker.Muted,
		Peer:            clusterPeer,
		Logger:          logger.With("component", "api"),
		Registry:        reg,
		RequestDuration: m.requestDuration,
		GroupFunc:       groupFn,
	})
	if err != nil {
		return fmt.Errorf("failed to create API: %w", err)
	}

	// A peer waits one PeerTimeout per peer ahead of it before notifying,
	// so the first one's nflog entry reaches it first (upstream
	// app/cluster.go clusterWait).
	waitFunc := func() time.Duration { return 0 }
	if peer != nil {
		waitFunc = func() time.Duration {
			return time.Duration(peer.Position()) * opts.PeerTimeout
		}
	}
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
		peer:                        peer,
		pipelineBuilder:             notify.NewPipelineBuilder(reg, ff, eventRec),
		retention:                   opts.Retention,
		notifyStage:                 opts.NotifyStage,
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

// chainCallback runs the upstream callback, then the AMP one. A PreStore
// error from either rejects the alert, as it would on its own.
type chainCallback struct {
	first, second mem.AlertStoreCallback
}

func (c chainCallback) PreStore(a *alert.Alert, existing bool) error {
	if err := c.first.PreStore(a, existing); err != nil {
		return err
	}
	return c.second.PreStore(a, existing)
}

func (c chainCallback) PostStore(a *alert.Alert, existing bool) {
	c.first.PostStore(a, existing)
	c.second.PostStore(a, existing)
}

func (c chainCallback) PostDelete(a *alert.Alert) {
	c.first.PostDelete(a)
	c.second.PostDelete(a)
}

func (c chainCallback) PostGC(fps model.Fingerprints) {
	c.first.PostGC(fps)
	c.second.PostGC(fps)
}

// serveReloads runs every reload request in turn until Stop.
func (c *Core) serveReloads() {
	for {
		select {
		case <-c.quit:
			return
		case errc := <-c.reloadReqs:
			errc <- c.reload()
		}
	}
}

func (c *Core) reload() error {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.stopped {
		return errStopped
	}
	return c.coordinator.Reload()
}

// Handler serves api/v2, /-/healthy, /-/ready, /-/reload, /metrics and
// /debug/* under the external URL's path. The caller must stop serving it
// before Stop: after Stop nothing answers /-/reload, whose upstream handler
// waits for an answer unconditionally.
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
		return errStopped
	}
}

// Stop stops the dispatcher and inhibitor, then the remaining components in
// reverse creation order, writing the final silence and nflog snapshots.
// The caller shuts its HTTP server down first (see Handler). Stop waits for
// a reload in progress and is idempotent.
func (c *Core) Stop() {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	close(c.quit)
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}
}

// metrics are the process-level metrics upstream's app package owns.
type metrics struct {
	requestDuration           *prometheus.HistogramVec
	responseSize              *prometheus.HistogramVec
	clusterEnabled            prometheus.Gauge
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
		// Set to 1 by setup when ClusterBindAddr starts the gossip cluster.
		clusterEnabled: f.NewGauge(
			prometheus.GaugeOpts{
				Name: "alertmanager_cluster_enabled",
				Help: "Indicates whether the clustering is enabled or not.",
			},
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
