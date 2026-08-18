// Package silencing: RedisSilenceEventBus closes the cross-replica cache
// staleness gap for silences (task 6.3, alertmanager-parity Phase 6 — HA
// clustering without gossip).
//
// The gap: silence writes (POST/DELETE /api/v2/silence*) are DB-first —
// PostgresSilenceRepository is the shared source of truth — but the actual
// read path for alert filtering is memory.SilenceStore, a per-process cache
// mirrored ONLY on the replica that handled the write (see
// internal/application/handlers/silences.go: persistSilenceDBFirst,
// handleSilenceDelete). In an HA deployment behind a load balancer, a
// silence created on replica A is invisible to replica B's IsAlertSilenced
// checks until B restarts. Unlike task 6.1's notification log (which had a
// periodic sync worker to fall back on, even if slow) and unlike alert
// ingestion (which upstream Alertmanager, and this port, rely on Prometheus
// fanning writes out to every replica directly — no gossip needed there),
// nothing periodically reconciles memory.SilenceStore across replicas today.
//
// RedisSilenceEventBus provides a minimal pub/sub primitive over one Redis
// channel ("amp:silence:events") so the writing replica can announce "silence
// X changed" and every other replica's subscriber can react in near
// real-time (typically well under a second) instead of waiting for a
// restart. See internal/application/service_registry.go
// (initializeSilenceEventSync, applySilenceEvent, resyncSilenceStore) for the
// subscriber side that turns these events into memory.SilenceStore
// mutations, and internal/application/handlers/silences.go for the publish
// call sites.
//
// Design notes:
//   - Events carry only {id, op} — never the full silence payload. The
//     receiving replica re-fetches the current row from
//     infrasilencing.SilenceRepository by ID before mirroring it, so the
//     database (not the wire message) stays the single source of truth and
//     stale/out-of-order pub/sub deliveries self-correct on the next fetch.
//   - Full resync (not just the missed event) is triggered on every
//     (re)subscribe — see Subscribe's onResync callback — because a dropped
//     Redis connection has an unknown-length blind spot: any number of
//     events, for any number of silences, may have been published while
//     disconnected. A periodic fallback resync (independent of pub/sub
//     health) is the caller's responsibility — see
//     ServiceRegistry.runSilencePeriodicResync — as a backstop against a
//     Publish call that itself failed silently on the writer's side (e.g. a
//     transient network blip that didn't surface as an HTTP error, per
//     persistSilenceDBFirst's "cache failure here must not fail the
//     request" posture applied identically to the publish step).
//
// Failure posture: mirrors RedisNotifyLog (redis_notify_log.go) — Publish
// errors are returned to the caller, who treats them as best-effort/
// non-fatal (the HTTP write already committed to the database); Subscribe
// errors are returned to the caller's retry loop, which resubscribes with a
// backoff and always triggers onResync again once reconnected.
//
// TN-6.3: Silence/alert-state sync
package silencing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// silenceEventsChannel is the single Redis pub/sub channel used for
// cross-replica silence cache invalidation.
const silenceEventsChannel = "amp:silence:events"

// SilenceEventOp identifies what kind of write produced a SilenceEvent.
type SilenceEventOp string

const (
	// SilenceEventUpsert announces that a silence was created or updated.
	// The subscriber re-fetches the row by ID rather than trusting a
	// payload on the wire (see package doc).
	SilenceEventUpsert SilenceEventOp = "upsert"

	// SilenceEventDelete announces that a silence was deleted (or, in the
	// "stale cache entry evicted" 404 path, that it no longer exists).
	SilenceEventDelete SilenceEventOp = "delete"
)

// SilenceEvent is the JSON payload published on silenceEventsChannel.
type SilenceEvent struct {
	ID string         `json:"id"`
	Op SilenceEventOp `json:"op"`
}

// SilenceEventPublisher publishes cross-replica invalidation events after a
// silence write commits to the database. Implemented by
// RedisSilenceEventBus in the standard profile; the HTTP handlers treat a
// nil publisher as a no-op (lite profile, or a standard-profile deployment
// without a live Redis cache backend — see
// ServiceRegistry.newSilenceEventBus).
type SilenceEventPublisher interface {
	Publish(ctx context.Context, event SilenceEvent) error
}

// RedisSilenceEventBus is the Redis-backed pub/sub implementation of
// SilenceEventPublisher, plus the subscriber-side Subscribe method. See the
// package doc comment for the full protocol.
//
// Thread-safety: Publish is safe for concurrent use (stateless, one Redis
// command per call). Subscribe owns its own *redis.PubSub for the duration
// of one call and is intended to be run from a single dedicated goroutine
// per process (see ServiceRegistry.runSilenceSubscribeLoop) — concurrent
// Subscribe calls each get their own independent subscription, which works
// but is not the intended usage.
type RedisSilenceEventBus struct {
	client *redis.Client
	logger *slog.Logger
}

