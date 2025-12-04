// Package handlers provides HTTP handlers for the Alert History Service.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/webhook"
)

// PrometheusAlertsHandler handles POST /api/v2/alerts endpoint.
//
// This handler provides Alertmanager-compatible API for receiving alerts
// directly from Prometheus servers. It supports both Prometheus v1 and v2
// alert formats and processes them through the full AlertProcessor pipeline.
//
// Architecture:
//   HTTP Request → Parse (TN-146) → Validate → Process → Response
//
// Supported Formats:
//   - Prometheus v1: Array of alerts [{"labels":..., "state":"firing", ...}]
//   - Prometheus v2: Grouped alerts {"groups":[{"labels":..., "alerts":[...]}]}
//
// Processing Pipeline:
//   1. Parse via TN-146 PrometheusParser (format auto-detection)
//   2. Validate structure via TN-043 WebhookValidator
//   3. Convert to []core.Alert domain models
//   4. Process each alert via AlertProcessor:
//      - Deduplication (TN-036)
//      - Inhibition (TN-130)
//      - Enrichment (TN-033/034, optional)
//      - Filtering (TN-035, optional)
//      - Storage (TN-032)
//      - Publishing (TN-051-060, optional)
//   5. Build response (200/207/400/500)
//   6. Record metrics and log results
//
// HTTP Status Codes:
//   - 200 OK: All alerts processed successfully
//   - 207 Multi-Status: Partial success (some alerts failed)
//   - 400 Bad Request: Validation failed, malformed JSON
//   - 405 Method Not Allowed: Non-POST request
//   - 413 Payload Too Large: Request > max size
//   - 422 Unprocessable Entity: Valid JSON but invalid data
//   - 500 Internal Server Error: Critical system failure
//
// Performance Targets (150% quality):
//   - p95 latency: < 5ms
//   - Throughput: 2,000+ req/s
//   - Memory: < 5 KB per request
//   - Zero allocations in hot path
//
// Compatibility:
//   - 100% Alertmanager API v2 compatible
//   - Works with Prometheus 2.x+
//   - Drop-in replacement for Alertmanager
//
// Example Usage:
//   parser := webhook.NewPrometheusParser()
//   handler, err := NewPrometheusAlertsHandler(parser, alertProcessor, logger, nil)
//   mux.HandleFunc("POST /api/v2/alerts", handler.HandlePrometheusAlerts)
type PrometheusAlertsHandler struct {
	parser    webhook.WebhookParser      // TN-146: Prometheus parser
	processor AlertProcessor              // TN-061: Alert processing pipeline
	metrics   *PrometheusAlertsMetrics   // TN-147: Endpoint metrics
	logger    *slog.Logger                // Structured logging
	config    *PrometheusAlertsConfig    // Handler configuration
}

// PrometheusAlertsConfig holds configuration for the handler.
//
// All fields are optional - defaults are used if not specified.
// Configuration can be overridden from environment variables or config files.
//
// Example:
//   config := &PrometheusAlertsConfig{
//       MaxRequestSize: 5 * 1024 * 1024,  // 5 MB
//       RequestTimeout: 15 * time.Second,
//       MaxAlertsPerReq: 500,
//   }
type PrometheusAlertsConfig struct {
	MaxRequestSize  int64         // Max request body size in bytes (default: 10 MB)
	RequestTimeout  time.Duration // Max request processing time (default: 30s)
	MaxAlertsPerReq int           // Max alerts per request (default: 1000)
	EnableMetrics   bool          // Enable Prometheus metrics (default: true)
	ReturnPartial   bool          // Return 207 on partial success (default: true)
}

// DefaultPrometheusAlertsConfig returns default configuration.
//
// Defaults:
//   - MaxRequestSize: 10 MB
//   - RequestTimeout: 30 seconds
//   - MaxAlertsPerReq: 1000
//   - EnableMetrics: true
//   - ReturnPartial: true
//
// Returns:
//   - *PrometheusAlertsConfig: Configuration with default values
func DefaultPrometheusAlertsConfig() *PrometheusAlertsConfig {
	return &PrometheusAlertsConfig{
		MaxRequestSize:  10 * 1024 * 1024, // 10 MB
		RequestTimeout:  30 * time.Second,
		MaxAlertsPerReq: 1000,
		EnableMetrics:   true,
		ReturnPartial:   true,
	}
}

