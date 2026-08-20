package templating

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// ============================================================================
// GOLDEN PROVENANCE — read this before touching any literal below
// ============================================================================
//
// Every expectation in this file was PRODUCED BY UPSTREAM ALERTMANAGER, not by
// this package and not by hand. They are byte-for-byte captures of
//
//	github.com/prometheus/alertmanager@v0.34.0/template
//
// rendering the same fixtures through its own Data() + ExecuteTextString().
// A mismatch here therefore means AMP diverged from upstream, which is the only
// question this file exists to answer.
//
// Five fixtures, each chosen for a branch the others do not reach: `mixed`
// (2 firing + 1 resolved), `single` (one alert, dotted receiver → QuoteMeta),
// `resolved-only` ([RESOLVED], no `:count`, empty Firing partition), `unicode`
// (cyrillic/CJK/emoji labels and annotations) and `disjoint` (alerts sharing
// nothing → both Common* maps empty). The last two were added in the fix round
// per review Minor 1.
//
// How they were generated (reproduce it the same way after an upstream bump):
//
//  1. A throwaway module OUTSIDE this repo (so AMP's go.mod gains nothing) with
//     a single require on github.com/prometheus/alertmanager v0.34.0, resolved
//     offline from the local module cache:
//
//     GOFLAGS=-mod=mod GOPROXY="file://$(go env GOMODCACHE)/cache/download" \
//     GOSUMDB=off go mod tidy
//
//  2. A main() that builds upstream's Data for each fixture below —
//
//     tmpl, _ := template.FromGlobs(nil)
//     tmpl.ExternalURL, _ = url.Parse("http://amp.example.com")
//     data := tmpl.Data(receiver, groupLabels, nil, "", alerts...)
//
//     — with the alerts constructed as alert.Alert{Alert: model.Alert{...}}
//     using exactly the labels/annotations/timestamps in fixtureAlerts below,
//     then prints %q of tmpl.ExecuteTextString(`{{ template "<name>" . }}`,
//     data) for each name (dot `.Alerts` for pagerduty.default.instances and
//     `.Alerts.Firing` for the two __text_alert_list definitions, matching how
//     upstream's own integrations call them).
//
//  3. The printed %q strings were pasted in as the want values, unedited.
//
// Two upstream behaviours the literals pin deliberately, because both look like
// bugs until you check upstream:
//
//   - Receiver goes through regexp.QuoteMeta: a receiver named `team.a` renders
//     as `team\.a`, and URL-escapes to `team%5C.a` in the alertmanager link.
//     See TestGolden_QuoteMetaReceiver.
//   - telegram.default.message and the markdown list are full of blank lines
//     ("\n\n\n"). That is upstream's whitespace, and reproducing it is the
//     point: a Telegram message must look the same after a migration.

const (
	goldenExternalURL = "http://amp.example.com"

	// goldenSubjectMixed is `__subject` for the three-alert fixture:
	// 2 firing (so ":2"), GroupLabels {alertname, severity} rendered
	// alertname-first, and CommonLabels minus GroupLabels leaving {job} in
	// parentheses.
	goldenSubjectMixed = "[FIRING:2] HighCPU critical (node)"

	// goldenSubjectSingle is `__subject` for the single-alert fixture, whose
	// GroupLabels are only {alertname} — so instance, job and severity all
	// land in the parenthesised remainder, sorted.
	goldenSubjectSingle = "[FIRING:1] HighCPU (server-1 node critical)"

	goldenTextAlertListFiring = "Labels:\n - alertname = HighCPU\n - instance = server-1\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-1\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-2\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-2\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n"

	goldenTextAlertListMarkdownFiring = "\nLabels:\n  - alertname = HighCPU\n  - instance = server-1\n  - job = node\n  - severity = critical\n" +
		"\nAnnotations:\n  - runbook_url = http://runbook.example/cpu\n  - summary = CPU high on server-1\n" +
		"\nSource: http://prometheus.example/graph?g0.expr=up\n" +
		"\nLabels:\n  - alertname = HighCPU\n  - instance = server-2\n  - job = node\n  - severity = critical\n" +
		"\nAnnotations:\n  - runbook_url = http://runbook.example/cpu\n  - summary = CPU high on server-2\n" +
		"\nSource: http://prometheus.example/graph?g0.expr=up\n\n"

	// goldenPagerDutyInstances covers ALL three alerts (upstream passes
	// `.Alerts`, not `.Alerts.Firing`), resolved one included.
	goldenPagerDutyInstances = "Labels:\n - alertname = HighCPU\n - instance = server-1\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-1\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-2\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-2\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-3\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-3\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n"

	goldenTelegramMessageMixed = "\n\nAlerts Firing:\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-1\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-1\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-2\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-2\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n" +
		"\n\n\nAlerts Resolved:\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-3\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-3\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n\n\n"

	goldenTelegramMessageSingle = "\n\nAlerts Firing:\n" +
		"Labels:\n - alertname = HighCPU\n - instance = server-1\n - job = node\n - severity = critical\n" +
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-1\n" +
		"Source: http://prometheus.example/graph?g0.expr=up\n\n\n\n"
)

