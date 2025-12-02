package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// EnhancedPagerDutyPublisher implements AlertPublisher with full PagerDuty Events API v2 support
// Provides incident lifecycle management (trigger, acknowledge, resolve) and change events
type EnhancedPagerDutyPublisher struct {
	client    PagerDutyEventsClient
	cache     EventKeyCache
	metrics   *PagerDutyMetrics
	formatter AlertFormatter
	logger    *slog.Logger
}

// NewEnhancedPagerDutyPublisher creates a new enhanced PagerDuty publisher
func NewEnhancedPagerDutyPublisher(
	client PagerDutyEventsClient,
	cache EventKeyCache,
	metrics *PagerDutyMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedPagerDutyPublisher{
		client:    client,
		cache:     cache,
		metrics:   metrics,
		formatter: formatter,
		logger:    logger,
	}
}

// Publish publishes enriched alert to PagerDuty
// Routes to trigger/acknowledge/resolve based on alert status
func (p *EnhancedPagerDutyPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	alert := enrichedAlert.Alert

	// Extract routing key from target
	routingKey := p.extractRoutingKey(target)
	if routingKey == "" {
		return ErrMissingRoutingKey
	}

	// Check for change event label
	if isChangeEvent(alert) {
		return p.sendChangeEvent(ctx, enrichedAlert, routingKey)
	}

	// Determine event action based on alert status
	switch alert.Status {
	case core.StatusFiring:
		return p.triggerEvent(ctx, enrichedAlert, routingKey)
	case core.StatusResolved:
		return p.resolveEvent(ctx, enrichedAlert, routingKey)
	default:
		return fmt.Errorf("unknown alert status: %s", alert.Status)
	}
}

// Name returns publisher name
func (p *EnhancedPagerDutyPublisher) Name() string {
	return "PagerDuty"
}

// triggerEvent sends a trigger event to PagerDuty (creates or updates incident)
func (p *EnhancedPagerDutyPublisher) triggerEvent(ctx context.Context, enrichedAlert *core.EnrichedAlert, routingKey string) error {
	alert := enrichedAlert.Alert

	// Format alert using TN-051 formatter
	formattedPayload, err := p.formatter.FormatAlert(ctx, enrichedAlert, core.FormatPagerDuty)
	if err != nil {
		return fmt.Errorf("failed to format alert: %w", err)
	}

	// Build payload from formatted data
	payload := p.buildPayload(formattedPayload)

	// Build trigger request
	req := &TriggerEventRequest{
		RoutingKey:  routingKey,
		EventAction: EventActionTrigger,
		DedupKey:    alert.Fingerprint,
		Payload:     payload,
		Links:       p.extractLinks(alert),
		Images:      p.extractImages(alert),
	}

	// Send to PagerDuty
	resp, err := p.client.TriggerEvent(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to trigger event: %w", err)
	}

	// Cache dedup key for future updates
	p.cache.Set(alert.Fingerprint, resp.DedupKey)

	// Record metrics
	severity := getSeverity(enrichedAlert)
	p.metrics.EventsTriggered.WithLabelValues(routingKey, severity).Inc()

	p.logger.Info("PagerDuty event triggered",
		"fingerprint", alert.Fingerprint,
		"dedup_key", resp.DedupKey,
		"routing_key", routingKey,
		"alert_name", alert.AlertName,
		"severity", severity,
	)

	return nil
}

// acknowledgeEvent acknowledges an event in PagerDuty
func (p *EnhancedPagerDutyPublisher) acknowledgeEvent(ctx context.Context, enrichedAlert *core.EnrichedAlert, routingKey string) error {
	alert := enrichedAlert.Alert

	// Lookup dedup key from cache
	dedupKey, found := p.cache.Get(alert.Fingerprint)
	if !found {
		p.logger.Warn("Cannot acknowledge event: not tracked in cache",
			"fingerprint", alert.Fingerprint,
			"alert_name", alert.AlertName,
		)
		return ErrEventNotTracked
	}

	// Build acknowledge request
	req := &AcknowledgeEventRequest{
		RoutingKey:  routingKey,
		EventAction: EventActionAcknowledge,
		DedupKey:    dedupKey,
	}

	// Send to PagerDuty
	_, err := p.client.AcknowledgeEvent(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to acknowledge event: %w", err)
	}

	// Record metrics
	p.metrics.EventsAcknowledged.WithLabelValues(routingKey).Inc()

	p.logger.Info("PagerDuty event acknowledged",
		"fingerprint", alert.Fingerprint,
		"dedup_key", dedupKey,
		"routing_key", routingKey,
		"alert_name", alert.AlertName,
	)

	return nil
}

// resolveEvent resolves an event in PagerDuty
func (p *EnhancedPagerDutyPublisher) resolveEvent(ctx context.Context, enrichedAlert *core.EnrichedAlert, routingKey string) error {
	alert := enrichedAlert.Alert

	// Lookup dedup key from cache
	dedupKey, found := p.cache.Get(alert.Fingerprint)
	if !found {
		p.logger.Warn("Cannot resolve event: not tracked in cache",
			"fingerprint", alert.Fingerprint,
			"alert_name", alert.AlertName,
		)
		return ErrEventNotTracked
	}

	// Build resolve request
	req := &ResolveEventRequest{
		RoutingKey:  routingKey,
		EventAction: EventActionResolve,
		DedupKey:    dedupKey,
	}

	// Send to PagerDuty
	_, err := p.client.ResolveEvent(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to resolve event: %w", err)
	}

	// Remove from cache (event lifecycle complete)
	p.cache.Delete(alert.Fingerprint)

	// Record metrics
	p.metrics.EventsResolved.WithLabelValues(routingKey).Inc()

	p.logger.Info("PagerDuty event resolved",
		"fingerprint", alert.Fingerprint,
		"dedup_key", dedupKey,
		"routing_key", routingKey,
		"alert_name", alert.AlertName,
	)

	return nil
}

