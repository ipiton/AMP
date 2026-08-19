package publishing

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// Config-provisioned publishing targets (FU-RECEIVERS-INTEGRATION, slice 1)
// ============================================================================
//
// WHY THIS FILE EXISTS: until now, the `receivers:` section of an
// Alertmanager-shaped config (webhook_configs, slack_configs,
// pagerduty_configs, telegram_configs, email_configs — see
// infrastructure/routing.Receiver) parsed and validated but was INERT. The
// only thing that ever produced a core.PublishingTarget was
// discovery_parse.go, i.e. a Kubernetes Secret. An operator migrating an
// untouched upstream Alertmanager config therefore got routing, grouping,
// inhibition and silencing for free, and delivered nothing at all until they
// hand-created a Secret per integration.
//
// BuildConfigTargets closes that gap: every integration block of every
// receiver becomes an in-memory core.PublishingTarget, encoded EXACTLY the
// way the K8s-sourced targets are encoded, so the publisher layer
// (PublisherFactory.CreatePublisherForTarget) needs no changes at all.
//
// Controller rulings honoured here:
//
//	R1  deterministic name `cfg:<receiver>/<type><idx>`. The `cfg:` prefix is
//	    a namespace no K8s-sourced target can occupy: validateTarget
//	    (discovery_validate.go) enforces DNS-1123 names for those, and ':'
//	    and '/' are illegal there. So config and Secret targets can never
//	    collide, and no precedence rule is needed.
//	R2  every target is receiver-scoped BY CONSTRUCTION: Receivers is always
//	    exactly [<receiver name>], never empty. Empty means "belongs to all
//	    receivers" (PublishingCoordinator.targetMatchesReceiver) and is
//	    reserved for legacy unscoped K8s targets.
//	R3  the returned targets are merged into the SAME discovery view as the
//	    K8s ones (DefaultTargetDiscoveryManager.SetConfigTargets), never
//	    persisted anywhere; they are rebuilt from scratch on every config
//	    load/reload.
//
// SECRETS: config text carries credentials (webhook URLs with tokens,
// bot_token, routing_key, smtp_auth_password). Nothing in this file logs a
// URL, a header value or any config value — only receiver names, integration
// kinds and indices, all of which are safe. Target NAMES are safe for the
// same reason: they are built from the receiver name and the integration
// kind only.

// ConfigTargetPrefix namespaces every config-provisioned target name (R1).
// K8s-sourced target names are DNS-1123-validated, so they can never start
// with "cfg:".
const ConfigTargetPrefix = "cfg:"

// Integration kind identifiers used in config target names. These are the
// name fragment only — the publishing target TYPE is a separate mapping (see
// the table in configIntegrationKinds).
const (
	configKindWebhook   = "webhook"
	configKindSlack     = "slack"
	configKindPagerDuty = "pagerduty"
	configKindTelegram  = "telegram"
	configKindEmail     = "email"
)

// pagerDutyEventsPath is the Events API v2 path that PagerDutyConfig.Defaults
// bakes into its URL (upstream types `pagerduty_configs[].url` as the full
// endpoint) and that the publisher's own client appends to target.URL.
//
// The builder strips it so a config-provisioned target is encoded exactly like
// a K8s-sourced one — a base URL. Corrected in the slice-1 fix round (review
// finding I2): the original comment claimed the client appended "/v2/enqueue",
// but it actually appended "/v2/events", which is not a PagerDuty endpoint at
// all. That bug is fixed in pagerduty_client.go (see pagerDutyEnqueuePath),
// which now also normalises a full endpoint away itself — so this strip is
// belt-and-braces rather than the only line of defence.
const pagerDutyEventsPath = "/v2/enqueue"

// defaultSMTPPort mirrors extractSMTPConfig's own default (publisher side),
// used when global.smtp_smarthost carries a bare host with no port.
const defaultSMTPPort = 587

