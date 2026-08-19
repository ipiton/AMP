package routing

import (
	"strings"
)

// Receiver represents a notification receiver configuration.
// Each receiver has a unique name (enforced by the parser) and zero or more
// notification configs.
//
// Supported config types:
//   - WebhookConfigs (generic HTTP webhook)
//   - PagerDutyConfigs (PagerDuty Events API v2)
//   - SlackConfigs (Slack Incoming Webhooks or API)
//   - EmailConfigs (SMTP email; SMTP endpoint from GlobalConfig)
//   - TelegramConfigs (Telegram Bot API)
//
// ZERO config types is also valid: that is upstream Alertmanager's blackhole
// receiver — see Validate().
//
// Example YAML:
//
//	receivers:
//	  - name: pagerduty-critical
//	    pagerduty_configs:
//	      - routing_key: "${PAGERDUTY_KEY}"
//	        severity: critical
//	  - name: slack-oncall
//	    slack_configs:
//	      - api_url: "${SLACK_WEBHOOK}"
//	        channel: "#oncall"
//	        title: "Alert: {{ .GroupLabels.alertname }}"
type Receiver struct {
	// Name is the unique identifier for this receiver (required).
	// Must match the receiver field in routes.
	//
	// Length: 1-255 characters. Any character is allowed EXCEPT '/', which is
	// reserved because group keys are formatted "receiver=<name>/<key>" and
	// the receiver is recovered by splitting on the first '/' — see
	// ReceiverNameReservedChar / validateReceiverName in parser.go.
	//
	// Final review finding 7: this used to be `alphanum_hyphen`, which
	// rejected upstream-legal names such as "team.dba", "email:sre" and
	// "ops team", so configs migrated from upstream Alertmanager failed to
	// load.
	Name string `yaml:"name" validate:"required,receiver_name,min=1,max=255"`

	// WebhookConfigs defines generic HTTP webhook receivers
	// Used for custom integrations or unsupported platforms
	// Each webhook sends a POST request with JSON payload
	WebhookConfigs []*WebhookConfig `yaml:"webhook_configs,omitempty" validate:"dive"`

	// PagerDutyConfigs defines PagerDuty receivers
	// Uses PagerDuty Events API v2 for incident creation
	// Integrates with TN-053 (PagerDuty Publisher)
	PagerDutyConfigs []*PagerDutyConfig `yaml:"pagerduty_configs,omitempty" validate:"dive"`

	// SlackConfigs defines Slack receivers
	// Uses Slack Incoming Webhooks or Web API
	// Integrates with TN-054 (Slack Publisher)
	SlackConfigs []*SlackConfig `yaml:"slack_configs,omitempty" validate:"dive"`

	// EmailConfigs defines SMTP email receivers (FUTURE - TN-154)
	// Uses global SMTP settings from GlobalConfig
	EmailConfigs []*EmailConfig `yaml:"email_configs,omitempty" validate:"dive"`

	// TelegramConfigs defines Telegram receivers
	// Uses Telegram Bot API (sendMessage) for notifications
	// Integrates with PARITY-B3 (Telegram Publisher)
	TelegramConfigs []*TelegramConfig `yaml:"telegram_configs,omitempty" validate:"dive"`

	// Internal: Referenced tracks if receiver is used by any route
	// Set to true during validation if route references this receiver
	// Used to detect unused receivers (warning only)
	Referenced bool `yaml:"-"`
}

