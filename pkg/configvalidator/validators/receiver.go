package validators

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator"
)

// ReceiverValidator performs semantic validation of Alertmanager receivers and their integrations.
type ReceiverValidator struct {
	options configvalidator.Options
	logger  *slog.Logger
}

// NewReceiverValidator creates a new ReceiverValidator instance.
func NewReceiverValidator(opts configvalidator.Options, logger *slog.Logger) *ReceiverValidator {
	return &ReceiverValidator{
		options: opts,
		logger:  logger,
	}
}

// Validate performs comprehensive validation of all receivers and their integrations.
func (rv *ReceiverValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	rv.logger.Debug("starting receiver validation")

	if cfg.Receivers == nil || len(cfg.Receivers) == 0 {
		result.AddError(
			"E110",
			"No receivers defined. At least one receiver is required.",
			nil,
			"receivers",
			"receivers",
			"",
			"Define at least one receiver in the 'receivers' section.",
			rv.options.DefaultDocsURL+"#receiver",
		)
		return
	}

	// Track receiver names for uniqueness (already done in structural, but double-check)
	receiverNames := make(map[string]bool)

	for i, receiver := range cfg.Receivers {
		fieldPath := fmt.Sprintf("receivers[%d]", i)

		// Check for empty name
		if receiver.Name == "" {
			result.AddError(
				"E111",
				"Receiver name is required.",
				nil,
				fieldPath+".name",
				"receivers",
				"",
				"Provide a unique name for each receiver.",
				rv.options.DefaultDocsURL+"#receiver",
			)
			continue
		}

		// Check for duplicate names
		if receiverNames[receiver.Name] {
			result.AddError(
				"E112",
				fmt.Sprintf("Duplicate receiver name '%s'. Receiver names must be unique.", receiver.Name),
				nil,
				fieldPath+".name",
				"receivers",
				"",
				"Rename the receiver to a unique value.",
				rv.options.DefaultDocsURL+"#receiver",
			)
		}
		receiverNames[receiver.Name] = true

		// Validate that receiver has at least one integration
		if !receiver.HasAnyIntegration() {
			result.AddWarning(
				"W110",
				fmt.Sprintf("Receiver '%s' has no integrations defined. It will not send any alerts.", receiver.Name),
				nil,
				fieldPath,
				"receivers",
				"",
				"Add at least one integration (e.g., webhook_configs, slack_configs, email_configs) to this receiver.",
				rv.options.DefaultDocsURL+"#receiver",
			)
		}

		// Validate individual integrations
		rv.validateWebhookConfigs(ctx, receiver, i, result)
		rv.validateSlackConfigs(ctx, receiver, i, result)
		rv.validateEmailConfigs(ctx, receiver, i, result)
		rv.validatePagerDutyConfigs(ctx, receiver, i, result)
		rv.validateOpsGenieConfigs(ctx, receiver, i, result)
		rv.validateVictorOpsConfigs(ctx, receiver, i, result)
		rv.validatePushoverConfigs(ctx, receiver, i, result)
		rv.validateWeChatConfigs(ctx, receiver, i, result)
	}

	rv.logger.Debug("receiver validation finished")
}

