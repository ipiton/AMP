package application

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// TEMPLATES-EPIC slice 2, item 8: configured templates reach the WIRE
// ============================================================================
//
// The epic's whole claim, end to end and asserted on the wire: an operator's
// `templates:` file plus a per-integration presentation field changes what the
// receiving service actually receives.
//
// Every layer is the real one — the template registry loaded from a file on
// disk, BuildConfigTargets, the config-only discovery view, the group_wait timer
// and notify chain, the coordinator, the queue worker pool and
// EnhancedSlackPublisher — with only the HTTP endpoint replaced by an httptest
// server. A test that stopped at "the formatter returned a map" would not have
// caught a payload that the publisher then discards, which is exactly the class
// of bug this asserts against.
//
// It reuses the slice-1 harness from config_only_delivery_e2e_test.go
// (newStackFromRouting + recordingEndpoint), with templates wired in through
// withTemplateRegistry — the same call production makes in
// initializePublishingRuntime.

// writeTemplateLibrary writes a `templates:`-style file and returns a registry
// loaded from it, exactly as internal/config + ServiceRegistry would.
func writeTemplateLibrary(t *testing.T, content string) *templating.Registry {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.tmpl"), []byte(content), 0o600))

	registry, err := templating.NewRegistry([]string{filepath.Join(dir, "*.tmpl")}, templating.Options{})
	require.NoError(t, err)
	return registry
}

// withTemplatingFromConfig drives production's OWN decision path instead of
// handing the harness a registry directly: it runs ServiceRegistry.initializeTemplating
// against the given publishing.templates config and wires whatever that produced,
// exactly as initializePublishingRuntime does (`if registry != nil`).
//
// That makes the kill switch testable ON THE WIRE in both states, which a
// hand-passed registry cannot do — the switch lives in initializeTemplating.
func withTemplatingFromConfig(t *testing.T, templates appconfig.PublishingTemplateConfig, globs []string) stackOption {
	t.Helper()

	registry := &ServiceRegistry{
		logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn})),
		config:          &appconfig.Config{Routing: &infraroute.RouteConfig{Templates: globs}},
		degradedReasons: make([]string, 0, 4),
	}
	registry.config.Publishing.Templates = templates
	registry.initializeTemplating()

	// nil is meaningful: it is what production hands the factory when templating
	// is off, and the harness skips SetTemplateRegistry for it just as
	// initializePublishingRuntime does.
	return func(s *stackSettings) { s.templates = registry.TemplateRegistry() }
}

func slackRoutingConfig(apiURL string) *infraroute.RouteConfig {
	return &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{{
			Name: "team-x",
			SlackConfigs: []*infraroute.SlackConfig{{
				APIURL:  apiURL,
				Channel: "#ops",
			}},
		}},
	}
}

func alertnameGroupingConfig() *grouping.GroupingConfig {
	return &grouping.GroupingConfig{
		Route: &grouping.Route{
			Receiver:       "team-x",
			GroupBy:        []string{"alertname"},
			GroupWait:      &grouping.Duration{Duration: 40 * time.Millisecond},
			GroupInterval:  &grouping.Duration{Duration: 200 * time.Millisecond},
			RepeatInterval: &grouping.Duration{Duration: time.Hour},
		},
	}
}

