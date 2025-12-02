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
)

// WebhookRequest represents the incoming webhook payload.
type WebhookRequest struct {
	AlertName    string                 `json:"alertname"`
	Status       string                 `json:"status"`
	Labels       map[string]string      `json:"labels"`
	Annotations  map[string]string      `json:"annotations"`
	StartsAt     string                 `json:"startsAt"`
	EndsAt       string                 `json:"endsAt"`
	GeneratorURL string                 `json:"generatorURL"`
	Fingerprint  string                 `json:"fingerprint"`
	Extra        map[string]interface{} `json:"-"` // Catch-all for additional fields
}

// WebhookResponse represents the webhook response.
type WebhookResponse struct {
	Status         string `json:"status"`
	Message        string `json:"message"`
	AlertID        string `json:"alert_id,omitempty"`
	Timestamp      string `json:"timestamp"`
	ProcessingTime string `json:"processing_time"`
}

// AlertProcessor interface for dependency injection
type AlertProcessor interface {
	ProcessAlert(ctx context.Context, alert *core.Alert) error
	Health(ctx context.Context) error
}

// WebhookConfig holds configuration for webhook HTTP handler.
type WebhookConfig struct {
	MaxRequestSize  int
	RequestTimeout  time.Duration
	MaxAlertsPerReq int
	EnableMetrics   bool
	EnableAuth      bool
	AuthType        string
	APIKey          string
	SignatureSecret string
}

// WebhookHandlers holds dependencies for webhook handlers
type WebhookHandlers struct {
	processor AlertProcessor
	logger    *slog.Logger
	config    *WebhookConfig
}

// ServeHTTP implements http.Handler interface
func (h *WebhookHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.HandleWebhook(w, r)
}

// NewWebhookHandlers creates a new WebhookHandlers instance
func NewWebhookHandlers(processor AlertProcessor, logger *slog.Logger) *WebhookHandlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandlers{
		processor: processor,
		logger:    logger,
		config:    nil, // No config for simple version
	}
}

// NewWebhookHTTPHandler creates a new WebhookHandlers instance with configuration.
func NewWebhookHTTPHandler(processor AlertProcessor, config *WebhookConfig, logger *slog.Logger) *WebhookHandlers {
	if logger == nil {
		logger = slog.Default()
	}
	if config == nil {
		config = &WebhookConfig{
			MaxRequestSize:  10 * 1024 * 1024, // 10 MB default
			RequestTimeout:  30 * time.Second,
			MaxAlertsPerReq: 1000,
			EnableMetrics:   true,
		}
	}
	return &WebhookHandlers{
		processor: processor,
		logger:    logger,
		config:    config,
	}
}

