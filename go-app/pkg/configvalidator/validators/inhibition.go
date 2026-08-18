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
// presence and syntax (legacy match/match_re maps and the AMP
// source_matchers/target_matchers list syntax), equal-label names, and the
// "at least one condition per side" rule mirrored from
// internal/infrastructure/inhibition/parser.go's validateSemantics.
//
// Runtime gap (fix round 1, Phase 5 review, ruling: do not add runtime
// support in this batch): the real runtime type,
// internal/infrastructure/inhibition.InhibitionRule, has no
// SourceMatchers/TargetMatchers fields. yaml.Unmarshal against that type
// silently drops matchers:-list content, InhibitionParser.Parse's
// validateSemantics then errors ("at least one of source_match or
// source_match_re required"), and internal/config/inhibition_adapter.go
// swallows that error - so a rule expressed only via
// source_matchers/target_matchers is silently missing at runtime even
// though it looks structurally fine. (The swallowed-error path itself is
// ledgered for task 5.4, not fixed here.)
//
// To avoid this validator saying "valid" for a config the runtime then
// drops, source_matchers/target_matchers alone do NOT satisfy E150/E151
// here - only the legacy source_match/source_match_re (target_match/
// target_match_re) maps do, matching what the runtime loader actually
// requires. The list-syntax fields are still syntax-checked (E153/E154)
// and, whenever present, get a W155 warning noting they aren't wired to
// the runtime loader yet.
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

		// Only the legacy maps count toward "has a condition": they're the
		// only forms internal/infrastructure/inhibition.InhibitionRule
		// actually loads (see type doc above).
		hasLegacySource := len(rule.SourceMatch) > 0 || len(rule.SourceMatchRE) > 0
		hasLegacyTarget := len(rule.TargetMatch) > 0 || len(rule.TargetMatchRE) > 0
		hasSourceMatchers := len(rule.SourceMatchers) > 0
		hasTargetMatchers := len(rule.TargetMatchers) > 0

		if !hasLegacySource {
			result.AddError(newError("E150", "inhibit_rules", path,
				"inhibit rule must specify source_match or source_match_re",
				"Define 'source_match' or 'source_match_re' (source_matchers is accepted for syntax checking but is not wired into the runtime inhibition loader yet)"))
		}
		if !hasLegacyTarget {
			result.AddError(newError("E151", "inhibit_rules", path,
				"inhibit rule must specify target_match or target_match_re",
				"Define 'target_match' or 'target_match_re' (target_matchers is accepted for syntax checking but is not wired into the runtime inhibition loader yet)"))
		}

		v.validateSide(rule.SourceMatch, rule.SourceMatchRE, rule.SourceMatchers, path, "source", "E153", result)
		v.validateSide(rule.TargetMatch, rule.TargetMatchRE, rule.TargetMatchers, path, "target", "E154", result)

		v.noteMatchersNotWired(hasSourceMatchers, path+".source_matchers", result)
		v.noteMatchersNotWired(hasTargetMatchers, path+".target_matchers", result)

		for _, label := range rule.Equal {
			if !isValidLabelName(label) {
				result.AddError(newError("E152", "inhibit_rules", path+".equal",
					fmt.Sprintf("invalid label name '%s' in equal", label),
					"Label names must match [a-zA-Z_][a-zA-Z0-9_]*"))
			}
		}

		if hasLegacySource && hasLegacyTarget && len(rule.Equal) == 0 {
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

// noteMatchersNotWired flags W155 whenever source_matchers/target_matchers
// is present at all - even alongside a legacy match/match_re map that
// already satisfies E150/E151 - because the list-syntax value is still
// silently ignored by the runtime loader (see type doc above), which is
// easy to miss if a legacy field happens to also be present.
func (v *InhibitionValidator) noteMatchersNotWired(present bool, field string, result *types.Result) {
	if !present {
		return
	}
	result.AddWarning(newWarning("W155", "inhibit_rules", field,
		"matchers list is accepted structurally but not wired into the runtime inhibition loader",
		"Rule must also define the equivalent source_match/source_match_re (or target_match/target_match_re) for this condition to take effect at runtime"))
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