// TestTemplatesDelivery_CustomTitleReachesTheWire is the headline assertion: a
// `templates:` file that redefines `slack.default.title` changes the title Slack
// receives.
//
// The slack_config names no title of its own, so the value being rendered is
// upstream's DEFAULT (`{{ template "slack.default.title" . }}`), materialized by
// BuildConfigTargets — which is what makes an untouched upstream config pick up
// an operator's template library.
func TestTemplatesDelivery_CustomTitleReachesTheWire(t *testing.T) {
	slack := newRecordingEndpoint(t)

	registry := writeTemplateLibrary(t,
		`{{ define "slack.default.title" }}[MYORG] {{ .GroupLabels.alertname }} / {{ .Status | toUpper }} / {{ .Receiver }}{{ end }}`)

	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig(), withTemplateRegistry(registry))

	require.Equal(t, "cfg:team-x/slack0", stack.discovery.ListTargets()[0].Name)

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	attachments, ok := got.body["attachments"].([]any)
	require.True(t, ok, "slack payload must carry attachments: %#v", got.body)
	require.NotEmpty(t, attachments)
	attachment, ok := attachments[0].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "[MYORG] HighCPU / FIRING / team-x", attachment["title"],
		"the operator's template — with the ROUTED receiver and the group's labels — is what reaches Slack")

	// `cfg:` is AMP's internal target-name encoding and must never appear.
	assert.NotContains(t, attachment["title"], "cfg:")

	// The operator's presentation replaces AMP's Block Kit rendering rather than
	// shipping both versions in one message.
	assert.NotContains(t, got.body, "blocks")
	assert.Equal(t, "#ops", got.body["channel"], "a literal config field still reaches the wire")
}

// TestTemplatesDelivery_PerIntegrationFieldReachesTheWire covers the other half
// of the epic: a `title:` written directly on the slack_config (an inline
// expression, no template library involved). This is FU-INTEGRATION-FIELD-
// FIDELITY closing — before slice 2 this field was parsed, validated and then
// ignored.
func TestTemplatesDelivery_PerIntegrationFieldReachesTheWire(t *testing.T) {
	slack := newRecordingEndpoint(t)

	routing := slackRoutingConfig(slack.server.URL + "/services/T/B/C")
	routing.Receivers[0].SlackConfigs[0].Title = `{{ .CommonLabels.severity | toUpper }}: {{ .CommonAnnotations.summary }}`
	routing.Receivers[0].SlackConfigs[0].Color = "danger"

	stack := newStackFromRouting(t, routing, "team-x", alertnameGroupingConfig(),
		withTemplateRegistry(writeTemplateLibrary(t, `{{ define "unused" }}x{{ end }}`)))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	attachments, _ := got.body["attachments"].([]any)
	require.NotEmpty(t, attachments)
	attachment, _ := attachments[0].(map[string]any)

	assert.Equal(t, "CRITICAL: cpu is high", attachment["title"])
	assert.Equal(t, "danger", attachment["color"])
}

// TestTemplatesDelivery_ExternalURLReachesTheWire pins item 3: the configured
// external URL is what every default template's link renders, all the way to the
// wire.
func TestTemplatesDelivery_ExternalURLReachesTheWire(t *testing.T) {
	slack := newRecordingEndpoint(t)

	registry := writeTemplateLibrary(t,
		`{{ define "slack.default.title" }}{{ template "__alertmanagerURL" . }}{{ end }}`)

	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig(), withTemplateRegistry(registry))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	attachments, _ := got.body["attachments"].([]any)
	require.NotEmpty(t, attachments)
	attachment, _ := attachments[0].(map[string]any)

	assert.Equal(t, externalURLForTests+"/#/alerts?receiver=team-x", attachment["title"])
}

// TestTemplatesDelivery_BrokenTemplateFallsBackOnTheWire is the fallback
// contract asserted where it matters — on the wire, not in a unit:
//
//   - the notification is STILL DELIVERED (never dropped),
//   - what lands is AMP's fixed formatter output (Block Kit blocks),
//   - and the fallback is counted, because from every other angle a fallback
//     looks like a perfectly successful notification.
func TestTemplatesDelivery_BrokenTemplateFallsBackOnTheWire(t *testing.T) {
	slack := newRecordingEndpoint(t)

	routing := slackRoutingConfig(slack.server.URL + "/services/T/B/C")
	routing.Receivers[0].SlackConfigs[0].Title = `{{ .NoSuchField.Nested }}`

	stack := newStackFromRouting(t, routing, "team-x", alertnameGroupingConfig(),
		withTemplateRegistry(writeTemplateLibrary(t, `{{ define "unused" }}x{{ end }}`)))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	assert.Contains(t, got.body, "blocks",
		"a broken template must deliver the fixed formatter's payload, not nothing")
	text, _ := got.body["text"].(string)
	assert.Contains(t, text, "HighCPU")

	assert.Equal(t, 1.0, e2eCounterValue(t, stack.promRegistry,
		"alert_history_publishing_template_fallbacks_total",
		map[string]string{"integration": "slack", "reason": "exec_error"}),
		"the silent-degradation case must be visible in metrics")
}

