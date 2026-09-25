package application

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ================================================================================
// PROD-AUTH: web config loading and basic-auth middleware
// ================================================================================

// testHash bcrypt-hashes at MinCost: the middleware accepts any valid cost,
// and cost 10 would make this suite slow for no coverage gain.
func testHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return string(hash)
}

func writeWebConfig(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func webConfigWithUsers(t *testing.T, users map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("basic_auth_users:\n")
	for user, password := range users {
		fmt.Fprintf(&b, "  %s: %s\n", user, testHash(t, password))
	}
	return b.String()
}

// webAuthHarness is a WebAuth over a temp file with a controllable clock, a
// private metrics registry and captured logs.
type webAuthHarness struct {
	auth     *WebAuth
	path     string
	clock    time.Time
	registry *prometheus.Registry
	logs     *bytes.Buffer
	compares atomic.Int32
	rewrites int
}

func newWebAuthHarness(t *testing.T, users map[string]string, exempt []string) *webAuthHarness {
	t.Helper()
	h := &webAuthHarness{
		path:     filepath.Join(t.TempDir(), "web-config.yml"),
		registry: prometheus.NewRegistry(),
		logs:     &bytes.Buffer{},
	}
	writeWebConfig(t, h.path, webConfigWithUsers(t, users))

	auth, err := NewWebAuth(WebAuthOptions{
		Path:                 h.path,
		UnauthenticatedPaths: exempt,
		Logger:               slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Registerer:           h.registry,
	})
	require.NoError(t, err)

	h.clock = time.Now()
	auth.now = func() time.Time { return h.clock }
	auth.lastCheck.Store(h.clock.UnixNano())
	auth.compare = func(hash, password []byte) error {
		h.compares.Add(1)
		return bcrypt.CompareHashAndPassword(hash, password)
	}
	h.auth = auth
	return h
}

// advance moves the clock past the recheck interval.
func (h *webAuthHarness) advance() {
	h.clock = h.clock.Add(webAuthRecheckInterval + time.Millisecond)
}

// rewrite replaces the file and bumps its mtime, so the change is visible
// even on filesystems with coarse timestamps.
func (h *webAuthHarness) rewrite(t *testing.T, content string) {
	t.Helper()
	writeWebConfig(t, h.path, content)
	h.rewrites++
	future := time.Now().Add(time.Duration(h.rewrites) * time.Hour)
	require.NoError(t, os.Chtimes(h.path, future, future))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func (h *webAuthHarness) do(method, path, user, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	rec := httptest.NewRecorder()
	h.auth.Wrap(okHandler()).ServeHTTP(rec, req)
	return rec
}

func (h *webAuthHarness) failures(reason string) float64 {
	return testutil.ToFloat64(h.auth.metrics.failures.WithLabelValues(reason))
}

func (h *webAuthHarness) reloadFailures() float64 {
	return testutil.ToFloat64(h.auth.metrics.reloadFailures)
}

// T1 (AC7): every way a web config can be unusable is a load error that
// names the problem.
func TestLoadWebAuthUsers_Validation(t *testing.T) {
	valid := testHash(t, "secret")

	tests := []struct {
		name    string
		content string
		wantErr string // empty = must load
	}{
		{name: "valid", content: "basic_auth_users:\n  alice: " + valid + "\n"},
		{name: "invalid yaml", content: "basic_auth_users: [\n", wantErr: "invalid YAML"},
		{name: "empty file", content: "", wantErr: "basic_auth_users is empty"},
		{name: "no users key", content: "# nothing\n", wantErr: "basic_auth_users is empty"},
		{name: "empty users", content: "basic_auth_users: {}\n", wantErr: "basic_auth_users is empty"},
		{name: "unknown key", content: "basic_auth_users:\n  alice: " + valid + "\nsomething_else: 1\n", wantErr: "something_else"},
		{name: "tls unsupported", content: "tls_server_config:\n  cert_file: x\n", wantErr: `"tls_server_config" is not supported`},
		{name: "http_server_config unsupported", content: "http_server_config:\n  http2: false\n", wantErr: `"http_server_config" is not supported`},
		{name: "rate_limit unsupported", content: "rate_limit:\n  interval: 1s\n", wantErr: `"rate_limit" is not supported`},
		{name: "empty user name", content: "basic_auth_users:\n  \"\": " + valid + "\n", wantErr: "empty user name"},
		{name: "plaintext password", content: "basic_auth_users:\n  alice: secret\n", wantErr: `user "alice" is not a bcrypt hash`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "web.yml")
			writeWebConfig(t, path, tc.content)

			users, err := loadWebAuthUsers(path)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Len(t, users, 1)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), valid, "a load error must never echo a hash")
		})
	}
}

