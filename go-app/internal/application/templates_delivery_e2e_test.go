package application

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
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

// TestTemplatesDelivery_NoTemplatesWiredIsUnchanged is the zero-behaviour-change
// guarantee for every deployment that configures no templates: without a
// registry the wire payload is AMP's pre-epic Block Kit shape.
func TestTemplatesDelivery_NoTemplatesWiredIsUnchanged(t *testing.T) {
	slack := newRecordingEndpoint(t)

	stack := newStackFromRouting(t, slackRoutingConfig(slack.server.URL+"/services/T/B/C"),
		"team-x", alertnameGroupingConfig()) // no withTemplateRegistry

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
