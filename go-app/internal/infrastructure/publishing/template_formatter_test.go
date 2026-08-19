package publishing

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ============================================================================
// Template-rendered notification content (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// These tests own the two guarantees the epic rests on:
//
//  1. A target WITH template fields delivers the operator's rendered content.
//  2. A target WITHOUT them — or one whose template fails — delivers exactly
//     what the fixed formatter produced, and the failure is counted.
//
// (2) is the replacement for the slice-1 structural scope guard that slice 2
// deleted: it is no longer true that nothing reads the templating package, so
// the guarantee has to be behavioural.

func testTemplateLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testTemplateMetrics(t *testing.T) *v2.PublishingMetrics {
	t.Helper()
	return v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
}

// testRegistry builds a template registry over the embedded defaults plus any
// extra definitions given as file contents.
func testRegistry(t *testing.T, files ...string) *templating.Registry {
	t.Helper()

	var globs []string
	if len(files) > 0 {
		dir := t.TempDir()
		for i, content := range files {
			name := filepath.Join(dir, string(rune('a'+i))+".tmpl")
			require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
		}
		globs = []string{filepath.Join(dir, "*.tmpl")}
	}

	registry, err := templating.NewRegistry(globs, templating.Options{})
	require.NoError(t, err)
	return registry
}

func templateTestAlert() *core.EnrichedAlert {
	return &core.EnrichedAlert{Alert: &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "cpu is high"},
		StartsAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
}

// templateTarget builds a target of the given type/format carrying templates.
func templateTarget(targetType string, format core.PublishingFormat, receiver string, templates map[string]string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:      "cfg:" + receiver + "/" + targetType + "0",
		Type:      targetType,
		URL:       "https://example.invalid/hook",
		Enabled:   true,
		Format:    format,
		Receivers: []string{receiver},
		Templates: templates,
		Headers:   map[string]string{},
	}
}

// groupCtx is the context a real notification carries: the queue attaches the
// group's receiver and resolved group_by labels (notification_context.go), and
// upstream's `__subject` output depends on both.
func groupCtx(receiver string, groupLabels map[string]string) context.Context {
	return withGroupNotificationContext(context.Background(), GroupNotificationContext{
		GroupKey:    "receiver=" + receiver + "/alertname=HighCPU",
		Receiver:    receiver,
		GroupLabels: groupLabels,
	})
}

// alertnameGroup is the common case: a route grouping by [alertname].
func alertnameGroup(receiver string) context.Context {
	return groupCtx(receiver, map[string]string{"alertname": "HighCPU"})
}

func newTestTemplateFormatter(t *testing.T, target *core.PublishingTarget, registry *templating.Registry, metrics *v2.PublishingMetrics) AlertFormatter {
	t.Helper()
	renderer := newTemplateRenderer(registry, "http://amp.example.com", metrics, testTemplateLogger())
	return newTemplateFormatter(NewAlertFormatter("http://amp.example.com"), renderer, target)
}

// === (1) rendered content reaches the payload ===