// Validate performs the receiver's semantic validation (the checks struct tags
// cannot express).
//
// A receiver with ZERO integration blocks is VALID — it is upstream
// Alertmanager's blackhole receiver, the classic
//
//	receivers:
//	  - name: 'null'
//
// paired with a route that sends unwanted alerts there to be dropped. Upstream
// accepts it, so an untouched upstream config must load here too.
//
// This used to return "receiver 'x' must have at least one config type
// defined", which broke two legal upstream configs (slice-1 review finding I1
// of FU-RECEIVERS-INTEGRATION):
//
//   - the blackhole receiver above;
//   - a receiver whose only block is an integration AMP cannot deliver
//     (opsgenie_configs, victorops_configs, wechat_configs). This struct has no
//     fields for those, so yaml.v3's non-strict unmarshal drops them and the
//     receiver arrives here looking empty — turning what should be a "AMP
//     cannot deliver through this integration" WARNING (see
//     internal/config.logUnsupportedReceiverIntegrations) into a hard load
//     failure.
//
// Kept as a method (rather than deleted) because it is the parser's hook for
// per-receiver semantic checks; it currently has none that can fail.
func (r *Receiver) Validate() error {
	return nil
}

// GetConfigCount returns the total number of notification configs.
// Used for logging and statistics.
func (r *Receiver) GetConfigCount() int {
	return len(r.WebhookConfigs) +
		len(r.PagerDutyConfigs) +
		len(r.SlackConfigs) +
		len(r.EmailConfigs) +
		len(r.TelegramConfigs)
}

// Clone creates a deep copy of the receiver.
func (r *Receiver) Clone() *Receiver {
	clone := &Receiver{
		Name:             r.Name,
		WebhookConfigs:   make([]*WebhookConfig, len(r.WebhookConfigs)),
		PagerDutyConfigs: make([]*PagerDutyConfig, len(r.PagerDutyConfigs)),
		SlackConfigs:     make([]*SlackConfig, len(r.SlackConfigs)),
		EmailConfigs:     make([]*EmailConfig, len(r.EmailConfigs)),
		TelegramConfigs:  make([]*TelegramConfig, len(r.TelegramConfigs)),
		Referenced:       r.Referenced,
	}

	for i, cfg := range r.WebhookConfigs {
		clone.WebhookConfigs[i] = cfg.Clone()
	}
	for i, cfg := range r.PagerDutyConfigs {
		clone.PagerDutyConfigs[i] = cfg.Clone()
	}
	for i, cfg := range r.SlackConfigs {
		clone.SlackConfigs[i] = cfg.Clone()
	}
	for i, cfg := range r.EmailConfigs {
		clone.EmailConfigs[i] = cfg.Clone()
	}
	for i, cfg := range r.TelegramConfigs {
		clone.TelegramConfigs[i] = cfg.Clone()
	}

	return clone
}

// Sanitize creates a copy with sensitive data redacted.
// Used for logging and API responses (to prevent secret leaks).
//
// Redacts:
//   - Webhook URLs (keeps scheme+host, redacts query/path params)
//   - API keys, routing keys, passwords
//   - Authorization headers
func (r *Receiver) Sanitize() *Receiver {
	clone := r.Clone()

	for i, cfg := range clone.WebhookConfigs {
		clone.WebhookConfigs[i] = cfg.Sanitize()
	}
	for i, cfg := range clone.PagerDutyConfigs {
		clone.PagerDutyConfigs[i] = cfg.Sanitize()
	}
	for i, cfg := range clone.SlackConfigs {
		clone.SlackConfigs[i] = cfg.Sanitize()
	}
	for i, cfg := range clone.EmailConfigs {
		clone.EmailConfigs[i] = cfg.Sanitize()
	}
	for i, cfg := range clone.TelegramConfigs {
		clone.TelegramConfigs[i] = cfg.Sanitize()
	}

	return clone
}

