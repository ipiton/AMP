// Package proxy provides business logic for intelligent proxy webhook processing.
package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/cmd/server/handlers/proxy"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/publishing"
	"github.com/ipiton/AMP/pkg/metrics"
)

// ProxyWebhookService implements the ProxyWebhookService interface.
type ProxyWebhookService struct {
	// Core dependencies
	alertProcessor    *services.AlertProcessor          // TN-061 (storage)
	classificationSvc services.ClassificationService    // TN-033
	filterEngine      services.FilterEngine             // TN-035
	targetManager     publishing.TargetDiscoveryManager // TN-047
	parallelPublisher publishing.ParallelPublisher      // TN-058

	// Configuration
	config  *proxy.ProxyWebhookConfig
	logger  *slog.Logger
	metrics *ProxyMetrics

	// Internal state
	mu    sync.RWMutex
	stats *ProxyStats
}

// ProxyMetrics holds Prometheus metrics for proxy operations.
type ProxyMetrics struct {
	// TODO: Implement Prometheus metrics in Phase 7
	// RequestsTotal       *prometheus.CounterVec
	// RequestDuration     *prometheus.HistogramVec
	// AlertsReceived      *prometheus.CounterVec
	// AlertsProcessed     *prometheus.CounterVec
	// ClassificationTime  *prometheus.HistogramVec
	// PublishingTime      *prometheus.HistogramVec
}

// ProxyStats holds internal statistics.
type ProxyStats struct {
	TotalRequests        int64
	TotalAlertsReceived  int64
	TotalAlertsProcessed int64
	TotalAlertsFiltered  int64
	TotalAlertsPublished int64
	TotalAlertsFailed    int64
	LastProcessedAt      time.Time
}

// ServiceConfig holds configuration for ProxyWebhookService.
type ServiceConfig struct {
	AlertProcessor    *services.AlertProcessor
	ClassificationSvc services.ClassificationService
	FilterEngine      services.FilterEngine
	TargetManager     publishing.TargetDiscoveryManager
	ParallelPublisher publishing.ParallelPublisher
	Config            *proxy.ProxyWebhookConfig
	Logger            *slog.Logger
	Metrics           *metrics.MetricsRegistry
}

