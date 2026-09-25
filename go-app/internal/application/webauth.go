package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// ================================================================================
// HTTP basic authentication (PROD-AUTH)
// ================================================================================
//
// WebAuth protects the whole HTTP API with basic auth read from an
// upstream-compatible web config file (Alertmanager's --web.config.file,
// prometheus/exporter-toolkit format). Only `basic_auth_users` is
// implemented; the upstream keys AMP does not implement are a load error, so
// a TLS block is never silently ignored.
//
// The request-time algorithm is carried over from exporter-toolkit's
// webHandler: an unknown user is checked against a placeholder hash (no user
// enumeration by timing), bcrypt runs under a mutex (it is CPU-heavy) and its
// verdicts are cached, because Prometheus re-sends alerts on every evaluation
// cycle and a 50-100ms bcrypt on each of those would throttle ingest.
//
// Deliberate divergences from upstream (see tasks/PROD-AUTH/Spec.md D3-D5):
//   - an empty or missing `basic_auth_users` is an error, not "auth off";
//   - a file edit that fails validation keeps the previous users instead of
//     answering 500 to everything;
//   - UnauthenticatedPaths (exact paths, e.g. kubelet probes) bypass auth.

const (
	// webAuthRecheckInterval bounds how often the file is stat-ed for changes.
	webAuthRecheckInterval = time.Second

	// webAuthCacheSize bounds the bcrypt verdict cache (upstream's value).
	webAuthCacheSize = 100

	// webAuthPlaceholderHash is compared against when the user does not
	// exist, so an unknown user costs the same bcrypt as a known one. Its
	// password is irrelevant: the verdict for an unknown user is always false.
	webAuthPlaceholderHash = "$2a$10$f3ZMvW1lLLrbNgluq3.Ht.cEk8M9YQQ3BiEgxgZsfAFhe8VnvyayG"

	webAuthReasonMissing = "missing"
	webAuthReasonInvalid = "invalid"
)

// unsupportedWebConfigKeys are upstream web config keys AMP does not
// implement, with the hint returned in the load error.
var unsupportedWebConfigKeys = map[string]string{
	"tls_server_config":  "terminate TLS at the ingress or service mesh",
	"http_server_config": "set response headers at the ingress",
	"rate_limit":         "rate-limit at the ingress",
}

// webConfigFile is the implemented subset of the upstream web config file.
type webConfigFile struct {
	BasicAuthUsers map[string]string `yaml:"basic_auth_users"`
}

// webAuthUsers maps user name to bcrypt hash.
type webAuthUsers map[string][]byte

// loadWebAuthUsers reads and validates a web config file. Errors name the
// offending key or user, never a hash.
func loadWebAuthUsers(path string) (webAuthUsers, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("web config: %w", err)
	}

	// Unsupported upstream keys get their own message before the strict
	// decode would reject them as merely "unknown".
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("web config %s: invalid YAML: %w", path, err)
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if hint, ok := unsupportedWebConfigKeys[key]; ok {
			return nil, fmt.Errorf("web config %s: %q is not supported by AMP; %s", path, key, hint)
		}
	}

	var file webConfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("web config %s: %w", path, err)
	}

	if len(file.BasicAuthUsers) == 0 {
		return nil, fmt.Errorf("web config %s: basic_auth_users is empty; remove the web config to disable authentication", path)
	}

	users := make(webAuthUsers, len(file.BasicAuthUsers))
	for user, hash := range file.BasicAuthUsers {
		if user == "" {
			return nil, fmt.Errorf("web config %s: basic_auth_users has an empty user name", path)
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return nil, fmt.Errorf("web config %s: password of user %q is not a bcrypt hash", path, user)
		}
		users[user] = []byte(hash)
	}
	return users, nil
}

// WebAuthOptions configures NewWebAuth.
type WebAuthOptions struct {
	// Path is the web config file. Required.
	Path string
	// UnauthenticatedPaths are served without credentials (exact match
	// against the request path, route prefix already stripped).
	UnauthenticatedPaths []string
	Logger               *slog.Logger
	// Registerer receives the auth metrics; nil means the default registry.
	Registerer prometheus.Registerer
}

// WebAuth is the basic-auth middleware. Build it with NewWebAuth.
type WebAuth struct {
	path    string
	exempt  map[string]struct{}
	logger  *slog.Logger
	metrics webAuthMetrics

	users atomic.Pointer[webAuthUsers]

	// Hot reload state. lastCheck lets all but one request skip the stat;
	// statMu serialises the stat + reload itself.
	lastCheck  atomic.Int64
	statMu     sync.Mutex
	modTime    time.Time
	size       int64
	failedFile string // identity of the last file that failed to load

	// bcryptMu serialises bcrypt, which is CPU-heavy.
	bcryptMu sync.Mutex
	cacheMu  sync.Mutex
	cache    map[[sha256.Size]byte]bool

	// Test seams.
	now     func() time.Time
	compare func(hash, password []byte) error
}

type webAuthMetrics struct {
	failures       *prometheus.CounterVec
	reloadFailures prometheus.Counter
}

func newWebAuthMetrics(reg prometheus.Registerer) (webAuthMetrics, error) {
	m := webAuthMetrics{
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "amp_http_auth_failures_total",
			Help: "HTTP requests rejected by basic authentication, by reason (missing, invalid).",
		}, []string{"reason"}),
		reloadFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amp_http_auth_config_reload_failures_total",
			Help: "Web config file changes rejected as invalid; the previous users stay in effect.",
		}),
	}
	for _, c := range []prometheus.Collector{m.failures, m.reloadFailures} {
		if err := reg.Register(c); err != nil {
			return webAuthMetrics{}, fmt.Errorf("register web auth metrics: %w", err)
		}
	}
	// Expose both reasons from the start so rate() works on the first failure.
	m.failures.WithLabelValues(webAuthReasonMissing)
	m.failures.WithLabelValues(webAuthReasonInvalid)
	return m, nil
}

