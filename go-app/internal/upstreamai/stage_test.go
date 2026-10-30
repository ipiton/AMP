package upstreamai

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

var discard = slog.New(slog.DiscardHandler)

func execStage(t *testing.T, c *Classifier, ctx context.Context, wait time.Duration, dropNoise bool, alerts ...*alert.Alert) ([]*alert.Alert, time.Duration) {
	t.Helper()
	start := time.Now()
	_, out, err := c.NotifyStage(wait, dropNoise).Exec(ctx, discard, alerts...)
	require.NoError(t, err, "the stage never fails a notification")
	return out, time.Since(start)
}

func waits(c *Classifier, o string) float64 {
	return testutil.ToFloat64(c.waits.WithLabelValues(o))
}

func noiseDropped(c *Classifier) float64 {
	return testutil.ToFloat64(c.noiseDropped)
}

func TestStageAnnotatesCopiesNotTheStore(t *testing.T) {
	c, _ := newTestClassifier(t, &fakeService{res: warning()}, Config{})
	a := firing("DiskFull")
	c.PostStore(a, false)
	waitDone(t, c, a)

	out, _ := execStage(t, c, context.Background(), time.Second, false, a)

	require.Len(t, out, 1)
	ann := out[0].Annotations
	assert.Equal(t, model.LabelValue("warning"), ann["llm_severity"])
	assert.Equal(t, model.LabelValue("0.87"), ann["llm_confidence"])
	assert.Equal(t, model.LabelValue("disk filling up"), ann["llm_reasoning"])
	assert.Equal(t, model.LabelValue("clean logs; grow volume; page owner"), ann["llm_recommendations"], "top three")
	assert.Equal(t, model.LabelValue("disk"), ann["summary"])

	assert.Equal(t, model.LabelSet{"summary": "disk"}, a.Annotations, "the stored alert must not change")
	assert.Equal(t, a.Fingerprint(), out[0].Fingerprint())
}

func TestStageWaitsForPendingClassification(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	c, _ := newTestClassifier(t, svc, Config{})
	a := firing("DiskFull")
	c.PostStore(a, false)
	time.AfterFunc(100*time.Millisecond, func() { close(svc.gate) })

	out, took := execStage(t, c, context.Background(), 5*time.Second, false, a)

	assert.Less(t, took, 3*time.Second, "returns as soon as the classification is ready")
	assert.Equal(t, model.LabelValue("warning"), out[0].Annotations["llm_severity"])
	assert.Equal(t, 1.0, waits(c, "ready"))
}

func TestStageGivesUpAtTheBudget(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	defer close(svc.gate)
	c, _ := newTestClassifier(t, svc, Config{})
	a, b := firing("A"), firing("B")
	c.PostStore(a, false)
	c.PostStore(b, false)

	out, took := execStage(t, c, context.Background(), 100*time.Millisecond, false, a, b)

	assert.GreaterOrEqual(t, took, 90*time.Millisecond)
	assert.Less(t, took, 2*time.Second, "one shared deadline, not one per alert")
	assert.Equal(t, []*alert.Alert{a, b}, out, "sent unannotated")
	assert.Equal(t, 2.0, waits(c, "timeout"))
}

func TestStageZeroWaitDoesNotWait(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	defer close(svc.gate)
	c, _ := newTestClassifier(t, svc, Config{})
	a := firing("A")
	c.PostStore(a, false)

	out, took := execStage(t, c, context.Background(), 0, false, a)

	assert.Less(t, took, 50*time.Millisecond)
	assert.Equal(t, []*alert.Alert{a}, out)
}

func TestStageWaitIsCappedAtHalfTheDeadline(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	defer close(svc.gate)
	c, _ := newTestClassifier(t, svc, Config{})
	a := firing("A")
	c.PostStore(a, false)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, took := execStage(t, c, ctx, 10*time.Second, false, a)

	assert.Less(t, took, 350*time.Millisecond, "half of the stage deadline is left for delivery")
}

func TestStageDoesNotWaitForSkippedOrUnknown(t *testing.T) {
	c, _ := newTestClassifier(t, &fakeService{res: warning()}, Config{})
	c.AlertCallback(func(context.Context, model.LabelSet) bool { return true })
	silenced := firing("Silenced")
	c.PostStore(silenced, false)
	waitDone(t, c, silenced)

	out, took := execStage(t, c, context.Background(), 5*time.Second, false, silenced, firing("Unknown"))

	assert.Less(t, took, 100*time.Millisecond)
	assert.Len(t, out, 2)
	for _, a := range out {
		assert.NotContains(t, a.Annotations, model.LabelName("llm_severity"))
	}
}

func TestStageDropNoise(t *testing.T) {
	noise := &core.ClassificationResult{Severity: core.SeverityNoise, Confidence: 0.9, Reasoning: "flapping"}
	c, _ := newTestClassifier(t, &fakeService{res: noise}, Config{})
	a := firing("Flappy")
	c.PostStore(a, false)
	waitDone(t, c, a)
	r := resolved("Flappy") // same labels, so the same fingerprint and entry

	out, _ := execStage(t, c, context.Background(), time.Second, false, a)
	require.Len(t, out, 1, "without drop_noise nothing is dropped")
	assert.Equal(t, model.LabelValue("noise"), out[0].Annotations["llm_severity"])

	out, _ = execStage(t, c, context.Background(), time.Second, true, a)
	assert.Empty(t, out, "a firing noise alert is left out")
	assert.Equal(t, 1.0, noiseDropped(c))

	out, _ = execStage(t, c, context.Background(), time.Second, true, r)
	require.Len(t, out, 1, "a resolved one always goes through: its firing may have been sent already")
	assert.Equal(t, 1.0, noiseDropped(c))
}

func TestTruncateIsRuneSafe(t *testing.T) {
	long := strings.Repeat("я", maxReasoningLen+10)
	got := truncate(long, maxReasoningLen)
	assert.Equal(t, maxReasoningLen, len([]rune(got)))
	assert.Equal(t, "short", truncate("short", maxReasoningLen))
}