// HandleWebhook handles incoming webhook requests
func (h *WebhookHandlers) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Log the webhook request
	h.logger.Info("Webhook request received",
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"content_type", r.Header.Get("Content-Type"),
		"user_agent", r.Header.Get("User-Agent"),
	)

	// Only accept POST requests
	if r.Method != http.MethodPost {
		h.logger.Warn("Invalid HTTP method for webhook", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read request body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Log raw payload for debugging
	h.logger.Debug("Webhook payload received", "payload", string(body))

	// Parse JSON payload
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		h.logger.Error("Failed to parse webhook JSON", "error", err, "payload", string(body))
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if webhookReq.AlertName == "" {
		h.logger.Warn("Missing required field: alertname", "payload", webhookReq)
		http.Error(w, "Missing required field: alertname", http.StatusBadRequest)
		return
	}

	// Convert to core.Alert
	alert, err := webhookRequestToAlert(&webhookReq)
	if err != nil {
		h.logger.Error("Failed to convert webhook to alert", "error", err)
		http.Error(w, "Failed to process webhook", http.StatusInternalServerError)
		return
	}

	// Process the alert
	ctx := r.Context()
	if err := h.processor.ProcessAlert(ctx, alert); err != nil {
		h.logger.Error("Failed to process alert", "error", err, "alert", alert.AlertName)
		http.Error(w, "Failed to process alert", http.StatusInternalServerError)
		return
	}

	// Calculate processing time
	processingTime := time.Since(startTime)

	// Create response
	response := WebhookResponse{
		Status:         "success",
		Message:        "Webhook processed successfully",
		AlertID:        alert.Fingerprint,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		ProcessingTime: processingTime.String(),
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode webhook response", "error", err)
		return
	}

	h.logger.Info("Webhook processed successfully",
		"alert_name", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"status", alert.Status,
		"processing_time", processingTime,
	)
}

// webhookRequestToAlert converts webhook request to core.Alert
func webhookRequestToAlert(req *WebhookRequest) (*core.Alert, error) {
	// Parse timestamps
	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		// Try alternative formats
		startsAt = time.Now()
	}

	var endsAt *time.Time
	if req.EndsAt != "" {
		t, err := time.Parse(time.RFC3339, req.EndsAt)
		if err == nil {
			endsAt = &t
		}
	}

	// Parse status
	status := core.StatusFiring
	if req.Status == "resolved" {
		status = core.StatusResolved
	}

	// Generate fingerprint if not provided
	fingerprint := req.Fingerprint
	if fingerprint == "" {
		fingerprint = fmt.Sprintf("%s_%d", req.AlertName, startsAt.Unix())
	}

	// Create alert
	alert := &core.Alert{
		Fingerprint:  fingerprint,
		AlertName:    req.AlertName,
		Status:       status,
		Labels:       req.Labels,
		Annotations:  req.Annotations,
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		GeneratorURL: nil,
		Timestamp:    &startsAt,
	}

	if req.GeneratorURL != "" {
		alert.GeneratorURL = &req.GeneratorURL
	}

	return alert, nil
}

// WebhookHandler handles incoming webhook requests from alerting systems.
func WebhookHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Log the webhook request
	slog.Info("Webhook request received",
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"content_type", r.Header.Get("Content-Type"),
		"user_agent", r.Header.Get("User-Agent"),
	)

	// Only accept POST requests
	if r.Method != http.MethodPost {
		slog.Warn("Invalid HTTP method for webhook", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Log raw payload for debugging
	slog.Debug("Webhook payload received", "payload", string(body))

	// Parse JSON payload
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		slog.Error("Failed to parse webhook JSON", "error", err, "payload", string(body))
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if webhookReq.AlertName == "" {
		slog.Warn("Missing required field: alertname", "payload", webhookReq)
		http.Error(w, "Missing required field: alertname", http.StatusBadRequest)
		return
	}

	// Process the webhook
	alertID, err := processWebhook(&webhookReq)
	if err != nil {
		slog.Error("Failed to process webhook", "error", err, "alert", webhookReq.AlertName)
		http.Error(w, "Failed to process webhook", http.StatusInternalServerError)
		return
	}

	// Calculate processing time
	processingTime := time.Since(startTime)

	// Create response
	response := WebhookResponse{
		Status:         "success",
		Message:        "Webhook processed successfully",
		AlertID:        alertID,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		ProcessingTime: processingTime.String(),
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode webhook response", "error", err)
		return
	}

	slog.Info("Webhook processed successfully",
		"alert_name", webhookReq.AlertName,
		"alert_id", alertID,
		"status", webhookReq.Status,
		"processing_time", processingTime,
	)
}

// processWebhook handles the business logic for processing webhook data.
func processWebhook(req *WebhookRequest) (string, error) {
	// Generate a simple alert ID for now
	alertID := fmt.Sprintf("alert_%d", time.Now().Unix())

	// Log the alert details
	slog.Info("Processing alert",
		"alert_id", alertID,
		"alert_name", req.AlertName,
		"status", req.Status,
		"starts_at", req.StartsAt,
		"ends_at", req.EndsAt,
		"labels", req.Labels,
		"annotations", req.Annotations,
	)

	// TODO: Implement actual alert processing logic:
	// 1. Store alert in database
	// 2. Apply business rules
	// 3. Send notifications if needed
	// 4. Update metrics

	// For now, just simulate processing
	time.Sleep(10 * time.Millisecond)

	return alertID, nil
}
