//go:build futureparity && upstreamcore

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"time"

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
	// upstream rejects with 415 (class b, see the header of
	// testdata/upstreamcore-divergences.tsv); set it as every real client
	// (Prometheus, amtool) does, so the suites reach the semantics behind it.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") == "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/alerts" {
			h.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		if rec.status == 0 || rec.status == http.StatusOK {
			awaitDispatched(h, body)
		}
	}))
}

// statusRecorder remembers the status code written through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// awaitDispatched waits until every active alert in an accepted POST body
// shows up in /api/v2/alerts/groups. Upstream's dispatcher takes alerts from
// a provider subscription, so groups trail ingest by about a millisecond
// (eventual consistency, see the baseline header); the suites read groups
// right after POST and pin the result, not the timing. A timeout returns
// without error: the suites then report what is missing.
func awaitDispatched(h http.Handler, body []byte) {
	var posted []struct {
		Labels map[string]string `json:"labels"`
		EndsAt time.Time         `json:"endsAt"`
	}
	if json.Unmarshal(body, &posted) != nil {
		return
	}
	now := time.Now()
	var want []map[string]string
	for _, a := range posted {
		if a.EndsAt.IsZero() || a.EndsAt.After(now) {
			want = append(want, a.Labels)
		}
	}
	for deadline := now.Add(time.Second); len(want) > 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil))
		var groups []struct {
			Alerts []struct {
				Labels map[string]string `json:"labels"`
			} `json:"alerts"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &groups) != nil {
			return
		}
		pending := want[:0]
		for _, labels := range want {
			found := false
			for _, g := range groups {
				for _, a := range g.Alerts {
					found = found || maps.Equal(a.Labels, labels)
				}
			}
			if !found {
				pending = append(pending, labels)
			}
		}
		want = pending
	}
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