// ============================================================================
// What PRODUCTION actually delivers (slice-2 review C1)
// ============================================================================
//
// The three tests below replace a test that claimed the epic's default was
// zero-behaviour-change. It is not, and the claim was reachable only because the
// test skipped the registry wiring — a configuration production never produces.
//
// Production materializes upstream's defaults for EVERY config-provisioned
// slack/pagerduty/telegram/email target and always wires the registry, so the
// default output of a `receivers:` deployment that names no template at all is
// UPSTREAM's output, not AMP's. That is the epic's point (drop-in parity) and its
// headline breaking change; these tests pin it, and pin the kill switch that
// reverts it.

// upstreamDefaultsOnTheWire is what the production wiring renders for the
// harness's single firing alert (GroupLabels {alertname}, CommonLabels
// {alertname, cluster, severity}) through upstream's own default definitions.
//
// Same `__subject` shape the slice-1 goldens pin against upstream v0.34.0
// (goldenSubjectSingle: `[FIRING:1] HighCPU (server-1 node critical)` for that
// fixture's labels) — here the parenthesised remainder is CommonLabels minus
// GroupLabels, sorted by label name: cluster=prod, severity=critical.
const (
	upstreamDefaultTitle = "[FIRING:1] HighCPU (prod critical)"
	upstreamDefaultLink  = externalURLForTests + "/#/alerts?receiver=team-x"
	// slack.default.fallback is `{{ template "slack.default.title" . }} | {{ template "slack.default.titlelink" . }}`.
	upstreamDefaultFallback = upstreamDefaultTitle + " | " + upstreamDefaultLink
)

// TestTemplatesDelivery_ProductionDefaultsRenderUpstreamShape is the test the
// review asked for: the PRODUCTION wiring (registry wired, no `templates:` file,
// no presentation field anywhere in the config) and the payload it actually puts
// on the wire.
//
// Every assertion here is a behaviour change against the pre-epic payload asserted
// by TestTemplatesDelivery_NoRegistryWiredIsThePreEpicPayload below: Block Kit is
// gone, and title/title_link/fallback/username are upstream's.
func TestTemplatesDelivery_ProductionDefaultsRenderUpstreamShape(t *testing.T) {
	slack := newRecordingEndpoint(t)

	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig(),
		// No globs, default switch state: precisely what an operator who never
		// heard of `templates:` runs.
		withTemplatingFromConfig(t, appconfig.PublishingTemplateConfig{}, nil))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	attachments, ok := got.body["attachments"].([]any)
	require.True(t, ok, "slack payload must carry attachments: %#v", got.body)
	require.Len(t, attachments, 1)
	attachment, ok := attachments[0].(map[string]any)
	require.True(t, ok)

	// Upstream's attachment presentation, rendered from upstream's own defaults.
	assert.Equal(t, upstreamDefaultTitle, attachment["title"])
	assert.Equal(t, upstreamDefaultLink, attachment["title_link"])
	assert.Equal(t, upstreamDefaultFallback, attachment["fallback"])
	assert.Equal(t, "danger", attachment["color"], "slack.default.color for a firing group")
	assert.NotContains(t, attachment, "pretext",
		"upstream's slack.default.pretext renders empty and must not add an empty key")

	// Message-level fields.
	assert.Equal(t, upstreamDefaultFallback, got.body["text"],
		"the top-level text is upstream's fallback string")
	assert.Equal(t, "Alertmanager", got.body["username"],
		"slack.default.username — a NEW field on every route-based Slack receiver")
	assert.Equal(t, "#ops", got.body["channel"])

	// THE breaking change: AMP's Block Kit rendering is gone by default.
	assert.NotContains(t, got.body, "blocks",
		"upstream renders attachments, so the default payload no longer carries AMP's Block Kit blocks")
}

