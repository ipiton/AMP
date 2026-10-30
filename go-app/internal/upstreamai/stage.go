package upstreamai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/notify"
	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
)

// maxReasoningLen caps llm_reasoning, as formatAlertmanager does.
const maxReasoningLen = 500

// NotifyStage returns the stage for upstreamcore.Options.NotifyStage. It
// waits at most wait — and at most half of what is left of the stage's
// deadline, leaving the rest for delivery — for the pending classifications
// of the alerts, then passes on copies of the classified ones with llm_*
// annotations. With dropNoise, firing alerts classified as noise are left
// out; a resolved one always goes through, since its firing notification
// may have been sent before the classification was ready.
func (c *Classifier) NotifyStage(wait time.Duration, dropNoise bool) notify.Stage {
	return notify.StageFunc(func(ctx context.Context, l *slog.Logger, alerts ...*alert.Alert) (context.Context, []*alert.Alert, error) {
		entries := c.lookup(alerts)
		c.await(ctx, entries, wait)

		out := make([]*alert.Alert, 0, len(alerts))
		for i, a := range alerts {
			res := finalResult(entries[i])
			if res == nil {
				out = append(out, a)
				continue
			}
			if dropNoise && res.Severity == core.SeverityNoise && !a.Resolved() {
				c.noiseDropped.Inc()
				l.Info("alert classified as noise left out of the notification",
					"alertname", string(a.Labels[model.AlertNameLabel]),
					"fingerprint", a.Fingerprint().String(),
					"confidence", res.Confidence)
				continue
			}
			out = append(out, annotated(a, res))
		}
		return ctx, out, nil
	})
}

// await waits for the pending entries until one shared deadline.
func (c *Classifier) await(ctx context.Context, entries []*entry, wait time.Duration) {
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half < wait {
			wait = half
		}
	}
	var timeout <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	expired := wait <= 0
	for _, e := range entries {
		if e == nil || isDone(e) {
			continue
		}
		if expired {
			c.waits.WithLabelValues("timeout").Inc()
			continue
		}
		select {
		case <-e.done:
			c.waits.WithLabelValues("ready").Inc()
		case <-timeout:
			expired = true
			c.waits.WithLabelValues("timeout").Inc()
		case <-ctx.Done():
			expired = true
			c.waits.WithLabelValues("timeout").Inc()
		}
	}
}

func isDone(e *entry) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// finalResult is the entry's classification, nil if absent, pending or
// skipped.
func finalResult(e *entry) *core.ClassificationResult {
	if e == nil || !isDone(e) {
		return nil
	}
	return e.result
}

// annotated copies a with the classification added to a clone of its
// annotations: the copy the dispatcher hands over shares the map with the
// alert in the store. The keys match formatAlertmanager's on cmd/server.
func annotated(a *alert.Alert, res *core.ClassificationResult) *alert.Alert {
	cp := *a
	cp.Annotations = a.Annotations.Clone()
	if cp.Annotations == nil {
		cp.Annotations = model.LabelSet{}
	}
	cp.Annotations["llm_severity"] = model.LabelValue(res.Severity)
	cp.Annotations["llm_confidence"] = model.LabelValue(fmt.Sprintf("%.2f", res.Confidence))
	cp.Annotations["llm_reasoning"] = model.LabelValue(truncate(res.Reasoning, maxReasoningLen))
	if recs := res.Recommendations; len(recs) > 0 {
		if len(recs) > 3 {
			recs = recs[:3]
		}
		cp.Annotations["llm_recommendations"] = model.LabelValue(strings.Join(recs, "; "))
	}
	return &cp
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
