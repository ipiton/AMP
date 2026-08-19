package application

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// === Task fu4 (alertmanager-parity wave 4): per-alert outcome tracking for
// non-batch publishers, end to end through the real notify chain ===
//
// A non-batch integration (Slack/Telegram/PagerDuty/Email) sends one wire
// message per alert. Before this task, alert 3 of 5 failing left the whole
// (group, target) pair unconfirmed — no nflog entry — and the group's next fire
// re-sent all five, duplicating the four that had already landed. These tests
// drive the real chain (group timers -> notify log -> coordinator -> worker pool
// -> HTTP) and assert on the wire: which alerts each fire actually sends.

// perAlertEndpoint is an httptest endpoint standing in for a non-batch
// integration. It records the FINGERPRINT of every alert it is asked to
// deliver and fails only the ones named in failFingerprints, so a test can
// assert exactly which alerts a given fire put on the wire.
type perAlertEndpoint struct {
	server *httptest.Server

	mu       sync.Mutex
	received []string
	failing  map[string]bool
}

func newPerAlertEndpoint(t *testing.T, failFingerprints ...string) *perAlertEndpoint {
	t.Helper()

	e := &perAlertEndpoint{failing: map[string]bool{}}
	for _, fp := range failFingerprints {
		e.failing[fp] = true
	}

	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Fingerprint string `json:"fingerprint"`
		}
		_ = json.Unmarshal(body, &payload)

		e.mu.Lock()
		e.received = append(e.received, payload.Fingerprint)
		shouldFail := e.failing[payload.Fingerprint]
		e.mu.Unlock()

		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(e.server.Close)
	return e
}

// target is a NON-BATCH target: Type telegram routes it to a publisher that
// does not implement BatchAlertPublisher (one Publish call per alert), while
// Format webhook makes the per-alert payload carry the fingerprint so the
// endpoint can tell the alerts apart. The delivery shape — one wire message per
// alert — is what the task is about; the body format is only the test's probe.
func (e *perAlertEndpoint) target(name string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:    name,
		Type:    "telegram",
		URL:     e.server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}
}

// heal stops failing anything and clears the request log, so what follows is
// attributable to the fires AFTER this point.
func (e *perAlertEndpoint) heal() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failing = map[string]bool{}
	e.received = nil
}

// forget clears the request log while LEAVING the failure set in place, for
// tests that need the target to keep failing one alert.
func (e *perAlertEndpoint) forget() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.received = nil
}

func (e *perAlertEndpoint) delivered() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.received...)
}

