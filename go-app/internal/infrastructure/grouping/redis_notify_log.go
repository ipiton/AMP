// Package grouping: RedisNotifyLog is the Redis-backed cross-replica
// notification log (task 6.1, alertmanager-parity Phase 6 — HA clustering
// without gossip). It implements GroupNotifyLog (see manager.go for the
// interface contract) so DefaultGroupManager's notify-stage chain (task 2.4
// Step 4: Dedup) works identically whether backed by the in-memory
// notifyDedupLog (dedup.go, lite profile / fallback) or this Redis
// implementation (standard profile).
//
// What it replaces: task 2.4's notifyDedupLog is single-process — its
// per-GroupKey publishLocks (publish_lock.go) prevent a double-send between
// two goroutines in the SAME process, but do nothing for two separate
// replica processes both handling the same group (the normal case in an HA
// deployment: identical alerts routed to N replicas, each running its own
// group_wait/group_interval/repeat_interval timers). RedisNotifyLog closes
// that gap using two Redis key families:
//
//   - "nflog:entry:{groupKey}" — the confirmed-sent record (JSON:
//     signature, sent_at, receiver), written by RecordSent AFTER a
//     confirmed successful publish, with TTL = repeat_interval (+ a grace
//     period). This is the actual dedup state IsDuplicate reads.
//   - "nflog:claim:{groupKey}" — a short-lived (claimTTL, seconds — see
//     TryClaim) SET-NX marker that arbitrates which replica is currently
//     allowed to run the check-publish-record sequence for this group.
//
// Cross-replica publish protocol (see DefaultGroupManager.publishGroupAlerts
// for the call site):
//
//  1. TryClaim: SET NX PX claimTTL a random claim ID. Only one replica can
//     win this for a given groupKey at a time. Losing replicas skip this
//     fire entirely — no error, no publish — and rely on their own
//     already-scheduled group_interval/repeat_interval timer to retry
//     later, by which point the winning replica's RecordSent (if it
//     published) will make IsDuplicate suppress the retry anyway.
//  2. The claim winner calls IsDuplicate against "nflog:entry:{groupKey}".
//  3. If not a duplicate: run the rest of the chain and publish.
//  4. On confirmed successful publish: RecordSent (sets the entry with TTL
//     = repeat_interval + grace).
//  5. Always release the claim (the closure returned by TryClaim) once the
//     sequence finishes, success or failure — do NOT hold it for claimTTL.
//     A crashed replica that never calls release simply lets the claim
//     expire after claimTTL (seconds), which is why claimTTL must be short:
//     a stuck claim would otherwise block every replica's retries for that
//     long.
//
// This is deliberately NOT a general-purpose distributed lock (task 6.2,
// full distributed timer ownership, is the follow-up that would let only
// ONE replica run a group's timers at all) — it only makes concurrent
// publish attempts for the same group safe, by ensuring at most one
// replica's publish call is in flight for a given group at a time and that
// its result is visible to every replica afterward via the entry key.
//
// Failure posture: any Redis error (network, timeout, ACL) is returned to
// the caller rather than swallowed. DefaultGroupManager treats a non-nil
// error from IsDuplicate/TryClaim as fail-open — proceed as if not a
// duplicate / claim acquired — matching the chain's existing Inhibit/
// Silence fail-open posture (see publishGroupAlerts). This accepts a
// duplicate-notification risk across replicas while Redis is down, which is
// the documented trade-off for task 6.1 (favors delivery over strict dedup
// during an outage).
//
// TN-6.1: Notification Log (Redis Backend)
package grouping

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Redis key prefixes and TTL defaults for the notification log (task 6.1).
const (
	// notifyLogEntryKeyPrefix stores the confirmed-sent record.
	// Format: "nflog:entry:{groupKey}" → JSON-serialized notifyLogEntry.
	notifyLogEntryKeyPrefix = "nflog:entry:"

	// notifyLogClaimKeyPrefix stores the short-lived cross-replica publish
	// claim. Format: "nflog:claim:{groupKey}" → random claim ID string.
	notifyLogClaimKeyPrefix = "nflog:claim:"

	// notifyLogEntryTTLFallback is used for the entry TTL only when
	// RecordSent is called with repeatInterval <= 0 (defensive; callers are
	// expected to always pass the group's effective repeat_interval).
	// Matches Route.GetEffectiveRepeatInterval's own upstream-compatible
	// default (4h).
	notifyLogEntryTTLFallback = 4 * time.Hour

	// notifyLogEntryTTLGracePeriod mirrors groupTTLGracePeriod (storage.go):
	// extra time beyond repeat_interval before the entry expires, so a
	// slightly-late retry (e.g. a stalled replica) still sees it.
	notifyLogEntryTTLGracePeriod = 60 * time.Second
)

// notifyLogEntry is the JSON payload stored at "nflog:entry:{groupKey}".
type notifyLogEntry struct {
	// Signature is alertSetSignature's output for the alert set that was
	// sent — see IsDuplicate's signature-comparison semantics.
	Signature string `json:"signature"`

	// SentAt is when the publish was confirmed successful.
	SentAt time.Time `json:"sent_at"`

	// Receiver is informational only (derived from the already
	// receiver-scoped GroupKey via receiverFromGroupKey) — not used for any
	// dedup decision, kept for observability/debugging entries directly in
	// Redis.
	Receiver string `json:"receiver"`
}

