package publishing

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/ipiton/AMP/internal/core"
)

// ============================================================================
// Per-target HTTP client construction (FU-HTTP-CONFIG, wave 7 track C)
// ============================================================================
//
// Upstream Alertmanager lets every integration set `http_config` — proxy, TLS,
// basic auth, bearer authorization, redirect policy. AMP's publishers each build
// one HTTP client with hardcoded settings, so a corp-proxy / mTLS /
// basic-auth-protected webhook could not be expressed at all.
//
// DESIGN: rather than rebuild each publisher's client from scratch (and silently
// lose its own connection-pool and timeout tuning in the process), this file
// takes the client a publisher WOULD have built and returns a configured COPY.
// Every existing tuning — MaxIdleConns, dial/handshake timeouts,
// ForceAttemptHTTP2, TLS 1.2 floor — survives verbatim, and a target without
// http_config gets the original client back untouched.
//
// FAILURE POLICY: tls_config's ca_file/cert_file/key_file and basic_auth's
// password_file are read HERE, at construction. An unreadable path is a target
// BUILD ERROR: CreatePublisherForTarget returns it, its callers log it and skip
// that target, and every other target keeps delivering. Never process-fatal —
// this mirrors how an invalid K8s Secret target is handled (parseSecret returns
// a typed error, parseAndValidateSecrets WARNs and skips it, discovery
// continues). Failing CLOSED matters here specifically: the alternative —
// falling back to a plain client — would deliver alerts unauthenticated or
// unverified, which is worse than not delivering them.
//
// SECRETS: errors and logs name FIELDS and PATHS, never file contents or
// credential values. A path is not a credential, and hiding it makes an
// unreadable ca_file undebuggable.

// ErrHTTPConfigAmbiguousAuth mirrors upstream Alertmanager's own validation
// ("at most one of basic_auth, oauth2 & authorization must be configured").
// Applying both would make the Authorization header depend on the order this
// file happens to set it in, which is not a behaviour to guess at.
var ErrHTTPConfigAmbiguousAuth = errors.New("http_config: at most one of basic_auth and authorization may be set")

// applyHTTPClientConfig returns a COPY of base with cfg applied, or base itself
// when cfg carries nothing.
//
// base must not be nil. Its Transport must be a *http.Transport (every
// publisher's client in this package uses one) or nil, in which case
// http.DefaultTransport's shape is cloned — anything else cannot be cloned
// safely and is rejected rather than silently ignored.
func applyHTTPClientConfig(base *http.Client, cfg *core.HTTPClientConfig) (*http.Client, error) {
	if base == nil {
		return nil, errors.New("http_config: base HTTP client is nil")
	}
	if cfg.IsZero() {
		// No per-target configuration: the publisher keeps exactly the client it
		// built for itself. This is the path every pre-existing target takes.
		return base, nil
	}
	if cfg.BasicAuth != nil && cfg.Authorization != nil {
		return nil, ErrHTTPConfigAmbiguousAuth
	}

	transport, err := cloneTransport(base.Transport)
	if err != nil {
		return nil, err
	}

	if cfg.ProxyURL != "" {
		proxyURL, parseErr := url.Parse(cfg.ProxyURL)
		if parseErr != nil {
			return nil, fmt.Errorf("http_config: invalid proxy_url: %w", parseErr)
		}
		if proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, fmt.Errorf("http_config: proxy_url %q must be an absolute URL with a scheme and host", cfg.ProxyURL)
		}
		// http.ProxyURL ignores NO_PROXY/no_proxy by design — an explicit
		// proxy_url is explicit. Documented as an unsupported extra in
		// docs/ALERTMANAGER_COMPATIBILITY.md; upstream behaves the same way.
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	if cfg.TLSConfig != nil {
		tlsConfig, tlsErr := buildTLSConfig(transport.TLSClientConfig, cfg.TLSConfig)
		if tlsErr != nil {
			return nil, tlsErr
		}
		transport.TLSClientConfig = tlsConfig
	}

	rt, err := wrapAuthRoundTripper(transport, cfg)
	if err != nil {
		return nil, err
	}

	// Shallow copy: Timeout, Jar and any other field the publisher set survives.
	client := *base
	client.Transport = rt

	// follow_redirects: false means "return the redirect response as-is",
	// which is exactly http.ErrUseLastResponse. nil/true keeps the standard
	// library's default (follow, max 10 hops) — upstream's default too.
	if cfg.FollowRedirects != nil && !*cfg.FollowRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	return &client, nil
}