// SilenceEventBusConfig holds configuration for RedisSilenceEventBus.
type SilenceEventBusConfig struct {
	// Client is the Redis client (obtained from cache.RedisCache.GetClient(),
	// the same client reused by RedisGroupStorage/RedisTimerStorage/
	// RedisNotifyLog — task 2.2/6.1's pattern).
	Client *redis.Client

	// Logger for structured logging (optional, defaults to slog.Default).
	Logger *slog.Logger
}

// NewRedisSilenceEventBus creates a new Redis-backed silence event bus.
//
// Verifies Redis connectivity (Ping) at construction time, mirroring
// NewRedisNotifyLog/NewRedisGroupStorage, so a dead Redis is caught at
// silence-sync-init time rather than at the first publish or subscribe.
func NewRedisSilenceEventBus(ctx context.Context, cfg *SilenceEventBusConfig) (*RedisSilenceEventBus, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("redis client cannot be nil")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	b := &RedisSilenceEventBus{client: cfg.Client, logger: logger}

	if err := b.client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connectivity check failed: %w", err)
	}

	logger.Info("Initialized Redis silence event bus (cross-replica cache invalidation)")
	return b, nil
}

// Publish implements SilenceEventPublisher: announces a silence change on
// silenceEventsChannel. Any subscriber (including, harmlessly, the
// publishing replica itself if it also runs a subscriber) will receive it.
func (b *RedisSilenceEventBus) Publish(ctx context.Context, event SilenceEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("silence event marshal %s: %w", event.ID, err)
	}

	if err := b.client.Publish(ctx, silenceEventsChannel, data).Err(); err != nil {
		return fmt.Errorf("silence event publish %s: %w", event.ID, err)
	}

	b.logger.Debug("Published silence event", "silence_id", event.ID, "op", event.Op)
	return nil
}

// Subscribe opens one subscription session on silenceEventsChannel and
// blocks, dispatching to onResync/onEvent, until ctx is cancelled or the
// underlying Redis connection fails unrecoverably.
//
// onResync is called every time a subscription is (successfully)
// established — including the very first one — because the Redis reply to
// SUBSCRIBE arrives as a *redis.Subscription message before any published
// event can. This is deliberately used as the "do a full resync" signal
// (see the caller, ServiceRegistry.runSilenceSubscribeLoop): a fresh
// subscription has an unknown-length blind spot behind it (this is either
// the initial subscribe, where the local cache may already be stale from
// before the process started subscribing, or a resubscribe after a dropped
// connection, where any number of events may have been missed), so a
// message-by-message catch-up is not possible — only a full re-list from
// the database closes the gap. onResync is called synchronously and
// SILENCE_EVENT messages are not read until it returns, so a real event
// published immediately after a resubscribe is never lost, only possibly
// redundant with what the resync already fetched.
//
// onEvent is called for every subsequently received *redis.Message, decoded
// as a SilenceEvent. A message that fails to decode is logged and skipped
// (does not terminate the subscription) — malformed data on this channel
// can only come from a future/incompatible version of this same process,
// never from user input.
//
// Returns nil if ctx was cancelled (the normal shutdown path) or a non-nil
// error if the Redis connection failed. Callers are expected to loop:
// resubscribing (with backoff) on a non-nil error, which naturally triggers
// onResync again once reconnected.
func (b *RedisSilenceEventBus) Subscribe(
	ctx context.Context,
	onResync func(context.Context),
	onEvent func(context.Context, SilenceEvent),
) error {
	pubsub := b.client.Subscribe(ctx, silenceEventsChannel)
	defer func() { _ = pubsub.Close() }()

	// pubsub.Receive(ctx) below does NOT unblock on ctx cancellation alone:
	// go-redis only turns ctx into a socket read deadline when ctx.Deadline()
	// is set (see redis/internal/pool.Conn.deadline) — a plain
	// context.WithCancel (what every caller here uses) has no Deadline, so
	// the read gets no deadline at all and blocks until a message arrives.
	// Closing the PubSub connection is what actually interrupts a pending
	// Receive, so a watcher goroutine does that as soon as ctx is done.
	watcherStopped := make(chan struct{})
	defer close(watcherStopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = pubsub.Close()
		case <-watcherStopped:
		}
	}()

	for {
		msg, err := pubsub.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("silence event subscribe receive: %w", err)
		}

		switch m := msg.(type) {
		case *redis.Subscription:
			b.logger.Info("Silence event subscription (re)established, triggering full resync",
				"channel", m.Channel)
			if onResync != nil {
				onResync(ctx)
			}

		case *redis.Message:
			var event SilenceEvent
			if err := json.Unmarshal([]byte(m.Payload), &event); err != nil {
				b.logger.Warn("Silence event decode failed, skipping message",
					"error", err, "payload", m.Payload)
				continue
			}
			if onEvent != nil {
				onEvent(ctx, event)
			}

		case *redis.Pong:
			// Health-check reply only, no action needed.
		}
	}
}

// Ping checks Redis connectivity, mirroring RedisNotifyLog.Ping.
func (b *RedisSilenceEventBus) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}
