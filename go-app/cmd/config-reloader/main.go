// Command config-reloader watches AMP's config file and asks the application to
// reload when it changes.
//
// It exists because a Kubernetes ConfigMap update reaches the pod as a changed
// file, and nothing tells the process about it: kubelet does not signal
// containers. Without this sidecar, a ConfigMap edit takes effect on the next
// pod restart — which is exactly the thing hot reload was built to avoid.
//
// Usage (defaults suit the Helm sidecar):
//
//	config-reloader --config-file=/etc/amp/config.yaml \
//	  --method=http \
//	  --reload-url=http://127.0.0.1:9093/-/reload \
//	  --health-url=http://127.0.0.1:9093/health/reload \
//	  --interval=10s --metrics-addr=:9091
//
// Every reload is VERIFIED against the application's /health/reload (see the
// verifier): triggering a reload and assuming it worked would report success
// for a config the application rejected.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	methodHTTP   = "http"
	methodSignal = "signal"
)

type options struct {
	configPath   string
	interval     time.Duration
	method       string
	pid          int
	allowSelfPID bool
	reloadURL    string
	healthURL    string
	metricsAddr  string

	verifyTimeout time.Duration
	verifyPoll    time.Duration
	maxBackoff    time.Duration

	logFormat string
	logLevel  string
}

func main() {
	opts := parseFlags()

	logger := newLogger(opts)
	slog.SetDefault(logger)

	if err := opts.validate(); err != nil {
		logger.Error("invalid flags", "error", err)
		os.Exit(2)
	}

	registry := prometheus.NewRegistry()
	metrics := newReloaderMetrics(registry)

	httpClient := &http.Client{Timeout: 10 * time.Second}

	notify, err := buildNotifier(opts, httpClient)
	if err != nil {
		logger.Error("cannot build the reload trigger", "error", err)
		os.Exit(2)
	}

	watcher := &reloader{
		configPath:    opts.configPath,
		interval:      opts.interval,
		notifier:      notify,
		verifier:      &verifier{url: opts.healthURL, client: httpClient},
		metrics:       metrics,
		logger:        logger,
		verifyTimeout: opts.verifyTimeout,
		verifyPoll:    opts.verifyPoll,
		maxBackoff:    opts.maxBackoff,
		now:           time.Now,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metricsServer := startMetricsServer(ctx, opts.metricsAddr, registry, logger)

	logger.Info("config-reloader starting",
		"config_file", opts.configPath,
		"interval", opts.interval,
		"trigger", notify.Describe(),
		"health_url", opts.healthURL,
		"metrics_addr", opts.metricsAddr,
	)

	watcher.prime()
	watcher.Run(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("metrics server shutdown failed", "error", err)
	}
	logger.Info("config-reloader stopped")
}

func parseFlags() *options {
	opts := &options{}

	flag.StringVar(&opts.configPath, "config-file", "/etc/amp/config.yaml",
		"Path to the config file to watch.")
	flag.DurationVar(&opts.interval, "interval", 10*time.Second,
		"How often to hash the config file.")
	flag.StringVar(&opts.method, "method", methodHTTP,
		"How to trigger a reload: \"http\" (POST --reload-url) or \"signal\" (SIGHUP to --pid). "+
			"\"signal\" only works when the pod sets shareProcessNamespace: true.")
	flag.IntVar(&opts.pid, "pid", 1,
		"Target PID for --method=signal.")
	flag.BoolVar(&opts.allowSelfPID, "allow-self-pid", false,
		"Permit --method=signal with a PID that is this process itself. Only useful in tests; "+
			"in a pod without shareProcessNamespace it means signalling the reloader instead of the app.")
	flag.StringVar(&opts.reloadURL, "reload-url", "http://127.0.0.1:9093/-/reload",
		"Application reload endpoint for --method=http.")
	flag.StringVar(&opts.healthURL, "health-url", "http://127.0.0.1:9093/health/reload",
		"Application reload-status endpoint used to verify every reload.")
	flag.StringVar(&opts.metricsAddr, "metrics-addr", ":9091",
		"Address for this sidecar's own /metrics endpoint.")
	flag.DurationVar(&opts.verifyTimeout, "verify-timeout", 30*time.Second,
		"How long to wait for the application to confirm a triggered reload.")
	flag.DurationVar(&opts.verifyPoll, "verify-poll", 500*time.Millisecond,
		"How often to poll the reload-status endpoint while waiting for confirmation.")
	flag.DurationVar(&opts.maxBackoff, "max-backoff", 5*time.Minute,
		"Cap on the retry delay for a config the application keeps rejecting.")
	flag.StringVar(&opts.logFormat, "log-format", "json", "Log format: json or text.")
	flag.StringVar(&opts.logLevel, "log-level", "info", "Log level: debug, info, warn or error.")
	flag.Parse()

	return opts
}

func (o *options) validate() error {
	if o.configPath == "" {
		return errors.New("--config-file is required")
	}
	if o.interval <= 0 {
		return errors.New("--interval must be positive")
	}
	if o.verifyTimeout <= 0 {
		return errors.New("--verify-timeout must be positive")
	}
	if o.verifyPoll <= 0 {
		return errors.New("--verify-poll must be positive")
	}
	if o.maxBackoff < o.interval {
		return errors.New("--max-backoff must be at least --interval")
	}

	switch o.method {
	case methodHTTP:
		if o.reloadURL == "" {
			return errors.New("--reload-url is required for --method=http")
		}
	case methodSignal:
		if o.pid <= 0 {
			return errors.New("--pid must be positive for --method=signal")
		}
		if o.pid == os.Getpid() && !o.allowSelfPID {
			// The default --pid=1 hits this in any pod WITHOUT
			// shareProcessNamespace: true, because then the sidecar is itself
			// PID 1. Refusing is the whole point — the alternative is
			// delivering SIGHUP to this process, which ignores it, and
			// reporting every reload as a success.
			return fmt.Errorf(
				"--method=signal would send SIGHUP to this process (pid %d): the pod is missing "+
					"shareProcessNamespace: true, or --pid is wrong. Use --method=http, or pass "+
					"--allow-self-pid if you really mean it", o.pid)
		}
	default:
		return fmt.Errorf("unknown --method %q (want %q or %q)", o.method, methodHTTP, methodSignal)
	}

	if o.healthURL == "" {
		return errors.New("--health-url is required: every reload is verified")
	}
	return nil
}

func buildNotifier(opts *options, client *http.Client) (notifier, error) {
	switch opts.method {
	case methodHTTP:
		return &httpNotifier{url: opts.reloadURL, client: client}, nil
	case methodSignal:
		return &signalNotifier{pid: opts.pid}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", opts.method)
	}
}

func newLogger(opts *options) *slog.Logger {
	level := slog.LevelInfo
	switch opts.logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handlerOpts := &slog.HandlerOptions{Level: level}
	if opts.logFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, handlerOpts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, handlerOpts))
}

// startMetricsServer serves this sidecar's own metrics plus a trivial /healthz,
// so the container can have a probe that does not depend on the application.
func startMetricsServer(
	ctx context.Context,
	addr string,
	registry *prometheus.Registry,
	logger *slog.Logger,
) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Not fatal: losing metrics must not stop config reloading, which is
			// the job that actually matters.
			logger.Error("metrics server failed", "addr", addr, "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	return server
}
