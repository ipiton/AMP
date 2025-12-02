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

// GlobalConfigValidator performs validation of global Alertmanager settings.
type GlobalConfigValidator struct {
	options configvalidator.Options
	logger  *slog.Logger
}

// NewGlobalConfigValidator creates a new GlobalConfigValidator instance.
func NewGlobalConfigValidator(opts configvalidator.Options, logger *slog.Logger) *GlobalConfigValidator {
	return &GlobalConfigValidator{
		options: opts,
		logger:  logger,
	}
}

// Validate performs comprehensive validation of global configuration.
func (gv *GlobalConfigValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	gv.logger.Debug("starting global config validation")

	if cfg.Global == nil {
		// Global config is optional, but we can suggest adding it for best practices
		if gv.options.EnableBestPractices {
			result.AddInfo(
				"I200",
				"No global configuration defined. Default values will be used.",
				nil,
				"global",
				"global",
				"",
				"Consider adding a 'global' section to customize default settings (resolve_timeout, SMTP, etc.).",
				gv.options.DefaultDocsURL+"#global",
			)
		}
		return
	}

	global := cfg.Global

	// Validate resolve_timeout
	gv.validateResolveTimeout(global, result)

	// Validate SMTP settings
	gv.validateSMTPConfig(global, result)

	// Validate Slack API URL
	gv.validateSlackAPIURL(global, result)

	// Validate PagerDuty URL
	gv.validatePagerDutyURL(global, result)

	// Validate OpsGenie settings
	gv.validateOpsGenieConfig(global, result)

	// Validate HTTP config
	gv.validateHTTPConfig(global, result)

	gv.logger.Debug("global config validation finished")
}

// validateResolveTimeout validates the global resolve_timeout setting.
func (gv *GlobalConfigValidator) validateResolveTimeout(global *config.GlobalConfig, result *configvalidator.Result) {
	if global.ResolveTimeout <= 0 {
		// Zero or negative resolve timeout
		result.AddError(
			"E200",
			fmt.Sprintf("Global resolve_timeout must be positive. Current value: %s", global.ResolveTimeout.String()),
			nil,
			"global.resolve_timeout",
			"global",
			"",
			"Set resolve_timeout to a positive duration (e.g., '5m', '10m'). Default is 5m if not specified.",
			gv.options.DefaultDocsURL+"#global",
		)
	} else if gv.options.EnableBestPractices {
		// Best practices: warn about very short or very long timeouts
		seconds := int64(global.ResolveTimeout) / int64(1e9)

		if seconds < 60 {
			result.AddWarning(
				"W200",
				fmt.Sprintf("Global resolve_timeout is very short: %s. Alerts may resolve too quickly.", global.ResolveTimeout.String()),
				nil,
				"global.resolve_timeout",
				"global",
				"",
				"Consider using at least 1m (60s) for resolve_timeout. Short timeouts can cause notification churn.",
				gv.options.DefaultDocsURL+"#global",
			)
		} else if seconds > 86400 { // 24 hours
			result.AddWarning(
				"W201",
				fmt.Sprintf("Global resolve_timeout is very long: %s. Alerts may stay active too long.", global.ResolveTimeout.String()),
				nil,
				"global.resolve_timeout",
				"global",
				"",
				"Consider using a shorter resolve_timeout (e.g., 5m-30m). Very long timeouts can delay alert resolution.",
				gv.options.DefaultDocsURL+"#global",
			)
		}
	}
}