// PrometheusAlertsResponse represents successful response.
//
// This format is compatible with Alertmanager API v2 expectations.
// Prometheus does not strictly validate the response, but this format
// ensures compatibility with monitoring tools and dashboards.
//
// Example (all success):
//   {
//     "status": "success",
//     "data": {
//       "received": 5,
//       "processed": 5,
//       "stored": 5,
//       "timestamp": "2025-11-18T10:01:30Z"
//     }
//   }
type PrometheusAlertsResponse struct {
	Status string                     `json:"status"` // "success" or "partial"
	Data   PrometheusAlertsResultData `json:"data"`   // Processing results
}

// PrometheusAlertsResultData contains processing results.
//
// Fields:
//   - Received: Total number of alerts in request
//   - Processed: Number successfully processed and stored
//   - Stored: Number stored in database (usually == processed)
//   - Failed: Number that failed processing (only in partial success)
//   - Errors: Details of failed alerts (only in partial success)
//   - Timestamp: Response timestamp in RFC3339 format
type PrometheusAlertsResultData struct {
	Received  int            `json:"received"`            // Total alerts received
	Processed int            `json:"processed"`           // Successfully processed
	Stored    int            `json:"stored,omitempty"`    // Stored in database
	Failed    int            `json:"failed,omitempty"`    // Failed to process
	Errors    []AlertFailure `json:"errors,omitempty"`    // Error details (207 only)
	Timestamp string         `json:"timestamp"`           // Response timestamp (RFC3339)
}

// AlertFailure represents a failed alert in partial success response.
//
// Includes context to help debug why specific alerts failed.
// This information is only included in 207 Multi-Status responses.
//
// Example:
//   {
//     "index": 1,
//     "fingerprint": "abc123",
//     "alertname": "HighCPU",
//     "error": "storage connection timeout"
//   }
type AlertFailure struct {
	Index       int    `json:"index"`                 // Alert index in request
	Fingerprint string `json:"fingerprint,omitempty"` // Alert fingerprint
	AlertName   string `json:"alertname,omitempty"`   // Alert name
	Error       string `json:"error"`                 // Error message
}

// PrometheusAlertsErrorResponse represents error response.
//
// This format is used for all error responses (400, 405, 413, 422, 500).
// Provides detailed error information to help diagnose issues.
//
// Example (validation error):
//   {
//     "status": "error",
//     "error": "validation failed",
//     "errors": [
//       {
//         "field": "alerts[0].labels.alertname",
//         "message": "required field missing",
//         "value": null
//       }
//     ]
//   }
type PrometheusAlertsErrorResponse struct {
	Status string            `json:"status"` // "error"
	Error  string            `json:"error"`  // High-level error message
	Errors []ValidationError `json:"errors,omitempty"` // Detailed validation errors (400 only)
}

// ValidationError represents a single validation error.
//
// Provides detailed context about what field failed validation and why.
// Only included in 400 Bad Request responses.
type ValidationError struct {
	Field   string      `json:"field"`             // Field path (e.g., "alerts[0].labels.alertname")
	Message string      `json:"message"`           // Error message
	Value   interface{} `json:"value,omitempty"`   // Invalid value (if available)
}

