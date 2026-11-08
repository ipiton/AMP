// Command amp-upstream serves the upstream Alertmanager core wired by
// internal/upstreamcore from an unmodified alertmanager.yml (ADR-019 PoC).
// It runs next to cmd/server and changes nothing in it; flags mirror the
// upstream binary's names for the subset it supports. --amp.config.file
// adds AMP's LLM classification (internal/upstreamai) when llm.enabled, and
// the K8s-Secret publishing targets (internal/upstreamtargets) in the
// standard profile with publishing.enabled.
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
	ampConfigFile := flag.String("amp.config.file", "", "AMP configuration file; its llm.* section enables alert classification. Empty: none.")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Starting Alertmanager", "version", version.Info())
	logger.Info("Build context", "build_context", version.BuildContext())
	prometheus.MustRegister(versioncollector.NewCollector("alertmanager"))
	if err := run(logger, *configFile, *dataDir, *retention, *listenAddress, *externalURL, *ampConfigFile); err != nil {
		logger.Error("amp-upstream exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, configFile, dataDir string, retention time.Duration, listenAddress, externalURL, ampConfigFile string) error {
	// Matcher parsing is process-global upstream; without this call it stays
	// in classic mode instead of upstream's default fallback mode.
	compat.InitFromFlags(logger, featurecontrol.NoopFlags{})

	opts := upstreamcore.Options{
		ConfigFile: configFile,
		DataDir:    dataDir,
		Retention:  retention,
		Logger:     logger,
		Registerer: prometheus.DefaultRegisterer,
	}
	var (
		targets         *upstreamtargets.Source
		targetsInterval time.Duration
	)
	if ampConfigFile != "" {
		cfg, err := appconfig.LoadConfig(ampConfigFile)
		if err != nil {
			return fmt.Errorf("error loading AMP configuration file: %w", err)
		}
		classifier, err := newClassifier(logger, cfg, &opts)
		if err != nil {
			return err
		}
		// Deferred first, so it runs after core.Stop: the dispatcher is
		// gone before the workers stop.
		if classifier != nil {
			defer classifier.Stop()
		}
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
func newClassifier(logger *slog.Logger, cfg *appconfig.Config, opts *upstreamcore.Options) (*upstreamai.Classifier, error) {
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
