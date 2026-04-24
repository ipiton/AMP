package investigation

import (
	"context"
	"time"
)

type contextKey int

const alertTimeKey contextKey = iota

// WithAlertTime returns a context carrying the alert firing time.
// Tools use this to anchor time-range queries without requiring LLM to pass timestamps.
func WithAlertTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, alertTimeKey, t)
}

// AlertTimeFromCtx extracts the alert time from ctx.
// Returns time.Now() if no alert time was set (safe fallback for tests / one-shot calls).
func AlertTimeFromCtx(ctx context.Context) time.Time {
	if t, ok := ctx.Value(alertTimeKey).(time.Time); ok && !t.IsZero() {
		return t
	}
	return time.Now()
}