// validateWebhookConfigs validates webhook integrations.
func (rv *ReceiverValidator) validateWebhookConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.WebhookConfigs == nil {
		return
	}

	for i, webhook := range receiver.WebhookConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].webhook_configs[%d]", receiverIdx, i)

		// URL is required
		if webhook.URL == "" {
			result.AddError(
				"E113",
				"Webhook URL is required.",
				nil,
				fieldPath+".url",
				"receivers",
				"",
				"Provide a valid webhook URL (e.g., 'http://example.com/webhook').",
				rv.options.DefaultDocsURL+"#webhook_config",
			)
			continue
		}

		// Validate URL format
		if err := rv.validateURL(webhook.URL, fieldPath+".url"); err != nil {
			result.AddError(
				"E114",
				fmt.Sprintf("Invalid webhook URL '%s': %v", webhook.URL, err),
				nil,
				fieldPath+".url",
				"receivers",
				"",
				"Ensure the URL is properly formatted with scheme (http/https) and valid hostname.",
				rv.options.DefaultDocsURL+"#webhook_config",
			)
		}

		// Security check: warn on HTTP (non-HTTPS) URLs
		if rv.options.EnableSecurityChecks && strings.HasPrefix(strings.ToLower(webhook.URL), "http://") {
			result.AddWarning(
				"W111",
				fmt.Sprintf("Webhook URL uses insecure HTTP protocol: %s", webhook.URL),
				nil,
				fieldPath+".url",
				"receivers",
				"",
				"Consider using HTTPS for secure communication. HTTP webhooks may expose sensitive alert data.",
				rv.options.DefaultDocsURL+"#webhook_config",
			)
		}

		// Check for localhost/internal IPs in production
		if rv.options.EnableBestPractices {
			rv.checkInternalURL(webhook.URL, fieldPath+".url", "webhook", result)
		}

		// Validate HTTP config if present
		if webhook.HTTPConfig != nil {
			rv.validateHTTPConfig(webhook.HTTPConfig, fieldPath+".http_config", "webhook", result)
		}
	}
}

