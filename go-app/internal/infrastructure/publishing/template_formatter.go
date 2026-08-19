package publishing

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/ipiton/AMP/internal/business/templating"
	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ============================================================================
// Template-rendered notification content (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// This is the half of the epic that makes configured templates REACH THE WIRE.
//
// Slice 1 built the engine (internal/business/templating: upstream's data model,
// DefaultFuncs, the default template library, `templates:` loading with atomic
// reload) but deliberately wired nothing into delivery, so output was unchanged.
// Slice 2 renders the per-integration presentation fields an operator writes —
// `slack_configs[].title`, `pagerduty_configs[].description`,
// `telegram_configs[].message`, `email_configs[].subject`/`.html` — and overlays
// the results onto the payload the fixed formatter produced.
//
// SHAPE OF THE CHANGE: a DECORATOR around AlertFormatter, not a rewrite of the
// per-type formatters. Every publisher already renders through
// GetFormatter().FormatAlert(...), so wrapping the formatter per target gets
// template output onto the wire without touching a single wire sender, HTTP
// client, or publisher contract. The fixed formatters stay exactly as they are
// and remain the fallback.
//
// THE FALLBACK CONTRACT (the important part): a template that fails to render
// NEVER drops the notification. The fixed formatter's payload is delivered
// instead, the failure is logged with the target and field, and
// publishing_template_fallbacks_total is incremented so the silent-degradation
// case is observable — because from every other angle (delivery metrics, logs,
// wire status) a fallback looks like a perfectly successful notification.
//
// WEBHOOK IS EXCLUDED BY DESIGN: upstream does not template webhook payloads
// (the v4 JSON body is struct-marshaled from the notification data), so a
// webhook target carries no Templates and this decorator passes its payload
// through untouched. AMP's wave-2 batch marshaling is not involved at all.

// templateRenderer renders a target's template-bearing fields. It is the piece
// shared by the formatter decorator (slack/pagerduty/telegram) and the email
// publisher, which does not go through AlertFormatter at all.
type templateRenderer struct {
	registry    *templating.Registry
	externalURL string
	metrics     *v2.PublishingMetrics
	logger      *slog.Logger
}

// newTemplateRenderer returns nil when templating is not configured, which is
// how every call site expresses "no templating: use the fixed formatter".
func newTemplateRenderer(registry *templating.Registry, externalURL string, metrics *v2.PublishingMetrics, logger *slog.Logger) *templateRenderer {
	if registry == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &templateRenderer{
		registry:    registry,
		externalURL: externalURL,
		metrics:     metrics,
		logger:      logger.With("component", "template_renderer"),
	}
}

// integrationOf maps a target to the metric label for its integration.
func integrationOf(target *core.PublishingTarget) string {
	if target == nil {
		return "unknown"
	}
	if target.Type != "" {
		return target.Type
	}
	return string(target.Format)
}

// receiverNameOf resolves the receiver name to show in templates
// (`{{ .Receiver }}`, and the `?receiver=` query of every default
// alertmanagerURL link).
//
// Order matters. A receiver-scoped target carries the CONFIGURED receiver name
// in Receivers (both for config-provisioned targets, where it is filled by
// construction, and for K8s ones labelled `amp.receiver`), which is the most
// faithful source. Only if that is missing does it fall back to decoding the
// target NAME, stripping AMP's `cfg:<receiver>/<kind><idx>` encoding via
// templating.ReceiverNameFromTarget — the slice-1 guarantee that `cfg:` never
// leaks into a notification title.
func receiverNameOf(ctx context.Context, target *core.PublishingTarget) string {
	if group := groupNotificationContextFrom(ctx); group.Receiver != "" {
		return group.Receiver
	}
	if target == nil {
		return ""
	}
	if len(target.Receivers) == 1 && target.Receivers[0] != "" {
		return target.Receivers[0]
	}
	return templating.ReceiverNameFromTarget(target.Name)
}