func TestNewWebAuth_MissingFileIsAnError(t *testing.T) {
	_, err := NewWebAuth(WebAuthOptions{
		Path:       filepath.Join(t.TempDir(), "absent.yml"),
		Registerer: prometheus.NewRegistry(),
	})
	require.Error(t, err)

	_, err = NewWebAuth(WebAuthOptions{Registerer: prometheus.NewRegistry()})
	require.Error(t, err, "an empty path must not build an open middleware")
}

// T2 (AC1-AC3): the request-time contract.
func TestWebAuth_RequestContract(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "secret"}, []string{"/-/healthy", "/-/ready", "/custom"})

	tests := []struct {
		name       string
		method     string
		path       string
		user, pass string
		wantStatus int
		wantReason string
	}{
		{name: "no credentials", method: http.MethodPost, path: "/api/v2/silences", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonMissing},
		{name: "wrong password", method: http.MethodPost, path: "/api/v2/silences", user: "alice", pass: "nope", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonInvalid},
		{name: "unknown user", method: http.MethodPost, path: "/api/v2/silences", user: "mallory", pass: "secret", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonInvalid},
		{name: "valid credentials", method: http.MethodPost, path: "/api/v2/silences", user: "alice", pass: "secret", wantStatus: http.StatusOK},
		{name: "exempt healthy", method: http.MethodGet, path: "/-/healthy", wantStatus: http.StatusOK},
		{name: "exempt ready", method: http.MethodGet, path: "/-/ready", wantStatus: http.StatusOK},
		{name: "custom exempt", method: http.MethodGet, path: "/custom", wantStatus: http.StatusOK},
		{name: "healthz protected", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonMissing},
		{name: "metrics protected", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonMissing},
		{name: "exempt match is exact (trailing slash)", method: http.MethodGet, path: "/-/healthy/", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonMissing},
		{name: "exempt match is exact (sub-path)", method: http.MethodGet, path: "/custom/x", wantStatus: http.StatusUnauthorized, wantReason: webAuthReasonMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := map[string]float64{
				webAuthReasonMissing: h.failures(webAuthReasonMissing),
				webAuthReasonInvalid: h.failures(webAuthReasonInvalid),
			}

			rec := h.do(tc.method, tc.path, tc.user, tc.pass)

			assert.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, "Basic", rec.Header().Get("WWW-Authenticate"))
			}
			for reason, was := range before {
				want := was
				if reason == tc.wantReason {
					want++
				}
				assert.Equal(t, want, h.failures(reason), "amp_http_auth_failures_total{reason=%q}", reason)
			}
		})
	}
}

// T3 (AC4): auth sits inside the route prefix, so exempt paths are written
// without it.
func TestWebAuth_InsideRoutePrefix(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "secret"}, []string{"/-/healthy"})

	mux := http.NewServeMux()
	mux.Handle("/", okHandler())
	root := WithRoutePrefix(h.auth.Wrap(mux), "/am")

	serve := func(path, user, pass string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, serve("/am/-/healthy", "", ""))
	assert.Equal(t, http.StatusUnauthorized, serve("/am/api/v2/silences", "", ""))
	assert.Equal(t, http.StatusOK, serve("/am/api/v2/silences", "alice", "secret"))
}

// T4 (AC5): bcrypt runs once per distinct credential, not once per request.
func TestWebAuth_CachesVerdicts(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "secret"}, nil)

	require.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "secret").Code)
	require.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "secret").Code)
	assert.Equal(t, int32(1), h.compares.Load(), "a repeated valid credential must hit the cache")

	require.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/x", "alice", "nope").Code)
	require.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/x", "alice", "nope").Code)
	assert.Equal(t, int32(2), h.compares.Load(), "a rejected credential is cached too")

	// An unknown user still pays for one bcrypt (against the placeholder).
	require.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/x", "mallory", "secret").Code)
	assert.Equal(t, int32(3), h.compares.Load())
}

