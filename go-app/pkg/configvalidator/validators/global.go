package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// GlobalConfigValidator validates the optional `global:` section: resolve
// timeout, SMTP defaults, and the per-integration URL/HTTPS defaults that
// receivers can fall back to (Slack/PagerDuty/OpsGenie) plus the shared
// HTTP proxy URL. TLS/secret concerns for this same section are handled
// by SecurityValidator, not here (see security.go).
//
// Scope decision: WeChatAPIURL and VictorOpsAPIURL globals are validated
// for format when used as a receiver fallback (receiver.go), but this
// validator does not add new error codes for them beyond ERROR_CODES.md's
// documented E200-E209 global range, to avoid diverging from that
// contract; format errors on those two fields still surface via the
// receiver that ends up using them.
type GlobalConfigValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewGlobalConfigValidator creates a new GlobalConfigValidator instance.
func NewGlobalConfigValidator(opts types.Options, logger *slog.Logger) *GlobalConfigValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalConfigValidator{options: opts, logger: logger}
}

// Validate performs validation of the global configuration.
func (gv *GlobalConfigValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil {
		return
	}

	if cfg.Global == nil {
		if gv.options.IncludeInfo {
			result.AddInfo(types.Info{
				Type:     types.InfoTypeRecommendation,
				Code:     "I200",
				Message:  "no global configuration defined; built-in defaults will be used",
				Location: types.Location{Section: "global"},
				DocsURL:  docsURL,
			})
		}
		return
	}

	g := cfg.Global

	// E200: resolve_timeout must not be negative (zero means "use the
	// built-in default").
	if !isNonNegativeDuration(int64(g.ResolveTimeout)) {
		result.AddError(newError("E200", "global", "global.resolve_timeout",
			"resolve_timeout must not be negative",
			"Set a non-negative duration (e.g. '5m')"))
	}

	// E201: smtp_from, if set, must be a valid email address.
	if g.SMTPFrom != "" && !isValidEmail(g.SMTPFrom) {
		result.AddError(newError("E201", "global", "global.smtp_from",
			"invalid SMTP 'from' address",
			"Provide a valid email address (e.g. 'alertmanager@example.com')"))
	}

	// E202: smtp_smarthost, if set, must be "host:port".
	if g.SMTPSmarthost != "" && !isValidHostPort(g.SMTPSmarthost) {
		result.AddError(newError("E202", "global", "global.smtp_smarthost",
			"invalid SMTP smarthost format",
			"Use format: 'host:port' (e.g. 'smtp.gmail.com:587')"))
	}

	gv.validateURLField(g.SlackAPIURL, "global.slack_api_url", "E203", "E204", "Slack", result)
	gv.validateURLField(g.PagerdutyURL, "global.pagerduty_url", "E205", "E206", "PagerDuty", result)
	gv.validateURLField(g.OpsGenieAPIURL, "global.opsgenie_api_url", "E207", "E208", "OpsGenie", result)

	// E209: http_config.proxy_url, if set, must be a well-formed URL.
	if g.HTTPConfig != nil && g.HTTPConfig.ProxyURL != "" {
		if err := validateURL(g.HTTPConfig.ProxyURL); err != nil {
			result.AddError(newError("E209", "global", "global.http_config.proxy_url",
				"invalid HTTP proxy URL",
				"Ensure the proxy URL is properly formatted"))
		}
	}
}

// validateURLField validates a global default integration URL: format
// (formatCode) and HTTPS enforcement (httpsCode), only when non-empty
// (all of these globals are optional).
func (gv *GlobalConfigValidator) validateURLField(value, field, formatCode, httpsCode, name string, result *types.Result) {
	if value == "" {
		return
	}
	if err := validateURL(value); err != nil {
		result.AddError(newError(formatCode, "global", field,
			"invalid "+name+" URL",
			"Ensure the URL is properly formatted with scheme and hostname"))
		return
	}
	if !isHTTPS(value) {
		result.AddError(newError(httpsCode, "global", field,
			name+" URL must use HTTPS",
			"Use an https:// URL"))
	}
}
