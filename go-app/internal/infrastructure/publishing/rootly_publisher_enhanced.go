package publishing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// EnhancedRootlyPublisher publishes alerts to Rootly with full incident lifecycle management
type EnhancedRootlyPublisher struct {
	*BaseEnhancedPublisher                       // Embedded base publisher for common functionality
	client                 RootlyIncidentsClient // Rootly-specific incidents client
	cache                  IncidentIDCache       // For tracking incident IDs (lifecycle)
}

// NewEnhancedRootlyPublisher creates a new enhanced Rootly publisher
func NewEnhancedRootlyPublisher(
	client RootlyIncidentsClient,
	cache IncidentIDCache,
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedRootlyPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "rootly_publisher"),
		),
		client: client,
		cache:  cache,
	}
}

// Publish implements AlertPublisher interface
func (p *EnhancedRootlyPublisher) Publish(
	ctx context.Context,
	enrichedAlert *core.EnrichedAlert,
	target *core.PublishingTarget,
) error {
	// Format alert for Rootly
	payload, err := p.formatter.FormatAlert(ctx, enrichedAlert, core.FormatRootly)
	if err != nil {
		return fmt.Errorf("format alert failed: %w", err)
	}

	// Route based on alert status
	switch enrichedAlert.Alert.Status {
	case core.StatusFiring:
		return p.createOrUpdateIncident(ctx, enrichedAlert, payload)
	case core.StatusResolved:
		return p.resolveIncident(ctx, enrichedAlert)
	default:
		return fmt.Errorf("unknown alert status: %s", enrichedAlert.Alert.Status)
	}
}

// createOrUpdateIncident creates new incident or updates existing
func (p *EnhancedRootlyPublisher) createOrUpdateIncident(
	ctx context.Context,
	enrichedAlert *core.EnrichedAlert,
	payload map[string]interface{},
) error {
	fingerprint := enrichedAlert.Alert.Fingerprint

	// Check if incident exists in cache
	incidentID, exists := p.cache.Get(fingerprint)

	if exists {
		// Update existing incident
		return p.updateIncident(ctx, incidentID, enrichedAlert, payload)
	}

	// Create new incident
	return p.createIncident(ctx, enrichedAlert, payload)
}

// createIncident creates a new Rootly incident
func (p *EnhancedRootlyPublisher) createIncident(
	ctx context.Context,
	enrichedAlert *core.EnrichedAlert,
	payload map[string]interface{},
) error {
	// Build CreateIncidentRequest from payload
	req := &CreateIncidentRequest{
		Title:       payload["title"].(string),
		Description: payload["description"].(string),
		Severity:    payload["severity"].(string),
		StartedAt:   enrichedAlert.Alert.StartsAt,
	}

	// Add tags if present
	if tags, ok := payload["tags"].([]string); ok {
		req.Tags = tags
	}

	// Add custom fields if present
	if customFields, ok := payload["custom_fields"].(map[string]interface{}); ok {
		req.CustomFields = customFields
	}

	// Call Rootly API
	resp, err := p.client.CreateIncident(ctx, req)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderRootly, "incidents", GetPublishingErrorType(err))
		}
		return fmt.Errorf("create incident failed: %w", err)
	}

	// Store incident ID in cache
	incidentID := resp.GetID()
	p.cache.Set(enrichedAlert.Alert.Fingerprint, incidentID)

	// Update metrics
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordIncidentCreated(req.Severity)
	}

	// Log success
	p.GetLogger().Info("Rootly incident created",
		"incident_id", incidentID,
		"fingerprint", enrichedAlert.Alert.Fingerprint,
		"severity", req.Severity,
		"alert_name", enrichedAlert.Alert.AlertName,
	)

	return nil
}

// updateIncident updates an existing Rootly incident
func (p *EnhancedRootlyPublisher) updateIncident(
	ctx context.Context,
	incidentID string,
	enrichedAlert *core.EnrichedAlert,
	payload map[string]interface{},
) error {
	// Build UpdateIncidentRequest (only fields that changed)
	req := &UpdateIncidentRequest{}

	// Update description if present
	if description, ok := payload["description"].(string); ok {
		req.Description = description
	}

	// Update custom fields if present
	if customFields, ok := payload["custom_fields"].(map[string]interface{}); ok {
		req.CustomFields = customFields
	}

	// Call Rootly API
	_, err := p.client.UpdateIncident(ctx, incidentID, req)
	if err != nil {
		// If 404 Not Found, incident was deleted in Rootly
		if IsRootlyNotFoundError(err) {
			p.GetLogger().Warn("Incident not found (deleted in Rootly), recreating",
				"incident_id", incidentID,
				"fingerprint", enrichedAlert.Alert.Fingerprint,
			)

			// Delete from cache and recreate
			p.cache.Delete(enrichedAlert.Alert.Fingerprint)
			return p.createIncident(ctx, enrichedAlert, payload)
		}

		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderRootly, "incidents", GetPublishingErrorType(err))
		}
		return fmt.Errorf("update incident failed: %w", err)
	}

	// Update metrics
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordIncidentUpdated("annotation_change")
	}

	// Log success
	p.GetLogger().Info("Rootly incident updated",
		"incident_id", incidentID,
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	return nil
}

// resolveIncident resolves a Rootly incident
func (p *EnhancedRootlyPublisher) resolveIncident(
	ctx context.Context,
	enrichedAlert *core.EnrichedAlert,
) error {
	// Lookup incident ID from cache
	incidentID, exists := p.cache.Get(enrichedAlert.Alert.Fingerprint)
	if !exists {
		// Not tracked, skip resolution (not an error)
		p.GetLogger().Debug("Incident ID not found in cache, skipping resolution",
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
		return nil
	}

	// Build ResolveIncidentRequest
	namespace := "unknown"
	if ns := enrichedAlert.Alert.Namespace(); ns != nil {
		namespace = *ns
	}

	req := &ResolveIncidentRequest{
		Summary: fmt.Sprintf("Alert resolved: %s in %s",
			enrichedAlert.Alert.AlertName,
			namespace,
		),
	}

	// Call Rootly API
	_, err := p.client.ResolveIncident(ctx, incidentID, req)
	if err != nil {
		// If 404 Not Found or 409 Conflict, handle gracefully
		if IsRootlyNotFoundError(err) || IsRootlyConflictError(err) {
			p.GetLogger().Info("Incident already resolved or deleted",
				"incident_id", incidentID,
				"fingerprint", enrichedAlert.Alert.Fingerprint,
			)

			// Delete from cache
			p.cache.Delete(enrichedAlert.Alert.Fingerprint)
			return nil // Not an error
		}

		if p.metrics != nil {
			p.metrics.RecordAPIError(v2.ProviderRootly, "incidents", GetPublishingErrorType(err))
		}
		return fmt.Errorf("resolve incident failed: %w", err)
	}

	// Delete from cache
	p.cache.Delete(enrichedAlert.Alert.Fingerprint)

	// Update metrics
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordIncidentResolved()
	}

	// Log success
	p.GetLogger().Info("Rootly incident resolved",
		"incident_id", incidentID,
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	return nil
}

// Name returns publisher name
func (p *EnhancedRootlyPublisher) Name() string {
	return "Rootly"
}
