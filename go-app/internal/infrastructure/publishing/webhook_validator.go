package publishing

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// WebhookValidator validates webhook configuration and payloads
type WebhookValidator struct {
	config ValidationConfig
	logger *slog.Logger
}

// NewWebhookValidator creates a new webhook validator with default configuration
func NewWebhookValidator(logger *slog.Logger) *WebhookValidator {
	return &WebhookValidator{
		config: DefaultValidationConfig,
		logger: logger,
	}
}

// NewWebhookValidatorWithConfig creates a new webhook validator with custom configuration
func NewWebhookValidatorWithConfig(config ValidationConfig, logger *slog.Logger) *WebhookValidator {
	return &WebhookValidator{
		config: config,
		logger: logger,
	}
}

// ValidateTarget validates entire publishing target configuration
func (v *WebhookValidator) ValidateTarget(target *core.PublishingTarget) error {
	// Validate URL
	if err := v.ValidateURL(target.URL); err != nil {
		return err
	}

	// Validate headers
	if err := v.ValidateHeaders(target.Headers); err != nil {
		return err
	}

	// Note: Individual per-target timeout configuration can be added in future if needed
	// Currently using default client timeout (10s)

	v.logger.Info("Target validation passed",
		slog.String("target", target.Name),
		slog.String("url", maskURL(target.URL)))

	return nil
}

// ValidateURL validates webhook URL according to security rules
func (v *WebhookValidator) ValidateURL(urlStr string) error {
	if urlStr == "" {
		return ErrEmptyURL
	}

	// Parse URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// Check scheme (HTTPS only)
	if !contains(v.config.AllowedSchemes, parsedURL.Scheme) {
		return fmt.Errorf("%w: scheme %s not allowed (allowed: %v)",
			ErrInsecureScheme, parsedURL.Scheme, v.config.AllowedSchemes)
	}

	// Check for credentials in URL (security risk)
	if parsedURL.User != nil {
		return ErrCredentialsInURL
	}

	// Check for blocked hosts (localhost, 127.0.0.1, ::1)
	hostname := parsedURL.Hostname()
	if contains(v.config.BlockedHosts, hostname) {
		return fmt.Errorf("%w: %s", ErrBlockedHost, hostname)
	}

	// Check for private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() {
			return fmt.Errorf("%w: private/loopback IP %s", ErrBlockedHost, ip)
		}
	}

	v.logger.Debug("URL validation passed", slog.String("url", maskURL(urlStr)))
	return nil
}

// ValidatePayloadSize validates payload size
func (v *WebhookValidator) ValidatePayloadSize(payload []byte) error {
	size := int64(len(payload))
	if size > v.config.MaxPayloadSize {
		return fmt.Errorf("%w: %d bytes exceeds limit of %d bytes",
			ErrPayloadTooLarge, size, v.config.MaxPayloadSize)
	}
	return nil
}

// ValidateHeaders validates HTTP headers
func (v *WebhookValidator) ValidateHeaders(headers map[string]string) error {
	// Check header count
	if len(headers) > v.config.MaxHeaders {
		return fmt.Errorf("%w: %d headers exceeds limit of %d",
			ErrTooManyHeaders, len(headers), v.config.MaxHeaders)
	}

	// Check header value sizes
	for key, value := range headers {
		if len(value) > v.config.MaxHeaderSize {
			return fmt.Errorf("%w: header %s value size %d exceeds limit of %d",
				ErrHeaderValueTooLarge, key, len(value), v.config.MaxHeaderSize)
		}
	}

	return nil
}

// ValidateTimeout validates timeout configuration
func (v *WebhookValidator) ValidateTimeout(timeout time.Duration) error {
	if timeout < v.config.MinTimeout {
		return fmt.Errorf("%w: %s is less than minimum %s",
			ErrInvalidTimeout, timeout, v.config.MinTimeout)
	}
	if timeout > v.config.MaxTimeout {
		return fmt.Errorf("%w: %s exceeds maximum %s",
			ErrInvalidTimeout, timeout, v.config.MaxTimeout)
	}
	return nil
}

// ValidateRetryConfig validates retry configuration
func (v *WebhookValidator) ValidateRetryConfig(config WebhookRetryConfig) error {
	if config.MaxRetries < 0 || config.MaxRetries > v.config.MaxRetries {
		return fmt.Errorf("%w: max retries %d out of range [0, %d]",
			ErrInvalidRetryConfig, config.MaxRetries, v.config.MaxRetries)
	}

	if config.BaseBackoff < 0 || config.BaseBackoff > 10*time.Second {
		return fmt.Errorf("%w: base backoff %s out of range [0, 10s]",
			ErrInvalidRetryConfig, config.BaseBackoff)
	}

	if config.MaxBackoff < config.BaseBackoff {
		return fmt.Errorf("%w: max backoff %s less than base backoff %s",
			ErrInvalidRetryConfig, config.MaxBackoff, config.BaseBackoff)
	}

	return nil
}

// ValidateFormat validates payload format (JSON serializable)
func (v *WebhookValidator) ValidateFormat(payload interface{}) error {
	// Try to marshal to JSON
	_, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}
	return nil
}

// ==================== HELPER FUNCTIONS ====================

// contains checks if slice contains string
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
