package silencing

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// setupTestSilenceEventBus creates a RedisSilenceEventBus backed by
// miniredis, mirroring redis_notify_log_test.go's setupTestRedisNotifyLog.
func setupTestSilenceEventBus(t *testing.T) (*RedisSilenceEventBus, *miniredis.Miniredis, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)

	bus, err := NewRedisSilenceEventBus(context.Background(), &SilenceEventBusConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	cleanup := func() {
		_ = redisCache.Close()
		mr.Close()
	}

	return bus, mr, cleanup
}

// newSecondReplicaSilenceEventBus builds a SECOND RedisSilenceEventBus
// sharing the same miniredis backend, simulating a second HA replica
// process that only shares state via Redis.
func newSecondReplicaSilenceEventBus(t *testing.T, mr *miniredis.Miniredis) (*RedisSilenceEventBus, func()) {
	t.Helper()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)

	bus, err := NewRedisSilenceEventBus(context.Background(), &SilenceEventBusConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	return bus, func() { _ = redisCache.Close() }
}

func TestNewRedisSilenceEventBus_NilConfigOrClientRejected(t *testing.T) {
	if _, err := NewRedisSilenceEventBus(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	if _, err := NewRedisSilenceEventBus(context.Background(), &SilenceEventBusConfig{}); err == nil {
		t.Fatal("expected error for nil client")
	}
}

// subscribeRecorder captures onResync/onEvent calls from Subscribe in a
// goroutine-safe way for assertions from the test's main goroutine.
type subscribeRecorder struct {
	mu        sync.Mutex
	resyncs   int
	events    []SilenceEvent
	firstBeat chan struct{} // closed once, on the first onResync
}

func newSubscribeRecorder() *subscribeRecorder {
	return &subscribeRecorder{firstBeat: make(chan struct{})}
}

func (r *subscribeRecorder) onResync(context.Context) {
	r.mu.Lock()
	r.resyncs++
	first := r.resyncs == 1
	r.mu.Unlock()
	if first {
		close(r.firstBeat)
	}
}

func (r *subscribeRecorder) onEvent(_ context.Context, event SilenceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *subscribeRecorder) eventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *subscribeRecorder) resyncCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resyncs
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func TestRedisSilenceEventBus_Subscribe_TriggersResyncOnConnect(t *testing.T) {
	bus, _, cleanup := setupTestSilenceEventBus(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := newSubscribeRecorder()
	done := make(chan error, 1)
	go func() {
		done <- bus.Subscribe(ctx, rec.onResync, rec.onEvent)
	}()

	select {
	case <-rec.firstBeat:
		// onResync fired on initial subscribe, before any event — this is
		// what lets a fresh subscriber catch up on its own without waiting
		// for the next published event.
	case <-time.After(2 * time.Second):
		t.Fatal("onResync was not called on initial subscribe")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Subscribe() returned error on context cancel = %v, want nil", err)
	}
}

func TestRedisSilenceEventBus_PublishSubscribe_CrossReplicaDelivery(t *testing.T) {
	// Two bus instances sharing one miniredis backend, simulating two HA
	// replicas (task 6.3): replica A publishes, replica B's subscriber must
	// receive it without any shared Go state between the two.
	busA, mr, cleanup := setupTestSilenceEventBus(t)
	defer cleanup()
	busB, cleanupB := newSecondReplicaSilenceEventBus(t, mr)
	defer cleanupB()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := newSubscribeRecorder()
	done := make(chan error, 1)
	go func() {
		done <- busB.Subscribe(ctx, rec.onResync, rec.onEvent)
	}()

	select {
	case <-rec.firstBeat:
	case <-time.After(2 * time.Second):
		t.Fatal("replica B never finished subscribing")
	}

	want := SilenceEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Op: SilenceEventUpsert}
	if err := busA.Publish(ctx, want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return rec.eventCount() == 1 })

	rec.mu.Lock()
	got := rec.events[0]
	rec.mu.Unlock()
	if got != want {
		t.Fatalf("replica B received %+v, want %+v", got, want)
	}

	cancel()
	<-done
}

func TestRedisSilenceEventBus_Subscribe_MalformedMessageIsSkippedNotFatal(t *testing.T) {
	bus, _, cleanup := setupTestSilenceEventBus(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := newSubscribeRecorder()
	done := make(chan error, 1)
	go func() {
		done <- bus.Subscribe(ctx, rec.onResync, rec.onEvent)
	}()

	select {
	case <-rec.firstBeat:
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe never completed")
	}

	// Publish garbage directly (bypassing Publish's JSON encoding) — this
	// must not kill the subscription.
	if err := bus.client.Publish(ctx, silenceEventsChannel, "not-json").Err(); err != nil {
		t.Fatalf("raw publish error = %v", err)
	}

	// A valid event published right after must still be delivered.
	want := SilenceEvent{ID: "660e8400-e29b-41d4-a716-446655440001", Op: SilenceEventDelete}
	if err := bus.Publish(ctx, want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return rec.eventCount() == 1 })

	rec.mu.Lock()
	got := rec.events[0]
	rec.mu.Unlock()
	if got != want {
		t.Fatalf("received %+v, want %+v (malformed message should be skipped, not fatal)", got, want)
	}

	cancel()
	<-done
}

func TestRedisSilenceEventBus_Subscribe_ResubscribeTriggersResyncAgain(t *testing.T) {
	// A fresh Subscribe() call (simulating the caller's retry loop after a
	// dropped connection — see ServiceRegistry.runSilenceSubscribeLoop) must
	// fire onResync again, since any number of events may have been missed
	// during the gap.
	bus, _, cleanup := setupTestSilenceEventBus(t)
	defer cleanup()

	rec := newSubscribeRecorder()

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { done1 <- bus.Subscribe(ctx1, rec.onResync, rec.onEvent) }()
	select {
	case <-rec.firstBeat:
	case <-time.After(2 * time.Second):
		t.Fatal("first subscribe never completed")
	}
	cancel1()
	<-done1

	if got := rec.resyncCount(); got != 1 {
		t.Fatalf("resync count after first session = %d, want 1", got)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan error, 1)
	go func() { done2 <- bus.Subscribe(ctx2, rec.onResync, rec.onEvent) }()

	waitFor(t, 2*time.Second, func() bool { return rec.resyncCount() == 2 })

	cancel2()
	<-done2
}
