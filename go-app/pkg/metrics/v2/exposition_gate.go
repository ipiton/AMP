package v2

import (
	"net/http"
	"sync/atomic"
)

// ================================================================================
// ExpositionGate — runtime on/off switch for the /metrics endpoint (INF-A slice 1)
// ================================================================================
// AMP's collectors are registered with promauto at package init and increment
// unconditionally; there is no per-collector enable flag to flip, and adding
// one would mean touching every metric in the codebase. What CAN be toggled at
// runtime is exposition: whether the scrape endpoint serves those collectors.
//
// So `metrics.enabled` means exactly "serve /metrics", and this gate is what
// makes that true. Collection keeps running either way — a scraper that comes
// back after the gate is re-opened sees the counters that accumulated while it
// was closed, it does not see a gap reset to zero. Anything stronger (actually
// stopping collection) is not something the metrics layer supports today.

// ExpositionGate decides whether the metrics endpoint answers scrapes.
type ExpositionGate struct {
	enabled atomic.Bool
}

// NewExpositionGate creates a gate in the given initial state.
func NewExpositionGate(enabled bool) *ExpositionGate {
	gate := &ExpositionGate{}
	gate.enabled.Store(enabled)
	return gate
}

// SetEnabled opens or closes the gate. Safe for concurrent use with Wrap's
// handler.
func (g *ExpositionGate) SetEnabled(enabled bool) {
	if g == nil {
		return
	}
	g.enabled.Store(enabled)
}

// Enabled reports the current state. A nil gate reports true, so a caller that
// never configured one keeps the pre-gate behaviour (always exposed).
func (g *ExpositionGate) Enabled() bool {
	if g == nil {
		return true
	}
	return g.enabled.Load()
}

// Wrap returns a handler that serves next while the gate is open and answers
// 404 while it is closed.
//
// 404 rather than 200-with-empty-body on purpose: an empty scrape looks like a
// live target reporting nothing, which silently zeroes dashboards and alerts on
// absent metrics. A 404 marks the target down, which is what "the operator
// turned metrics off" should look like.
func (g *ExpositionGate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.Enabled() {
			http.Error(w, "metrics exposition is disabled (metrics.enabled=false)", http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}