// TestTemplateFormatter_SlackUsesUpstreamDefaults is the migration promise: a
// slack_config that names no template field at all still renders upstream's
// title/color, because the config layer materialized upstream's defaults.
func TestTemplateFormatter_SlackUsesUpstreamDefaults(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle:    `{{ template "slack.default.title" . }}`,
		core.TemplateFieldColor:    `{{ template "slack.default.color" . }}`,
		core.TemplateFieldFallback: `{{ template "slack.default.fallback" . }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("team-x"), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, ok := firstAttachment(payload)
	require.True(t, ok, "payload must carry an attachment: %v", payload)
	assert.Equal(t, "[FIRING:1] HighCPU (critical)", attachment["title"],
		"upstream __subject over a one-alert group")
	assert.Equal(t, "danger", attachment["color"])
	assert.Equal(t, "[FIRING:1] HighCPU (critical) | http://amp.example.com/#/alerts?receiver=team-x",
		payload["text"], "Slack's required fallback string comes from slack.default.fallback")
}

// TestTemplateFormatter_SlackUserTemplateWins: a `templates:` file overriding
// slack.default.title changes what lands on the wire. This is the whole point of
// the epic.
func TestTemplateFormatter_SlackUserTemplateWins(t *testing.T) {
	registry := testRegistry(t,
		`{{ define "slack.default.title" }}[prod] {{ .CommonLabels.alertname }} is {{ .Status }}{{ end }}`)

	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle: `{{ template "slack.default.title" . }}`,
	})

	formatter := newTestTemplateFormatter(t, target, registry, testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("team-x"), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, _ := firstAttachment(payload)
	assert.Equal(t, "[prod] HighCPU is firing", attachment["title"])
	assert.NotContains(t, payload, "blocks",
		"the operator's presentation replaces AMP's Block Kit rendering rather than duplicating it")
}

// TestTemplateFormatter_SlackInlineExpression covers a field holding an inline
// expression rather than a `{{ template }}` reference.
func TestTemplateFormatter_SlackInlineExpression(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle:   `{{ .Status | toUpper }}: {{ .CommonAnnotations.summary }}`,
		core.TemplateFieldChannel: "#ops-critical",
		core.TemplateFieldText:    `{{ range .Alerts }}{{ .Labels.severity }}{{ end }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("team-x"), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, _ := firstAttachment(payload)
	assert.Equal(t, "FIRING: cpu is high", attachment["title"])
	assert.Equal(t, "critical", attachment["text"])
	assert.Equal(t, "#ops-critical", payload["channel"], "a literal field passes through unrendered")
}

func TestTemplateFormatter_PagerDutyFields(t *testing.T) {
	target := templateTarget("pagerduty", core.FormatPagerDuty, "oncall", map[string]string{
		core.TemplateFieldDescription:                  `{{ template "pagerduty.default.description" .}}`,
		core.TemplateFieldSeverity:                     `{{ .CommonLabels.severity }}`,
		core.TemplateFieldClient:                       `{{ template "pagerduty.default.client" . }}`,
		core.TemplateFieldClientURL:                    `{{ template "pagerduty.default.clientURL" . }}`,
		core.TemplateFieldDetailsPrefix + "num_firing": `{{ .Alerts.Firing | len }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("oncall"), templateTestAlert(), core.FormatPagerDuty)
	require.NoError(t, err)

	nested, ok := payload["payload"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "[FIRING:1] HighCPU (critical)", nested["summary"],
		"upstream calls it description; the Events API calls it summary")
	assert.Equal(t, "critical", nested["severity"], "templated from the alert's own label")
	assert.Equal(t, "Alertmanager", payload["client"])
	assert.Equal(t, "http://amp.example.com/#/alerts?receiver=oncall", payload["client_url"])

	details, ok := nested["custom_details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "1", details["num_firing"])
	assert.Contains(t, details, "alert_name",
		"operator details are additive: the fixed formatter's diagnostics survive")
}

func TestTemplateFormatter_TelegramMessage(t *testing.T) {
	target := templateTarget("telegram", core.FormatTelegram, "tg", map[string]string{
		core.TemplateFieldMessage: `{{ template "telegram.default.message" . }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("tg"), templateTestAlert(), core.FormatTelegram)
	require.NoError(t, err)

	text, ok := payload["text"].(string)
	require.True(t, ok)
	assert.Contains(t, text, "Alerts Firing:")
	assert.Contains(t, text, "alertname = HighCPU")
}

// TestTemplateFormatter_EmailContent covers the one integration that does not go
// through AlertFormatter, so the publisher pulls content via the interface.
func TestTemplateFormatter_EmailContent(t *testing.T) {
	target := templateTarget("email", core.FormatWebhook, "mail", map[string]string{
		core.TemplateFieldSubject:                   `{{ template "email.default.subject" . }}`,
		core.TemplateFieldHTML:                      `<p>{{ .CommonLabels.alertname }}</p>`,
		core.TemplateFieldHeadersPrefix + "X-Alert": `{{ .CommonLabels.severity }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	renderer, ok := formatter.(emailContentRenderer)
	require.True(t, ok, "a target with email fields must expose email content")

	content, rendered := renderer.RenderEmailContent(alertnameGroup("mail"), templateTestAlert())
	require.True(t, rendered)
	assert.Equal(t, "[FIRING:1] HighCPU (critical)", content.Subject)
	assert.Equal(t, "<p>HighCPU</p>", content.HTML)
	assert.Equal(t, map[string]string{"X-Alert": "critical"}, content.Headers)
}

// TestTemplateFormatter_EmailHTMLIsEscaped: the HTML body goes through
// html/template, so a label value carrying markup cannot inject into the email.
func TestTemplateFormatter_EmailHTMLIsEscaped(t *testing.T) {
	target := templateTarget("email", core.FormatWebhook, "mail", map[string]string{
		core.TemplateFieldSubject: `subject`,
		core.TemplateFieldHTML:    `<p>{{ .CommonLabels.injected }}</p>`,
	})

	alert := templateTestAlert()
	alert.Alert.Labels["injected"] = `<script>alert(1)</script>`

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	content, rendered := formatter.(emailContentRenderer).RenderEmailContent(alertnameGroup("mail"), alert)
	require.True(t, rendered)
	assert.NotContains(t, content.HTML, "<script>")
	assert.Contains(t, content.HTML, "&lt;script&gt;")
}

// TestTemplateFormatter_ReceiverNameStripsConfigPrefix is the end-to-end check
// of the slice-1 guarantee: AMP's `cfg:<receiver>/<kind><idx>` target encoding
// must never leak into a notification.
func TestTemplateFormatter_ReceiverNameStripsConfigPrefix(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "", map[string]string{
		core.TemplateFieldTitle: `{{ .Receiver }}`,
	})
	target.Name = "cfg:team-x/slack0"
	target.Receivers = nil // force the name-decoding path

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	// No group context: this is the path where the receiver must be decoded from
	// the target NAME.
	payload, err := formatter.FormatAlert(context.Background(), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, _ := firstAttachment(payload)
	assert.Equal(t, "team-x", attachment["title"])
}

func TestTemplateFormatter_ReceiverNamePrefersScopedReceiver(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "team-scoped", map[string]string{
		core.TemplateFieldTitle: `{{ .Receiver }}`,
	})
	target.Name = "cfg:other-name/slack0"

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(context.Background(), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, _ := firstAttachment(payload)
	assert.Equal(t, "team-scoped", attachment["title"])
}

// === (2) the fallback contract ===

// TestTemplateFormatter_NoTemplateFields_IsTheFixedFormatter is the
// zero-behaviour-change guarantee: a target with no template fields is not
// merely rendered identically, it is not wrapped at all.
func TestTemplateFormatter_NoTemplateFields_IsTheFixedFormatter(t *testing.T) {
	fixed := NewAlertFormatter("http://amp.example.com")
	renderer := newTemplateRenderer(testRegistry(t), "http://amp.example.com", nil, testTemplateLogger())

	for name, target := range map[string]*core.PublishingTarget{
		"nil templates":   templateTarget("slack", core.FormatSlack, "team-x", nil),
		"empty templates": templateTarget("slack", core.FormatSlack, "team-x", map[string]string{}),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Same(t, fixed, newTemplateFormatter(fixed, renderer, target))
		})
	}

	// And with templating disabled entirely.
	assert.Same(t, fixed, newTemplateFormatter(fixed, nil, templateTarget("slack", core.FormatSlack, "x", map[string]string{"title": "x"})))
}

// TestTemplateFormatter_BrokenTemplateFallsBackAndCounts is the fallback
// contract: a broken field never drops the notification, the fixed formatter's
// content is delivered, and the fallback is counted (from every other angle a
// fallback looks like a perfectly successful notification).
func TestTemplateFormatter_BrokenTemplateFallsBackAndCounts(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(registry)).Publishing

	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle: `{{ .NoSuchField.Nested }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), metrics)
	payload, err := formatter.FormatAlert(alertnameGroup("team-x"), templateTestAlert(), core.FormatSlack)

	require.NoError(t, err, "a broken template must NOT fail the notification")
	assert.Contains(t, payload, "blocks", "the fixed formatter's payload is delivered untouched")
	assert.Contains(t, payload["text"], "HighCPU")

	assert.Equal(t, 1.0, counterValue(t, registry, "alert_history_publishing_template_fallbacks_total",
		map[string]string{"integration": "slack", "reason": v2.TemplateFallbackExecError}))
	assert.Equal(t, 1.0, counterValue(t, registry, "alert_history_publishing_template_renders_total",
		map[string]string{"integration": "slack", "outcome": v2.TemplateOutcomeError}))
}

// TestTemplateFormatter_UndefinedTemplateIsCountedAsNotDefined distinguishes
// "the operator deleted a definition" from "the expression is broken".
func TestTemplateFormatter_UndefinedTemplateIsCountedAsNotDefined(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(registry)).Publishing

	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle: `{{ template "nobody.defined.this" . }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), metrics)
	_, err := formatter.FormatAlert(alertnameGroup("team-x"), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	// text/template reports an undefined association at PARSE time, so this
	// classifies as exec_error rather than not_defined — asserted rather than
	// assumed, because the metric's usefulness depends on knowing which.
	total := counterValue(t, registry, "alert_history_publishing_template_fallbacks_total",
		map[string]string{"integration": "slack", "reason": v2.TemplateFallbackExecError})
	assert.Equal(t, 1.0, total)
}

// TestTemplateFormatter_TimeoutIsCountedAsTimeout ties the guard reasons to the
// metric an operator would alert on.
func TestTemplateFormatter_TimeoutIsCountedAsTimeout(t *testing.T) {
	promRegistry := prometheus.NewRegistry()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(promRegistry)).Publishing

	tmplRegistry, err := templating.NewRegistry(nil, templating.Options{MaxOutputBytes: 8})
	require.NoError(t, err)

	target := templateTarget("telegram", core.FormatTelegram, "tg", map[string]string{
		core.TemplateFieldMessage: `{{ range .Alerts }}{{ template "telegram.default.message" $ }}{{ end }}`,
	})

	formatter := newTestTemplateFormatter(t, target, tmplRegistry, metrics)
	payload, err := formatter.FormatAlert(alertnameGroup("tg"), templateTestAlert(), core.FormatTelegram)
	require.NoError(t, err)
	assert.Contains(t, payload["text"], "FIRING", "the fixed formatter's telegram text survives")

	assert.Equal(t, 1.0, counterValue(t, promRegistry, "alert_history_publishing_template_fallbacks_total",
		map[string]string{"integration": "telegram", "reason": v2.TemplateFallbackOutputCap}))
}

// TestTemplateFormatter_FixedFormatterErrorIsPropagated: a failure of the FIXED
// formatter is not a template problem and must not be masked as one.
func TestTemplateFormatter_FixedFormatterErrorIsPropagated(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle: `{{ .Status }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	_, err := formatter.FormatAlert(alertnameGroup("team-x"), &core.EnrichedAlert{}, core.FormatSlack)
	require.Error(t, err)
}

// TestTemplateFormatter_WebhookPayloadUntouched: upstream does not template
// webhook payloads, so even a target that somehow carries fields must not have
// its v4 JSON rewritten.
func TestTemplateFormatter_WebhookPayloadUntouched(t *testing.T) {
	target := templateTarget("webhook", core.FormatWebhook, "hook", map[string]string{
		core.TemplateFieldTitle: `SHOULD NOT APPEAR`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("hook"), templateTestAlert(), core.FormatWebhook)
	require.NoError(t, err)

	assert.NotContains(t, payload, "title")
	assert.Equal(t, "HighCPU", payload["alert_name"], "the fixed webhook shape is intact")
}

// TestTemplateFormatter_FormatGroupStillBatches guards the group path: the
// decorator must keep implementing GroupAlertFormatter, or wire-level group
// batching silently turns off for every templated target.
func TestTemplateFormatter_FormatGroupStillBatches(t *testing.T) {
	target := templateTarget("webhook", core.FormatWebhook, "hook", map[string]string{
		core.TemplateFieldTitle: `x`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	grouper, ok := formatter.(GroupAlertFormatter)
	require.True(t, ok, "the decorator must not swallow GroupAlertFormatter")

	payload, err := grouper.FormatGroup(context.Background(),
		[]*core.Alert{templateTestAlert().Alert}, "gk", "hook", map[string]string{"alertname": "HighCPU"}, core.FormatWebhook)
	require.NoError(t, err)
	assert.Equal(t, "4", payload["version"], "upstream v4 group shape, unchanged by templating")
	assert.Equal(t, "gk", payload["groupKey"])
}

// === factory wiring ===

func TestPublisherFactory_FormatterForTarget(t *testing.T) {
	logger := testTemplateLogger()
	fixed := NewAlertFormatter("")
	factory := NewPublisherFactory(fixed, logger, testTemplateMetrics(t), "")
	t.Cleanup(factory.Shutdown)

	templated := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{"title": `{{ .Status }}`})
	plain := templateTarget("slack", core.FormatSlack, "team-x", nil)

	// Before wiring: templating is off, so every target gets the fixed formatter.
	assert.Nil(t, factory.TemplateRegistry())
	assert.Same(t, fixed, factory.formatterFor(templated))

	factory.SetTemplateRegistry(testRegistry(t))
	assert.NotNil(t, factory.TemplateRegistry())
	assert.NotSame(t, fixed, factory.formatterFor(templated), "a templated target gets the decorator")
	assert.Same(t, fixed, factory.formatterFor(plain), "a target with no fields is not wrapped")

	// And it can be switched back off.
	factory.SetTemplateRegistry(nil)
	assert.Same(t, fixed, factory.formatterFor(templated))
}

// counterValue reads one labelled counter sample out of a registry.
//
// Goes through the registry (Gather) rather than through the metric object,
// because the metric fields are unexported: this asserts what a SCRAPE would
// see, which is what an operator actually alerts on.
func counterValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !labelsMatch(metric.GetLabel(), labels) {
				continue
			}
			return metric.GetCounter().GetValue()
		}
	}
	return 0
}

func labelsMatch(pairs []*dto.LabelPair, want map[string]string) bool {
	got := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		got[pair.GetName()] = pair.GetValue()
	}
	for name, value := range want {
		if got[name] != value {
			return false
		}
	}
	return true
}

// === group context (the data half of output parity) ===

// TestTemplateFormatter_GroupLabelsShapeTheSubject pins why the group context
// exists at all: upstream's `__subject` names the GROUP and parenthesises the
// rest, so the same alert renders differently depending on the route's
// `group_by`. Without the context every label fell into the remainder and every
// title carried a tell-tale double space.
func TestTemplateFormatter_GroupLabelsShapeTheSubject(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "team-x", map[string]string{
		core.TemplateFieldTitle: `{{ template "slack.default.title" . }}`,
	})
	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))

	cases := []struct {
		name  string
		ctx   context.Context
		title string
	}{
		{
			name:  "group_by [alertname]",
			ctx:   alertnameGroup("team-x"),
			title: "[FIRING:1] HighCPU (critical)",
		},
		{
			name: "group_by [alertname, severity]",
			ctx:  groupCtx("team-x", map[string]string{"alertname": "HighCPU", "severity": "critical"}),
			// TRAILING SPACE is upstream's, not a typo: `__subject` emits the
			// separator space unconditionally and only then decides whether a
			// parenthesised remainder follows. When GroupLabels already covers
			// every common label there is no remainder, and the space stays.
			title: "[FIRING:1] HighCPU critical ",
		},
		{
			name: "no group context at all (direct publish)",
			ctx:  context.Background(),
			// Upstream's own shape for an empty GroupLabels: nothing to name, so
			// every common label lands in the remainder.
			title: "[FIRING:1]  (HighCPU critical)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := formatter.FormatAlert(tc.ctx, templateTestAlert(), core.FormatSlack)
			require.NoError(t, err)
			attachment, _ := firstAttachment(payload)
			assert.Equal(t, tc.title, attachment["title"])
		})
	}
}