func TestWebAuth_CacheStaysBounded(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "secret"}, nil)
	h.auth.compare = func(_, _ []byte) error { return bcrypt.ErrMismatchedHashAndPassword }

	for i := 0; i < webAuthCacheSize*3; i++ {
		h.do(http.MethodGet, "/x", "alice", fmt.Sprintf("guess-%d", i))
		h.auth.cacheMu.Lock()
		size := len(h.auth.cache)
		h.auth.cacheMu.Unlock()
		require.LessOrEqual(t, size, webAuthCacheSize)
	}
}

func TestWebAuthCacheKey_LengthPrefixed(t *testing.T) {
	// With a plain separator these two would hash the same input bytes.
	a := webAuthCacheKey("ab", []byte("c"), "d")
	b := webAuthCacheKey("a", []byte("bc"), "d")
	assert.NotEqual(t, a, b)
}

// T5 (AC6): password rotation without restart; an invalid edit keeps the
// previous users.
func TestWebAuth_HotReload(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "old"}, nil)
	require.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "old").Code)

	h.rewrite(t, webConfigWithUsers(t, map[string]string{"alice": "new", "bob": "b"}))

	// Within the recheck interval the change is not looked at yet.
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "old").Code)

	h.advance()
	assert.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/x", "alice", "old").Code)
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "new").Code)
	assert.Equal(t, 2, h.auth.UserCount())
	assert.Zero(t, h.reloadFailures())

	// Invalid edit: previous users stay, one failure counted, one ERROR.
	h.rewrite(t, "basic_auth_users:\n  alice: plaintext\n")
	h.advance()
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "new").Code)
	h.advance()
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "new").Code)
	assert.Equal(t, float64(1), h.reloadFailures(), "the same bad file is counted once, not per request")
	assert.Equal(t, 1, strings.Count(h.logs.String(), "Web config change rejected"))

	// Vanished file: still the previous users.
	require.NoError(t, os.Remove(h.path))
	h.advance()
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "alice", "new").Code)
	assert.Equal(t, float64(2), h.reloadFailures())

	// Fixed file is picked up again.
	h.rewrite(t, webConfigWithUsers(t, map[string]string{"carol": "c"}))
	h.advance()
	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/x", "carol", "c").Code)
	assert.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/x", "alice", "new").Code)
}

// T6 (AC10): no password, hash or Authorization header ever reaches the log,
// including at debug level.
func TestWebAuth_DoesNotLogSecrets(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "s3cr3t-pass"}, nil)
	hash := string((*h.auth.users.Load())["alice"])

	h.do(http.MethodGet, "/x", "alice", "wrong-pass-42")
	h.do(http.MethodGet, "/x", "alice", "s3cr3t-pass")
	h.do(http.MethodGet, "/x", "", "")

	h.rewrite(t, "basic_auth_users:\n  alice: not-a-hash-value\n")
	h.advance()
	h.do(http.MethodGet, "/x", "alice", "s3cr3t-pass")

	logs := h.logs.String()
	require.Contains(t, logs, "rejected by basic authentication", "debug logging must be on for this test to mean anything")
	for _, secret := range []string{"s3cr3t-pass", "wrong-pass-42", hash, "not-a-hash-value", "Authorization", "Basic "} {
		assert.NotContains(t, logs, secret)
	}
}

// T8 (AC1 end to end): the real router behind the middleware.
func TestWebAuth_RealRouter(t *testing.T) {
	h := newWebAuthHarness(t, map[string]string{"alice": "secret"}, []string{"/-/healthy", "/-/ready"})
	handler := h.auth.Wrap(newActiveContractMux(t, nil))

	silence := `{
		"matchers": [{"name":"alertname","value":"AuthProbe","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "prod-auth",
		"comment": "auth probe"
	}`
	post := func(user, pass string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", strings.NewReader(silence))
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusUnauthorized, post("", ""))
	assert.Equal(t, http.StatusOK, post("alice", "secret"))

	req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "the probe path must answer without credentials")
}