// validateSMTPConfig validates global SMTP settings.
func (gv *GlobalConfigValidator) validateSMTPConfig(global *config.GlobalConfig, result *configvalidator.Result) {
	hasSMTPConfig := global.SMTPFrom != "" || global.SMTPSmartHost != ""

	if !hasSMTPConfig {
		// No SMTP config is fine if no email receivers
		return
	}

	// Validate SMTP From address
	if global.SMTPFrom != "" {
		if !isValidEmail(global.SMTPFrom) {
			result.AddError(
				"E201",
				fmt.Sprintf("Invalid global SMTP from address: '%s'", global.SMTPFrom),
				nil,
				"global.smtp_from",
				"global",
				"",
				"Provide a valid email address for smtp_from (e.g., 'alertmanager@example.com').",
				gv.options.DefaultDocsURL+"#global",
			)
		}
	}

	// Validate SMTP Smarthost
	if global.SMTPSmartHost != "" {
		if !gv.isValidSmarthost(global.SMTPSmartHost) {
			result.AddError(
				"E202",
				fmt.Sprintf("Invalid global SMTP smarthost format: '%s'", global.SMTPSmartHost),
				nil,
				"global.smtp_smarthost",
				"global",
				"",
				"Smarthost should be in 'host:port' format (e.g., 'smtp.gmail.com:587').",
				gv.options.DefaultDocsURL+"#global",
			)
		}
	}

	// Check for authentication credentials
	hasAuth := global.SMTPAuthUsername != "" || global.SMTPAuthPassword != "" || global.SMTPAuthSecret != ""
	if hasAuth && global.SMTPAuthUsername == "" {
		result.AddWarning(
			"W202",
			"SMTP authentication password/secret provided but username is missing.",
			nil,
			"global.smtp_auth_username",
			"global",
			"",
			"Provide smtp_auth_username for SMTP authentication.",
			gv.options.DefaultDocsURL+"#global",
		)
	}

	// Security check: TLS
	if gv.options.EnableSecurityChecks {
		if global.SMTPRequireTLS != nil && !*global.SMTPRequireTLS {
			result.AddWarning(
				"W203",
				"Global SMTP TLS is disabled. Credentials may be sent unencrypted.",
				nil,
				"global.smtp_require_tls",
				"global",
				"",
				"Enable TLS ('smtp_require_tls: true') to secure SMTP communication.",
				gv.options.DefaultDocsURL+"#global",
			)
		}
	}

	// Best practice: suggest setting smtp_from if smarthost is set but from is not
	if gv.options.EnableBestPractices && global.SMTPSmartHost != "" && global.SMTPFrom == "" {
		result.AddSuggestion(
			"S200",
			"Global smtp_smarthost is set but smtp_from is not. Consider setting a default sender address.",
			nil,
			"global.smtp_from",
			"global",
			"",
			"Set smtp_from to a valid email address to improve email deliverability.",
			gv.options.DefaultDocsURL+"#global",
		)
	}
}

// validateSlackAPIURL validates global Slack API URL.
func (gv *GlobalConfigValidator) validateSlackAPIURL(global *config.GlobalConfig, result *configvalidator.Result) {
	if global.SlackAPIURL == "" && global.SlackAPIURLFile == "" {
		return
	}

	if global.SlackAPIURL != "" {
		// Validate URL format
		if err := gv.validateURL(global.SlackAPIURL); err != nil {
			result.AddError(
				"E203",
				fmt.Sprintf("Invalid global Slack API URL: '%s' - %v", global.SlackAPIURL, err),
				nil,
				"global.slack_api_url",
				"global",
				"",
				"Ensure the Slack webhook URL is properly formatted.",
				gv.options.DefaultDocsURL+"#global",
			)
		} else {
			// Security check: must use HTTPS
			if gv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(global.SlackAPIURL), "https://") {
				result.AddError(
					"E204",
					"Global Slack API URL must use HTTPS.",
					nil,
					"global.slack_api_url",
					"global",
					"",
					"Slack webhooks must use HTTPS for security.",
					gv.options.DefaultDocsURL+"#global",
				)
			}

			// Check for standard Slack webhook URL
			if !strings.Contains(global.SlackAPIURL, "hooks.slack.com") && !strings.Contains(global.SlackAPIURL, "slack.com/api") {
				result.AddWarning(
					"W204",
					"Global Slack API URL does not appear to be a standard Slack webhook.",
					nil,
					"global.slack_api_url",
					"global",
					"",
					"Standard Slack webhooks use 'hooks.slack.com/services/...' format.",
					gv.options.DefaultDocsURL+"#global",
				)
			}
		}
	}

	// Check for both URL and file specified
	if global.SlackAPIURL != "" && global.SlackAPIURLFile != "" {
		result.AddWarning(
			"W205",
			"Both slack_api_url and slack_api_url_file are set. The URL file takes precedence.",
			nil,
			"global.slack_api_url",
			"global",
			"",
			"Choose either slack_api_url or slack_api_url_file, not both.",
			gv.options.DefaultDocsURL+"#global",
		)
	}
}

