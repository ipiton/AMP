// Command amp-upstream serves the upstream Alertmanager core wired by
// internal/upstreamcore from an unmodified alertmanager.yml (ADR-019 PoC).
// It runs next to cmd/server and changes nothing in it; flags mirror the
// upstream binary's names for the subset it supports.
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

	"github.com/ipiton/AMP/internal/upstreamcore"
)

const shutdownTimeout = 30 * time.Second

func main() {
	configFile := flag.String("config.file", "alertmanager.yml", "Alertmanager configuration file name.")
	dataDir := flag.String("storage.path", "data/", "Base path for data storage.")
	retention := flag.Duration("data.retention", upstreamcore.DefaultRetention, "How long to keep data for.")
	listenAddress := flag.String("web.listen-address", ":9093", "Address to listen on for the web interface and API.")
	externalURL := flag.String("web.external-url", "", "The URL under which Alertmanager is externally reachable. Its path prefixes all HTTP endpoints.")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger, *configFile, *dataDir, *retention, *listenAddress, *externalURL); err != nil {
		logger.Error("amp-upstream exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, configFile, dataDir string, retention time.Duration, listenAddress, externalURL string) error {
	// Matcher parsing is process-global upstream; without this call it stays
	// in classic mode instead of upstream's default fallback mode.
	compat.InitFromFlags(logger, featurecontrol.NoopFlags{})

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	amURL, err := extURL(os.Hostname, listener.Addr().String(), externalURL)
	if err != nil {
		return fmt.Errorf("failed to determine external URL: %w", err)
	}

	core, err := upstreamcore.New(upstreamcore.Options{
		ConfigFile:  configFile,
		DataDir:     dataDir,
		ExternalURL: amURL,
		Retention:   retention,
		Logger:      logger,
		Registerer:  prometheus.DefaultRegisterer,
	})
	if err != nil {
		return err
	}
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
				return err
			}
			return nil
		}
	}
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
