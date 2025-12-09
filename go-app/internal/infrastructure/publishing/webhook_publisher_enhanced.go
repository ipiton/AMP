package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// EnhancedWebhookPublisher implements AlertPublisher with advanced features
type EnhancedWebhookPublisher struct {
	*BaseEnhancedPublisher                 // Embedded base publisher for common functionality
	client                 *WebhookHTTPClient // Generic HTTP client for webhooks
	validator              *WebhookValidator  // Webhook-specific validator
}

// NewEnhancedWebhookPublisher creates a new enhanced webhook publisher
func NewEnhancedWebhookPublisher(
	client *WebhookHTTPClient,
	validator *WebhookValidator,
	formatter AlertFormatter,
	metrics *v2.PublishingMetrics,
	logger *slog.Logger,
) *EnhancedWebhookPublisher {
	return &EnhancedWebhookPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "webhook_publisher"),
		),
		client:    client,
		validator: validator,
	}
}

// Publish publishes enriched alert to webhook endpoint
func (p *EnhancedWebhookPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	startTime := time.Now()

	p.GetLogger().InfoContext(ctx, "Publishing alert to webhook",
		slog.String("target", target.Name),
		slog.String("url", maskURL(target.URL)),
		slog.String("fingerprint", enrichedAlert.Alert.Fingerprint))

	// Validate target configuration
	if err := p.validator.ValidateTarget(target); err != nil {
		p.GetLogger().ErrorContext(ctx, "Target validation failed",
			slog.String("target", target.Name),
			slog.String("error", err.Error()))
		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", "validation_error")
		}
		return fmt.Errorf("target validation failed: %w", err)
	}

	// Format alert for webhook (generic JSON format)
	payload, err := p.formatter.FormatAlert(ctx, enrichedAlert, core.FormatWebhook)
	if err != nil {
		p.GetLogger().ErrorContext(ctx, "Failed to format alert",
			slog.String("target", target.Name),
			slog.String("error", err.Error()))
		return fmt.Errorf("failed to format alert: %w", err)
	}

	// Validate payload format
	if err := p.validator.ValidateFormat(payload); err != nil {
		p.GetLogger().ErrorContext(ctx, "Payload format validation failed",
			slog.String("target", target.Name),
			slog.String("error", err.Error()))
		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", "format_validation_error")
		}
		return fmt.Errorf("payload format validation failed: %w", err)
	}

	// Marshal payload to JSON for size validation
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Validate payload size
	if err := p.validator.ValidatePayloadSize(payloadBytes); err != nil {
		p.GetLogger().ErrorContext(ctx, "Payload size validation failed",
			slog.String("target", target.Name),
			slog.Int("size", len(payloadBytes)),
			slog.String("error", err.Error()))
		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", "payload_size_error")
		}
		return fmt.Errorf("payload size validation failed: %w", err)
	}

	// Record payload size metric
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordPayloadSize(v2.ProviderWebhook, len(payloadBytes))
	}

	// Parse authentication config from target headers
	authConfig := p.extractAuthConfig(target)

	// Note: Timeout is currently set at client level during initialization
	// Individual per-target timeout configuration can be added in future if needed

	// Execute HTTP POST with retry logic
	resp, err := p.client.Post(ctx, target.URL, payload, target.Headers, authConfig)
	duration := time.Since(startTime)

	if err != nil {
		// Record error metrics using unified classification
		errorType := GetPublishingErrorType(err)
		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", errorType)
			p.metrics.RecordAPIDuration(v2.ProviderWebhook, "publish", "POST", duration)

			// Record specific error types
			if IsPublishingAuthError(err) {
				p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", "auth_error")
			} else if IsPublishingTimeout(err) {
				p.metrics.RecordAPIError(v2.ProviderWebhook, "publish", "timeout")
			}
		}

		p.GetLogger().ErrorContext(ctx, "Failed to publish alert",
			slog.String("target", target.Name),
			slog.String("url", maskURL(target.URL)),
			slog.Duration("duration", duration),
			slog.String("error", err.Error()))
		return fmt.Errorf("failed to publish alert: %w", err)
	}

	// Record success metrics
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordMessage(v2.ProviderWebhook, "success")
		p.metrics.RecordAPIDuration(v2.ProviderWebhook, "publish", "POST", duration)
	}

	p.GetLogger().InfoContext(ctx, "Alert published successfully",
		slog.String("target", target.Name),
		slog.String("url", maskURL(target.URL)),
		slog.Int("status_code", resp.StatusCode),
		slog.Duration("duration", duration),
		slog.Int("payload_size", len(payloadBytes)),
		slog.String("fingerprint", enrichedAlert.Alert.Fingerprint))

	return nil
}

