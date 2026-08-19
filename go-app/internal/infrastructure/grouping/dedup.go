package grouping

import (
	"context"
	"errors"
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
	// EXPIRES, on the same schedule as the Redis implementation's TTL (review
	// round 1, finding I1). The first cut kept this state forever on the theory
	// that "the entries map's caller-supplied cutoff answers freshness" — it
	// does not: nothing consults a cutoff on this path, so while one alert of a
	// group kept failing, the alerts that HAD landed were filtered out of every
	// subsequent fire indefinitely and their repeat notifications were lost.
	// This is production code (the lite profile, and the standard profile's
	// fallback when Redis is unavailable at grouping-init), not a test double.
	//
	// Also bounded by deletion — RecordSent drops a target's state, Forget
	// drops the whole group's — and by maxDeliveredAlertsPerTarget.
	delivered map[dedupKey]*deliveredState
}

// deliveredState is one (group, target) pair's per-alert delivery progress in
// the in-memory log (task fu4).
//
// statuses is keyed by FINGERPRINT, holding that alert's delivered status, so
// there is at most one entry per alert (review round 1, finding C1): recording
// `resolved` for an alert overwrites a previously recorded `firing` instead of
// accumulating both, which is what a flapping alert needs in order not to be
// suppressed when it fires again.
type deliveredState struct {
	statuses   map[string]string // fingerprint -> delivered status
	recordedAt time.Time
	ttl        time.Duration
}

// expired reports whether this state is past its TTL and must be treated as
// absent — the in-memory equivalent of the Redis key expiring.
func (s *deliveredState) expired(now time.Time) bool {
	return s == nil || now.Sub(s.recordedAt) > s.ttl
}

// dedupKey scopes a dedup entry to one (group, target) pair (task fwb).
type dedupKey struct {
	groupKey GroupKey
	target   string
}

type dedupEntry struct {
	signature string
	sentAt    time.Time

	// ttl is the entry's freshness window (wave 6, FU-LITE-FILE-SNAPSHOT):
	// deliveredStateTTL(repeatInterval) computed at RecordSent time, mirroring
	// the TTL RedisNotifyLog attaches to the same key. It plays NO role in
	// live duplicate detection — IsDuplicate still recomputes freshness
	// against a caller-supplied cutoff on every call, unchanged — this field
	// exists solely so a file snapshot can drop stale entries at load time
	// (LoadNflogSnapshot, nflog_snapshot.go) the same way Redis would have
	// let the key expire while the process was down.
	ttl time.Duration
}

