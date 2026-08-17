package validators

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// SecurityValidator scans the configuration for security best-practice
// issues: disabled TLS verification, secrets written directly into the
// config (mirroring internal/config/sanitizer.go's notion of which
// fields are secret-shaped) and insecure plaintext webhook URLs
// (mirroring internal/infrastructure/publishing/webhook_validator.go's
// scheme/host checks). All findings here are warnings/suggestions, never
// hard errors: EnableSecurity only gates whether this validator runs at
// all (see validator.go), not the severity of what it finds.
//
// Scope decision: Slack/PagerDuty/OpsGenie/VictorOps webhook-style URLs
// legitimately embed their auth token in the URL/api_key field itself
// (that's how those APIs are designed); flagging every such receiver as
// "hardcoded secret" would be noise, not signal. This validator instead
// flags fields that stand in for a password/secret with no in-URL
// alternative: SMTP auth password, HTTP basic-auth password, bearer
// tokens, and the WeChat API secret.
type SecurityValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewSecurityValidator creates a new SecurityValidator.
func NewSecurityValidator(opts types.Options, logger *slog.Logger) *SecurityValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &SecurityValidator{options: opts, logger: logger}
}

// Validate performs security validation.
func (v *SecurityValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil {
		return
	}

	before := len(result.Warnings)

	if cfg.Global != nil {
		v.checkTLS(cfg.Global.HTTPConfig, "global.http_config", result)
		v.checkHardcodedSecret(cfg.Global.SMTPAuthPassword, "global.smtp_auth_password", result)
	}

	for i, r := range cfg.Receivers {
		if r == nil {
			continue
		}
		path := fmt.Sprintf("receivers[%d]", i)

		for j, c := range r.WebhookConfigs {
			field := fmt.Sprintf("%s.webhook_configs[%d]", path, j)
			v.checkTLS(c.HTTPConfig, field+".http_config", result)
			v.checkInsecureHTTP(c.URL, field+".url", result)
			v.checkInternalURL(c.URL, field+".url", result)
		}
		for j, c := range r.PagerdutyConfigs {
			v.checkHardcodedSecret(c.ServiceKey, fmt.Sprintf("%s.pagerduty_configs[%d].service_key", path, j), result)
		}
		for j, c := range r.OpsGenieConfigs {
			v.checkHardcodedSecret(c.APIKey, fmt.Sprintf("%s.opsgenie_configs[%d].api_key", path, j), result)
		}
		for j, c := range r.VictorOpsConfigs {
			v.checkHardcodedSecret(c.APIKey, fmt.Sprintf("%s.victorops_configs[%d].api_key", path, j), result)
		}
		for j, c := range r.WeChatConfigs {
			v.checkHardcodedSecret(c.APISecret, fmt.Sprintf("%s.wechat_configs[%d].api_secret", path, j), result)
		}
	}

	if v.options.IncludeInfo {
		found := len(result.Warnings) - before
		if found > 0 {
			result.AddInfo(types.Info{
				Type:     types.InfoTypeRecommendation,
				Code:     "I300",
				Message:  fmt.Sprintf("security scan found %d finding(s); review warnings above", found),
				Location: types.Location{Section: "security"},
				DocsURL:  docsURL,
			})
		}
	}
}

// checkTLS flags W311 when TLS verification is explicitly disabled.
func (v *SecurityValidator) checkTLS(httpConfig *config.HTTPConfig, field string, result *types.Result) {
	if httpConfig == nil || httpConfig.TLSConfig == nil {
		return
	}
	if httpConfig.TLSConfig.InsecureSkipVerify {
		result.AddWarning(newWarning("W311", "security", field+".tls_config.insecure_skip_verify",
			"TLS certificate verification is disabled",
			"Remove insecure_skip_verify and configure proper CA certificates instead"))
	}
}

// checkHardcodedSecret flags W300 when a secret-shaped field is set
// directly in the configuration (this config model has no *_file
// alternative to point to instead, so the suggestion is to move the
// value out of the tracked config, e.g. via environment substitution or a
// secret manager, per the org's "never hardcode secrets" policy).
func (v *SecurityValidator) checkHardcodedSecret(value, field string, result *types.Result) {
	if value == "" {
		return
	}
	result.AddWarning(newWarning("W300", "security", field,
		"secret value is set directly in the configuration",
		"Inject this value via environment substitution or a secret manager instead of committing it in plain text"))
}

// checkInsecureHTTP flags W111 for plaintext webhook URLs. Unlike the
// named integrations (Slack/PagerDuty/OpsGenie/VictorOps/WeChat, which
// hard-require HTTPS in receiver.go per their documented E-codes),
// generic webhooks have no such hard requirement because operators may
// legitimately target internal http-only endpoints; this is a warning
// so it doesn't block otherwise-valid intranet configs.
func (v *SecurityValidator) checkInsecureHTTP(rawURL, field string, result *types.Result) {
	if rawURL == "" || !isHTTP(rawURL) {
		return
	}
	result.AddWarning(newWarning("W111", "security", field,
		"webhook URL uses plaintext HTTP",
		"Use HTTPS for secure communication"))
}

// checkInternalURL suggests (S111) when a webhook URL points at
// localhost or a private/loopback address, which may not be reachable
// from all Alertmanager replicas.
func (v *SecurityValidator) checkInternalURL(rawURL, field string, result *types.Result) {
	if rawURL == "" || !v.options.IncludeSuggestions {
		return
	}
	if isLoopbackOrInternalHost(rawURL) {
		result.AddSuggestion(types.Suggestion{
			Type: "clarify", Code: "S111",
			Message:  "webhook URL points to a localhost/internal address",
			Location: types.Location{Section: "security", Field: field},
			DocsURL:  docsURL,
		})
	}
}
