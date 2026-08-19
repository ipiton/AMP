package grouping

import (
	"context"
	"fmt"
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

	// delivered holds the per-(group, target) PARTIAL delivery state (task
	// fu4): which individual alerts a non-batch target accepted while the
	// group as a whole stayed unconfirmed. Guarded by mu, same as entries.
	//
	// No TTL, unlike the Redis implementation's set: this log is already
	// process-lifetime state (a restart loses everything, see the type's doc
	// comment), and the freshness question is answered by the entries map's
	// caller-supplied cutoff. Bounded by deletion — RecordSent drops a
	// target's set, Forget drops the whole group's — plus the same
	// maxDeliveredAlertsPerTarget cap the Redis side applies, so a
	// pathological group cannot grow it without bound either.
	delivered map[dedupKey]map[string]struct{}
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
	return &notifyDedupLog{
		entries:   make(map[dedupKey]dedupEntry),
		delivered: make(map[dedupKey]map[string]struct{}),
	}
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
//
// Also drops this target's partial delivered set (task fu4): a full entry
// covering the whole alert set supersedes any per-alert progress toward it.
func (l *notifyDedupLog) RecordSent(_ context.Context, groupKey GroupKey, target string, signature string, now time.Time, _ time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := dedupKey{groupKey: groupKey, target: target}
	l.entries[key] = dedupEntry{signature: signature, sentAt: now}
	delete(l.delivered, key)
	return nil
}

// DeliveredAlerts implements GroupNotifyLog (task fu4): the delivery keys of
// the alerts target accepted while the group stayed unconfirmed. ctx is unused
// (in-memory) and the error is always nil.
func (l *notifyDedupLog) DeliveredAlerts(_ context.Context, groupKey GroupKey, target string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	set := l.delivered[dedupKey{groupKey: groupKey, target: target}]
	if len(set) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out, nil
}

// RecordPartialDelivery implements GroupNotifyLog (task fu4): additively
// records the alerts target accepted during an otherwise unconfirmed fire.
// Capped at maxDeliveredAlertsPerTarget for the same reason the Redis
// implementation caps it — see that constant.
func (l *notifyDedupLog) RecordPartialDelivery(_ context.Context, groupKey GroupKey, target string, deliveryKeys []string, _ time.Duration) error {
	if len(deliveryKeys) == 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	key := dedupKey{groupKey: groupKey, target: target}
	set := l.delivered[key]

	// Checked up front, so the whole batch is either recorded or refused —
	// same all-or-nothing shape as RedisNotifyLog's SCARD pre-check, so the two
	// implementations cannot disagree about what happens at the boundary.
	if len(set)+len(deliveryKeys) > maxDeliveredAlertsPerTarget {
		return fmt.Errorf("delivered set for %s/%s would exceed its %d-entry cap (%d + %d); per-alert progress is not recorded for this fire",
			groupKey, target, maxDeliveredAlertsPerTarget, len(set), len(deliveryKeys))
	}

	if set == nil {
		set = make(map[string]struct{}, len(deliveryKeys))
		l.delivered[key] = set
	}
	for _, deliveryKey := range deliveryKeys {
		if deliveryKey == "" {
			continue
		}
		set[deliveryKey] = struct{}{}
	}
	return nil
}

// Forget removes every dedup entry AND every partial delivered set (for every
// target) belonging to groupKey. Called when a group is deleted (emptied) so
// the dedup log doesn't grow unbounded independent of active groups.
// Implements GroupNotifyLog.
func (l *notifyDedupLog) Forget(_ context.Context, groupKey GroupKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.entries {
		if k.groupKey == groupKey {
			delete(l.entries, k)
		}
	}
	for k := range l.delivered {
		if k.groupKey == groupKey {
			delete(l.delivered, k)
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
