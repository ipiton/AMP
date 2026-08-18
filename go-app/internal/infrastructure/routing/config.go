// Package routing provides Alertmanager-compatible route configuration parsing.
// It extends TN-121 GroupingConfig with full receiver support and advanced routing.
//
// This package implements:
//   - RouteConfig: Complete Alertmanager v0.27+ configuration
//   - Receiver: Multiple notification receiver types (webhook, PagerDuty, Slack)
//   - Parser: 4-layer validation (YAML → structural → semantic → cross-ref)
//   - Security: YAML bomb protection, SSRF prevention, secret sanitization
//
// Example usage:
//
//	parser := routing.NewRouteConfigParser()
//	config, err := parser.ParseFile("/etc/alertmanager/config.yml")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Access receiver by name (O(1) lookup)
//	receiver, ok := config.GetReceiver("pagerduty")
//	if !ok {
//	    log.Fatal("receiver not found")
//	}
//
//	// Use receiver for alert publishing
//	for _, pdConfig := range receiver.PagerDutyConfigs {
//	    // Publish to PagerDuty
//	}
//
// Integration:
//   - TN-121: Inherits grouping.Route for backward compatibility
//   - TN-138-141: Used by Route Tree Builder, Matcher, Evaluator
//   - TN-046-047: Integrates with K8s Secrets discovery
//   - TN-053-055: Integrates with publisher implementations
package routing

import (
	"fmt"
	"regexp"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
)

// RouteConfig represents the complete Alertmanager-compatible configuration.
// This extends TN-121 GroupingConfig with receivers support.
//
// Structure:
//
//	route:                     # Root route (required)
//	  receiver: default
//	  group_by: [alertname]
//	  routes: [...]            # Nested child routes
//	receivers:                 # Notification receivers (required, min=1)
//	  - name: default
//	    webhook_configs: [...]
//	global:                    # Global configuration (optional)
//	  resolve_timeout: 5m
//
// The RouteConfig provides:
//   - O(1) receiver lookup via ReceiverIndex
//   - Pre-compiled regex patterns for performance
//   - Version tracking for hot reload (FUTURE - TN-152)
//   - Configuration metadata (source, load time)
type RouteConfig struct {
	// Global configuration (optional)
	// Contains resolve_timeout, SMTP settings, HTTP client config
	Global *GlobalConfig `yaml:"global,omitempty"`

	// Route tree (required)
	// Root route with nested child routes
	// Inherited from TN-121 grouping.Route
	Route *grouping.Route `yaml:"route" validate:"required"`

	// Receivers configuration (required)
	// At least one receiver must be defined
	// Each receiver must have a unique name
	Receivers []*Receiver `yaml:"receivers" validate:"required,min=1,dive"`

	// Templates configuration (FUTURE - TN-153)
	// List of template files to load for message formatting
	Templates []string `yaml:"templates,omitempty"`

	// Inhibit rules (EXISTING - from TN-126)
	// Moved here for full Alertmanager compatibility
	InhibitRules []InhibitRule `yaml:"inhibit_rules,omitempty"`

	// TimeIntervals is the primary (Alertmanager >=0.24) section name for
	// named time interval groups, referenced by route.mute_time_intervals
	// and route.active_time_intervals (PARITY-B1, Task 3.1).
	TimeIntervals []timeinterval.TimeInterval `yaml:"time_intervals,omitempty"`

	// MuteTimeIntervalsSection is the deprecated top-level alias for
	// TimeIntervals (upstream's original section name before it was
	// renamed to `time_intervals:`). Entries from both sections are merged
	// into TimeIntervalIndex at parse time; using this alias logs a
	// deprecation warning.
	MuteTimeIntervalsSection []timeinterval.TimeInterval `yaml:"mute_time_intervals,omitempty"`

	// Internal: Receiver index for O(1) lookup (built at parse time)
	// Key: receiver.Name, Value: *Receiver
	// Not serialized to YAML
	ReceiverIndex map[string]*Receiver `yaml:"-"`

	// Internal: Time interval index for O(1) lookup by name (built at
	// parse time from TimeIntervals + MuteTimeIntervalsSection). Used by
	// the TimeMute pipeline step (Task 3.2) and by route reference
	// validation. Not serialized to YAML.
	TimeIntervalIndex map[string]timeinterval.TimeInterval `yaml:"-"`

	// Internal: Compiled regex patterns (built at parse time)
	// Key1: Route pointer, Key2: MatchRE label key, Value: Compiled regex
	// Used by RouteMatcher (TN-139) for fast pattern matching
	// Not serialized to YAML
	CompiledRegex map[*grouping.Route]map[string]*regexp.Regexp `yaml:"-"`

	// Internal: Configuration metadata
	Version    int       `yaml:"-"` // Incremented on each reload (for hot reload - TN-152)
	LoadedAt   time.Time `yaml:"-"` // When config was loaded
	SourceFile string    `yaml:"-"` // Path to source file (for debugging)
}