// WebhookConfig represents a generic HTTP webhook receiver configuration.
// Integrates with TN-055 (Generic Webhook Publisher).
//
// Example:
//
//	webhook_configs:
//	  - url: https://webhook.site/xxx
//	    http_method: POST
//	    http_headers:
//	      Authorization: "Bearer ${API_TOKEN}"
//	    send_resolved: true
//	    max_alerts: 10
type WebhookConfig struct {
	// URL is the webhook endpoint (required — either set directly or via
	// URLFile below; the parser resolves URLFile into this field before
	// structural validation runs, so this tag alone still catches "neither
	// set").
	// Must be HTTPS in production mode
	// SSRF protection: no private IPs allowed
	//
	// Mutually exclusive with URLFile (FU7-B: *_file secret variants) — the
	// parser (resolveFileSecrets) rejects a config setting both, and resolves
	// URLFile into this field before anything else (validation, `global:`
	// fallbacks, the publishing-target builder) ever reads it, so URL is
	// always the value to use downstream regardless of which one the
	// operator set.
	URL string `yaml:"url,omitempty" validate:"required,url,https_production"`

	// URLFile is upstream's `url_file`: a path to a file containing the
	// webhook URL, re-read on every config load/reload (see
	// docs/ALERTMANAGER_COMPATIBILITY.md's rotation-caveat note). Content is
	// trimmed of trailing newline/whitespace. Not itself secret — a
	// filesystem path leaks nothing an attacker can use — so the status API
	// leaves it visible even though URL (the resolved value) is redacted.
	URLFile string `yaml:"url_file,omitempty"`

	// HTTPMethod specifies the HTTP method (default: POST)
	// Allowed: POST, PUT, PATCH
	HTTPMethod string `yaml:"http_method,omitempty"`

	// HTTPHeaders are custom headers to include in requests
	// Sensitive headers (Authorization, API-Key) should use secret references
	HTTPHeaders map[string]string `yaml:"http_headers,omitempty"`

	// HTTPConfig specifies HTTP client configuration
	// Includes proxy, TLS, timeouts
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`

	// SendResolved determines if resolved notifications are sent
	// Default: true (inherited from global or route)
	SendResolved *bool `yaml:"send_resolved,omitempty"`

	// MaxAlerts limits the number of alerts per notification
	// Range: 0 (unlimited) to 1000
	// Default: 0 (unlimited)
	MaxAlerts int `yaml:"max_alerts,omitempty" validate:"min=0,max=1000"`
}

// Defaults applies default values to webhook config.
func (w *WebhookConfig) Defaults() {
	if w.HTTPMethod == "" {
		w.HTTPMethod = "POST"
	}
	if w.SendResolved == nil {
		sendResolved := true
		w.SendResolved = &sendResolved
	}
	if w.HTTPConfig != nil {
		w.HTTPConfig.Defaults()
	}
}

// Clone creates a deep copy.
func (w *WebhookConfig) Clone() *WebhookConfig {
	clone := &WebhookConfig{
		URL:        w.URL,
		URLFile:    w.URLFile,
		HTTPMethod: w.HTTPMethod,
		MaxAlerts:  w.MaxAlerts,
	}

	if w.HTTPHeaders != nil {
		clone.HTTPHeaders = make(map[string]string, len(w.HTTPHeaders))
		for k, v := range w.HTTPHeaders {
			clone.HTTPHeaders[k] = v
		}
	}
	if w.HTTPConfig != nil {
		clone.HTTPConfig = w.HTTPConfig.Clone()
	}
	if w.SendResolved != nil {
		sendResolved := *w.SendResolved
		clone.SendResolved = &sendResolved
	}

	return clone
}

// Sanitize redacts sensitive data.
func (w *WebhookConfig) Sanitize() *WebhookConfig {
	clone := w.Clone()
	clone.URL = sanitizeURL(clone.URL)

	// Redact sensitive headers
	if clone.HTTPHeaders != nil {
		for key := range clone.HTTPHeaders {
			if isSensitiveHeader(key) {
				clone.HTTPHeaders[key] = "[REDACTED]"
			}
		}
	}

	return clone
}

// PagerDutyConfig represents a PagerDuty receiver configuration.
// Uses PagerDuty Events API v2 for incident management.
// Integrates with TN-053 (PagerDuty Publisher).
//
// Example:
//
//	pagerduty_configs:
//	  - routing_key: "${PAGERDUTY_ROUTING_KEY}"
//	    severity: critical
//	    description: "{{ .GroupLabels.alertname }}"
//	    details:
//	      environment: "{{ .CommonLabels.environment }}"
type PagerDutyConfig struct {
	// RoutingKey is the integration key (required, non-empty).
	// Obtained from PagerDuty service integration
	// Should use secret reference: ${PAGERDUTY_KEY}
	//
	// task fu2-d item 10: was validate:"required,len=32", stricter than
	// upstream Alertmanager's PagerDuty routing_key (a plain Secret, no
	// fixed length) — PagerDuty's own integration keys are not guaranteed
	// to stay exactly 32 characters, so a key of any other length was
	// silently rejected by config validation.
	//
	// Mutually exclusive with RoutingKeyFile (FU7-B: *_file secret variants);
	// see WebhookConfig.URLFile's doc comment for the resolution mechanics
	// shared by every *_file field in this package.
	RoutingKey string `yaml:"routing_key,omitempty" validate:"required"`

	// RoutingKeyFile is upstream's `routing_key_file`: a path to a file
	// containing the integration key, re-read on every config load/reload.
	// Content is trimmed of trailing newline/whitespace. The path itself is
	// not secret, so the status API leaves it visible.
	RoutingKeyFile string `yaml:"routing_key_file,omitempty"`

	// ServiceKey is the legacy service key (deprecated)
	// Use RoutingKey instead
	ServiceKey string `yaml:"service_key,omitempty"`

	// URL is the PagerDuty Events API endpoint
	// Default: https://events.pagerduty.com/v2/enqueue
	URL string `yaml:"url,omitempty" validate:"omitempty,url,https_production"`

	// Severity specifies the incident severity
	// Values: critical, error, warning, info
	// Default: error
	Severity string `yaml:"severity,omitempty" validate:"omitempty,oneof=critical error warning info"`

	// Class, Component, Group provide incident categorization
	Class     string `yaml:"class,omitempty"`
	Component string `yaml:"component,omitempty"`
	Group     string `yaml:"group,omitempty"`

	// Description is the incident summary (default: alert summary)
	Description string `yaml:"description,omitempty"`

	// Details are custom key-value pairs for incident context
	Details map[string]string `yaml:"details,omitempty"`

	// SendResolved determines if resolved notifications are sent
	SendResolved *bool `yaml:"send_resolved,omitempty"`

	// HTTPConfig specifies HTTP client configuration
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`
}