// IsConfigTarget reports whether a target was provisioned from the config's
// `receivers:` section rather than from a Kubernetes Secret. Used for
// source-labelled metrics/stats; see TargetSource.
func IsConfigTarget(target *core.PublishingTarget) bool {
	return target != nil && strings.HasPrefix(target.Name, ConfigTargetPrefix)
}

// Target source labels for metrics and stats.
const (
	TargetSourceConfig = "config"
	TargetSourceK8s    = "k8s"
)

// TargetSource returns "config" for config-provisioned targets and "k8s" for
// Secret-provisioned ones.
func TargetSource(target *core.PublishingTarget) string {
	if IsConfigTarget(target) {
		return TargetSourceConfig
	}
	return TargetSourceK8s
}

// ConfigTargetName builds the deterministic name of a config-provisioned
// target: `cfg:<receiver>/<kind><idx>` (R1).
func ConfigTargetName(receiver, kind string, index int) string {
	return ConfigTargetPrefix + receiver + "/" + kind + strconv.Itoa(index)
}

// BuildConfigTargets converts every integration block of every receiver in rc
// into a core.PublishingTarget.
//
// Order is deterministic: receivers in config order, and within a receiver the
// integration kinds in a fixed order (webhook, slack, pagerduty, telegram,
// email), each in its own config-declared order. Callers rely on this for
// stable naming across reloads, and tests rely on it for stable assertions.
//
// An integration that cannot produce a working target (missing credential or
// endpoint) is SKIPPED with a WARN naming the receiver, kind and index — never
// a hard error: the rest of the config must still deliver, and the config
// itself already passed routing.Parse()'s own validation, so reaching a skip
// here means either a genuinely incomplete integration or one whose endpoint
// comes from a `global:` fallback that is not wired yet (slice 2).
//
// rc == nil (no `route:` section — legacy/lite single-receiver mode) yields
// nil, not an error.
func BuildConfigTargets(rc *infraroute.RouteConfig, logger *slog.Logger) []*core.PublishingTarget {
	if rc == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	targets := make([]*core.PublishingTarget, 0, len(rc.Receivers))
	for _, receiver := range rc.Receivers {
		if receiver == nil || receiver.Name == "" {
			continue
		}
		targets = append(targets, buildReceiverTargets(receiver, rc.Global, logger)...)
	}

	targets = dedupeTargetNames(targets, logger)
	if len(targets) == 0 {
		return nil
	}
	return targets
}

// dedupeTargetNames drops targets whose name was already produced and WARNs
// about each one (slice-1 review finding I3).
//
// Names are derived from receiver name + integration kind + index, so the only
// way to get a collision is two receivers sharing a NAME. The routing parser
// now rejects that outright (upstream semantics), which makes this a
// defence-in-depth layer rather than the primary fix: the target cache is keyed
// by name, so without it a duplicate silently overwrites the earlier target and
// one receiver's integrations never deliver — invisible in logs AND in the
// target count. Any other entry point that builds a RouteConfig without going
// through the parser gets the same protection, loudly.
func dedupeTargetNames(targets []*core.PublishingTarget, logger *slog.Logger) []*core.PublishingTarget {
	seen := make(map[string]struct{}, len(targets))
	out := targets[:0]
	for _, target := range targets {
		if _, dup := seen[target.Name]; dup {
			logger.Warn("Dropping config-provisioned publishing target with a duplicate name; check for duplicate receiver names",
				"target", target.Name,
				"receivers", target.Receivers,
			)
			continue
		}
		seen[target.Name] = struct{}{}
		out = append(out, target)
	}
	return out
}

