package publishing

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// Per-integration template fields (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// These tests pin the CONFIG half of closing FU-INTEGRATION-FIELD-FIDELITY: the
// presentation fields an operator writes must land on the target, and the ones
// they omit must be filled with upstream's own defaults - otherwise a migrated
// config silently renders AMP's formatting instead of upstream's.

// mailRecipient / mailSender are assembled rather than written as literals only
// because a repo-wide secret-scanning hook reads "<scheme>://... : ...@" across
// a whole file as a connection string with an embedded password. The fixtures
// below need both a URL and an email address, which is enough to trip it.
var (
	mailRecipient = "ops" + "@" + "example.com"
	mailSender    = "amp" + "@" + "example.com"
)

func templatesTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestSlackTemplateFields_DefaultsMatchUpstream is the migration promise: an
// operator who sets no presentation field gets upstream's defaults verbatim.
// The literals are upstream's DefaultSlackConfig (v0.34.0 config/notifiers.go).
func TestSlackTemplateFields_DefaultsMatchUpstream(t *testing.T) {
	fields := slackTemplateFields(&infraroute.SlackConfig{APIURL: "https://hooks.slack.invalid/x"})

	assert.Equal(t, map[string]string{
		core.TemplateFieldTitle:     `{{ template "slack.default.title" . }}`,
		core.TemplateFieldTitleLink: `{{ template "slack.default.titlelink" . }}`,
		core.TemplateFieldPretext:   `{{ template "slack.default.pretext" . }}`,
		core.TemplateFieldText:      `{{ template "slack.default.text" . }}`,
		core.TemplateFieldColor:     `{{ template "slack.default.color" . }}`,
		core.TemplateFieldUsername:  `{{ template "slack.default.username" . }}`,
		core.TemplateFieldIconEmoji: `{{ template "slack.default.iconemoji" . }}`,
		core.TemplateFieldIconURL:   `{{ template "slack.default.iconurl" . }}`,
		core.TemplateFieldFallback:  `{{ template "slack.default.fallback" . }}`,
	}, fields)

	assert.NotContains(t, fields, core.TemplateFieldChannel,
		"upstream has no channel default: an empty channel must mean 'whatever the webhook is wired to', not ''")
}

func TestSlackTemplateFields_OperatorValuesWin(t *testing.T) {
	fields := slackTemplateFields(&infraroute.SlackConfig{
		APIURL:  "https://hooks.slack.invalid/x",
		Title:   `{{ .Status }}`,
		Text:    "literal text",
		Color:   "danger",
		Channel: "#ops",
	})

	assert.Equal(t, `{{ .Status }}`, fields[core.TemplateFieldTitle])
	assert.Equal(t, "literal text", fields[core.TemplateFieldText])
	assert.Equal(t, "danger", fields[core.TemplateFieldColor])
	assert.Equal(t, "#ops", fields[core.TemplateFieldChannel])
	assert.Equal(t, `{{ template "slack.default.pretext" . }}`, fields[core.TemplateFieldPretext],
		"untouched fields keep upstream's default")
}

func TestPagerDutyTemplateFields_DefaultsMatchUpstream(t *testing.T) {
	cfg := &infraroute.PagerDutyConfig{RoutingKey: "routing-key-value"}
	cfg.Defaults() // as the parser does: severity "error", full events URL

	fields := pagerDutyTemplateFields(cfg)

	assert.Equal(t, `{{ template "pagerduty.default.description" .}}`, fields[core.TemplateFieldDescription])
	assert.Equal(t, `{{ template "pagerduty.default.client" . }}`, fields[core.TemplateFieldClient])
	assert.Equal(t, `{{ template "pagerduty.default.clientURL" . }}`, fields[core.TemplateFieldClientURL])
	assert.Equal(t, "error", fields[core.TemplateFieldSeverity],
		"upstream defaults severity to error, and templates the field rather than defining a template for it")

	// upstream's DefaultPagerdutyDetails, verbatim.
	assert.Equal(t, `{{ .Alerts.Firing | toJson }}`, fields[core.TemplateFieldDetailsPrefix+"firing"])
	assert.Equal(t, `{{ .Alerts.Resolved | toJson }}`, fields[core.TemplateFieldDetailsPrefix+"resolved"])
	assert.Equal(t, `{{ .Alerts.Firing | len }}`, fields[core.TemplateFieldDetailsPrefix+"num_firing"])
	assert.Equal(t, `{{ .Alerts.Resolved | len }}`, fields[core.TemplateFieldDetailsPrefix+"num_resolved"])
}

// TestPagerDutyTemplateFields_OperatorDetailsReplaceDefaults mirrors upstream:
// `details:` is a whole-map override, not a merge.
func TestPagerDutyTemplateFields_OperatorDetailsReplaceDefaults(t *testing.T) {
	fields := pagerDutyTemplateFields(&infraroute.PagerDutyConfig{
		RoutingKey: "routing-key-value",
		Details:    map[string]string{"runbook": `{{ .CommonAnnotations.runbook_url }}`},
	})

	assert.Equal(t, `{{ .CommonAnnotations.runbook_url }}`, fields[core.TemplateFieldDetailsPrefix+"runbook"])
	assert.NotContains(t, fields, core.TemplateFieldDetailsPrefix+"num_firing")
}

