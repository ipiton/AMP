package templating

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

func alertWith(labels, annotations map[string]string, status core.AlertStatus) *core.Alert {
	return &core.Alert{
		AlertName:   labels["alertname"],
		Status:      status,
		Labels:      labels,
		Annotations: annotations,
		StartsAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// === CommonLabels / CommonAnnotations intersection semantics ===

func TestBuildData_CommonLabels_IsIntersectionNotUnion(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A", "shared": "same", "differs": "1"}, nil, core.StatusFiring),
			alertWith(map[string]string{"alertname": "A", "shared": "same", "differs": "2"}, nil, core.StatusFiring),
		},
	})

	assert.Equal(t, KV{"alertname": "A", "shared": "same"}, data.CommonLabels)
}

// TestBuildData_CommonLabels_MissingLabelDrops pins the subtle half of
// upstream's loop: a key absent from a later alert compares against the zero
// value and therefore DROPS, rather than surviving as "present on some".
func TestBuildData_CommonLabels_MissingLabelDrops(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A", "only_on_first": "x"}, nil, core.StatusFiring),
			alertWith(map[string]string{"alertname": "A"}, nil, core.StatusFiring),
		},
	})

	assert.Equal(t, KV{"alertname": "A"}, data.CommonLabels)
}

// TestBuildData_CommonLabels_EmptyValueIsNotMissing guards the other side of
// the same comparison: a label PRESENT with an empty value on both alerts is
// common, and must not be confused with an absent label.
func TestBuildData_CommonLabels_EmptyValueIsNotMissing(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A", "blank": ""}, nil, core.StatusFiring),
			alertWith(map[string]string{"alertname": "A", "blank": ""}, nil, core.StatusFiring),
		},
	})

	assert.Equal(t, KV{"alertname": "A", "blank": ""}, data.CommonLabels)
}

func TestBuildData_CommonAnnotations_IsIntersection(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A"}, map[string]string{"runbook": "u", "summary": "s1"}, core.StatusFiring),
			alertWith(map[string]string{"alertname": "A"}, map[string]string{"runbook": "u", "summary": "s2"}, core.StatusFiring),
		},
	})

	assert.Equal(t, KV{"runbook": "u"}, data.CommonAnnotations)
}

func TestBuildData_SingleAlert_EverythingIsCommon(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A", "x": "1"}, map[string]string{"s": "v"}, core.StatusFiring),
		},
	})

	assert.Equal(t, KV{"alertname": "A", "x": "1"}, data.CommonLabels)
	assert.Equal(t, KV{"s": "v"}, data.CommonAnnotations)
}

