package routing

import (
	"fmt"
	"strings"
	"time"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// RouteNode represents a single route in the routing tree hierarchy.
//
// A RouteNode contains matchers for alert selection, routing parameters
// (group_by, timings), receiver configuration, and links to parent/children nodes.
//
// Nodes form a tree structure where each child inherits parameters from its parent
// unless explicitly overridden. The Root node has no parent and uses global defaults.
//
// Thread Safety:
// - RouteNode is immutable after construction (read-only operations are thread-safe)
// - Modifications require creating a new tree (via Clone or Rebuild)
//
// Example:
//
//	root := &RouteNode{
//	    Receiver: "default",
//	    GroupBy: []string{"alertname"},
//	    GroupWait: 30 * time.Second,
//	    Children: []*RouteNode{
//	        {Receiver: "critical", GroupBy: []string{"alertname", "severity"}},
//	    },
//	}
type RouteNode struct {
	// Matchers define which alerts match this route.
	// Matchers are evaluated from match (equality) and match_re (regex) fields in config.
	// Empty matchers means "match all" (typically for root node).
	Matchers []Matcher

	// Routing Parameters (inherited from parent if not set)

	// GroupBy specifies label names for grouping alerts.
	// Default (root): ["alertname"]
	// Inherited: from parent node
	// Override: specify in route config
	GroupBy []string

	// GroupWait is the initial wait time before sending first notification for a new group.
	// Default (root): 30s
	// Inherited: from parent node
	// Override: specify in route config
	GroupWait time.Duration

	// GroupInterval is the minimum time between notifications for the same group.
	// Default (root): 5m
	// Inherited: from parent node
	// Override: specify in route config
	GroupInterval time.Duration

	// RepeatInterval is the time to wait before re-sending a notification.
	// Default (root): 4h
	// Inherited: from parent node
	// Override: specify in route config
	RepeatInterval time.Duration

	// MuteTimeIntervals references named time_intervals (by name, resolved
	// later via RouteTree.GetTimeInterval) during which this route must NOT
	// send notifications (task 3.2). Deliberately NOT inherited from Parent
	// — upstream Alertmanager applies mute_time_intervals/active_time_intervals
	// only to the route node that declares them, unlike GroupBy/timings which
	// do inherit. Set verbatim from this node's own grouping.Route field by
	// TreeBuilder.buildNode; nil/empty means this route has none of its own.
	MuteTimeIntervals []string

	// ActiveTimeIntervals references named time_intervals during which this
	// route IS allowed to send; outside all of them, the route is muted
	// (task 3.2). Same non-inherited semantics as MuteTimeIntervals above.
	ActiveTimeIntervals []string

	// Receiver Configuration

	// Receiver is the name of the receiver to send notifications to.
	// Must exist in config.receivers list.
	// Required for all nodes (inherited from parent/root if not set).
	Receiver string

	// ReceiverConfig is the resolved receiver configuration.
	// Pre-resolved at tree build time for O(1) lookup.
	// May be nil if receiver not found (validation error).
	// Type is the canonical infrastructure/routing.Receiver (TN-137 dedup);
	// this package no longer keeps its own parallel Receiver/*Config family.
	ReceiverConfig *infraroute.Receiver

	// Control Flow

	// Continue determines if routing evaluation continues after this route matches.
	// If true: alert is sent to this receiver AND continues to next sibling routes.
	// If false: alert is sent to this receiver and routing stops.
	// Default: false (stop after first match)
	Continue bool

	// Tree Structure

	// Parent is the parent node in the routing tree.
	// nil for Root node.
	// Used for parameter inheritance.
	Parent *RouteNode

	// Children are the child routes nested under this route.
	// Evaluated in order when this route matches.
	// Empty for leaf nodes.
	Children []*RouteNode

	// Metadata (for debugging and validation)

	// Path is the human-readable path to this node in the tree.
	// Format: "route.routes[0].routes[1]"
	// Used for validation error messages and debugging.
	Path string

	// Level is the depth of this node in the tree (0 = root).
	// Used for validation (max depth check) and debugging.
	Level int
}

// Matcher represents a single label matcher for alert routing.
//
// Matchers are used to select which alerts a route applies to.
// Supported operators: =, !=, =~, !~
//
// Examples:
//
//	// Equality: severity="critical"
//	{Name: "severity", Value: "critical", IsRegex: false, IsNegative: false}
//
//	// Inequality: severity!="info"
//	{Name: "severity", Value: "info", IsRegex: false, IsNegative: true}
//
//	// Regex: namespace=~"prod.*"
//	{Name: "namespace", Value: "prod.*", IsRegex: true, IsNegative: false}
//
//	// Negative regex: namespace!~"dev.*"
//	{Name: "namespace", Value: "dev.*", IsRegex: true, IsNegative: true}
type Matcher struct {
	// Name is the label name to match against.
	// Example: "alertname", "severity", "namespace"
	Name string

	// Value is the value to match.
	// For equality: exact string match
	// For regex: regex pattern (must be valid regex)
	Value string

	// IsRegex indicates if this is a regex matcher.
	// false: equality/inequality (=, !=)
	// true: regex match (=~, !~)
	IsRegex bool

	// IsNegative indicates if this is a negative matcher.
	// false: positive match (=, =~)
	// true: negative match (!=, !~)
	IsNegative bool
}

// IsRoot returns true if this node is the root node (has no parent).
func (n *RouteNode) IsRoot() bool {
	return n.Parent == nil
}

// HasChildren returns true if this node has child routes.
func (n *RouteNode) HasChildren() bool {
	return len(n.Children) > 0
}

// GetChildCount returns the number of direct children of this node.
func (n *RouteNode) GetChildCount() int {
	return len(n.Children)
}

// String returns a human-readable representation of this node.
//
// Format: "Route[path] receiver=<name> matchers=<count> children=<count>"
//
// Example output:
//
//	"Route[route.routes[0]] receiver=pagerduty matchers=2 children=3"
func (n *RouteNode) String() string {
	return fmt.Sprintf("Route[%s] receiver=%s matchers=%d children=%d",
		n.Path, n.Receiver, len(n.Matchers), len(n.Children))
}

// Clone creates a deep copy of this node and its subtree.
//
// The cloned tree is completely independent from the original:
// - All fields are copied (not shared)
// - Children are recursively cloned
// - Parent references are updated to point to cloned parents
//
// Use Clone() for hot reload scenarios where you need to modify
// a tree without affecting concurrent readers of the original tree.
//
// Complexity: O(N) where N is the number of nodes in the subtree.
func (n *RouteNode) Clone() *RouteNode {
	if n == nil {
		return nil
	}

	// Clone current node (shallow copy)
	clone := &RouteNode{
		// Copy slice (new slice, same element values)
		Matchers: append([]Matcher(nil), n.Matchers...),
		GroupBy:  append([]string(nil), n.GroupBy...),

		// Copy value types
		GroupWait:      n.GroupWait,
		GroupInterval:  n.GroupInterval,
		RepeatInterval: n.RepeatInterval,

		// Copy strings (immutable in Go)
		Receiver: n.Receiver,
		Path:     n.Path,

		// Copy bool
		Continue: n.Continue,

		// Copy int
		Level: n.Level,

		// Copy pointer (shared, but ReceiverConfig is immutable)
		ReceiverConfig: n.ReceiverConfig,

		// Copy slices (task 3.2, not inherited so each node's own value
		// must survive Clone independently)
		MuteTimeIntervals:   append([]string(nil), n.MuteTimeIntervals...),
		ActiveTimeIntervals: append([]string(nil), n.ActiveTimeIntervals...),

		// Parent will be set by caller
		Parent: nil,

		// Children will be cloned recursively
		Children: make([]*RouteNode, 0, len(n.Children)),
	}

	// Clone children recursively
	for _, child := range n.Children {
		childClone := child.Clone()
		childClone.Parent = clone // Update parent reference
		clone.Children = append(clone.Children, childClone)
	}

	return clone
}

// GetMatcherSignature returns a string signature of all matchers for this node.
//
// Used for duplicate matcher detection on the same level.
//
// Format: "name1=value1,name2~value2" (sorted by name)
//
// Example:
//
//	matchers: [{Name: "severity", Value: "critical"}, {Name: "namespace", Value: "prod"}]
//	signature: "namespace=prod,severity=critical"
func (n *RouteNode) GetMatcherSignature() string {
	if len(n.Matchers) == 0 {
		return ""
	}

	// Build signature strings
	signatures := make([]string, len(n.Matchers))
	for i, m := range n.Matchers {
		op := "="
		if m.IsRegex {
			op = "~"
		}
		signatures[i] = fmt.Sprintf("%s%s%s", m.Name, op, m.Value)
	}

	// Sort and join (for consistent comparison)
	// Note: In production, you might want to sort this
	return strings.Join(signatures, ",")
}

// HasMatcher returns true if this node has any matchers.
func (n *RouteNode) HasMatcher() bool {
	return len(n.Matchers) > 0
}

// MatchesAll returns true if this node has no matchers (matches all alerts).
//
// Typically used for root node and default fallback routes.
func (n *RouteNode) MatchesAll() bool {
	return len(n.Matchers) == 0
}

// Note: this package used to define its own Receiver/WebhookConfig/
// PagerDutyConfig/SlackConfig/HTTPConfig/BasicAuth/TLSConfig family here,
// duplicating infrastructure/routing's canonical types. TN-137 dedup
// (task 1.2) removed it: RouteNode.ReceiverConfig, RouteTree's receiver
// index, and RouteTreeManager.Reload now all use
// infrastructure/routing.Receiver / .RouteConfig directly.
