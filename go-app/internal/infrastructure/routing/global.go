package routing

import (
	"time"
)

// GlobalConfig represents global Alertmanager configuration.
// These settings apply to all receivers unless overridden.
//
// Example:
//
//	global:
//	  resolve_timeout: 5m
//	  http_config:
//	    proxy_url: http://proxy.corp:8080
//	    tls_config:
//	      insecure_skip_verify: false
type GlobalConfig struct {
	// ResolveTimeout is the default time to wait before resolving an alert
	// Default: 5m
	// Used when route doesn't specify repeat_interval
	ResolveTimeout *Duration `yaml:"resolve_timeout,omitempty"`

	// Per-integration ENDPOINT fallbacks (FU-RECEIVERS-INTEGRATION slice 2,
	// upstream fidelity). Upstream Alertmanager lets an integration omit its
	// endpoint and inherit it from `global:`; the per-integration value always
	// wins. Resolved at PARSE time (RouteConfigParser.resolveGlobalFallbacks),
	// so everything downstream — the route tree, the publishing-target builder,
	// the status API — sees one already-resolved endpoint rather than having to
	// re-implement the fallback.
	//
	// SlackAPIURL is upstream's `global.slack_api_url`: a Slack incoming-webhook
	// URL, i.e. itself a CREDENTIAL (upstream types it SecretURL, and the status
	// API redacts it — see handlers.sectionSecretKeys).
	SlackAPIURL string `yaml:"slack_api_url,omitempty" validate:"omitempty,url,https_production"`

	// PagerDutyURL is upstream's `global.pagerduty_url`: the Events API
	// endpoint, public and credential-free (the routing_key is the secret).
	PagerDutyURL string `yaml:"pagerduty_url,omitempty" validate:"omitempty,url,https_production"`

	// TelegramAPIURL is upstream's `global.telegram_api_url`: the Bot API base,
	// public (the bot_token is the secret).
	TelegramAPIURL string `yaml:"telegram_api_url,omitempty" validate:"omitempty,url,https_production"`

	// SMTP configuration. smtp_smarthost/smtp_from are REQUIRED (by
	// validateSemantics) as soon as any receiver declares email_configs, because
	// routing.EmailConfig models no per-integration SMTP fields at all — same
	// posture as upstream, which requires a global smarthost/from whenever an
	// email_config omits its own.
	SMTPFrom         string `yaml:"smtp_from,omitempty" validate:"omitempty,email"`
	SMTPSmartHost    string `yaml:"smtp_smarthost,omitempty"`
	SMTPAuthUsername string `yaml:"smtp_auth_username,omitempty"`
	SMTPAuthPassword string `yaml:"smtp_auth_password,omitempty"`
	SMTPRequireTLS   bool   `yaml:"smtp_require_tls,omitempty"`

	// HTTPConfig specifies default HTTP client settings
	// Used by all receivers unless overridden
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`

	// GroupBy/GroupWait/GroupInterval/RepeatInterval (alertmanager-parity
	// wave-5, FU-GLOB-DEFAULT-VALUES) are grouping-default fallbacks
	// consulted by business/routing.TreeBuilder's inheritGroupBy/
	// inheritDuration when NEITHER a route NOR any of its ancestors set the
	// corresponding field — i.e. below root-route inheritance, above the
	// hardcoded upstream defaults (["alertname"] / 30s / 5m / 4h).
	//
	// Honesty note: this is an AMP-only convenience, not part of upstream
	// Alertmanager's `global:` schema (upstream's global: has no grouping
	// fields at all — its equivalent mechanism is simply setting these on
	// the root `route:`, which cascades via the same parent-chain
	// inheritance these fields sit below). It existed in this package's
	// pre-dedup local GlobalConfig (deleted by 3f8d69d, TN-137) and is
	// restored here on the canonical type; see tree_builder.go's
	// inheritGroupBy/inheritDuration for the consulting side and
	// docs/ALERTMANAGER_COMPATIBILITY.md for the parity note.
	//
	// nil/empty means "not set" — GroupBy/durations left unset here do NOT
	// get defaulted by GlobalConfig.Defaults() below; an unset field simply
	// falls through to the next priority (the hardcoded default).
	GroupBy        []string  `yaml:"group_by,omitempty"`
	GroupWait      *Duration `yaml:"group_wait,omitempty"`
	GroupInterval  *Duration `yaml:"group_interval,omitempty"`
	RepeatInterval *Duration `yaml:"repeat_interval,omitempty"`
}

// Defaults applies default values.
func (g *GlobalConfig) Defaults() {
	if g.ResolveTimeout == nil {
		defaultTimeout := Duration(5 * time.Minute)
		g.ResolveTimeout = &defaultTimeout
	}
	if g.HTTPConfig != nil {
		g.HTTPConfig.Defaults()
	}
}

// Clone creates a deep copy.
func (g *GlobalConfig) Clone() *GlobalConfig {
	clone := &GlobalConfig{
		SlackAPIURL:      g.SlackAPIURL,
		PagerDutyURL:     g.PagerDutyURL,
		TelegramAPIURL:   g.TelegramAPIURL,
		SMTPFrom:         g.SMTPFrom,
		SMTPSmartHost:    g.SMTPSmartHost,
		SMTPAuthUsername: g.SMTPAuthUsername,
		SMTPAuthPassword: g.SMTPAuthPassword,
		SMTPRequireTLS:   g.SMTPRequireTLS,
	}

	if g.ResolveTimeout != nil {
		timeout := *g.ResolveTimeout
		clone.ResolveTimeout = &timeout
	}
	if g.HTTPConfig != nil {
		clone.HTTPConfig = g.HTTPConfig.Clone()
	}

	if len(g.GroupBy) > 0 {
		clone.GroupBy = append([]string(nil), g.GroupBy...)
	}
	if g.GroupWait != nil {
		v := *g.GroupWait
		clone.GroupWait = &v
	}
	if g.GroupInterval != nil {
		v := *g.GroupInterval
		clone.GroupInterval = &v
	}
	if g.RepeatInterval != nil {
		v := *g.RepeatInterval
		clone.RepeatInterval = &v
	}

	return clone
}

// HTTPConfig specifies HTTP client configuration.
// Used by webhook, PagerDuty, and Slack receivers.
//
// Example:
//
//	http_config:
//	  proxy_url: http://proxy.corp:8080
//	  tls_config:
//	    ca_file: /etc/ssl/ca.crt
//	    cert_file: /etc/ssl/client.crt
//	    key_file: /etc/ssl/client.key
//	  follow_redirects: true
//	  connect_timeout: 10s
//	  request_timeout: 30s
type HTTPConfig struct {
	// ProxyURL specifies an HTTP proxy
	// Format: http://host:port or https://host:port
	ProxyURL string `yaml:"proxy_url,omitempty" validate:"omitempty,url"`

	// TLSConfig specifies TLS settings
	TLSConfig *TLSConfig `yaml:"tls_config,omitempty"`

	// BasicAuth is upstream's `http_config.basic_auth` (FU-HTTP-CONFIG):
	// HTTP Basic credentials applied to every request this integration makes.
	BasicAuth *BasicAuth `yaml:"basic_auth,omitempty"`

	// Authorization is upstream's `http_config.authorization` (FU-HTTP-CONFIG):
	// an Authorization header with an explicit scheme, the modern replacement
	// for upstream's deprecated `bearer_token`. Type defaults to "Bearer".
	Authorization *Authorization `yaml:"authorization,omitempty"`

	// OAuth2 is PARSED BUT NOT HONOURED (FU-HTTP-OAUTH2, open).
	//
	// It is modelled deliberately rather than left to fall through YAML's
	// permissive unmarshalling: without a field here, an operator migrating a
	// config whose webhook is protected by OAuth2 would get UNAUTHENTICATED
	// requests with no signal whatsoever. With it, the publishing-target
	// builder can log one loud WARN naming the receiver and integration (see
	// business/publishing.httpConfigFromRouting), which — together with the
	// 401s the endpoint will return — makes the gap visible.
	//
	// Supporting it for real needs a token endpoint client plus a refresh loop
	// with its own failure/retry semantics; that is its own unit of work, not a
	// field mapping. Tracked as FU-HTTP-OAUTH2 in docs/06-planning/BACKLOG.md.
	OAuth2 *OAuth2Config `yaml:"oauth2,omitempty"`

	// FollowRedirects determines if HTTP redirects are followed
	// Default: true
	FollowRedirects *bool `yaml:"follow_redirects,omitempty"`

	// inheritedFromGlobal records that this block is a CLONE of
	// `global.http_config` rather than the integration's own declaration
	// (set by ResolveHTTPConfigFallback).
	//
	// It exists for one decision only: the oauth2 provenance split (review I1).
	// An `oauth2:` block the integration declared ITSELF is an explicit
	// per-endpoint auth requirement, so that target fails closed. The same block
	// INHERITED from `global:` must not blackhole integrations that never asked
	// for OAuth2 — one global block would otherwise take down every webhook,
	// Slack, Telegram and PagerDuty target in the process, most of which
	// authenticate through their own URL/header credential and never needed
	// OAuth2 at all.
	//
	// Deliberately UNEXPORTED: `gopkg.in/yaml.v3` and the struct validator both
	// ignore unexported fields, so this cannot leak into /api/v2/status's
	// rendered config or affect validation. Read it through
	// InheritedFromGlobal().
	inheritedFromGlobal bool

	// ConnectTimeout specifies the maximum time to establish a connection
	// Default: 10s
	ConnectTimeout time.Duration `yaml:"connect_timeout,omitempty"`

	// RequestTimeout specifies the maximum time for the entire request
	// Default: 30s
	RequestTimeout time.Duration `yaml:"request_timeout,omitempty"`
}

// Defaults applies default values.
func (h *HTTPConfig) Defaults() {
	if h.FollowRedirects == nil {
		followRedirects := true
		h.FollowRedirects = &followRedirects
	}
	if h.ConnectTimeout == 0 {
		h.ConnectTimeout = 10 * time.Second
	}
	if h.RequestTimeout == 0 {
		h.RequestTimeout = 30 * time.Second
	}
	// No defaults for h.TLSConfig currently
}

// Clone creates a deep copy.
func (h *HTTPConfig) Clone() *HTTPConfig {
	clone := &HTTPConfig{
		ProxyURL:            h.ProxyURL,
		ConnectTimeout:      h.ConnectTimeout,
		RequestTimeout:      h.RequestTimeout,
		inheritedFromGlobal: h.inheritedFromGlobal,
	}

	if h.FollowRedirects != nil {
		followRedirects := *h.FollowRedirects
		clone.FollowRedirects = &followRedirects
	}
	if h.TLSConfig != nil {
		clone.TLSConfig = h.TLSConfig.Clone()
	}
	if h.BasicAuth != nil {
		clone.BasicAuth = h.BasicAuth.Clone()
	}
	if h.Authorization != nil {
		clone.Authorization = h.Authorization.Clone()
	}
	if h.OAuth2 != nil {
		clone.OAuth2 = h.OAuth2.Clone()
	}

	return clone
}

// RedactedValue is what Sanitize substitutes for a credential.
const RedactedValue = "[REDACTED]"

// Sanitize returns a deep copy with every credential replaced by RedactedValue,
// for log lines and any other rendering of a receiver config.
//
// Non-secret fields survive so the copy stays diagnostically useful: proxy_url,
// every tls_config field (paths, not contents), basic_auth.username,
// authorization.type, follow_redirects, timeouts, and the *_file PATHS.
//
// A nil receiver returns nil, so the caller's `if cfg.HTTPConfig != nil` shape
// is preserved through sanitisation.
func (h *HTTPConfig) Sanitize() *HTTPConfig {
	if h == nil {
		return nil
	}
	clone := h.Clone()
	if clone.BasicAuth != nil && clone.BasicAuth.Password != "" {
		clone.BasicAuth.Password = RedactedValue
	}
	if clone.Authorization != nil && clone.Authorization.Credentials != "" {
		clone.Authorization.Credentials = RedactedValue
	}
	if clone.OAuth2 != nil && clone.OAuth2.ClientSecret != "" {
		clone.OAuth2.ClientSecret = RedactedValue
	}
	return clone
}

// InheritedFromGlobal reports whether this block was inherited wholesale from
// `global.http_config` rather than declared by the integration itself. See the
// inheritedFromGlobal field for the one decision that depends on it.
//
// A nil receiver reports false: there is no block, so nothing was inherited.
func (h *HTTPConfig) InheritedFromGlobal() bool {
	return h != nil && h.inheritedFromGlobal
}

// markInheritedFromGlobal is the only way the flag is ever set — see
// ResolveHTTPConfigFallback.
func (h *HTTPConfig) markInheritedFromGlobal() {
	if h != nil {
		h.inheritedFromGlobal = true
	}
}

// BasicAuth specifies HTTP Basic authentication credentials, mirroring
// upstream Alertmanager's `http_config.basic_auth`.
//
// Exactly one of Password / PasswordFile should be set. PasswordFile wins when
// both are, matching upstream's precedence; the file is read at HTTP-client
// construction time by the publisher layer, and an unreadable path makes the
// target fail to build (logged loudly, target skipped) rather than sending
// requests with no credential at all.
type BasicAuth struct {
	// Username is the Basic auth user.
	Username string `yaml:"username,omitempty"`

	// Password is the inline credential. Prefer PasswordFile in production so
	// the secret never lands in the config document (which the status API
	// renders, redacted — see handlers.secretKeySubstrings).
	Password string `yaml:"password,omitempty"`

	// PasswordFile is a path whose CONTENTS are the credential, trailing
	// whitespace trimmed. Wins over Password when both are set.
	PasswordFile string `yaml:"password_file,omitempty"`
}

// Clone creates a deep copy.
func (b *BasicAuth) Clone() *BasicAuth {
	return &BasicAuth{
		Username:     b.Username,
		Password:     b.Password,
		PasswordFile: b.PasswordFile,
	}
}

// DefaultAuthorizationType is the scheme upstream assumes when
// `http_config.authorization.type` is omitted.
const DefaultAuthorizationType = "Bearer"

// Authorization specifies an Authorization header with an explicit scheme,
// mirroring upstream Alertmanager's `http_config.authorization`.
//
// The rendered header is `<Type> <credentials>`; Type defaults to "Bearer".
// CredentialsFile behaves exactly like BasicAuth.PasswordFile — read at client
// construction, wins over the inline value.
type Authorization struct {
	// Type is the auth scheme (default: Bearer).
	Type string `yaml:"type,omitempty"`

	// Credentials is the inline credential.
	Credentials string `yaml:"credentials,omitempty"`

	// CredentialsFile is a path whose CONTENTS are the credential.
	CredentialsFile string `yaml:"credentials_file,omitempty"`
}

// Clone creates a deep copy.
func (a *Authorization) Clone() *Authorization {
	return &Authorization{
		Type:            a.Type,
		Credentials:     a.Credentials,
		CredentialsFile: a.CredentialsFile,
	}
}

// OAuth2Config models upstream's `http_config.oauth2` so its presence can be
// DETECTED and reported. AMP does not implement it — see HTTPConfig.OAuth2 and
// FU-HTTP-OAUTH2. Fields exist only so a config carrying an oauth2 block still
// round-trips through Clone and the status API's redaction pass.
type OAuth2Config struct {
	ClientID         string            `yaml:"client_id,omitempty"`
	ClientSecret     string            `yaml:"client_secret,omitempty"`
	ClientSecretFile string            `yaml:"client_secret_file,omitempty"`
	TokenURL         string            `yaml:"token_url,omitempty"`
	Scopes           []string          `yaml:"scopes,omitempty"`
	EndpointParams   map[string]string `yaml:"endpoint_params,omitempty"`
}

// Clone creates a deep copy.
func (o *OAuth2Config) Clone() *OAuth2Config {
	clone := &OAuth2Config{
		ClientID:         o.ClientID,
		ClientSecret:     o.ClientSecret,
		ClientSecretFile: o.ClientSecretFile,
		TokenURL:         o.TokenURL,
	}
	if len(o.Scopes) > 0 {
		clone.Scopes = append([]string(nil), o.Scopes...)
	}
	if o.EndpointParams != nil {
		clone.EndpointParams = make(map[string]string, len(o.EndpointParams))
		for k, v := range o.EndpointParams {
			clone.EndpointParams[k] = v
		}
	}
	return clone
}

// TLSConfig specifies TLS client configuration.
//
// Example:
//
//	tls_config:
//	  ca_file: /etc/ssl/ca.crt
//	  cert_file: /etc/ssl/client.crt
//	  key_file: /etc/ssl/client.key
//	  server_name: webhook.example.com
//	  insecure_skip_verify: false
type TLSConfig struct {
	// CAFile is the path to the CA certificate file
	// Used to verify server certificates
	CAFile string `yaml:"ca_file,omitempty"`

	// CertFile is the path to the client certificate file
	// Used for mutual TLS authentication
	CertFile string `yaml:"cert_file,omitempty"`

	// KeyFile is the path to the client private key file
	// Used for mutual TLS authentication
	KeyFile string `yaml:"key_file,omitempty"`

	// ServerName overrides the server name for SNI
	// Used when connecting via IP or proxy
	ServerName string `yaml:"server_name,omitempty"`

	// InsecureSkipVerify disables server certificate verification
	// WARNING: This is insecure and should only be used for testing
	// Default: false
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// Clone creates a deep copy.
func (t *TLSConfig) Clone() *TLSConfig {
	return &TLSConfig{
		CAFile:             t.CAFile,
		CertFile:           t.CertFile,
		KeyFile:            t.KeyFile,
		ServerName:         t.ServerName,
		InsecureSkipVerify: t.InsecureSkipVerify,
	}
}

// Duration wraps time.Duration to support YAML unmarshaling.
// Allows human-readable durations in config: 1m, 5m, 1h, etc.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var str string
	if err := unmarshal(&str); err != nil {
		return err
	}

	duration, err := time.ParseDuration(str)
	if err != nil {
		return err
	}

	*d = Duration(duration)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}
