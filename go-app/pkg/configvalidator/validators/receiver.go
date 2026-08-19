package validators

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// ReceiverValidator validates receiver configurations: name uniqueness,
// "at least one integration configured", and per-integration required
// fields / URL format / HTTPS enforcement (mirroring the shape of
// internal/infrastructure/publishing/webhook_validator.go's ValidateURL,
// applied here per integration type rather than one generic webhook).
type ReceiverValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewReceiverValidator creates a new ReceiverValidator.
func NewReceiverValidator(opts types.Options, logger *slog.Logger) *ReceiverValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReceiverValidator{options: opts, logger: logger}
}

// Validate performs receiver validation.
func (v *ReceiverValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil || len(cfg.Receivers) == 0 {
		// Empty receivers list is already reported as E021 by
		// StructuralValidator.
		return
	}

	var global *config.GlobalConfig
	if cfg.Global != nil {
		global = cfg.Global
	} else {
		global = &config.GlobalConfig{}
	}

	seen := make(map[string]int, len(cfg.Receivers))
	for i, r := range cfg.Receivers {
		if r == nil {
			continue
		}
		path := fmt.Sprintf("receivers[%d]", i)

		if r.Name == "" {
			result.AddError(newError(
				"E022", "receivers", path+".name",
				"receiver name is required",
				"Provide a unique, non-empty name for the receiver",
			))
		} else {
			seen[r.Name]++
			if seen[r.Name] > 1 {
				result.AddError(newError(
					"E023", "receivers", path+".name",
					fmt.Sprintf("duplicate receiver name '%s'", r.Name),
					"Rename the duplicate receiver to a unique value",
				))
			}
		}

		if !v.hasAnyIntegration(r) {
			// W024, not an error (FU-RECEIVERS-INTEGRATION slice-1 review
			// finding I1): a receiver with no integrations is upstream
			// Alertmanager's BLACKHOLE receiver — the classic
			// `- name: 'null'` paired with a route that drops unwanted alerts
			// there. Upstream accepts it, and internal/config blocks the load
			// on E-codes only, so this used to fail an untouched upstream
			// config outright. It stays a warning because it is also what an
			// accidentally-empty receiver looks like.
			result.AddWarning(newWarning(
				"W024", "receivers", path,
				fmt.Sprintf("receiver '%s' has no integrations configured; alerts routed here are dropped (blackhole receiver)", receiverLabel(r)),
				"Intentional for a blackhole receiver; otherwise add one of: webhook_configs, email_configs, slack_configs, pagerduty_configs, telegram_configs",
			))
		}

		v.validateEmailConfigs(r, path, result)
		v.validatePagerdutyConfigs(r, path, result)
		v.validateSlackConfigs(r, path, global, result)
		v.validateWebhookConfigs(r, path, result)
		v.validateOpsGenieConfigs(r, path, global, result)
		v.validateVictorOpsConfigs(r, path, global, result)
		v.validateWeChatConfigs(r, path, global, result)
	}
}

func receiverLabel(r *config.Receiver) string {
	if r.Name == "" {
		return "<unnamed>"
	}
	return r.Name
}

func (v *ReceiverValidator) hasAnyIntegration(r *config.Receiver) bool {
	return len(r.EmailConfigs) > 0 ||
		len(r.PagerdutyConfigs) > 0 ||
		len(r.SlackConfigs) > 0 ||
		len(r.WebhookConfigs) > 0 ||
		len(r.OpsGenieConfigs) > 0 ||
		len(r.WeChatConfigs) > 0 ||
		len(r.VictorOpsConfigs) > 0 ||
		len(r.TelegramConfigs) > 0
}

// validateEmailConfigs checks E118 (to required), E119 (invalid to
// address(es)), E120 (invalid from), E121 (invalid smarthost host:port).
func (v *ReceiverValidator) validateEmailConfigs(r *config.Receiver, path string, result *types.Result) {
	for i, c := range r.EmailConfigs {
		field := fmt.Sprintf("%s.email_configs[%d]", path, i)
		if c.To == "" {
			result.AddError(newError("E118", "receivers", field+".to",
				"email 'to' address is required",
				"Provide at least one recipient email address"))
		} else if !isValidEmailList(c.To) {
			result.AddError(newError("E119", "receivers", field+".to",
				fmt.Sprintf("invalid email address in 'to': %s", c.To),
				"Use format: user@domain.com (comma-separated for multiple recipients)"))
		}

		if c.From != "" && !isValidEmail(c.From) {
			result.AddError(newError("E120", "receivers", field+".from",
				fmt.Sprintf("invalid 'from' address: %s", c.From),
				"Provide a valid sender email address (user@domain.com)"))
		} else if c.From == "" && v.options.IncludeSuggestions {
			result.AddSuggestion(types.Suggestion{
				Type: "clarify", Code: "S110",
				Message:  "email config has no 'from' address set",
				Location: types.Location{Section: "receivers", Field: field + ".from"},
				DocsURL:  docsURL,
			})
		}

		if c.Smarthost != "" && !isValidHostPort(c.Smarthost) {
			result.AddError(newError("E121", "receivers", field+".smarthost",
				fmt.Sprintf("invalid smarthost format: %s", c.Smarthost),
				"Use format: 'host:port' (e.g. 'smtp.gmail.com:587')"))
		}
	}
}