// NewWebAuth loads the web config file and builds the middleware. A missing
// or invalid file is an error: the caller must not start serving.
func NewWebAuth(opts WebAuthOptions) (*WebAuth, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("web config: path is empty")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	reg := opts.Registerer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("web config: %w", err)
	}
	users, err := loadWebAuthUsers(opts.Path)
	if err != nil {
		return nil, err
	}
	metrics, err := newWebAuthMetrics(reg)
	if err != nil {
		return nil, err
	}

	a := &WebAuth{
		path:    opts.Path,
		exempt:  make(map[string]struct{}, len(opts.UnauthenticatedPaths)),
		logger:  logger,
		metrics: metrics,
		modTime: info.ModTime(),
		size:    info.Size(),
		cache:   make(map[[sha256.Size]byte]bool),
		now:     time.Now,
		compare: bcrypt.CompareHashAndPassword,
	}
	for _, p := range opts.UnauthenticatedPaths {
		a.exempt[p] = struct{}{}
	}
	a.users.Store(&users)
	a.lastCheck.Store(a.now().UnixNano())
	return a, nil
}

// UserCount reports how many users are currently in effect.
func (a *WebAuth) UserCount() int {
	return len(*a.users.Load())
}

// Wrap returns next guarded by basic auth.
func (a *WebAuth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.exempt[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}

		a.maybeReload()

		user, password, ok := r.BasicAuth()
		if !ok {
			a.reject(w, r, webAuthReasonMissing, "")
			return
		}
		if !a.authenticate(user, password) {
			a.reject(w, r, webAuthReasonInvalid, user)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *WebAuth) authenticate(user, password string) bool {
	users := *a.users.Load()
	hash, known := users[user]
	if !known {
		hash = []byte(webAuthPlaceholderHash)
	}

	key := webAuthCacheKey(user, hash, password)
	if verdict, hit := a.cacheGet(key); hit {
		return verdict
	}

	a.bcryptMu.Lock()
	err := a.compare(hash, []byte(password))
	a.bcryptMu.Unlock()

	verdict := known && err == nil
	a.cacheSet(key, verdict)
	return verdict
}

func (a *WebAuth) reject(w http.ResponseWriter, r *http.Request, reason, user string) {
	a.metrics.failures.WithLabelValues(reason).Inc()
	a.logger.Debug("HTTP request rejected by basic authentication",
		"reason", reason,
		"user", user,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
	)
	w.Header().Set("WWW-Authenticate", "Basic")
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// webAuthCacheKey hashes the credentials so the cache never holds a
// plaintext password. Fields are length-prefixed: a separator byte could be
// smuggled inside a user name and make two different inputs collide.
func webAuthCacheKey(user string, hash []byte, password string) [sha256.Size]byte {
	h := sha256.New()
	var length [8]byte
	for _, field := range [][]byte{[]byte(user), hash, []byte(password)} {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write(field)
	}
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (a *WebAuth) cacheGet(key [sha256.Size]byte) (bool, bool) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	verdict, ok := a.cache[key]
	return verdict, ok
}

func (a *WebAuth) cacheSet(key [sha256.Size]byte, verdict bool) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	if len(a.cache) >= webAuthCacheSize {
		// Evict ~10% so a full cache does not pay for an eviction on every
		// miss. Map iteration starts at a random position, which is random
		// enough for a cache this small.
		evict := len(a.cache)/10 + 1
		for k := range a.cache {
			if evict == 0 {
				break
			}
			delete(a.cache, k)
			evict--
		}
	}
	a.cache[key] = verdict
}

func (a *WebAuth) cacheClear() {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	clear(a.cache)
}

// maybeReload re-reads the file when its mtime or size changed, at most once
// per webAuthRecheckInterval. An invalid or vanished file keeps the users in
// effect: an edit can never open the API or lock out ingest.
func (a *WebAuth) maybeReload() {
	now := a.now().UnixNano()
	last := a.lastCheck.Load()
	if now-last < int64(webAuthRecheckInterval) || !a.lastCheck.CompareAndSwap(last, now) {
		return
	}

	a.statMu.Lock()
	defer a.statMu.Unlock()

	info, err := os.Stat(a.path)
	if err != nil {
		a.reloadFailed("stat:"+err.Error(), err)
		return
	}
	if info.ModTime().Equal(a.modTime) && info.Size() == a.size {
		return
	}

	users, err := loadWebAuthUsers(a.path)
	if err != nil {
		a.reloadFailed(fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), err)
		return
	}

	a.users.Store(&users)
	a.modTime = info.ModTime()
	a.size = info.Size()
	a.failedFile = ""
	a.cacheClear()
	a.logger.Info("Web config reloaded", "path", a.path, "users", len(users))
}

// reloadFailed logs and counts a rejected file once per distinct file state,
// not once per request.
func (a *WebAuth) reloadFailed(identity string, err error) {
	if identity == a.failedFile {
		return
	}
	a.failedFile = identity
	a.metrics.reloadFailures.Inc()
	a.logger.Error("Web config change rejected; previous users stay in effect",
		"path", a.path,
		"error", err,
	)
}
