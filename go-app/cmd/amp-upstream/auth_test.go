package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The flag wins over server.web_config_file, which wins over the env: all
// three enabled auth on the removed cmd/server (deep-review F1).
func TestResolveWebConfigFile(t *testing.T) {
	t.Setenv(webConfigFileEnv, " /env/web.yml ")
	assert.Equal(t, "/flag/web.yml", resolveWebConfigFile("/flag/web.yml", "/cfg/web.yml"))
	assert.Equal(t, "/cfg/web.yml", resolveWebConfigFile(" ", "/cfg/web.yml"))
	assert.Equal(t, "/env/web.yml", resolveWebConfigFile("", ""))

	t.Setenv(webConfigFileEnv, "")
	assert.Empty(t, resolveWebConfigFile("", ""), "nothing set: auth off")
}

func TestWrapWebAuthOffWithoutPath(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h, err := wrapWebAuth(next, "", "", nil, discardLogger(), prometheus.NewRegistry())
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/status", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code, "no web config: the core's handler as is")
}

// Basic auth in front of the core, under the --web.external-url prefix; the
// probes stay open there (the chart's probes use the prefixed paths).
func TestWrapWebAuthUnderRoutePrefix(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "web.yml")
	require.NoError(t, os.WriteFile(path, []byte("basic_auth_users:\n  alice: "+string(hash)+"\n"), 0o600))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h, err := wrapWebAuth(next, path, "/am", nil, discardLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	do := func(target string, auth bool) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if auth {
			req.SetBasicAuth("alice", "secret")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusUnauthorized, do("/am/api/v2/status", false), "API needs credentials")
	assert.Equal(t, http.StatusOK, do("/am/api/v2/status", true), "valid credentials pass")
	assert.Equal(t, http.StatusOK, do("/am/-/healthy", false), "prefixed probe is open")
	assert.Equal(t, http.StatusOK, do("/am/-/ready", false), "prefixed probe is open")
	assert.Equal(t, http.StatusUnauthorized, do("/-/healthy", false), "exempt paths carry the prefix")
}

func TestWrapWebAuthCustomExemptPaths(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "web.yml")
	require.NoError(t, os.WriteFile(path, []byte("basic_auth_users:\n  alice: "+string(hash)+"\n"), 0o600))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h, err := wrapWebAuth(next, path, "", []string{"/metrics"}, discardLogger(), prometheus.NewRegistry())
	require.NoError(t, err)
	for target, want := range map[string]int{"/metrics": http.StatusOK, "/-/healthy": http.StatusUnauthorized} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		assert.Equal(t, want, rec.Code, target)
	}
}

func TestWrapWebAuthInvalidFileFailsStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.yml")
	require.NoError(t, os.WriteFile(path, []byte("basic_auth_users: [\n"), 0o600))
	_, err := wrapWebAuth(http.NotFoundHandler(), path, "", nil, discardLogger(), prometheus.NewRegistry())
	assert.Error(t, err)

	_, err = wrapWebAuth(http.NotFoundHandler(), filepath.Join(t.TempDir(), "missing.yml"), "", nil, discardLogger(), prometheus.NewRegistry())
	assert.Error(t, err, "a missing file is an error, not auth off")
}
