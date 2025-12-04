// Package realtime provides real-time event broadcasting system for dashboard updates.
// TN-78: Real-time Updates (SSE/WebSocket) - 150% Quality Target
package realtime

import (
	"time"

	"github.com/google/uuid"
)

// Event represents a real-time event broadcast to subscribers.
type Event struct {
	// Type is the event type (alert_created, stats_updated, silence_created, etc.)
	Type string `json:"type"`

	// ID is a unique event ID (UUID)
	ID string `json:"id"`

	// Data is the event payload (varies by event type)
	Data map[string]interface{} `json:"data"`

	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// Source is the event source (alert_processor, silence_manager, stats_collector, etc.)
	Source string `json:"source"`

	// Sequence is a sequence number for event ordering (monotonically increasing)
	Sequence int64 `json:"sequence"`
}

// EventType constants for dashboard events.
const (
	// Alert Events
	EventTypeAlertCreated   = "alert_created"
	EventTypeAlertResolved  = "alert_resolved"
	EventTypeAlertFiring     = "alert_firing"
	EventTypeAlertInhibited  = "alert_inhibited"

	// Stats Events
	EventTypeStatsUpdated = "stats_updated"

	// Silence Events (reuse from TN-136)
	EventTypeSilenceCreated = "silence_created"
	EventTypeSilenceUpdated = "silence_updated"
	EventTypeSilenceDeleted = "silence_deleted"
	EventTypeSilenceExpired = "silence_expired"

	// Health Events
	EventTypeHealthChanged = "health_changed"

	// System Events
	EventTypeSystemNotification = "system_notification"
)

// EventSource constants.
const (
	EventSourceAlertProcessor  = "alert_processor"
	EventSourceSilenceManager  = "silence_manager"
	EventSourceStatsCollector   = "stats_collector"
	EventSourceHealthMonitor    = "health_monitor"
	EventSourceSystem           = "system"
)

// NewEvent creates a new Event with the given type, data, and source.
func NewEvent(eventType string, data map[string]interface{}, source string) *Event {
	return &Event{
		Type:      eventType,
		ID:        generateEventID(),
		Data:      data,
		Timestamp: time.Now(),
		Source:    source,
		Sequence:  0, // Will be set by EventBus
	}
}

// generateEventID generates a unique event ID (UUID).
func generateEventID() string {
	return uuid.New().String()
}
