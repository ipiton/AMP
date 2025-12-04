// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"context"
)

// EventSubscriber represents a subscriber to events (SSE or WebSocket connection).
type EventSubscriber interface {
	// ID returns the unique subscriber ID.
	ID() string

	// Send sends an event to the subscriber.
	// Returns an error if the subscriber is closed or the event cannot be sent.
	Send(event Event) error

	// Close closes the subscriber connection.
	Close() error

	// Context returns the subscriber context (for cancellation).
	Context() context.Context
}

// baseSubscriber provides common functionality for subscribers.
type baseSubscriber struct {
	id    string
	ctx   context.Context
	onClose func()
}

// ID returns the subscriber ID.
func (s *baseSubscriber) ID() string {
	return s.id
}

// Context returns the subscriber context.
func (s *baseSubscriber) Context() context.Context {
	return s.ctx
}
