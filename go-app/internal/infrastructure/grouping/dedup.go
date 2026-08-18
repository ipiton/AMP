package grouping

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// notifyDedupLog is a minimal in-memory notification-log (task 2.4, notify-
// stage chain Step 3: Dedup). It answers the same question upstream
// Alertmanager's nflog answers: "did we already send a notification for
// this exact alert set, for this group+receiver, within repeat_interval?"
//
// Deliberately minimal: keyed by GroupKey alone (which is already
// receiver-scoped — see AlertProcessor.groupKeyFor's "receiver=<name>/..."
// prefix from task 2.3), storing only the last-sent alert-set signature and
// timestamp. This is NOT the upstream nflog's Redis-backed, gossip-
// replicated notification log — it is a single-process, best-effort
// substitute. A restart loses all entries (a restart can therefore cause
// one duplicate notification per active group — acceptable for this slice).
// Redis-backed nflog is tracked as a Phase 6 follow-up (alertmanager-parity
// plan), NOT implemented here.
//
// Thread-safe via mu.
type notifyDedupLog struct {
	mu      sync.Mutex
	entries map[GroupKey]dedupEntry
}

type dedupEntry struct {
	signature string
	sentAt    time.Time
}

func newNotifyDedupLog() *notifyDedupLog {
	return &notifyDedupLog{entries: make(map[GroupKey]dedupEntry)}
}

// IsDuplicate reports whether a notification for groupKey carrying exactly
// this alert set was already sent within ttl (the group's effective
// repeat_interval). It does NOT record anything — call RecordSent after a
// successful publish.
func (l *notifyDedupLog) IsDuplicate(groupKey GroupKey, signature string, ttl time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[groupKey]
	if !ok {
		return false
	}
	if entry.signature != signature {
		// Alert set changed since the last send (new alert, one resolved,
		// etc.) — never a duplicate, matches upstream nflog semantics.
		return false
	}
	return entry.sentAt.After(ttl)
}

// RecordSent records that a notification carrying signature for groupKey
// was just sent successfully, at now.
func (l *notifyDedupLog) RecordSent(groupKey GroupKey, signature string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[groupKey] = dedupEntry{signature: signature, sentAt: now}
}

// Forget removes any dedup entry for groupKey. Called when a group is
// deleted (emptied) so the dedup log doesn't grow unbounded independent of
// active groups.
func (l *notifyDedupLog) Forget(groupKey GroupKey) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, groupKey)
}

// alertSetSignature computes a deterministic signature for alerts: sorted
// "fingerprint:status" pairs joined by "|". Order-independent (a group's
// alerts map iteration order is not stable) and status-sensitive (an alert
// flipping firing<->resolved changes the signature, so it is never treated
// as a duplicate of the prior send — matching upstream nflog, where a
// changed alert set always triggers a fresh notification).
func alertSetSignature(alerts []*core.Alert) string {
	parts := make([]string, 0, len(alerts))
	for _, a := range alerts {
		parts = append(parts, a.Fingerprint+":"+string(a.Status))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
