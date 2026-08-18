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
func (c *InhibitionConfig) ToInhibitionRules() ([]inhibition.InhibitionRule, error) {
	rules := make([]inhibition.InhibitionRule, 0, len(c.Rules))

	for _, r := range c.Rules {
		rules = append(rules, inhibition.InhibitionRule{
			SourceMatch:   r.SourceMatch,
			SourceMatchRE: r.SourceMatchRE,
			TargetMatch:   r.TargetMatch,
			TargetMatchRE: r.TargetMatchRE,
			Equal:         r.Equal,
			Name:          r.Name,
		})
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