// Upstream 16-hex FNV-1a fingerprints of the three fixture label sets, captured
// from the same generator run (upstream's Data() fills Fingerprint from
// model.LabelSet.Fingerprint()).
const (
	goldenFingerprintServer1 = "3e9d47cecd58c911"
	goldenFingerprintServer2 = "4a79297fc9ad3d38"
	goldenFingerprintServer3 = "e36a73d3f9c288cb"
)

var (
	goldenStartsAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	goldenEndsAt   = time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
)

const goldenGeneratorURL = "http://prometheus.example/graph?g0.expr=up"

// fixtureAlerts returns the three-alert fixture: two firing on different
// instances plus one resolved, all sharing alertname/severity/job (so the
// intersection is non-trivial) and a shared runbook_url annotation with a
// per-instance summary (so the annotation intersection is non-trivial too).
func fixtureAlerts() []*core.Alert {
	generatorURL := goldenGeneratorURL
	endsAt := goldenEndsAt

	alert := func(instance, status string) *core.Alert {
		a := &core.Alert{
			Fingerprint: "internal-sha256-key-" + instance, // deliberately NOT the value templates see
			AlertName:   "HighCPU",
			Status:      core.AlertStatus(status),
			Labels: map[string]string{
				"alertname": "HighCPU",
				"severity":  "critical",
				"instance":  instance,
				"job":       "node",
			},
			Annotations: map[string]string{
				"summary":     "CPU high on " + instance,
				"runbook_url": "http://runbook.example/cpu",
			},
			StartsAt:     goldenStartsAt,
			GeneratorURL: &generatorURL,
		}
		if status == string(core.StatusResolved) {
			a.EndsAt = &endsAt
		}
		return a
	}

	return []*core.Alert{
		alert("server-1", string(core.StatusFiring)),
		alert("server-2", string(core.StatusFiring)),
		alert("server-3", string(core.StatusResolved)),
	}
}

func mixedData() *Data {
	return BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Alerts:      fixtureAlerts(),
		ExternalURL: goldenExternalURL,
	})
}

func singleData() *Data {
	return BuildData(DataInput{
		Receiver:    "team.a",
		GroupLabels: map[string]string{"alertname": "HighCPU"},
		Alerts:      fixtureAlerts()[:1],
		ExternalURL: goldenExternalURL,
	})
}

func goldenTemplate(t *testing.T) *Template {
	t.Helper()
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)
	return tmpl
}

// TestGolden_DataMatchesUpstream pins the Data model itself: status, receiver,
// the three KV maps and the per-alert fields, all against upstream's output for
// the same fixture.
func TestGolden_DataMatchesUpstream(t *testing.T) {
	data := mixedData()

	assert.Equal(t, "team-x", data.Receiver)
	assert.Equal(t, "firing", data.Status, "group status is firing when ANY alert fires")
	assert.Equal(t, goldenExternalURL, data.ExternalURL)

	assert.Equal(t, KV{"alertname": "HighCPU", "severity": "critical"}, data.GroupLabels)
	assert.Equal(t, KV{"alertname": "HighCPU", "job": "node", "severity": "critical"}, data.CommonLabels,
		"instance differs across the set and must drop out of the intersection")
	assert.Equal(t, KV{"runbook_url": "http://runbook.example/cpu"}, data.CommonAnnotations,
		"summary differs per instance and must drop out of the intersection")

	require.Len(t, data.Alerts, 3)
	assert.Len(t, data.Alerts.Firing(), 2)
	assert.Len(t, data.Alerts.Resolved(), 1)

	assert.Equal(t, goldenFingerprintServer1, data.Alerts[0].Fingerprint)
	assert.Equal(t, goldenFingerprintServer2, data.Alerts[1].Fingerprint)
	assert.Equal(t, goldenFingerprintServer3, data.Alerts[2].Fingerprint)

	assert.Equal(t, goldenStartsAt, data.Alerts[0].StartsAt)
	assert.True(t, data.Alerts[0].EndsAt.IsZero(),
		"upstream does not expose EndsAt for a firing alert")
	assert.Equal(t, goldenEndsAt, data.Alerts[2].EndsAt,
		"upstream exposes EndsAt once the alert resolved")
	assert.Equal(t, goldenGeneratorURL, data.Alerts[0].GeneratorURL)
}