// cloneTransport deep-copies the base transport so mutating it cannot affect the
// publisher's own client (or any other target that shares it).
func cloneTransport(rt http.RoundTripper) (*http.Transport, error) {
	switch typed := rt.(type) {
	case nil:
		//nolint:forcetypeassert // http.DefaultTransport is documented as *http.Transport.
		return http.DefaultTransport.(*http.Transport).Clone(), nil
	case *http.Transport:
		return typed.Clone(), nil
	default:
		return nil, fmt.Errorf("http_config: cannot apply per-target HTTP config to transport of type %T", rt)
	}
}

// buildTLSConfig layers cfg on top of the base *tls.Config the publisher
// already had, preserving whatever it set (notably the TLS 1.2 floor every
// client in this package enforces).
func buildTLSConfig(base *tls.Config, cfg *core.TLSClientConfig) (*tls.Config, error) {
	var out *tls.Config
	if base != nil {
		out = base.Clone()
	} else {
		out = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	if cfg.ServerName != "" {
		out.ServerName = cfg.ServerName
	}
	if cfg.InsecureSkipVerify {
		//nolint:gosec // G402: this is the operator's explicit insecure_skip_verify.
		out.InsecureSkipVerify = true
	}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("http_config: tls_config.ca_file %q: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			// A file that exists but holds no usable certificate is just as fatal
			// as a missing one: verification would silently fall back to the
			// system trust store, which is not what the operator asked for.
			return nil, fmt.Errorf("http_config: tls_config.ca_file %q contains no PEM certificate", cfg.CAFile)
		}
		out.RootCAs = pool
	}

	// Both halves of the client certificate are required together — one alone is
	// a misconfiguration that would silently produce a connection with no client
	// certificate at all.
	switch {
	case cfg.CertFile != "" && cfg.KeyFile == "":
		return nil, errors.New("http_config: tls_config.cert_file is set without key_file")
	case cfg.KeyFile != "" && cfg.CertFile == "":
		return nil, errors.New("http_config: tls_config.key_file is set without cert_file")
	case cfg.CertFile != "":
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			// LoadX509KeyPair's message names both paths but never their
			// contents, so it is safe to wrap verbatim.
			return nil, fmt.Errorf("http_config: tls_config client certificate: %w", err)
		}
		out.Certificates = []tls.Certificate{cert}
	}

	return out, nil
}

// authRoundTripper stamps an Authorization header on every request.
//
// It CLONES the request rather than mutating it: the same *http.Request may be
// retried by a publisher's own retry loop, and mutating a caller's request from
// inside a RoundTripper violates the http.RoundTripper contract.
type authRoundTripper struct {
	next http.RoundTripper

	// basicUsername/basicPassword are set together when basic_auth applies.
	basicAuth                    bool
	basicUsername, basicPassword string

	// authorizationHeader is the fully rendered "<Type> <credentials>" value.
	authorizationHeader string
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if a.basicAuth {
		clone.SetBasicAuth(a.basicUsername, a.basicPassword)
	}
	if a.authorizationHeader != "" {
		clone.Header.Set("Authorization", a.authorizationHeader)
	}
	return a.next.RoundTrip(clone)
}

// wrapAuthRoundTripper returns transport unchanged when cfg carries no
// credentials, and otherwise wraps it in an authRoundTripper. Credential FILES
// are read here, so an unreadable one fails the target build.
func wrapAuthRoundTripper(transport http.RoundTripper, cfg *core.HTTPClientConfig) (http.RoundTripper, error) {
	wrapper := &authRoundTripper{next: transport}

	if basic := cfg.BasicAuth; basic != nil {
		password, err := resolveCredential(basic.Password, basic.PasswordFile, "basic_auth.password_file")
		if err != nil {
			return nil, err
		}
		if basic.Username == "" && password == "" {
			return transport, nil
		}
		wrapper.basicAuth = true
		wrapper.basicUsername = basic.Username
		wrapper.basicPassword = password
	}

	if authz := cfg.Authorization; authz != nil {
		credentials, err := resolveCredential(authz.Credentials, authz.CredentialsFile, "authorization.credentials_file")
		if err != nil {
			return nil, err
		}
		if credentials == "" {
			// A scheme with no credential renders as a bare "Bearer", which no
			// endpoint accepts. The config-target builder already drops this
			// shape with a WARN; this is the K8s-Secret path's equivalent guard.
			return nil, errors.New("http_config: authorization has neither credentials nor a readable credentials_file")
		}
		authType := strings.TrimSpace(authz.Type)
		if authType == "" {
			authType = core.DefaultAuthorizationType
		}
		wrapper.authorizationHeader = authType + " " + credentials
	}

	if !wrapper.basicAuth && wrapper.authorizationHeader == "" {
		return transport, nil
	}
	return wrapper, nil
}

