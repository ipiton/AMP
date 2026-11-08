// Command amp-upstream serves the upstream Alertmanager core wired by
// internal/upstreamcore from an unmodified alertmanager.yml (ADR-019 PoC).
// It runs next to cmd/server and changes nothing in it; flags mirror the
// upstream binary's names for the subset it supports. --amp.config.file
// adds AMP's own layer (internal/upstreamai, amp.go): alert history in
// PostgreSQL, LLM classification and investigation; and the K8s-Secret
// publishing targets (internal/upstreamtargets) in the standard profile with
// publishing.enabled.
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
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/alertmanager/cluster"
	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/matcher/compat"
	"github.com/prometheus/client_golang/prometheus"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/common/version"

	"github.com/ipiton/AMP/internal/buildinfo"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/upstreamcore"
	"github.com/ipiton/AMP/internal/upstreamtargets"
)

// shutdownTimeout matches upstream's app.Stop.
const shutdownTimeout = 5 * time.Second

func main() {
	configFile := flag.String("config.file", "alertmanager.yml", "Alertmanager configuration file name.")
	dataDir := flag.String("storage.path", "data/", "Base path for data storage.")
	retention := flag.Duration("data.retention", upstreamcore.DefaultRetention, "How long to keep data for.")
	listenAddress := flag.String("web.listen-address", ":9093", "Address to listen on for the web interface and API.")
	externalURL := flag.String("web.external-url", "", "The URL under which Alertmanager is externally reachable. Its path prefixes all HTTP endpoints.")
	webConfigFile := flag.String("web.config.file", "", "Path to the web configuration file: basic_auth_users (bcrypt). The probes /-/healthy and /-/ready (server.auth.unauthenticated_paths in the AMP configuration) are served without credentials. Empty: no authentication.")
	ampConfigFile := flag.String("amp.config.file", "", "AMP configuration file: alert history (database.*, profile standard), classification (llm.*) and investigation (investigation.*). Empty: none.")
	clusterOpts := clusterFlags(flag.CommandLine)
	flag.Parse()

	setContractVersion(debug.ReadBuildInfo)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Starting Alertmanager", "version", version.Info(), "amp_version", buildinfo.Version)
	logger.Info("Build context", "build_context", version.BuildContext())
	prometheus.MustRegister(versioncollector.NewCollector("alertmanager"))
	if err := buildinfo.Register(prometheus.DefaultRegisterer); err != nil {
		logger.Error("amp_build_info registration failed", "err", err)
		os.Exit(1)
	}
	if err := run(logger, *configFile, *dataDir, *retention, *listenAddress, *externalURL, *webConfigFile, *ampConfigFile, *clusterOpts); err != nil {
		logger.Error("amp-upstream exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, configFile, dataDir string, retention time.Duration, listenAddress, externalURL, webConfigFile, ampConfigFile string, clusterOpts upstreamcore.Options) error {
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
	var (
		targets         *upstreamtargets.Source
		targetsInterval time.Duration
		// nil: the default exempt paths (no AMP configuration).
		unauthenticatedPaths []string
	)
	if ampConfigFile != "" {
		cfg, err := appconfig.LoadConfig(ampConfigFile)
		if err != nil {
			return fmt.Errorf("error loading AMP configuration file: %w", err)
		}
		unauthenticatedPaths = cfg.Server.Auth.UnauthenticatedPaths
		stopAMP, err := newAMPLayer(logger, cfg, &opts)
		if err != nil {
			return err
		}
		// Deferred first, so it runs after core.Stop: the dispatcher and
		// the alert store are gone before AMP's workers stop.
		defer stopAMP()
		if targets, targetsInterval, err = newTargetSource(logger, cfg, &opts); err != nil {
			return err
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

	handler, err := wrapWebAuth(core.Handler(), webConfigFile, amURL.Path, unauthenticatedPaths, logger, opts.Registerer)
	if err != nil {
		_ = listener.Close()
		return err
	}

	if targets != nil {
		// Cancelled before core.Stop runs: a poll must not reload a stopped
		// core (Reload would only return an error, but there is no reason to
		// list Secrets during shutdown).
		pollCtx, stopPoll := context.WithCancel(context.Background())
		defer stopPoll()
		go targets.Run(pollCtx, targetsInterval, core.Reload)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
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