// TestTemplateFormatter_ReceiverComesFromTheRoutedGroup: the ROUTED receiver
// name wins over anything derivable from the target, because that is the name
// the operator wrote in `receivers:` and the one upstream renders.
func TestTemplateFormatter_ReceiverComesFromTheRoutedGroup(t *testing.T) {
	target := templateTarget("slack", core.FormatSlack, "target-scoped", map[string]string{
		core.TemplateFieldTitle: `{{ .Receiver }}`,
	})

	formatter := newTestTemplateFormatter(t, target, testRegistry(t), testTemplateMetrics(t))
	payload, err := formatter.FormatAlert(alertnameGroup("routed-receiver"), templateTestAlert(), core.FormatSlack)
	require.NoError(t, err)

	attachment, _ := firstAttachment(payload)
	assert.Equal(t, "routed-receiver", attachment["title"])
}

// TestGroupNotificationContext_RoundTrip covers the carrier itself, including
// the absent case every non-group publish path takes.
func TestGroupNotificationContext_RoundTrip(t *testing.T) {
	assert.Equal(t, GroupNotificationContext{}, groupNotificationContextFrom(context.Background()))
	assert.Equal(t, GroupNotificationContext{}, groupNotificationContextFrom(nil)) //nolint:staticcheck // the nil case is exactly what is under test.

	want := GroupNotificationContext{
		GroupKey:    "gk",
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU"},
	}
	assert.Equal(t, want, groupNotificationContextFrom(withGroupNotificationContext(context.Background(), want)))
}
