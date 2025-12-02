package validators

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator"
	"github.com/ipiton/AMP/pkg/configvalidator/matcher"
)

// InhibitionValidator performs semantic validation of Alertmanager inhibition rules.
type InhibitionValidator struct {
	options configvalidator.Options
	logger  *slog.Logger
}

// NewInhibitionValidator creates a new InhibitionValidator instance.
func NewInhibitionValidator(opts configvalidator.Options, logger *slog.Logger) *InhibitionValidator {
	return &InhibitionValidator{
		options: opts,
		logger:  logger,
	}
}

// Validate performs comprehensive validation of all inhibition rules.
func (iv *InhibitionValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *configvalidator.Result) {
	iv.logger.Debug("starting inhibition validation")

	if cfg.InhibitRules == nil || len(cfg.InhibitRules) == 0 {
		// No inhibit rules is valid (they're optional)
		iv.logger.Debug("no inhibit rules to validate")
		return
	}

	for i, rule := range cfg.InhibitRules {
		fieldPath := fmt.Sprintf("inhibit_rules[%d]", i)
		iv.validateInhibitRule(ctx, &rule, i, fieldPath, result)
	}

	// Cross-rule validation
	iv.detectConflictingRules(ctx, cfg.InhibitRules, result)
	iv.detectOverlappingRules(ctx, cfg.InhibitRules, result)

	iv.logger.Debug("inhibition validation finished")
}

