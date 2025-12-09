package config

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// HTTPClientConfig holds configuration for HTTP clients
type HTTPClientConfig struct {
	// Timeout is the total timeout for the entire request (including retries)
	Timeout time.Duration `mapstructure:"timeout" yaml:"timeout"`

	// DialTimeout is the timeout for establishing a TCP connection
	DialTimeout time.Duration `mapstructure:"dial_timeout" yaml:"dial_timeout"`

	// TLSHandshakeTimeout is the timeout for the TLS handshake
	TLSHandshakeTimeout time.Duration `mapstructure:"tls_handshake_timeout" yaml:"tls_handshake_timeout"`

	// ResponseHeaderTimeout is the timeout for receiving response headers
	ResponseHeaderTimeout time.Duration `mapstructure:"response_header_timeout" yaml:"response_header_timeout"`

	// ExpectContinueTimeout is the timeout for waiting for a "100 Continue" response
	ExpectContinueTimeout time.Duration `mapstructure:"expect_continue_timeout" yaml:"expect_continue_timeout"`

	// KeepAlive is the interval for sending keep-alive probes
	KeepAlive time.Duration `mapstructure:"keep_alive" yaml:"keep_alive"`

	// IdleConnTimeout is the timeout for idle connections
	IdleConnTimeout time.Duration `mapstructure:"idle_conn_timeout" yaml:"idle_conn_timeout"`

	// MaxIdleConns is the maximum number of idle connections across all hosts
	MaxIdleConns int `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`

	// MaxIdleConnsPerHost is the maximum number of idle connections per host
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host" yaml:"max_idle_conns_per_host"`

	// MaxConnsPerHost is the maximum number of total connections per host (0 = unlimited)
	MaxConnsPerHost int `mapstructure:"max_conns_per_host" yaml:"max_conns_per_host"`

	// MinTLSVersion is the minimum TLS version (e.g., "1.2", "1.3")
	MinTLSVersion string `mapstructure:"min_tls_version" yaml:"min_tls_version"`

	// DisableHTTP2 disables HTTP/2 support
	DisableHTTP2 bool `mapstructure:"disable_http2" yaml:"disable_http2"`

	// InsecureSkipVerify skips TLS certificate verification (INSECURE, only for development)
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify" yaml:"insecure_skip_verify"`
}

// DefaultHTTPClientConfig returns default HTTP client configuration
func DefaultHTTPClientConfig() HTTPClientConfig {
	return HTTPClientConfig{
		Timeout:                30 * time.Second,
		DialTimeout:            5 * time.Second,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		KeepAlive:              30 * time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		MaxConnsPerHost:        0, // Unlimited
		MinTLSVersion:          "1.2",
		DisableHTTP2:           false,
		InsecureSkipVerify:     false,
	}
}

// NewHTTPClient creates a new http.Client from the configuration
func NewHTTPClient(cfg HTTPClientConfig) *http.Client {
	// Set defaults for zero values
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.TLSHandshakeTimeout == 0 {
		cfg.TLSHandshakeTimeout = 5 * time.Second
	}
	if cfg.ResponseHeaderTimeout == 0 {
		cfg.ResponseHeaderTimeout = 10 * time.Second
	}
	if cfg.ExpectContinueTimeout == 0 {
		cfg.ExpectContinueTimeout = 1 * time.Second
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 30 * time.Second
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 100
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = 10
	}
	if cfg.MinTLSVersion == "" {
		cfg.MinTLSVersion = "1.2"
	}

	// Parse TLS version
	tlsVersion := parseTLSVersion(cfg.MinTLSVersion)

	// Create transport with configured settings
	transport := &http.Transport{
		// TLS configuration
		TLSClientConfig: &tls.Config{
			MinVersion:         tlsVersion,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		},
		// Connection pooling settings
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		// HTTP/2 support
		ForceAttemptHTTP2: !cfg.DisableHTTP2,
		// Timeouts
		DialContext: (&net.Dialer{
			Timeout:   cfg.DialTimeout,
			KeepAlive: cfg.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
	}

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}
}

// parseTLSVersion converts a string TLS version to tls constant
func parseTLSVersion(version string) uint16 {
	switch version {
	case "1.0":
		return tls.VersionTLS10
	case "1.1":
		return tls.VersionTLS11
	case "1.2":
		return tls.VersionTLS12
	case "1.3":
		return tls.VersionTLS13
	default:
		return tls.VersionTLS12 // Default to TLS 1.2
	}
}