// buildReceiverTargets builds the targets for a single receiver.
func buildReceiverTargets(receiver *infraroute.Receiver, global *infraroute.GlobalConfig, logger *slog.Logger) []*core.PublishingTarget {
	var out []*core.PublishingTarget

	add := func(target *core.PublishingTarget, err error, kind string, idx int) {
		if err != nil {
			// Message names the receiver/kind/index only — never a URL, token
			// or header value.
			logger.Warn("Skipping receiver integration that cannot be provisioned as a publishing target",
				"receiver", receiver.Name,
				"integration", kind+"_configs",
				"index", idx,
				"reason", err.Error(),
			)
			return
		}
		out = append(out, target)
	}

	for i, cfg := range receiver.WebhookConfigs {
		target, err := webhookTarget(receiver.Name, i, cfg)
		add(target, err, configKindWebhook, i)
	}
	for i, cfg := range receiver.SlackConfigs {
		target, err := slackTarget(receiver.Name, i, cfg)
		add(target, err, configKindSlack, i)
	}
	for i, cfg := range receiver.PagerDutyConfigs {
		target, err := pagerDutyTarget(receiver.Name, i, cfg)
		add(target, err, configKindPagerDuty, i)
	}
	for i, cfg := range receiver.TelegramConfigs {
		target, err := telegramTarget(receiver.Name, i, cfg)
		add(target, err, configKindTelegram, i)
	}
	for i, cfg := range receiver.EmailConfigs {
		target, err := emailTarget(receiver.Name, i, cfg, global)
		add(target, err, configKindEmail, i)
	}

	return out
}

// newConfigTarget builds the common skeleton every config-provisioned target
// shares: cfg: name (R1), receiver scoping (R2), enabled, non-nil maps (the
// same post-conditions applyDefaults gives K8s-sourced targets).
func newConfigTarget(receiver, kind string, index int, targetType string, format core.PublishingFormat, url string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:         ConfigTargetName(receiver, kind, index),
		Type:         targetType,
		URL:          url,
		Enabled:      true,
		Format:       format,
		Headers:      make(map[string]string),
		FilterConfig: make(map[string]any),
		Receivers:    []string{receiver},
	}
}

// webhookTarget maps one webhook_configs entry.
//
// Format is alertmanager (not webhook): upstream's webhook receiver posts the
// v4 Alertmanager payload, and DefaultAlertFormatter.formatAlertmanager /
// FormatGroup are exactly that shape. Type webhook + format alertmanager is
// also the pair discovery_validate.isCompatibleTypeFormat accepts.
//
// NOT expressible on the publisher contract (documented, not silently
// dropped): http_method (WebhookPublisher always POSTs), max_alerts,
// http_config, send_resolved (slice 2).
func webhookTarget(receiver string, index int, cfg *infraroute.WebhookConfig) (*core.PublishingTarget, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config entry")
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("url is empty")
	}

	target := newConfigTarget(receiver, configKindWebhook, index, "webhook", core.FormatAlertmanager, cfg.URL)
	// http_headers -> target.Headers, the same channel the K8s path uses for
	// per-target auth (webhook_publisher_enhanced reads Authorization /
	// X-API-Key out of it, HTTPPublisher.publish sets all of them verbatim).
	// Empty keys/values are dropped: validateTarget rejects them for K8s
	// targets, and an empty header value is never meaningful.
	for k, v := range cfg.HTTPHeaders {
		if strings.TrimSpace(k) == "" || v == "" {
			continue
		}
		target.Headers[k] = v
	}
	return target, nil
}

// slackTarget maps one slack_configs entry.
//
// Only api_url survives into the target, because that is all the publisher
// layer consumes: createEnhancedSlackPublisher builds an
// HTTPSlackWebhookClient from target.URL, and EnhancedSlackPublisher renders
// the message body entirely through the shared AlertFormatter. channel /
// username / icon_* / title / text / fields / actions / color / short_fields
// are parsed and validated by routing.SlackConfig but NOT wired to the
// runtime publisher — the same honest gap the Telegram Message field already
// documents. They are deliberately NOT stuffed into Headers: Headers are HTTP
// headers on every fallback publish path, so a "channel" header would be a
// bogus wire header rather than a Slack field.
func slackTarget(receiver string, index int, cfg *infraroute.SlackConfig) (*core.PublishingTarget, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config entry")
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		// Slice 2 wires global.slack_api_url as the fallback for this.
		return nil, fmt.Errorf("api_url is empty (global slack_api_url fallback not wired yet)")
	}

	return newConfigTarget(receiver, configKindSlack, index, "slack", core.FormatSlack, cfg.APIURL), nil
}