// NewProxyWebhookService creates a new proxy webhook service.
func NewProxyWebhookService(cfg ServiceConfig) (*ProxyWebhookService, error) {
	// Validate configuration
	if cfg.AlertProcessor == nil {
		return nil, fmt.Errorf("alert processor is required")
	}
	if cfg.ClassificationSvc == nil {
		return nil, fmt.Errorf("classification service is required")
	}
	if cfg.FilterEngine == nil {
		return nil, fmt.Errorf("filter engine is required")
	}
	if cfg.TargetManager == nil {
		return nil, fmt.Errorf("target manager is required")
	}
	if cfg.ParallelPublisher == nil {
		return nil, fmt.Errorf("parallel publisher is required")
	}
	if cfg.Config == nil {
		cfg.Config = proxy.DefaultProxyWebhookConfig()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Validate config
	if err := cfg.Config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	svc := &ProxyWebhookService{
		alertProcessor:    cfg.AlertProcessor,
		classificationSvc: cfg.ClassificationSvc,
		filterEngine:      cfg.FilterEngine,
		targetManager:     cfg.TargetManager,
		parallelPublisher: cfg.ParallelPublisher,
		config:            cfg.Config,
		logger:            cfg.Logger,
		metrics:           &ProxyMetrics{},
		stats:             &ProxyStats{},
	}

	cfg.Logger.Info("Proxy webhook service initialized",
		"classification_enabled", cfg.Config.EnableClassification,
		"filtering_enabled", cfg.Config.EnableFiltering,
		"publishing_enabled", cfg.Config.EnablePublishing,
		"max_concurrent_alerts", cfg.Config.MaxConcurrentAlerts,
		"targets_available", cfg.TargetManager.GetTargetCount(),
	)

	return svc, nil
}

// ProcessWebhook processes a proxy webhook request end-to-end.
func (s *ProxyWebhookService) ProcessWebhook(
	ctx context.Context,
	req *proxy.ProxyWebhookRequest,
) (*proxy.ProxyWebhookResponse, error) {
	startTime := time.Now()

	s.logger.Info("Processing proxy webhook",
		"receiver", req.Receiver,
		"alerts_count", len(req.Alerts),
	)

	// Update stats
	s.updateStats(func(stats *ProxyStats) {
		stats.TotalRequests++
		stats.TotalAlertsReceived += int64(len(req.Alerts))
		stats.LastProcessedAt = time.Now()
	})

	// Process each alert through pipelines
	results := make([]proxy.AlertProcessingResult, 0, len(req.Alerts))

	// Process alerts (sequential for now, can parallelize later with semaphore)
	for _, alertPayload := range req.Alerts {
		result, err := s.processAlert(ctx, &alertPayload, req.Receiver)
		if err != nil && !s.config.ContinueOnError {
			// Fail fast if configured
			return nil, fmt.Errorf("failed to process alert: %w", err)
		}

		if result != nil {
			results = append(results, *result)
		}
	}

	// Aggregate results and build response
	response := s.aggregateResults(results, time.Since(startTime))

	s.logger.Info("Proxy webhook processing complete",
		"receiver", req.Receiver,
		"status", response.Status,
		"processed", response.AlertsSummary.TotalProcessed,
		"filtered", response.AlertsSummary.TotalFiltered,
		"published", response.AlertsSummary.TotalPublished,
		"failed", response.AlertsSummary.TotalFailed,
		"duration", time.Since(startTime),
	)

	return response, nil
}

// processAlert processes a single alert through all pipelines.
func (s *ProxyWebhookService) processAlert(
	ctx context.Context,
	payload *proxy.AlertPayload,
	receiver string,
) (*proxy.AlertProcessingResult, error) {
	result := &proxy.AlertProcessingResult{
		Fingerprint: payload.Fingerprint,
		AlertName:   payload.Labels["alertname"],
	}

	// Convert to internal alert format
	alert, err := payload.ConvertToAlert()
	if err != nil {
		result.Status = "failed"
		result.ErrorMessage = fmt.Sprintf("conversion error: %v", err)
		s.logger.Error("Failed to convert alert", "error", err, "fingerprint", payload.Fingerprint)
		return result, nil // Don't fail entire batch
	}

	result.Fingerprint = alert.Fingerprint
	result.AlertName = alert.AlertName

	// Store alert in database (backward compatibility with TN-061)
	if err := s.alertProcessor.ProcessAlert(ctx, alert); err != nil {
		s.logger.Warn("Failed to store alert", "error", err, "fingerprint", alert.Fingerprint)
		// Continue processing even if storage fails (non-critical)
	}

	// Pipeline 1: Classification
	classification, classTime, err := s.classifyAlert(ctx, alert)
	if err != nil {
		// Classification errors are logged but not fatal (fallback used)
		s.logger.Warn("Classification pipeline error", "error", err, "fingerprint", alert.Fingerprint)
	}
	result.Classification = classification
	result.ClassificationTime = classTime

	// Pipeline 2: Filtering
	filterAction, filterReason, err := s.filterAlert(ctx, alert, classification, receiver)
	if err != nil {
		s.logger.Error("Filtering pipeline error", "error", err, "fingerprint", alert.Fingerprint)
		// Default to ALLOW on error (fail open for availability)
		filterAction = proxy.FilterActionAllow
		filterReason = "filter error (default allow)"
	}
	result.FilterAction = string(filterAction)
	result.FilterReason = filterReason

	// If filtered, skip publishing
	if filterAction == proxy.FilterActionDeny {
		result.Status = "filtered"
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsFiltered++
		})
		return result, nil
	}

	// Pipeline 3: Publishing
	publishResults, err := s.publishAlert(ctx, alert, classification)
	if err != nil {
		s.logger.Error("Publishing pipeline error", "error", err, "fingerprint", alert.Fingerprint)
		result.Status = "failed"
		result.ErrorMessage = fmt.Sprintf("publishing error: %v", err)
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsFailed++
		})
		return result, nil
	}

	result.PublishingResults = publishResults

	// Determine final status based on publishing results
	successCount := 0
	for _, pubResult := range publishResults {
		if pubResult.Success {
			successCount++
		}
	}

	if len(publishResults) == 0 {
		// No targets available - still consider success
		result.Status = "success"
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsProcessed++
		})
	} else if successCount == len(publishResults) {
		result.Status = "success"
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsProcessed++
			stats.TotalAlertsPublished++
		})
	} else if successCount > 0 {
		result.Status = "partial"
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsProcessed++
		})
	} else {
		result.Status = "failed"
		s.updateStats(func(stats *ProxyStats) {
			stats.TotalAlertsFailed++
		})
	}

	return result, nil
}