// NewPrometheusAlertsHandler creates a new Prometheus alerts handler.
//
// This handler requires:
//   - parser: TN-146 PrometheusParser for parsing v1/v2 formats
//   - processor: AlertProcessor for processing pipeline
//   - logger: Structured logger (uses slog.Default() if nil)
//
// Optional:
//   - config: Handler configuration (uses defaults if nil)
//
// Returns:
//   - *PrometheusAlertsHandler: Initialized handler ready for use
//   - error: Configuration error (parser/processor nil)
//
// Example:
//   parser := webhook.NewPrometheusParser()
//   handler, err := NewPrometheusAlertsHandler(parser, alertProcessor, logger, nil)
//   if err != nil {
//       log.Fatal(err)
//   }
//   mux.HandleFunc("POST /api/v2/alerts", handler.HandlePrometheusAlerts)
func NewPrometheusAlertsHandler(
	parser webhook.WebhookParser,
	processor AlertProcessor,
	logger *slog.Logger,
	config *PrometheusAlertsConfig,
) (*PrometheusAlertsHandler, error) {
	// Validate dependencies
	if parser == nil {
		return nil, fmt.Errorf("parser is required")
	}
	if processor == nil {
		return nil, fmt.Errorf("processor is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config == nil {
		config = DefaultPrometheusAlertsConfig()
	}

	// Initialize metrics (if enabled)
	var metricsCollector *PrometheusAlertsMetrics
	if config.EnableMetrics {
		metricsCollector = NewPrometheusAlertsMetrics()
	}

	return &PrometheusAlertsHandler{
		parser:    parser,
		processor: processor,
		metrics:   metricsCollector,
		logger:    logger,
		config:    config,
	}, nil
}

// HandlePrometheusAlerts handles POST /api/v2/alerts requests.
//
// Request flow:
//   1. Validate HTTP method (POST only)
//   2. Read and validate request body size
//   3. Parse JSON (Prometheus v1/v2 format via TN-146)
//   4. Validate alert structure (via TN-043)
//   5. Convert to domain models (via TN-146)
//   6. Check alert count limit
//   7. Process each alert (via AlertProcessor)
//   8. Build response (200/207/400/500)
//   9. Record metrics and log results
//
// HTTP Status Codes:
//   - 200: All alerts processed successfully
//   - 207: Partial success (some alerts failed)
//   - 400: Validation failed (bad request)
//   - 405: Method not allowed (non-POST)
//   - 413: Payload too large
//   - 422: Unprocessable entity (valid JSON, invalid data)
//   - 500: Internal server error
//
// Performance:
//   - Target: < 5ms p95 latency (150% quality)
//   - Measured: See benchmarks in prometheus_alerts_bench_test.go
//
// Thread Safety:
//   - Handler is safe for concurrent use
//   - Each request is processed independently
//   - No shared mutable state between requests
func (h *PrometheusAlertsHandler) HandlePrometheusAlerts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()

	// Track concurrent requests
	if h.metrics != nil {
		h.metrics.IncrementConcurrent()
		defer h.metrics.DecrementConcurrent()
	}

	// Add request timeout
	if h.config.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.config.RequestTimeout)
		defer cancel()
	}

	// Log incoming request
	h.logger.Info("Prometheus alerts request received",
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"content_length", r.ContentLength,
		"user_agent", r.Header.Get("User-Agent"),
	)

	// Step 1: Validate HTTP method
	if r.Method != http.MethodPost {
		h.logger.Warn("Invalid HTTP method for Prometheus alerts", "method", r.Method)
		h.respondError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		h.recordMetrics("validation_failed", "method_not_allowed", 0, time.Since(startTime))
		return
	}

	// Step 2: Read request body with size limit
	body, err := h.readRequestBody(r)
	if err != nil {
		h.logger.Error("Failed to read request body", "error", err)

		// Determine appropriate status code
		statusCode := http.StatusBadRequest
		if err.Error() == "request body too large" {
			statusCode = http.StatusRequestEntityTooLarge
		}

		h.respondError(w, statusCode, "failed to read request body", err)
		h.recordMetrics("validation_failed", "read_body_error", 0, time.Since(startTime))

		// Record payload size metric (if available)
		if h.metrics != nil && r.ContentLength > 0 {
			h.metrics.RecordPayloadSize(int(r.ContentLength))
		}
		return
	}

	// Record payload size
	if h.metrics != nil {
		h.metrics.RecordPayloadSize(len(body))
	}

	// Step 3: Parse Prometheus alerts (v1 or v2 format)
	webhook, err := h.parser.Parse(body)
	if err != nil {
		h.logger.Error("Failed to parse Prometheus webhook", "error", err, "payload_size", len(body))
		h.respondError(w, http.StatusBadRequest, "failed to parse webhook", err)
		h.recordMetrics("validation_failed", "parse_error", 0, time.Since(startTime))
		return
	}

	// Step 4: Validate webhook structure
	validationResult := h.parser.Validate(webhook)
	if !validationResult.Valid {
		h.logger.Warn("Webhook validation failed",
			"errors", validationResult.Errors,
			"error_count", len(validationResult.Errors),
		)
		h.respondValidationError(w, validationResult)
		h.recordMetrics("validation_failed", "validation_error", 0, time.Since(startTime))
		return
	}

	// Step 5: Convert to domain models
	alerts, err := h.parser.ConvertToDomain(webhook)
	if err != nil {
		h.logger.Error("Failed to convert webhook to domain", "error", err)
		h.respondError(w, http.StatusUnprocessableEntity, "failed to convert alerts", err)
		h.recordMetrics("validation_failed", "conversion_error", 0, time.Since(startTime))
		return
	}

	receivedCount := len(alerts)
	h.logger.Info("Alerts parsed successfully",
		"received_count", receivedCount,
		"format", webhook.Version, // "prom_v1" or "prom_v2"
	)

	// Record format-specific metrics
	if h.metrics != nil {
		format := "v1" // default
		if webhook.Version == "prom_v2" {
			format = "v2"
		}
		h.metrics.RecordAlerts(format, receivedCount, 0, 0) // Will update counts after processing
	}

	// Step 6: Check alert count limit
	if receivedCount > h.config.MaxAlertsPerReq {
		h.logger.Warn("Too many alerts in request",
			"received", receivedCount,
			"max_allowed", h.config.MaxAlertsPerReq,
		)
		h.respondError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("too many alerts (received: %d, max: %d)", receivedCount, h.config.MaxAlertsPerReq), nil)
		h.recordMetrics("validation_failed", "too_many_alerts", receivedCount, time.Since(startTime))
		return
	}

	// Step 7: Process alerts through AlertProcessor pipeline
	processedCount, failedAlerts := h.processAlerts(ctx, alerts)

	duration := time.Since(startTime)

	// Update final metrics
	if h.metrics != nil {
		format := "v1"
		if webhook.Version == "prom_v2" {
			format = "v2"
		}
		h.metrics.RecordAlerts(format, 0, processedCount, len(failedAlerts)) // Update processed/failed counts
	}

	// Step 8: Build and send response
	if len(failedAlerts) == 0 {
		// All alerts processed successfully → 200 OK
		h.logger.Info("All alerts processed successfully",
			"received", receivedCount,
			"processed", processedCount,
			"duration_ms", duration.Milliseconds(),
		)
		h.respondSuccess(w, receivedCount, processedCount, duration)
		h.recordMetrics("success", "all_processed", receivedCount, duration)
	} else if processedCount > 0 && h.config.ReturnPartial {
		// Partial success → 207 Multi-Status
		h.logger.Warn("Partial success - some alerts failed",
			"received", receivedCount,
			"processed", processedCount,
			"failed", len(failedAlerts),
			"duration_ms", duration.Milliseconds(),
		)
		h.respondPartialSuccess(w, receivedCount, processedCount, failedAlerts, duration)
		h.recordMetrics("partial", "some_failed", receivedCount, duration)
	} else {
		// All alerts failed → 500 Internal Server Error
		h.logger.Error("All alerts failed to process",
			"received", receivedCount,
			"failed", len(failedAlerts),
			"duration_ms", duration.Milliseconds(),
		)
		h.respondError(w, http.StatusInternalServerError, "all alerts failed to process", nil)
		h.recordMetrics("error", "all_failed", receivedCount, duration)
	}

	h.logger.Info("Request processing complete",
		"received", receivedCount,
		"processed", processedCount,
		"failed", len(failedAlerts),
		"duration_ms", duration.Milliseconds(),
		"success_rate_pct", (float64(processedCount)/float64(receivedCount))*100,
	)
}

