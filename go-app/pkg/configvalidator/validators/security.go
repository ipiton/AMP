package validators

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator"
)

// SecurityValidator performs comprehensive security validation of Alertmanager configuration.
type SecurityValidator struct {
	options configvalidator.Options
	logger  *slog.Logger
}

// NewSecurityValidator creates a new SecurityValidator instance.
func NewSecurityValidator(opts configvalidator.Options, logger *slog.Logger) *SecurityValidator {
	return &SecurityValidator{
		options: opts,
		logger:  logger,
	}
}

// Validate performs comprehensive security checks on the entire configuration.
func (sv *SecurityValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	if !sv.options.EnableSecurityChecks {
		sv.logger.Debug("security validation disabled")
		return
	}

	sv.logger.Debug("starting security validation")

	// Check for exposed secrets in configuration
	sv.detectExposedSecrets(cfg, result)

	// Check for insecure protocols (HTTP instead of HTTPS)
	sv.detectInsecureProtocols(cfg, result)

	// Check for weak TLS configurations
	sv.detectWeakTLSConfig(cfg, result)

	// Check for overly permissive configurations
	sv.detectPermissiveConfigs(cfg, result)

	// Check for sensitive data exposure risks
	sv.detectSensitiveDataRisks(cfg, result)

	sv.logger.Debug("security validation finished")
}

