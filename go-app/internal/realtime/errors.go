// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import "errors"

var (
	// ErrEventChannelFull is returned when the event channel is full.
	ErrEventChannelFull = errors.New("event channel full")

	// ErrSubscriberClosed is returned when trying to send to a closed subscriber.
	ErrSubscriberClosed = errors.New("subscriber closed")

	// ErrInvalidEvent is returned when an event is invalid.
	ErrInvalidEvent = errors.New("invalid event")
)
