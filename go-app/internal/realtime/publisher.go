// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// EventPublisher publishes events to EventBus from various sources.
type EventPublisher struct {
	eventBus *DefaultEventBus
	logger   *slog.Logger
	metrics  *RealtimeMetrics
}

// NewEventPublisher creates a new event publisher.
func NewEventPublisher(eventBus *DefaultEventBus, logger *slog.Logger, metrics *RealtimeMetrics) *EventPublisher {
	return &EventPublisher{
		eventBus: eventBus,
		logger:   logger.With("component", "event_publisher"),
		metrics:  metrics,
	}
}

// PublishAlertEvent publishes an alert event.
func (p *EventPublisher) PublishAlertEvent(eventType string, alert *core.Alert) error {
	if p.eventBus == nil {
		return nil // EventBus not initialized, skip
	}

	data := map[string]interface{}{
		"fingerprint": alert.Fingerprint,
		"alertname":  alert.AlertName,
		"status":     string(alert.Status),
		"severity":   alert.Labels["severity"], // Get severity from labels
		"labels":     alert.Labels,
		"starts_at":  alert.StartsAt.Format(time.RFC3339),
	}

	if alert.EndsAt != nil {
		data["ends_at"] = alert.EndsAt.Format(time.RFC3339)
	}

	event := NewEvent(eventType, data, EventSourceAlertProcessor)
	return p.eventBus.Publish(*event)
}

// DashboardStats represents dashboard statistics.
type DashboardStats struct {
	FiringAlerts    int `json:"firing_alerts"`
	ResolvedAlerts  int `json:"resolved_today"`
	ActiveSilences  int `json:"active_silences"`
	InhibitedAlerts int `json:"inhibited_alerts"`
}

// PublishStatsEvent publishes a stats update event.
func (p *EventPublisher) PublishStatsEvent(stats *DashboardStats) error {
	if p.eventBus == nil {
		return nil // EventBus not initialized, skip
	}

	data := map[string]interface{}{
		"firing_alerts":    stats.FiringAlerts,
		"resolved_alerts":  stats.ResolvedAlerts,
		"active_silences":  stats.ActiveSilences,
		"inhibited_alerts": stats.InhibitedAlerts,
	}

	event := NewEvent(EventTypeStatsUpdated, data, EventSourceStatsCollector)
	return p.eventBus.Publish(*event)
}

// PublishHealthEvent publishes a health change event.
func (p *EventPublisher) PublishHealthEvent(component string, status string, latency float64, message string) error {
	if p.eventBus == nil {
		return nil // EventBus not initialized, skip
	}

	data := map[string]interface{}{
		"component":  component,
		"status":     status,
		"latency_ms": latency,
	}

	if message != "" {
		data["message"] = message
	}

	event := NewEvent(EventTypeHealthChanged, data, EventSourceHealthMonitor)
	return p.eventBus.Publish(*event)
}

// PublishSystemNotification publishes a system notification event.
func (p *EventPublisher) PublishSystemNotification(level string, message string) error {
	if p.eventBus == nil {
		return nil // EventBus not initialized, skip
	}

	data := map[string]interface{}{
		"level":   level, // info, warning, error
		"message": message,
	}

	event := NewEvent(EventTypeSystemNotification, data, EventSourceSystem)
	return p.eventBus.Publish(*event)
}
