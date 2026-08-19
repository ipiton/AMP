package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

type fakePublishingCoordinator struct {
	results []*infrapublishing.PublishingResult
	err     error
	alert   *core.EnrichedAlert

	// task 2.4: PublishGroupToTargets call recording
	groupResults  []*infrapublishing.PublishingResult
	groupErr      error
	groupAlerts   []*core.Alert
	groupReceiver string
	groupKey      string
	groupLabels   map[string]string

	// task fwb/fu4: records which target names the caller's targetAlerts
	// callback was asked about, and the alert subset it returned for each.
	skipTargetAsked  []string
	targetAlertsSeen map[string][]*core.Alert

	// review finding C1: non-grouped publish call recording.
	directReceiver    string
	directTargetNames []string
	directCalls       int
}

// PublishToTargets records the receiver the adapter passed (review finding C1:
// the non-grouped path must resolve targets by receiver, not fan out).
func (f *fakePublishingCoordinator) PublishToTargets(_ context.Context, alert *core.EnrichedAlert, targetNames []string, receiverName string) ([]*infrapublishing.PublishingResult, error) {
	f.alert = alert
	f.directTargetNames = targetNames
	f.directReceiver = receiverName
	f.directCalls++
	return f.results, f.err
}

func (f *fakePublishingCoordinator) PublishGroupToTargets(_ context.Context, alerts []*core.Alert, receiver string, groupKey string, groupLabels map[string]string, targetAlerts func(string, []*core.Alert) []*core.Alert) ([]*infrapublishing.PublishingResult, error) {
	f.groupAlerts = alerts
	f.groupReceiver = receiver
	f.groupKey = groupKey
	f.groupLabels = groupLabels

	if targetAlerts == nil {
		return f.groupResults, f.groupErr
	}

	// Mirror the real coordinator: ask targetAlerts once per configured
	// result's target name, omit any target it returns no alerts for, and
	// record the per-target subset it asked us to deliver (task fu4).
	if f.targetAlertsSeen == nil {
		f.targetAlertsSeen = map[string][]*core.Alert{}
	}
	filtered := make([]*infrapublishing.PublishingResult, 0, len(f.groupResults))
	for _, result := range f.groupResults {
		name := ""
		if result != nil && result.Target != nil {
			name = result.Target.Name
		}
		f.skipTargetAsked = append(f.skipTargetAsked, name)
		owed := targetAlerts(name, alerts)
		if len(owed) == 0 {
			continue
		}
		f.targetAlertsSeen[name] = owed
		filtered = append(filtered, result)
	}
	return filtered, f.groupErr
}

type fakeBusinessDiscoveryManager struct {
	targets []*core.PublishingTarget
}

func (f *fakeBusinessDiscoveryManager) DiscoverTargets(context.Context) error { return nil }

func (f *fakeBusinessDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	for _, target := range f.targets {
		if target.Name == name {
			return target, nil
		}
	}
	return nil, errors.New("target not found")
}

func (f *fakeBusinessDiscoveryManager) ListTargets() []*core.PublishingTarget {
	return f.targets
}

