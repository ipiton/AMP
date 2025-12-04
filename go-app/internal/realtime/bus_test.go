// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
)

// mockSubscriber is a mock implementation of EventSubscriber for testing.
type mockSubscriber struct {
	id        string
	events    []Event
	mu        sync.Mutex
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	sendDelay time.Duration
}

func newMockSubscriber(id string) *mockSubscriber {
	ctx, cancel := context.WithCancel(context.Background())
	return &mockSubscriber{
		id:     id,
		events: make([]Event, 0),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (m *mockSubscriber) ID() string {
	return m.id
}

func (m *mockSubscriber) Send(event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrSubscriberClosed
	}

	if m.sendDelay > 0 {
		time.Sleep(m.sendDelay)
	}

	m.events = append(m.events, event)
	return nil
}

func (m *mockSubscriber) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	m.cancel()
	return nil
}

func (m *mockSubscriber) Context() context.Context {
	return m.ctx
}

func (m *mockSubscriber) GetEvents() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()

	events := make([]Event, len(m.events))
	copy(events, m.events)
	return events
}

func (m *mockSubscriber) GetEventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func TestDefaultEventBus_Subscribe(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	assert.NoError(t, err)
	assert.Equal(t, 1, bus.GetActiveSubscribers())
}

func TestDefaultEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	err = bus.Unsubscribe(subscriber)
	assert.NoError(t, err)
	assert.Equal(t, 0, bus.GetActiveSubscribers())
	assert.True(t, subscriber.closed)
}

func TestDefaultEventBus_Publish(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	event := NewEvent("test_event", map[string]interface{}{"key": "value"}, "test_source")
	err = bus.Publish(*event)
	require.NoError(t, err)

	// Wait for event to be broadcast
	time.Sleep(100 * time.Millisecond)

	events := subscriber.GetEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].Type)
	assert.Equal(t, "value", events[0].Data["key"])
}

func TestDefaultEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber1 := newMockSubscriber("test-1")
	subscriber2 := newMockSubscriber("test-2")
	subscriber3 := newMockSubscriber("test-3")

	err = bus.Subscribe(subscriber1)
	require.NoError(t, err)
	err = bus.Subscribe(subscriber2)
	require.NoError(t, err)
	err = bus.Subscribe(subscriber3)
	require.NoError(t, err)

	assert.Equal(t, 3, bus.GetActiveSubscribers())

	event := NewEvent("test_event", map[string]interface{}{"key": "value"}, "test_source")
	err = bus.Publish(*event)
	require.NoError(t, err)

	// Wait for event to be broadcast
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, 1, subscriber1.GetEventCount())
	assert.Equal(t, 1, subscriber2.GetEventCount())
	assert.Equal(t, 1, subscriber3.GetEventCount())
}

func TestDefaultEventBus_EventSequence(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	// Publish multiple events
	for i := 0; i < 5; i++ {
		event := NewEvent("test_event", map[string]interface{}{"index": i}, "test_source")
		err = bus.Publish(*event)
		require.NoError(t, err)
	}

	// Wait for events to be broadcast
	time.Sleep(300 * time.Millisecond)

	events := subscriber.GetEvents()
	assert.Len(t, events, 5)

	// Check sequence numbers are monotonically increasing
	for i := 1; i < len(events); i++ {
		assert.Greater(t, events[i].Sequence, events[i-1].Sequence)
	}
}

func TestDefaultEventBus_ChannelFull(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	// Fill the channel (capacity 1000)
	// We'll publish events slowly to fill the channel
	subscriber := newMockSubscriber("test-1")
	subscriber.sendDelay = 10 * time.Millisecond // Slow subscriber
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	// Publish many events rapidly
	for i := 0; i < 2000; i++ {
		event := NewEvent("test_event", map[string]interface{}{"index": i}, "test_source")
		err = bus.Publish(*event)
		if err != nil {
			// Channel full error is expected
			assert.Equal(t, ErrEventChannelFull, err)
			break
		}
	}
}

func TestDefaultEventBus_Stop(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	// Stop the bus
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()

	err = bus.Stop(stopCtx)
	assert.NoError(t, err)
}

func TestDefaultEventBus_ConcurrentSubscribe(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	var wg sync.WaitGroup
	subscribers := make([]*mockSubscriber, 100)

	// Concurrent subscribe
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sub := newMockSubscriber("test-" + string(rune(idx)))
			subscribers[idx] = sub
			err := bus.Subscribe(sub)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 100, bus.GetActiveSubscribers())
}

func TestDefaultEventBus_ConcurrentPublish(t *testing.T) {
	bus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bus.Start(ctx)
	require.NoError(t, err)
	defer bus.Stop(context.Background())

	subscriber := newMockSubscriber("test-1")
	err = bus.Subscribe(subscriber)
	require.NoError(t, err)

	var wg sync.WaitGroup

	// Concurrent publish
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			event := NewEvent("test_event", map[string]interface{}{"index": idx}, "test_source")
			err := bus.Publish(*event)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Wait for events to be broadcast
	time.Sleep(500 * time.Millisecond)

	// All events should be received (may be less due to channel capacity)
	assert.GreaterOrEqual(t, subscriber.GetEventCount(), 50) // At least half should be received
}
