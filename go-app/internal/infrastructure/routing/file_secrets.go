package routing

import (
	"fmt"
	"os"
	"strings"
)

// ============================================================================
// *_file secret variants (FU7-B, alertmanager-parity wave 7 track B)
// ============================================================================
//
// PROBLEM: upstream Alertmanager lets every integration secret be supplied
// either inline or via a `*_file` twin that names a file on disk — the
// standard shape for a Kubernetes Secret mounted as a volume
// (`api_url_file`, `routing_key_file`, `service_key_file`, `bot_token_file`,
// `url_file`, `smtp_auth_password_file`, `slack_api_url_file`). Before this
// file, AMP
// parsed none of them: `routing.Receiver`/`GlobalConfig` had no such fields,
// so a config using them either failed structural validation (the inline
// field's own `required` tag firing on an empty value) or silently lost the
// credential (an optional field left empty, e.g. `slack_configs[].api_url`).
//
// FIX: every secret field AMP's five supported integrations (+ `global:`)
// already carry now has a `*_file` sibling field (see receiver.go /
// global.go). resolveFileSecrets, called from Parse() right after YAML
// unmarshal, enforces upstream's mutual-exclusion rule (inline XOR file, or
// neither where the field is optional) and — when the file variant is set —
// reads it and writes the resolved content into the SAME inline field
// everything downstream already reads: resolveGlobalFallbacks,
// applyDefaults, structural/semantic validation, and ultimately
// business/publishing.BuildConfigTargets. None of those need to know or care
// whether a value came from `api_url:` or `api_url_file:` — they see one
// already-resolved string, the same trick resolveGlobalFallbacks already
// uses for `global:` endpoint inheritance.
//
// The `*_file` field itself is left exactly as configured (the path) even
// after resolution — it is not secret (a filesystem path leaks nothing an
// attacker can use), so the status API's redaction pass
// (internal/application/handlers/status_config.go) is taught to leave it
// visible while the resolved inline field is redacted the same way it always
// was, regardless of source.
//
// ROTATION CAVEAT (honesty note, mirrored in
// docs/ALERTMANAGER_COMPATIBILITY.md): upstream re-reads some `*_file`
// secrets lazily, per notification, so rotating the file's content updates
// live delivery without a reload. AMP reads at config load and on every
// `/-/reload`/SIGHUP (loadRouteConfig re-reads the raw file and calls
// Parse() fresh each time — see internal/config/config.go), but NOT
// per-notification: a secret rotated on disk between reloads keeps using the
// old value until the next reload. Lazy per-publish re-reading was
// evaluated and intentionally not implemented — it would mean
// CreatePublisherForTarget (or every publisher) reading a file on the
// request path, changing the publisher contract this whole epic promises to
// leave untouched. Rotation therefore requires `/-/reload` or SIGHUP, same
// as every other config change.