func (f *fakeBusinessDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	filtered := make([]*core.PublishingTarget, 0, len(f.targets))
	for _, target := range f.targets {
		if target.Type == targetType {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func (f *fakeBusinessDiscoveryManager) GetStats() businesspublishing.DiscoveryStats {
	return businesspublishing.DiscoveryStats{ValidTargets: len(f.targets)}
}

func (f *fakeBusinessDiscoveryManager) Health(context.Context) error { return nil }

func TestApplicationPublishingAdapter_BuildsEnrichedAlert(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{
				Target:  &core.PublishingTarget{Name: "ops"},
				Success: true,
			},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alert := &core.Alert{Fingerprint: "abc123", AlertName: "HighCPU"}
	classification := &core.ClassificationResult{Severity: core.SeverityWarning}

	if err := adapter.PublishToReceiverWithClassification(context.Background(), alert, classification, ""); err != nil {
		t.Fatalf("PublishToReceiverWithClassification() error = %v", err)
	}

	if coordinator.alert == nil {
		t.Fatalf("coordinator did not receive enriched alert")
	}
	if coordinator.alert.Alert != alert {
		t.Fatalf("expected original alert to be passed through")
	}
	if coordinator.alert.Classification != classification {
		t.Fatalf("expected classification to be passed through")
	}
	if coordinator.alert.ProcessingTimestamp == nil {
		t.Fatalf("expected processing timestamp to be set")
	}
	if coordinator.alert.ProcessingTimestamp.After(time.Now().UTC().Add(1 * time.Second)) {
		t.Fatalf("unexpected future processing timestamp: %v", coordinator.alert.ProcessingTimestamp)
	}
}

func TestApplicationPublishingAdapter_ReturnsErrorWhenAllTargetsFail(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{
				Target:  &core.PublishingTarget{Name: "ops"},
				Success: false,
				Error:   errors.New("queue full"),
			},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}, ""); err == nil {
		t.Fatalf("expected publish error when all targets fail")
	}
}

// TestApplicationPublishingAdapter_PartialFailureReturnsError is final review
// finding 11: the single-alert path returned nil as soon as ONE target
// succeeded, while PublishGroup treats any unconfirmed target as a failure.
// The failed target's notification was simply dropped. Now aligned, so the
// webhook handler answers 5xx and Prometheus retries.
func TestApplicationPublishingAdapter_PartialFailureReturnsError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
			{Target: &core.PublishingTarget{Name: "paging"}, Success: false, Error: errors.New("queue full")},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	err = adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}, "")
	if err == nil {
		t.Fatal("expected an error when only some targets confirmed the enqueue")
	}
	if !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("error should carry the failing target's cause, got %v", err)
	}
}

// TestApplicationPublishingAdapter_PartialFailureWithoutCauseStillErrors covers
// a result marked unsuccessful but carrying no Error — the adapter must still
// refuse to report success.
func TestApplicationPublishingAdapter_PartialFailureWithoutCauseStillErrors(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
			{Target: &core.PublishingTarget{Name: "paging"}, Success: false},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc"}, ""); err == nil {
		t.Fatal("expected an error when a target is unsuccessful without an explicit cause")
	}
}

