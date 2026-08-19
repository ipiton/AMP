package grouping

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// notifyDedupLog is a minimal in-memory notification-log (task 2.4, notify-
// stage chain Step 3: Dedup), implementing GroupNotifyLog. It answers the
// same question upstream Alertmanager's nflog answers: "did we already
// send a notification for this exact alert set, for this group+receiver+
// target, within repeat_interval?"
//
// Deliberately minimal: keyed by (GroupKey, target) — GroupKey alone is
// already receiver-scoped (see AlertProcessor.groupKeyFor's
// "receiver=<name>/..." prefix from task 2.3); target adds the per-target
// granularity task fwb introduces (mirrors upstream nflog's
// group:receiver:integration key) — storing only the last-sent alert-set
// signature and timestamp per (group, target) pair. This is NOT the
// upstream nflog's Redis-backed, gossip-replicated notification log — it is
// a single-process, best-effort substitute. A restart loses all entries (a
// restart can therefore cause one duplicate notification per active
// group/target — acceptable for this slice).
//
// Used by the lite profile (always) and by the standard profile as the
// fallback when Redis is unavailable at grouping-init time. Its TryClaim is
// a no-op (always succeeds) because DefaultGroupManager's own per-GroupKey
// publishLocks already fully serialize same-process callers — see
// GroupNotifyLog's doc comment. The cross-replica, Redis-backed
// implementation is RedisNotifyLog (redis_notify_log.go, task 6.1).
//
// Thread-safe via mu.
type notifyDedupLog struct {
	mu      sync.Mutex
	entries map[dedupKey]dedupEntry
}

// dedupKey scopes a dedup entry to one (group, target) pair (task fwb).
type dedupKey struct {
	groupKey GroupKey
	target   string
}

type dedupEntry struct {
	signature string
	sentAt    time.Time
}

func newNotifyDedupLog() *notifyDedupLog {
	return &notifyDedupLog{entries: make(map[dedupKey]dedupEntry)}
}

// IsDuplicate reports whether a notification for (groupKey, target) carrying
// exactly this alert set was already sent within ttl (the group's effective
// repeat_interval). It does NOT record anything — call RecordSent after a
// successful publish. Implements GroupNotifyLog; ctx is unused (in-memory,
// never blocks), and the error return is always nil.
func (l *notifyDedupLog) IsDuplicate(_ context.Context, groupKey GroupKey, target string, signature string, ttl time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[dedupKey{groupKey: groupKey, target: target}]
	if !ok {
		return false, nil
	}
	if entry.signature != signature {
		// Alert set changed since the last send (new alert, one resolved,
		// etc.) — never a duplicate, matches upstream nflog semantics.
		return false, nil
	}
	return entry.sentAt.After(ttl), nil
}

// RecordSent records that a notification carrying signature for
// (groupKey, target) was just sent successfully, at now. Implements
// GroupNotifyLog; repeatInterval is ignored (see GroupNotifyLog's doc
// comment for why).
func (l *notifyDedupLog) RecordSent(_ context.Context, groupKey GroupKey, target string, signature string, now time.Time, _ time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[dedupKey{groupKey: groupKey, target: target}] = dedupEntry{signature: signature, sentAt: now}
	return nil
}

// Forget removes every dedup entry (for every target) belonging to
// groupKey. Called when a group is deleted (emptied) so the dedup log
// doesn't grow unbounded independent of active groups. Implements
// GroupNotifyLog.
func (l *notifyDedupLog) Forget(_ context.Context, groupKey GroupKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.entries {
		if k.groupKey == groupKey {
			delete(l.entries, k)
		}
	}
	return nil
}

// TryClaim implements GroupNotifyLog's cross-replica publish claim as a
// no-op that always succeeds: single-process/lite deployments have no
// other replica to race against, and DefaultGroupManager's own per-GroupKey
// publishLocks already fully serialize same-process callers (see
// GroupNotifyLog's doc comment).
func (l *notifyDedupLog) TryClaim(_ context.Context, _ GroupKey, _ time.Duration) (bool, func() error, error) {
	return true, noopRelease, nil
}

// noopRelease is the shared no-op release func returned by TryClaim
// implementations that need no actual release step (notifyDedupLog; also
// returned by RedisNotifyLog.TryClaim when acquired == false).
func noopRelease() error { return nil }

// alertSetSignature computes a deterministic signature for alerts: sorted
// core.Alert.DeliveryKey values ("fingerprint:status") joined by "|".
// Order-independent (a group's alerts map iteration order is not stable) and
// status-sensitive (an alert flipping firing<->resolved changes the signature,
// so it is never treated as a duplicate of the prior send — matching upstream
// nflog, where a changed alert set always triggers a fresh notification).
//
// The per-element format is core.Alert.DeliveryKey and NOT an inline
// concatenation (task fu4): the per-(group, target) delivered set that
// tracks which individual alerts a non-batch target accepted is keyed by
// exactly the same atoms, so the two must never drift apart. See
// DeliveryKey's doc comment.
func alertSetSignature(alerts []*core.Alert) string {
	parts := make([]string, 0, len(alerts))
	for _, a := range alerts {
		parts = append(parts, a.DeliveryKey())
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
