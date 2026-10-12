package config

import (
	"fmt"

	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// ToInhibitionRules converts config rules to inhibition.InhibitionRule slice.
// Used during ServiceRegistry initialization and hot reload.
// If ConfigFile is set, rules from the file are parsed and merged with inline Rules.
//
// Task 5.4 (carried fix): a failing ConfigFile (missing file, invalid
// YAML, or semantic errors from inhibition.Parser.Validate) used to be
// swallowed here (`if err == nil && cfg != nil`), silently dropping ALL
// file-based inhibition rules while the inline Rules kept loading fine -
// an operator fixing a typo in the file would see no error and no
// indication the rules never took effect. The error is now propagated so
// callers can fail startup / reject the reload instead.
//
// Wave 7 (FU-INHIBIT-MATCHERS): SourceMatchers/TargetMatchers (upstream's
// modern `matchers:` list syntax) used to be captured here only to log a
// loud per-rule Error ("this rule will NOT inhibit anything through
// them") because the inhibition engine had no runtime support for them.
// The engine now does (internal/infrastructure/inhibition.InhibitionRule.
// CompileMatchers), so this carries the fields through and compiles them
// like a real condition — a config using only the matchers-form on a rule
// now inhibits exactly as written, instead of silently (or loudly)
// nothing.
//
// Review fix round 1 (S1): calls rule.Compile(), not rule.CompileMatchers()
// alone — Compile also runs CompileLegacyRegex, so an inline
// source_match_re/target_match_re rule gets its regexes compiled here too.
// Before this fix, this was the ONLY InhibitionRule construction path that
// never compiled legacy regexes at all (DefaultInhibitionParser, used for
// ConfigFile-sourced rules, always did), so matchRuleFast's
// `hasRE := rule.compiledSourceRE[key]; if !hasRE { return false }` made
// every inline legacy regex rule a permanent, silent no-op.
func (c *InhibitionConfig) ToInhibitionRules() ([]inhibition.InhibitionRule, error) {
	rules, err := compileInhibitionRules(c.Rules, "inline")
	if err != nil {
		return nil, err
	}

	if c.ConfigFile != "" {
		parser := inhibition.NewParser()
		cfg, err := parser.ParseFile(c.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to parse inhibition config_file %q: %w", c.ConfigFile, err)
		}
		if cfg != nil {
			rules = append(rules, cfg.Rules...)
		}
	}

	return rules, nil
}

// InhibitionRules returns every inhibition rule the config defines, in this
// order: upstream's top-level `inhibit_rules:` (c.InhibitRules), then AMP's
// `inhibition.inhibit_rules`, then `inhibition.config_file`. Both placements
// are merged, as inline rules and config_file already were; a rule present in
// both just inhibits the same alerts twice over.
//
// This is the only path from config to the inhibition engine: startup,
// hot reload and the fail-fast in LoadConfig all call it
// (FU-TOPLEVEL-INHIBIT-RULES — before, the top-level key was parsed and
// silently ignored). A rule that fails to compile fails the whole call, so a
// config never loads with part of its rules.
func (c *Config) InhibitionRules() ([]inhibition.InhibitionRule, error) {
	topLevel, err := compileInhibitionRules(c.InhibitRules, "top-level")
	if err != nil {
		return nil, err
	}

	wrapped, err := c.Inhibition.ToInhibitionRules()
	if err != nil {
		return nil, err
	}

	return append(topLevel, wrapped...), nil
}

// compileInhibitionRules converts config rules into runtime rules and compiles
// them. source ("inline", "top-level") names where the rules came from in the
// error, so an unnamed broken rule can be told apart between the two lists.
func compileInhibitionRules(cfgRules []InhibitionRuleConfig, source string) ([]inhibition.InhibitionRule, error) {
	rules := make([]inhibition.InhibitionRule, 0, len(cfgRules))

	for i, r := range cfgRules {
		rule := inhibition.InhibitionRule{
			SourceMatch:    r.SourceMatch,
			SourceMatchRE:  r.SourceMatchRE,
			TargetMatch:    r.TargetMatch,
			TargetMatchRE:  r.TargetMatchRE,
			SourceMatchers: r.SourceMatchers,
			TargetMatchers: r.TargetMatchers,
			Equal:          r.Equal,
			Name:           r.Name,
		}

		if err := rule.Compile(); err != nil {
			name := r.Name
			if name == "" {
				name = fmt.Sprintf("inhibit_rules[%d]", i)
			}
			return nil, fmt.Errorf("%s inhibition rule %q: %w", source, name, err)
		}

		rules = append(rules, rule)
	}

	return rules, nil
}