// readRequestBody reads and validates request body size.
//
// Performs two levels of validation:
//   1. Check Content-Length header (fast, early rejection)
//   2. Read with io.LimitReader (defense in depth, actual size validation)
//
// Returns:
//   - []byte: Request body contents
//   - error: Size limit exceeded, empty body, or read error
func (h *PrometheusAlertsHandler) readRequestBody(r *http.Request) ([]byte, error) {
	// Check Content-Length header (fast rejection)
	if r.ContentLength > h.config.MaxRequestSize {
		return nil, fmt.Errorf("request body too large: %d bytes (max: %d)",
			r.ContentLength, h.config.MaxRequestSize)
	}

	// Read body with size limit (defense in depth)
	// Add +1 to detect if body exceeds limit
	limitedReader := io.LimitReader(r.Body, h.config.MaxRequestSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	// Check actual size
	if int64(len(body)) > h.config.MaxRequestSize {
		return nil, fmt.Errorf("request body too large: %d bytes (max: %d)",
			len(body), h.config.MaxRequestSize)
	}

	// Check for empty body
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}

	return body, nil
}

// processAlerts processes alerts through AlertProcessor pipeline.
//
// Processing strategy:
//   - Best-effort: Continue processing even if some alerts fail
//   - Sequential: Process alerts in order (preserves temporal ordering)
//   - Collect failures: Track which alerts failed and why
//
// This approach ensures:
//   - Maximum alert throughput (don't fail entire batch on single error)
//   - Temporal ordering preserved (important for alert correlation)
//   - Detailed error reporting (know exactly which alerts failed)
//
// Returns:
//   - processedCount: Number of successfully processed alerts
//   - failedAlerts: List of alerts that failed processing (with errors)
func (h *PrometheusAlertsHandler) processAlerts(
	ctx context.Context,
	alerts []*core.Alert,
) (int, []AlertFailure) {
	processedCount := 0
	failedAlerts := make([]AlertFailure, 0, len(alerts)/10) // Pre-allocate assuming 10% failure rate

	for i, alert := range alerts {
		// Check context cancellation (timeout or client disconnect)
		select {
		case <-ctx.Done():
			h.logger.Warn("Context cancelled, stopping alert processing",
				"processed", processedCount,
				"remaining", len(alerts)-i,
				"error", ctx.Err(),
			)
			// Add remaining alerts to failed list
			for j := i; j < len(alerts); j++ {
				failedAlerts = append(failedAlerts, AlertFailure{
					Index:       j,
					Fingerprint: alerts[j].Fingerprint,
					AlertName:   alerts[j].AlertName,
					Error:       "context cancelled: " + ctx.Err().Error(),
				})
			}
			return processedCount, failedAlerts
		default:
			// Continue processing
		}

		// Process alert through pipeline
		err := h.processor.ProcessAlert(ctx, alert)
		if err != nil {
			// Log error but continue processing (best-effort)
			h.logger.Warn("Alert processing failed",
				"index", i,
				"fingerprint", alert.Fingerprint,
				"alertname", alert.AlertName,
				"status", alert.Status,
				"error", err,
			)

			// Record processing error metric
			if h.metrics != nil {
				h.metrics.RecordProcessingError(classifyError(err))
			}

			failedAlerts = append(failedAlerts, AlertFailure{
				Index:       i,
				Fingerprint: alert.Fingerprint,
				AlertName:   alert.AlertName,
				Error:       err.Error(),
			})
			continue
		}

		processedCount++
	}

	return processedCount, failedAlerts
}

