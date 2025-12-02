// Package inhibition implements inhibition rules engine for Alert History Service.
//
// Inhibition rules allow you to mute a set of alerts given that another alert is firing.
// This is useful for silencing alerts about dependent services when the root cause is known.
//
// Example use case:
//   - NodeDown alert inhibits all InstanceDown alerts on the same node
//   - Critical severity alerts inhibit warning/info alerts for the same service
//
// Alertmanager compatibility: 100%
// Reference: https://prometheus.io/docs/alerting/latest/configuration/#inhibit_rule
package inhibition

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// InhibitionRule represents a rule for inhibiting (suppressing) target alerts
// when source alerts are firing.
//
// A rule consists of:
//  1. Source matchers: conditions for the inhibiting alert (the alert causing the inhibition)
//  2. Target matchers: conditions for the inhibited alert (the alert being suppressed)
//  3. Equal labels: labels that must have the same value in both source and target
//
// The rule triggers when:
//   - Source alert matches all source conditions (source_match AND source_match_re)
//   - Target alert matches all target conditions (target_match AND target_match_re)
//   - All equal labels have the same value in both alerts
//
// Example:
//
//	# Inhibit InstanceDown when NodeDown is firing on the same node
//	- source_match:
//	    alertname: "NodeDown"
//	    severity: "critical"
//	  target_match:
//	    alertname: "InstanceDown"
//	  equal:
//	    - node
//	    - cluster
//
// Alertmanager compatibility: 100%
type InhibitionRule struct {
	// SourceMatch defines exact label matches for the source alert (inhibitor).
	// The source alert must have all specified labels with exact values.
	//
	// Example:
	//   source_match:
	//     alertname: "NodeDown"
	//     severity: "critical"
	//
	// Optional: at least one of (SourceMatch, SourceMatchRE) must be present
	SourceMatch map[string]string `yaml:"source_match,omitempty" json:"source_match,omitempty"`

	// SourceMatchRE defines regex label matches for the source alert.
	// Label values are matched against regular expressions.
	//
	// Example:
	//   source_match_re:
	//     service: "^(api|web).*"
	//     environment: "prod.*"
	//
	// Regex syntax: Go RE2 (no backreferences)
	// Optional: at least one of (SourceMatch, SourceMatchRE) must be present
	SourceMatchRE map[string]string `yaml:"source_match_re,omitempty" json:"source_match_re,omitempty"`

	// TargetMatch defines exact label matches for the target alert (inhibited).
	// The target alert must have all specified labels with exact values.
	//
	// Example:
	//   target_match:
	//     alertname: "InstanceDown"
	//     severity: "warning"
	//
	// Optional: at least one of (TargetMatch, TargetMatchRE) must be present
	TargetMatch map[string]string `yaml:"target_match,omitempty" json:"target_match,omitempty"`

	// TargetMatchRE defines regex label matches for the target alert.
	// Label values are matched against regular expressions.
	//
	// Example:
	//   target_match_re:
	//     severity: "warning|info"
	//     alertname: ".*Down$"
	//
	// Regex syntax: Go RE2 (no backreferences)
	// Optional: at least one of (TargetMatch, TargetMatchRE) must be present
	TargetMatchRE map[string]string `yaml:"target_match_re,omitempty" json:"target_match_re,omitempty"`

	// Equal defines labels that must have the same value in both source and target alerts.
	// If any of these labels is missing in either alert, the rule does not match.
	//
	// Example:
	//   equal:
	//     - cluster    # Must have same cluster label
	//     - namespace  # Must have same namespace label
	//
	// Common use cases:
	//   - Same infrastructure (node, cluster, datacenter)
	//   - Same application (namespace, service, job)
	//
	// Optional: can be empty (no equality checks)
	Equal []string `yaml:"equal,omitempty" json:"equal,omitempty"`

	// Name is an optional name for the rule used for debugging and metrics.
	// If not specified, a default name is generated based on the rule index.
	//
	// Example:
	//   name: "node-down-inhibits-instance-down"
	//
	// Optional: defaults to "rule-<index>"
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// --- Internal fields (not serialized to YAML/JSON) ---

	// compiledSourceRE contains pre-compiled regex patterns for source_match_re.
	// Compiled during parsing for performance optimization.
	//
	// Key: label name
	// Value: compiled regexp.Regexp
	compiledSourceRE map[string]*regexp.Regexp `yaml:"-" json:"-"`

	// compiledTargetRE contains pre-compiled regex patterns for target_match_re.
	// Compiled during parsing for performance optimization.
	//
	// Key: label name
	// Value: compiled regexp.Regexp
	compiledTargetRE map[string]*regexp.Regexp `yaml:"-" json:"-"`

	// CreatedAt is the timestamp when the rule was created/loaded.
	// Set automatically during parsing.
	CreatedAt time.Time `yaml:"-" json:"-"`

	// Version is used for optimistic locking in concurrent environments.
	// Incremented on each modification.
	Version int `yaml:"-" json:"-"`
}

