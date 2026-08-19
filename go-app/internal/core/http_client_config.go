package core

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
)

// ============================================================================
// Per-integration HTTP client configuration (FU-HTTP-CONFIG, wave 7 track C)
// ============================================================================
//
// WHY THIS TYPE LIVES IN core: it is carried by PublishingTarget, which is the
// contract between the target SOURCES (a K8s Secret's JSON blob, parsed by
// business/publishing.parseSecret; the `receivers:` section of an
// Alertmanager-shaped config, mapped by business/publishing.BuildConfigTargets)
// and the target CONSUMER (infrastructure/publishing.PublisherFactory.
// CreatePublisherForTarget, which turns it into an *http.Client). core is the
// only package all three may import.
//
// It deliberately MIRRORS upstream Alertmanager's `http_config` (which is
// prometheus/common/config.HTTPClientConfig) rather than inventing a shape, so
// an untouched upstream config maps field-for-field. The routing package's own
// YAML-facing routing.HTTPConfig is the parse-time twin of this type; this one
// is the transport-neutral, JSON-serialisable form that reaches the publisher.
//
// SUPPORTED SUBSET (see docs/ALERTMANAGER_COMPATIBILITY.md): proxy_url,
// tls_config (all five fields), basic_auth, authorization (bearer),
// follow_redirects. NOT supported: oauth2 (needs a token-refresh loop and a
// dependency AMP does not carry) — an oauth2 block is reported loudly rather
// than silently ignored, see routing.HTTPConfig.OAuth2.
//
// SECRETS: BasicAuth.Password and Authorization.Credentials are credentials.
// Nothing in this file emits them: Fingerprint hashes them (so a cache key
// derived from it is safe to log) and Redacted returns a copy safe to serialise
// into an API response or a log line.