// classifyAlert runs classification pipeline.
func (s *ProxyWebhookService) classifyAlert(
	ctx context.Context,
	alert *core.Alert,
) (*proxy.ClassificationResult, time.Duration, error) {
	startTime := time.Now()

	// Check if classification is enabled
	if !s.config.EnableClassification {
		return s.defaultClassification(alert), time.Since(startTime), nil
	}

	// Add timeout to context
	ctx, cancel := context.WithTimeout(ctx, s.config.ClassificationTimeout)
	defer cancel()

	// Call classification service (handles caching, circuit breaker, fallback)
	coreResult, err := s.classificationSvc.ClassifyAlert(ctx, alert)
	duration := time.Since(startTime)

	if err != nil {
		s.logger.Warn("Classification failed, using default",
			"fingerprint", alert.Fingerprint,
			"error", err)
		return s.defaultClassification(alert), duration, nil
	}

	// Convert to proxy classification result
	result := &proxy.ClassificationResult{
		Severity:        string(coreResult.Severity),
		Category:        coreResult.Category,
		Confidence:      coreResult.Confidence,
		Source:          "llm",      // TODO: Get from coreResult
		Recommendations: []string{}, // TODO: Get from coreResult if available
		Timestamp:       time.Now(),
	}

	return result, duration, nil
}

// defaultClassification provides a default classification based on labels.
func (s *ProxyWebhookService) defaultClassification(alert *core.Alert) *proxy.ClassificationResult {
	// Simple rule-based classification from labels
	severity := "info" // default
	if sev, ok := alert.Labels["severity"]; ok {
		switch sev {
		case "critical":
			severity = "critical"
		case "warning":
			severity = "warning"
		case "info":
			severity = "info"
		}
	}

	return &proxy.ClassificationResult{
		Severity:   severity,
		Category:   "unknown",
		Confidence: 0.5,
		Source:     "default",
		Timestamp:  time.Now(),
	}
}

// filterAlert runs filtering pipeline.
func (s *ProxyWebhookService) filterAlert(
	ctx context.Context,
	alert *core.Alert,
	classification *proxy.ClassificationResult,
	receiver string,
) (proxy.FilterAction, string, error) {
	// Check if filtering is enabled
	if !s.config.EnableFiltering {
		return proxy.FilterActionAllow, "filtering disabled", nil
	}

	// Add timeout to context
	ctx, cancel := context.WithTimeout(ctx, s.config.FilteringTimeout)
	defer cancel()

	// Convert proxy.ClassificationResult to core.ClassificationResult
	var coreClassification *core.ClassificationResult
	if classification != nil {
		coreClassification = &core.ClassificationResult{
			Severity:   severityStringToCore(classification.Severity),
			Category:   classification.Category,
			Confidence: classification.Confidence,
		}
	}

	// Call TN-035 FilterEngine
	blocked, reason := s.filterEngine.ShouldBlock(alert, coreClassification)

	if blocked {
		return proxy.FilterActionDeny, reason, nil
	}

	return proxy.FilterActionAllow, "filter passed", nil
}

// severityStringToCore converts string severity to core.Severity
func severityStringToCore(severity string) core.Severity {
	switch severity {
	case "critical":
		return core.SeverityCritical
	case "warning":
		return core.SeverityWarning
	case "info":
		return core.SeverityInfo
	default:
		return core.SeverityUnknown
	}
}

// publishAlert runs publishing pipeline.
func (s *ProxyWebhookService) publishAlert(
	ctx context.Context,
	alert *core.Alert,
	classification *proxy.ClassificationResult,
) ([]proxy.TargetPublishingResult, error) {
	startTime := time.Now()

	// Check if publishing is enabled
	if !s.config.EnablePublishing {
		return []proxy.TargetPublishingResult{}, nil
	}

	// Add timeout to context
	ctx, cancel := context.WithTimeout(ctx, s.config.PublishingTimeout)
	defer cancel()

	// Get all enabled targets from TN-047 TargetDiscoveryManager
	targets := s.targetManager.ListTargets()
	if len(targets) == 0 {
		s.logger.Warn("No publishing targets available",
			"fingerprint", alert.Fingerprint)
		return []proxy.TargetPublishingResult{}, nil
	}

	// Filter enabled targets
	enabledTargets := make([]*core.PublishingTarget, 0, len(targets))
	for _, target := range targets {
		if target.Enabled {
			enabledTargets = append(enabledTargets, target)
		}
	}

	if len(enabledTargets) == 0 {
		s.logger.Warn("No enabled targets available",
			"fingerprint", alert.Fingerprint,
			"total_targets", len(targets))
		return []proxy.TargetPublishingResult{}, nil
	}

	// Convert to EnrichedAlert for TN-058 ParallelPublisher
	enrichedAlert := &core.EnrichedAlert{
		Alert: alert,
		Classification: &core.ClassificationResult{
			Severity:   severityStringToCore(classification.Severity),
			Category:   classification.Category,
			Confidence: classification.Confidence,
		},
		EnrichmentMetadata: map[string]string{
			"source":    "proxy_webhook",
			"timestamp": time.Now().Format(time.RFC3339),
		},
		ProcessingTimestamp: time.Now(),
	}

	// Publish to all enabled targets in parallel using TN-058
	result, err := s.parallelPublisher.PublishToMultiple(ctx, enrichedAlert, enabledTargets)
	if err != nil {
		s.logger.Error("Parallel publishing failed",
			"fingerprint", alert.Fingerprint,
			"error", err,
			"targets", len(enabledTargets))
		// Continue with partial results if available
	}

	// Convert publishing.TargetPublishResult to proxy.TargetPublishingResult
	proxyResults := make([]proxy.TargetPublishingResult, 0)
	if result != nil {
		for _, pubResult := range result.Results {
			proxyResult := proxy.TargetPublishingResult{
				TargetName:     pubResult.TargetName,
				TargetType:     pubResult.TargetType,
				Success:        pubResult.Success,
				StatusCode:     pubResult.StatusCode,
				ErrorMessage:   pubResult.ErrorMessage,
				ErrorCode:      pubResult.ErrorCode,
				RetryCount:     pubResult.RetryCount,
				ProcessingTime: pubResult.Duration,
			}
			proxyResults = append(proxyResults, proxyResult)
		}
	}

	s.logger.Info("Publishing pipeline complete",
		"fingerprint", alert.Fingerprint,
		"targets", len(enabledTargets),
		"successful", result.SuccessCount,
		"failed", result.FailureCount,
		"duration", time.Since(startTime),
	)

	return proxyResults, nil
}