// resolveFileSecrets resolves every `*_file` secret reference in config into
// the inline field the rest of the pipeline reads, enforcing upstream's
// mutual-exclusion semantics along the way.
//
// Must run BEFORE resolveGlobalFallbacks: a per-integration file-based value
// has to land in the inline field first, or the empty-string check that
// decides whether `global:` fills the gap would wrongly conclude the
// integration set nothing. Must also run before global's OWN file variant
// (SlackAPIURLFile) is consulted by that same fallback — handled here by
// resolving global's file secrets in this same pass, before the receivers'.
//
// Returns ValidationErrors (mutual-exclusion violations, unreadable files) so
// every problem in the config is reported together, matching the rest of the
// parser's error-aggregation style.
func resolveFileSecrets(config *RouteConfig) error {
	if config == nil {
		return nil
	}
	var errors ValidationErrors

	if config.Global != nil {
		global := config.Global

		resolved, err := resolveFileSecret(global.SlackAPIURL, global.SlackAPIURLFile)
		if err != nil {
			errors.Add("global.slack_api_url", err.Error(),
				"Set either global.slack_api_url or global.slack_api_url_file, not both")
		} else {
			global.SlackAPIURL = resolved
		}

		resolved, err = resolveFileSecret(global.SMTPAuthPassword, global.SMTPAuthPasswordFile)
		if err != nil {
			errors.Add("global.smtp_auth_password", err.Error(),
				"Set either global.smtp_auth_password or global.smtp_auth_password_file, not both")
		} else {
			global.SMTPAuthPassword = resolved
		}
	}

	for ri, receiver := range config.Receivers {
		if receiver == nil {
			continue
		}

		for wi, cfg := range receiver.WebhookConfigs {
			if cfg == nil {
				continue
			}
			resolved, err := resolveFileSecret(cfg.URL, cfg.URLFile)
			if err != nil {
				errors.Add(
					fmt.Sprintf("receivers[%d].webhook_configs[%d].url", ri, wi),
					err.Error(),
					"Set either url or url_file, not both",
				)
				continue
			}
			cfg.URL = resolved
		}

		for pi, cfg := range receiver.PagerDutyConfigs {
			if cfg == nil {
				continue
			}
			resolved, err := resolveFileSecret(cfg.RoutingKey, cfg.RoutingKeyFile)
			if err != nil {
				errors.Add(
					fmt.Sprintf("receivers[%d].pagerduty_configs[%d].routing_key", ri, pi),
					err.Error(),
					"Set either routing_key or routing_key_file, not both",
				)
			} else {
				cfg.RoutingKey = resolved
			}

			// ServiceKey is the legacy fallback (config_targets.go's
			// pagerDutyTarget reads it when RoutingKey is empty) — upstream
			// carries both routing_key/routing_key_file AND
			// service_key/service_key_file, so it gets the same *_file twin.
			resolvedService, err := resolveFileSecret(cfg.ServiceKey, cfg.ServiceKeyFile)
			if err != nil {
				errors.Add(
					fmt.Sprintf("receivers[%d].pagerduty_configs[%d].service_key", ri, pi),
					err.Error(),
					"Set either service_key or service_key_file, not both",
				)
				continue
			}
			cfg.ServiceKey = resolvedService
		}

		for si, cfg := range receiver.SlackConfigs {
			if cfg == nil {
				continue
			}
			resolved, err := resolveFileSecret(cfg.APIURL, cfg.APIURLFile)
			if err != nil {
				errors.Add(
					fmt.Sprintf("receivers[%d].slack_configs[%d].api_url", ri, si),
					err.Error(),
					"Set either api_url or api_url_file, not both",
				)
				continue
			}
			cfg.APIURL = resolved
		}

		for ti, cfg := range receiver.TelegramConfigs {
			if cfg == nil {
				continue
			}
			resolved, err := resolveFileSecret(cfg.BotToken, cfg.BotTokenFile)
			if err != nil {
				errors.Add(
					fmt.Sprintf("receivers[%d].telegram_configs[%d].bot_token", ri, ti),
					err.Error(),
					"Set either bot_token or bot_token_file, not both",
				)
				continue
			}
			cfg.BotToken = resolved
		}
	}

	return errors.ErrType()
}

// resolveFileSecret enforces upstream's mutual-exclusion semantics for one
// inline/`*_file` secret pair and returns the value the rest of the pipeline
// should use.
//
// Exactly one of inline/file may be set. Neither set is not an error here —
// that is either legal (an optional field, e.g. slack_configs[].api_url,
// which validateReceiverEndpoints / a `global:` fallback may still fill) or
// caught downstream by the inline field's own `required` struct tag (e.g.
// PagerDutyConfig.RoutingKey) — this function has no way to know which case
// it is for a given caller, so it defers that judgment entirely.
func resolveFileSecret(inline, file string) (string, error) {
	hasInline := inline != ""
	hasFile := file != ""

	switch {
	case hasInline && hasFile:
		return "", fmt.Errorf("exactly one of the inline value and its _file variant may be set, not both")
	case !hasFile:
		return inline, nil
	}

	content, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file %q: %w", file, err)
	}
	// Upstream trims trailing newline/whitespace (e.g. a file written by
	// `echo secret > file` carries a trailing "\n" that is never part of the
	// actual credential). Leading whitespace is left alone on purpose: it is
	// not upstream's documented behavior and a leading-whitespace credential,
	// while unusual, is not this function's business to silently alter.
	return strings.TrimRight(string(content), " \t\r\n"), nil
}
