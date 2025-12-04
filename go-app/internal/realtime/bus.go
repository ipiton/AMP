// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// EventBus manages event subscriptions and broadcasting.
type EventBus interface {
	// Subscribe adds a subscriber to the event bus.
	Subscribe(subscriber EventSubscriber) error

	// Unsubscribe removes a subscriber from the event bus.
	Unsubscribe(subscriber EventSubscriber) error

	// Publish broadcasts an event to all subscribers.
	Publish(event Event) error

	// GetActiveSubscribers returns the number of active subscribers.
	GetActiveSubscribers() int

	// Start starts the event bus (run in goroutine).
	Start(ctx context.Context) error

	// Stop stops the event bus gracefully.
	Stop(ctx context.Context) error
}

// DefaultEventBus is the default implementation of EventBus.
type DefaultEventBus struct {
	// subscribers is a map of active subscribers
	subscribers map[EventSubscriber]bool

	// mu protects the subscribers map
	mu sync.RWMutex

	// eventChan is a buffered channel for events to broadcast
	eventChan chan Event

	// sequence is a monotonically increasing sequence number for events
	sequence int64

	// logger for structured logging
	logger *slog.Logger

	// metrics for Prometheus metrics (optional)
	metrics *RealtimeMetrics

	// stopChan signals the broadcast worker to stop
	stopChan chan struct{}

	// wg waits for the broadcast worker to finish
	wg sync.WaitGroup
}

// NewEventBus creates a new EventBus.
func NewEventBus(logger *slog.Logger, metrics *RealtimeMetrics) *DefaultEventBus {
	return &DefaultEventBus{
		subscribers: make(map[EventSubscriber]bool),
		eventChan:   make(chan Event, 1000), // Buffered channel
		sequence:   0,
		logger:     logger.With("component", "event_bus"),
		metrics:    metrics,
		stopChan:   make(chan struct{}),
	}
}

// Subscribe adds a subscriber to the event bus.
func (b *DefaultEventBus) Subscribe(subscriber EventSubscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subscribers == nil {
		b.subscribers = make(map[EventSubscriber]bool)
	}

	b.subscribers[subscriber] = true

	b.logger.Info("Subscriber added",
		"subscriber_id", subscriber.ID(),
		"total_subscribers", len(b.subscribers),
	)

	if b.metrics != nil {
		b.metrics.ConnectionsActive.Set(float64(len(b.subscribers)))
	}

	return nil
}

// Unsubscribe removes a subscriber from the event bus.
func (b *DefaultEventBus) Unsubscribe(subscriber EventSubscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[subscriber]; ok {
		delete(b.subscribers, subscriber)
		subscriber.Close()

		b.logger.Info("Subscriber removed",
			"subscriber_id", subscriber.ID(),
			"total_subscribers", len(b.subscribers),
		)

		if b.metrics != nil {
			b.metrics.ConnectionsActive.Set(float64(len(b.subscribers)))
		}
	}

	return nil
}

// Publish broadcasts an event to all subscribers.
func (b *DefaultEventBus) Publish(event Event) error {
	// Set sequence number
	event.Sequence = atomic.AddInt64(&b.sequence, 1)

	// Non-blocking send to event channel
	select {
	case b.eventChan <- event:
		b.logger.Debug("Event queued for broadcast",
			"event_type", event.Type,
			"event_id", event.ID,
			"sequence", event.Sequence,
		)
		return nil
	default:
		// Channel full, drop event
		b.logger.Warn("Event channel full, dropping event",
			"event_type", event.Type,
			"event_id", event.ID,
		)
		if b.metrics != nil {
			b.metrics.ErrorsTotal.WithLabelValues("channel_full").Inc()
		}
		return ErrEventChannelFull
	}
}

// GetActiveSubscribers returns the number of active subscribers.
func (b *DefaultEventBus) GetActiveSubscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Start starts the event bus broadcast worker (run in goroutine).
func (b *DefaultEventBus) Start(ctx context.Context) error {
	b.wg.Add(1)
	go b.broadcastWorker(ctx)
	b.logger.Info("Event bus started")
	return nil
}

// Stop stops the event bus gracefully.
func (b *DefaultEventBus) Stop(ctx context.Context) error {
	b.logger.Info("Stopping event bus")

	// Signal broadcast worker to stop
	close(b.stopChan)

	// Wait for broadcast worker to finish (with timeout)
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.logger.Info("Event bus stopped gracefully")
		return nil
	case <-ctx.Done():
		b.logger.Warn("Event bus stop timeout")
		return ctx.Err()
	}
}

// broadcastWorker is the background worker that broadcasts events to all subscribers.
func (b *DefaultEventBus) broadcastWorker(ctx context.Context) {
	defer b.wg.Done()

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Broadcast worker stopping (context cancelled)")
			return

		case <-b.stopChan:
			b.logger.Info("Broadcast worker stopping (stop signal)")
			return

		case event := <-b.eventChan:
			b.broadcastEvent(event)
		}
	}
}

// broadcastEvent broadcasts an event to all subscribers concurrently.
func (b *DefaultEventBus) broadcastEvent(event Event) {
	start := time.Now()

	// Get snapshot of subscribers
	b.mu.RLock()
	subscribers := make([]EventSubscriber, 0, len(b.subscribers))
	for sub := range b.subscribers {
		subscribers = append(subscribers, sub)
	}
	b.mu.RUnlock()

	if len(subscribers) == 0 {
		b.logger.Debug("No subscribers to broadcast event",
			"event_type", event.Type,
			"event_id", event.ID,
		)
		return
	}

	b.logger.Debug("Broadcasting event",
		"event_type", event.Type,
		"event_id", event.ID,
		"subscribers", len(subscribers),
	)

	// Broadcast to all subscribers concurrently
	var wg sync.WaitGroup
	successCount := int64(0)
	errorCount := int64(0)

	for _, subscriber := range subscribers {
		wg.Add(1)
		go func(sub EventSubscriber) {
			defer wg.Done()

			// Check if subscriber context is cancelled
			select {
			case <-sub.Context().Done():
				// Subscriber disconnected, remove it
				b.Unsubscribe(sub)
				return
			default:
			}

			// Send event to subscriber
			if err := sub.Send(event); err != nil {
				atomic.AddInt64(&errorCount, 1)
				b.logger.Warn("Failed to send event to subscriber",
					"subscriber_id", sub.ID(),
					"event_type", event.Type,
					"error", err,
				)
				// Remove failed subscriber
				b.Unsubscribe(sub)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(subscriber)
	}

	wg.Wait()

	duration := time.Since(start)

	// Record metrics
	if b.metrics != nil {
		b.metrics.EventsTotal.WithLabelValues(event.Type, event.Source).Inc()
		b.metrics.EventLatencySeconds.Observe(duration.Seconds())
		b.metrics.BroadcastDuration.Observe(duration.Seconds())
	}

	b.logger.Debug("Event broadcast complete",
		"event_type", event.Type,
		"event_id", event.ID,
		"success", successCount,
		"errors", errorCount,
		"duration_ms", duration.Milliseconds(),
	)
}
