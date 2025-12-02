// Package grouping provides configuration parsing and management for alert grouping.
// It implements Alertmanager-compatible grouping configuration with YAML support.
//
// Example usage:
//
//	parser := grouping.NewParser()
//	config, err := parser.ParseFile("alertmanager.yml")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Group by: %v\n", config.Route.GroupBy)
package grouping

import (
	"fmt"
	"time"
)

// GroupingConfig represents the complete alert grouping configuration.
// It is compatible with Alertmanager's route configuration format.
type GroupingConfig struct {
	// Route is the root route configuration with grouping parameters
	Route *Route `yaml:"route" validate:"required"`
}

// Route defines a routing path with grouping parameters.
// Routes can be nested to create hierarchical routing trees.
//
// Example:
//
//	route:
//	  receiver: 'default'
//	  group_by: ['alertname', 'cluster']
//	  group_wait: 30s
//	  group_interval: 5m
//	  repeat_interval: 4h
type Route struct {
	// Receiver is the name of the notification receiver
	Receiver string `yaml:"receiver" validate:"required"`

	// GroupBy is a list of label names to group alerts by.
	// Special value ['...'] means group by all labels (effectively disables grouping).
	// Empty list [] creates a single global group.
	GroupBy []string `yaml:"group_by"`

	// GroupWait is the time to wait before sending the first notification for a new group.
	// This allows similar alerts to accumulate into the same group.
	// Default: 30s, Range: 0s-1h
	GroupWait *Duration `yaml:"group_wait,omitempty"`

	// GroupInterval is the time to wait before sending updated notifications for a group.
	// This controls how frequently updates are sent for active alert groups.
	// Default: 5m, Range: 1s-24h
	GroupInterval *Duration `yaml:"group_interval,omitempty"`

	// RepeatInterval is the time to wait before re-sending notifications for long-running alerts.
	// This prevents notification fatigue for alerts that remain active for extended periods.
	// Default: 4h, Range: 1m-168h (7 days)
	RepeatInterval *Duration `yaml:"repeat_interval,omitempty"`

	// Match specifies exact label matches for routing.
	// Alerts must match all specified label key-value pairs.
	// Example: match: {severity: "critical", team: "frontend"}
	Match map[string]string `yaml:"match,omitempty"`

	// MatchRE specifies regex label matches for routing.
	// Label values are matched against regular expressions.
	// Example: match_re: {service: "^(api|web)$"}
	MatchRE map[string]string `yaml:"match_re,omitempty"`

	// Continue indicates whether to continue matching after this route matches.
	// If true, the alert will be sent to multiple receivers.
	// Default: false
	Continue bool `yaml:"continue,omitempty"`

	// Routes contains nested child routes for hierarchical routing.
	// Child routes inherit parent's grouping settings unless overridden.
	Routes []*Route `yaml:"routes,omitempty"`

	// Internal metadata fields (not serialized)
	parsedAt time.Time `yaml:"-"`
	source   string    `yaml:"-"` // Path to config file
}

// Duration wraps time.Duration to provide custom YAML marshaling/unmarshaling.
// It supports Prometheus-style duration strings: "30s", "5m", "1h", "7d".
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a duration string from YAML.
// Supported formats: "30s", "5m", "1h", "24h"
//
// Example:
//
//	group_wait: 30s  # Parsed as 30 seconds
//	group_interval: 5m  # Parsed as 5 minutes
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return &ParseError{
			Field: "duration",
			Value: s,
			Err:   err,
		}
	}

	d.Duration = dur
	return nil
}

// MarshalYAML serializes a duration to YAML format.
// Outputs standard Go duration string format.
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

// String returns the string representation of the duration.
func (d Duration) String() string {
	return d.Duration.String()
}

// Defaults applies default values to route parameters if they are not set.
// This method should be called after parsing configuration.
//
// Default values:
//   - group_wait: 30s
//   - group_interval: 5m
//   - repeat_interval: 4h
func (r *Route) Defaults() {
	if r.GroupWait == nil {
		r.GroupWait = &Duration{30 * time.Second}
	}
	if r.GroupInterval == nil {
		r.GroupInterval = &Duration{5 * time.Minute}
	}
	if r.RepeatInterval == nil {
		r.RepeatInterval = &Duration{4 * time.Hour}
	}
}