// validateSlackConfigs validates Slack integrations.
func (rv *ReceiverValidator) validateSlackConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.SlackConfigs == nil {
		return
	}

	for i, slack := range receiver.SlackConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].slack_configs[%d]", receiverIdx, i)

		// API URL is required
		if slack.APIURL == "" {
			result.AddError(
				"E115",
				"Slack API URL is required.",
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Provide a Slack webhook URL (e.g., 'https://hooks.slack.com/services/...').",
				rv.options.DefaultDocsURL+"#slack_config",
			)
			continue
		}

		// Validate URL format
		if err := rv.validateURL(slack.APIURL, fieldPath+".api_url"); err != nil {
			result.AddError(
				"E116",
				fmt.Sprintf("Invalid Slack API URL '%s': %v", slack.APIURL, err),
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Ensure the Slack webhook URL is properly formatted.",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Check for valid Slack webhook URL pattern
		if !strings.Contains(slack.APIURL, "hooks.slack.com") && !strings.Contains(slack.APIURL, "slack.com/api") {
			result.AddWarning(
				"W112",
				fmt.Sprintf("Slack API URL does not appear to be a standard Slack webhook: %s", slack.APIURL),
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Standard Slack webhooks use 'hooks.slack.com/services/...' format. Verify this is the correct URL.",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Security check: warn on HTTP
		if rv.options.EnableSecurityChecks && strings.HasPrefix(strings.ToLower(slack.APIURL), "http://") {
			result.AddError(
				"E117",
				"Slack API URL must use HTTPS.",
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Slack webhooks must use HTTPS. Update the URL to use 'https://'.",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Validate channel format if specified
		if slack.Channel != "" && !rv.isValidSlackChannel(slack.Channel) {
			result.AddWarning(
				"W113",
				fmt.Sprintf("Invalid Slack channel format: '%s'", slack.Channel),
				nil,
				fieldPath+".channel",
				"receivers",
				"",
				"Slack channels should start with '#' (e.g., '#alerts') or '@' for direct messages (e.g., '@user').",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Validate color if specified
		if slack.Color != "" && !rv.isValidColor(slack.Color) {
			result.AddWarning(
				"W114",
				fmt.Sprintf("Invalid Slack color format: '%s'", slack.Color),
				nil,
				fieldPath+".color",
				"receivers",
				"",
				"Color should be 'good', 'warning', 'danger', or a hex color code (e.g., '#FF5733').",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Best practice: suggest using templates for dynamic content
		if rv.options.EnableBestPractices && slack.Title == "" && slack.Text == "" {
			result.AddInfo(
				"I110",
				fmt.Sprintf("Slack config in receiver '%s' has no custom title or text. Using defaults.", receiver.Name),
				nil,
				fieldPath,
				"receivers",
				"",
				"Consider customizing 'title' and 'text' fields for better alert context.",
				rv.options.DefaultDocsURL+"#slack_config",
			)
		}

		// Validate HTTP config if present
		if slack.HTTPConfig != nil {
			rv.validateHTTPConfig(slack.HTTPConfig, fieldPath+".http_config", "slack", result)
		}
	}
}

// validateEmailConfigs validates email integrations.
func (rv *ReceiverValidator) validateEmailConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.EmailConfigs == nil {
		return
	}

	for i, email := range receiver.EmailConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].email_configs[%d]", receiverIdx, i)

		// 'to' field is required
		if email.To == "" {
			result.AddError(
				"E118",
				"Email 'to' address is required.",
				nil,
				fieldPath+".to",
				"receivers",
				"",
				"Provide a recipient email address (e.g., 'alerts@example.com').",
				rv.options.DefaultDocsURL+"#email_config",
			)
			continue
		}

		// Validate email format
		if !rv.isValidEmail(email.To) {
			result.AddError(
				"E119",
				fmt.Sprintf("Invalid email address: '%s'", email.To),
				nil,
				fieldPath+".to",
				"receivers",
				"",
				"Provide a valid email address in the format 'user@domain.com'.",
				rv.options.DefaultDocsURL+"#email_config",
			)
		}

		// Validate 'from' if specified
		if email.From != "" && !rv.isValidEmail(email.From) {
			result.AddError(
				"E120",
				fmt.Sprintf("Invalid 'from' email address: '%s'", email.From),
				nil,
				fieldPath+".from",
				"receivers",
				"",
				"Provide a valid email address for the 'from' field.",
				rv.options.DefaultDocsURL+"#email_config",
			)
		}

		// Validate smarthost format (host:port)
		if email.Smarthost != "" && !rv.isValidSmarthost(email.Smarthost) {
			result.AddError(
				"E121",
				fmt.Sprintf("Invalid smarthost format: '%s'", email.Smarthost),
				nil,
				fieldPath+".smarthost",
				"receivers",
				"",
				"Smarthost should be in 'host:port' format (e.g., 'smtp.example.com:587').",
				rv.options.DefaultDocsURL+"#email_config",
			)
		}

		// Security check: warn on unencrypted SMTP
		if rv.options.EnableSecurityChecks && email.RequireTLS != nil && !*email.RequireTLS {
			result.AddWarning(
				"W115",
				"Email config has TLS disabled. Credentials may be sent unencrypted.",
				nil,
				fieldPath+".require_tls",
				"receivers",
				"",
				"Enable TLS ('require_tls: true') to secure email transmission.",
				rv.options.DefaultDocsURL+"#email_config",
			)
		}

		// Best practice: suggest setting 'from' address
		if rv.options.EnableBestPractices && email.From == "" {
			result.AddSuggestion(
				"S110",
				"Email config has no 'from' address. Consider setting it for better deliverability.",
				nil,
				fieldPath+".from",
				"receivers",
				"",
				"Set 'from' to a valid email address (e.g., 'alertmanager@yourdomain.com') to improve email deliverability.",
				rv.options.DefaultDocsURL+"#email_config",
			)
		}
	}
}

// validatePagerDutyConfigs validates PagerDuty integrations.
func (rv *ReceiverValidator) validatePagerDutyConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.PagerDutyConfigs == nil {
		return
	}

	for i, pd := range receiver.PagerDutyConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].pagerduty_configs[%d]", receiverIdx, i)

		// Either routing_key or service_key is required
		if pd.RoutingKey == "" && pd.ServiceKey == "" {
			result.AddError(
				"E122",
				"PagerDuty config requires either 'routing_key' or 'service_key' (deprecated).",
				nil,
				fieldPath,
				"receivers",
				"",
				"Provide a 'routing_key' (preferred) or 'service_key' to route alerts to PagerDuty.",
				rv.options.DefaultDocsURL+"#pagerduty_config",
			)
			continue
		}

		// Warn if using deprecated service_key
		if pd.ServiceKey != "" {
			result.AddWarning(
				"W116",
				"PagerDuty 'service_key' is deprecated. Use 'routing_key' instead.",
				nil,
				fieldPath+".service_key",
				"receivers",
				"",
				"Migrate to 'routing_key' for PagerDuty Events API v2. 'service_key' uses the deprecated v1 API.",
				rv.options.DefaultDocsURL+"#pagerduty_config",
			)
		}

		// Validate URL if custom URL is provided
		if pd.URL != "" {
			if err := rv.validateURL(pd.URL, fieldPath+".url"); err != nil {
				result.AddError(
					"E123",
					fmt.Sprintf("Invalid PagerDuty URL '%s': %v", pd.URL, err),
					nil,
					fieldPath+".url",
					"receivers",
					"",
					"Ensure the PagerDuty URL is properly formatted.",
					rv.options.DefaultDocsURL+"#pagerduty_config",
				)
			}

			// Security check: must use HTTPS for PagerDuty
			if rv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(pd.URL), "https://") {
				result.AddError(
					"E124",
					"PagerDuty URL must use HTTPS.",
					nil,
					fieldPath+".url",
					"receivers",
					"",
					"PagerDuty requires HTTPS. Update the URL to use 'https://'.",
					rv.options.DefaultDocsURL+"#pagerduty_config",
				)
			}
		}

		// Validate severity if specified
		if pd.Severity != "" && !rv.isValidPagerDutySeverity(pd.Severity) {
			result.AddError(
				"E125",
				fmt.Sprintf("Invalid PagerDuty severity: '%s'", pd.Severity),
				nil,
				fieldPath+".severity",
				"receivers",
				"",
				"Severity must be one of: 'critical', 'error', 'warning', 'info'.",
				rv.options.DefaultDocsURL+"#pagerduty_config",
			)
		}

		// Validate HTTP config if present
		if pd.HTTPConfig != nil {
			rv.validateHTTPConfig(pd.HTTPConfig, fieldPath+".http_config", "pagerduty", result)
		}
	}
}

// validateOpsGenieConfigs validates OpsGenie integrations.
func (rv *ReceiverValidator) validateOpsGenieConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.OpsGenieConfigs == nil {
		return
	}

	for i, og := range receiver.OpsGenieConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].opsgenie_configs[%d]", receiverIdx, i)

		// API key is required
		if og.APIKey == "" {
			result.AddError(
				"E126",
				"OpsGenie API key is required.",
				nil,
				fieldPath+".api_key",
				"receivers",
				"",
				"Provide an OpsGenie API key to authenticate with OpsGenie.",
				rv.options.DefaultDocsURL+"#opsgenie_config",
			)
			continue
		}

		// Validate API URL if custom URL is provided
		if og.APIURL != "" {
			if err := rv.validateURL(og.APIURL, fieldPath+".api_url"); err != nil {
				result.AddError(
					"E127",
					fmt.Sprintf("Invalid OpsGenie API URL '%s': %v", og.APIURL, err),
					nil,
					fieldPath+".api_url",
					"receivers",
					"",
					"Ensure the OpsGenie API URL is properly formatted.",
					rv.options.DefaultDocsURL+"#opsgenie_config",
				)
			}

			// Security check: must use HTTPS
			if rv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(og.APIURL), "https://") {
				result.AddError(
					"E128",
					"OpsGenie API URL must use HTTPS.",
					nil,
					fieldPath+".api_url",
					"receivers",
					"",
					"OpsGenie requires HTTPS. Update the URL to use 'https://'.",
					rv.options.DefaultDocsURL+"#opsgenie_config",
				)
			}
		}

		// Validate priority if specified
		if og.Priority != "" && !rv.isValidOpsgeniePriority(og.Priority) {
			result.AddError(
				"E129",
				fmt.Sprintf("Invalid OpsGenie priority: '%s'", og.Priority),
				nil,
				fieldPath+".priority",
				"receivers",
				"",
				"Priority must be one of: 'P1', 'P2', 'P3', 'P4', 'P5'.",
				rv.options.DefaultDocsURL+"#opsgenie_config",
			)
		}

		// Validate HTTP config if present
		if og.HTTPConfig != nil {
			rv.validateHTTPConfig(og.HTTPConfig, fieldPath+".http_config", "opsgenie", result)
		}
	}
}