// Defaults applies defaults.
func (p *PagerDutyConfig) Defaults() {
	if p.URL == "" {
		p.URL = "https://events.pagerduty.com/v2/enqueue"
	}
	if p.Severity == "" {
		p.Severity = "error"
	}
	if p.SendResolved == nil {
		sendResolved := true
		p.SendResolved = &sendResolved
	}
	if p.HTTPConfig != nil {
		p.HTTPConfig.Defaults()
	}
}

// Clone creates deep copy.
func (p *PagerDutyConfig) Clone() *PagerDutyConfig {
	clone := &PagerDutyConfig{
		RoutingKey:     p.RoutingKey,
		RoutingKeyFile: p.RoutingKeyFile,
		ServiceKey:     p.ServiceKey,
		URL:            p.URL,
		Severity:       p.Severity,
		Class:          p.Class,
		Component:      p.Component,
		Group:          p.Group,
		Description:    p.Description,
	}

	if p.Details != nil {
		clone.Details = make(map[string]string, len(p.Details))
		for k, v := range p.Details {
			clone.Details[k] = v
		}
	}
	if p.HTTPConfig != nil {
		clone.HTTPConfig = p.HTTPConfig.Clone()
	}
	if p.SendResolved != nil {
		sendResolved := *p.SendResolved
		clone.SendResolved = &sendResolved
	}

	return clone
}

// Sanitize redacts routing key.
func (p *PagerDutyConfig) Sanitize() *PagerDutyConfig {
	clone := p.Clone()
	if len(clone.RoutingKey) > 0 {
		clone.RoutingKey = clone.RoutingKey[:8] + "..." + "[REDACTED]"
	}
	if len(clone.ServiceKey) > 0 {
		clone.ServiceKey = "[REDACTED]"
	}
	return clone
}

