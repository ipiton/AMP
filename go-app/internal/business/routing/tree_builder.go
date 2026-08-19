package routing

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// TreeBuilder constructs a RouteTree from RouteConfig.
//
// TreeBuilder handles:
// - Parsing route hierarchy from config
// - Applying parameter inheritance (group_by, timings)
// - Resolving receiver references
// - Validating tree structure (if enabled)
//
// Usage:
//
//	builder := routing.NewTreeBuilder(config, routing.BuildOptions{
//	    ValidateOnBuild: true,
//	    CompileMatchers: true,
//	})
//	tree, err := builder.Build()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Thread Safety:
// - TreeBuilder is not thread-safe (build one tree per instance)
// - The resulting RouteTree is immutable and thread-safe
type TreeBuilder struct {
	// config is the input routing configuration.
	// Canonical type from infrastructure/routing (TN-137 dedup, task 1.2):
	// its route tree is grouping.Route, its receivers infraroute.Receiver.
	config *infraroute.RouteConfig

	// tree is the work-in-progress tree being built
	tree *RouteTree

	// errors collects validation errors during build
	errors []TreeValidationError

	// opts controls build behavior
	opts BuildOptions
}

// BuildOptions controls TreeBuilder behavior.
type BuildOptions struct {
	// ValidateOnBuild enables automatic validation after tree construction.
	// If validation fails, Build() returns error with detailed validation errors.
	// Default: true
	ValidateOnBuild bool

	// CompileMatchers enables eager regex compilation during tree build.
	// If disabled, regexes are compiled lazily on first use.
	// Default: true (fail-fast on invalid regex)
	CompileMatchers bool

	// StrictMode treats warnings as errors.
	// Warnings: unused receivers, empty matchers on non-root, etc.
	// Default: false
	StrictMode bool
}

// DefaultBuildOptions returns the recommended build options.
func DefaultBuildOptions() BuildOptions {
	return BuildOptions{
		ValidateOnBuild: true,
		CompileMatchers: true,
		StrictMode:      false,
	}
}

// NewTreeBuilder creates a new TreeBuilder with the given config and options.
//
// Returns:
// - TreeBuilder instance (ready to call Build())
// - Error if config is nil or invalid
//
// Example:
//
//	builder := routing.NewTreeBuilder(config, routing.DefaultBuildOptions())
//	tree, err := builder.Build()
func NewTreeBuilder(config *infraroute.RouteConfig, opts BuildOptions) *TreeBuilder {
	return &TreeBuilder{
		config: config,
		tree:   nil, // Will be initialized in Build()
		errors: make([]TreeValidationError, 0),
		opts:   opts,
	}
}

// Build constructs the RouteTree from config.
//
// Build Process:
// 1. Validate input config (non-nil, has root route)
// 2. Initialize tree structure
// 3. Build receiver lookup map
// 4. Build root node (recursively builds entire tree)
// 5. Calculate tree statistics (node count, depth, receiver count)
// 6. Validate tree structure (if opts.ValidateOnBuild)
//
// Returns:
// - RouteTree if successful
// - Error if config invalid or validation fails
//
// Complexity: O(N) where N is the number of routes in config
func (b *TreeBuilder) Build() (*RouteTree, error) {
	// 1. Validate input config
	if b.config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if b.config.Route == nil {
		return nil, fmt.Errorf("config has no root route")
	}

	// 2. Initialize tree
	b.tree = &RouteTree{
		Root:      nil, // Will be built below
		receivers: make(map[string]*infraroute.Receiver),
		built:     time.Now(),
	}

	// 3. Build receiver lookup map
	for _, receiver := range b.config.Receivers {
		if receiver.Name == "" {
			continue // Skip receivers without name (validation will catch this)
		}
		b.tree.receivers[receiver.Name] = receiver
	}

	// 3b. Snapshot the config's named time_intervals for this tree (task
	// 3.2). config.TimeIntervalIndex may be nil (no time_intervals: section)
	// — a nil map still reads safely (zero value, ok=false) via
	// RouteTree.GetTimeInterval, so no special-casing is needed here.
	b.tree.timeIntervals = b.config.TimeIntervalIndex

	// 4. Build root node (recursively builds entire tree)
	b.tree.Root = b.buildNode(nil, b.config.Route, "route", 0)

	// 5. Calculate tree statistics
	b.tree.stats = b.calculateStats(b.tree.Root)

	// 6. Validate tree (if enabled)
	if b.opts.ValidateOnBuild {
		validationErrors := b.tree.Validate()
		if len(validationErrors) > 0 {
			return nil, fmt.Errorf("tree validation failed: %d errors (first: %s)",
				len(validationErrors), validationErrors[0].Message)
		}
	}

	return b.tree, nil
}

