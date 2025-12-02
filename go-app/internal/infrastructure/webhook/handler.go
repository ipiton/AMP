package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// AlertProcessor defines the interface for processing alerts.
type AlertProcessor interface {
	ProcessAlert(ctx context.Context, alert *core.Alert) error
	Health(ctx context.Context) error // Added for compatibility with handlers.AlertProcessor
}

// UniversalWebhookHandler handles webhook requests with auto-detection and validation.
//
// This handler provides:
//   - Auto-detection of webhook format (Alertmanager, Prometheus, Generic)
//   - Dynamic parser selection using Strategy pattern
//   - Parsing using appropriate parser based on detected type
//   - Validation of parsed webhook
//   - Conversion to domain models
//   - Metrics recording
//   - Error handling with detailed responses
//
// TN-146: Enhanced to support multiple parsers via map (Alertmanager + Prometheus).
type UniversalWebhookHandler struct {
	detector  WebhookDetector
	parsers   map[WebhookType]WebhookParser // Strategy pattern: dynamic parser selection
	validator WebhookValidator
	processor AlertProcessor
	metrics   *metrics.WebhookMetrics
	logger    *slog.Logger
}

// NewUniversalWebhookHandler creates a new universal webhook handler.
//
// The handler supports multiple webhook formats via Strategy pattern:
//   - Alertmanager webhooks (v0.25+ format)
//   - Prometheus direct alerts (v1 array and v2 grouped formats)
//
// Parser selection is automatic based on webhook detection.
//
// Parameters:
//   - processor: Alert processor for handling converted alerts
//   - logger: Structured logger (optional, defaults to slog.Default())
//
// Returns:
//   - *UniversalWebhookHandler: Initialized handler with all dependencies
//
// TN-146: Added Prometheus parser support via parsers map.
func NewUniversalWebhookHandler(processor AlertProcessor, logger *slog.Logger) *UniversalWebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &UniversalWebhookHandler{
		detector: NewWebhookDetector(),
		parsers: map[WebhookType]WebhookParser{
			WebhookTypeAlertmanager: NewAlertmanagerParser(),
			WebhookTypePrometheus:   NewPrometheusParser(), // TN-146: Prometheus support
		},
		validator: NewWebhookValidator(),
		processor: processor,
		metrics:   metrics.NewWebhookMetrics(),
		logger:    logger,
	}
}

// Health checks the health of the webhook handler.
func (h *UniversalWebhookHandler) Health(ctx context.Context) error {
	// Delegate to processor if available
	if h.processor != nil {
		return h.processor.Health(ctx)
	}
	return nil
}

// ProcessAlert implements handlers.AlertProcessor interface.
// This is an adapter method that delegates to the underlying processor.
func (h *UniversalWebhookHandler) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	if h.processor != nil {
		return h.processor.ProcessAlert(ctx, alert)
	}
	return fmt.Errorf("processor not initialized")
}

// HandleWebhookRequest represents the webhook processing request.
type HandleWebhookRequest struct {
	Payload     []byte
	ContentType string
	UserAgent   string
}

// HandleWebhookResponse represents the webhook processing response.
type HandleWebhookResponse struct {
	Status         string   `json:"status"`
	Message        string   `json:"message"`
	WebhookType    string   `json:"webhook_type"`
	AlertsReceived int      `json:"alerts_received"`
	AlertsProcessed int     `json:"alerts_processed"`
	Errors         []string `json:"errors,omitempty"`
	ProcessingTime string   `json:"processing_time"`
}