// SlackConfig represents a Slack receiver configuration.
// Integrates with TN-054 (Slack Publisher).
//
// Example:
//
//	slack_configs:
//	  - api_url: "${SLACK_WEBHOOK_URL}"
//	    channel: "#oncall"
//	    username: "Alertmanager"
//	    title: "{{ .GroupLabels.alertname }}"
//	    text: "{{ range .Alerts }}{{ .Annotations.summary }}\n{{ end }}"
//	    color: danger
type SlackConfig struct {
	// APIURL is the Slack webhook or API URL.
	// Must be HTTPS.
	//
	// Optional at the FIELD level since FU-RECEIVERS-INTEGRATION slice 2:
	// upstream lets a slack_config omit it and inherit `global.slack_api_url`.
	// The parser resolves that fallback (resolveGlobalFallbacks) and then
	// requires the result to be non-empty (validateSemantics), so a config with
	// neither still fails to load — with a message naming both places.
	APIURL string `yaml:"api_url,omitempty" validate:"omitempty,url,https_production"`

	// APIURLFile is upstream's `api_url_file`: a path to a file containing
	// the Slack webhook/API URL, re-read on every config load/reload.
	// Mutually exclusive with APIURL (FU7-B: *_file secret variants); see
	// WebhookConfig.URLFile's doc comment for the resolution mechanics. Not
	// itself secret, so the status API leaves it visible.
	APIURLFile string `yaml:"api_url_file,omitempty"`

	// Channel specifies the target channel or user
	// Format: #channel or @username
	Channel string `yaml:"channel,omitempty" validate:"omitempty,slack_channel"`

	// Username overrides the bot display name
	Username string `yaml:"username,omitempty"`

	// IconEmoji overrides the bot icon (e.g., :alert:)
	// Mutually exclusive with IconURL
	IconEmoji string `yaml:"icon_emoji,omitempty" validate:"omitempty,emoji"`

	// IconURL overrides the bot icon with a URL
	// Mutually exclusive with IconEmoji
	IconURL string `yaml:"icon_url,omitempty" validate:"omitempty,url"`

	// Title is the message title
	Title string `yaml:"title,omitempty"`

	// TitleLink adds a URL to the title
	TitleLink string `yaml:"title_link,omitempty" validate:"omitempty,url"`

	// Pretext appears above the main message block
	Pretext string `yaml:"pretext,omitempty"`

	// Text is the main message body
	Text string `yaml:"text,omitempty"`

	// Fields are structured key-value pairs
	Fields []*SlackField `yaml:"fields,omitempty"`

	// Actions are interactive buttons (Slack API only)
	Actions []*SlackAction `yaml:"actions,omitempty"`

	// Color determines the message color bar
	// Values: good (green), warning (yellow), danger (red), #hex
	Color string `yaml:"color,omitempty" validate:"omitempty,slack_color"`

	// SendResolved determines if resolved notifications are sent
	SendResolved *bool `yaml:"send_resolved,omitempty"`

	// ShortFields renders fields in two columns
	ShortFields bool `yaml:"short_fields,omitempty"`

	// HTTPConfig specifies HTTP client configuration
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`
}

// Defaults applies defaults.
func (s *SlackConfig) Defaults() {
	if s.SendResolved == nil {
		sendResolved := true
		s.SendResolved = &sendResolved
	}
	if s.HTTPConfig != nil {
		s.HTTPConfig.Defaults()
	}
}

// Clone creates deep copy.
func (s *SlackConfig) Clone() *SlackConfig {
	clone := &SlackConfig{
		APIURL:      s.APIURL,
		APIURLFile:  s.APIURLFile,
		Channel:     s.Channel,
		Username:    s.Username,
		IconEmoji:   s.IconEmoji,
		IconURL:     s.IconURL,
		Title:       s.Title,
		TitleLink:   s.TitleLink,
		Pretext:     s.Pretext,
		Text:        s.Text,
		Color:       s.Color,
		ShortFields: s.ShortFields,
		Fields:      make([]*SlackField, len(s.Fields)),
		Actions:     make([]*SlackAction, len(s.Actions)),
	}

	for i, f := range s.Fields {
		clone.Fields[i] = f.Clone()
	}
	for i, a := range s.Actions {
		clone.Actions[i] = a.Clone()
	}

	if s.HTTPConfig != nil {
		clone.HTTPConfig = s.HTTPConfig.Clone()
	}
	if s.SendResolved != nil {
		sendResolved := *s.SendResolved
		clone.SendResolved = &sendResolved
	}

	return clone
}

// Sanitize redacts API URL.
func (s *SlackConfig) Sanitize() *SlackConfig {
	clone := s.Clone()
	clone.APIURL = sanitizeURL(clone.APIURL)
	return clone
}

// SlackField represents a Slack message field.
type SlackField struct {
	Title string `yaml:"title"`
	Value string `yaml:"value"`
	Short bool   `yaml:"short,omitempty"`
}

// Clone creates deep copy.
func (f *SlackField) Clone() *SlackField {
	return &SlackField{
		Title: f.Title,
		Value: f.Value,
		Short: f.Short,
	}
}

// SlackAction represents a Slack interactive button.
type SlackAction struct {
	Type  string `yaml:"type" validate:"required"`
	Text  string `yaml:"text" validate:"required"`
	URL   string `yaml:"url,omitempty" validate:"omitempty,url"`
	Style string `yaml:"style,omitempty" validate:"omitempty,oneof=default primary danger"`
}

// Clone creates deep copy.
func (a *SlackAction) Clone() *SlackAction {
	return &SlackAction{
		Type:  a.Type,
		Text:  a.Text,
		URL:   a.URL,
		Style: a.Style,
	}
}

// EmailConfig represents an SMTP email receiver (FUTURE - TN-154).
type EmailConfig struct {
	To           string            `yaml:"to" validate:"required,email"`
	From         string            `yaml:"from,omitempty"`
	Subject      string            `yaml:"subject,omitempty"`
	HTML         string            `yaml:"html,omitempty"`
	Text         string            `yaml:"text,omitempty"`
	Headers      map[string]string `yaml:"headers,omitempty"`
	SendResolved *bool             `yaml:"send_resolved,omitempty"`
}

// Defaults applies defaults.
func (e *EmailConfig) Defaults() {
	if e.SendResolved == nil {
		sendResolved := true
		e.SendResolved = &sendResolved
	}
}

// Clone creates deep copy.
func (e *EmailConfig) Clone() *EmailConfig {
	clone := &EmailConfig{
		To:      e.To,
		From:    e.From,
		Subject: e.Subject,
		HTML:    e.HTML,
		Text:    e.Text,
	}

	if e.Headers != nil {
		clone.Headers = make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			clone.Headers[k] = v
		}
	}
	if e.SendResolved != nil {
		sendResolved := *e.SendResolved
		clone.SendResolved = &sendResolved
	}

	return clone
}

// Sanitize redacts email addresses.
func (e *EmailConfig) Sanitize() *EmailConfig {
	clone := e.Clone()
	clone.To = maskEmail(clone.To)
	clone.From = maskEmail(clone.From)
	return clone
}

// maskEmail partially masks an email address.
func maskEmail(email string) string {
	if email == "" {
		return ""
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "[INVALID]"
	}
	local := parts[0]
	if len(local) <= 2 {
		return "**@" + parts[1]
	}
	return local[:2] + "***@" + parts[1]
}

// TelegramConfig represents a Telegram receiver configuration.
// Uses the Telegram Bot API (sendMessage) for notifications.
// Integrates with PARITY-B3 (Telegram Publisher).
//
// Note: this struct is this layer's config schema (parsed/validated from
// route YAML). The runtime publisher (internal/infrastructure/publishing)
// is target-based and does not currently consume TelegramConfig directly -
// same as SlackConfig/PagerDutyConfig today. See Message below.
//
// Example:
//
//	telegram_configs:
//	  - bot_token: "${TELEGRAM_BOT_TOKEN}"
//	    chat_id: "-1001234567890"
//	    parse_mode: HTML
type TelegramConfig struct {
	// BotToken is the Telegram bot API token (required)
	// Obtained from @BotFather. Should use a secret reference: ${TELEGRAM_BOT_TOKEN}
	// NEVER logged in plaintext - see Sanitize().
	BotToken string `yaml:"bot_token,omitempty" validate:"required"`

	// BotTokenFile is upstream's `bot_token_file`: a path to a file
	// containing the bot API token, re-read on every config load/reload.
	// Mutually exclusive with BotToken (FU7-B: *_file secret variants); see
	// WebhookConfig.URLFile's doc comment for the resolution mechanics. Not
	// itself secret, so the status API leaves it visible.
	BotTokenFile string `yaml:"bot_token_file,omitempty"`

	// ChatID is the target chat/channel/group identifier (required)
	// Format: numeric chat id (may be negative for groups/channels) or "@channelusername"
	ChatID string `yaml:"chat_id" validate:"required,telegram_chat_id"`

	// MessageThreadID targets a specific forum topic thread (optional)
	MessageThreadID int `yaml:"message_thread_id,omitempty" validate:"omitempty,min=0"`

	// ParseMode specifies message formatting
	// Values: HTML, Markdown, MarkdownV2
	// Default: HTML
	ParseMode string `yaml:"parse_mode,omitempty" validate:"omitempty,oneof=HTML Markdown MarkdownV2"`

	// APIURL is the Telegram Bot API base URL
	// Default: https://api.telegram.org
	APIURL string `yaml:"api_url,omitempty" validate:"omitempty,url,https_production"`

	// Message is reserved for a future notification message template
	// override. NOT YET WIRED to the runtime publisher - the publisher
	// currently always renders via the shared AlertFormatter
	// (core.FormatTelegram), same as Slack's own Text/Title/Pretext fields
	// here are parsed but not consumed by the runtime publisher today.
	Message string `yaml:"message,omitempty"`

	// DisableNotifications sends the message silently (no sound/vibration)
	DisableNotifications bool `yaml:"disable_notifications,omitempty"`

	// SendResolved determines if resolved notifications are sent
	// Default: true
	SendResolved *bool `yaml:"send_resolved,omitempty"`

	// HTTPConfig specifies HTTP client configuration
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty"`
}