// GetReceiver returns a receiver by name.
// Uses ReceiverIndex for O(1) lookup performance.
//
// Returns (nil, false) if receiver not found.
//
// Complexity: O(1)
//
// Example:
//
//	receiver, ok := config.GetReceiver("pagerduty")
//	if !ok {
//	    return fmt.Errorf("receiver 'pagerduty' not found")
//	}
//	// Use receiver...
func (c *RouteConfig) GetReceiver(name string) (*Receiver, bool) {
	if c.ReceiverIndex == nil {
		return nil, false
	}
	receiver, ok := c.ReceiverIndex[name]
	return receiver, ok
}

// GetTimeInterval returns a named time interval group by name.
// Uses TimeIntervalIndex for O(1) lookup performance.
//
// Returns (zero value, false) if no group with that name was defined.
//
// Complexity: O(1)
//
// Example:
//
//	group, ok := config.GetTimeInterval("business-hours")
//	if ok && group.Matches(time.Now()) {
//	    // suppress/allow notification depending on route field
//	}
func (c *RouteConfig) GetTimeInterval(name string) (timeinterval.TimeInterval, bool) {
	if c.TimeIntervalIndex == nil {
		return timeinterval.TimeInterval{}, false
	}
	group, ok := c.TimeIntervalIndex[name]
	return group, ok
}

// ListReceivers returns all receivers in configuration order.
// This is useful for iterating over all receivers.
//
// Complexity: O(1) (returns slice reference)
//
// Example:
//
//	for _, receiver := range config.ListReceivers() {
//	    fmt.Printf("Receiver: %s (%d configs)\n", receiver.Name, receiver.GetConfigCount())
//	}
func (c *RouteConfig) ListReceivers() []*Receiver {
	return c.Receivers
}

// GetCompiledRegex returns the compiled regex for a route's MatchRE pattern.
// This is used by RouteMatcher (TN-139) to avoid recompiling regex on every match.
//
// Returns (nil, false) if pattern not found or not compiled.
//
// Complexity: O(1)
//
// Example:
//
//	regex, ok := config.GetCompiledRegex(route, "service")
//	if ok && regex.MatchString(alert.Labels["service"]) {
//	    // Route matches this alert
//	}
func (c *RouteConfig) GetCompiledRegex(route *grouping.Route, key string) (*regexp.Regexp, bool) {
	if c.CompiledRegex == nil {
		return nil, false
	}
	patterns, ok := c.CompiledRegex[route]
	if !ok {
		return nil, false
	}
	regex, ok := patterns[key]
	return regex, ok
}

// Validate performs comprehensive validation on the configuration.
// This is called automatically by Parse() but can be invoked separately.
//
// Validation layers:
//  1. Structural validation (validator tags)
//  2. Semantic validation (custom business rules)
//  3. Cross-reference validation (receiver references)
//  4. Security validation (SSRF, YAML bombs)
//
// Returns ValidationErrors if validation fails, nil otherwise.
//
// Example:
//
//	if err := config.Validate(); err != nil {
//	    if validationErrs, ok := err.(ValidationErrors); ok {
//	        for _, e := range validationErrs {
//	            fmt.Printf("Field: %s, Error: %s\n", e.Field, e.Message)
//	        }
//	    }
//	}
func (c *RouteConfig) Validate() error {
	parser := NewRouteConfigParser()
	return parser.ValidateConfig(c)
}