// dataFor builds the upstream template data model for one alert delivered to
// one target.
//
// One ALERT, but the real GROUP context: slack/pagerduty/telegram/email are
// one-message-per-alert integrations in AMP (see PublishingQueue.publishJob), so
// Alerts holds the single alert being delivered — while GroupLabels and Receiver
// come from the group the alert belongs to, carried on the context by the queue
// (notification_context.go).
//
// Both halves matter for output parity. Without the group labels, upstream's
// `__subject` renders every label into its parenthesised remainder
// (`[FIRING:1]  (HighCPU critical)`) instead of naming the group
// (`[FIRING:1] HighCPU (critical)`). CommonLabels/CommonAnnotations equal the
// alert's own, which is exactly what upstream computes for a one-alert
// notification.
func (r *templateRenderer) dataFor(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) *templating.Data {
	var alerts []*core.Alert
	if enrichedAlert != nil && enrichedAlert.Alert != nil {
		alerts = []*core.Alert{enrichedAlert.Alert}
	}

	return templating.BuildData(templating.DataInput{
		Receiver:    receiverNameOf(ctx, target),
		GroupLabels: groupNotificationContextFrom(ctx).GroupLabels,
		Alerts:      alerts,
		ExternalURL: r.externalURL,
	})
}

// renderField renders one template-bearing field.
//
// html selects the html/template instance, which is required for
// `email.default.html` (contextual auto-escaping of attacker-influenced label
// values) and wrong for everything else — Slack titles and Telegram HTML
// messages must not have their own markup escaped a second time.
//
// The bool result is "usable": false means the caller must keep whatever the
// fixed formatter produced for that field. An empty rendered value is NOT a
// failure — upstream's `slack.default.text` renders to "" by design — so it is
// returned as usable and the caller decides whether an empty value should
// overwrite anything.
func (r *templateRenderer) renderField(target *core.PublishingTarget, field, expression string, data *templating.Data, html bool) (string, bool) {
	if expression == "" {
		return "", false
	}
	if !core.IsTemplateExpression(expression) {
		// A literal (e.g. `color: danger`, `channel: '#ops'`). Nothing to
		// render, nothing to fail, and nothing worth a metric sample.
		return expression, true
	}

	tmpl := r.registry.Current()
	if tmpl == nil {
		return "", false
	}

	var (
		out string
		err error
	)
	if html {
		out, err = tmpl.ExecuteHTMLString(expression, data)
	} else {
		out, err = tmpl.ExecuteTextString(expression, data)
	}

	integration := integrationOf(target)
	if err != nil {
		// Never log the expression's rendered output or the target URL — the
		// field name and the target name are enough to find the config line.
		r.logger.Warn("Notification template failed to render; falling back to the built-in formatter for this field",
			"target", target.Name,
			"integration", integration,
			"field", field,
			"error", err)
		if r.metrics != nil {
			r.metrics.RecordTemplateRender(integration, v2.TemplateOutcomeError)
			r.metrics.RecordTemplateFallback(integration, fallbackReason(err))
		}
		return "", false
	}

	if r.metrics != nil {
		r.metrics.RecordTemplateRender(integration, v2.TemplateOutcomeSuccess)
	}
	return out, true
}

// fallbackReason classifies a render failure for the fallback metric, so an
// operator can tell "my template is broken" from "my template is too slow/too
// big" without reading logs.
func fallbackReason(err error) string {
	switch {
	case errors.Is(err, templating.ErrTimeout):
		return v2.TemplateFallbackTimeout
	case errors.Is(err, templating.ErrOutputTooLarge):
		return v2.TemplateFallbackOutputCap
	case errors.Is(err, templating.ErrNotDefined):
		return v2.TemplateFallbackNotDefined
	default:
		return v2.TemplateFallbackExecError
	}
}

// renderAll renders every field of target.Templates whose key passes keep,
// returning the rendered values by field name. Fields that failed are absent
// from the result, so a caller can only ever overlay something that rendered.
func (r *templateRenderer) renderAll(target *core.PublishingTarget, data *templating.Data, htmlFields map[string]bool, keep func(string) bool) map[string]string {
	rendered := make(map[string]string, len(target.Templates))
	for field, expression := range target.Templates {
		if keep != nil && !keep(field) {
			continue
		}
		if value, ok := r.renderField(target, field, expression, data, htmlFields[field]); ok {
			rendered[field] = value
		}
	}
	return rendered
}