// HTTPClientConfig is the per-target HTTP client configuration carried by
// PublishingTarget.
//
// A nil *HTTPClientConfig means "no per-target configuration" — every publisher
// keeps its own built-in client, byte-for-byte the behaviour that predates this
// type. IsZero reports the same thing for a non-nil but empty struct (a
// config block present in YAML/JSON but carrying nothing).
type HTTPClientConfig struct {
	// ProxyURL is upstream's `http_config.proxy_url`: the proxy every request
	// from this target goes through. Empty means the standard library's own
	// environment-based proxy resolution (HTTP_PROXY/HTTPS_PROXY/NO_PROXY)
	// applies, which is what upstream falls back to as well.
	ProxyURL string `json:"proxy_url,omitempty"`

	// TLSConfig is upstream's `http_config.tls_config`.
	TLSConfig *TLSClientConfig `json:"tls_config,omitempty"`

	// BasicAuth is upstream's `http_config.basic_auth`.
	BasicAuth *BasicAuthConfig `json:"basic_auth,omitempty"`

	// Authorization is upstream's `http_config.authorization` (the modern
	// replacement for the deprecated `bearer_token`).
	Authorization *AuthorizationConfig `json:"authorization,omitempty"`

	// FollowRedirects is upstream's `http_config.follow_redirects`.
	// nil means the upstream default, true.
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

// TLSClientConfig mirrors upstream's `http_config.tls_config`.
//
// The three *File fields are PATHS read at client-construction time (see
// infrastructure/publishing.buildTLSConfig). A path that cannot be read makes
// the whole target fail to build — loudly logged and skipped, never
// process-fatal.
type TLSClientConfig struct {
	// CAFile is the PEM bundle used to verify the server certificate. Empty
	// means the system trust store.
	CAFile string `json:"ca_file,omitempty"`

	// CertFile/KeyFile are the client certificate pair for mutual TLS. Both
	// must be set together; one without the other is a build error.
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`

	// ServerName overrides SNI / the hostname verified against the
	// certificate. Needed when connecting to an IP or through a proxy that
	// terminates on a different name.
	ServerName string `json:"server_name,omitempty"`

	// InsecureSkipVerify disables server certificate verification entirely.
	// Testing only — it defeats TLS's authentication guarantee.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
}

// BasicAuthConfig mirrors upstream's `http_config.basic_auth`.
//
// PasswordFile is an alternative source for Password: exactly one of the two
// should be set, and PasswordFile wins if both are (matching upstream's
// precedence). The file is read at client-construction time, same failure
// handling as the TLS files.
type BasicAuthConfig struct {
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	PasswordFile string `json:"password_file,omitempty"`
}

// AuthorizationConfig mirrors upstream's `http_config.authorization`.
//
// Type defaults to "Bearer" when empty (upstream's default). Credentials and
// CredentialsFile are the two sources for the value; CredentialsFile wins if
// both are set.
type AuthorizationConfig struct {
	Type            string `json:"type,omitempty"`
	Credentials     string `json:"credentials,omitempty"`
	CredentialsFile string `json:"credentials_file,omitempty"`
}

// DefaultAuthorizationType is the scheme upstream assumes when
// `authorization.type` is omitted.
const DefaultAuthorizationType = "Bearer"

// RedactedPlaceholder replaces every credential in the output of Redacted.
const RedactedPlaceholder = "<secret>"

// IsZero reports whether c carries no configuration at all, in which case the
// publisher layer must behave exactly as it did before per-target HTTP config
// existed (keep the built-in client, add nothing to the cache key).
//
// A nil receiver is zero. This is deliberately a method on the pointer type so
// callers can write cfg.IsZero() without a nil check of their own — the single
// most likely place to introduce a nil-deref in this feature.
func (c *HTTPClientConfig) IsZero() bool {
	if c == nil {
		return true
	}
	return c.ProxyURL == "" &&
		c.TLSConfig == nil &&
		c.BasicAuth == nil &&
		c.Authorization == nil &&
		c.FollowRedirects == nil
}

// Clone returns a deep copy. Used wherever a target is handed across a
// boundary that may mutate it (config reload rebuilds, test fixtures), so two
// targets can never share mutable HTTP config state.
func (c *HTTPClientConfig) Clone() *HTTPClientConfig {
	if c == nil {
		return nil
	}
	clone := &HTTPClientConfig{ProxyURL: c.ProxyURL}
	if c.TLSConfig != nil {
		tlsCopy := *c.TLSConfig
		clone.TLSConfig = &tlsCopy
	}
	if c.BasicAuth != nil {
		authCopy := *c.BasicAuth
		clone.BasicAuth = &authCopy
	}
	if c.Authorization != nil {
		authzCopy := *c.Authorization
		clone.Authorization = &authzCopy
	}
	if c.FollowRedirects != nil {
		follow := *c.FollowRedirects
		clone.FollowRedirects = &follow
	}
	return clone
}

// Fingerprint returns a stable, secret-free hash of every field that changes
// the behaviour of the resulting *http.Client.
//
// WHY IT EXISTS: publisher clients are CACHED, and every cache in
// PublisherFactory is keyed by endpoint + credential. Without the fingerprint
// in that key, two targets pointing at the same URL with the same token but
// DIFFERENT http_config (one direct, one through a corp proxy; one with mTLS,
// one without) silently share whichever client was constructed first — the
// exact defect class already fixed twice for the telegram and pagerduty caches
// (cache-key I3 / R5). This is the third occurrence of the pattern, so the
// fingerprint is mandatory rather than an optimisation.
//
// A zero config fingerprints as "" so cache keys for targets WITHOUT any HTTP
// config are unchanged in shape and no extra client is ever allocated.
//
// The digest covers the credential VALUES (two targets differing only by
// password must not share a client) but is a SHA-256 hash, so the result is
// safe to embed in a key that ends up in a log line or a metric label. Fields
// are written with an explicit separator that cannot occur in any of them
// (0x00) so no two distinct configs can serialise to the same byte stream.
func (c *HTTPClientConfig) Fingerprint() string {
	if c.IsZero() {
		return ""
	}

	h := sha256.New()
	writeField := func(values ...string) {
		for _, v := range values {
			_, _ = io.WriteString(h, v)
			_, _ = h.Write([]byte{0})
		}
	}

	writeField("proxy", c.ProxyURL)

	if c.TLSConfig != nil {
		writeField("tls",
			c.TLSConfig.CAFile,
			c.TLSConfig.CertFile,
			c.TLSConfig.KeyFile,
			c.TLSConfig.ServerName,
			strconv.FormatBool(c.TLSConfig.InsecureSkipVerify),
		)
	}
	if c.BasicAuth != nil {
		writeField("basic",
			c.BasicAuth.Username,
			c.BasicAuth.Password,
			c.BasicAuth.PasswordFile,
		)
	}
	if c.Authorization != nil {
		writeField("authz",
			c.Authorization.Type,
			c.Authorization.Credentials,
			c.Authorization.CredentialsFile,
		)
	}
	if c.FollowRedirects != nil {
		writeField("redirects", strconv.FormatBool(*c.FollowRedirects))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Redacted returns a deep copy with every credential replaced by
// RedactedPlaceholder, for API responses, status output and log lines.
//
// Non-secret fields survive verbatim so the copy stays diagnostically useful:
// proxy_url, every tls_config field (paths, not contents), basic_auth.username,
// authorization.type, follow_redirects. The *_file PATHS also survive — a path
// is not a credential, and hiding it makes "unreadable ca_file" impossible to
// debug — while password/credentials VALUES do not.
//
// A nil receiver returns nil rather than an empty struct, so a caller
// serialising the result emits no `http_config` key at all for a target that
// has none.
func (c *HTTPClientConfig) Redacted() *HTTPClientConfig {
	clone := c.Clone()
	if clone == nil {
		return nil
	}
	if clone.BasicAuth != nil && clone.BasicAuth.Password != "" {
		clone.BasicAuth.Password = RedactedPlaceholder
	}
	if clone.Authorization != nil && clone.Authorization.Credentials != "" {
		clone.Authorization.Credentials = RedactedPlaceholder
	}
	return clone
}