// sendChangeEvent sends a change event to PagerDuty (deployment, config change, etc.)
func (p *EnhancedPagerDutyPublisher) sendChangeEvent(ctx context.Context, enrichedAlert *core.EnrichedAlert, routingKey string) error {
	alert := enrichedAlert.Alert

	// Build change event request
	req := &ChangeEventRequest{
		RoutingKey: routingKey,
		Payload: ChangeEventPayload{
			Summary:   fmt.Sprintf("Change: %s", alert.AlertName),
			Source:    "alert-history-service",
			Timestamp: alert.StartsAt.Format(time.RFC3339),
			CustomDetails: map[string]interface{}{
				"alert_name":  alert.AlertName,
				"fingerprint": alert.Fingerprint,
				"labels":      alert.Labels,
				"annotations": alert.Annotations,
			},
		},
		Links: p.extractLinks(alert),
	}

	// Send to PagerDuty
	_, err := p.client.SendChangeEvent(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to send change event: %w", err)
	}

	// Record metrics
	p.metrics.ChangeEvents.WithLabelValues(routingKey).Inc()

	p.logger.Info("PagerDuty change event sent",
		"fingerprint", alert.Fingerprint,
		"routing_key", routingKey,
		"alert_name", alert.AlertName,
	)

	return nil
}

// Helper Methods

// extractRoutingKey extracts routing key from target configuration
func (p *EnhancedPagerDutyPublisher) extractRoutingKey(target *core.PublishingTarget) string {
	// Check target headers for routing_key
	if routingKey, ok := target.Headers["routing_key"]; ok {
		return routingKey
	}

	// Check for Authorization header (Bearer token format)
	if auth, ok := target.Headers["Authorization"]; ok {
		// Remove "Bearer " prefix if present
		const bearerPrefix = "Bearer "
		if len(auth) > len(bearerPrefix) && auth[:len(bearerPrefix)] == bearerPrefix {
			return auth[len(bearerPrefix):]
		}
		return auth
	}

	return ""
}

// buildPayload builds TriggerEventPayload from formatted alert data
func (p *EnhancedPagerDutyPublisher) buildPayload(formattedData map[string]any) TriggerEventPayload {
	payload := TriggerEventPayload{
		Source: "alert-history-service",
	}

	// Extract fields from formatted data
	if summary, ok := formattedData["summary"].(string); ok {
		payload.Summary = summary
	}
	if severity, ok := formattedData["severity"].(string); ok {
		payload.Severity = severity
	}
	if timestamp, ok := formattedData["timestamp"].(string); ok {
		payload.Timestamp = timestamp
	}
	if source, ok := formattedData["source"].(string); ok {
		payload.Source = source
	}

	// Extract custom_details from payload
	if payloadMap, ok := formattedData["payload"].(map[string]any); ok {
		if customDetails, ok := payloadMap["custom_details"].(map[string]any); ok {
			payload.CustomDetails = customDetails
		}
	}

	return payload
}

// extractLinks extracts links from alert annotations
func (p *EnhancedPagerDutyPublisher) extractLinks(alert *core.Alert) []EventLink {
	var links []EventLink

	// Extract Grafana dashboard link
	if grafanaURL, ok := alert.Annotations["grafana_url"]; ok {
		links = append(links, EventLink{
			Href: grafanaURL,
			Text: "Grafana Dashboard",
		})
	}

	// Extract Runbook link
	if runbookURL, ok := alert.Annotations["runbook_url"]; ok {
		links = append(links, EventLink{
			Href: runbookURL,
			Text: "Runbook",
		})
	}

	return links
}

// extractImages extracts images from alert annotations
func (p *EnhancedPagerDutyPublisher) extractImages(alert *core.Alert) []EventImage {
	var images []EventImage

	// Extract Grafana snapshot image
	if snapshotURL, ok := alert.Annotations["grafana_snapshot"]; ok {
		images = append(images, EventImage{
			Src: snapshotURL,
			Alt: "Grafana Snapshot",
		})
	}

	return images
}

// getSeverity gets severity from enriched alert
func getSeverity(enrichedAlert *core.EnrichedAlert) string {
	if enrichedAlert.Classification != nil {
		switch enrichedAlert.Classification.Severity {
		case core.SeverityCritical:
			return SeverityCritical
		case core.SeverityWarning:
			return SeverityWarning
		case core.SeverityInfo:
			return SeverityInfo
		default:
			return SeverityWarning
		}
	}
	return SeverityWarning
}

// isChangeEvent checks if alert is a change event (deployment, config change, etc.)
func isChangeEvent(alert *core.Alert) bool {
	// Check for change_event label
	if changeEvent, ok := alert.Labels["change_event"]; ok {
		return changeEvent == "true"
	}
	return false
}