// validateVictorOpsConfigs validates VictorOps integrations.
func (rv *ReceiverValidator) validateVictorOpsConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.VictorOpsConfigs == nil {
		return
	}

	for i, vo := range receiver.VictorOpsConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].victorops_configs[%d]", receiverIdx, i)

		// API key is required
		if vo.APIKey == "" {
			result.AddError(
				"E130",
				"VictorOps API key is required.",
				nil,
				fieldPath+".api_key",
				"receivers",
				"",
				"Provide a VictorOps API key to authenticate.",
				rv.options.DefaultDocsURL+"#victorops_config",
			)
		}

		// Routing key is required
		if vo.RoutingKey == "" {
			result.AddError(
				"E131",
				"VictorOps routing key is required.",
				nil,
				fieldPath+".routing_key",
				"receivers",
				"",
				"Provide a VictorOps routing key to route alerts to the correct team.",
				rv.options.DefaultDocsURL+"#victorops_config",
			)
		}

		// Validate API URL if custom URL is provided
		if vo.APIURL != "" {
			if err := rv.validateURL(vo.APIURL, fieldPath+".api_url"); err != nil {
				result.AddError(
					"E132",
					fmt.Sprintf("Invalid VictorOps API URL '%s': %v", vo.APIURL, err),
					nil,
					fieldPath+".api_url",
					"receivers",
					"",
					"Ensure the VictorOps API URL is properly formatted.",
					rv.options.DefaultDocsURL+"#victorops_config",
				)
			}

			// Security check: must use HTTPS
			if rv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(vo.APIURL), "https://") {
				result.AddError(
					"E133",
					"VictorOps API URL must use HTTPS.",
					nil,
					fieldPath+".api_url",
					"receivers",
					"",
					"VictorOps requires HTTPS. Update the URL to use 'https://'.",
					rv.options.DefaultDocsURL+"#victorops_config",
				)
			}
		}

		// Validate message type if specified
		if vo.MessageType != "" && !rv.isValidVictorOpsMessageType(vo.MessageType) {
			result.AddError(
				"E134",
				fmt.Sprintf("Invalid VictorOps message type: '%s'", vo.MessageType),
				nil,
				fieldPath+".message_type",
				"receivers",
				"",
				"Message type must be one of: 'CRITICAL', 'WARNING', 'INFO'.",
				rv.options.DefaultDocsURL+"#victorops_config",
			)
		}

		// Validate HTTP config if present
		if vo.HTTPConfig != nil {
			rv.validateHTTPConfig(vo.HTTPConfig, fieldPath+".http_config", "victorops", result)
		}
	}
}