// buildNode constructs a single RouteNode with parameter inheritance.
//
// This is the core of the tree building process:
// - Creates node from route config
// - Applies parameter inheritance from parent
// - Resolves receiver reference
// - Recursively builds children
//
// Parameters:
// - parent: parent node (nil for root)
// - route: route config to build from
// - path: human-readable path for debugging ("route.routes[0]")
// - level: depth in tree (0 = root)
//
// Returns: constructed RouteNode
//
// Complexity: O(1) per node, O(N) total
func (b *TreeBuilder) buildNode(
	parent *RouteNode,
	route *grouping.Route,
	path string,
	level int,
) *RouteNode {
	node := &RouteNode{
		Parent: parent,
		Path:   path,
		Level:  level,
	}

	// 1. Parse matchers (match + match_re + matchers list syntax)
	node.Matchers = b.parseMatchers(route.Match, route.MatchRE, route.Matchers)

	// 2. Set receiver name
	node.Receiver = route.Receiver
	if node.Receiver == "" && parent != nil {
		node.Receiver = parent.Receiver
	}

	// 3. Resolve receiver config
	if node.Receiver != "" {
		node.ReceiverConfig = b.tree.receivers[node.Receiver]
	}

	// 4. Set continue flag
	node.Continue = route.Continue

	// 5. Apply parameter inheritance
	// grouping.Route stores durations as *grouping.Duration (nil = unset);
	// durationOrZero unwraps to time.Duration so inheritDuration's existing
	// "> 0 means explicitly set" check keeps working unchanged.
	node.GroupBy = b.inheritGroupBy(parent, route)
	node.GroupWait = b.inheritDuration(parent, durationOrZero(route.GroupWait), "group_wait")
	node.GroupInterval = b.inheritDuration(parent, durationOrZero(route.GroupInterval), "group_interval")
	node.RepeatInterval = b.inheritDuration(parent, durationOrZero(route.RepeatInterval), "repeat_interval")

	// 5b. mute_time_intervals/active_time_intervals (task 3.2) are
	// deliberately NOT inherited from parent — set verbatim from this
	// node's own route config, unlike every field above.
	node.MuteTimeIntervals = route.MuteTimeIntervals
	node.ActiveTimeIntervals = route.ActiveTimeIntervals

	// 6. Build children recursively
	for i, childRoute := range route.Routes {
		childPath := fmt.Sprintf("%s.routes[%d]", path, i)
		child := b.buildNode(node, childRoute, childPath, level+1)
		node.Children = append(node.Children, child)
	}

	return node
}