// validatePagerdutyConfigs checks E122 (routing/service key required),
// W116 (deprecated service_key), E123 (invalid url), E124 (https required).
func (v *ReceiverValidator) validatePagerdutyConfigs(r *config.Receiver, path string, result *types.Result) {
	for i, c := range r.PagerdutyConfigs {
		field := fmt.Sprintf("%s.pagerduty_configs[%d]", path, i)
		if c.RoutingKey == "" && c.ServiceKey == "" {
			result.AddError(newError("E122", "receivers", field,
				"PagerDuty routing_key (or deprecated service_key) is required",
				"Provide 'routing_key' (Events API v2)"))
		}
		if c.ServiceKey != "" {
			result.AddWarning(newWarning("W116", "receivers", field+".service_key",
				"'service_key' is deprecated",
				"Use 'routing_key' for the PagerDuty Events API v2"))
		}
		if c.URL != "" {
			if err := validateURL(c.URL); err != nil {
				result.AddError(newError("E123", "receivers", field+".url",
					fmt.Sprintf("invalid PagerDuty URL: %s", c.URL),
					"Ensure the URL is properly formatted with scheme and hostname"))
			} else if !isHTTPS(c.URL) {
				result.AddError(newError("E124", "receivers", field+".url",
					"PagerDuty URL must use HTTPS",
					"Use an https:// URL"))
			}
		}
	}
}

// validateSlackConfigs checks E115 (api_url required, with global fallback),
// E116 (invalid url), E117 (https required).
func (v *ReceiverValidator) validateSlackConfigs(r *config.Receiver, path string, global *config.GlobalConfig, result *types.Result) {
	for i, c := range r.SlackConfigs {
		field := fmt.Sprintf("%s.slack_configs[%d]", path, i)
		effective := c.APIURL
		if effective == "" {
			effective = global.SlackAPIURL
		}
		if effective == "" {
			result.AddError(newError("E115", "receivers", field+".api_url",
				"Slack API URL is required (set here or in global.slack_api_url)",
				"Provide the Slack incoming-webhook URL"))
			continue
		}
		if err := validateURL(effective); err != nil {
			result.AddError(newError("E116", "receivers", field+".api_url",
				fmt.Sprintf("invalid Slack API URL: %s", effective),
				"Ensure the Slack webhook URL is properly formatted"))
			continue
		}
		if !isHTTPS(effective) {
			result.AddError(newError("E117", "receivers", field+".api_url",
				"Slack API URL must use HTTPS",
				"Use an https:// URL"))
		}
	}
}

// validateWebhookConfigs checks E113 (url required), E114 (invalid url).
// Unlike the named integrations, generic webhooks have no documented
// HTTPS-required error code (README: HTTPS is a "best practice", not a
// hard requirement, since operators may target internal http endpoints);
// see security.go for the corresponding W111 warning.
func (v *ReceiverValidator) validateWebhookConfigs(r *config.Receiver, path string, result *types.Result) {
	for i, c := range r.WebhookConfigs {
		field := fmt.Sprintf("%s.webhook_configs[%d]", path, i)
		if c.URL == "" {
			result.AddError(newError("E113", "receivers", field+".url",
				"webhook URL is required",
				"Provide a valid webhook URL (e.g. 'https://example.com/webhook')"))
			continue
		}
		if err := validateURL(c.URL); err != nil {
			result.AddError(newError("E114", "receivers", field+".url",
				fmt.Sprintf("invalid webhook URL: %s", c.URL),
				"Ensure the URL is properly formatted with scheme and hostname"))
		}
	}
}