// validateInhibitRule validates a single inhibition rule.
func (iv *InhibitionValidator) validateInhibitRule(ctx context.Context, rule *config.InhibitRule, ruleIdx int, fieldPath string, result *configvalidator.Result) {
	// Check that rule has source matchers (new or old format)
	hasSourceMatchers := len(rule.SourceMatchers) > 0
	hasDeprecatedSourceMatch := len(rule.SourceMatch) > 0 || len(rule.SourceMatchRE) > 0

	if !hasSourceMatchers && !hasDeprecatedSourceMatch {
		result.AddError(
			"E150",
			"Inhibit rule must specify 'source_matchers' or deprecated 'source_match'/'source_match_re'.",
			nil,
			fieldPath,
			"inhibit_rules",
			"",
			"Define at least one source matcher to identify which alerts should inhibit others.",
			iv.options.DefaultDocsURL+"#inhibit_rule",
		)
	}

	// Check that rule has target matchers (new or old format)
	hasTargetMatchers := len(rule.TargetMatchers) > 0
	hasDeprecatedTargetMatch := len(rule.TargetMatch) > 0 || len(rule.TargetMatchRE) > 0

	if !hasTargetMatchers && !hasDeprecatedTargetMatch {
		result.AddError(
			"E151",
			"Inhibit rule must specify 'target_matchers' or deprecated 'target_match'/'target_match_re'.",
			nil,
			fieldPath,
			"inhibit_rules",
			"",
			"Define at least one target matcher to identify which alerts should be inhibited.",
			iv.options.DefaultDocsURL+"#inhibit_rule",
		)
	}

	// Validate source_matchers (new format)
	if hasSourceMatchers {
		iv.validateMatchers(rule.SourceMatchers, fieldPath+".source_matchers", "source", ruleIdx, result)
	}

	// Validate target_matchers (new format)
	if hasTargetMatchers {
		iv.validateMatchers(rule.TargetMatchers, fieldPath+".target_matchers", "target", ruleIdx, result)
	}

	// Warn about deprecated fields
	if hasDeprecatedSourceMatch {
		if len(rule.SourceMatch) > 0 {
			result.AddWarning(
				"W150",
				"Inhibit rule uses deprecated 'source_match' field. Use 'source_matchers' instead.",
				nil,
				fieldPath+".source_match",
				"inhibit_rules",
				"",
				"Migrate to 'source_matchers' for consistency with Alertmanager v0.22+. Example: source_matchers: [\"severity=critical\"]",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
		if len(rule.SourceMatchRE) > 0 {
			result.AddWarning(
				"W151",
				"Inhibit rule uses deprecated 'source_match_re' field. Use 'source_matchers' with regex operators instead.",
				nil,
				fieldPath+".source_match_re",
				"inhibit_rules",
				"",
				"Migrate to 'source_matchers' with =~ operator. Example: source_matchers: [\"severity=~critical|error\"]",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
	}

	if hasDeprecatedTargetMatch {
		if len(rule.TargetMatch) > 0 {
			result.AddWarning(
				"W152",
				"Inhibit rule uses deprecated 'target_match' field. Use 'target_matchers' instead.",
				nil,
				fieldPath+".target_match",
				"inhibit_rules",
				"",
				"Migrate to 'target_matchers' for consistency with Alertmanager v0.22+. Example: target_matchers: [\"severity=warning\"]",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
		if len(rule.TargetMatchRE) > 0 {
			result.AddWarning(
				"W153",
				"Inhibit rule uses deprecated 'target_match_re' field. Use 'target_matchers' with regex operators instead.",
				nil,
				fieldPath+".target_match_re",
				"inhibit_rules",
				"",
				"Migrate to 'target_matchers' with =~ operator. Example: target_matchers: [\"severity=~warning|info\"]",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
	}

	// Validate 'equal' labels
	if len(rule.Equal) == 0 {
		result.AddWarning(
			"W154",
			fmt.Sprintf("Inhibit rule #%d has no 'equal' labels defined. This rule might inhibit too broadly.", ruleIdx),
			nil,
			fieldPath+".equal",
			"inhibit_rules",
			"",
			"Consider adding 'equal' labels to make inhibition more specific. Common labels: ['alertname', 'instance', 'cluster']",
			iv.options.DefaultDocsURL+"#inhibit_rule",
		)
	} else {
		// Validate that equal labels are valid label names
		for j, label := range rule.Equal {
			if !isValidLabelName(label) {
				result.AddError(
					"E152",
					fmt.Sprintf("Invalid label name in 'equal' field: '%s'", label),
					nil,
					fmt.Sprintf("%s.equal[%d]", fieldPath, j),
					"inhibit_rules",
					"",
					"Label names must match [a-zA-Z_][a-zA-Z0-9_]*. Example: 'alertname', 'instance', 'cluster'",
					iv.options.DefaultDocsURL+"#inhibit_rule",
				)
			}
		}
	}

	// Best practices: check for overly broad rules
	if iv.options.EnableBestPractices {
		iv.checkBroadInhibitionRule(rule, ruleIdx, fieldPath, result)
	}
}

// validateMatchers validates a list of matchers for inhibition rules.
func (iv *InhibitionValidator) validateMatchers(matchers []string, fieldPath, matcherType string, ruleIdx int, result *configvalidator.Result) {
	if len(matchers) == 0 {
		return
	}

	for i, matcherStr := range matchers {
		m, err := matcher.Parse(matcherStr)
		if err != nil {
			result.AddError(
				"E153",
				fmt.Sprintf("Invalid %s matcher in inhibit rule #%d: '%s' - %v", matcherType, ruleIdx, matcherStr, err),
				nil,
				fmt.Sprintf("%s[%d]", fieldPath, i),
				"inhibit_rules",
				"",
				"Use format: label=value, label!=value, label=~regex, or label!~regex",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
			continue
		}

		// Validate the matcher
		if err := m.Validate(); err != nil {
			result.AddError(
				"E154",
				fmt.Sprintf("Invalid %s matcher in inhibit rule #%d: '%s' - %v", matcherType, ruleIdx, matcherStr, err),
				nil,
				fmt.Sprintf("%s[%d]", fieldPath, i),
				"inhibit_rules",
				"",
				"Check the label name and regex pattern for correctness.",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
	}
}

// checkBroadInhibitionRule checks if an inhibition rule is too broad.
func (iv *InhibitionValidator) checkBroadInhibitionRule(rule *config.InhibitRule, ruleIdx int, fieldPath string, result *configvalidator.Result) {
	// Check 1: Very few matchers with no 'equal' labels
	totalMatchers := len(rule.SourceMatchers) + len(rule.TargetMatchers) + len(rule.SourceMatch) + len(rule.SourceMatchRE) + len(rule.TargetMatch) + len(rule.TargetMatchRE)

	if totalMatchers <= 2 && len(rule.Equal) == 0 {
		result.AddSuggestion(
			"S150",
			fmt.Sprintf("Inhibit rule #%d has only %d matcher(s) and no 'equal' labels. This might inhibit too many alerts.", ruleIdx, totalMatchers),
			nil,
			fieldPath,
			"inhibit_rules",
			"",
			"Consider adding more specific matchers or 'equal' labels to narrow the scope of inhibition.",
			iv.options.DefaultDocsURL+"#inhibit_rule",
		)
	}

	// Check 2: Catch-all matchers (e.g., alertname=~.*)
	catchAllPattern := false
	for _, m := range rule.SourceMatchers {
		if strings.Contains(m, "=~.*") || strings.Contains(m, "=~.+") {
			catchAllPattern = true
			break
		}
	}
	for _, m := range rule.TargetMatchers {
		if strings.Contains(m, "=~.*") || strings.Contains(m, "=~.+") {
			catchAllPattern = true
			break
		}
	}

	if catchAllPattern {
		result.AddWarning(
			"W155",
			fmt.Sprintf("Inhibit rule #%d uses catch-all regex pattern (e.g., '=~.*'). This might inhibit too many alerts.", ruleIdx),
			nil,
			fieldPath,
			"inhibit_rules",
			"",
			"Avoid overly broad regex patterns in inhibition rules. Be specific about which alerts to inhibit.",
			iv.options.DefaultDocsURL+"#inhibit_rule",
		)
	}

	// Check 3: Same matchers for source and target
	if len(rule.SourceMatchers) > 0 && len(rule.TargetMatchers) > 0 {
		sourceSet := make(map[string]bool)
		for _, m := range rule.SourceMatchers {
			sourceSet[m] = true
		}

		sameCount := 0
		for _, m := range rule.TargetMatchers {
			if sourceSet[m] {
				sameCount++
			}
		}

		if sameCount == len(rule.SourceMatchers) && sameCount == len(rule.TargetMatchers) {
			result.AddWarning(
				"W156",
				fmt.Sprintf("Inhibit rule #%d has identical source and target matchers. This rule will never trigger.", ruleIdx),
				nil,
				fieldPath,
				"inhibit_rules",
				"",
				"Source and target matchers should be different. Source alerts inhibit target alerts when 'equal' labels match.",
				iv.options.DefaultDocsURL+"#inhibit_rule",
			)
		}
	}
}

// detectConflictingRules detects inhibition rules that might conflict with each other.
func (iv *InhibitionValidator) detectConflictingRules(ctx context.Context, rules []config.InhibitRule, result *configvalidator.Result) {
	if len(rules) < 2 || !iv.options.EnableBestPractices {
		return
	}

	// Check for duplicate rules
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			if iv.rulesAreDuplicates(&rules[i], &rules[j]) {
				result.AddWarning(
					"W157",
					fmt.Sprintf("Inhibit rules #%d and #%d appear to be duplicates.", i, j),
					nil,
					"inhibit_rules",
					"inhibit_rules",
					"",
					"Remove duplicate inhibition rules to simplify configuration.",
					iv.options.DefaultDocsURL+"#inhibit_rule",
				)
			}
		}
	}
}

// rulesAreDuplicates checks if two inhibition rules are duplicates.
func (iv *InhibitionValidator) rulesAreDuplicates(r1, r2 *config.InhibitRule) bool {
	// Compare source matchers
	if !stringSlicesEqual(r1.SourceMatchers, r2.SourceMatchers) {
		return false
	}

	// Compare target matchers
	if !stringSlicesEqual(r1.TargetMatchers, r2.TargetMatchers) {
		return false
	}

	// Compare equal labels
	if !stringSlicesEqual(r1.Equal, r2.Equal) {
		return false
	}

	// Compare deprecated fields
	if !stringMapsEqual(r1.SourceMatch, r2.SourceMatch) {
		return false
	}
	if !stringMapsEqual(r1.SourceMatchRE, r2.SourceMatchRE) {
		return false
	}
	if !stringMapsEqual(r1.TargetMatch, r2.TargetMatch) {
		return false
	}
	if !stringMapsEqual(r1.TargetMatchRE, r2.TargetMatchRE) {
		return false
	}

	return true
}

// detectOverlappingRules detects inhibition rules that might overlap in scope.
func (iv *InhibitionValidator) detectOverlappingRules(ctx context.Context, rules []config.InhibitRule, result *configvalidator.Result) {
	if len(rules) < 2 || !iv.options.EnableBestPractices {
		return
	}

	// This is a simplified check - in practice, determining rule overlap requires
	// evaluating matcher semantics, which is complex. We do a basic check for
	// rules with very similar matchers.

	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			similarity := iv.calculateRuleSimilarity(&rules[i], &rules[j])
			if similarity > 0.7 { // 70% similar
				result.AddInfo(
					"I150",
					fmt.Sprintf("Inhibit rules #%d and #%d have similar matchers (%.0f%% overlap). Verify they don't conflict.", i, j, similarity*100),
					nil,
					"inhibit_rules",
					"inhibit_rules",
					"",
					"Review these rules to ensure they work together as intended.",
					iv.options.DefaultDocsURL+"#inhibit_rule",
				)
			}
		}
	}
}

// calculateRuleSimilarity calculates a similarity score between two rules (0.0 to 1.0).
func (iv *InhibitionValidator) calculateRuleSimilarity(r1, r2 *config.InhibitRule) float64 {
	totalFields := 0
	matchingFields := 0

	// Compare source matchers
	totalFields++
	if len(r1.SourceMatchers) > 0 && len(r2.SourceMatchers) > 0 {
		overlap := calculateSliceOverlap(r1.SourceMatchers, r2.SourceMatchers)
		if overlap > 0.5 {
			matchingFields++
		}
	}

	// Compare target matchers
	totalFields++
	if len(r1.TargetMatchers) > 0 && len(r2.TargetMatchers) > 0 {
		overlap := calculateSliceOverlap(r1.TargetMatchers, r2.TargetMatchers)
		if overlap > 0.5 {
			matchingFields++
		}
	}

	// Compare equal labels
	totalFields++
	if len(r1.Equal) > 0 && len(r2.Equal) > 0 {
		overlap := calculateSliceOverlap(r1.Equal, r2.Equal)
		if overlap > 0.5 {
			matchingFields++
		}
	}

	if totalFields == 0 {
		return 0.0
	}

	return float64(matchingFields) / float64(totalFields)
}

// Helper functions

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	// Create maps for comparison (order doesn't matter)
	aMap := make(map[string]bool)
	for _, s := range a {
		aMap[s] = true
	}

	for _, s := range b {
		if !aMap[s] {
			return false
		}
	}

	return true
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

func calculateSliceOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	aMap := make(map[string]bool)
	for _, s := range a {
		aMap[s] = true
	}

	overlap := 0
	for _, s := range b {
		if aMap[s] {
			overlap++
		}
	}

	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return float64(overlap) / float64(maxLen)
}