// aggregateResults builds final response from pipeline results.
func (s *ProxyWebhookService) aggregateResults(
	results []proxy.AlertProcessingResult,
	totalTime time.Duration,
) *proxy.ProxyWebhookResponse {
	response := &proxy.ProxyWebhookResponse{
		Timestamp:      time.Now(),
		ProcessingTime: totalTime,
		AlertResults:   results,
	}

	// Calculate summary counts
	summary := proxy.AlertsProcessingSummary{
		TotalReceived: len(results),
	}

	publishingSummary := proxy.PublishingSummary{}

	for _, result := range results {
		switch result.Status {
		case "success":
			summary.TotalProcessed++
			summary.TotalPublished++
		case "filtered":
			summary.TotalProcessed++
			summary.TotalFiltered++
		case "failed":
			summary.TotalFailed++
		case "partial":
			summary.TotalProcessed++
			summary.TotalPublished++
		}

		if result.Classification != nil {
			summary.TotalClassified++
		}

		// Aggregate publishing results
		for _, pubResult := range result.PublishingResults {
			publishingSummary.TotalTargets++
			if pubResult.Success {
				publishingSummary.SuccessfulTargets++
			} else {
				publishingSummary.FailedTargets++
			}
			publishingSummary.TotalPublishTime += pubResult.ProcessingTime
		}
	}

	response.AlertsSummary = summary
	response.PublishingSummary = publishingSummary

	// Determine overall status
	if summary.TotalFailed == 0 && summary.TotalFiltered < len(results) {
		response.Status = "success"
		response.Message = "All alerts processed successfully"
	} else if summary.TotalFailed == len(results) {
		response.Status = "failed"
		response.Message = "All alerts failed processing"
	} else {
		response.Status = "partial"
		response.Message = fmt.Sprintf(
			"%d of %d alerts filtered, %d failed",
			summary.TotalFiltered,
			len(results),
			summary.TotalFailed,
		)
		if publishingSummary.FailedTargets > 0 {
			response.Message = fmt.Sprintf(
				"%d of %d alerts filtered, %d of %d targets failed",
				summary.TotalFiltered,
				len(results),
				publishingSummary.FailedTargets,
				publishingSummary.TotalTargets,
			)
		}
	}

	return response
}

// updateStats safely updates internal statistics.
func (s *ProxyWebhookService) updateStats(fn func(*ProxyStats)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.stats)
}

// GetStats returns current statistics.
func (s *ProxyWebhookService) GetStats() ProxyStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.stats
}

// Health checks service health.
func (s *ProxyWebhookService) Health(ctx context.Context) error {
	// Check alert processor
	if err := s.alertProcessor.Health(ctx); err != nil {
		return fmt.Errorf("alert processor unhealthy: %w", err)
	}

	// Check classification service
	if err := s.classificationSvc.Health(ctx); err != nil {
		return fmt.Errorf("classification service unhealthy: %w", err)
	}

	// FilterEngine doesn't have Health() method - skip health check

	// Check target manager (has targets discovered)
	if s.targetManager.GetTargetCount() == 0 {
		s.logger.Warn("No publishing targets discovered (non-critical)")
		// Not critical - we can continue without publishing
	}

	// ParallelPublisher doesn't have Health() method - skip health check

	return nil
}