// validatePushoverConfigs validates Pushover integrations.
func (rv *ReceiverValidator) validatePushoverConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.PushoverConfigs == nil {
		return
	}

	for i, po := range receiver.PushoverConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].pushover_configs[%d]", receiverIdx, i)

		// User key is required
		if po.UserKey == "" {
			result.AddError(
				"E135",
				"Pushover user key is required.",
				nil,
				fieldPath+".user_key",
				"receivers",
				"",
				"Provide a Pushover user key to identify the recipient.",
				rv.options.DefaultDocsURL+"#pushover_config",
			)
		}

		// Token is required
		if po.Token == "" {
			result.AddError(
				"E136",
				"Pushover token is required.",
				nil,
				fieldPath+".token",
				"receivers",
				"",
				"Provide a Pushover application token to authenticate.",
				rv.options.DefaultDocsURL+"#pushover_config",
			)
		}

		// Validate priority if specified
		if po.Priority != "" && !rv.isValidPushoverPriority(po.Priority) {
			result.AddError(
				"E137",
				fmt.Sprintf("Invalid Pushover priority: '%s'", po.Priority),
				nil,
				fieldPath+".priority",
				"receivers",
				"",
				"Priority must be a template or one of: '-2', '-1', '0', '1', '2'.",
				rv.options.DefaultDocsURL+"#pushover_config",
			)
		}

		// Validate HTTP config if present
		if po.HTTPConfig != nil {
			rv.validateHTTPConfig(po.HTTPConfig, fieldPath+".http_config", "pushover", result)
		}
	}
}

