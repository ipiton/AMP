package grouping

import "time"

// NflogSnapshot is the file-snapshot shape of an in-memory GroupNotifyLog's
// state (wave 6, FU-LITE-FILE-SNAPSHOT): entries (the dedup log) plus
// per-target delivered-state (task fu4's partial-delivery tracking) — both
// live inside notifyDedupLog, which is the only GroupNotifyLog
// implementation this applies to. RedisNotifyLog owns its own durability via
// Redis's own persistence and does NOT implement NflogSnapshotter; a
// standard-profile deployment must never engage file snapshotting (see
// ServiceRegistry.initializeSnapshotting).
//
// Encoding is plain encoding/json (versioned by the wrapping snapshot
// package, not by this type) — no protobuf/snappy like upstream
// Alertmanager's own nflog snapshot, matching the wave-6 brief's "keep it
// simple, stdlib only" constraint. GroupKey is a plain string type and
// time.Duration/time.Time marshal via their standard encoding/json support,
// so no custom (Un)MarshalJSON is needed here.
type NflogSnapshot struct {
	Entries   []NflogEntrySnapshot     `json:"entries,omitempty"`
	Delivered []NflogDeliveredSnapshot `json:"delivered,omitempty"`
}

// NflogEntrySnapshot is one (group, target) dedup entry: "the last alert set
// sent to target for groupKey, and when." TTL is the freshness window
// captured at RecordSent time (dedup.go) — LoadNflogSnapshot drops an entry
// whose TTL has already elapsed as of the snapshot's load time, the
// in-memory equivalent of the entry's Redis key having expired while the
// process was down.
type NflogEntrySnapshot struct {
	GroupKey  GroupKey      `json:"group_key"`
	Target    string        `json:"target"`
	Signature string        `json:"signature"`
	SentAt    time.Time     `json:"sent_at"`
	TTL       time.Duration `json:"ttl"`
}

// NflogDeliveredSnapshot is one (group, target) pair's partial per-alert
// delivery progress (task fu4) — statuses is keyed by alert FINGERPRINT
// (never by the full DeliveryKey), matching notifyDedupLog.deliveredState.
type NflogDeliveredSnapshot struct {
	GroupKey   GroupKey          `json:"group_key"`
	Target     string            `json:"target"`
	Statuses   map[string]string `json:"statuses,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
	TTL        time.Duration     `json:"ttl"`
}

// NflogSnapshotter is implemented by GroupNotifyLog backends that support
// file-snapshot persistence (wave 6, FU-LITE-FILE-SNAPSHOT) — today, only
// notifyDedupLog. ServiceRegistry type-asserts the grouping.GroupNotifyLog
// returned by newNotifyLog against this interface rather than depending on
// the concrete (unexported) type directly.
type NflogSnapshotter interface {
	// SnapshotNflog returns a point-in-time copy of the log's state, safe to
	// serialize and safe to keep after the call returns (no shared mutable
	// state with the live log).
	SnapshotNflog() NflogSnapshot

	// LoadNflogSnapshot replaces the log's current state with snap, dropping
	// any entry/delivered-state whose TTL has already elapsed as of now.
	// Called once, at startup, before the log is used for real duplicate
	// checks.
	LoadNflogSnapshot(snap NflogSnapshot, now time.Time) error
}

// NewMemoryNotifyLog constructs the in-memory GroupNotifyLog implementation
// explicitly (wave 6, FU-LITE-FILE-SNAPSHOT). Before this, the lite profile
// (and the standard-profile Redis-unavailable fallback) left
// DefaultGroupManagerConfig.NotifyLog nil and let NewDefaultGroupManager
// default it internally (newNotifyDedupLog) — functionally identical, but
// ServiceRegistry then had no handle on the instance to snapshot. Returning
// the exported GroupNotifyLog interface (rather than the unexported
// *notifyDedupLog type) keeps notifyDedupLog itself private; callers that
// need snapshot access type-assert the returned value against
// NflogSnapshotter instead of naming the concrete type.
func NewMemoryNotifyLog() GroupNotifyLog {
	return newNotifyDedupLog()
}

// SnapshotNflog implements NflogSnapshotter.
func (l *notifyDedupLog) SnapshotNflog() NflogSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	snap := NflogSnapshot{
		Entries:   make([]NflogEntrySnapshot, 0, len(l.entries)),
		Delivered: make([]NflogDeliveredSnapshot, 0, len(l.delivered)),
	}
	for key, entry := range l.entries {
		snap.Entries = append(snap.Entries, NflogEntrySnapshot{
			GroupKey:  key.groupKey,
			Target:    key.target,
			Signature: entry.signature,
			SentAt:    entry.sentAt,
			TTL:       entry.ttl,
		})
	}
	for key, state := range l.delivered {
		if state == nil {
			continue
		}
		statuses := make(map[string]string, len(state.statuses))
		for fingerprint, status := range state.statuses {
			statuses[fingerprint] = status
		}
		snap.Delivered = append(snap.Delivered, NflogDeliveredSnapshot{
			GroupKey:   key.groupKey,
			Target:     key.target,
			Statuses:   statuses,
			RecordedAt: state.recordedAt,
			TTL:        state.ttl,
		})
	}
	return snap
}

// LoadNflogSnapshot implements NflogSnapshotter. Entries/delivered-states
// whose TTL has already elapsed as of now are dropped rather than loaded —
// the brief's "respect TTL semantics on load" requirement, computed from the
// snapshot's own recorded timestamp vs now rather than trusting the
// snapshot to be fresh. A zero/negative TTL (an older snapshot written
// before this field existed, or a corrupt individual record) falls back to
// deliveredStateTTL(0), the same default RecordSent/RecordPartialDelivery
// apply live, rather than treating it as "never expires."
func (l *notifyDedupLog) LoadNflogSnapshot(snap NflogSnapshot, now time.Time) error {
	entries := make(map[dedupKey]dedupEntry, len(snap.Entries))
	for _, e := range snap.Entries {
		ttl := e.TTL
		if ttl <= 0 {
			ttl = deliveredStateTTL(0)
		}
		if now.Sub(e.SentAt) > ttl {
			continue
		}
		entries[dedupKey{groupKey: e.GroupKey, target: e.Target}] = dedupEntry{
			signature: e.Signature,
			sentAt:    e.SentAt,
			ttl:       ttl,
		}
	}

	delivered := make(map[dedupKey]*deliveredState, len(snap.Delivered))
	for _, d := range snap.Delivered {
		ttl := d.TTL
		if ttl <= 0 {
			ttl = deliveredStateTTL(0)
		}
		if now.Sub(d.RecordedAt) > ttl {
			continue
		}
		statuses := make(map[string]string, len(d.Statuses))
		for fingerprint, status := range d.Statuses {
			statuses[fingerprint] = status
		}
		delivered[dedupKey{groupKey: d.GroupKey, target: d.Target}] = &deliveredState{
			statuses:   statuses,
			recordedAt: d.RecordedAt,
			ttl:        ttl,
		}
	}

	l.mu.Lock()
	l.entries = entries
	l.delivered = delivered
	l.mu.Unlock()
	return nil
}