// HandleWebhook processes a webhook request with auto-detection and validation.
//
// Processing flow:
//   1. Detect webhook type (Alertmanager vs Generic)
//   2. Parse payload using appropriate parser
//   3. Validate parsed webhook
//   4. Convert to domain alerts
//   5. Process each alert
//   6. Record metrics
//   7. Return response
//
// Parameters:
//   - ctx: Request context for cancellation
//   - req: Webhook request with payload and headers
//
// Returns:
//   - *HandleWebhookResponse: Processing result with status and metrics
//   - error: Processing error (validation, parsing, or processing failure)
func (h *UniversalWebhookHandler) HandleWebhook(ctx context.Context, req *HandleWebhookRequest) (*HandleWebhookResponse, error) {
	startTime := time.Now()

	// Record payload size
	h.metrics.RecordPayloadSize("unknown", len(req.Payload))

	// Step 1: Detect webhook type
	webhookType, err := h.detector.Detect(req.Payload)
	if err != nil {
		h.logger.Error("Failed to detect webhook type",
			"error", err,
			"payload_size", len(req.Payload))
		h.metrics.RecordError("unknown", "detection_error")
		return nil, fmt.Errorf("webhook detection failed: %w", err)
	}

	h.logger.Info("Webhook detected",
		"type", webhookType,
		"payload_size", len(req.Payload))

	// Step 2: Select parser based on detected webhook type (TN-146: Strategy pattern)
	parser, ok := h.parsers[webhookType]
	if !ok {
		// Unknown type: fallback to Alertmanager parser (conservative approach)
		h.logger.Warn("Unknown webhook type, falling back to Alertmanager parser",
			"detected_type", webhookType,
			"fallback_to", WebhookTypeAlertmanager)
		parser = h.parsers[WebhookTypeAlertmanager]
		webhookType = WebhookTypeAlertmanager // Update type for metrics
	}

	// Parse webhook using selected parser
	parseStart := time.Now()
	webhook, err := parser.Parse(req.Payload)
	parseDuration := time.Since(parseStart).Seconds()
	h.metrics.RecordProcessingStage(string(webhookType), "parse", parseDuration)

	if err != nil {
		h.logger.Error("Failed to parse webhook",
			"error", err,
			"webhook_type", webhookType)
		h.metrics.RecordError(string(webhookType), "parse_error")
		h.metrics.RecordRequest(string(webhookType), "failure", time.Since(startTime).Seconds())
		return nil, fmt.Errorf("webhook parsing failed: %w", err)
	}

	// Step 3: Validate webhook
	validateStart := time.Now()
	validationResult := h.validator.ValidateAlertmanager(webhook)
	validateDuration := time.Since(validateStart).Seconds()
	h.metrics.RecordProcessingStage(string(webhookType), "validate", validateDuration)

	if !validationResult.Valid {
		h.logger.Warn("Webhook validation failed",
			"webhook_type", webhookType,
			"errors", validationResult.Errors)
		h.metrics.RecordError(string(webhookType), "validation_error")
		h.metrics.RecordRequest(string(webhookType), "failure", time.Since(startTime).Seconds())

		// Return detailed validation errors
		errorMessages := make([]string, len(validationResult.Errors))
		for i, ve := range validationResult.Errors {
			errorMessages[i] = fmt.Sprintf("%s: %s", ve.Field, ve.Message)
		}

		return &HandleWebhookResponse{
			Status:         "validation_failed",
			Message:        "Webhook validation failed",
			WebhookType:    string(webhookType),
			AlertsReceived: len(webhook.Alerts),
			Errors:         errorMessages,
			ProcessingTime: time.Since(startTime).String(),
		}, fmt.Errorf("validation failed: %d errors", len(validationResult.Errors))
	}

	// Step 4: Convert to domain alerts (using selected parser)
	convertStart := time.Now()
	alerts, err := parser.ConvertToDomain(webhook)
	convertDuration := time.Since(convertStart).Seconds()
	h.metrics.RecordProcessingStage(string(webhookType), "convert", convertDuration)

	if err != nil {
		h.logger.Error("Failed to convert webhook to domain alerts",
			"error", err,
			"webhook_type", webhookType)
		h.metrics.RecordError(string(webhookType), "conversion_error")
		h.metrics.RecordRequest(string(webhookType), "failure", time.Since(startTime).Seconds())
		return nil, fmt.Errorf("domain conversion failed: %w", err)
	}

	// Step 5: Process alerts
	processStart := time.Now()
	processedCount := 0
	var processingErrors []string

	for i, alert := range alerts {
		if err := h.processor.ProcessAlert(ctx, alert); err != nil {
			h.logger.Error("Failed to process alert",
				"error", err,
				"alert_index", i,
				"alert_name", alert.AlertName,
				"fingerprint", alert.Fingerprint)
			processingErrors = append(processingErrors, fmt.Sprintf("Alert %d (%s): %v", i, alert.AlertName, err))
			continue
		}
		processedCount++
	}

	processDuration := time.Since(processStart).Seconds()
	h.metrics.RecordProcessingStage(string(webhookType), "process", processDuration)

	// Step 6: Record overall metrics
	totalDuration := time.Since(startTime).Seconds()
	if len(processingErrors) > 0 {
		h.metrics.RecordRequest(string(webhookType), "partial_failure", totalDuration)
	} else {
		h.metrics.RecordRequest(string(webhookType), "success", totalDuration)
	}

	h.logger.Info("Webhook processed",
		"webhook_type", webhookType,
		"alerts_received", len(alerts),
		"alerts_processed", processedCount,
		"duration", totalDuration)

	// Step 7: Build response
	status := "success"
	message := "Webhook processed successfully"
	if len(processingErrors) > 0 {
		if processedCount == 0 {
			status = "failure"
			message = "All alerts failed to process"
		} else {
			status = "partial_success"
			message = fmt.Sprintf("Processed %d of %d alerts", processedCount, len(alerts))
		}
	}

	return &HandleWebhookResponse{
		Status:          status,
		Message:         message,
		WebhookType:     string(webhookType),
		AlertsReceived:  len(alerts),
		AlertsProcessed: processedCount,
		Errors:          processingErrors,
		ProcessingTime:  time.Since(startTime).String(),
	}, nil
}

// HandleWebhookSync is a convenience method for synchronous webhook processing.
// It wraps HandleWebhook and returns a JSON-serializable response.
func (h *UniversalWebhookHandler) HandleWebhookSync(ctx context.Context, payload []byte) ([]byte, int, error) {
	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	response, err := h.HandleWebhook(ctx, req)
	if err != nil {
		// Return error response with appropriate status code
		statusCode := 400 // Bad Request (validation/parsing errors)
		if response != nil && response.Status == "failure" {
			statusCode = 500 // Internal Server Error (processing errors)
		}

		errorResponse := &HandleWebhookResponse{
			Status:         "error",
			Message:        err.Error(),
			ProcessingTime: "0s",
		}
		if response != nil {
			errorResponse = response
		}

		jsonResponse, _ := json.Marshal(errorResponse)
		return jsonResponse, statusCode, err
	}

	// Success or partial success
	statusCode := 200
	if response.Status == "partial_success" {
		statusCode = 207 // Multi-Status
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		h.logger.Error("Failed to marshal response", "error", err)
		return []byte(`{"status":"error","message":"failed to serialize response"}`), 500, err
	}

	return jsonResponse, statusCode, nil
}

// GetMetrics returns the webhook metrics instance for external access.
func (h *UniversalWebhookHandler) GetMetrics() *metrics.WebhookMetrics {
	return h.metrics
}
