// Command amp-upstream serves the upstream Alertmanager core wired by
// internal/upstreamcore from an unmodified alertmanager.yml (ADR-019 PoC).
// It runs next to cmd/server and changes nothing in it; flags mirror the
// upstream binary's names for the subset it supports. --amp.config.file
// adds AMP's LLM classification (internal/upstreamai) when llm.enabled.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/alertmanager/cluster"
	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/matcher/compat"
	"github.com/prometheus/client_golang/prometheus"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/common/version"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	"github.com/ipiton/AMP/internal/upstreamai"
	"github.com/ipiton/AMP/internal/upstreamcore"
)

// shutdownTimeout matches upstream's app.Stop.
const shutdownTimeout = 5 * time.Second

func main() {
	configFile := flag.String("config.file", "alertmanager.yml", "Alertmanager configuration file name.")
	dataDir := flag.String("storage.path", "data/", "Base path for data storage.")
	retention := flag.Duration("data.retention", upstreamcore.DefaultRetention, "How long to keep data for.")
	listenAddress := flag.String("web.listen-address", ":9093", "Address to listen on for the web interface and API.")
	externalURL := flag.String("web.external-url", "", "The URL under which Alertmanager is externally reachable. Its path prefixes all HTTP endpoints.")
	ampConfigFile := flag.String("amp.config.file", "", "AMP configuration file; its llm.* section enables alert classification. Empty: none.")
	clusterOpts := clusterFlags(flag.CommandLine)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Starting Alertmanager", "version", version.Info())
	logger.Info("Build context", "build_context", version.BuildContext())
	prometheus.MustRegister(versioncollector.NewCollector("alertmanager"))
	if err := run(logger, *configFile, *dataDir, *retention, *listenAddress, *externalURL, *ampConfigFile, *clusterOpts); err != nil {
		logger.Error("amp-upstream exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, configFile, dataDir string, retention time.Duration, listenAddress, externalURL, ampConfigFile string, clusterOpts upstreamcore.Options) error {
	// Matcher parsing is process-global upstream; without this call it stays
	// in classic mode instead of upstream's default fallback mode.
	compat.InitFromFlags(logger, featurecontrol.NoopFlags{})

	if err := validateClusterFlags(clusterOpts); err != nil {
		return err
	}
	// clusterOpts carries only the --cluster.* flags.
	opts := clusterOpts
	opts.ConfigFile = configFile
	opts.DataDir = dataDir
	opts.Retention = retention
	opts.Logger = logger
	opts.Registerer = prometheus.DefaultRegisterer
	if ampConfigFile != "" {
		classifier, err := newClassifier(logger, ampConfigFile, &opts)
		if err != nil {
			return err
		}
		// Deferred first, so it runs after core.Stop: the dispatcher is
		// gone before the workers stop.
		if classifier != nil {
			defer classifier.Stop()
		}
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return err
	}
	// http.Server owns the listener once Serve runs; until then close it on
	// every early return.
	amURL, err := extURL(os.Hostname, listener.Addr().String(), externalURL)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to determine external URL: %w", err)
	}

	opts.ExternalURL = amURL
	core, err := upstreamcore.New(opts)
	if err != nil {
		_ = listener.Close()
		return err
	}
	// Runs after the server is shut down below: the core must not stop
	// while its handler still serves (upstreamcore.Core.Handler).
	defer core.Stop()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	server := &http.Server{Handler: core.Handler(), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("listening", "address", listener.Addr().String(), "external_url", amURL.String())

	for {
		select {
		case <-hup:
			if err := core.Reload(); err != nil {
				logger.Error("configuration reload failed", "err", err)
			}
		case err := <-serveErr:
			return err
		case <-ctx.Done():
			logger.Info("received shutdown signal, exiting gracefully")
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancelShutdown()
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				// Drop the connections still open so no handler outlives
				// the core.
				_ = server.Close()
				return err
			}
			return nil
		}
	}
}

// newClassifier builds the LLM classification from the AMP config and hooks
// it into opts. It returns nil when llm.enabled is false.
func newClassifier(logger *slog.Logger, path string, opts *upstreamcore.Options) (*upstreamai.Classifier, error) {
	cfg, err := appconfig.LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("error loading AMP configuration file: %w", err)
	}
	if !cfg.LLM.Enabled {
		logger.Info("AMP configuration loaded, alert classification disabled (llm.enabled=false)")
		return nil, nil
	}

	llmCfg := llm.DefaultConfig()
	llmCfg.Provider = cfg.LLM.Provider
	llmCfg.BaseURL = cfg.LLM.BaseURL
	llmCfg.APIKey = cfg.LLM.APIKey
	llmCfg.Model = cfg.LLM.Model
	llmCfg.MaxTokens = cfg.LLM.MaxTokens
	llmCfg.Temperature = cfg.LLM.Temperature
	llmCfg.Timeout = cfg.LLM.Timeout
	llmCfg.MaxRetries = cfg.LLM.MaxRetries

	classCfg := services.DefaultClassificationConfig()
	classCfg.EnableLLM = true
	if cfg.LLM.Timeout > 0 {
		classCfg.LLMTimeout = cfg.LLM.Timeout
	}
	// In-memory L2 cache: a single instance until the gossip cluster
	// (UPSTREAM-HA-MEMBERLIST) decides what stays in Redis.
	svc, err := services.NewClassificationService(services.ClassificationServiceConfig{
		LLMClient: llm.NewHTTPLLMClient(llmCfg, logger),
		Cache:     cache.NewMemoryCache(logger),
		Config:    classCfg,
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create classification service: %w", err)
	}

	classifier := upstreamai.New(upstreamai.Config{
		Service:    svc,
		Logger:     logger,
		Registerer: opts.Registerer,
	})
	opts.NewAlertCallback = classifier.AlertCallback
	opts.NotifyStage = classifier.NotifyStage(cfg.LLM.NotifyWait, cfg.LLM.DropNoise)
	logger.Info("alert classification enabled",
		"provider", llmCfg.Provider,
		"model", llmCfg.Model,
		"notify_wait", cfg.LLM.NotifyWait,
		"drop_noise", cfg.LLM.DropNoise)
	return classifier, nil
}

// clusterFlags registers upstream's --cluster.* flags
// (github.com/prometheus/alertmanager@v0.34.1/cmd/alertmanager/main.go) with
// the same names, help and defaults, and returns the Options they fill.
func clusterFlags(fs *flag.FlagSet) *upstreamcore.Options {
	o := &upstreamcore.Options{}
	fs.StringVar(&o.ClusterBindAddr, "cluster.listen-address", upstreamcore.DefaultClusterAddr, "Listen address for cluster. Set to empty string to disable HA mode.")
	fs.StringVar(&o.ClusterAdvertiseAddr, "cluster.advertise-address", "", "Explicit address to advertise in cluster.")
	fs.StringVar(&o.ClusterPeerName, "cluster.peer-name", "", "Explicit name of the peer, rather than generating a random one")
	fs.Var((*stringsFlag)(&o.Peers), "cluster.peer", "Initial peers (may be repeated).")
	fs.DurationVar(&o.PeerTimeout, "cluster.peer-timeout", upstreamcore.DefaultPeerTimeout, "Time to wait between peers to send notifications.")
	fs.DurationVar(&o.PeersResolveTimeout, "cluster.peers-resolve-timeout", cluster.DefaultResolvePeersTimeout, "Time to resolve peers.")
	fs.DurationVar(&o.GossipInterval, "cluster.gossip-interval", cluster.DefaultGossipInterval, "Interval between sending gossip messages. By lowering this value (more frequent) gossip messages are propagated across the cluster more quickly at the expense of increased bandwidth.")
	fs.DurationVar(&o.PushPullInterval, "cluster.pushpull-interval", cluster.DefaultPushPullInterval, "Interval for gossip state syncs. Setting this interval lower (more frequent) will increase convergence speeds across larger clusters at the expense of increased bandwidth usage.")
	fs.DurationVar(&o.TCPTimeout, "cluster.tcp-timeout", cluster.DefaultTCPTimeout, "Timeout for establishing a stream connection with a remote node for a full state sync, and for stream read and write operations.")
	fs.DurationVar(&o.ProbeTimeout, "cluster.probe-timeout", cluster.DefaultProbeTimeout, "Timeout to wait for an ack from a probed node before assuming it is unhealthy. This should be set to 99-percentile of RTT (round-trip time) on your network.")
	fs.DurationVar(&o.ProbeInterval, "cluster.probe-interval", cluster.DefaultProbeInterval, "Interval between random node probes. Setting this lower (more frequent) will cause the cluster to detect failed nodes more quickly at the expense of increased bandwidth usage.")
	fs.DurationVar(&o.SettleTimeout, "cluster.settle-timeout", upstreamcore.DefaultSettleTimeout, "Maximum time to wait for cluster connections to settle before evaluating notifications.")
	fs.DurationVar(&o.ReconnectInterval, "cluster.reconnect-interval", cluster.DefaultReconnectInterval, "Interval between attempting to reconnect to lost peers.")
	fs.DurationVar(&o.PeerReconnectTimeout, "cluster.reconnect-timeout", cluster.DefaultReconnectTimeout, "Length of time to attempt to reconnect to a lost peer.")
	fs.StringVar(&o.TLSConfigFile, "cluster.tls-config", "", "[EXPERIMENTAL] Path to config yaml file that can enable mutual TLS within the gossip protocol.")
	fs.BoolVar(&o.AllowInsecureAdvertise, "cluster.allow-insecure-public-advertise-address-discovery", false, "[EXPERIMENTAL] Allow alertmanager to discover and listen on a public IP address.")
	fs.StringVar(&o.Label, "cluster.label", "", "The cluster label is an optional string to include on each packet and stream. It uniquely identifies the cluster and prevents cross-communication issues when sending gossip messages.")
	return o
}

// validateClusterFlags rejects non-positive cluster durations when the
// cluster is on, as upstream app.Options.validate: upstreamcore would
// otherwise replace a zero with the default. SettleTimeout is a deadline,
// so zero is valid there (settle at once).
func validateClusterFlags(o upstreamcore.Options) error {
	if o.ClusterBindAddr == "" {
		return nil
	}
	for _, f := range []struct {
		name string
		val  time.Duration
	}{
		{"cluster.peer-timeout", o.PeerTimeout},
		{"cluster.peers-resolve-timeout", o.PeersResolveTimeout},
		{"cluster.gossip-interval", o.GossipInterval},
		{"cluster.pushpull-interval", o.PushPullInterval},
		{"cluster.tcp-timeout", o.TCPTimeout},
		{"cluster.probe-timeout", o.ProbeTimeout},
		{"cluster.probe-interval", o.ProbeInterval},
		{"cluster.reconnect-interval", o.ReconnectInterval},
		{"cluster.reconnect-timeout", o.PeerReconnectTimeout},
	} {
		if f.val <= 0 {
			return fmt.Errorf("--%s must be positive when clustering is enabled", f.name)
		}
	}
	return nil
}

// stringsFlag is a repeatable string flag, as kingpin's Strings upstream.
type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ",") }

func (s *stringsFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// extURL is a port of github.com/prometheus/alertmanager@v0.34.1/app/url.go
// (Copyright The Prometheus Authors, Apache License 2.0; see
// internal/upstreamcore/doc.go): without --web.external-url it derives
// http://<hostname>:<port>/ from the bound address.
func extURL(hostnamef func() (string, error), listen, external string) (*url.URL, error) {
	if external == "" {
		hostname, err := hostnamef()
		if err != nil {
			return nil, err
		}
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			return nil, err
		}
		external = fmt.Sprintf("http://%s:%s/", hostname, port)
	}

	u, err := url.Parse(external)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%q: invalid %q scheme, only 'http' and 'https' are supported", u.String(), u.Scheme)
	}

	ppref := strings.TrimRight(u.Path, "/")
	if ppref != "" && !strings.HasPrefix(ppref, "/") {
		ppref = "/" + ppref
	}
	u.Path = ppref

	return u, nil
}