// templateFormatter decorates an AlertFormatter with per-target template
// rendering. It is created per target by PublisherFactory (formatterFor), so the
// target's Templates map is captured once rather than looked up per alert.
type templateFormatter struct {
	next     AlertFormatter
	renderer *templateRenderer
	target   *core.PublishingTarget
}

// newTemplateFormatter wraps next for target. It returns next UNCHANGED when
// there is nothing to render — no renderer, no target, or a target with no
// template fields (webhook, or a K8s target whose Secret carries none). That is
// the zero-behaviour-change guarantee for template-less deployments: the
// decorator is not merely inert, it is absent.
func newTemplateFormatter(next AlertFormatter, renderer *templateRenderer, target *core.PublishingTarget) AlertFormatter {
	if next == nil || renderer == nil || target == nil || len(target.Templates) == 0 {
		return next
	}
	return &templateFormatter{next: next, renderer: renderer, target: target}
}

// FormatAlert renders the fixed payload first, then overlays whatever the
// operator's templates produced.
//
// Order is deliberate: the fixed formatter runs UNCONDITIONALLY, so its output
// is always available as the fallback, and a template failure costs a wasted
// render rather than a dropped notification.
func (f *templateFormatter) FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error) {
	payload, err := f.next.FormatAlert(ctx, enrichedAlert, format)
	if err != nil {
		// The fixed formatter itself failed — nothing to overlay onto, and this
		// is not a template problem. Propagate untouched.
		return nil, err
	}

	data := f.renderer.dataFor(ctx, enrichedAlert, f.target)

	switch format {
	case core.FormatSlack:
		f.overlaySlack(payload, data)
	case core.FormatPagerDuty:
		f.overlayPagerDuty(payload, data)
	case core.FormatTelegram:
		f.overlayTelegram(payload, data)
	default:
		// alertmanager/webhook/rootly: upstream templates none of these
		// payloads. Left exactly as the fixed formatter produced it.
	}

	return payload, nil
}

// FormatGroup passes through to the wrapped formatter without templating.
//
// The group path is the upstream v4 webhook payload, which upstream does not
// template either — it is struct-marshaled from the notification data. Keeping
// the passthrough (rather than omitting the method) preserves the
// GroupAlertFormatter type assertion that BatchAlertPublisher relies on: a
// decorator that swallowed it would silently turn wire-level group batching off.
func (f *templateFormatter) FormatGroup(ctx context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, format core.PublishingFormat) (map[string]any, error) {
	grouper, ok := f.next.(GroupAlertFormatter)
	if !ok {
		return nil, errors.New("wrapped formatter does not support wire-level group batching")
	}
	return grouper.FormatGroup(ctx, alerts, groupKey, receiver, groupLabels, format)
}

