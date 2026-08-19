package validators

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/matcher"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// InhibitionValidator validates inhibit_rules: source/target matcher
// presence and syntax (legacy match/match_re maps and upstream
// Alertmanager's modern source_matchers/target_matchers list syntax),
// equal-label names, and the "at least one condition per side" rule
// mirrored from internal/infrastructure/inhibition/parser.go's
// validateSemantics.
//
// Wave 7 (FU-INHIBIT-MATCHERS) closed the runtime gap fix round 1 (Phase 5
// review) had deliberately left open: internal/infrastructure/inhibition.
// InhibitionRule now has SourceMatchers/TargetMatchers fields, compiles
// them (CompileMatchers), and evaluates them in matchRuleFast with the
// same AND semantics as the legacy maps - both forms may appear on the
// same rule. Consequently source_matchers/target_matchers alone now DO
// satisfy E150/E151 here, matching what the runtime loader actually
// wires. The list-syntax fields are still syntax-checked (E153/E154) via
// the shared pkg/configvalidator/matcher.ParseMatchers port; the W155
// "not wired at runtime" warning this validator used to emit is gone -
// there is nothing left to warn about.
type InhibitionValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewInhibitionValidator creates a new InhibitionValidator.
func NewInhibitionValidator(opts types.Options, logger *slog.Logger) *InhibitionValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &InhibitionValidator{options: opts, logger: logger}
}

// Validate performs inhibition rules validation.
func (v *InhibitionValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil {
		return
	}

	for i, rule := range cfg.InhibitRules {
		if rule == nil {
			continue
		}
		path := fmt.Sprintf("inhibit_rules[%d]", i)

		// A rule satisfies each side's "has a condition" requirement via
		// either the legacy map form OR the matchers-form list (wave 7):
		// both are fully wired into the runtime inhibition loader.
		hasSourceCondition := len(rule.SourceMatch) > 0 || len(rule.SourceMatchRE) > 0 || len(rule.SourceMatchers) > 0
		hasTargetCondition := len(rule.TargetMatch) > 0 || len(rule.TargetMatchRE) > 0 || len(rule.TargetMatchers) > 0

		if !hasSourceCondition {
			result.AddError(newError("E150", "inhibit_rules", path,
				"inhibit rule must specify source_match, source_match_re, or source_matchers",
				"Define 'source_match', 'source_match_re', or 'source_matchers'"))
		}
		if !hasTargetCondition {
			result.AddError(newError("E151", "inhibit_rules", path,
				"inhibit rule must specify target_match, target_match_re, or target_matchers",
				"Define 'target_match', 'target_match_re', or 'target_matchers'"))
		}

		v.validateSide(rule.SourceMatch, rule.SourceMatchRE, rule.SourceMatchers, path, "source", "E153", result)
		v.validateSide(rule.TargetMatch, rule.TargetMatchRE, rule.TargetMatchers, path, "target", "E154", result)

		for _, label := range rule.Equal {
			if !isValidLabelName(label) {
				result.AddError(newError("E152", "inhibit_rules", path+".equal",
					fmt.Sprintf("invalid label name '%s' in equal", label),
					"Label names must match [a-zA-Z_][a-zA-Z0-9_]*"))
			}
		}

		if hasSourceCondition && hasTargetCondition && len(rule.Equal) == 0 {
			result.AddWarning(newWarning("W154", "inhibit_rules", path+".equal",
				"inhibit rule has no 'equal' labels",
				"Add 'equal' labels to scope inhibition to matching alert instances"))
			if v.options.IncludeSuggestions {
				result.AddSuggestion(types.Suggestion{
					Type: "clarify", Code: "S150",
					Message:  "inhibition rule might be too broad without 'equal' labels",
					Location: types.Location{Section: "inhibit_rules", Field: path},
					DocsURL:  docsURL,
				})
			}
		}
	}
}

// validateSide validates one side (source or target) of an inhibit rule:
// label names in the equality map, regex validity in the *_re map, and
// syntax of the AMP matchers list, using code as the error code for any
// syntax problem found on this side.
func (v *InhibitionValidator) validateSide(
	match map[string]string,
	matchRE map[string]string,
	matchers []string,
	path, side, code string,
	result *types.Result,
) {
	for label := range match {
		if !isValidLabelName(label) {
			result.AddError(newError(code, "inhibit_rules", path+"."+side+"_match",
				fmt.Sprintf("invalid label name '%s' in %s_match", label, side),
				"Label names must match [a-zA-Z_][a-zA-Z0-9_]*"))
		}
	}

	for label, pattern := range matchRE {
		if !isValidLabelName(label) {
			result.AddError(newError(code, "inhibit_rules", path+"."+side+"_match_re",
				fmt.Sprintf("invalid label name '%s' in %s_match_re", label, side),
				"Label names must match [a-zA-Z_][a-zA-Z0-9_]*"))
			continue
		}
		if !isValidRegex(pattern) {
			result.AddError(newError(code, "inhibit_rules", path+"."+side+"_match_re",
				fmt.Sprintf("invalid regex '%s' for label '%s' in %s_match_re", pattern, label, side),
				"Check regex syntax and escaping"))
		}
	}

	if len(matchers) > 0 {
		_, errs := matcher.ParseMatchers(matchers)
		for _, err := range errs {
			result.AddError(newError(code, "inhibit_rules", path+"."+side+"_matchers",
				err.Error(),
				"Use format: label=value, label!=value, label=~regex, or label!~regex"))
		}
	}
}