// validateWeChatConfigs validates WeChat integrations.
func (rv *ReceiverValidator) validateWeChatConfigs(ctx context.Context, receiver *config.Receiver, receiverIdx int, result *configvalidator.Result) {
	if receiver.WeChatConfigs == nil {
		return
	}

	for i, wc := range receiver.WeChatConfigs {
		fieldPath := fmt.Sprintf("receivers[%d].wechat_configs[%d]", receiverIdx, i)

		// API URL is required
		if wc.APIURL == "" {
			result.AddError(
				"E138",
				"WeChat API URL is required.",
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Provide a WeChat API URL (e.g., 'https://qyapi.weixin.qq.com/cgi-bin/').",
				rv.options.DefaultDocsURL+"#wechat_config",
			)
			continue
		}

		// Validate URL format
		if err := rv.validateURL(wc.APIURL, fieldPath+".api_url"); err != nil {
			result.AddError(
				"E139",
				fmt.Sprintf("Invalid WeChat API URL '%s': %v", wc.APIURL, err),
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"Ensure the WeChat API URL is properly formatted.",
				rv.options.DefaultDocsURL+"#wechat_config",
			)
		}

		// Security check: must use HTTPS
		if rv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(wc.APIURL), "https://") {
			result.AddError(
				"E140",
				"WeChat API URL must use HTTPS.",
				nil,
				fieldPath+".api_url",
				"receivers",
				"",
				"WeChat requires HTTPS. Update the URL to use 'https://'.",
				rv.options.DefaultDocsURL+"#wechat_config",
			)
		}

		// Corp ID is required
		if wc.CorpID == "" {
			result.AddError(
				"E141",
				"WeChat corp_id is required.",
				nil,
				fieldPath+".corp_id",
				"receivers",
				"",
				"Provide a WeChat Work corp_id to identify your organization.",
				rv.options.DefaultDocsURL+"#wechat_config",
			)
		}

		// Validate HTTP config if present
		if wc.HTTPConfig != nil {
			rv.validateHTTPConfig(wc.HTTPConfig, fieldPath+".http_config", "wechat", result)
		}
	}
}

// validateHTTPConfig validates common HTTP configuration.
func (rv *ReceiverValidator) validateHTTPConfig(httpConfig *config.HTTPConfig, fieldPath, integration string, result *configvalidator.Result) {
	if httpConfig == nil {
		return
	}

	// Warn if basic auth username is set but password is not
	if httpConfig.BasicAuth != nil {
		if httpConfig.BasicAuth.Username != "" && httpConfig.BasicAuth.Password == "" && httpConfig.BasicAuth.PasswordFile == "" {
			result.AddWarning(
				"W117",
				fmt.Sprintf("%s: Basic auth username is set but no password provided.", integration),
				nil,
				fieldPath+".basic_auth",
				"receivers",
				"",
				"Provide either 'password' or 'password_file' for basic authentication.",
				rv.options.DefaultDocsURL+"#http_config",
			)
		}
	}

	// Validate proxy URL if specified
	if httpConfig.ProxyURL != "" {
		if err := rv.validateURL(httpConfig.ProxyURL, fieldPath+".proxy_url"); err != nil {
			result.AddError(
				"E142",
				fmt.Sprintf("Invalid proxy URL '%s': %v", httpConfig.ProxyURL, err),
				nil,
				fieldPath+".proxy_url",
				"receivers",
				"",
				"Ensure the proxy URL is properly formatted with scheme and hostname.",
				rv.options.DefaultDocsURL+"#http_config",
			)
		}
	}

	// Security check: warn on insecure_skip_verify
	if rv.options.EnableSecurityChecks && httpConfig.TLSConfig != nil && httpConfig.TLSConfig.InsecureSkipVerify {
		result.AddWarning(
			"W118",
			fmt.Sprintf("%s: TLS certificate verification is disabled (insecure_skip_verify: true).", integration),
			nil,
			fieldPath+".tls_config.insecure_skip_verify",
			"receivers",
			"",
			"Disabling TLS verification exposes you to man-in-the-middle attacks. Use proper certificates instead.",
			rv.options.DefaultDocsURL+"#tls_config",
		)
	}
}

