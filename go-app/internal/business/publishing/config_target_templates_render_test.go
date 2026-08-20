package publishing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// The SHIPPED field map, rendered (slice-2 review I2)
// ============================================================================
//
// config_target_templates_test.go pins the EXPRESSIONS the production map
// carries; the decorator tests pin the overlay with 3-5 hand-written fields. What
// neither did is render the map that actually ships — all nine slack fields, all
// four-plus-details pagerduty fields, telegram's one and email's two — as a unit,
// which is the only way "an untouched upstream config renders upstream's output"
// can be checked rather than asserted.
//
// So: build the target exactly as BuildConfigTargets does from a PURE-DEFAULT
// config, render every field of target.Templates through the real engine, and
// compare against upstream's own output.
//
// GOLDEN PROVENANCE: every want below is a byte-copy of a golden in
// internal/business/templating/golden_test.go, which documents how it was
// generated (upstream alertmanager v0.34.0's own template package, rendering the
// SAME fixture in a throwaway module). The fixture here is deliberately identical
// to that file's `mixed` fixture — three alerts (2 firing + 1 resolved),
// GroupLabels {alertname, severity}, ExternalURL http://amp.example.com — so the
// expectations are transferable literals, not re-derived ones. Re-generate both
// places together after an upstream bump.

const (
	renderExternalURL = "http://amp.example.com"
	renderReceiver    = "team-x"

	// goldenSubjectMixed / the alertmanager URL / slack.default.fallback.
	wantUpstreamSubject  = "[FIRING:2] HighCPU critical (node)"
	wantUpstreamLink     = renderExternalURL + "/#/alerts?receiver=" + renderReceiver
	wantUpstreamFallback = wantUpstreamSubject + " | " + wantUpstreamLink

	// goldenTelegramMessageMixed. The blank-line runs are upstream's own
	// whitespace — see the golden file's note; reproducing them is the point.
	wantUpstreamTelegramMessage = "\n\nAlerts Firing:\n" +
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
)

// renderFixtureData mirrors templating's `mixed` golden fixture exactly.
func renderFixtureData() *templating.Data {
	generatorURL := "http://prometheus.example/graph?g0.expr=up"
	endsAt := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	startsAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	alert := func(instance string, status core.AlertStatus) *core.Alert {
		a := &core.Alert{
			AlertName: "HighCPU",
			Status:    status,
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
			StartsAt:     startsAt,
			GeneratorURL: &generatorURL,
		}
		if status == core.StatusResolved {
			a.EndsAt = &endsAt
		}
		return a
	}

	return templating.BuildData(templating.DataInput{
		Receiver:    renderReceiver,
		GroupLabels: map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Alerts: []*core.Alert{
			alert("server-1", core.StatusFiring),
			alert("server-2", core.StatusFiring),
			alert("server-3", core.StatusResolved),
		},
		ExternalURL: renderExternalURL,
	})
}

// renderShippedFields renders every field of the target's Templates map through
// the shipped default library, the way the publishing-side renderer does
// (text instance; the email HTML field is handled separately below).
func renderShippedFields(t *testing.T, target *core.PublishingTarget) map[string]string {
	t.Helper()

	tmpl, err := templating.FromGlobs(nil, templating.Options{})
	require.NoError(t, err)

	data := renderFixtureData()
	out := make(map[string]string, len(target.Templates))
	for field, expression := range target.Templates {
		if !core.IsTemplateExpression(expression) {
			out[field] = expression
			continue
		}
		rendered, err := tmpl.ExecuteTextString(expression, data)
		require.NoError(t, err, "field %q (%s) must render", field, expression)
		out[field] = rendered
	}
	return out
}

// targetFor builds one config target the way production does and returns it.
func targetFor(t *testing.T, receiver *infraroute.Receiver, global *infraroute.GlobalConfig) *core.PublishingTarget {
	t.Helper()

	targets := BuildConfigTargets(&infraroute.RouteConfig{
		Global:    global,
		Receivers: []*infraroute.Receiver{receiver},
	}, templatesTestLogger())
	require.Len(t, targets, 1)
	return targets[0]
}

// TestShippedSlackFieldMap_RendersUpstreamOutput renders ALL NINE fields a
// pure-default slack_config ships with. `channel` is absent by design (upstream
// has no default for it), which is why the count is nine and not ten.
func TestShippedSlackFieldMap_RendersUpstreamOutput(t *testing.T) {
	target := targetFor(t, &infraroute.Receiver{
		Name:         renderReceiver,
		SlackConfigs: []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.invalid/x"}},
	}, nil)

	require.Len(t, target.Templates, 9,
		"the production map — a change here changes every Slack notification")

	got := renderShippedFields(t, target)

	assert.Equal(t, map[string]string{
		core.TemplateFieldTitle:     wantUpstreamSubject,
		core.TemplateFieldTitleLink: wantUpstreamLink,
		core.TemplateFieldFallback:  wantUpstreamFallback,
		core.TemplateFieldColor:     "danger",
		core.TemplateFieldUsername:  "Alertmanager",

		// Upstream defines these as EMPTY definitions. They must render "" with
		// no error (an undefined-template fallback would show up as
		// template_fallbacks_total{reason="not_defined"} on every notification).
		core.TemplateFieldText:      "",
		core.TemplateFieldPretext:   "",
		core.TemplateFieldIconEmoji: "",
		core.TemplateFieldIconURL:   "",
	}, got)
}