// TestNotifyChain_PerAlertPartialFailure_RetryResendsOnlyTheFailedAlert is the
// headline end-to-end regression for FU-PER-ALERT-OUTCOMES (the wave-3 M4
// residual): five alerts, the third one's wire message fails, and the retry
// fire must carry ONLY that alert.
func TestNotifyChain_PerAlertPartialFailure_RetryResendsOnlyTheFailedAlert(t *testing.T) {
	endpoint := newPerAlertEndpoint(t, "fp-3")
	stack := newNotifyChainStack(t, time.Hour, endpoint.target("telegram-partial"))

	for _, fp := range []string{"fp-1", "fp-2", "fp-3", "fp-4", "fp-5"} {
		stack.addFiringAlert(t, fp)
	}

	// Fire 1: the target is unconfirmed (one alert failed), so NO full nflog
	// entry is written — but the four alerts that landed are recorded.
	require.Eventually(t, func() bool {
		return len(stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-partial")) == 4
	}, 5*time.Second, 10*time.Millisecond,
		"the four alerts the target accepted must be recorded as a per-alert delivered set")

	assert.Empty(t, stack.notifyLog.recordedSends(),
		"a partially delivered target must not get a full nflog entry (that would suppress the missing alert for a whole repeat_interval)")
	assert.ElementsMatch(t,
		[]string{"fp-1:firing", "fp-2:firing", "fp-4:firing", "fp-5:firing"},
		stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-partial"),
		"exactly the delivered alerts, keyed by fingerprint:status — never the failed one")

	// Fire 2+: endpoint healthy. Only the alert still owed may go on the wire.
	endpoint.heal()

	require.Eventually(t, func() bool {
		return len(stack.notifyLog.recordedSends()) > 0
	}, 5*time.Second, 10*time.Millisecond,
		"once the last owed alert is accepted the target must get its full nflog entry")

	for _, fp := range endpoint.delivered() {
		assert.Equal(t, "fp-3", fp,
			"a retry fire must re-send ONLY the alert that failed; %q already landed and must never be sent twice", fp)
	}

	assert.Empty(t, stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-partial"),
		"the full entry supersedes the per-alert delivered set, which must be cleaned up")
}

// TestNotifyChain_PerAlertFullSuccess_WritesOneEntryAndNoDeliveredSet is the
// happy-path guard: the per-alert bookkeeping must cost nothing when every
// alert lands (no extra Redis key on the common path).
func TestNotifyChain_PerAlertFullSuccess_WritesOneEntryAndNoDeliveredSet(t *testing.T) {
	endpoint := newPerAlertEndpoint(t)
	stack := newNotifyChainStack(t, time.Hour, endpoint.target("telegram-ok"))

	for _, fp := range []string{"fp-1", "fp-2", "fp-3"} {
		stack.addFiringAlert(t, fp)
	}

	require.Eventually(t, func() bool {
		return len(stack.notifyLog.recordedSends()) > 0
	}, 5*time.Second, 10*time.Millisecond, "a fully delivered target must get its nflog entry")

	assert.Empty(t, stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-ok"),
		"full success must leave no per-alert delivered set behind")
	assert.Zero(t, stack.notifyLog.partialRecords(),
		"the per-alert delivered set must never be written on the happy path")
}

// TestNotifyChain_PerAlertRetry_SendsNewAlertAlongsideTheOwedOne covers group
// mutation between fires: the delivered set is keyed by alert, not by set
// membership, so a NEW alert is sent together with the one still owed — and the
// ones that already landed still are not.
func TestNotifyChain_PerAlertRetry_SendsNewAlertAlongsideTheOwedOne(t *testing.T) {
	endpoint := newPerAlertEndpoint(t, "fp-2")
	stack := newNotifyChainStack(t, time.Hour, endpoint.target("telegram-growing"))

	stack.addFiringAlert(t, "fp-1")
	stack.addFiringAlert(t, "fp-2")

	require.Eventually(t, func() bool {
		return len(stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-growing")) == 1
	}, 5*time.Second, 10*time.Millisecond, "fp-1 landed, fp-2 did not")

	endpoint.heal()
	stack.addFiringAlert(t, "fp-3") // arrives between fires

	require.Eventually(t, func() bool {
		sent := map[string]bool{}
		for _, fp := range endpoint.delivered() {
			sent[fp] = true
		}
		return sent["fp-2"] && sent["fp-3"]
	}, 5*time.Second, 10*time.Millisecond,
		"the retried alert and the new one must both be sent")

	for _, fp := range endpoint.delivered() {
		assert.NotEqual(t, "fp-1", fp, "fp-1 already landed and must not be re-sent")
	}
}

// TestNotifyChain_BatchTargetIsUnaffectedByPerAlertTracking is the wave-3
// regression guard at chain level: a batch target still gets ONE POST per fire,
// still records nothing on failure, and never produces per-alert state.
func TestNotifyChain_BatchTargetIsUnaffectedByPerAlertTracking(t *testing.T) {
	webhook := newCountingWebhook(t, http.StatusInternalServerError)
	stack := newNotifyChainStack(t, time.Hour, webhook.target("webhook-500"))

	stack.addFiringAlert(t, "fp-1")
	stack.addFiringAlert(t, "fp-2")

	require.Eventually(t, func() bool { return webhook.hits.Load() >= 2 }, 5*time.Second, 10*time.Millisecond,
		"a failed batch delivery must still be retried on the next scheduled fire")

	assert.Empty(t, stack.notifyLog.recordedSends(),
		"a 500 must never be recorded as sent (wave-3 semantics, unchanged)")
	assert.Zero(t, stack.notifyLog.partialRecords(),
		"a batch target has no per-alert outcome, so no delivered set may ever be written for it")
	assert.Empty(t, stack.notifyLog.deliveredAlerts(stack.groupKey, "webhook-500"))
}

// TestNotifyChain_FlappingAlertReachesTheWireWhenItFiresAgain is the C1
// regression (review round 1, Critical) end to end, on the wire: with one alert
// of the group permanently failing — so no full nflog entry is ever written and
// the delivered state is never cleared — an alert going firing → resolved →
// firing MUST be delivered again.
//
// Before the fix the delivered state accumulated `fp-1:firing` AND
// `fp-1:resolved`, so the re-fire was filtered out as already-delivered and the
// notification was LOST for up to repeat_interval — the one place this design
// degraded to a drop instead of a duplicate.
func TestNotifyChain_FlappingAlertReachesTheWireWhenItFiresAgain(t *testing.T) {
	endpoint := newPerAlertEndpoint(t, "fp-3") // fp-3 fails on every fire, forever
	// Short repeat_interval so the timer chain keeps firing across all three
	// phases of the flap: after the group_interval fire AMP moves to
	// repeat_interval cadence (documented wave-3 behaviour), and an hour-long
	// one would end the test's observable fires after phase 2. It stays far
	// above the delivered state's own lifetime (repeat_interval + 60s grace),
	// so nothing here expires mid-test.
	stack := newNotifyChainStack(t, 300*time.Millisecond, endpoint.target("telegram-flap"))

	stack.addFiringAlert(t, "fp-1")
	stack.addFiringAlert(t, "fp-3")

	// Fire 1: fp-1 lands as firing, fp-3 fails.
	require.Eventually(t, func() bool {
		return len(stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-flap")) == 1
	}, 5*time.Second, 10*time.Millisecond, "fp-1 landed while fp-3 failed")

	// fp-1 resolves; its resolved notification lands too. The delivered state
	// must now describe fp-1 as resolved and NOT also as firing.
	stack.addResolvedAlert(t, "fp-1")
	require.Eventually(t, func() bool {
		delivered := stack.notifyLog.deliveredAlerts(stack.groupKey, "telegram-flap")
		return len(delivered) == 1 && delivered[0] == "fp-1:resolved"
	}, 5*time.Second, 10*time.Millisecond,
		"the resolved notification must land and REPLACE the recorded firing status, not accumulate alongside it")

	// fp-1 fires again, and the wire log is cleared so what follows is
	// attributable to the fires after this point.
	endpoint.forget()
	stack.addFiringAlert(t, "fp-1")

	require.Eventually(t, func() bool {
		for _, fp := range endpoint.delivered() {
			if fp == "fp-1" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"fp-1 firing again is a NEW notification and must reach the target; a stale same-fingerprint key must never suppress it")
}