// validatePagerDutyURL validates global PagerDuty URL.
func (gv *GlobalConfigValidator) validatePagerDutyURL(global *config.GlobalConfig, result *configvalidator.Result) {
	if global.PagerdutyURL == "" {
		return
	}

	// Validate URL format
	if err := gv.validateURL(global.PagerdutyURL); err != nil {
		result.AddError(
			"E205",
			fmt.Sprintf("Invalid global PagerDuty URL: '%s' - %v", global.PagerdutyURL, err),
			nil,
			"global.pagerduty_url",
			"global",
			"",
			"Ensure the PagerDuty URL is properly formatted.",
			gv.options.DefaultDocsURL+"#global",
		)
		return
	}

	// Security check: must use HTTPS
	if gv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(global.PagerdutyURL), "https://") {
		result.AddError(
			"E206",
			"Global PagerDuty URL must use HTTPS.",
			nil,
			"global.pagerduty_url",
			"global",
			"",
			"PagerDuty API requires HTTPS for security.",
			gv.options.DefaultDocsURL+"#global",
		)
	}
}

// validateOpsGenieConfig validates global OpsGenie settings.
func (gv *GlobalConfigValidator) validateOpsGenieConfig(global *config.GlobalConfig, result *configvalidator.Result) {
	hasOpsGenieConfig := global.OpsGenieAPIURL != "" || global.OpsGenieAPIKey != "" || global.OpsGenieAPIKeyFile != ""

	if !hasOpsGenieConfig {
		return
	}

	// Validate OpsGenie API URL
	if global.OpsGenieAPIURL != "" {
		if err := gv.validateURL(global.OpsGenieAPIURL); err != nil {
			result.AddError(
				"E207",
				fmt.Sprintf("Invalid global OpsGenie API URL: '%s' - %v", global.OpsGenieAPIURL, err),
				nil,
				"global.opsgenie_api_url",
				"global",
				"",
				"Ensure the OpsGenie API URL is properly formatted.",
				gv.options.DefaultDocsURL+"#global",
			)
		} else {
			// Security check: must use HTTPS
			if gv.options.EnableSecurityChecks && !strings.HasPrefix(strings.ToLower(global.OpsGenieAPIURL), "https://") {
				result.AddError(
					"E208",
					"Global OpsGenie API URL must use HTTPS.",
					nil,
					"global.opsgenie_api_url",
					"global",
					"",
					"OpsGenie API requires HTTPS for security.",
					gv.options.DefaultDocsURL+"#global",
				)
			}
		}
	}

	// Check for both API key and file specified
	if global.OpsGenieAPIKey != "" && global.OpsGenieAPIKeyFile != "" {
		result.AddWarning(
			"W206",
			"Both opsgenie_api_key and opsgenie_api_key_file are set. The key file takes precedence.",
			nil,
			"global.opsgenie_api_key",
			"global",
			"",
			"Choose either opsgenie_api_key or opsgenie_api_key_file, not both.",
			gv.options.DefaultDocsURL+"#global",
		)
	}
}

