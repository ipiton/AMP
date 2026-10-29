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

const alertNamespaceKey contextKey = alertTimeKey + 1

// WithAlertNamespace returns a context carrying the alert's `namespace` label.
// Tools that read cluster state scope themselves to it
// (INVESTIGATION-PROMPT-ISOLATION): the namespace comes from the alert, not
// from the LLM, which the alert payload can steer.
func WithAlertNamespace(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, alertNamespaceKey, namespace)
}

// AlertNamespaceFromCtx returns the alert namespace set by WithAlertNamespace,
// or "" when the alert has none or no namespace was set.
func AlertNamespaceFromCtx(ctx context.Context) string {
	ns, _ := ctx.Value(alertNamespaceKey).(string)
	return ns
}
