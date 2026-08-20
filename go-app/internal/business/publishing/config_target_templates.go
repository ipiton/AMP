package publishing

import (
	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// Per-integration template-bearing fields (TEMPLATES-EPIC slice 2,
// closing FU-INTEGRATION-FIELD-FIDELITY)
// ============================================================================
//
// Upstream Alertmanager gives every integration a set of PRESENTATION fields
// that are Go templates: `slack_configs[].title`, `.text`, `.color`, ...,
// `pagerduty_configs[].description`, `telegram_configs[].message`,
// `email_configs[].subject`/`.html`. Each has a DEFAULT that references the
// default template library (`{{ template "slack.default.title" . }}`), which is
// why an upstream config that sets none of them still renders upstream's
// familiar output.
//
// AMP parsed all of these fields and consumed none of them: the runtime always
// rendered through the fixed per-type formatters in
// internal/infrastructure/publishing. That is FU-INTEGRATION-FIELD-FIDELITY,
// and this file is the config half of closing it — it lifts the fields onto
// core.PublishingTarget.Templates, where the publishing layer's template
// formatter renders them (see infrastructure/publishing/template_formatter.go).
//
// Two deliberate rules:
//
//  1. Upstream's DEFAULTS are materialized here, not left implicit. An
//     operator who writes `slack_configs: [{api_url: ...}]` gets
//     `{{ template "slack.default.title" . }}` etc., exactly as upstream does
//     in DefaultSlackConfig — so a migrated config renders identically without
//     naming a single template field.
//  2. Only PRESENTATION fields are copied. No credential, endpoint or
//     transport setting is ever placed in Templates: those stay in
//     URL/Headers, out of reach of any template expression.

// Upstream default values, verbatim from
// github.com/prometheus/alertmanager@v0.34.0:
//
//	config/notifiers.go        DefaultSlackConfig, DefaultEmailConfig,
//	                           DefaultEmailSubject
//	notify/pagerduty/config.go DefaultPagerdutyConfig, DefaultPagerdutyDetails
//	notify/telegram/config.go  DefaultTelegramConfig
//
// Copied as literals (they are configuration values, not code) so the
// dependency stays out of go.mod; golden tests in
// infrastructure/publishing pin what they render to.
const (
	defaultSlackTitle     = `{{ template "slack.default.title" . }}`
	defaultSlackTitleLink = `{{ template "slack.default.titlelink" . }}`
	defaultSlackPretext   = `{{ template "slack.default.pretext" . }}`
	defaultSlackText      = `{{ template "slack.default.text" . }}`
	defaultSlackColor     = `{{ template "slack.default.color" . }}`
	defaultSlackUsername  = `{{ template "slack.default.username" . }}`
	defaultSlackIconEmoji = `{{ template "slack.default.iconemoji" . }}`
	defaultSlackIconURL   = `{{ template "slack.default.iconurl" . }}`
	defaultSlackFallback  = `{{ template "slack.default.fallback" . }}`

	defaultPagerDutyDescription = `{{ template "pagerduty.default.description" .}}`
	defaultPagerDutyClient      = `{{ template "pagerduty.default.client" . }}`
	defaultPagerDutyClientURL   = `{{ template "pagerduty.default.clientURL" . }}`

	defaultTelegramMessage = `{{ template "telegram.default.message" . }}`

	defaultEmailSubject = `{{ template "email.default.subject" . }}`
	defaultEmailHTML    = `{{ template "email.default.html" . }}`
)

// defaultPagerDutyDetails mirrors upstream's DefaultPagerdutyDetails.
var defaultPagerDutyDetails = map[string]string{
	"firing":       `{{ .Alerts.Firing | toJson }}`,
	"resolved":     `{{ .Alerts.Resolved | toJson }}`,
	"num_firing":   `{{ .Alerts.Firing | len }}`,
	"num_resolved": `{{ .Alerts.Resolved | len }}`,
}

// slackTemplateFields returns the template-bearing fields of one slack_config,
// with upstream's defaults filled in for anything the operator left unset.
//
// `channel` is NOT defaulted: upstream has no default for it (an unset channel
// means "whatever the webhook is wired to"), and templating an empty channel
// would override that with the empty string.
func slackTemplateFields(cfg *infraroute.SlackConfig) map[string]string {
	if cfg == nil {
		return nil
	}

	fields := map[string]string{
		core.TemplateFieldTitle:     orDefault(cfg.Title, defaultSlackTitle),
		core.TemplateFieldTitleLink: orDefault(cfg.TitleLink, defaultSlackTitleLink),
		core.TemplateFieldPretext:   orDefault(cfg.Pretext, defaultSlackPretext),
		core.TemplateFieldText:      orDefault(cfg.Text, defaultSlackText),
		core.TemplateFieldColor:     orDefault(cfg.Color, defaultSlackColor),
		core.TemplateFieldUsername:  orDefault(cfg.Username, defaultSlackUsername),
		core.TemplateFieldIconEmoji: orDefault(cfg.IconEmoji, defaultSlackIconEmoji),
		core.TemplateFieldIconURL:   orDefault(cfg.IconURL, defaultSlackIconURL),
		core.TemplateFieldFallback:  defaultSlackFallback,
	}
	if cfg.Channel != "" {
		fields[core.TemplateFieldChannel] = cfg.Channel
	}
	return fields
}

// pagerDutyTemplateFields returns the template-bearing fields of one
// pagerduty_config.
//
// `severity` carries the operator's value (or upstream's "error" default, which
// PagerDutyConfig.Defaults already applied) rather than a template reference:
// upstream has no `pagerduty.default.severity` definition, but it DOES run the
// field through the template engine, so `severity: '{{ .CommonLabels.severity }}'`
// works here exactly as it does upstream.
func pagerDutyTemplateFields(cfg *infraroute.PagerDutyConfig) map[string]string {
	if cfg == nil {
		return nil
	}

	fields := map[string]string{
		core.TemplateFieldDescription: orDefault(cfg.Description, defaultPagerDutyDescription),
		core.TemplateFieldClient:      defaultPagerDutyClient,
		core.TemplateFieldClientURL:   defaultPagerDutyClientURL,
	}
	if cfg.Severity != "" {
		fields[core.TemplateFieldSeverity] = cfg.Severity
	}

	details := cfg.Details
	if len(details) == 0 {
		details = defaultPagerDutyDetails
	}
	for key, value := range details {
		fields[core.TemplateFieldDetailsPrefix+key] = value
	}
	return fields
}

// telegramTemplateFields returns the template-bearing fields of one
// telegram_config.
//
// parse_mode is deliberately absent: upstream treats it as a plain enum, not a
// template, and AMP already plumbs it to the publisher through the target's
// FilterConfig — templating it would let a typo produce an invalid Bot API
// request.
func telegramTemplateFields(cfg *infraroute.TelegramConfig) map[string]string {
	if cfg == nil {
		return nil
	}
	return map[string]string{
		core.TemplateFieldMessage: orDefault(cfg.Message, defaultTelegramMessage),
	}
}

// emailTemplateFields returns the template-bearing fields of one email_config.
//
// `text` is defaulted to EMPTY, matching upstream's DefaultEmailConfig
// (`Text: ""`): upstream sends a text part only when the operator asks for one.
// Headers are flattened under `headers.` and are templated upstream too.
func emailTemplateFields(cfg *infraroute.EmailConfig) map[string]string {
	if cfg == nil {
		return nil
	}

	fields := map[string]string{
		core.TemplateFieldSubject: orDefault(cfg.Subject, defaultEmailSubject),
		core.TemplateFieldHTML:    orDefault(cfg.HTML, defaultEmailHTML),
	}
	if cfg.Text != "" {
		fields[core.TemplateFieldText] = cfg.Text
	}
	for key, value := range cfg.Headers {
		fields[core.TemplateFieldHeadersPrefix+key] = value
	}
	return fields
}

// webhookTemplateFields is intentionally empty.
//
// Upstream does NOT template webhook payloads: the v4 JSON body is
// struct-marshaled from the notification data, and there is no
// `webhook_configs[].*` presentation field to render. AMP's wave-2 batch
// marshaling therefore stays exactly as it is, and a webhook target carries no
// Templates map at all — the formatter then has nothing to overlay and skips
// templating entirely.
func webhookTemplateFields() map[string]string { return nil }

// orDefault returns value, or fallback when value is empty.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