// Validate checks if the inhibition rule is valid.
//
// Validation rules:
//  1. At least one source condition (source_match or source_match_re) must be present
//  2. At least one target condition (target_match or target_match_re) must be present
//  3. All label names must be valid Prometheus label names
//  4. Regex patterns must be valid (this is checked during parsing, not here)
//
// Returns:
//   - nil if rule is valid
//   - ValidationError if validation fails
//
// Note: Regex compilation is handled by the parser, not by this method.
func (r *InhibitionRule) Validate() error {
	// Check source conditions
	if len(r.SourceMatch) == 0 && len(r.SourceMatchRE) == 0 {
		return &ValidationError{
			Field:   "source_match/source_match_re",
			Rule:    "required_one_of",
			Message: "at least one of source_match or source_match_re must be present",
		}
	}

	// Check target conditions
	if len(r.TargetMatch) == 0 && len(r.TargetMatchRE) == 0 {
		return &ValidationError{
			Field:   "target_match/target_match_re",
			Rule:    "required_one_of",
			Message: "at least one of target_match or target_match_re must be present",
		}
	}

	// Validate label names in source_match
	for labelName := range r.SourceMatch {
		if !isValidLabelName(labelName) {
			return &ValidationError{
				Field:   "source_match." + labelName,
				Rule:    "valid_label_name",
				Message: fmt.Sprintf("invalid label name: %s", labelName),
			}
		}
	}

	// Validate label names in source_match_re
	for labelName := range r.SourceMatchRE {
		if !isValidLabelName(labelName) {
			return &ValidationError{
				Field:   "source_match_re." + labelName,
				Rule:    "valid_label_name",
				Message: fmt.Sprintf("invalid label name: %s", labelName),
			}
		}
	}

	// Validate label names in target_match
	for labelName := range r.TargetMatch {
		if !isValidLabelName(labelName) {
			return &ValidationError{
				Field:   "target_match." + labelName,
				Rule:    "valid_label_name",
				Message: fmt.Sprintf("invalid label name: %s", labelName),
			}
		}
	}

	// Validate label names in target_match_re
	for labelName := range r.TargetMatchRE {
		if !isValidLabelName(labelName) {
			return &ValidationError{
				Field:   "target_match_re." + labelName,
				Rule:    "valid_label_name",
				Message: fmt.Sprintf("invalid label name: %s", labelName),
			}
		}
	}

	// Validate equal labels
	for _, labelName := range r.Equal {
		if !isValidLabelName(labelName) {
			return &ValidationError{
				Field:   "equal",
				Rule:    "valid_label_name",
				Message: fmt.Sprintf("invalid label name in equal: %s", labelName),
			}
		}
	}

	return nil
}

// GetCompiledSourceRE returns the pre-compiled regex for a source label.
// Returns nil if the pattern doesn't exist or wasn't compiled.
//
// Parameters:
//   - key: label name
//
// Returns:
//   - *regexp.Regexp: compiled regex pattern, or nil if not found
//
// Example:
//
//	re := rule.GetCompiledSourceRE("service")
//	if re != nil && re.MatchString(alert.Labels["service"]) {
//	    // Match found
//	}
func (r *InhibitionRule) GetCompiledSourceRE(key string) *regexp.Regexp {
	if r.compiledSourceRE == nil {
		return nil
	}
	return r.compiledSourceRE[key]
}

// GetCompiledTargetRE returns the pre-compiled regex for a target label.
// Returns nil if the pattern doesn't exist or wasn't compiled.
//
// Parameters:
//   - key: label name
//
// Returns:
//   - *regexp.Regexp: compiled regex pattern, or nil if not found
//
// Example:
//
//	re := rule.GetCompiledTargetRE("severity")
//	if re != nil && re.MatchString(alert.Labels["severity"]) {
//	    // Match found
//	}
func (r *InhibitionRule) GetCompiledTargetRE(key string) *regexp.Regexp {
	if r.compiledTargetRE == nil {
		return nil
	}
	return r.compiledTargetRE[key]
}

// String returns a human-readable representation of the rule.
// Useful for debugging and logging.
//
// Format: "InhibitionRule{name=..., source=..., target=..., equal=[...]}"
func (r *InhibitionRule) String() string {
	var sb strings.Builder
	sb.WriteString("InhibitionRule{")

	if r.Name != "" {
		sb.WriteString(fmt.Sprintf("name=%q, ", r.Name))
	}

	sb.WriteString(fmt.Sprintf("source=%d matchers, ", len(r.SourceMatch)+len(r.SourceMatchRE)))
	sb.WriteString(fmt.Sprintf("target=%d matchers, ", len(r.TargetMatch)+len(r.TargetMatchRE)))
	sb.WriteString(fmt.Sprintf("equal=%v", r.Equal))
	sb.WriteString("}")

	return sb.String()
}