// RedisNotifyLog is the Redis-backed GroupNotifyLog implementation (task
// 6.1). See the package-level doc comment above for the full protocol.
//
// Thread-safety: safe for concurrent use — all state lives in Redis, and
// every operation is a single atomic Redis command (GET/SET/SETNX/DEL) or a
// Lua script (claim release).
type RedisNotifyLog struct {
	client *redis.Client
	logger *slog.Logger
}

// RedisNotifyLogConfig holds configuration for RedisNotifyLog.
type RedisNotifyLogConfig struct {
	// Client is the Redis client (obtained from cache.RedisCache.GetClient(),
	// same client reused by RedisGroupStorage/RedisTimerStorage — task 2.2's
	// pattern).
	Client *redis.Client

	// Logger for structured logging (optional, defaults to slog.Default).
	Logger *slog.Logger
}

// NewRedisNotifyLog creates a new Redis-backed notification log.
//
// Verifies Redis connectivity (Ping) at construction time, mirroring
// NewRedisGroupStorage/NewRedisTimerStorage, so a dead Redis is caught at
// grouping-init time rather than at the first publish.
func NewRedisNotifyLog(ctx context.Context, cfg *RedisNotifyLogConfig) (*RedisNotifyLog, error) {
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

	l := &RedisNotifyLog{client: cfg.Client, logger: logger}

	if err := l.client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connectivity check failed: %w", err)
	}

	logger.Info("Initialized Redis notification log (nflog)")
	return l, nil
}

// IsDuplicate implements GroupNotifyLog. See its doc comment for semantics.
func (l *RedisNotifyLog) IsDuplicate(ctx context.Context, groupKey GroupKey, signature string, ttl time.Time) (bool, error) {
	data, err := l.client.Get(ctx, notifyLogEntryKeyPrefix+string(groupKey)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, fmt.Errorf("nflog get %s: %w", groupKey, err)
	}

	var entry notifyLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return false, fmt.Errorf("nflog unmarshal %s: %w", groupKey, err)
	}

	if entry.Signature != signature {
		// Alert set changed since the last send — never a duplicate,
		// matches notifyDedupLog/upstream nflog semantics.
		return false, nil
	}
	return entry.SentAt.After(ttl), nil
}

// RecordSent implements GroupNotifyLog. See its doc comment for semantics.
func (l *RedisNotifyLog) RecordSent(ctx context.Context, groupKey GroupKey, signature string, now time.Time, repeatInterval time.Duration) error {
	receiver := receiverFromGroupKey(groupKey)
	entry := notifyLogEntry{Signature: signature, SentAt: now, Receiver: receiver}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("nflog marshal %s: %w", groupKey, err)
	}

	entryTTL := repeatInterval
	if entryTTL <= 0 {
		entryTTL = notifyLogEntryTTLFallback
	}
	entryTTL += notifyLogEntryTTLGracePeriod

	if err := l.client.Set(ctx, notifyLogEntryKeyPrefix+string(groupKey), data, entryTTL).Err(); err != nil {
		return fmt.Errorf("nflog set %s: %w", groupKey, err)
	}

	l.logger.Debug("Recorded nflog entry",
		"group_key", groupKey,
		"receiver", receiver,
		"ttl", entryTTL)
	return nil
}

// Forget implements GroupNotifyLog: removes both the entry and any
// lingering claim for groupKey.
func (l *RedisNotifyLog) Forget(ctx context.Context, groupKey GroupKey) error {
	if err := l.client.Del(ctx,
		notifyLogEntryKeyPrefix+string(groupKey),
		notifyLogClaimKeyPrefix+string(groupKey),
	).Err(); err != nil {
		return fmt.Errorf("nflog del %s: %w", groupKey, err)
	}
	return nil
}

// TryClaim implements GroupNotifyLog's cross-replica publish claim.
//
// Uses SET NX PX for atomic acquire (only one replica can win for a given
// groupKey) and a Lua check-and-delete script for release, guarded by a
// random claim ID so a slow/stalled replica can never release a claim a
// different replica has since (re-)acquired after the original one
// expired — the same pattern RedisTimerStorage.AcquireLock already uses for
// its distributed lock (redis_timer_storage.go).
func (l *RedisNotifyLog) TryClaim(ctx context.Context, groupKey GroupKey, claimTTL time.Duration) (bool, func() error, error) {
	claimKey := notifyLogClaimKeyPrefix + string(groupKey)
	claimID := uuid.New().String()

	acquired, err := l.client.SetNX(ctx, claimKey, claimID, claimTTL).Result()
	if err != nil {
		return false, noopRelease, fmt.Errorf("nflog claim setnx %s: %w", groupKey, err)
	}
	if !acquired {
		l.logger.Debug("nflog publish claim held by another replica", "group_key", groupKey)
		return false, noopRelease, nil
	}

	release := func() error {
		script := `
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			else
				return 0
			end
		`
		if _, err := l.client.Eval(ctx, script, []string{claimKey}, claimID).Result(); err != nil {
			return fmt.Errorf("nflog claim release %s: %w", groupKey, err)
		}
		return nil
	}

	l.logger.Debug("Acquired nflog publish claim", "group_key", groupKey, "ttl", claimTTL)
	return true, release, nil
}

// Ping checks Redis connectivity, mirroring RedisGroupStorage.Ping (used by
// health checks / future StorageManager-style fallback coordination).
func (l *RedisNotifyLog) Ping(ctx context.Context) error {
	return l.client.Ping(ctx).Err()
}