// TestTemplatesDelivery_KillSwitchRestoresTheFixedFormatter is the other wire
// state of publishing.templates.enabled: false must reproduce the pre-epic payload
// wholesale, for the same config that produced upstream's shape above.
func TestTemplatesDelivery_KillSwitchRestoresTheFixedFormatter(t *testing.T) {
	slack := newRecordingEndpoint(t)

	disabled := false
	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig(),
		withTemplatingFromConfig(t, appconfig.PublishingTemplateConfig{Enabled: &disabled}, nil))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	assert.Contains(t, got.body, "blocks", "AMP's Block Kit rendering is back")
	assert.NotContains(t, got.body, "username", "and upstream's added fields are gone")

	attachments, ok := got.body["attachments"].([]any)
	if ok && len(attachments) > 0 {
		attachment, _ := attachments[0].(map[string]any)
		assert.NotContains(t, attachment, "title",
			"the fixed formatter renders no attachment title")
	}

	text, _ := got.body["text"].(string)
	assert.Contains(t, text, "HighCPU")
}

// TestTemplatesDelivery_KillSwitchIgnoresAConfiguredTemplateFile: with the switch
// off, an operator's `templates:` file must not reach the wire either — the revert
// is wholesale, not "defaults only".
func TestTemplatesDelivery_KillSwitchIgnoresAConfiguredTemplateFile(t *testing.T) {
	slack := newRecordingEndpoint(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.tmpl"),
		[]byte(`{{ define "slack.default.title" }}SHOULD-NOT-SHIP{{ end }}`), 0o600))

	disabled := false
	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig(),
		withTemplatingFromConfig(t, appconfig.PublishingTemplateConfig{Enabled: &disabled},
			[]string{filepath.Join(dir, "*.tmpl")}))

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	assert.NotContains(t, got.body["text"], "SHOULD-NOT-SHIP")
	assert.Contains(t, got.body, "blocks")
}

// TestTemplatesDelivery_PagerDutyDefaultsReachTheWire is the pagerduty
// representative of the same question (slice-2 review I2): the shipped field map,
// rendered, on the wire — Events API `summary` is upstream's `__subject`, the
// client fields are upstream's, and upstream's four default `details.*` entries
// arrive alongside AMP's own diagnostics rather than instead of them.
func TestTemplatesDelivery_PagerDutyDefaultsReachTheWire(t *testing.T) {
	pd := newRecordingEndpoint(t)

	cfg := &infraroute.PagerDutyConfig{RoutingKey: "routing-key-value"}
	cfg.Defaults() // as the parser does: severity "error", full events URL
	cfg.URL = pd.server.URL + "/v2/enqueue"

	routing := &infraroute.RouteConfig{Receivers: []*infraroute.Receiver{{
		Name:             "team-x",
		PagerDutyConfigs: []*infraroute.PagerDutyConfig{cfg},
	}}}

	stack := newStackFromRouting(t, routing, "team-x", alertnameGroupingConfig(),
		withTemplatingFromConfig(t, appconfig.PublishingTemplateConfig{}, nil))

	stack.ingest(t, "fp-1")
	got := pd.waitForRequest(t, 3*time.Second)
	require.Equal(t, "/v2/enqueue", got.path)

	assert.Equal(t, upstreamDefaultLink, got.body["client_url"])
	assert.Equal(t, "Alertmanager", got.body["client"])

	nested, ok := got.body["payload"].(map[string]any)
	require.True(t, ok, "events v2 payload: %#v", got.body)
	assert.Equal(t, upstreamDefaultTitle, nested["summary"],
		"pagerduty.default.description is upstream's __subject")

	details, ok := nested["custom_details"].(map[string]any)
	require.True(t, ok, "custom_details: %#v", nested)
	assert.Equal(t, "1", details["num_firing"])
	assert.Equal(t, "0", details["num_resolved"])
	assert.Contains(t, details["firing"], `"labels"`,
		"upstream's toJson dump of the firing partition")
}