// Name returns publisher name
func (p *EnhancedWebhookPublisher) Name() string {
	return "EnhancedWebhook"
}

// extractAuthConfig extracts authentication configuration from target headers
func (p *EnhancedWebhookPublisher) extractAuthConfig(target *core.PublishingTarget) *AuthConfig {
	// Check for Authorization header (Bearer or Basic)
	if authHeader, exists := target.Headers["Authorization"]; exists {
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			// Bearer Token authentication
			return &AuthConfig{
				Type:  AuthTypeBearer,
				Token: authHeader[7:],
			}
		}
		// Could be Basic Auth, but we'll let the header pass through
		// and not override it here
	}

	// Check for API Key header
	if apiKey, exists := target.Headers["X-API-Key"]; exists {
		return &AuthConfig{
			Type:         AuthTypeAPIKey,
			APIKey:       apiKey,
			APIKeyHeader: "X-API-Key",
		}
	}

	// Check for custom API key header patterns
	for key, value := range target.Headers {
		if key == "X-Api-Key" || key == "X-ApiKey" || key == "Api-Key" {
			return &AuthConfig{
				Type:         AuthTypeAPIKey,
				APIKey:       value,
				APIKeyHeader: key,
			}
		}
	}

	// If no specific auth detected, use custom headers
	if len(target.Headers) > 0 {
		// Filter out standard headers
		customHeaders := make(map[string]string)
		for key, value := range target.Headers {
			if !isStandardHeader(key) {
				customHeaders[key] = value
			}
		}

		if len(customHeaders) > 0 {
			return &AuthConfig{
				Type:          AuthTypeCustom,
				CustomHeaders: customHeaders,
			}
		}
	}

	// No authentication configured
	return nil
}

// isStandardHeader checks if header is a standard HTTP header
func isStandardHeader(key string) bool {
	standardHeaders := map[string]bool{
		"Content-Type":    true,
		"User-Agent":      true,
		"Accept":          true,
		"Accept-Encoding": true,
		"Connection":      true,
		"Host":            true,
		"Content-Length":  true,
	}
	return standardHeaders[key]
}

// ==================== FACTORY METHODS ====================

// NewEnhancedWebhookPublisherWithDefaults creates publisher with default configuration
func NewEnhancedWebhookPublisherWithDefaults(formatter AlertFormatter, metrics *v2.PublishingMetrics, logger *slog.Logger) *EnhancedWebhookPublisher {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, logger)
	validator := NewWebhookValidator(logger)

	return NewEnhancedWebhookPublisher(client, validator, formatter, metrics, logger)
}

// NewEnhancedWebhookPublisherWithRetry creates publisher with custom retry configuration
func NewEnhancedWebhookPublisherWithRetry(
	retryConfig WebhookRetryConfig,
	formatter AlertFormatter,
	metrics *v2.PublishingMetrics,
	logger *slog.Logger,
) *EnhancedWebhookPublisher {
	client := NewWebhookHTTPClient(retryConfig, logger)
	validator := NewWebhookValidator(logger)

	return NewEnhancedWebhookPublisher(client, validator, formatter, metrics, logger)
}

// NewEnhancedWebhookPublisherWithValidation creates publisher with custom validation configuration
func NewEnhancedWebhookPublisherWithValidation(
	validationConfig ValidationConfig,
	formatter AlertFormatter,
	metrics *v2.PublishingMetrics,
	logger *slog.Logger,
) *EnhancedWebhookPublisher {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, logger)
	validator := NewWebhookValidatorWithConfig(validationConfig, logger)

	return NewEnhancedWebhookPublisher(client, validator, formatter, metrics, logger)
}
