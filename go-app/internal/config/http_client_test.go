package config

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestDefaultHTTPClientConfig(t *testing.T) {
	cfg := DefaultHTTPClientConfig()

	if cfg.Timeout != 30*time.Second {
		t.Errorf("Expected Timeout=30s, got %v", cfg.Timeout)
	}
	if cfg.DialTimeout != 5*time.Second {
		t.Errorf("Expected DialTimeout=5s, got %v", cfg.DialTimeout)
	}
	if cfg.TLSHandshakeTimeout != 5*time.Second {
		t.Errorf("Expected TLSHandshakeTimeout=5s, got %v", cfg.TLSHandshakeTimeout)
	}
	if cfg.ResponseHeaderTimeout != 10*time.Second {
		t.Errorf("Expected ResponseHeaderTimeout=10s, got %v", cfg.ResponseHeaderTimeout)
	}
	if cfg.MaxIdleConns != 100 {
		t.Errorf("Expected MaxIdleConns=100, got %d", cfg.MaxIdleConns)
	}
	if cfg.MaxIdleConnsPerHost != 10 {
		t.Errorf("Expected MaxIdleConnsPerHost=10, got %d", cfg.MaxIdleConnsPerHost)
	}
	if cfg.MinTLSVersion != "1.2" {
		t.Errorf("Expected MinTLSVersion=1.2, got %s", cfg.MinTLSVersion)
	}
	if cfg.DisableHTTP2 {
		t.Error("Expected DisableHTTP2=false")
	}
	if cfg.InsecureSkipVerify {
		t.Error("Expected InsecureSkipVerify=false")
	}
}

func TestNewHTTPClient(t *testing.T) {
	tests := []struct {
		name   string
		config HTTPClientConfig
		verify func(*testing.T, HTTPClientConfig)
	}{
		{
			name:   "default config",
			config: DefaultHTTPClientConfig(),
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				if client == nil {
					t.Fatal("Expected non-nil client")
				}
				if client.Timeout != 30*time.Second {
					t.Errorf("Expected Timeout=30s, got %v", client.Timeout)
				}
				if client.Transport == nil {
					t.Fatal("Expected non-nil Transport")
				}
			},
		},
		{
			name: "custom timeout",
			config: HTTPClientConfig{
				Timeout:     60 * time.Second,
				DialTimeout: 10 * time.Second,
			},
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				if client.Timeout != 60*time.Second {
					t.Errorf("Expected Timeout=60s, got %v", client.Timeout)
				}
			},
		},
		{
			name: "zero values use defaults",
			config: HTTPClientConfig{
				// All zero values
			},
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				if client.Timeout != 30*time.Second {
					t.Errorf("Expected default Timeout=30s, got %v", client.Timeout)
				}
			},
		},
		{
			name: "custom TLS version",
			config: HTTPClientConfig{
				MinTLSVersion: "1.3",
			},
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				transport := client.Transport.(*http.Transport)
				if transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
					t.Errorf("Expected TLS 1.3, got %d", transport.TLSClientConfig.MinVersion)
				}
			},
		},
		{
			name: "disable HTTP/2",
			config: HTTPClientConfig{
				DisableHTTP2: true,
			},
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				transport := client.Transport.(*http.Transport)
				if transport.ForceAttemptHTTP2 {
					t.Error("Expected ForceAttemptHTTP2=false")
				}
			},
		},
		{
			name: "insecure skip verify (development only)",
			config: HTTPClientConfig{
				InsecureSkipVerify: true,
			},
			verify: func(t *testing.T, cfg HTTPClientConfig) {
				client := NewHTTPClient(cfg)
				transport := client.Transport.(*http.Transport)
				if !transport.TLSClientConfig.InsecureSkipVerify {
					t.Error("Expected InsecureSkipVerify=true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, tt.config)
		})
	}
}

func TestParseTLSVersion(t *testing.T) {
	tests := []struct {
		version  string
		expected uint16
	}{
		{"1.0", tls.VersionTLS10},
		{"1.1", tls.VersionTLS11},
		{"1.2", tls.VersionTLS12},
		{"1.3", tls.VersionTLS13},
		{"invalid", tls.VersionTLS12}, // Default
		{"", tls.VersionTLS12},        // Default
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			result := parseTLSVersion(tt.version)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestHTTPClientConfigIntegration(t *testing.T) {
	// Test that client can be created and used
	cfg := HTTPClientConfig{
		Timeout:                10 * time.Second,
		DialTimeout:            2 * time.Second,
		TLSHandshakeTimeout:    3 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		ExpectContinueTimeout:  500 * time.Millisecond,
		KeepAlive:              15 * time.Second,
		IdleConnTimeout:        60 * time.Second,
		MaxIdleConns:           50,
		MaxIdleConnsPerHost:    5,
		MaxConnsPerHost:        10,
		MinTLSVersion:          "1.3",
		DisableHTTP2:           false,
		InsecureSkipVerify:     false,
	}

	client := NewHTTPClient(cfg)

	// Verify all settings
	if client.Timeout != 10*time.Second {
		t.Errorf("Expected Timeout=10s, got %v", client.Timeout)
	}

	transport := client.Transport.(*http.Transport)

	if transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Errorf("Expected TLS 1.3, got %d", transport.TLSClientConfig.MinVersion)
	}
	if transport.MaxIdleConns != 50 {
		t.Errorf("Expected MaxIdleConns=50, got %d", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 5 {
		t.Errorf("Expected MaxIdleConnsPerHost=5, got %d", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxConnsPerHost != 10 {
		t.Errorf("Expected MaxConnsPerHost=10, got %d", transport.MaxConnsPerHost)
	}
	if transport.IdleConnTimeout != 60*time.Second {
		t.Errorf("Expected IdleConnTimeout=60s, got %v", transport.IdleConnTimeout)
	}
	if transport.TLSHandshakeTimeout != 3*time.Second {
		t.Errorf("Expected TLSHandshakeTimeout=3s, got %v", transport.TLSHandshakeTimeout)
	}
	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("Expected ResponseHeaderTimeout=5s, got %v", transport.ResponseHeaderTimeout)
	}
	if transport.ExpectContinueTimeout != 500*time.Millisecond {
		t.Errorf("Expected ExpectContinueTimeout=500ms, got %v", transport.ExpectContinueTimeout)
	}
	if !transport.ForceAttemptHTTP2 {
		t.Error("Expected ForceAttemptHTTP2=true")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("Expected InsecureSkipVerify=false")
	}
}
