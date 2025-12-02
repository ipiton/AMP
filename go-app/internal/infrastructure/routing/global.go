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

	// SMTP configuration (FUTURE - TN-154)
	SMTPFrom         string `yaml:"smtp_from,omitempty" validate:"omitempty,email"`
	SMTPSmartHost    string `yaml:"smtp_smarthost,omitempty"`
	SMTPAuthUsername string `yaml:"smtp_auth_username,omitempty"`
	SMTPAuthPassword string `yaml:"smtp_auth_password,omitempty"`
	SMTPRequireTLS   bool   `yaml:"smtp_require_tls,omitempty"`

	// HTTPConfig specifies default HTTP client settings
	// Used by all receivers unless overridden
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`
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

	// FollowRedirects determines if HTTP redirects are followed
	// Default: true
	FollowRedirects *bool `yaml:"follow_redirects,omitempty"`

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
	if h.TLSConfig != nil {
		// No defaults for TLSConfig currently
	}
}

// Clone creates a deep copy.
func (h *HTTPConfig) Clone() *HTTPConfig {
	clone := &HTTPConfig{
		ProxyURL:       h.ProxyURL,
		ConnectTimeout: h.ConnectTimeout,
		RequestTimeout: h.RequestTimeout,
	}

	if h.FollowRedirects != nil {
		followRedirects := *h.FollowRedirects
		clone.FollowRedirects = &followRedirects
	}
	if h.TLSConfig != nil {
		clone.TLSConfig = h.TLSConfig.Clone()
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
