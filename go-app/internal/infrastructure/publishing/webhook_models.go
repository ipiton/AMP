package publishing

import (
	"net/http"
	"time"
)

// WebhookRequest represents an HTTP request to a webhook endpoint
type WebhookRequest struct {
	// URL is the webhook endpoint URL
	URL string

	// Payload is the JSON payload to send
	Payload map[string]interface{}

	// Headers are custom HTTP headers to include
	Headers map[string]string

	// Timeout is the request timeout
	Timeout time.Duration
}

// WebhookResponse represents an HTTP response from a webhook endpoint
type WebhookResponse struct {
	// StatusCode is the HTTP status code
	StatusCode int

	// Body is the response body
	Body []byte

	// Headers are the response headers
	Headers http.Header

	// Duration is the request duration
	Duration time.Duration
}

// WebhookConfig holds webhook publisher configuration
type WebhookConfig struct {
	// URL is the webhook endpoint URL
	URL string `json:"url"`

	// Headers are custom HTTP headers
	Headers map[string]string `json:"headers,omitempty"`

	// Timeout is the HTTP client timeout
	Timeout time.Duration `json:"timeout,omitempty"`

	// RetryConfig is the retry configuration
	RetryConfig *WebhookRetryConfig `json:"retry,omitempty"`

	// AuthConfig is the authentication configuration
	AuthConfig *AuthConfig `json:"auth,omitempty"`

	// MaxPayloadSize is the maximum payload size in bytes
	MaxPayloadSize int64 `json:"max_payload_size,omitempty"`
}

// WebhookRetryConfig defines retry behavior for webhook requests
type WebhookRetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default: 3, range: 0-5)
	MaxRetries int `json:"max_retries"`

	// BaseBackoff is the initial backoff duration (default: 100ms)
	BaseBackoff time.Duration `json:"base_backoff"`

	// MaxBackoff is the maximum backoff duration (default: 5s)
	MaxBackoff time.Duration `json:"max_backoff"`

	// Multiplier is the exponential backoff multiplier (default: 2.0)
	Multiplier float64 `json:"multiplier"`
}

// DefaultWebhookRetryConfig is the default retry configuration
var DefaultWebhookRetryConfig = WebhookRetryConfig{
	MaxRetries:  3,
	BaseBackoff: 100 * time.Millisecond,
	MaxBackoff:  5 * time.Second,
	Multiplier:  2.0,
}

// CalculateBackoff calculates exponential backoff duration for a given attempt
func (c *WebhookRetryConfig) CalculateBackoff(attempt int) time.Duration {
	if attempt < 0 {
		return c.BaseBackoff
	}

	// Calculate exponential backoff: BaseBackoff * (Multiplier ^ attempt)
	multiplier := c.Multiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}

	backoff := float64(c.BaseBackoff)
	for i := 0; i < attempt; i++ {
		backoff *= multiplier
	}

	duration := time.Duration(backoff)

	// Cap at MaxBackoff
	if duration > c.MaxBackoff {
		return c.MaxBackoff
	}

	return duration
}

// AuthType represents authentication type
type AuthType string

const (
	// AuthTypeBearer is Bearer token authentication
	AuthTypeBearer AuthType = "bearer"

	// AuthTypeBasic is HTTP Basic authentication
	AuthTypeBasic AuthType = "basic"

	// AuthTypeAPIKey is API Key header authentication
	AuthTypeAPIKey AuthType = "apikey"

	// AuthTypeCustom is custom header authentication
	AuthTypeCustom AuthType = "custom"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	// Type is the authentication type
	Type AuthType `json:"type"`

	// Token is the bearer token (for AuthTypeBearer)
	Token string `json:"token,omitempty"`

	// Username is the basic auth username (for AuthTypeBasic)
	Username string `json:"username,omitempty"`

	// Password is the basic auth password (for AuthTypeBasic)
	Password string `json:"password,omitempty"`

	// APIKey is the API key value (for AuthTypeAPIKey)
	APIKey string `json:"api_key,omitempty"`

	// APIKeyHeader is the custom API key header name (default: "X-API-Key")
	APIKeyHeader string `json:"api_key_header,omitempty"`

	// CustomHeaders are custom authentication headers (for AuthTypeCustom)
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
}

// ValidationConfig holds validation configuration
type ValidationConfig struct {
	// MaxPayloadSize is the maximum payload size in bytes (default: 1 MB)
	MaxPayloadSize int64 `json:"max_payload_size,omitempty"`

	// MaxHeaders is the maximum number of headers (default: 100)
	MaxHeaders int `json:"max_headers,omitempty"`

	// MaxHeaderSize is the maximum header value size in bytes (default: 4 KB)
	MaxHeaderSize int `json:"max_header_size,omitempty"`

	// AllowedSchemes are allowed URL schemes (default: ["https"])
	AllowedSchemes []string `json:"allowed_schemes,omitempty"`

	// BlockedHosts are blocked hostname patterns (default: ["localhost", "127.0.0.1"])
	BlockedHosts []string `json:"blocked_hosts,omitempty"`

	// MinTimeout is the minimum timeout (default: 1s)
	MinTimeout time.Duration `json:"min_timeout,omitempty"`

	// MaxTimeout is the maximum timeout (default: 60s)
	MaxTimeout time.Duration `json:"max_timeout,omitempty"`

	// MaxRetries is the maximum number of retries (default: 5)
	MaxRetries int `json:"max_retries,omitempty"`
}

// DefaultValidationConfig is the default validation configuration
var DefaultValidationConfig = ValidationConfig{
	MaxPayloadSize: 1 * 1024 * 1024, // 1 MB
	MaxHeaders:     100,
	MaxHeaderSize:  4 * 1024, // 4 KB
	AllowedSchemes: []string{"https"},
	BlockedHosts: []string{
		"localhost",
		"127.0.0.1",
		"0.0.0.0",
		"::1",
		"[::1]",
	},
	MinTimeout: 1 * time.Second,
	MaxTimeout: 60 * time.Second,
	MaxRetries: 5,
}
