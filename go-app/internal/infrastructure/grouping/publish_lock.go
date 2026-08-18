package grouping

import (
	"hash/fnv"
	"sync"
)

// groupPublishLockStripes is the fixed number of mutexes backing
// groupPublishLocks. Bounded, never grows — see groupPublishLocks' doc
// comment for why a striped lock was chosen over a per-GroupKey map.
const groupPublishLockStripes = 256

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
// Why striped (fixed array) instead of a map keyed by GroupKey: a map
// needs either unbounded growth (one mutex per distinct group ever seen,
// never freed — the same class of "small leak, no cleanup" trade-off
// notifyDedupLog explicitly avoids via Forget) or lifecycle-matched cleanup
// coordinated with group deletion, which risks unlocking/deleting a mutex
// another goroutine is still waiting on. A small fixed array of mutexes,
// indexed by a hash of the key, has fixed memory (256 mutexes, ~1 cache
// line group total) and needs no cleanup. Cost: two DIFFERENT group keys
// that hash to the same stripe serialize against each other unnecessarily
// — harmless correctness-wise, just occasional extra contention. At 256
// stripes this is negligible for realistic group-count scale.
type groupPublishLocks struct {
	stripes [groupPublishLockStripes]sync.Mutex
}

// lockFor returns the mutex for key's stripe. Callers must Lock/Unlock it
// themselves (no helper method, so `defer` reads naturally at the call
// site).
func (l *groupPublishLocks) lockFor(key GroupKey) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &l.stripes[h.Sum32()%groupPublishLockStripes]
}