// overlaySlack replaces the fixed Block Kit payload's presentation with the
// operator's rendered fields, in upstream's own attachment shape.
//
// When a config templates `title`/`text`, the operator has asked for THEIR
// formatting — so the AMP-specific `blocks` array is dropped rather than
// rendered alongside it, which would deliver both versions in one message. That
// is the visible behaviour change this epic is for, and it only ever happens for
// a target that actually carries slack template fields.
//
// `text` (top level) stays populated because Slack REQUIRES a fallback string
// for notification previews and screen readers: the rendered `fallback` field is
// upstream's own value for it, with the title as the backstop.
func (f *templateFormatter) overlaySlack(payload map[string]any, data *templating.Data) {
	rendered := f.renderer.renderAll(f.target, data, nil, nil)
	if len(rendered) == 0 {
		return
	}

	attachment := map[string]any{}
	// Carry the fixed formatter's colour/fields over as the base, so a config
	// that templates only `title` keeps AMP's severity colouring and field list.
	if existing, ok := firstAttachment(payload); ok {
		for k, v := range existing {
			attachment[k] = v
		}
	}

	setIfPresent := func(field, key string, into map[string]any) {
		if value, ok := rendered[field]; ok && value != "" {
			into[key] = value
		}
	}

	setIfPresent(core.TemplateFieldTitle, "title", attachment)
	setIfPresent(core.TemplateFieldTitleLink, "title_link", attachment)
	setIfPresent(core.TemplateFieldPretext, "pretext", attachment)
	setIfPresent(core.TemplateFieldText, "text", attachment)
	setIfPresent(core.TemplateFieldColor, "color", attachment)
	setIfPresent(core.TemplateFieldFallback, "fallback", attachment)

	setIfPresent(core.TemplateFieldChannel, "channel", payload)
	setIfPresent(core.TemplateFieldUsername, "username", payload)
	setIfPresent(core.TemplateFieldIconEmoji, "icon_emoji", payload)
	setIfPresent(core.TemplateFieldIconURL, "icon_url", payload)

	payload["attachments"] = []map[string]any{attachment}

	// The operator's presentation replaces AMP's Block Kit rendering.
	title, hasTitle := rendered[core.TemplateFieldTitle]
	text, hasText := rendered[core.TemplateFieldText]
	if (hasTitle && title != "") || (hasText && text != "") {
		delete(payload, "blocks")
	}

	switch {
	case rendered[core.TemplateFieldFallback] != "":
		payload["text"] = rendered[core.TemplateFieldFallback]
	case title != "":
		payload["text"] = title
	case text != "":
		payload["text"] = text
	}
}

// firstAttachment returns the first attachment map of a Slack payload, coping
// with both shapes the fixed formatter and tests produce
// ([]map[string]any and []any).
func firstAttachment(payload map[string]any) (map[string]any, bool) {
	switch attachments := payload["attachments"].(type) {
	case []map[string]any:
		if len(attachments) > 0 {
			return attachments[0], true
		}
	case []any:
		if len(attachments) > 0 {
			if first, ok := attachments[0].(map[string]any); ok {
				return first, true
			}
		}
	}
	return nil, false
}

// overlayPagerDuty maps the rendered fields onto the Events API v2 payload the
// PagerDuty publisher consumes (buildPayload reads summary/severity/source/
// timestamp and custom_details out of the nested "payload" map).
//
// `description` is upstream's name for what the Events API calls `summary`.
// `details.<key>` entries become custom_details entries, added to — not
// replacing — the fixed formatter's own diagnostics, so an operator's details
// are additive rather than destructive.
func (f *templateFormatter) overlayPagerDuty(payload map[string]any, data *templating.Data) {
	rendered := f.renderer.renderAll(f.target, data, nil, nil)
	if len(rendered) == 0 {
		return
	}

	nested, ok := payload["payload"].(map[string]any)
	if !ok {
		nested = map[string]any{}
		payload["payload"] = nested
	}

	if summary := rendered[core.TemplateFieldDescription]; summary != "" {
		nested["summary"] = summary
	}
	if severity := rendered[core.TemplateFieldSeverity]; severity != "" {
		nested["severity"] = severity
	}
	if client := rendered[core.TemplateFieldClient]; client != "" {
		payload["client"] = client
	}
	if clientURL := rendered[core.TemplateFieldClientURL]; clientURL != "" {
		payload["client_url"] = clientURL
	}

	details, ok := nested["custom_details"].(map[string]any)
	if !ok {
		details = map[string]any{}
		nested["custom_details"] = details
	}
	for field, value := range rendered {
		if key, found := strings.CutPrefix(field, core.TemplateFieldDetailsPrefix); found && key != "" {
			details[key] = value
		}
	}
}

// overlayTelegram replaces the message body with the rendered `message` field.
//
// parse_mode is NOT touched: it is a plain enum that the publisher already takes
// from the target's own config, and upstream does not template it either.
// Truncation to Telegram's 4096-character limit stays where it was — in the
// publisher, after formatting — so a long rendered message is cut exactly like a
// long fixed-formatter one.
func (f *templateFormatter) overlayTelegram(payload map[string]any, data *templating.Data) {
	rendered := f.renderer.renderAll(f.target, data, nil, func(field string) bool {
		return field == core.TemplateFieldMessage
	})

	if message, ok := rendered[core.TemplateFieldMessage]; ok && strings.TrimSpace(message) != "" {
		payload["text"] = message
	}
}