// TestApplicationPublishingAdapter_NoTargetsIsNotAnError pins the DELIBERATE
// divergence from PublishGroup documented in publish(): an empty result set is a
// legitimate steady state on this path (no amp.receiver Secrets provisioned yet,
// or metrics-only mode) and there is no shared notification log to poison.
// Erroring would 5xx every ingested alert on a working deployment.
func TestApplicationPublishingAdapter_NoTargetsIsNotAnError(t *testing.T) {
	adapter, err := NewApplicationPublishingAdapter(&fakePublishingCoordinator{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc"}, ""); err != nil {
		t.Fatalf("empty results must stay non-error on the single-alert path, got %v", err)
	}
}

// TestApplicationPublishingAdapter_NilResultsDoNotSynthesizePartialFailure is
// wave re-review Minor 4: the partial-failure comparison ran against
// len(results), which counts nil entries. A run where every REAL target
// succeeded but the coordinator returned a padded slice therefore reported a
// partial failure — a spurious 5xx and a Prometheus retry for a delivery that
// fully succeeded.
func TestApplicationPublishingAdapter_NilResultsDoNotSynthesizePartialFailure(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			nil,
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
			nil,
		},
		groupResults: []*infrapublishing.PublishingResult{
			nil,
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
			nil,
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc"}, ""); err != nil {
		t.Fatalf("single-alert path: nil padding must not read as a partial failure, got %v", err)
	}

	outcomes, err := adapter.PublishGroup(context.Background(), "gk", []*core.Alert{{Fingerprint: "abc"}}, "default", nil, nil)
	if err != nil {
		t.Fatalf("group path: nil padding must not read as a partial failure, got %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Success {
		t.Fatalf("group path: expected exactly one successful outcome (nil entries dropped), got %+v", outcomes)
	}
}

// TestApplicationPublishingAdapter_PublishGroup_AllNilResultsIsNotConfirmed
// keeps the group path's "nil must never be reported as a delivery outcome"
// contract intact for the degenerate all-nil case: no usable entries means
// no outcomes at all, so the caller's RecordSent loop (task fwb) has
// nothing to record — same "not confirmed" effect the old all-or-nothing
// error used to convey, just via an empty outcome list instead of an error.
func TestApplicationPublishingAdapter_PublishGroup_AllNilResultsIsNotConfirmed(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{nil, nil},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	outcomes, err := adapter.PublishGroup(context.Background(), "gk", []*core.Alert{{Fingerprint: "abc"}}, "default", nil, nil)
	if err != nil {
		t.Fatalf("PublishGroup() error = %v, want nil (the coordinator itself did not error)", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("a result set with no usable entries must produce zero outcomes, got %+v", outcomes)
	}
}

func TestMetricsOnlyPublisher_Noops(t *testing.T) {
	publisher := NewMetricsOnlyPublisher("test_reason", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := publisher.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}, ""); err != nil {
		t.Fatalf("PublishToAll() error = %v", err)
	}
}

// TestMetricsOnlyPublisher_PublishGroupSignalsNoDelivery is the contract half
// of final review finding 4. DefaultGroupManager.publishGroupAlerts records the
// group in the SHARED, cross-replica notification log (TTL = repeat_interval)
// whenever PublishGroup returns nil. This publisher delivers nothing, so a nil
// return silenced the group on every HEALTHY replica for a full
// repeat_interval; it must return grouping.ErrDeliveryNotConfirmed instead.
//
// The consuming side (publishGroupAlerts skipping RecordSent for this sentinel)
// is covered in internal/infrastructure/grouping/metrics_only_nflog_test.go.
func TestMetricsOnlyPublisher_PublishGroupSignalsNoDelivery(t *testing.T) {
	publisher := NewMetricsOnlyPublisher("publishing_disabled", slog.New(slog.NewTextHandler(io.Discard, nil)))
	alerts := []*core.Alert{{Fingerprint: "abc", AlertName: "Test"}}

	outcomes, err := publisher.PublishGroup(context.Background(), "gk", alerts, "default", nil, nil)
	if !errors.Is(err, grouping.ErrDeliveryNotConfirmed) {
		t.Fatalf("PublishGroup() error = %v, want it to wrap grouping.ErrDeliveryNotConfirmed", err)
	}
	if !strings.Contains(err.Error(), "publishing_disabled") {
		t.Fatalf("PublishGroup() error = %q, want it to carry the degradation reason", err)
	}
	if outcomes != nil {
		t.Fatalf("PublishGroup() outcomes = %+v, want nil (nothing was delivered)", outcomes)
	}

	// Empty alert set is genuinely nothing to deliver, not a suppressed send.
	if _, err := publisher.PublishGroup(context.Background(), "gk", nil, "default", nil, nil); err != nil {
		t.Fatalf("PublishGroup(no alerts) error = %v, want nil", err)
	}
}

func TestDiscoveryAdapter_DelegatesToBusinessDiscovery(t *testing.T) {
	manager := &fakeBusinessDiscoveryManager{
		targets: []*core.PublishingTarget{
			{Name: "ops", Type: "webhook"},
			{Name: "paging", Type: "pagerduty"},
		},
	}

	adapter, err := NewDiscoveryAdapter(manager)
	if err != nil {
		t.Fatalf("NewDiscoveryAdapter() error = %v", err)
	}

	if got := adapter.GetTargetCount(); got != 2 {
		t.Fatalf("GetTargetCount() = %d, want 2", got)
	}

	target, err := adapter.GetTarget("paging")
	if err != nil {
		t.Fatalf("GetTarget() error = %v", err)
	}
	if target.Name != "paging" {
		t.Fatalf("GetTarget() returned %q, want paging", target.Name)
	}
}

func TestInitializeBusinessServices_LiteProfileUsesMetricsOnlyPublisher(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileLite,
			Publishing: appconfig.PublishingConfig{
				Enabled: true,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := registry.initializeBusinessServices(context.Background()); err != nil {
		t.Fatalf("initializeBusinessServices() error = %v", err)
	}

	if _, ok := registry.publisher.(*MetricsOnlyPublisher); !ok {
		t.Fatalf("expected MetricsOnlyPublisher, got %T", registry.publisher)
	}
}

// --- task 2.4: PublishGroup (notify-stage chain, batch publish) ----------

func TestApplicationPublishingAdapter_PublishGroup_PropagatesAlertsAndReceiver(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{
		{Fingerprint: "a1", AlertName: "HighCPU"},
		{Fingerprint: "a2", AlertName: "HighCPU"},
	}
	groupLabels := map[string]string{"alertname": "HighCPU"}

	outcomes, err := adapter.PublishGroup(context.Background(), "receiver=critical-pagerduty/alertname=HighCPU", alerts, "critical-pagerduty", groupLabels, nil)
	if err != nil {
		t.Fatalf("PublishGroup() error = %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Success || outcomes[0].Target != "ops" {
		t.Fatalf("expected exactly one successful outcome for target %q, got %+v", "ops", outcomes)
	}

	if len(coordinator.groupAlerts) != 2 {
		t.Fatalf("expected coordinator to receive 2 alerts, got %d", len(coordinator.groupAlerts))
	}
	if coordinator.groupReceiver != "critical-pagerduty" {
		t.Fatalf("expected receiver %q, got %q", "critical-pagerduty", coordinator.groupReceiver)
	}
	if coordinator.groupKey != "receiver=critical-pagerduty/alertname=HighCPU" {
		t.Fatalf("expected groupKey to be forwarded to the coordinator, got %q", coordinator.groupKey)
	}
	if !reflect.DeepEqual(coordinator.groupLabels, groupLabels) {
		t.Fatalf("expected groupLabels to be forwarded to the coordinator, got %+v", coordinator.groupLabels)
	}
}

func TestApplicationPublishingAdapter_PublishGroup_EmptyAlertsIsNoop(t *testing.T) {
	coordinator := &fakePublishingCoordinator{}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	outcomes, err := adapter.PublishGroup(context.Background(), "gk", nil, "any", nil, nil)
	if err != nil {
		t.Fatalf("PublishGroup() with no alerts should be a no-op, got error = %v", err)
	}
	if outcomes != nil {
		t.Fatalf("PublishGroup() with no alerts should report no outcomes, got %+v", outcomes)
	}
	if coordinator.groupAlerts != nil {
		t.Fatalf("coordinator should not have been called for an empty alert set")
	}
}

func TestApplicationPublishingAdapter_PublishGroup_NoTargetsForReceiverReturnsError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{},
		groupErr:     errors.New(`no targets found for receiver "unknown-receiver"`),
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	_, err = adapter.PublishGroup(context.Background(), "gk", alerts, "unknown-receiver", nil, nil)
	if err == nil {
		t.Fatal("expected an error when no targets match the receiver")
	}
}

// --- task fwb: PublishGroup reports per-target outcomes instead of an
// all-or-nothing error, so the caller (DefaultGroupManager.
// publishGroupAlerts) can record successes and retry only the failures. ---

// TestApplicationPublishingAdapter_PublishGroup_EmptyResultsIsNotAnError
// covers the metrics-only-mode fallback: PublishingCoordinator.
// PublishGroupToTargets returns (empty results, nil error). Under the task
// fwb contract this must surface as ZERO outcomes with a nil error — not an
// error — since the caller only records an nflog entry for outcomes it
// actually receives; an empty outcome list already means "nothing to
// record," which is exactly the "no confirmed delivery" effect the old
// all-or-nothing contract needed an explicit error for.
func TestApplicationPublishingAdapter_PublishGroup_EmptyResultsIsNotAnError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{}, // e.g. metrics-only mode
		groupErr:     nil,
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	outcomes, err := adapter.PublishGroup(context.Background(), "gk", alerts, "any-receiver", nil, nil)
	if err != nil {
		t.Fatalf("PublishGroup() error = %v, want nil (the coordinator itself did not error)", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("expected zero outcomes for empty results, got %+v", outcomes)
	}
}

// TestApplicationPublishingAdapter_PublishGroup_PartialFailureReportsPerTargetOutcomes
// covers what task fwb replaces the old all-or-nothing error with: 1 of 2
// targets fails, and PublishGroup must report BOTH outcomes (one success,
// one failure) with a nil error, so the caller can record the success and
// retry only the failure on the next scheduled fire — instead of treating
// the whole call as failed and resending to both targets again.
func TestApplicationPublishingAdapter_PublishGroup_PartialFailureReportsPerTargetOutcomes(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops-a"}, Success: true},
			{Target: &core.PublishingTarget{Name: "ops-b"}, Success: false, Error: errors.New("ops-b unreachable")},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	outcomes, err := adapter.PublishGroup(context.Background(), "gk", alerts, "multi-target-receiver", nil, nil)
	if err != nil {
		t.Fatalf("PublishGroup() error = %v, want nil (per-target outcomes carry the partial failure now)", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %+v", outcomes)
	}

	byTarget := map[string]bool{}
	for _, o := range outcomes {
		byTarget[o.Target] = o.Success
	}
	if !byTarget["ops-a"] {
		t.Fatalf("expected ops-a to be reported as a success, got %+v", outcomes)
	}
	if byTarget["ops-b"] {
		t.Fatalf("expected ops-b to be reported as a failure, got %+v", outcomes)
	}
}

// TestApplicationPublishingAdapter_PublishGroup_ForwardsTargetAlerts proves
// targetAlerts (task fwb's per-target nflog dedup callback, widened to the
// per-alert filter by task fu4) reaches the coordinator unchanged, and that a
// target it returns no alerts for produces no outcome.
func TestApplicationPublishingAdapter_PublishGroup_ForwardsTargetAlerts(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops-a"}, Success: true},
			{Target: &core.PublishingTarget{Name: "ops-b"}, Success: true},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	skipCalls := 0
	targetAlerts := func(target string, alerts []*core.Alert) []*core.Alert {
		skipCalls++
		if target == "ops-b" {
			return nil
		}
		return alerts
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	outcomes, err := adapter.PublishGroup(context.Background(), "gk", alerts, "multi-target-receiver", nil, targetAlerts)
	if err != nil {
		t.Fatalf("PublishGroup() error = %v", err)
	}
	if skipCalls == 0 {
		t.Fatal("expected targetAlerts to be forwarded to the coordinator and invoked")
	}
	if len(outcomes) != 1 || outcomes[0].Target != "ops-a" {
		t.Fatalf("expected only ops-a to be reported (ops-b skipped), got %+v", outcomes)
	}
}

// TestApplicationPublishingAdapter_ForwardsReceiver is the adapter half of
// slice-1 review finding C1: the non-grouped publish path must resolve targets
// BY RECEIVER (coordinator.PublishToTargets with nil target names), not through
// the unfiltered PublishToAll it used to call.
func TestApplicationPublishingAdapter_ForwardsReceiver(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "cfg:team-x/webhook0"}, Success: true},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToReceiver(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}, "team-x"); err != nil {
		t.Fatalf("PublishToReceiver() error = %v", err)
	}
	if coordinator.directReceiver != "team-x" {
		t.Errorf("coordinator receiver = %q, want %q", coordinator.directReceiver, "team-x")
	}
	if coordinator.directTargetNames != nil {
		t.Errorf("target names = %v, want nil (receiver-scoped resolution)", coordinator.directTargetNames)
	}

	if err := adapter.PublishToReceiverWithClassification(context.Background(),
		&core.Alert{Fingerprint: "abc", AlertName: "Test"},
		&core.ClassificationResult{Severity: "critical", Confidence: 1, Reasoning: "t"},
		"team-y"); err != nil {
		t.Fatalf("PublishToReceiverWithClassification() error = %v", err)
	}
	if coordinator.directReceiver != "team-y" {
		t.Errorf("coordinator receiver = %q, want %q", coordinator.directReceiver, "team-y")
	}
	if coordinator.directCalls != 2 {
		t.Errorf("coordinator calls = %d, want 2", coordinator.directCalls)
	}
}
