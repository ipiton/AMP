package grouping

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ownSendsSweepInterval bounds how often resilientNotifyLog scans its local
// record for expired entries.
const ownSendsSweepInterval = time.Minute

// ErrNotifyLogAnsweredLocally is returned by resilientNotifyLog.IsDuplicate
// TOGETHER WITH true when the wrapped log could not be read and the answer
// comes from this replica's own record. The caller should not send, and must
// not treat the target as known to be up to date either. It wraps the wrapped
// log's error.
var ErrNotifyLogAnsweredLocally = errors.New("notification log unreadable, answered from this replica's own record")

// resilientNotifyLog wraps a notification log whose reads can fail (the
// Redis-backed one) and remembers, in this process only, every send it was
// asked to record.
//
// The local record is consulted ONLY when the wrapped log's IsDuplicate
// returns an error. A group is flushed every group_interval, so answering
// every failed lookup with "not a duplicate" would re-notify every target of
// every group on every flush for as long as the log is unreachable. What this
// replica sent itself it does not send again (true with
// ErrNotifyLogAnsweredLocally); a send made by another replica is unknown
// here, so that case still returns the wrapped error and the caller fails
// open.
//
// Known limit: the local record knows nothing about what other replicas did
// since. If another replica sent a newer notification for the same (group,
// target), or resolved and deleted the group, and the wrapped log then
// becomes unreadable, the stale record holds back the notifications it
// covers — including the first one of a group that fired again — until reads
// recover or repeat_interval has passed since this replica's own send.
//
// Local entries are dropped by Forget and, for groups this replica did not
// delete itself, by a sweep that runs on RecordSent and removes entries older
// than their TTL (repeat_interval plus grace).
type resilientNotifyLog struct {
	GroupNotifyLog

	local *notifyDedupLog

	sweepMu   sync.Mutex
	lastSweep time.Time
}

// NewResilientNotifyLog wraps primary with a per-process record of this
// replica's own sends, used when primary cannot answer IsDuplicate.
func NewResilientNotifyLog(primary GroupNotifyLog) GroupNotifyLog {
	return &resilientNotifyLog{
		GroupNotifyLog: primary,
		local:          newNotifyDedupLog(),
	}
}

// IsDuplicate implements GroupNotifyLog.
func (l *resilientNotifyLog) IsDuplicate(ctx context.Context, groupKey GroupKey, target string, signature string, ttl time.Time) (bool, error) {
	dup, err := l.GroupNotifyLog.IsDuplicate(ctx, groupKey, target, signature, ttl)
	if err == nil {
		return dup, nil
	}

	if sentHere, _ := l.local.IsDuplicate(ctx, groupKey, target, signature, ttl); sentHere {
		return true, fmt.Errorf("%w: %w", ErrNotifyLogAnsweredLocally, err)
	}
	return false, err
}

// RecordSent implements GroupNotifyLog. The local record is written first and
// cannot fail, so it is in place even when the wrapped log rejects the write.
func (l *resilientNotifyLog) RecordSent(ctx context.Context, groupKey GroupKey, target string, signature string, now time.Time, repeatInterval time.Duration) error {
	_ = l.local.RecordSent(ctx, groupKey, target, signature, now, repeatInterval)
	l.sweep(now)
	return l.GroupNotifyLog.RecordSent(ctx, groupKey, target, signature, now, repeatInterval)
}

// Forget implements GroupNotifyLog.
func (l *resilientNotifyLog) Forget(ctx context.Context, groupKey GroupKey) error {
	_ = l.local.Forget(ctx, groupKey)
	return l.GroupNotifyLog.Forget(ctx, groupKey)
}

func (l *resilientNotifyLog) sweep(now time.Time) {
	l.sweepMu.Lock()
	due := now.Sub(l.lastSweep) >= ownSendsSweepInterval
	if due {
		l.lastSweep = now
	}
	l.sweepMu.Unlock()

	if due {
		l.local.evictExpired(now)
	}
}