// validateHTTPConfig validates global HTTP client configuration.
func (gv *GlobalConfigValidator) validateHTTPConfig(global *config.GlobalConfig, result *configvalidator.Result) {
	if global.HTTPConfig == nil {
		return
	}

	httpConfig := global.HTTPConfig

	// Validate proxy URL if specified
	if httpConfig.ProxyURL != "" {
		if err := gv.validateURL(httpConfig.ProxyURL); err != nil {
			result.AddError(
				"E209",
				fmt.Sprintf("Invalid global HTTP proxy URL: '%s' - %v", httpConfig.ProxyURL, err),
				nil,
				"global.http_config.proxy_url",
				"global",
				"",
				"Ensure the proxy URL is properly formatted with scheme and hostname.",
				gv.options.DefaultDocsURL+"#http_config",
			)
		}
	}

	// Security check: warn on insecure_skip_verify
	if gv.options.EnableSecurityChecks && httpConfig.TLSConfig != nil && httpConfig.TLSConfig.InsecureSkipVerify {
		result.AddWarning(
			"W207",
			"Global HTTP config has TLS certificate verification disabled (insecure_skip_verify: true).",
			nil,
			"global.http_config.tls_config.insecure_skip_verify",
			"global",
			"",
			"Disabling TLS verification exposes you to man-in-the-middle attacks. Use proper certificates instead.",
			gv.options.DefaultDocsURL+"#tls_config",
		)
	}

	// Check for bearer token and basic auth conflict
	if httpConfig.BearerToken != "" && httpConfig.BasicAuth != nil && httpConfig.BasicAuth.Username != "" {
		result.AddWarning(
			"W208",
			"Global HTTP config has both bearer token and basic auth configured. Bearer token takes precedence.",
			nil,
			"global.http_config",
			"global",
			"",
			"Use either bearer token or basic auth, not both.",
			gv.options.DefaultDocsURL+"#http_config",
		)
	}

	// Check for both bearer token and file specified
	if httpConfig.BearerToken != "" && httpConfig.BearerTokenFile != "" {
		result.AddWarning(
			"W209",
			"Global HTTP config has both bearer_token and bearer_token_file set. The token file takes precedence.",
			nil,
			"global.http_config.bearer_token",
			"global",
			"",
			"Choose either bearer_token or bearer_token_file, not both.",
			gv.options.DefaultDocsURL+"#http_config",
		)
	}

	// Validate basic auth if present
	if httpConfig.BasicAuth != nil {
		if httpConfig.BasicAuth.Username != "" && httpConfig.BasicAuth.Password == "" && httpConfig.BasicAuth.PasswordFile == "" {
			result.AddWarning(
				"W210",
				"Global HTTP basic auth username is set but no password provided.",
				nil,
				"global.http_config.basic_auth",
				"global",
				"",
				"Provide either 'password' or 'password_file' for basic authentication.",
				gv.options.DefaultDocsURL+"#http_config",
			)
		}

		// Check for both password and file specified
		if httpConfig.BasicAuth.Password != "" && httpConfig.BasicAuth.PasswordFile != "" {
			result.AddWarning(
				"W211",
				"Global HTTP basic auth has both password and password_file set. The password file takes precedence.",
				nil,
				"global.http_config.basic_auth.password",
				"global",
				"",
				"Choose either password or password_file, not both.",
				gv.options.DefaultDocsURL+"#http_config",
			)
		}
	}
}

// Helper functions

func (gv *GlobalConfigValidator) validateURL(rawURL string) error {
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

func (gv *GlobalConfigValidator) isValidSmarthost(smarthost string) bool {
	// Format: host:port
	parts := strings.Split(smarthost, ":")
	if len(parts) != 2 {
		return false
	}
	// Basic validation: host is non-empty, port is numeric
	return parts[0] != "" && regexp.MustCompile(`^\d+$`).MatchString(parts[1])
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func isValidEmail(email string) bool {
	return emailRegex.MatchString(email)
}
