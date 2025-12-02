package webhook

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// ValidationConfig contains configuration for enhanced input validation.
type ValidationConfig struct {
	// MaxLabelKeyLength is the maximum length for label keys
	MaxLabelKeyLength int

	// MaxLabelValueLength is the maximum length for label values
	MaxLabelValueLength int

	// MaxAnnotationKeyLength is the maximum length for annotation keys
	MaxAnnotationKeyLength int

	// MaxAnnotationValueLength is the maximum length for annotation values
	MaxAnnotationValueLength int

	// AllowedLabelKeyPattern is the regex pattern for valid label keys
	AllowedLabelKeyPattern string

	// AllowedStatuses is the list of allowed alert statuses
	AllowedStatuses []string

	// ValidateURLs enables URL validation in annotations
	ValidateURLs bool

	// BlockPrivateIPs blocks private IP addresses in URLs
	BlockPrivateIPs bool
}

// DefaultValidationConfig returns the default validation configuration.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		MaxLabelKeyLength:        255,
		MaxLabelValueLength:      1024,
		MaxAnnotationKeyLength:   255,
		MaxAnnotationValueLength: 4096,
		AllowedLabelKeyPattern:   `^[a-zA-Z_][a-zA-Z0-9_]*$`,
		AllowedStatuses:          []string{"firing", "resolved"},
		ValidateURLs:             true,
		BlockPrivateIPs:          true,
	}
}

var (
	// ErrInvalidLabelKey is returned when a label key is invalid
	ErrInvalidLabelKey = errors.New("invalid label key")

	// ErrInvalidLabelValue is returned when a label value is invalid
	ErrInvalidLabelValue = errors.New("invalid label value")

	// ErrInvalidAnnotationKey is returned when an annotation key is invalid
	ErrInvalidAnnotationKey = errors.New("invalid annotation key")

	// ErrInvalidAnnotationValue is returned when an annotation value is invalid
	ErrInvalidAnnotationValue = errors.New("invalid annotation value")

	// ErrInvalidStatus is returned when alert status is invalid
	ErrInvalidStatus = errors.New("invalid alert status")

	// ErrInvalidURL is returned when a URL is invalid or unsafe
	ErrInvalidURL = errors.New("invalid or unsafe URL")

	// privateIPRanges contains CIDR blocks for private IP ranges
	privateIPRanges = []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"fc00::/7",
		"fe80::/10",
		"::1/128",
	}
)

// EnhancedValidator provides enhanced input validation for webhooks.
type EnhancedValidator struct {
	config        ValidationConfig
	labelKeyRegex *regexp.Regexp
	privateNets   []*net.IPNet
}

// NewEnhancedValidator creates a new enhanced validator with the given configuration.
func NewEnhancedValidator(config ValidationConfig) (*EnhancedValidator, error) {
	// Compile label key regex
	labelKeyRegex, err := regexp.Compile(config.AllowedLabelKeyPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid label key pattern: %w", err)
	}

	// Parse private IP ranges
	var privateNets []*net.IPNet
	for _, cidr := range privateIPRanges {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid private IP range %s: %w", cidr, err)
		}
		privateNets = append(privateNets, ipNet)
	}

	return &EnhancedValidator{
		config:        config,
		labelKeyRegex: labelKeyRegex,
		privateNets:   privateNets,
	}, nil
}

// ValidateLabels validates alert labels according to the configured rules.
func (v *EnhancedValidator) ValidateLabels(labels map[string]string) error {
	for key, value := range labels {
		if err := v.ValidateLabelKey(key); err != nil {
			return err
		}
		if err := v.ValidateLabelValue(value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateLabelKey validates a single label key.
func (v *EnhancedValidator) ValidateLabelKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidLabelKey)
	}

	if len(key) > v.config.MaxLabelKeyLength {
		return fmt.Errorf("%w: key too long (%d > %d): %s",
			ErrInvalidLabelKey, len(key), v.config.MaxLabelKeyLength, key)
	}

	if !v.labelKeyRegex.MatchString(key) {
		return fmt.Errorf("%w: key does not match pattern %s: %s",
			ErrInvalidLabelKey, v.config.AllowedLabelKeyPattern, key)
	}

	return nil
}