// TestBuildData_NoAlerts_IsSafe: an empty group should not happen, but a Data
// with nil maps would panic every default template (they all call
// .SortedPairs). Upstream's zero-alert Data is "resolved" with empty maps.
func TestBuildData_NoAlerts_IsSafe(t *testing.T) {
	data := BuildData(DataInput{Receiver: "r"})

	assert.Equal(t, "resolved", data.Status)
	assert.NotNil(t, data.GroupLabels)
	assert.NotNil(t, data.CommonLabels)
	assert.NotNil(t, data.CommonAnnotations)
	assert.Empty(t, data.Alerts)

	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)
	got, err := tmpl.ExecuteTextString(`{{ template "__subject" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "[RESOLVED]  ", got)
}

// TestBuildData_NilAlertEntry_IsSkipped: the notify chain filters alerts in
// place in several passes (inhibition, silencing, delivered-set), so a nil
// entry is a plausible caller bug. It must not panic the render.
func TestBuildData_NilAlertEntry_IsSkipped(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			nil,
			alertWith(map[string]string{"alertname": "A"}, nil, core.StatusFiring),
			nil,
		},
	})

	require.Len(t, data.Alerts, 1)
	assert.Equal(t, "firing", data.Status)
	assert.Equal(t, KV{"alertname": "A"}, data.CommonLabels)
}

// === Firing / Resolved filters ===

func TestAlerts_FiringResolved_PartitionTheSet(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{
			alertWith(map[string]string{"alertname": "A", "i": "1"}, nil, core.StatusFiring),
			alertWith(map[string]string{"alertname": "A", "i": "2"}, nil, core.StatusResolved),
			alertWith(map[string]string{"alertname": "A", "i": "3"}, nil, core.StatusFiring),
		},
	})

	firing, resolved := data.Alerts.Firing(), data.Alerts.Resolved()
	require.Len(t, firing, 2)
	require.Len(t, resolved, 1)
	assert.Equal(t, "1", firing[0].Labels["i"])
	assert.Equal(t, "3", firing[1].Labels["i"], "order within a partition follows the input order")
	assert.Equal(t, "2", resolved[0].Labels["i"])
}

func TestAlerts_FiringResolved_NeverNil(t *testing.T) {
	// A nil slice would make `{{ range }}` fine but `len` and JSON differ from
	// upstream, which always returns an initialized slice.
	var as Alerts
	assert.NotNil(t, as.Firing())
	assert.NotNil(t, as.Resolved())
	assert.Empty(t, as.Firing())
}

// TestBuildData_UnknownStatusIsResolved documents the mapping for a status that
// is neither firing nor resolved: core.Alert.Status is validated to one of the
// two everywhere it is ingested, so this is defensive — and "resolved" is the
// safe default (it cannot invent a firing notification).
func TestBuildData_UnknownStatusIsResolved(t *testing.T) {
	data := BuildData(DataInput{
		Receiver: "r",
		Alerts:   []*core.Alert{alertWith(map[string]string{"alertname": "A"}, nil, core.AlertStatus("weird"))},
	})

	require.Len(t, data.Alerts, 1)
	assert.Equal(t, "resolved", data.Alerts[0].Status)
	assert.Equal(t, "resolved", data.Status)
}

// === Fingerprint ===

// TestBuildData_UsesUpstreamFingerprintNotInternalKey is the wave-5 contract:
// templates and wire payloads carry the 16-hex upstream form, never AMP's
// internal 64-hex SHA-256 dedup key.
func TestBuildData_UsesUpstreamFingerprintNotInternalKey(t *testing.T) {
	labels := map[string]string{"alertname": "HighCPU", "severity": "critical", "instance": "server-1"}
	alert := alertWith(labels, nil, core.StatusFiring)
	alert.Fingerprint = alertconv.Fingerprint(labels) // the internal 64-hex key

	data := BuildData(DataInput{Receiver: "r", Alerts: []*core.Alert{alert}})

	require.Len(t, data.Alerts, 1)
	assert.Len(t, data.Alerts[0].Fingerprint, 16)
	assert.Equal(t, alertconv.UpstreamFingerprint(labels), data.Alerts[0].Fingerprint)
	assert.NotEqual(t, alert.Fingerprint, data.Alerts[0].Fingerprint)
}

// === Receiver name ===

func TestReceiverNameFromTarget(t *testing.T) {
	cases := map[string]string{
		"cfg:team-x/slack0":       "team-x",
		"cfg:team-x/pagerduty12":  "team-x",
		"cfg:team.a/email0":       "team.a",
		"cfg:with/slash/webhook0": "with/slash",
		"team-x":                  "team-x",
		"k8s-target-name":         "k8s-target-name",
		"":                        "",
		"cfg:":                    "",
		"cfg:noslash":             "noslash",
	}

	for in, want := range cases {
		assert.Equal(t, want, ReceiverNameFromTarget(in), "input %q", in)
	}
}

// TestConfigTargetPrefix_LiteralIsUnchanged is the in-package half of pinning
// the duplicated constant; the half that compares it to
// business/publishing.ConfigTargetPrefix lives in the EXTERNAL test package
// (prefix_parity_test.go) — see the note there for why the import cannot live
// in this file.
func TestConfigTargetPrefix_LiteralIsUnchanged(t *testing.T) {
	assert.Equal(t, "cfg:", configTargetPrefix)
}

// TestBuildData_ReceiverIsQuoteMetaOfInput documents (and pins) the upstream
// quirk at the BuildData level, separately from the golden rendering test.
func TestBuildData_ReceiverIsQuoteMetaOfInput(t *testing.T) {
	assert.Equal(t, `team\.a`, BuildData(DataInput{Receiver: "team.a"}).Receiver)
	assert.Equal(t, "team-x", BuildData(DataInput{Receiver: "team-x"}).Receiver)
	assert.Equal(t, `a\+b`, BuildData(DataInput{Receiver: "a+b"}).Receiver)
}

// === KV / Pairs / Strings ===

// TestKV_SortedPairs_AlertnameFirst is the ordering `__subject` depends on.
func TestKV_SortedPairs_AlertnameFirst(t *testing.T) {
	kv := KV{"zzz": "1", "alertname": "A", "aaa": "2", "mmm": "3"}

	assert.Equal(t, Pairs{
		{"alertname", "A"},
		{"aaa", "2"},
		{"mmm", "3"},
		{"zzz", "1"},
	}, kv.SortedPairs())
	assert.Equal(t, Strings{"alertname", "aaa", "mmm", "zzz"}, kv.Names())
	assert.Equal(t, Strings{"A", "2", "3", "1"}, kv.Values())
}

func TestKV_SortedPairs_NoAlertnameIsPlainSort(t *testing.T) {
	kv := KV{"b": "2", "a": "1"}
	assert.Equal(t, Pairs{{"a", "1"}, {"b", "2"}}, kv.SortedPairs())
}

func TestKV_Remove_ReturnsCopyWithoutKeys(t *testing.T) {
	kv := KV{"a": "1", "b": "2", "c": "3"}
	got := kv.Remove([]string{"b", "missing"})

	assert.Equal(t, KV{"a": "1", "c": "3"}, got)
	assert.Equal(t, KV{"a": "1", "b": "2", "c": "3"}, kv, "Remove must not mutate the receiver")
}

func TestKV_String_AndPairsString(t *testing.T) {
	assert.Equal(t, "alertname=A, b=2", KV{"b": "2", "alertname": "A"}.String())
	assert.Equal(t, "", KV{}.String())
}

func TestStrings_Join(t *testing.T) {
	assert.Equal(t, "a:b:c", Strings{"a", "b", "c"}.Join(":"))
	assert.Equal(t, "", Strings{}.Join(":"))
}

// TestData_JSONTagsMatchUpstream keeps the wire-visible field names pinned:
// a user template can reach them via `toJson`, and slice 2 must not rename
// anything.
func TestData_JSONTagsMatchUpstream(t *testing.T) {
	raw, err := json.Marshal(BuildData(DataInput{
		Receiver: "r",
		Alerts:   []*core.Alert{alertWith(map[string]string{"alertname": "A"}, nil, core.StatusFiring)},
	}))
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))

	for _, key := range []string{"receiver", "status", "alerts", "groupLabels", "commonLabels", "commonAnnotations", "externalURL"} {
		assert.Contains(t, decoded, key)
	}

	var alerts []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decoded["alerts"], &alerts))
	require.Len(t, alerts, 1)
	for _, key := range []string{"status", "labels", "annotations", "startsAt", "endsAt", "generatorURL", "fingerprint"} {
		assert.Contains(t, alerts[0], key)
	}
}

// TestBuildData_NoSecretsReachData is the epic's "no secrets pass through Data"
// constraint, expressed as a test: Data is built ONLY from the alert's
// labels/annotations plus the receiver name and the external URL. Nothing
// reads the receiver's integration config, so a bot_token / routing_key /
// smtp password has no path into a template. If a future field is added that
// does, this test's field-count check fails and forces the question.
func TestBuildData_NoSecretsReachData(t *testing.T) {
	// A credential-looking value in a LABEL is the user's own doing and does
	// render (upstream behaves the same); a credential in the target config
	// does not exist in Data at all. The guard is structural: count the fields.
	raw, err := json.Marshal(BuildData(DataInput{Receiver: "r"}))
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Len(t, decoded, 7,
		"Data gained a field: verify it cannot carry receiver credentials before updating this count")
}