// TestTemplatesDelivery_TelegramDefaultsReachTheWire: the telegram representative.
// The body is upstream's `telegram.default.message` — blank-line runs and
// `Labels:`/`Annotations:` sections — where AMP's fixed formatter would have sent
// its own HTML-tagged layout.
func TestTemplatesDelivery_TelegramDefaultsReachTheWire(t *testing.T) {
	telegram := newRecordingEndpoint(t)

	routing := &infraroute.RouteConfig{Receivers: []*infraroute.Receiver{{
		Name: "team-x",
		TelegramConfigs: []*infraroute.TelegramConfig{{
			BotToken: "bot-token-value",
			ChatID:   "12345",
			APIURL:   telegram.server.URL,
		}},
	}}}

	stack := newStackFromRouting(t, routing, "team-x", alertnameGroupingConfig(),
		withTemplatingFromConfig(t, appconfig.PublishingTemplateConfig{}, nil))

	stack.ingest(t, "fp-1")
	got := telegram.waitForRequest(t, 3*time.Second)
	assert.Equal(t, "/botbot-token-value/sendMessage", got.path)

	text, _ := got.body["text"].(string)
	assert.True(t, strings.HasPrefix(text, "\n\nAlerts Firing:\n"),
		"upstream's own leading whitespace, not AMP's layout: %q", text)
	assert.Contains(t, text, "Labels:\n - alertname = HighCPU\n - cluster = prod\n - severity = critical\n")
	assert.Contains(t, text, " - summary = cpu is high\n")
	assert.NotContains(t, text, "<b>", "AMP's fixed Telegram formatter is not what shipped")
}

// TestTemplatesDelivery_NoRegistryWiredIsThePreEpicPayload pins the pre-epic
// payload for the ONE production configuration that still produces it: no
// registry wired at all (templating off, or a template load that failed so hard
// the registry stayed nil).
//
// Renamed and rescoped from a version whose comment claimed this was the
// "zero-behaviour-change guarantee for every deployment that configures no
// templates" — it never was: production always wires the registry, and every
// config-provisioned slack/pagerduty/telegram/email target carries upstream's
// materialized defaults (slice-2 review C1).
func TestTemplatesDelivery_NoRegistryWiredIsThePreEpicPayload(t *testing.T) {
	slack := newRecordingEndpoint(t)

	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig()) // no registry wired

	stack.ingest(t, "fp-1")
	got := slack.waitForRequest(t, 3*time.Second)

	assert.Contains(t, got.body, "blocks")
	text, _ := got.body["text"].(string)
	assert.Contains(t, text, "HighCPU", "AMP's fixed formatter output, unchanged")
}

// e2eCounterValue reads one labelled counter out of the stack's own registry.
func e2eCounterValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			matched := true
			for wantName, wantValue := range labels {
				found := false
				for _, pair := range metric.GetLabel() {
					if pair.GetName() == wantName && pair.GetValue() == wantValue {
						found = true
						break
					}
				}
				if !found {
					matched = false
					break
				}
			}
			if matched {
				return metricCounterValue(metric)
			}
		}
	}
	return 0
}

func metricCounterValue(metric *dto.Metric) float64 {
	return metric.GetCounter().GetValue()
}