// detectExposedSecrets checks for secrets that might be exposed in the configuration.
func (sv *SecurityValidator) detectExposedSecrets(cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	// Check for hardcoded API keys, tokens, passwords in receivers
	if cfg.Receivers != nil {
		for i, receiver := range cfg.Receivers {
			// Webhook configs
			for j, webhook := range receiver.WebhookConfigs {
				if webhook.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(webhook.HTTPConfig, fmt.Sprintf("receivers[%d].webhook_configs[%d].http_config", i, j), result)
				}
			}

			// Slack configs
			for j, slack := range receiver.SlackConfigs {
				if slack.APIURL != "" && sv.looksLikeHardcodedSecret(slack.APIURL) {
					result.AddWarning(
						"W300",
						fmt.Sprintf("Receiver '%s': Slack API URL appears to contain a hardcoded token.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].slack_configs[%d].api_url", i, j),
						"receivers",
						"",
						"Consider using 'api_url_file' to load the Slack webhook URL from a file instead of hardcoding it.",
						sv.options.DefaultDocsURL+"#slack_config",
					)
				}
				if slack.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(slack.HTTPConfig, fmt.Sprintf("receivers[%d].slack_configs[%d].http_config", i, j), result)
				}
			}

			// Email configs
			for j, email := range receiver.EmailConfigs {
				if email.AuthPassword != "" {
					result.AddWarning(
						"W301",
						fmt.Sprintf("Receiver '%s': Email password is hardcoded in configuration.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].email_configs[%d].auth_password", i, j),
						"receivers",
						"",
						"Use 'auth_secret' or environment variables instead of hardcoding passwords.",
						sv.options.DefaultDocsURL+"#email_config",
					)
				}
			}

			// PagerDuty configs
			for j, pd := range receiver.PagerDutyConfigs {
				if pd.RoutingKey != "" && sv.looksLikeAPIKey(pd.RoutingKey) {
					result.AddWarning(
						"W302",
						fmt.Sprintf("Receiver '%s': PagerDuty routing key appears to be hardcoded.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].pagerduty_configs[%d].routing_key", i, j),
						"receivers",
						"",
						"Consider using environment variables or secret management for API keys.",
						sv.options.DefaultDocsURL+"#pagerduty_config",
					)
				}
				if pd.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(pd.HTTPConfig, fmt.Sprintf("receivers[%d].pagerduty_configs[%d].http_config", i, j), result)
				}
			}

			// OpsGenie configs
			for j, og := range receiver.OpsGenieConfigs {
				if og.APIKey != "" {
					result.AddWarning(
						"W303",
						fmt.Sprintf("Receiver '%s': OpsGenie API key is hardcoded in configuration.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].opsgenie_configs[%d].api_key", i, j),
						"receivers",
						"",
						"Use 'api_key_file' to load the API key from a file instead of hardcoding it.",
						sv.options.DefaultDocsURL+"#opsgenie_config",
					)
				}
				if og.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(og.HTTPConfig, fmt.Sprintf("receivers[%d].opsgenie_configs[%d].http_config", i, j), result)
				}
			}

			// VictorOps configs
			for j, vo := range receiver.VictorOpsConfigs {
				if vo.APIKey != "" {
					result.AddWarning(
						"W304",
						fmt.Sprintf("Receiver '%s': VictorOps API key is hardcoded in configuration.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].victorops_configs[%d].api_key", i, j),
						"receivers",
						"",
						"Use 'api_key_file' to load the API key from a file instead of hardcoding it.",
						sv.options.DefaultDocsURL+"#victorops_config",
					)
				}
				if vo.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(vo.HTTPConfig, fmt.Sprintf("receivers[%d].victorops_configs[%d].http_config", i, j), result)
				}
			}

			// Pushover configs
			for j, po := range receiver.PushoverConfigs {
				if po.Token != "" {
					result.AddWarning(
						"W305",
						fmt.Sprintf("Receiver '%s': Pushover token is hardcoded in configuration.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].pushover_configs[%d].token", i, j),
						"receivers",
						"",
						"Use 'token_file' to load the token from a file instead of hardcoding it.",
						sv.options.DefaultDocsURL+"#pushover_config",
					)
				}
				if po.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(po.HTTPConfig, fmt.Sprintf("receivers[%d].pushover_configs[%d].http_config", i, j), result)
				}
			}

			// WeChat configs
			for j, wc := range receiver.WeChatConfigs {
				if wc.APISecret != "" {
					result.AddWarning(
						"W306",
						fmt.Sprintf("Receiver '%s': WeChat API secret is hardcoded in configuration.", receiver.Name),
						nil,
						fmt.Sprintf("receivers[%d].wechat_configs[%d].api_secret", i, j),
						"receivers",
						"",
						"Consider using environment variables or secret management for API secrets.",
						sv.options.DefaultDocsURL+"#wechat_config",
					)
				}
				if wc.HTTPConfig != nil {
					sv.checkHTTPConfigForSecrets(wc.HTTPConfig, fmt.Sprintf("receivers[%d].wechat_configs[%d].http_config", i, j), result)
				}
			}
		}
	}

	// Check global config for secrets
	if cfg.Global != nil {
		if cfg.Global.SMTPAuthPassword != "" {
			result.AddWarning(
				"W307",
				"Global SMTP password is hardcoded in configuration.",
				nil,
				"global.smtp_auth_password",
				"global",
				"",
				"Use 'smtp_auth_secret' or environment variables instead of hardcoding passwords.",
				sv.options.DefaultDocsURL+"#global",
			)
		}

		if cfg.Global.OpsGenieAPIKey != "" {
			result.AddWarning(
				"W308",
				"Global OpsGenie API key is hardcoded in configuration.",
				nil,
				"global.opsgenie_api_key",
				"global",
				"",
				"Use 'opsgenie_api_key_file' to load the API key from a file.",
				sv.options.DefaultDocsURL+"#global",
			)
		}

		if cfg.Global.HTTPConfig != nil {
			sv.checkHTTPConfigForSecrets(cfg.Global.HTTPConfig, "global.http_config", result)
		}
	}
}

// checkHTTPConfigForSecrets checks HTTP config for exposed secrets.
func (sv *SecurityValidator) checkHTTPConfigForSecrets(httpConfig *config.HTTPConfig, fieldPath string, result *configvalidator.Result) {
	if httpConfig.BearerToken != "" {
		result.AddWarning(
			"W309",
			"Bearer token is hardcoded in configuration.",
			nil,
			fieldPath+".bearer_token",
			"",
			"",
			"Use 'bearer_token_file' to load the token from a file instead of hardcoding it.",
			sv.options.DefaultDocsURL+"#http_config",
		)
	}

	if httpConfig.BasicAuth != nil && httpConfig.BasicAuth.Password != "" {
		result.AddWarning(
			"W310",
			"Basic auth password is hardcoded in configuration.",
			nil,
			fieldPath+".basic_auth.password",
			"",
			"",
			"Use 'password_file' to load the password from a file instead of hardcoding it.",
			sv.options.DefaultDocsURL+"#http_config",
		)
	}
}