// matcherExprPattern parses a single entry of the `matchers:` list syntax,
// e.g. `severity="critical"`, `severity != critical`, `sev =~ "a|b"`.
//
// Group 1: label name
// Group 2: operator (=, !=, =~, !~)
// Group 3: value (quotes, if any, stripped separately)
var matcherExprPattern = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(!=|=~|!~|=)\s*(.*)$`)

// parseMatchers converts match, match_re, and the `matchers:` list syntax
// into a unified Matcher list.
//
//   - match: equality matchers (=), never negative.
//   - match_re: regex matchers (=~), never negative.
//   - matchers: free-form expressions supporting all 4 operators
//     (=, !=, =~, !~), the only syntax that can express negative matchers
//     (IsNegative=true), since match/match_re have no way to encode negation.
//
// Malformed entries in matchers are skipped (best-effort parsing; full
// validation is out of scope here).
func (b *TreeBuilder) parseMatchers(match map[string]string, matchRE map[string]string, matcherExprs []string) []Matcher {
	matchers := make([]Matcher, 0, len(match)+len(matchRE)+len(matcherExprs))

	// Equality matchers (match: label -> value)
	for name, value := range match {
		matchers = append(matchers, Matcher{
			Name:       name,
			Value:      value,
			IsRegex:    false,
			IsNegative: false,
		})
	}

	// Regex matchers (match_re: label -> pattern)
	for name, pattern := range matchRE {
		matchers = append(matchers, Matcher{
			Name:       name,
			Value:      pattern,
			IsRegex:    true,
			IsNegative: false,
		})
	}

	// matchers: list syntax (free-form expressions, all 4 operators)
	for _, expr := range matcherExprs {
		if m, ok := parseMatcherExpr(expr); ok {
			matchers = append(matchers, m)
		}
	}

	return matchers
}

// unquoteMatcherValue strips a matched pair of surrounding double quotes
// from a `matchers:` list value and unescapes `\"`/`\\` within it, per
// prometheus/alertmanager's pkg/labels matcher grammar — the authority for
// this parser and pkg/configvalidator/matcher.Parse alike (alertmanager-
// parity wave-5 item 5, FU-PARSEARGUMENT-QUOTE-HANDLING: quote handling was
// the third matcher-grammar divergence found between the two, after
// pkg/configvalidator/matcher.Parse turned out not to strip quotes at ALL —
// see that function's own doc comment).
//
// An unquoted value, or one without a real matched closing quote, is
// returned unchanged: strings.Trim would also strip an unmatched quote
// (e.g. `foo"` -> `foo`), silently mangling malformed input instead of
// leaving it visibly wrong — the same posture this function's predecessor
// (a bare quote-strip with no unescaping) already had.
//
// Only `\"` and `\\` are recognized escapes, matching upstream's matcher
// grammar; any other backslash sequence (e.g. `\n`) is left as a literal
// two-character pair rather than interpreted as a Go string escape.
//
// Known limitation shared with the pre-existing bare quote-strip this
// replaces: detecting the "real" closing quote is a simple last-byte check,
// not an escape-aware scan from the start — a value like `"foo\"` (an
// escaped quote with no actual closing quote after it) still misreads as
// quoted-and-terminated. A correct fix needs a real tokenizer, not a regex
// capture group; out of scope for this task (see FU-PARSEARGUMENT-QUOTE-
// HANDLING's brief: quote handling, not a lexer rewrite).
func unquoteMatcherValue(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}

	inner := value[1 : len(value)-1]
	var sb strings.Builder
	sb.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' && i+1 < len(inner) {
			next := inner[i+1]
			if next == '"' || next == '\\' {
				sb.WriteByte(next)
				i++
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// parseMatcherExpr parses one `matchers:` list entry into a Matcher.
//
// Supported forms (whitespace around the operator is optional):
//
//	label=value
//	label="value"
//	label="va\"lue"   (escaped quote, unescaped to `va"lue`)
//	label!=value
//	label=~"regex"
//	label!~"regex"
//
// Returns ok=false if expr doesn't match the expected grammar.
func parseMatcherExpr(expr string) (Matcher, bool) {
	groups := matcherExprPattern.FindStringSubmatch(expr)
	if groups == nil {
		return Matcher{}, false
	}

	name := groups[1]
	op := groups[2]
	value := unquoteMatcherValue(strings.TrimSpace(groups[3]))

	m := Matcher{Name: name, Value: value}
	switch op {
	case "=":
		// IsRegex=false, IsNegative=false (zero values)
	case "!=":
		m.IsNegative = true
	case "=~":
		m.IsRegex = true
	case "!~":
		m.IsRegex = true
		m.IsNegative = true
	default:
		return Matcher{}, false
	}

	return m, true
}

// durationOrZero unwraps a *grouping.Duration into a time.Duration,
// treating nil (field absent from config) as the zero value. This keeps
// inheritDuration's "> 0 means explicitly set" check meaningful for the
// canonical grouping.Route, which represents unset durations as nil
// pointers rather than zero time.Duration.
func durationOrZero(d *grouping.Duration) time.Duration {
	if d == nil {
		return 0
	}
	return d.Duration
}

// inheritGroupBy applies inheritance logic for group_by parameter.
func (b *TreeBuilder) inheritGroupBy(parent *RouteNode, route *grouping.Route) []string {
	// Priority:
	// 1. Route's own group_by (if set)
	// 2. Parent's group_by (if exists)
	// 3. global.group_by (alertmanager-parity wave-5, FU-GLOB-DEFAULT-VALUES
	//    — restored on infraroute.GlobalConfig; see that type's doc comment
	//    for why this is an AMP-only convenience, not an upstream field)
	// 4. Default: ["alertname"]

	if len(route.GroupBy) > 0 {
		return route.GroupBy
	}

	if parent != nil && len(parent.GroupBy) > 0 {
		return parent.GroupBy
	}

	if b.config != nil && b.config.Global != nil && len(b.config.Global.GroupBy) > 0 {
		return b.config.Global.GroupBy
	}

	return []string{"alertname"}
}

// inheritDuration applies inheritance logic for duration parameters.
func (b *TreeBuilder) inheritDuration(
	parent *RouteNode,
	routeValue time.Duration,
	fieldName string,
) time.Duration {
	// Priority:
	// 1. Route's own value (if > 0)
	// 2. Parent's value (if exists and > 0)
	// 3. global.<field> (alertmanager-parity wave-5, FU-GLOB-DEFAULT-VALUES
	//    — restored on infraroute.GlobalConfig; see that type's doc comment
	//    for why this is an AMP-only convenience, not an upstream field)
	// 4. Default value (based on field name)

	if routeValue > 0 {
		return routeValue
	}

	// Get parent value based on field name
	if parent != nil {
		switch fieldName {
		case "group_wait":
			if parent.GroupWait > 0 {
				return parent.GroupWait
			}
		case "group_interval":
			if parent.GroupInterval > 0 {
				return parent.GroupInterval
			}
		case "repeat_interval":
			if parent.RepeatInterval > 0 {
				return parent.RepeatInterval
			}
		}
	}

	if global := b.globalDuration(fieldName); global > 0 {
		return global
	}

	// Return default value
	return b.getDefaultDuration(fieldName)
}

// globalDuration reads the global.<fieldName> fallback (see inheritDuration's
// priority list above), returning 0 when config/Global is nil or the field
// itself is unset — both mean "nothing to fall back to here", identical to
// how routeValue/parent's 0 is treated by the caller.
//
// b.config.Global's duration fields are *infraroute.Duration (a defined
// time.Duration type, not the grouping.Duration struct durationOrZero
// unwraps above) — hence the explicit conversion instead of reusing that
// helper.
func (b *TreeBuilder) globalDuration(fieldName string) time.Duration {
	if b.config == nil || b.config.Global == nil {
		return 0
	}

	var d *infraroute.Duration
	switch fieldName {
	case "group_wait":
		d = b.config.Global.GroupWait
	case "group_interval":
		d = b.config.Global.GroupInterval
	case "repeat_interval":
		d = b.config.Global.RepeatInterval
	}
	if d == nil {
		return 0
	}
	return time.Duration(*d)
}

// getDefaultDuration returns the default duration for a field.
func (b *TreeBuilder) getDefaultDuration(fieldName string) time.Duration {
	switch fieldName {
	case "group_wait":
		return 30 * time.Second
	case "group_interval":
		return 5 * time.Minute
	case "repeat_interval":
		return 4 * time.Hour
	default:
		return 0
	}
}

// calculateStats computes statistics about the tree.
//
// Statistics:
// - NodeCount: total nodes (including root)
// - MaxDepth: maximum depth (root = 0)
// - ReceiverCount: unique receivers used in tree
//
// Complexity: O(N) where N is nodes
func (b *TreeBuilder) calculateStats(root *RouteNode) TreeStats {
	stats := TreeStats{
		NodeCount:     0,
		MaxDepth:      0,
		ReceiverCount: 0,
	}

	if root == nil {
		return stats
	}

	// Traverse tree to count nodes and find max depth
	var traverse func(*RouteNode, int)
	receivers := make(map[string]bool)

	traverse = func(node *RouteNode, depth int) {
		stats.NodeCount++

		if depth > stats.MaxDepth {
			stats.MaxDepth = depth
		}

		if node.Receiver != "" {
			receivers[node.Receiver] = true
		}

		for _, child := range node.Children {
			traverse(child, depth+1)
		}
	}

	traverse(root, 0)
	stats.ReceiverCount = len(receivers)

	return stats
}

// Note: this package used to define its own RouteConfig/Route/GlobalConfig
// family here, duplicating infrastructure/routing's canonical types (and
// grouping.Route for the route tree itself). TN-137 dedup (task 1.2)
// removed it: TreeBuilder now takes infrastructure/routing.RouteConfig
// directly, whose Route field is *grouping.Route.