func newNotifyDedupLog() *notifyDedupLog {
	return &notifyDedupLog{
		entries:   make(map[dedupKey]dedupEntry),
		delivered: make(map[dedupKey]*deliveredState),
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
// GroupNotifyLog; repeatInterval plays no role in LIVE duplicate detection
// (IsDuplicate recomputes freshness against a caller-supplied cutoff on
// every call instead — see GroupNotifyLog's doc comment), but is captured
// into the entry's ttl field (wave 6, FU-LITE-FILE-SNAPSHOT) via the same
// deliveredStateTTL formula RedisNotifyLog uses for its key TTL, so a file
// snapshot can apply the same freshness window at load time.
//
// Also drops this target's partial delivered set (task fu4): a full entry
// covering the whole alert set supersedes any per-alert progress toward it.
func (l *notifyDedupLog) RecordSent(_ context.Context, groupKey GroupKey, target string, signature string, now time.Time, repeatInterval time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := dedupKey{groupKey: groupKey, target: target}
	l.entries[key] = dedupEntry{signature: signature, sentAt: now, ttl: deliveredStateTTL(repeatInterval)}
	delete(l.delivered, key)
	return nil
}

// DeliveredAlerts implements GroupNotifyLog (task fu4): the delivery keys of
// the alerts target accepted while the group stayed unconfirmed. ctx is unused
// (in-memory) and the error is always nil.
//
// Expired state is dropped here rather than by a background sweeper (review
// round 1, finding I1): this is the only read path, so expiring on read gives
// the same observable behaviour as the Redis TTL with no extra goroutine, and it
// reclaims the memory at the same time.
func (l *notifyDedupLog) DeliveredAlerts(_ context.Context, groupKey GroupKey, target string) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := dedupKey{groupKey: groupKey, target: target}
	state := l.delivered[key]
	if state.expired(time.Now()) {
		delete(l.delivered, key)
		return nil, nil
	}

	out := make([]string, 0, len(state.statuses))
	for fingerprint, status := range state.statuses {
		out = append(out, deliveryKeyFor(fingerprint, status))
	}
	return out, nil
}

// RecordPartialDelivery implements GroupNotifyLog (task fu4): additively
// records the alerts target accepted during an otherwise unconfirmed fire.
//
// Additive per FINGERPRINT: a newly recorded status replaces that alert's
// previous one (review round 1, finding C1), so a flapping alert can never
// accumulate two statuses and suppress its own re-fire.
//
// Capped at maxDeliveredAlertsPerTarget, checked up front against the count of
// genuinely NEW (distinct) fingerprints in this call so the batch is either
// recorded whole or refused whole — the same rule the Redis Lua script uses
// (re-review, finding r2: a batch that names the same new fingerprint twice,
// e.g. ["new:firing","new:resolved"], must consume exactly one slot in both
// implementations, not one per occurrence).
//
// The state's expiry is set ONLY when this call CREATES it (re-review, finding
// r5): a state that already exists keeps the TTL its first write gave it.
// Refreshing the TTL on every partial write let a group with one
// persistently-failing alert and other alerts trickling in keep already-
// delivered alerts suppressed well past one repeat_interval — this is the
// in-memory half of that fix (see recordPartialDeliveryScript for the Redis
// half). Not refreshing can only make the state expire EARLIER than a fresh
// window would, which resends sooner — the at-least-once floor stays intact.
//
// Writes l.delivered[key] only after every check has passed (re-review,
// finding r6): a refused (cap) or no-op (nothing genuinely new) call used to
// still plant a fresh, empty *deliveredState in the map when the previous one
// had expired — harmless (it reads back as expired and is reclaimed on the
// next read) but pointless bookkeeping for a call that recorded nothing.
func (l *notifyDedupLog) RecordPartialDelivery(_ context.Context, groupKey GroupKey, target string, deliveryKeys []string, repeatInterval time.Duration) error {
	if len(deliveryKeys) == 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	key := dedupKey{groupKey: groupKey, target: target}

	existing, found := l.delivered[key]
	isNewState := !found || existing.expired(now)

	var statuses map[string]string
	if isNewState {
		statuses = make(map[string]string, len(deliveryKeys))
	} else {
		statuses = existing.statuses
	}

	incoming := make(map[string]string, len(deliveryKeys))
	newAlerts := 0
	for _, deliveryKey := range deliveryKeys {
		fingerprint, status, ok := splitDeliveryKey(deliveryKey)
		if !ok {
			continue
		}
		if _, exists := statuses[fingerprint]; !exists {
			if _, counted := incoming[fingerprint]; !counted {
				newAlerts++
			}
		}
		incoming[fingerprint] = status
	}
	if len(incoming) == 0 {
		return nil
	}

	if len(statuses)+newAlerts > maxDeliveredAlertsPerTarget {
		return fmt.Errorf("%w: delivered state for %s/%s would exceed its %d-alert cap (%d + %d); per-alert progress is not recorded for this fire",
			ErrDeliveredStateCapped, groupKey, target, maxDeliveredAlertsPerTarget, len(statuses), newAlerts)
	}

	for fingerprint, status := range incoming {
		statuses[fingerprint] = status
	}

	var state *deliveredState
	if isNewState {
		state = &deliveredState{
			statuses:   statuses,
			recordedAt: now,
			ttl:        deliveredStateTTL(repeatInterval),
		}
	} else {
		state = existing
	}
	l.delivered[key] = state
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

// ErrDeliveredStateCapped is returned by RecordPartialDelivery when recording
// would push a target's delivered state past maxDeliveredAlertsPerTarget (task
// fu4). Distinguished from a backend failure so the caller can count the
// reversion to at-least-once separately from "Redis is broken" — see
// DefaultGroupManager's RecordPartialDelivery call site.
var ErrDeliveredStateCapped = errors.New("grouping: per-alert delivered state is at its cap")

// splitDeliveryKey inverts core.Alert.DeliveryKey: "fingerprint:status" →
// (fingerprint, status). ok is false for a key with no status segment or an
// empty fingerprint, which the caller skips rather than storing.
//
// Split at the LAST colon: alert statuses ("firing"/"resolved") never contain
// one, so this is exact even if a fingerprint did.
//
// This exists because the delivered state is stored per FINGERPRINT (one status
// per alert — review round 1, finding C1) while the notify chain compares whole
// DeliveryKeys, so the two representations have to convert cleanly in both
// directions. Keeping both halves of that conversion next to alertSetSignature
// keeps every user of the format in one file.
func splitDeliveryKey(deliveryKey string) (fingerprint string, status string, ok bool) {
	idx := strings.LastIndex(deliveryKey, ":")
	if idx <= 0 || idx == len(deliveryKey)-1 {
		return "", "", false
	}
	return deliveryKey[:idx], deliveryKey[idx+1:], true
}

// deliveryKeyFor rebuilds a core.Alert.DeliveryKey from its stored halves.
func deliveryKeyFor(fingerprint string, status string) string {
	return fingerprint + ":" + status
}

// deliveredStateTTL is how long a per-alert delivered state stays valid: the
// group's repeat_interval plus the same grace an nflog entry gets (task fu4), so
// stale partial progress ages out into a full resend rather than suppressing
// notifications indefinitely. Shared by both GroupNotifyLog implementations so
// the in-memory one cannot drift from the Redis TTL (review round 1, finding I1).
func deliveredStateTTL(repeatInterval time.Duration) time.Duration {
	ttl := repeatInterval
	if ttl <= 0 {
		ttl = notifyLogEntryTTLFallback
	}
	return ttl + notifyLogEntryTTLGracePeriod
}

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