// Defaults applies default values to the Telegram config.
func (t *TelegramConfig) Defaults() {
	if t.APIURL == "" {
		t.APIURL = "https://api.telegram.org"
	}
	if t.ParseMode == "" {
		t.ParseMode = "HTML"
	}
	if t.SendResolved == nil {
		sendResolved := true
		t.SendResolved = &sendResolved
	}
	if t.HTTPConfig != nil {
		t.HTTPConfig.Defaults()
	}
}

// Clone creates a deep copy.
func (t *TelegramConfig) Clone() *TelegramConfig {
	clone := &TelegramConfig{
		BotToken:             t.BotToken,
		BotTokenFile:         t.BotTokenFile,
		ChatID:               t.ChatID,
		MessageThreadID:      t.MessageThreadID,
		ParseMode:            t.ParseMode,
		APIURL:               t.APIURL,
		Message:              t.Message,
		DisableNotifications: t.DisableNotifications,
	}

	if t.HTTPConfig != nil {
		clone.HTTPConfig = t.HTTPConfig.Clone()
	}
	if t.SendResolved != nil {
		sendResolved := *t.SendResolved
		clone.SendResolved = &sendResolved
	}

	return clone
}

// Sanitize redacts the bot token (never logged in plaintext).
func (t *TelegramConfig) Sanitize() *TelegramConfig {
	clone := t.Clone()
	if clone.BotToken != "" {
		clone.BotToken = "[REDACTED]"
	}
	return clone
}
