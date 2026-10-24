//go:build futureparity && upstreamcore

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/matcher/compat"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ipiton/AMP/internal/upstreamcore"
)

// The historical suites run unchanged against the upstream Alertmanager core
// (ADR-019, UPSTREAM-CORE-POC-GATE): same AMP_CONFIG_FILE, same mux, the
// core's handler instead of AMP's router.
func init() {
	registerRoutesImpl = registerUpstreamCoreRoutes
}

// The seam has no *testing.T, so the cores and their data directories are
// tracked here and torn down by stopUpstreamCores from TestMain.
var (
	initUpstreamMatchers sync.Once
	upstreamCoresMu      sync.Mutex
	upstreamCores        []*upstreamcore.Core
	upstreamDataRoot     string
)

func registerUpstreamCoreRoutes(mux *http.ServeMux) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	initUpstreamMatchers.Do(func() { compat.InitFromFlags(logger, featurecontrol.NoopFlags{}) })

	upstreamCoresMu.Lock()
	defer upstreamCoresMu.Unlock()
	if upstreamDataRoot == "" {
		root, err := os.MkdirTemp("", "amp-upstreamcore-")
		if err != nil {
			registerFutureParityBootstrapFailure(mux, err)
			return
		}
		upstreamDataRoot = root
	}

	configPath, _ := resolveRuntimeConfigPath()
	dataDir, err := os.MkdirTemp(upstreamDataRoot, "core-")
	if err != nil {
		registerFutureParityBootstrapFailure(mux, err)
		return
	}
	core, err := upstreamcore.New(upstreamcore.Options{
		ConfigFile:  configPath,
		DataDir:     dataDir,
		ExternalURL: &url.URL{Scheme: "http", Host: "127.0.0.1:9093"},
		Logger:      logger,
		Registerer:  prometheus.NewRegistry(),
	})
	if err != nil {
		registerFutureParityBootstrapFailure(mux, err)
		return
	}
	upstreamCores = append(upstreamCores, core)

	h := core.Handler()
	// The suites POST JSON without a Content-Type, which AMP accepted and
	// upstream rejects with 415 (classified in the task's divergence table);
	// set it as every real client (Prometheus, amtool) does, so the suites
	// reach the semantics behind it.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") == "" {
			r.Header.Set("Content-Type", "application/json")
		}
		h.ServeHTTP(w, r)
	}))
}

// stopUpstreamCores stops every core the seam started and removes their data.
// The suites' httptest recorders never outlive a test, so no handler is
// serving by then (upstreamcore.Core.Handler).
func stopUpstreamCores() {
	upstreamCoresMu.Lock()
	defer upstreamCoresMu.Unlock()
	for _, c := range upstreamCores {
		c.Stop()
	}
	upstreamCores = nil
	if upstreamDataRoot != "" {
		_ = os.RemoveAll(upstreamDataRoot)
		upstreamDataRoot = ""
	}
}