// TestGolden_DefaultTemplates renders every default definition AMP's five
// delivered integrations need and compares against upstream's own output.
func TestGolden_DefaultTemplates(t *testing.T) {
	tmpl := goldenTemplate(t)
	data := mixedData()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{"__subject", `{{ template "__subject" . }}`, goldenSubjectMixed},
		{"__description", `{{ template "__description" . }}`, ""},
		{"__alertmanager", `{{ template "__alertmanager" . }}`, "Alertmanager"},
		{"__alertmanagerURL", `{{ template "__alertmanagerURL" . }}`, "http://amp.example.com/#/alerts?receiver=team-x"},
		{"__text_alert_list", `{{ template "__text_alert_list" .Alerts.Firing }}`, goldenTextAlertListFiring},
		{"__text_alert_list_markdown", `{{ template "__text_alert_list_markdown" .Alerts.Firing }}`, goldenTextAlertListMarkdownFiring},

		{"slack.default.title", `{{ template "slack.default.title" . }}`, goldenSubjectMixed},
		{"slack.default.text", `{{ template "slack.default.text" . }}`, ""},
		{"slack.default.color", `{{ template "slack.default.color" . }}`, "danger"},
		{"slack.default.username", `{{ template "slack.default.username" . }}`, "Alertmanager"},
		{"slack.default.titlelink", `{{ template "slack.default.titlelink" . }}`, "http://amp.example.com/#/alerts?receiver=team-x"},
		{"slack.default.fallback", `{{ template "slack.default.fallback" . }}`, goldenSubjectMixed + " | http://amp.example.com/#/alerts?receiver=team-x"},

		{"pagerduty.default.description", `{{ template "pagerduty.default.description" . }}`, goldenSubjectMixed},
		{"pagerduty.default.client", `{{ template "pagerduty.default.client" . }}`, "Alertmanager"},
		{"pagerduty.default.clientURL", `{{ template "pagerduty.default.clientURL" . }}`, "http://amp.example.com/#/alerts?receiver=team-x"},
		{"pagerduty.default.instances", `{{ template "pagerduty.default.instances" .Alerts }}`, goldenPagerDutyInstances},

		{"email.default.subject", `{{ template "email.default.subject" . }}`, goldenSubjectMixed},

		{"telegram.default.message", `{{ template "telegram.default.message" . }}`, goldenTelegramMessageMixed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tmpl.ExecuteTextString(tc.expr, data)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestGolden_SingleAlertFixture covers the other shape of `__subject` (a group
// whose CommonLabels are a strict superset of a single-label GroupLabels) and
// the Resolved-branch-absent telegram message.
func TestGolden_SingleAlertFixture(t *testing.T) {
	tmpl := goldenTemplate(t)
	data := singleData()

	assert.Equal(t, KV{"alertname": "HighCPU", "instance": "server-1", "job": "node", "severity": "critical"}, data.CommonLabels,
		"a single-alert group has every label in common with itself")
	assert.Equal(t, KV{"runbook_url": "http://runbook.example/cpu", "summary": "CPU high on server-1"}, data.CommonAnnotations)

	got, err := tmpl.ExecuteTextString(`{{ template "__subject" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, goldenSubjectSingle, got)

	got, err = tmpl.ExecuteTextString(`{{ template "telegram.default.message" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, goldenTelegramMessageSingle, got)
}

// TestGolden_QuoteMetaReceiver pins upstream's regexp.QuoteMeta of the receiver
// name — an upstream quirk, reproduced on purpose so a migrated config renders
// identically. A receiver named `team.a` becomes `team\.a`, and urlquery then
// escapes the backslash to `%5C`.
func TestGolden_QuoteMetaReceiver(t *testing.T) {
	tmpl := goldenTemplate(t)
	data := singleData()

	assert.Equal(t, `team\.a`, data.Receiver)

	got, err := tmpl.ExecuteTextString(`{{ template "__alertmanagerURL" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "http://amp.example.com/#/alerts?receiver=team%5C.a", got)
}

// TestGolden_ResolvedOnlyGroupStatus checks the other branch of the group
// status and of slack.default.color: an all-resolved group is "resolved", which
// upstream renders without the ":<count>" suffix and in green.
func TestGolden_ResolvedOnlyGroupStatus(t *testing.T) {
	tmpl := goldenTemplate(t)
	data := BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Alerts:      fixtureAlerts()[2:],
		ExternalURL: goldenExternalURL,
	})

	require.Equal(t, "resolved", data.Status)

	subject, err := tmpl.ExecuteTextString(`{{ template "__subject" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "[RESOLVED] HighCPU critical (server-3 node)", subject,
		"no :count suffix for a resolved group; instance+job remain in the parenthesised remainder")

	color, err := tmpl.ExecuteTextString(`{{ template "slack.default.color" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "good", color)

	message, err := tmpl.ExecuteTextString(`{{ template "telegram.default.message" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "\n\n\nAlerts Resolved:\n"+
		"Labels:\n - alertname = HighCPU\n - instance = server-3\n - job = node\n - severity = critical\n"+
		"Annotations:\n - runbook_url = http://runbook.example/cpu\n - summary = CPU high on server-3\n"+
		"Source: http://prometheus.example/graph?g0.expr=up\n\n\n", message,
		"the Firing branch collapses to a single leading newline when no alert fires")
}

// TestGolden_EmailHTMLRenders exercises the html/template instance on the one
// definition that needs it. The full email body is not pinned byte-for-byte
// (it is ~10 KB of upstream's mailgun-derived markup, embedded verbatim — the
// checksum in templates/NOTICE is the authority on it not drifting); what
// matters here is that it renders through the HTML variant, carries the
// subject, and escapes label values.
func TestGolden_EmailHTMLRenders(t *testing.T) {
	tmpl := goldenTemplate(t)

	alerts := fixtureAlerts()
	alerts[0].Labels["instance"] = `<script>alert("xss")</script>`

	data := BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU"},
		Alerts:      alerts[:1],
		ExternalURL: goldenExternalURL,
	})

	body, err := tmpl.ExecuteHTMLDefinition("email.default.html", data)
	require.NoError(t, err)
	assert.Contains(t, body, "<!DOCTYPE html")
	assert.NotContains(t, body, `<script>alert("xss")</script>`,
		"html/template must escape a label value carrying markup")
	assert.Contains(t, body, "&lt;script&gt;")
}

// TestGolden_UnicodeLabelsAndAnnotations closes a golden coverage gap flagged in
// review (Minor 1): every earlier fixture is ASCII, yet real deployments label
// alerts in cyrillic/CJK and put emoji in annotations. Upstream's output for this
// fixture was captured by the same generator; note that `__subject` sorts
// `сервер-1` before `东京` (byte order over UTF-8, not locale collation) and that
// the receiver is percent-encoded in the URL.
func TestGolden_UnicodeLabelsAndAnnotations(t *testing.T) {
	tmpl := goldenTemplate(t)

	generatorURL := goldenGeneratorURL
	data := BuildData(DataInput{
		Receiver:    "команда-х",
		GroupLabels: map[string]string{"alertname": "ДискЗаполнен", "severity": "критично"},
		Alerts: []*core.Alert{{
			AlertName: "ДискЗаполнен",
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": "ДискЗаполнен",
				"severity":  "критично",
				"instance":  "сервер-1",
				"区域":        "东京",
			},
			Annotations: map[string]string{
				"summary":  "Диск заполнен на 95% 🔥",
				"описание": "требуется вмешательство",
			},
			StartsAt:     goldenStartsAt,
			GeneratorURL: &generatorURL,
		}},
		ExternalURL: goldenExternalURL,
	})

	assert.Equal(t, "6d1b9e68b3ab396f", data.Alerts[0].Fingerprint,
		"upstream fingerprints UTF-8 label values byte-wise")

	const wantSubject = "[FIRING:1] ДискЗаполнен критично (сервер-1 东京)"
	const wantURL = "http://amp.example.com/#/alerts?receiver=%D0%BA%D0%BE%D0%BC%D0%B0%D0%BD%D0%B4%D0%B0-%D1%85"

	cases := map[string]string{
		`{{ template "__subject" . }}`:              wantSubject,
		`{{ template "slack.default.title" . }}`:    wantSubject,
		`{{ template "email.default.subject" . }}`:  wantSubject,
		`{{ template "__alertmanagerURL" . }}`:      wantURL,
		`{{ template "slack.default.fallback" . }}`: wantSubject + " | " + wantURL,
		`{{ template "telegram.default.message" . }}`: "\n\nAlerts Firing:\n" +
			"Labels:\n - alertname = ДискЗаполнен\n - instance = сервер-1\n - severity = критично\n - 区域 = 东京\n" +
			"Annotations:\n - summary = Диск заполнен на 95% 🔥\n - описание = требуется вмешательство\n" +
			"Source: http://prometheus.example/graph?g0.expr=up\n\n\n\n",
	}

	for expr, want := range cases {
		got, err := tmpl.ExecuteTextString(expr, data)
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}
}

// TestGolden_DisjointGroupHasEmptyCommonMaps closes the second coverage gap
// (review Minor 1): a group whose alerts share nothing. Both Common* maps are
// empty, and `__subject` renders its no-remainder branch — which upstream emits
// with TWO trailing spaces ("[FIRING:2]  "), one from the empty GroupLabels join
// and one from the omitted parenthesis group. Pinning that exact whitespace is
// the point.
func TestGolden_DisjointGroupHasEmptyCommonMaps(t *testing.T) {
	tmpl := goldenTemplate(t)

	data := BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{},
		Alerts: []*core.Alert{
			{
				AlertName: "A", Status: core.StatusFiring,
				Labels: map[string]string{"alertname": "A", "x": "1"}, Annotations: map[string]string{"a": "1"},
				StartsAt: goldenStartsAt,
			},
			{
				AlertName: "B", Status: core.StatusFiring,
				Labels: map[string]string{"alertname": "B", "y": "2"}, Annotations: map[string]string{"b": "2"},
				StartsAt: goldenStartsAt,
			},
		},
		ExternalURL: goldenExternalURL,
	})

	assert.Empty(t, data.CommonLabels)
	assert.Empty(t, data.CommonAnnotations)
	assert.NotNil(t, data.CommonLabels, "empty, never nil")

	got, err := tmpl.ExecuteTextString(`{{ template "__subject" . }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "[FIRING:2]  ", got)

	// GeneratorURL is unset on both alerts, so upstream renders a bare "Source: ".
	got, err = tmpl.ExecuteTextString(`{{ template "__text_alert_list" .Alerts }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "Labels:\n - alertname = A\n - x = 1\nAnnotations:\n - a = 1\nSource: \n"+
		"Labels:\n - alertname = B\n - y = 2\nAnnotations:\n - b = 2\nSource: \n", got)
}

// TestGolden_FiringListOnAllResolvedGroup pins the empty-partition case (review
// Minor 1): `.Alerts.Firing` on an all-resolved group renders "" for the plain
// list and a single newline for the markdown one.
func TestGolden_FiringListOnAllResolvedGroup(t *testing.T) {
	tmpl := goldenTemplate(t)
	data := BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Alerts:      fixtureAlerts()[2:],
		ExternalURL: goldenExternalURL,
	})

	got, err := tmpl.ExecuteTextString(`{{ template "__text_alert_list" .Alerts.Firing }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "", got)

	got, err = tmpl.ExecuteTextString(`{{ template "__text_alert_list_markdown" .Alerts.Firing }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "\n", got)
}

// TestGolden_DefaultLibraryDefinitionsPresent guards against a truncated or
// half-embedded copy of upstream's library: the definitions AMP's integrations
// depend on must all exist after FromGlobs(nil).
func TestGolden_DefaultLibraryDefinitionsPresent(t *testing.T) {
	tmpl := goldenTemplate(t)

	for _, name := range []string{
		"__alertmanager", "__alertmanagerURL", "__subject", "__description",
		"__text_alert_list", "__text_alert_list_markdown",
		"slack.default.title", "slack.default.text", "slack.default.color",
		"slack.default.fallback", "slack.default.titlelink", "slack.default.username",
		"pagerduty.default.description", "pagerduty.default.client",
		"pagerduty.default.clientURL", "pagerduty.default.instances",
		"email.default.subject", "email.default.html",
		"telegram.default.message",
	} {
		assert.True(t, tmpl.HasDefinition(name), "missing default definition %q", name)
	}
}