// validateOpsGenieConfigs checks E126 (api_key required), E127 (invalid
// url), E128 (https required), E129 (invalid priority).
func (v *ReceiverValidator) validateOpsGenieConfigs(r *config.Receiver, path string, global *config.GlobalConfig, result *types.Result) {
	for i, c := range r.OpsGenieConfigs {
		field := fmt.Sprintf("%s.opsgenie_configs[%d]", path, i)
		if c.APIKey == "" {
			result.AddError(newError("E126", "receivers", field+".api_key",
				"OpsGenie API key is required",
				"Provide the OpsGenie API key"))
		}
		effective := c.APIURL
		if effective == "" {
			effective = global.OpsGenieAPIURL
		}
		if effective != "" {
			if err := validateURL(effective); err != nil {
				result.AddError(newError("E127", "receivers", field+".api_url",
					fmt.Sprintf("invalid OpsGenie API URL: %s", effective),
					"Ensure the OpsGenie API URL is properly formatted"))
			} else if !isHTTPS(effective) {
				result.AddError(newError("E128", "receivers", field+".api_url",
					"OpsGenie API URL must use HTTPS",
					"Use an https:// URL"))
			}
		}
		if c.Priority != "" && !oneOfFold(c.Priority, "P1", "P2", "P3", "P4", "P5") {
			result.AddError(newError("E129", "receivers", field+".priority",
				fmt.Sprintf("invalid OpsGenie priority: %s", c.Priority),
				"Priority must be one of: P1, P2, P3, P4, P5"))
		}
	}
}

// validateVictorOpsConfigs checks E130 (api_key required), E131 (routing_key
// required), E132 (invalid url), E133 (https required), E134 (invalid
// message_type).
func (v *ReceiverValidator) validateVictorOpsConfigs(r *config.Receiver, path string, global *config.GlobalConfig, result *types.Result) {
	for i, c := range r.VictorOpsConfigs {
		field := fmt.Sprintf("%s.victorops_configs[%d]", path, i)
		if c.APIKey == "" {
			result.AddError(newError("E130", "receivers", field+".api_key",
				"VictorOps API key is required",
				"Provide the VictorOps API key"))
		}
		if c.RoutingKey == "" {
			result.AddError(newError("E131", "receivers", field+".routing_key",
				"VictorOps routing key is required",
				"Provide the VictorOps routing key"))
		}
		effective := c.APIURL
		if effective == "" {
			effective = global.VictorOpsAPIURL
		}
		if effective != "" {
			if err := validateURL(effective); err != nil {
				result.AddError(newError("E132", "receivers", field+".api_url",
					fmt.Sprintf("invalid VictorOps URL: %s", effective),
					"Ensure the URL is properly formatted"))
			} else if !isHTTPS(effective) {
				result.AddError(newError("E133", "receivers", field+".api_url",
					"VictorOps URL must use HTTPS",
					"Use an https:// URL"))
			}
		}
		if c.MessageType != "" && !oneOfFold(c.MessageType, "CRITICAL", "WARNING", "INFO") {
			result.AddError(newError("E134", "receivers", field+".message_type",
				fmt.Sprintf("invalid VictorOps message_type: %s", c.MessageType),
				"message_type must be one of: CRITICAL, WARNING, INFO"))
		}
	}
}

// validateWeChatConfigs checks E138 (api_url required, with global
// fallback), E139 (invalid url), E140 (https required), E141 (corp_id
// required).
func (v *ReceiverValidator) validateWeChatConfigs(r *config.Receiver, path string, global *config.GlobalConfig, result *types.Result) {
	for i, c := range r.WeChatConfigs {
		field := fmt.Sprintf("%s.wechat_configs[%d]", path, i)
		effective := c.APIURL
		if effective == "" {
			effective = global.WeChatAPIURL
		}
		if effective == "" {
			result.AddError(newError("E138", "receivers", field+".api_url",
				"WeChat API URL is required (set here or in global.wechat_api_url)",
				"Provide the WeChat API URL"))
		} else if err := validateURL(effective); err != nil {
			result.AddError(newError("E139", "receivers", field+".api_url",
				fmt.Sprintf("invalid WeChat URL: %s", effective),
				"Ensure the URL is properly formatted"))
		} else if !isHTTPS(effective) {
			result.AddError(newError("E140", "receivers", field+".api_url",
				"WeChat API URL must use HTTPS",
				"Use an https:// URL"))
		}

		if c.CorpID == "" {
			result.AddError(newError("E141", "receivers", field+".corp_id",
				"WeChat corp_id is required",
				"Provide the WeChat corp_id"))
		}
	}
}