// Helper functions for validation

func (rv *ReceiverValidator) validateURL(rawURL, fieldPath string) error {
	if rawURL == "" {
		return fmt.Errorf("empty URL")
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("missing scheme (http/https)")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("missing host")
	}

	return nil
}

func (rv *ReceiverValidator) checkInternalURL(rawURL, fieldPath, integration string, result *configvalidator.Result) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return
	}

	host := strings.ToLower(parsedURL.Hostname())
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "172.16.") {
		result.AddSuggestion(
			"S111",
			fmt.Sprintf("%s URL points to internal/localhost address: %s", integration, rawURL),
			nil,
			fieldPath,
			"receivers",
			"",
			"Ensure this is intentional. Internal URLs may not be accessible from all Alertmanager instances.",
			rv.options.DefaultDocsURL,
		)
	}
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func (rv *ReceiverValidator) isValidEmail(email string) bool {
	return emailRegex.MatchString(email)
}

func (rv *ReceiverValidator) isValidSmarthost(smarthost string) bool {
	// Format: host:port
	parts := strings.Split(smarthost, ":")
	if len(parts) != 2 {
		return false
	}
	// Basic validation: host is non-empty, port is numeric
	return parts[0] != "" && regexp.MustCompile(`^\d+$`).MatchString(parts[1])
}

func (rv *ReceiverValidator) isValidSlackChannel(channel string) bool {
	// Slack channels start with '#' or '@'
	return strings.HasPrefix(channel, "#") || strings.HasPrefix(channel, "@")
}

func (rv *ReceiverValidator) isValidColor(color string) bool {
	// Valid colors: 'good', 'warning', 'danger', or hex color '#RRGGBB'
	if color == "good" || color == "warning" || color == "danger" {
		return true
	}
	// Hex color validation
	matched, _ := regexp.MatchString(`^#[0-9A-Fa-f]{6}$`, color)
	return matched
}

func (rv *ReceiverValidator) isValidPagerDutySeverity(severity string) bool {
	validSeverities := []string{"critical", "error", "warning", "info"}
	for _, valid := range validSeverities {
		if severity == valid {
			return true
		}
	}
	return false
}

func (rv *ReceiverValidator) isValidOpsgeniePriority(priority string) bool {
	validPriorities := []string{"P1", "P2", "P3", "P4", "P5"}
	for _, valid := range validPriorities {
		if priority == valid {
			return true
		}
	}
	return false
}

func (rv *ReceiverValidator) isValidVictorOpsMessageType(msgType string) bool {
	validTypes := []string{"CRITICAL", "WARNING", "INFO"}
	for _, valid := range validTypes {
		if msgType == valid {
			return true
		}
	}
	return false
}

func (rv *ReceiverValidator) isValidPushoverPriority(priority string) bool {
	// Pushover priorities: -2, -1, 0, 1, 2
	// Can also be a template, so we do a basic check
	validPriorities := []string{"-2", "-1", "0", "1", "2"}
	for _, valid := range validPriorities {
		if priority == valid {
			return true
		}
	}
	// If it contains template syntax, allow it
	if strings.Contains(priority, "{{") || strings.Contains(priority, "}}") {
		return true
	}
	return false
}
