package grouping

import "sync"

// groupPublishLocks serializes the full notify-stage chain
// (Inhibit -> Silence -> Dedup -> publish, see publishGroupAlerts) per
// GroupKey (task 2.4 fix round 1, Finding 4).
//
// Why this is needed: IsDuplicate (the Dedup check) and RecordSent (the
// Dedup write) are two separate locked operations in notifyDedupLog, with
// the entire publish call to the GroupNotificationPublisher happening in
// between, outside of notifyDedupLog's own lock. Two concurrent
// publishGroupAlerts calls for the SAME group — e.g. two timer types
// firing close together, or (in a future multi-instance HA deployment) two
// instances racing on a lock-expiry edge — could both observe
// IsDuplicate == false before either calls RecordSent, and both publish:
// a double-send that no amount of locking inside notifyDedupLog alone can
// prevent, because the race spans the publish call itself.
//
// Fix: acquire a lock for this GroupKey before the Dedup check and hold it
// until after RecordSent (or the equivalent "we decided not to publish"
// exit), so the check-publish-record sequence is atomic with respect to
// any other publishGroupAlerts call for the same group. This is coarser
// than a real per-group actor/single-flight (it also serializes the
// publisher call itself, not just the bookkeeping), but it exactly
// reproduces upstream Alertmanager's own invariant — each aggrGroup is
// driven by a single goroutine, so its notify calls are inherently
// serialized — at the cost of one lock instead of a redesign.
//
// WHY NOT STRIPED ANY MORE (task rec fix round 1, review finding I1): this
// was a fixed array of 256 mutexes indexed by fnv32a(GroupKey)%256, on the
// argument that a hash collision only costs "occasional extra contention".
// That argument died with task rec: the critical section used to be an
// enqueue (microseconds) and is now a blocking wait for confirmed HTTP
// delivery (up to the delivery-confirmation timeout). Two unrelated groups
// sharing a stripe would serialize for that whole duration — and by the
// birthday bound a collision among ~25 concurrently-firing groups is already
// ~70% likely, certain above 256 groups. Worse, the group waiting on a
// stripe burns its own timer-callback budget while parked, so it could reach
// the publish with an already-expired context and lose the fire outright.
//
// So this is now a real per-GroupKey lock: a map of refcounted entries.
// Memory is bounded by the number of groups CONCURRENTLY inside
// publishGroupAlerts (not by the number of groups ever seen): the entry is
// deleted as soon as its last holder/waiter releases it, so there is no
// unbounded-growth leak — the concern that originally motivated striping —
// and no risk of freeing a mutex someone still waits on, because a waiter
// holds a reference before it blocks.
type groupPublishLocks struct {
	mu      sync.Mutex
	entries map[GroupKey]*groupPublishLockEntry
}

// groupPublishLockEntry is one group's mutex plus the number of goroutines
// currently holding OR waiting for it. refs is guarded by
// groupPublishLocks.mu, never by mu itself.
type groupPublishLockEntry struct {
	mu   sync.Mutex
	refs int
}

func newGroupPublishLocks() *groupPublishLocks {
	return &groupPublishLocks{entries: make(map[GroupKey]*groupPublishLockEntry)}
}

// acquire locks key's mutex and returns the release func. The returned func
// must be called exactly once (callers use `defer`).
//
// Reference counting happens under l.mu BEFORE blocking on the entry's own
// mutex, so an entry can never be evicted while another goroutine is queued
// on it.
func (l *groupPublishLocks) acquire(key GroupKey) func() {
	l.mu.Lock()
	entry, ok := l.entries[key]
	if !ok {
		entry = &groupPublishLockEntry{}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()

		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, key)
		}
		l.mu.Unlock()
	}
}

// tracked reports how many per-group lock entries exist right now. Test-only
// helper: the count must return to 0 once every fire has released, which is
// what proves the map does not leak.
func (l *groupPublishLocks) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