// TestShippedSlackFieldMap_ChannelIsALiteral: the one slack field that is NOT a
// template still has to travel through the map, because that is how it reaches
// the wire (`channel` never worked before slice 2).
func TestShippedSlackFieldMap_ChannelIsALiteral(t *testing.T) {
	target := targetFor(t, &infraroute.Receiver{
		Name:         renderReceiver,
		SlackConfigs: []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.invalid/x", Channel: "#ops"}},
	}, nil)

	require.Len(t, target.Templates, 10)
	assert.Equal(t, "#ops", renderShippedFields(t, target)[core.TemplateFieldChannel])
}

// TestShippedPagerDutyFieldMap_RendersUpstreamOutput: description is upstream's
// `__subject` (the representative assertion), and the four default `details.*`
// entries must all render — `firing`/`resolved` are `toJson` over the alert
// partitions, which is where a Data-model regression would show up first.
func TestShippedPagerDutyFieldMap_RendersUpstreamOutput(t *testing.T) {
	cfg := &infraroute.PagerDutyConfig{RoutingKey: "routing-key-value"}
	cfg.Defaults() // as the parser does

	target := targetFor(t, &infraroute.Receiver{
		Name:             renderReceiver,
		PagerDutyConfigs: []*infraroute.PagerDutyConfig{cfg},
	}, nil)

	got := renderShippedFields(t, target)

	assert.Equal(t, wantUpstreamSubject, got[core.TemplateFieldDescription])
	assert.Equal(t, "Alertmanager", got[core.TemplateFieldClient])
	assert.Equal(t, wantUpstreamLink, got[core.TemplateFieldClientURL])
	assert.Equal(t, "error", got[core.TemplateFieldSeverity], "upstream's default severity")

	assert.Equal(t, "2", got[core.TemplateFieldDetailsPrefix+"num_firing"])
	assert.Equal(t, "1", got[core.TemplateFieldDetailsPrefix+"num_resolved"])

	var firing, resolved []map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[core.TemplateFieldDetailsPrefix+"firing"]), &firing))
	require.NoError(t, json.Unmarshal([]byte(got[core.TemplateFieldDetailsPrefix+"resolved"]), &resolved))
	assert.Len(t, firing, 2)
	assert.Len(t, resolved, 1)
	assert.Equal(t, "firing", firing[0]["status"],
		"upstream's own json tags (lowercase) — a `details.firing` payload an operator's PagerDuty automation may parse")
	assert.Equal(t, "resolved", resolved[0]["status"])
}

// TestShippedTelegramFieldMap_RendersUpstreamOutput compares the whole message
// body, blank lines included — a Telegram message must look the same after a
// migration.
func TestShippedTelegramFieldMap_RendersUpstreamOutput(t *testing.T) {
	target := targetFor(t, &infraroute.Receiver{
		Name:            renderReceiver,
		TelegramConfigs: []*infraroute.TelegramConfig{{BotToken: "bot-token-value", ChatID: "1"}},
	}, nil)

	require.Len(t, target.Templates, 1)
	assert.Equal(t, wantUpstreamTelegramMessage,
		renderShippedFields(t, target)[core.TemplateFieldMessage])
}

// TestShippedEmailFieldMap_RendersUpstreamOutput: subject is the representative
// literal; the HTML body is upstream's ~10 KB `email.default.html`, rendered
// through the HTML instance (as the publisher does) and checked for the parts an
// operator would notice — it is not a literal anyone should paste into a test.
func TestShippedEmailFieldMap_RendersUpstreamOutput(t *testing.T) {
	target := targetFor(t, &infraroute.Receiver{
		Name:         renderReceiver,
		EmailConfigs: []*infraroute.EmailConfig{{To: mailRecipient}},
	}, &infraroute.GlobalConfig{
		SMTPSmartHost: "smtp.example.com:587",
		SMTPFrom:      mailSender,
	})

	require.Len(t, target.Templates, 2, "subject + html; upstream defaults text to empty")
	assert.Equal(t, wantUpstreamSubject, renderShippedFields(t, target)[core.TemplateFieldSubject])

	tmpl, err := templating.FromGlobs(nil, templating.Options{})
	require.NoError(t, err)
	html, err := tmpl.ExecuteHTMLString(target.Templates[core.TemplateFieldHTML], renderFixtureData())
	require.NoError(t, err)

	assert.Contains(t, html, wantUpstreamSubject, "upstream's body leads with the subject line")
	assert.Contains(t, html, wantUpstreamLink)
	assert.Contains(t, html, "CPU high on server-1", "the alert's own annotations reach the body")
	assert.Greater(t, len(html), 2000, "upstream's email body is a full HTML document")
}
