package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/ipiton/AMP/internal/ampapi"
	"github.com/ipiton/AMP/internal/upstreamcore"
)

const routesTestConfig = `route:
  receiver: blackhole
receivers:
  - name: blackhole
inhibit_rules:
  - source_matchers: [severity="critical"]
    target_matchers: [severity="warning"]
    equal: [instance]
`

// newRoutesCore starts an upstream core under the external URL's path
// prefix and stops it when the test ends.
func newRoutesCore(t *testing.T, prefix string) *upstreamcore.Core {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, []byte(routesTestConfig), 0o600))
	core, err := upstreamcore.New(upstreamcore.Options{
		ConfigFile:  configFile,
		DataDir:     filepath.Join(dir, "data"),
		ExternalURL: &url.URL{Scheme: "http", Host: "am.example", Path: prefix},
		Logger:      slog.New(slog.DiscardHandler),
		Registerer:  prometheus.NewRegistry(),
	})
	require.NoError(t, err)
	t.Cleanup(core.Stop)
	return core
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Everything outside <prefix>/api/amp/ is answered by the upstream core
// byte for byte; the AMP prefix goes to AMP's handler only.
func TestWithAMPRoutesKeepsUpstreamResponses(t *testing.T) {
	for _, prefix := range []string{"", "/am"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			core := newRoutesCore(t, prefix)
			direct := core.Handler()
			amp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
			composed := withAMPRoutes(direct, amp, prefix)

			rec := do(composed, http.MethodPost, prefix+"/api/v2/alerts",
				`[{"labels":{"alertname":"Disk","instance":"a","severity":"warning"}}]`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			for _, path := range []string{
				"/api/v2/status",
				"/api/v2/alerts",
				"/api/v2/alerts/groups",
				"/api/v2/silences",
				"/api/v2/receivers",
				"/api/v2/inhibitions", // removed: upstream 404 (research Q3)
				"/api/v1/alerts",      // upstream 410
				"/-/healthy",
				"/-/ready",
				"/api/v2/../amp/v1/alerts/0123456789abcdef", // upstream's own path cleaning
				"/api/ampx/v1",
				"/healthz",
			} {
				want := do(direct, http.MethodGet, prefix+path, "")
				got := do(composed, http.MethodGet, prefix+path, "")
				assert.Equal(t, want.Code, got.Code, path)
				assert.Equal(t, want.Body.String(), got.Body.String(), path)
				want.Header().Del("Date")
				got.Header().Del("Date")
				assert.Equal(t, want.Header(), got.Header(), path)
				assert.NotEqual(t, http.StatusTeapot, got.Code, path)
			}

			assert.Equal(t, http.StatusTeapot, do(composed, http.MethodGet, prefix+"/api/amp/v1/alerts/0123456789abcdef", "").Code)
			assert.Equal(t, http.StatusTeapot, do(composed, http.MethodPost, prefix+"/api/amp/anything", "").Code)
			if prefix != "" {
				rec := do(composed, http.MethodGet, "/api/amp/v1/alerts/0123456789abcdef", "")
				assert.NotEqual(t, http.StatusTeapot, rec.Code, "without the prefix the core answers")
			}
		})
	}
}

// Basic auth covers the AMP routes as it covers api/v2; the probes stay
// open (they are exact paths).
func TestAMPRoutesBehindWebAuth(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	require.NoError(t, err)
	webConfig := filepath.Join(t.TempDir(), "web.yml")
	require.NoError(t, os.WriteFile(webConfig, []byte("basic_auth_users:\n  alice: "+string(hash)+"\n"), 0o600))

	core := newRoutesCore(t, "/am")
	amp := ampapi.New(ampapi.Options{RoutePrefix: "/am", Logger: discardLogger(), Registerer: prometheus.NewRegistry()})
	h, err := wrapWebAuth(withAMPRoutes(core.Handler(), amp, "/am"), webConfig, "/am", nil, discardLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	target := "/am/api/amp/v1/alerts/0123456789abcdef"
	assert.Equal(t, http.StatusUnauthorized, do(h, http.MethodGet, target, "").Code)
	assert.Equal(t, http.StatusUnauthorized, do(h, http.MethodGet, target+"/investigation", "").Code)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetBasicAuth("alice", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "authenticated, no PostgreSQL: 503 from ampapi")

	assert.Equal(t, http.StatusOK, do(h, http.MethodGet, "/am/-/healthy", "").Code)
}