// resolveCredential returns the credential from file when a path is given,
// otherwise the inline value. The FILE wins when both are set, matching
// upstream's precedence.
//
// Trailing whitespace is trimmed: a Kubernetes Secret mounted as a file, or one
// written with `echo`, routinely carries a trailing newline, and sending it
// inside an Authorization header silently breaks the request.
func resolveCredential(inline, path, fieldName string) (string, error) {
	if path == "" {
		return inline, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("http_config: %s %q: %w", fieldName, path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// ============================================================================
// Per-target client cache
// ============================================================================

// httpClientShape names the BASE client template a per-target client is built
// from. It is part of the cache key because the base tunings genuinely differ
// per publisher (the webhook client pools 100 idle connections and forces
// HTTP/2; the Slack and Telegram clients pool 10). Keying on the http_config
// fingerprint alone would let a webhook target and a Slack target with
// identical http_config share whichever client was built first — the same
// cache-key defect class as the telegram and pagerduty keys.
type httpClientShape string

const (
	shapeWebhook   httpClientShape = "webhook"
	shapeSlack     httpClientShape = "slack"
	shapeTelegram  httpClientShape = "telegram"
	shapePagerDuty httpClientShape = "pagerduty"
	shapeRootly    httpClientShape = "rootly"

	// shapeBasic is the plain HTTPPublisher client (30s timeout, default
	// transport) used by CreateBasicPublisherForTarget.
	shapeBasic httpClientShape = "basic"
)

// perTargetHTTPClient returns the *http.Client for (shape, cfg), or nil when cfg
// carries nothing — a nil result tells the caller to let its own client
// constructor build the default, which keeps every pre-existing target on
// exactly its current code path.
//
// Clients are CACHED so the TLS and credential files behind an http_config are
// read once per distinct configuration rather than on every publish job
// (CreatePublisherForTarget runs per job on the queue's worker pool).
//
// newBase is only invoked on a cache miss.
//
// CONSEQUENCE OF CACHING, deliberately accepted and documented: the file
// CONTENTS are captured at first use. Rotating a client certificate or a
// password file on disk does not affect an already-built client, so it takes a
// restart (or a config reload that changes the fingerprint) to pick up. Upstream
// re-reads on each handshake; matching that needs tls.Config.
// GetClientCertificate plumbing and is tracked separately.
func (f *PublisherFactory) perTargetHTTPClient(
	shape httpClientShape,
	cfg *core.HTTPClientConfig,
	newBase func() *http.Client,
) (*http.Client, error) {
	fingerprint := cfg.Fingerprint()
	if fingerprint == "" {
		return nil, nil
	}
	cacheKey := string(shape) + "|" + fingerprint

	f.clientMu.RLock()
	client, ok := f.httpClientMap[cacheKey]
	f.clientMu.RUnlock()
	if ok {
		return client, nil
	}

	// Built OUTSIDE the write lock: this reads files off disk and must not hold
	// up every other publisher construction while it does. A concurrent builder
	// may win the race, in which case the double-check below discards this copy —
	// cheap and rare compared to serialising file I/O under a lock.
	built, err := applyHTTPClientConfig(newBase(), cfg)
	if err != nil {
		return nil, err
	}

	f.clientMu.Lock()
	defer f.clientMu.Unlock()
	if client, ok = f.httpClientMap[cacheKey]; ok {
		return client, nil
	}
	f.httpClientMap[cacheKey] = built
	return built, nil
}