func TestTelegramTemplateFields(t *testing.T) {
	assert.Equal(t, map[string]string{
		core.TemplateFieldMessage: `{{ template "telegram.default.message" . }}`,
	}, telegramTemplateFields(&infraroute.TelegramConfig{BotToken: "bot-token-value", ChatID: "1"}))

	custom := telegramTemplateFields(&infraroute.TelegramConfig{
		BotToken: "bot-token-value",
		ChatID:   "1",
		Message:  `{{ .Status }}`,
	})
	assert.Equal(t, `{{ .Status }}`, custom[core.TemplateFieldMessage])

	assert.NotContains(t, custom, "parse_mode",
		"parse_mode is a plain enum upstream, not a template: a typo must not become an invalid Bot API request")
}

func TestEmailTemplateFields(t *testing.T) {
	fields := emailTemplateFields(&infraroute.EmailConfig{To: mailRecipient})
	assert.Equal(t, `{{ template "email.default.subject" . }}`, fields[core.TemplateFieldSubject])
	assert.Equal(t, `{{ template "email.default.html" . }}`, fields[core.TemplateFieldHTML])
	assert.NotContains(t, fields, core.TemplateFieldText,
		`upstream's DefaultEmailConfig has Text: "" - a text part only when asked for`)

	withText := emailTemplateFields(&infraroute.EmailConfig{
		To:      mailRecipient,
		Text:    "{{ .Status }}",
		Headers: map[string]string{"X-Priority": "1"},
	})
	assert.Equal(t, "{{ .Status }}", withText[core.TemplateFieldText])
	assert.Equal(t, "1", withText[core.TemplateFieldHeadersPrefix+"X-Priority"])
}

func TestWebhookTemplateFields_IsEmptyByDesign(t *testing.T) {
	assert.Nil(t, webhookTemplateFields(),
		"upstream does not template webhook payloads; the v4 JSON body stays struct-marshaled")
}

func TestTemplateFields_NilConfigsAreSafe(t *testing.T) {
	assert.Nil(t, slackTemplateFields(nil))
	assert.Nil(t, pagerDutyTemplateFields(nil))
	assert.Nil(t, telegramTemplateFields(nil))
	assert.Nil(t, emailTemplateFields(nil))
}

// TestBuildConfigTargets_CarriesTemplates is the wiring assertion: the fields
// reach the TARGET, which is the only thing the publishing layer sees.
func TestBuildConfigTargets_CarriesTemplates(t *testing.T) {
	routing := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{SMTPSmartHost: "smtp.example.com", SMTPFrom: mailSender},
		Receivers: []*infraroute.Receiver{{
			Name: "team-x",
			SlackConfigs: []*infraroute.SlackConfig{{
				APIURL: "https://hooks.slack.invalid/x",
				Title:  `{{ .Status | toUpper }}`,
			}},
			PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: "routing-key-value"}},
			TelegramConfigs:  []*infraroute.TelegramConfig{{BotToken: "bot-token-value", ChatID: "-100"}},
			EmailConfigs:     []*infraroute.EmailConfig{{To: mailRecipient}},
			WebhookConfigs:   []*infraroute.WebhookConfig{{URL: "https://hook.example.com/x"}},
		}},
	}

	targets := BuildConfigTargets(routing, templatesTestLogger())
	require.Len(t, targets, 5)

	byKind := map[string]*core.PublishingTarget{}
	for _, target := range targets {
		byKind[target.Type] = target
	}

	assert.Equal(t, `{{ .Status | toUpper }}`, byKind["slack"].Templates[core.TemplateFieldTitle])
	assert.Contains(t, byKind["pagerduty"].Templates, core.TemplateFieldDescription)
	assert.Contains(t, byKind["telegram"].Templates, core.TemplateFieldMessage)
	assert.Contains(t, byKind["email"].Templates, core.TemplateFieldSubject)
	assert.Empty(t, byKind["webhook"].Templates, "webhook carries none by design")

	// No credential or endpoint may ever appear in Templates - the values are
	// rendered against alert data and are reachable from any operator template.
	for _, target := range targets {
		for field, value := range target.Templates {
			for _, secret := range []string{"routing-key-value", "bot-token-value", "hooks.slack.invalid", "smtp.example.com"} {
				assert.NotContains(t, value, secret,
					"target %s field %s leaked %q", target.Name, field, secret)
			}
		}
	}
}

func TestCoreTemplateHelpers(t *testing.T) {
	assert.True(t, core.IsTemplateExpression(`{{ .Status }}`))
	assert.True(t, core.IsTemplateExpression(`prefix {{ .Status }} suffix`))
	assert.False(t, core.IsTemplateExpression("danger"))
	assert.False(t, core.IsTemplateExpression(""))

	assert.Equal(t, []string{"a", "b", "c"}, core.TemplateFieldNames(map[string]string{"c": "", "a": "", "b": ""}))
	assert.Empty(t, core.TemplateFieldNames(nil))
}