// setCompiledSourceRE sets a compiled regex for a source label.
// Internal method used by the parser.
func (r *InhibitionRule) setCompiledSourceRE(key string, re *regexp.Regexp) {
	if r.compiledSourceRE == nil {
		r.compiledSourceRE = make(map[string]*regexp.Regexp)
	}
	r.compiledSourceRE[key] = re
}

// setCompiledTargetRE sets a compiled regex for a target label.
// Internal method used by the parser.
func (r *InhibitionRule) setCompiledTargetRE(key string, re *regexp.Regexp) {
	if r.compiledTargetRE == nil {
		r.compiledTargetRE = make(map[string]*regexp.Regexp)
	}
	r.compiledTargetRE[key] = re
}

// InhibitionConfig represents the complete inhibition rules configuration.
//
// This is the root configuration object that contains all inhibition rules.
// In Alertmanager config, this corresponds to the "inhibit_rules" section.
//
// Example:
//
//	inhibit_rules:
//	  - source_match:
//	      alertname: "NodeDown"
//	    target_match:
//	      alertname: "InstanceDown"
//	    equal:
//	      - node
//	  - source_match:
//	      severity: "critical"
//	    target_match_re:
//	      severity: "warning|info"
//	    equal:
//	      - cluster
//
// Alertmanager compatibility: 100%
type InhibitionConfig struct {
	// Rules is the list of inhibition rules.
	// At least one rule must be present for a valid configuration.
	//
	// Rules are evaluated in order, and the first matching rule wins.
	// If multiple rules match the same alert pair, only the first is applied.
	Rules []InhibitionRule `yaml:"inhibit_rules" json:"inhibit_rules" validate:"dive"`

	// --- Internal metadata fields (not serialized) ---

	// LoadedAt is the timestamp when the configuration was loaded.
	// Set automatically during parsing.
	LoadedAt time.Time `yaml:"-" json:"-"`

	// SourceFile is the path to the source file (if loaded from a file).
	// Empty if loaded from bytes or string.
	SourceFile string `yaml:"-" json:"-"`
}

// Validate checks if the configuration is valid.
//
// Validation rules:
//  1. At least one rule must be present
//  2. All rules must be individually valid (calls rule.Validate())
//
// Returns:
//   - nil if configuration is valid
//   - ConfigError with multiple ValidationErrors if validation fails
func (c *InhibitionConfig) Validate() error {
	if len(c.Rules) == 0 {
		return &ConfigError{
			Message: "no inhibition rules found",
			Errors:  nil,
		}
	}

	var errors []error

	// Validate each rule
	for i, rule := range c.Rules {
		if err := rule.Validate(); err != nil {
			errors = append(errors, fmt.Errorf("rule %d: %w", i, err))
		}
	}

	if len(errors) > 0 {
		return &ConfigError{
			Message: fmt.Sprintf("configuration validation failed: %d errors", len(errors)),
			Errors:  errors,
		}
	}

	return nil
}

// RuleCount returns the number of inhibition rules in the configuration.
func (c *InhibitionConfig) RuleCount() int {
	return len(c.Rules)
}

// GetRuleByName returns the first rule with the specified name.
// Returns nil if no rule with that name is found.
//
// Parameters:
//   - name: rule name to search for
//
// Returns:
//   - *InhibitionRule: pointer to the rule, or nil if not found
//
// Note: If multiple rules have the same name, only the first is returned.
func (c *InhibitionConfig) GetRuleByName(name string) *InhibitionRule {
	for i := range c.Rules {
		if c.Rules[i].Name == name {
			return &c.Rules[i]
		}
	}
	return nil
}

// GetRuleByIndex returns the rule at the specified index.
// Returns nil if index is out of bounds.
//
// Parameters:
//   - index: rule index (0-based)
//
// Returns:
//   - *InhibitionRule: pointer to the rule, or nil if index out of bounds
func (c *InhibitionConfig) GetRuleByIndex(index int) *InhibitionRule {
	if index < 0 || index >= len(c.Rules) {
		return nil
	}
	return &c.Rules[index]
}

// String returns a human-readable representation of the configuration.
// Useful for debugging and logging.
func (c *InhibitionConfig) String() string {
	return fmt.Sprintf("InhibitionConfig{rules=%d, loaded_at=%v, source_file=%q}",
		len(c.Rules), c.LoadedAt.Format(time.RFC3339), c.SourceFile)
}

// isValidLabelName checks if a label name is valid according to Prometheus conventions.
//
// Valid label names must match: ^[a-zA-Z_][a-zA-Z0-9_]*$
//
// Reference: https://prometheus.io/docs/concepts/data_model/#metric-names-and-labels
var labelNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func isValidLabelName(name string) bool {
	if name == "" {
		return false
	}
	return labelNameRegex.MatchString(name)
}