// Clone creates a deep copy of the configuration.
// This is useful for hot reload and atomic config swapping (TN-152).
//
// The cloned config includes:
//   - All routes (deep copy via Route.Clone())
//   - All receivers (deep copy via Receiver.Clone())
//   - Receiver index (rebuilt)
//   - Metadata (version, timestamps)
//
// CompiledRegex is NOT copied (will be rebuilt on next parse).
//
// Complexity: O(n) where n = total routes + receivers
//
// Example:
//
//	// Atomic config swap
//	newConfig := oldConfig.Clone()
//	newConfig.Version++
//	atomic.StorePointer(&activeConfig, unsafe.Pointer(newConfig))
func (c *RouteConfig) Clone() *RouteConfig {
	clone := &RouteConfig{
		Route:         c.Route.Clone(),
		Receivers:     make([]*Receiver, len(c.Receivers)),
		Templates:     append([]string{}, c.Templates...),
		ReceiverIndex: make(map[string]*Receiver, len(c.ReceiverIndex)),
		Version:       c.Version,
		LoadedAt:      c.LoadedAt,
		SourceFile:    c.SourceFile,
	}

	if c.Global != nil {
		clone.Global = c.Global.Clone()
	}

	// Deep copy receivers and rebuild index
	for i, receiver := range c.Receivers {
		clone.Receivers[i] = receiver.Clone()
		clone.ReceiverIndex[receiver.Name] = clone.Receivers[i]
	}

	// Deep copy time interval groups (both sections) and rebuild index
	if c.TimeIntervals != nil {
		clone.TimeIntervals = make([]timeinterval.TimeInterval, len(c.TimeIntervals))
		for i, group := range c.TimeIntervals {
			clone.TimeIntervals[i] = group.Clone()
		}
	}
	if c.MuteTimeIntervalsSection != nil {
		clone.MuteTimeIntervalsSection = make([]timeinterval.TimeInterval, len(c.MuteTimeIntervalsSection))
		for i, group := range c.MuteTimeIntervalsSection {
			clone.MuteTimeIntervalsSection[i] = group.Clone()
		}
	}
	if c.TimeIntervalIndex != nil {
		clone.TimeIntervalIndex = make(map[string]timeinterval.TimeInterval, len(c.TimeIntervalIndex))
		for name, group := range c.TimeIntervalIndex {
			clone.TimeIntervalIndex[name] = group.Clone()
		}
	}

	// Note: CompiledRegex not copied (rebuilt during parse)
	// This is intentional - regex compilation is fast enough

	return clone
}

// String returns a human-readable string representation of the configuration.
// Useful for logging and debugging.
//
// Example output:
//
//	RouteConfig{version=3, routes=5, receivers=3, loaded_at=2025-11-17T10:30:00Z}
func (c *RouteConfig) String() string {
	routeCount := 1 // root route
	if c.Route != nil {
		routeCount += countNestedRoutes(c.Route)
	}

	return fmt.Sprintf(
		"RouteConfig{version=%d, routes=%d, receivers=%d, loaded_at=%s, source=%s}",
		c.Version,
		routeCount,
		len(c.Receivers),
		c.LoadedAt.Format(time.RFC3339),
		c.SourceFile,
	)
}

// countNestedRoutes recursively counts the number of routes in the tree.
func countNestedRoutes(route *grouping.Route) int {
	if route == nil {
		return 0
	}

	count := len(route.Routes)
	for _, child := range route.Routes {
		count += countNestedRoutes(child)
	}

	return count
}

// InhibitRule represents an inhibition rule (from TN-126).
// This is a placeholder for full Alertmanager compatibility.
// Actual implementation is in TN-126 (Inhibition Rule Parser).
//
// TODO(TN-137): Import from TN-126 when integrating inhibition rules
type InhibitRule struct {
	// SourceMatch defines labels that must match for the source alert
	SourceMatch map[string]string `yaml:"source_match,omitempty"`

	// SourceMatchRE defines regex patterns for the source alert
	SourceMatchRE map[string]string `yaml:"source_match_re,omitempty"`

	// TargetMatch defines labels that must match for the target alert
	TargetMatch map[string]string `yaml:"target_match,omitempty"`

	// TargetMatchRE defines regex patterns for the target alert
	TargetMatchRE map[string]string `yaml:"target_match_re,omitempty"`

	// Equal specifies which label names must be equal between source and target
	Equal []string `yaml:"equal,omitempty"`
}