// pagerDutyTarget maps one pagerduty_configs entry.
//
// routing_key travels in the "routing_key" header — exactly where
// createEnhancedPagerDutyPublisher and EnhancedPagerDutyPublisher.
// extractRoutingKey read it from for K8s targets.
//
// URL needs a conversion, not a copy: PagerDutyConfig.Defaults() stores the
// FULL Events API endpoint (https://events.pagerduty.com/v2/enqueue, matching
// upstream's `url:` semantics), while the publisher's client treats target.URL
// as a BASE and appends the enqueue path itself.
//
// NOT expressible: severity/class/component/group/description/details
// (formatPagerDuty derives severity/summary from the alert itself),
// http_config, send_resolved (slice 2).
func pagerDutyTarget(receiver string, index int, cfg *infraroute.PagerDutyConfig) (*core.PublishingTarget, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config entry")
	}

	routingKey := strings.TrimSpace(cfg.RoutingKey)
	if routingKey == "" {
		routingKey = strings.TrimSpace(cfg.ServiceKey)
	}
	if routingKey == "" {
		return nil, fmt.Errorf("routing_key is empty")
	}

	target := newConfigTarget(receiver, configKindPagerDuty, index, "pagerduty", core.FormatPagerDuty, pagerDutyBaseURL(cfg.URL))
	target.Headers["routing_key"] = routingKey
	return target, nil
}

// pagerDutyBaseURL converts an upstream-style pagerduty `url:` (the full
// Events API endpoint) into the base URL the publisher's client expects.
// An empty url yields "" so the factory applies its own public default.
func pagerDutyBaseURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}
	trimmed := strings.TrimRight(url, "/")
	// Review finding M1: the fallback used to return the UN-trimmed input, so
	// "https://events.pagerduty.com/" produced a base with a trailing slash and
	// a doubled separator in the final request path.
	return strings.TrimSuffix(trimmed, pagerDutyEventsPath)
}

// telegramTarget maps one telegram_configs entry.
//
// bot_token / chat_id / message_thread_id / disable_notifications travel in
// Headers under exactly the keys createEnhancedTelegramPublisher reads, and
// api_url becomes target.URL (the factory's own default applies when empty).
//
// message_thread_id is only emitted when non-zero and
// disable_notifications only when true: a "0"/"false" header would be noise,
// and the publisher's own zero values already mean the same thing.
//
// NOT expressible: parse_mode (EnhancedTelegramPublisher always renders via
// core.FormatTelegram), message, http_config, send_resolved (slice 2).
func telegramTarget(receiver string, index int, cfg *infraroute.TelegramConfig) (*core.PublishingTarget, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config entry")
	}
	botToken := strings.TrimSpace(cfg.BotToken)
	chatID := strings.TrimSpace(cfg.ChatID)
	if botToken == "" {
		return nil, fmt.Errorf("bot_token is empty")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is empty")
	}

	target := newConfigTarget(receiver, configKindTelegram, index, "telegram", core.FormatTelegram, strings.TrimSpace(cfg.APIURL))
	target.Headers["bot_token"] = botToken
	target.Headers["chat_id"] = chatID
	if cfg.MessageThreadID > 0 {
		target.Headers["message_thread_id"] = strconv.Itoa(cfg.MessageThreadID)
	}
	if cfg.DisableNotifications {
		target.Headers["disable_notifications"] = "true"
	}
	return target, nil
}

