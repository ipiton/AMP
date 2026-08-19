package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// EnhancedPagerDutyPublisher implements AlertPublisher with full PagerDuty Events API v2 support
// Provides incident lifecycle management (trigger, acknowledge, resolve) and change events
type EnhancedPagerDutyPublisher struct {
	*BaseEnhancedPublisher                       // Embedded base publisher for common functionality
	client                 PagerDutyEventsClient // PagerDuty-specific events client
	cache                  EventKeyCache         // For tracking event keys (incident lifecycle)
}

// NewEnhancedPagerDutyPublisher creates a new enhanced PagerDuty publisher
func NewEnhancedPagerDutyPublisher(
	client PagerDutyEventsClient,
	cache EventKeyCache,
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedPagerDutyPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "pagerduty_publisher"),
		),
		client: client,
		cache:  cache,
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
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordEventTriggered(severity)
	}

	p.GetLogger().Info("PagerDuty event triggered",
		"fingerprint", alert.Fingerprint,
		"dedup_key", resp.DedupKey,
		"routing_key", routingKey,
		"alert_name", alert.AlertName,
		"severity", severity,
	)

	return nil
}

// resolveEvent resolves an event in PagerDuty
func (p *EnhancedPagerDutyPublisher) resolveEvent(ctx context.Context, enrichedAlert *core.EnrichedAlert, routingKey string) error {
	alert := enrichedAlert.Alert

	// Lookup dedup key from cache
	dedupKey, found := p.cache.Get(alert.Fingerprint)
	if !found {
		p.GetLogger().Warn("Cannot resolve event: not tracked in cache",
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
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordEventResolved()
	}

	p.GetLogger().Info("PagerDuty event resolved",
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

	// Record metrics (use message counter for change events)
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordMessage(v2.ProviderPagerDuty, "success")
	}

	p.GetLogger().Info("PagerDuty change event sent",
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

// buildPayload builds TriggerEventPayload from formatted alert data.
//
// formatPagerDuty (formatter.go) nests summary/severity/timestamp/source under
// formattedData["payload"] — mirroring the Events API v2 request shape it is
// itself building toward (TriggerEventRequest.Payload) — not at the top level
// of the map. Reading them at top level (review wave 5, finding C2) always
// missed, so every trigger shipped payload.summary/payload.severity empty;
// PagerDuty's Events API v2 requires both non-blank and returned 400. Only
// custom_details was read correctly, because that access already went through
// the nested "payload" map.
//
// Reads the nested map first and falls back to a flat top-level read (the
// pre-fix shape) so a differently-shaped formatter implementation — a custom
// AlertFormatter, or a future formatPagerDuty revision — still degrades to
// something rather than silently dropping the field.
func (p *EnhancedPagerDutyPublisher) buildPayload(formattedData map[string]any) TriggerEventPayload {
	payload := TriggerEventPayload{
		Source: "alert-history-service",
	}

	// The real shape: everything (including custom_details) nested under
	// "payload".
	nested, hasNested := formattedData["payload"].(map[string]any)

	stringField := func(key string) (string, bool) {
		if hasNested {
			if v, ok := nested[key].(string); ok {
				return v, true
			}
		}
		v, ok := formattedData[key].(string)
		return v, ok
	}

	if summary, ok := stringField("summary"); ok {
		payload.Summary = summary
	}
	if severity, ok := stringField("severity"); ok {
		payload.Severity = severity
	}
	if timestamp, ok := stringField("timestamp"); ok {
		payload.Timestamp = timestamp
	}
	if source, ok := stringField("source"); ok {
		payload.Source = source
	}

	if hasNested {
		if customDetails, ok := nested["custom_details"].(map[string]any); ok {
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