// ValidateLabelValue validates a single label value.
func (v *EnhancedValidator) ValidateLabelValue(value string) error {
	if len(value) > v.config.MaxLabelValueLength {
		return fmt.Errorf("%w: value too long (%d > %d)",
			ErrInvalidLabelValue, len(value), v.config.MaxLabelValueLength)
	}

	// Check for control characters (except tab, newline, carriage return)
	for i, r := range value {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return fmt.Errorf("%w: contains control character at position %d",
				ErrInvalidLabelValue, i)
		}
	}

	return nil
}

// ValidateAnnotations validates alert annotations according to the configured rules.
func (v *EnhancedValidator) ValidateAnnotations(annotations map[string]string) error {
	for key, value := range annotations {
		if err := v.ValidateAnnotationKey(key); err != nil {
			return err
		}
		if err := v.ValidateAnnotationValue(value); err != nil {
			return err
		}

		// If the annotation looks like a URL, validate it
		if v.config.ValidateURLs && (key == "url" || key == "runbook_url" || key == "dashboard_url" || strings.HasSuffix(key, "_url")) {
			if err := v.ValidateURL(value); err != nil {
				return fmt.Errorf("annotation %s: %w", key, err)
			}
		}
	}
	return nil
}

// ValidateAnnotationKey validates a single annotation key.
func (v *EnhancedValidator) ValidateAnnotationKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidAnnotationKey)
	}

	if len(key) > v.config.MaxAnnotationKeyLength {
		return fmt.Errorf("%w: key too long (%d > %d): %s",
			ErrInvalidAnnotationKey, len(key), v.config.MaxAnnotationKeyLength, key)
	}

	return nil
}

// ValidateAnnotationValue validates a single annotation value.
func (v *EnhancedValidator) ValidateAnnotationValue(value string) error {
	if len(value) > v.config.MaxAnnotationValueLength {
		return fmt.Errorf("%w: value too long (%d > %d)",
			ErrInvalidAnnotationValue, len(value), v.config.MaxAnnotationValueLength)
	}

	return nil
}

// ValidateStatus validates an alert status.
func (v *EnhancedValidator) ValidateStatus(status string) error {
	if status == "" {
		return fmt.Errorf("%w: empty status", ErrInvalidStatus)
	}

	for _, allowed := range v.config.AllowedStatuses {
		if status == allowed {
			return nil
		}
	}

	return fmt.Errorf("%w: %s (allowed: %v)", ErrInvalidStatus, status, v.config.AllowedStatuses)
}

// ValidateURL validates a URL for safety (SSRF protection).
func (v *EnhancedValidator) ValidateURL(urlStr string) error {
	if urlStr == "" {
		return nil // Empty URLs are allowed
	}

	// Parse URL
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// Only allow HTTP and HTTPS schemes
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %s not allowed (only http/https)", ErrInvalidURL, u.Scheme)
	}

	// Block localhost variants
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: localhost not allowed", ErrInvalidURL)
	}

	// Block private IPs if configured
	if v.config.BlockPrivateIPs {
		ip := net.ParseIP(host)
		if ip != nil {
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
				return fmt.Errorf("%w: private IP address not allowed", ErrInvalidURL)
			}

			// Check against private IP ranges
			for _, ipNet := range v.privateNets {
				if ipNet.Contains(ip) {
					return fmt.Errorf("%w: IP in private range %s", ErrInvalidURL, ipNet.String())
				}
			}
		}
	}

	// Block IPv6 loopback
	if host == "::1" || host == "0:0:0:0:0:0:0:1" {
		return fmt.Errorf("%w: loopback address not allowed", ErrInvalidURL)
	}

	return nil
}

// ValidateAlert performs comprehensive validation on an alert.
func (v *EnhancedValidator) ValidateAlert(alert *AlertmanagerAlert) error {
	// Validate status
	if err := v.ValidateStatus(alert.Status); err != nil {
		return err
	}

	// Validate labels
	if err := v.ValidateLabels(alert.Labels); err != nil {
		return fmt.Errorf("labels: %w", err)
	}

	// Validate annotations
	if err := v.ValidateAnnotations(alert.Annotations); err != nil {
		return fmt.Errorf("annotations: %w", err)
	}

	return nil
}

// ValidateWebhook performs comprehensive validation on a webhook payload.
func (v *EnhancedValidator) ValidateWebhook(webhook *AlertmanagerWebhook) error {
	if len(webhook.Alerts) == 0 {
		return errors.New("webhook must contain at least one alert")
	}

	for i, alert := range webhook.Alerts {
		if err := v.ValidateAlert(&alert); err != nil {
			return fmt.Errorf("alert[%d]: %w", i, err)
		}
	}

	return nil
}