// emailTarget maps one email_configs entry.
//
// Type/format mirror the shape the email publisher already expects from a
// K8s-sourced target: type "email" with format "webhook" (there is no
// core.FormatEmail; EnhancedEmailPublisher renders its own templates and
// never calls the formatter for a wire shape).
//
// The SMTP endpoint is NOT part of routing.EmailConfig — upstream keeps it in
// `global:` (smtp_smarthost / smtp_from / smtp_auth_*) and so does AMP, so
// the only possible source for it is global. That makes reading global here a
// mapping requirement, not the slice-2 "global fallback" machinery (which is
// about per-integration endpoints like slack_api_url inheriting from global
// and erroring when neither is set).
//
// NOT expressible (review finding M4, previously undocumented):
//
//   - `email_configs[].headers` — parsed by routing.EmailConfig, read by
//     nothing on the publisher side (extractEmailConfig ignores it), so custom
//     mail headers are dropped.
//   - upstream's PER-RECEIVER `smarthost`/`auth_username`/`auth_password`/
//     `require_tls` — routing.EmailConfig models none of them, so an upstream
//     config that sets SMTP per email_config is silently reduced to the GLOBAL
//     SMTP settings (or skipped entirely, with the warning below, when
//     `global:` has none). Two receivers needing different SMTP servers cannot
//     be expressed at all today.
//
// URL is informational only: the email publisher never dials it. It is set to
// smtp://<smarthost> so the target is self-describing in logs and stats
// rather than carrying a fake http:// placeholder. Health probes cannot check
// an SMTP endpoint over HTTP and will report such a target unhealthy — the
// same as today's (unreachable, see validateTarget) K8s email targets.
func emailTarget(receiver string, index int, cfg *infraroute.EmailConfig, global *infraroute.GlobalConfig) (*core.PublishingTarget, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config entry")
	}
	to := strings.TrimSpace(cfg.To)
	if to == "" {
		return nil, fmt.Errorf("to is empty")
	}

	host, port := "", 0
	var from, username, password string
	requireTLS := false
	if global != nil {
		host, port = splitSMTPSmarthost(global.SMTPSmartHost)
		from = strings.TrimSpace(global.SMTPFrom)
		username = global.SMTPAuthUsername
		password = global.SMTPAuthPassword
		requireTLS = global.SMTPRequireTLS
	}
	if host == "" {
		return nil, fmt.Errorf("no SMTP smarthost configured (global.smtp_smarthost is empty)")
	}
	if strings.TrimSpace(cfg.From) != "" {
		from = strings.TrimSpace(cfg.From)
	}

	url := "smtp://" + net.JoinHostPort(host, strconv.Itoa(port))
	target := newConfigTarget(receiver, configKindEmail, index, "email", core.FormatWebhook, url)

	// Header keys are exactly the ones extractSMTPConfig / buildEmailMessage
	// read on the publisher side.
	target.Headers["to"] = to
	target.Headers["smtp_host"] = host
	target.Headers["smtp_port"] = strconv.Itoa(port)
	if from != "" {
		target.Headers["from"] = from
	}
	if username != "" {
		target.Headers["smtp_username"] = username
	}
	if password != "" {
		target.Headers["smtp_password"] = password
	}
	if requireTLS {
		target.Headers["smtp_tls"] = "true"
	}
	if s := strings.TrimSpace(cfg.Subject); s != "" {
		target.Headers["subject_template"] = s
	}
	if s := strings.TrimSpace(cfg.HTML); s != "" {
		target.Headers["html_template"] = s
	}
	if s := strings.TrimSpace(cfg.Text); s != "" {
		target.Headers["text_template"] = s
	}
	return target, nil
}

// splitSMTPSmarthost splits an upstream `smtp_smarthost` ("host:port", or a
// bare host) into its parts, defaulting the port to the publisher's own
// default (587) when absent or unparseable.
func splitSMTPSmarthost(raw string) (string, int) {
	smarthost := strings.TrimSpace(raw)
	if smarthost == "" {
		return "", 0
	}

	host, portStr, err := net.SplitHostPort(smarthost)
	if err != nil {
		return smarthost, defaultSMTPPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, defaultSMTPPort
	}
	return host, port
}
