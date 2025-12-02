package publishing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/core"
)

// EnhancedRootlyPublisher publishes alerts to Rootly with full incident lifecycle management
type EnhancedRootlyPublisher struct {
	client    RootlyIncidentsClient
	cache     IncidentIDCache
	metrics   *RootlyMetrics
	formatter AlertFormatter
	logger    *slog.Logger
}

// NewEnhancedRootlyPublisher creates a new enhanced Rootly publisher
func NewEnhancedRootlyPublisher(
	client RootlyIncidentsClient,
	cache IncidentIDCache,
	metrics *RootlyMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedRootlyPublisher{
		client:    client,
		cache:     cache,
		metrics:   metrics,
		formatter: formatter,
		logger:    logger,
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
		p.metrics.RecordError("create", err)
		return fmt.Errorf("create incident failed: %w", err)
	}

	// Store incident ID in cache
	incidentID := resp.GetID()
	p.cache.Set(enrichedAlert.Alert.Fingerprint, incidentID)

	// Update metrics
	p.metrics.RecordIncidentCreated(req.Severity)

	// Log success
	p.logger.Info("Rootly incident created",
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
		if IsNotFoundError(err) {
			p.logger.Warn("Incident not found (deleted in Rootly), recreating",
				"incident_id", incidentID,
				"fingerprint", enrichedAlert.Alert.Fingerprint,
			)

			// Delete from cache and recreate
			p.cache.Delete(enrichedAlert.Alert.Fingerprint)
			return p.createIncident(ctx, enrichedAlert, payload)
		}

		p.metrics.RecordError("update", err)
		return fmt.Errorf("update incident failed: %w", err)
	}

	// Update metrics
	p.metrics.RecordIncidentUpdated("annotation_change")

	// Log success
	p.logger.Info("Rootly incident updated",
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
		p.logger.Debug("Incident ID not found in cache, skipping resolution",
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
		if IsNotFoundError(err) || IsConflictError(err) {
			p.logger.Info("Incident already resolved or deleted",
				"incident_id", incidentID,
				"fingerprint", enrichedAlert.Alert.Fingerprint,
			)

			// Delete from cache
			p.cache.Delete(enrichedAlert.Alert.Fingerprint)
			return nil // Not an error
		}

		p.metrics.RecordError("resolve", err)
		return fmt.Errorf("resolve incident failed: %w", err)
	}

	// Delete from cache
	p.cache.Delete(enrichedAlert.Alert.Fingerprint)

	// Update metrics
	p.metrics.RecordIncidentResolved()

	// Log success
	p.logger.Info("Rootly incident resolved",
		"incident_id", incidentID,
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	return nil
}

// Name returns publisher name
func (p *EnhancedRootlyPublisher) Name() string {
	return "Rootly"
}