// detectInsecureProtocols checks for HTTP URLs that should use HTTPS.
func (sv *SecurityValidator) detectInsecureProtocols(cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	// This is mostly covered by receiver validator, but we do a final sweep
	insecureCount := 0

	if cfg.Receivers != nil {
		for _, receiver := range cfg.Receivers {
			// Webhooks
			for _, webhook := range receiver.WebhookConfigs {
				if strings.HasPrefix(strings.ToLower(webhook.URL), "http://") {
					insecureCount++
				}
			}

			// Slack
			for _, slack := range receiver.SlackConfigs {
				if strings.HasPrefix(strings.ToLower(slack.APIURL), "http://") {
					insecureCount++
				}
			}
		}
	}

	if insecureCount > 0 {
		result.AddInfo(
			"I300",
			fmt.Sprintf("Security summary: Found %d insecure HTTP URL(s) in receiver configurations.", insecureCount),
			nil,
			"",
			"",
			"",
			"Review all HTTP URLs and migrate to HTTPS for secure communication.",
			sv.options.DefaultDocsURL,
		)
	}
}

// detectWeakTLSConfig checks for weak TLS configurations.
func (sv *SecurityValidator) detectWeakTLSConfig(cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	insecureSkipVerifyCount := 0

	// Check global HTTP config
	if cfg.Global != nil && cfg.Global.HTTPConfig != nil && cfg.Global.HTTPConfig.TLSConfig != nil {
		if cfg.Global.HTTPConfig.TLSConfig.InsecureSkipVerify {
			insecureSkipVerifyCount++
		}
	}

	// Check receiver HTTP configs
	if cfg.Receivers != nil {
		for _, receiver := range cfg.Receivers {
			// Webhook configs
			for _, webhook := range receiver.WebhookConfigs {
				if webhook.HTTPConfig != nil && webhook.HTTPConfig.TLSConfig != nil && webhook.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// Slack configs
			for _, slack := range receiver.SlackConfigs {
				if slack.HTTPConfig != nil && slack.HTTPConfig.TLSConfig != nil && slack.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// PagerDuty configs
			for _, pd := range receiver.PagerDutyConfigs {
				if pd.HTTPConfig != nil && pd.HTTPConfig.TLSConfig != nil && pd.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// OpsGenie configs
			for _, og := range receiver.OpsGenieConfigs {
				if og.HTTPConfig != nil && og.HTTPConfig.TLSConfig != nil && og.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// VictorOps configs
			for _, vo := range receiver.VictorOpsConfigs {
				if vo.HTTPConfig != nil && vo.HTTPConfig.TLSConfig != nil && vo.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// Pushover configs
			for _, po := range receiver.PushoverConfigs {
				if po.HTTPConfig != nil && po.HTTPConfig.TLSConfig != nil && po.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}

			// WeChat configs
			for _, wc := range receiver.WeChatConfigs {
				if wc.HTTPConfig != nil && wc.HTTPConfig.TLSConfig != nil && wc.HTTPConfig.TLSConfig.InsecureSkipVerify {
					insecureSkipVerifyCount++
				}
			}
		}
	}

	if insecureSkipVerifyCount > 0 {
		result.AddWarning(
			"W311",
			fmt.Sprintf("Security warning: Found %d configuration(s) with TLS verification disabled (insecure_skip_verify: true).", insecureSkipVerifyCount),
			nil,
			"",
			"",
			"",
			"Disabling TLS verification exposes you to man-in-the-middle attacks. Use proper CA certificates instead.",
			sv.options.DefaultDocsURL+"#tls_config",
		)
	}
}

// detectPermissiveConfigs checks for overly permissive configurations.
func (sv *SecurityValidator) detectPermissiveConfigs(cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	// Check for receivers with no specific integrations (catch-all)
	if cfg.Receivers != nil {
		catchAllReceivers := 0
		for _, receiver := range cfg.Receivers {
			if !receiver.HasAnyIntegration() {
				catchAllReceivers++
			}
		}

		if catchAllReceivers > 0 {
			result.AddInfo(
				"I301",
				fmt.Sprintf("Found %d receiver(s) with no integrations. These receivers will not send alerts.", catchAllReceivers),
				nil,
				"receivers",
				"receivers",
				"",
				"Review receivers without integrations and add appropriate notification methods.",
				sv.options.DefaultDocsURL+"#receiver",
			)
		}
	}

	// Check for routes with very broad matchers
	// (This is also checked in route validator, but we summarize here for security context)
	if cfg.Route != nil {
		broadRoutes := sv.countBroadRoutes(cfg.Route, 0)
		if broadRoutes > 2 {
			result.AddSuggestion(
				"S300",
				fmt.Sprintf("Found %d route(s) with potentially broad matchers. Review for unintended alert routing.", broadRoutes),
				nil,
				"route",
				"route",
				"",
				"Use specific matchers to ensure alerts are routed correctly and securely.",
				sv.options.DefaultDocsURL+"#route",
			)
		}
	}
}

// countBroadRoutes recursively counts routes with broad or no matchers.
func (sv *SecurityValidator) countBroadRoutes(route *config.Route, count int) int {
	if route == nil {
		return count
	}

	// Consider a route "broad" if it has 0-1 matchers and no match/match_re
	totalMatchers := len(route.Matchers) + len(route.Match) + len(route.MatchRE)
	if totalMatchers <= 1 {
		count++
	}

	// Recursively check child routes
	for _, childRoute := range route.Routes {
		count = sv.countBroadRoutes(&childRoute, count)
	}

	return count
}

// detectSensitiveDataRisks checks for configurations that might expose sensitive data.
func (sv *SecurityValidator) detectSensitiveDataRisks(cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	// Check if templates might expose sensitive labels/annotations
	if len(cfg.Templates) > 0 {
		result.AddInfo(
			"I302",
			fmt.Sprintf("Configuration uses %d template file(s). Ensure templates don't expose sensitive data.", len(cfg.Templates)),
			nil,
			"templates",
			"templates",
			"",
			"Review templates to ensure they don't accidentally expose sensitive labels, annotations, or values.",
			sv.options.DefaultDocsURL+"#template",
		)
	}

	// Check for webhook URLs pointing to internal services
	if cfg.Receivers != nil {
		internalWebhooks := 0
		for _, receiver := range cfg.Receivers {
			for _, webhook := range receiver.WebhookConfigs {
				if sv.isInternalURL(webhook.URL) {
					internalWebhooks++
				}
			}
		}

		if internalWebhooks > 0 {
			result.AddSuggestion(
				"S301",
				fmt.Sprintf("Found %d webhook(s) pointing to internal/localhost addresses.", internalWebhooks),
				nil,
				"receivers",
				"receivers",
				"",
				"Ensure internal webhooks are properly secured and accessible only from Alertmanager.",
				sv.options.DefaultDocsURL+"#webhook_config",
			)
		}
	}
}

// Helper functions

func (sv *SecurityValidator) looksLikeHardcodedSecret(value string) bool {
	// Check if value looks like a hardcoded API key or token
	// Slack webhook tokens typically have format: https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX
	if strings.Contains(value, "hooks.slack.com/services/") {
		parts := strings.Split(value, "/")
		if len(parts) >= 3 {
			// Check if last parts look like tokens (long alphanumeric strings)
			for i := len(parts) - 3; i < len(parts); i++ {
				if len(parts[i]) > 8 && sv.isAlphanumeric(parts[i]) {
					return true
				}
			}
		}
	}
	return false
}

func (sv *SecurityValidator) looksLikeAPIKey(value string) bool {
	// API keys are typically long alphanumeric strings (20+ chars)
	return len(value) >= 20 && sv.isAlphanumeric(value)
}

func (sv *SecurityValidator) isAlphanumeric(s string) bool {
	return regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(s)
}

func (sv *SecurityValidator) isInternalURL(rawURL string) bool {
	lowerURL := strings.ToLower(rawURL)
	return strings.Contains(lowerURL, "localhost") ||
		strings.Contains(lowerURL, "127.0.0.1") ||
		strings.Contains(lowerURL, "192.168.") ||
		strings.Contains(lowerURL, "10.") ||
		strings.Contains(lowerURL, "172.16.")
}
