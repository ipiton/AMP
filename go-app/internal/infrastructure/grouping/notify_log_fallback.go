package grouping

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// localSentSweepInterval bounds how often resilientNotifyLog scans its local
// record for expired entries.
const localSentSweepInterval = time.Minute

// resilientNotifyLog wraps a notification log whose reads can fail (the
// Redis-backed one) and remembers, in this process only, every send it was
// asked to record.
//
// The local record is consulted ONLY when the wrapped log's IsDuplicate
// returns an error. A group is flushed every group_interval, so answering
// every failed lookup with "not a duplicate" would re-notify every target of
// every group on every flush for as long as the log is unreachable. What this
// replica sent itself it does not send again; a send made by another replica
// is unknown here, so that case still returns the error and the caller
// fails open.
//
// Known limit: if another replica sent a newer notification for the same
// (group, target) and the wrapped log then becomes unreadable, the local
// record is stale and can suppress one notification until reads recover or
// repeat_interval elapses.
//
// Local entries expire on the wrapped log's schedule (repeat_interval plus
// grace), so a group deleted by another replica does not leave one behind.
type resilientNotifyLog struct {
	GroupNotifyLog

	local  *notifyDedupLog
	logger *slog.Logger

	sweepMu   sync.Mutex
	lastSweep time.Time
}

// NewResilientNotifyLog wraps primary with a per-process record of this
// replica's own sends, used when primary cannot answer IsDuplicate.
func NewResilientNotifyLog(primary GroupNotifyLog, logger *slog.Logger) GroupNotifyLog {
	if logger == nil {
		logger = slog.Default()
	}
	return &resilientNotifyLog{
		GroupNotifyLog: primary,
		local:          newNotifyDedupLog(),
		logger:         logger,
	}
}

// IsDuplicate implements GroupNotifyLog.
func (l *resilientNotifyLog) IsDuplicate(ctx context.Context, groupKey GroupKey, target string, signature string, ttl time.Time) (bool, error) {
	dup, err := l.GroupNotifyLog.IsDuplicate(ctx, groupKey, target, signature, ttl)
	if err == nil {
		return dup, nil
	}

	if sentHere, _ := l.local.IsDuplicate(ctx, groupKey, target, signature, ttl); sentHere {
		l.logger.Warn("nflog duplicate check failed for target; this replica already sent this alert set, skipping",
			"group_key", groupKey,
			"target", target,
			"error", err)
		return true, nil
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
	due := now.Sub(l.lastSweep) >= localSentSweepInterval
	if due {
		l.lastSweep = now
	}
	l.sweepMu.Unlock()

	if due {
		l.local.evictExpired(now)
	}
}
