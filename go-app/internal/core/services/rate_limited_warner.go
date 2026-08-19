package services

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// rateLimitedWarner caps a repeating Warn to one per fallbackWarnWindow,
// folding the count of suppressed occurrences into the next one so the
// "this keeps happening, N times since" signal is batched rather than lost.
//
// Extracted for the re-review finding R1 warning, which has the same shape as
// warnGroupingFallback's own rate limiting: with a broken route tree every
// single alert takes that path, so one line per alert would drown the log.
// warnGroupingFallback keeps its own inlined counters (it also folds in
// diagnostic fields), deliberately not refactored here.
type rateLimitedWarner struct {
	lastUnixNano atomic.Int64
	suppressed   atomic.Int64
}

// warn logs at Warn if the window has elapsed (adding
// suppressed_since_last_warn), and at Debug otherwise.
func (w *rateLimitedWarner) warn(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}

	now := time.Now()
	last := w.lastUnixNano.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < fallbackWarnWindow {
		w.suppressed.Add(1)
		logger.Debug(msg, args...)
		return
	}
	if !w.lastUnixNano.CompareAndSwap(last, now.UnixNano()) {
		// Another goroutine just took the Warn slot for this window.
		w.suppressed.Add(1)
		logger.Debug(msg, args...)
		return
	}

	suppressed := w.suppressed.Swap(0)
	logger.Warn(msg, append(args, "suppressed_since_last_warn", suppressed)...)
}