// classifyError classifies error for metrics.
//
// Categorizes errors into types for better observability:
//   - storage_error: Database connection/query errors
//   - processor_error: AlertProcessor internal errors
//   - validation_error: Unexpected validation errors
//   - timeout_error: Context timeout errors
//   - unknown_error: Other errors
//
// Returns:
//   - string: Error type for metrics label
func classifyError(err error) string {
	if err == nil {
		return "unknown"
	}

	errMsg := err.Error()

	// Common error patterns
	if containsAny(errMsg, []string{"storage", "database", "postgres", "sql"}) {
		return "storage_error"
	}
	if containsAny(errMsg, []string{"processor", "processing"}) {
		return "processor_error"
	}
	if containsAny(errMsg, []string{"validation", "invalid"}) {
		return "validation_error"
	}
	if containsAny(errMsg, []string{"timeout", "deadline", "context"}) {
		return "timeout_error"
	}

	return "unknown_error"
}

// containsAny checks if string contains any of the substrings.
func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// respondSuccess sends 200 OK response.
//
// Indicates all alerts were successfully processed and stored.
// Response includes statistics for monitoring and debugging.
func (h *PrometheusAlertsHandler) respondSuccess(
	w http.ResponseWriter,
	received, processed int,
	duration time.Duration,
) {
	response := PrometheusAlertsResponse{
		Status: "success",
		Data: PrometheusAlertsResultData{
			Received:  received,
			Processed: processed,
			Stored:    processed, // Assume stored = processed
			Timestamp: time.Now().Format(time.RFC3339),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode success response", "error", err)
	}
}

// respondPartialSuccess sends 207 Multi-Status response.
//
// Indicates some alerts succeeded and some failed.
// Includes detailed error information for failed alerts.
func (h *PrometheusAlertsHandler) respondPartialSuccess(
	w http.ResponseWriter,
	received, processed int,
	failedAlerts []AlertFailure,
	duration time.Duration,
) {
	response := PrometheusAlertsResponse{
		Status: "partial",
		Data: PrometheusAlertsResultData{
			Received:  received,
			Processed: processed,
			Stored:    processed,
			Failed:    len(failedAlerts),
			Errors:    failedAlerts,
			Timestamp: time.Now().Format(time.RFC3339),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMultiStatus) // 207

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode partial success response", "error", err)
	}
}

// respondError sends error response.
//
// Used for all error scenarios (400, 405, 413, 422, 500).
// Provides clear error message for debugging.
func (h *PrometheusAlertsHandler) respondError(
	w http.ResponseWriter,
	statusCode int,
	message string,
	err error,
) {
	errorMsg := message
	if err != nil {
		errorMsg = fmt.Sprintf("%s: %v", message, err)
	}

	response := PrometheusAlertsErrorResponse{
		Status: "error",
		Error:  errorMsg,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		h.logger.Error("Failed to encode error response", "error", encErr)
	}

	h.logger.Error("Request failed", "status", statusCode, "error", errorMsg)
}

// respondValidationError sends 400 Bad Request with validation errors.
//
// Provides detailed information about which fields failed validation.
// Helps users fix their requests.
func (h *PrometheusAlertsHandler) respondValidationError(
	w http.ResponseWriter,
	validationResult *webhook.ValidationResult,
) {
	errors := make([]ValidationError, len(validationResult.Errors))
	for i, err := range validationResult.Errors {
		errors[i] = ValidationError{
			Field:   err.Field,
			Message: err.Message,
			Value:   err.Value,
		}
	}

	response := PrometheusAlertsErrorResponse{
		Status: "error",
		Error:  "validation failed",
		Errors: errors,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		h.logger.Error("Failed to encode validation error response", "error", encErr)
	}

	// Record validation errors in metrics
	if h.metrics != nil {
		for _, valErr := range validationResult.Errors {
			h.metrics.RecordValidationError(valErr.Field)
		}
	}
}

// recordMetrics records all metrics for a request.
//
// This is a convenience method to record metrics consistently.
// Safe to call with nil metrics (no-op).
func (h *PrometheusAlertsHandler) recordMetrics(
	status, reason string,
	alertCount int,
	duration time.Duration,
) {
	if h.metrics == nil {
		return
	}

	// Record request metrics
	h.metrics.RecordRequest("POST", float64(duration))

	// Record validation errors if applicable
	if status == "validation_failed" {
		h.metrics.RecordValidationError(reason)
	}
}
