package alertconv

import (
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/prometheus/common/model"
)

// TestUpstreamFingerprint_MatchesModelLabelSet pins UpstreamFingerprint to
// exactly what upstream Alertmanager computes for the same label set: the
// expected value is derived independently via model.LabelSet.Fingerprint()
// (not by re-deriving our own implementation), so a regression in either the
// hashing algorithm or the hex formatting fails this test.
func TestUpstreamFingerprint_MatchesModelLabelSet(t *testing.T) {
	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	want := model.LabelSet{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}.Fingerprint().String()

	got := UpstreamFingerprint(labels)

	if got != want {
		t.Fatalf("UpstreamFingerprint() = %q, want %q (upstream model.LabelSet.Fingerprint())", got, want)
	}
	if len(got) != 16 {
		t.Fatalf("UpstreamFingerprint() length = %d, want 16 (upstream FNV-1a 64-bit hex)", len(got))
	}
}

func TestUpstreamFingerprint_EmptyLabels_ReturnsEmpty(t *testing.T) {
	if got := UpstreamFingerprint(nil); got != "" {
		t.Fatalf("UpstreamFingerprint(nil) = %q, want empty", got)
	}
	if got := UpstreamFingerprint(map[string]string{}); got != "" {
		t.Fatalf("UpstreamFingerprint({}) = %q, want empty", got)
	}
}

// TestUpstreamFingerprint_OrderIndependent guards the "sorted labels" part of
// upstream's algorithm: label insertion order must not affect the result.
func TestUpstreamFingerprint_OrderIndependent(t *testing.T) {
	a := UpstreamFingerprint(map[string]string{"b": "2", "a": "1", "c": "3"})
	b := UpstreamFingerprint(map[string]string{"c": "3", "b": "2", "a": "1"})
	if a != b {
		t.Fatalf("fingerprint depends on map iteration order: %q vs %q", a, b)
	}
}

// TestToGettableAlert_FingerprintIsUpstreamShape_NotInternalKey verifies the
// API-facing fix end to end: ToGettableAlert must serialize the 16-hex
// upstream fingerprint, not alert.Fingerprint (the 64-hex internal SHA-256
// dedup key) — and the two must actually differ for a realistic input so this
// test cannot pass by accident.
func TestToGettableAlert_FingerprintIsUpstreamShape_NotInternalKey(t *testing.T) {
	labels := map[string]string{"alertname": "HighCPU", "severity": "critical"}
	internalKey := Fingerprint(labels) // the pre-existing SHA-256 dedup key

	alert := core.APIAlert{
		Labels:      labels,
		Annotations: map[string]string{},
		StartsAt:    time.Now().UTC().Format(time.RFC3339),
		Status:      "firing",
		Fingerprint: internalKey,
	}

	got := ToGettableAlert(alert, nil, time.Now().UTC())

	if len(got.Fingerprint) != 16 {
		t.Fatalf("ToGettableAlert().Fingerprint = %q (len %d), want 16 hex chars", got.Fingerprint, len(got.Fingerprint))
	}
	if got.Fingerprint == internalKey {
		t.Fatalf("ToGettableAlert().Fingerprint unexpectedly equals the internal 64-hex key: %q", got.Fingerprint)
	}
	if strings.ToLower(got.Fingerprint) != got.Fingerprint {
		t.Fatalf("ToGettableAlert().Fingerprint = %q, want lowercase hex", got.Fingerprint)
	}
	want := UpstreamFingerprint(labels)
	if got.Fingerprint != want {
		t.Fatalf("ToGettableAlert().Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}