// ============================================================================
// Email (does not go through AlertFormatter)
// ============================================================================

// emailContentRenderer is implemented by templateFormatter and consumed by the
// email publisher.
//
// Email is the one delivered integration whose body does NOT come from
// AlertFormatter: EnhancedEmailPublisher renders its own subject/HTML/text from
// target.Headers through a separate template package. So the decorator cannot
// overlay anything for it — instead the publisher asks the formatter it was
// given whether it can supply upstream-templated content, via this interface.
//
// Why a type assertion on the formatter rather than a new constructor argument:
// the publisher already receives PublisherFactory.formatterFor(target), which
// carries both the renderer and the target. Threading a second dependency
// through NewEnhancedEmailPublisher would change a constructor that other tracks
// are editing, for information the publisher can already reach.
type emailContentRenderer interface {
	// RenderEmailContent renders the target's `subject`/`html`/`text`/`headers.*`
	// fields. ok is false when this target has no email template fields, or when
	// the required ones failed to render — in which case the publisher keeps its
	// existing behaviour untouched.
	//
	// ctx carries the group context (see notification_context.go), so an email
	// subject renders the same `__subject` text as every other integration.
	RenderEmailContent(ctx context.Context, enrichedAlert *core.EnrichedAlert) (content emailTemplateContent, ok bool)
}

// emailTemplateContent is the rendered result. Empty fields mean "not
// templated": the publisher keeps whatever it would have produced.
type emailTemplateContent struct {
	Subject string
	HTML    string
	Text    string
	Headers map[string]string
}

// RenderEmailContent implements emailContentRenderer.
//
// The HTML body renders through the html/template instance (upstream's
// `email.default.html` is HTML, and label values reaching it are
// attacker-influenced), while subject, text body and headers render as plain
// text — escaping a subject line would be a bug, not a defence.
func (f *templateFormatter) RenderEmailContent(ctx context.Context, enrichedAlert *core.EnrichedAlert) (emailTemplateContent, bool) {
	if !f.hasAnyEmailField() {
		return emailTemplateContent{}, false
	}

	data := f.renderer.dataFor(ctx, enrichedAlert, f.target)
	htmlFields := map[string]bool{core.TemplateFieldHTML: true}
	rendered := f.renderer.renderAll(f.target, data, htmlFields, func(field string) bool {
		switch field {
		case core.TemplateFieldSubject, core.TemplateFieldHTML, core.TemplateFieldText:
			return true
		}
		return strings.HasPrefix(field, core.TemplateFieldHeadersPrefix)
	})
	if len(rendered) == 0 {
		return emailTemplateContent{}, false
	}

	content := emailTemplateContent{
		Subject: rendered[core.TemplateFieldSubject],
		HTML:    rendered[core.TemplateFieldHTML],
		Text:    rendered[core.TemplateFieldText],
	}
	for field, value := range rendered {
		if key, found := strings.CutPrefix(field, core.TemplateFieldHeadersPrefix); found && key != "" {
			if content.Headers == nil {
				content.Headers = map[string]string{}
			}
			content.Headers[key] = value
		}
	}

	// A rendered-but-empty body is not usable content: it would deliver a blank
	// email where the fixed renderer would have produced a real one.
	if content.Subject == "" && content.HTML == "" && content.Text == "" {
		return emailTemplateContent{}, false
	}
	return content, true
}

// hasAnyEmailField reports whether the target carries email template fields at
// all, so a slack/pagerduty/telegram formatter never claims to render email.
func (f *templateFormatter) hasAnyEmailField() bool {
	for field := range f.target.Templates {
		switch field {
		case core.TemplateFieldSubject, core.TemplateFieldHTML:
			return true
		}
	}
	return false
}
