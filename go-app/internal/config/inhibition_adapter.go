package config

import (
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// ToInhibitionRules converts config rules to inhibition.InhibitionRule slice.
// Used during ServiceRegistry initialization.
// If ConfigFile is set, rules from the file are parsed and merged with inline Rules.
func (c *InhibitionConfig) ToInhibitionRules() []inhibition.InhibitionRule {
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
		if err == nil && cfg != nil {
			rules = append(rules, cfg.Rules...)
		}
	}

	return rules
}
