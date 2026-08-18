package config

import (
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"

	amcfg "github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// ================================================================================
// pkg/configvalidator wiring (task 5.4: alertmanager-parity)
// ================================================================================
//
// AMP's own config file mixes AMP-only top-level sections (server,
// database, redis, llm, storage, ...) with an Alertmanager-shaped subset
// (route, receivers, global, templates, time_intervals,
// mute_time_intervals - task 1.3) plus AMP's own `inhibition:` wrapper
// (task 3.x) around what upstream Alertmanager keeps as a bare top-level
// `inhibit_rules:` key.
//
// pkg/configvalidator only understands the upstream Alertmanager shape
// (internal/alertmanager/config.AlertmanagerConfig) and, via its own
// parser, decodes YAML in *strict* mode (unknown top-level fields are a
// hard error) - feeding it the whole raw AMP config file directly would
// make every AMP-only section ("server", "database", ...) fail as an
// "unknown field". validateAlertmanagerSubset instead extracts just the
// known Alertmanager-shaped keys, bridges in AMP's own inline
// inhibition rules, and calls configvalidator.Validator.ValidateConfig
// directly (bypassing its strict YAML parser) against the resulting
// *amcfg.AlertmanagerConfig.
//
// Gate: only runs when the raw file has a top-level `route:` key - the
// same condition internal/config.loadRouteConfig already uses to decide
// whether infrastructure/routing.Parse() applies. Without a `route:`
// section, the config is in AMP's legacy single-receiver mode (a bare
// `receivers:` list of names, no Alertmanager-shaped receivers at all),
// and configvalidator's StructuralValidator would otherwise report a
// false-positive E100 ("root route is required")/E021 ("no receivers")
// for a config that was never meant to have either.
//
// Ordering: called from loadRouteConfig BEFORE infraroute.Parse() runs
// on the same bytes, so a config that fails both gets configvalidator's
// more detailed message first; routing.Parse() remains a backstop for
// anything this check does not (yet) cover.
var alertmanagerSubsetKeys = []string{
	"route", "receivers", "global", "templates", "time_intervals", "mute_time_intervals",
}

// validateAlertmanagerSubset builds the Alertmanager-shaped subset of the
// given raw config file bytes and runs pkg/configvalidator's checks
// (structural, route, receiver integration shapes, inhibition, global,
// security) against it. Returns a non-nil error (with validator error
// details) when the subset has E-code errors; W-code warnings are only
// logged via slog, never block.
//
// data must be the same bytes the caller is about to hand to
// infraroute.Parse() / has already used for viper (loadRouteConfig's
// contract); cfg.Inhibition.Rules must already be populated (viper's
// mapstructure pass happens before this in LoadConfig).
func validateAlertmanagerSubset(data []byte, cfg *Config) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		// Not this check's job to report plain YAML syntax errors -
		// viper/routing.Parse() already do that with their own messages.
		return nil
	}

	subset := make(map[string]any, len(alertmanagerSubsetKeys))
	for _, key := range alertmanagerSubsetKeys {
		if v, ok := raw[key]; ok {
			subset[key] = v
		}
	}

	var amConfig amcfg.AlertmanagerConfig
	if len(subset) > 0 {
		subsetBytes, err := yaml.Marshal(subset)
		if err != nil {
			return fmt.Errorf("failed to prepare alertmanager config subset for validation: %w", err)
		}
		// Deliberately non-strict: internal/alertmanager/config models
		// only the fields configvalidator's own checks care about and is
		// not guaranteed to have 1:1 field parity with
		// infrastructure/routing's richer YAML schema (e.g. per-integration
		// http_method/max_alerts knobs); unknown fields here are exactly
		// that gap, not a config error, and infraroute.Parse() below is
		// the authority on what the runtime schema actually accepts.
		if err := yaml.Unmarshal(subsetBytes, &amConfig); err != nil {
			return nil
		}
	}

	if len(cfg.Inhibition.Rules) > 0 {
		amConfig.InhibitRules = toAlertmanagerInhibitRules(cfg.Inhibition.Rules)
	}

	validator := configvalidator.New(types.Options{
		Mode:           types.StrictMode,
		EnableSecurity: true,
	})
	result, err := validator.ValidateConfig(&amConfig)
	if err != nil {
		return fmt.Errorf("configvalidator failed: %w", err)
	}

	logConfigValidatorWarnings(result)

	if len(result.Errors) > 0 {
		return fmt.Errorf("alertmanager config validation failed (%d error(s)):\n%s",
			len(result.Errors), formatConfigValidatorErrors(result.Errors))
	}

	return nil
}

// toAlertmanagerInhibitRules bridges AMP's inline inhibition rules
// (nested under `inhibition.inhibit_rules`, task 3.x) into the shape
// pkg/configvalidator's InhibitionValidator checks (a top-level
// `inhibit_rules:` list, matching upstream Alertmanager).
func toAlertmanagerInhibitRules(rules []InhibitionRuleConfig) []*amcfg.InhibitRule {
	out := make([]*amcfg.InhibitRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &amcfg.InhibitRule{
			SourceMatch:   r.SourceMatch,
			SourceMatchRE: r.SourceMatchRE,
			TargetMatch:   r.TargetMatch,
			TargetMatchRE: r.TargetMatchRE,
			Equal:         r.Equal,
		})
	}
	return out
}

// logConfigValidatorWarnings logs W-code findings at Warn level. Per task
// 5.4: warnings never block the load/reload, but must not be silently
// dropped either.
func logConfigValidatorWarnings(result *types.Result) {
	if result == nil {
		return
	}
	for _, w := range result.Warnings {
		slog.Warn("configvalidator warning",
			"code", w.Code,
			"section", w.Location.Section,
			"field", w.Location.Field,
			"message", w.Message,
		)
	}
}

// formatConfigValidatorErrors renders validator errors as a multi-line,
// human-readable list for inclusion in the LoadConfig/reload error - the
// detail the reload HTTP response is expected to surface (task 5.4).
func formatConfigValidatorErrors(errs []types.Error) string {
	var b strings.Builder
	for _, e := range errs {
		field := e.Location.Field
		if field == "" {
			field = e.Location.Section
		}
		fmt.Fprintf(&b, "  [%s] %s: %s", e.Code, field, e.Message)
		if e.Suggestion != "" {
			fmt.Fprintf(&b, " (%s)", e.Suggestion)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