// HasSpecialGrouping checks if the route uses the special grouping value '...'.
// The special value '...' means "group by all labels", which effectively
// creates a separate group for each unique set of labels (no actual grouping).
//
// Example:
//
//	group_by: ['...']  # Returns true
//	group_by: ['alertname']  # Returns false
func (r *Route) HasSpecialGrouping() bool {
	return len(r.GroupBy) == 1 && r.GroupBy[0] == "..."
}

// IsGlobalGroup checks if the route is configured for a single global group.
// An empty group_by list means all alerts go into one group.
//
// Example:
//
//	group_by: []  # Returns true
//	group_by: ['alertname']  # Returns false
func (r *Route) IsGlobalGroup() bool {
	return len(r.GroupBy) == 0
}

// GetGroupingLabels returns the list of labels used for grouping.
// For special grouping ('...'), returns nil.
// For global grouping (empty list), returns empty slice.
func (r *Route) GetGroupingLabels() []string {
	if r.HasSpecialGrouping() {
		return nil
	}
	return r.GroupBy
}

// GetEffectiveGroupWait returns the effective group_wait duration.
// If not set, returns the default value (30s).
func (r *Route) GetEffectiveGroupWait() time.Duration {
	if r.GroupWait == nil {
		return 30 * time.Second
	}
	return r.GroupWait.Duration
}

// GetEffectiveGroupInterval returns the effective group_interval duration.
// If not set, returns the default value (5m).
func (r *Route) GetEffectiveGroupInterval() time.Duration {
	if r.GroupInterval == nil {
		return 5 * time.Minute
	}
	return r.GroupInterval.Duration
}

// GetEffectiveRepeatInterval returns the effective repeat_interval duration.
// If not set, returns the default value (4h).
func (r *Route) GetEffectiveRepeatInterval() time.Duration {
	if r.RepeatInterval == nil {
		return 4 * time.Hour
	}
	return r.RepeatInterval.Duration
}

// Validate performs basic validation on the route configuration.
// This is a quick sanity check before deeper semantic validation.
func (r *Route) Validate() error {
	if r.Receiver == "" {
		return fmt.Errorf("receiver is required")
	}
	if len(r.GroupBy) == 0 && !r.IsGlobalGroup() {
		return fmt.Errorf("group_by must have at least one label or be empty for global grouping")
	}
	return nil
}

// Clone creates a deep copy of the route.
// Useful for route tree manipulation and testing.
func (r *Route) Clone() *Route {
	clone := &Route{
		Receiver:       r.Receiver,
		GroupBy:        make([]string, len(r.GroupBy)),
		Continue:       r.Continue,
		parsedAt:       r.parsedAt,
		source:         r.source,
	}

	copy(clone.GroupBy, r.GroupBy)

	if r.GroupWait != nil {
		clone.GroupWait = &Duration{r.GroupWait.Duration}
	}
	if r.GroupInterval != nil {
		clone.GroupInterval = &Duration{r.GroupInterval.Duration}
	}
	if r.RepeatInterval != nil {
		clone.RepeatInterval = &Duration{r.RepeatInterval.Duration}
	}

	if r.Match != nil {
		clone.Match = make(map[string]string, len(r.Match))
		for k, v := range r.Match {
			clone.Match[k] = v
		}
	}

	if r.MatchRE != nil {
		clone.MatchRE = make(map[string]string, len(r.MatchRE))
		for k, v := range r.MatchRE {
			clone.MatchRE[k] = v
		}
	}

	if r.Routes != nil {
		clone.Routes = make([]*Route, len(r.Routes))
		for i, route := range r.Routes {
			clone.Routes[i] = route.Clone()
		}
	}

	return clone
}

// String returns a human-readable string representation of the route.
func (r *Route) String() string {
	return fmt.Sprintf("Route{receiver=%s, group_by=%v, group_wait=%s, group_interval=%s, repeat_interval=%s, nested_routes=%d}",
		r.Receiver,
		r.GroupBy,
		r.GetEffectiveGroupWait(),
		r.GetEffectiveGroupInterval(),
		r.GetEffectiveRepeatInterval(),
		len(r.Routes),
	)
}
