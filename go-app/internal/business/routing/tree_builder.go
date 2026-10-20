package routing

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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
	// grouping.Route stores durations as *grouping.Duration: nil = unset
	// (inherit), non-nil = set, including an explicit 0s group_wait.
	node.GroupBy = b.inheritGroupBy(parent, route)
	node.GroupWait = b.inheritDuration(parent, route.GroupWait, "group_wait")
	node.GroupInterval = b.inheritDuration(parent, route.GroupInterval, "group_interval")
	node.RepeatInterval = b.inheritDuration(parent, route.RepeatInterval, "repeat_interval")

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

// The unescaping loop in unquoteMatcherValue below is a verbatim port of
// ParseMatcher's value-unescaping logic from
// github.com/prometheus/alertmanager@v0.34.0/pkg/labels/parse.go:
//
//	Copyright 2018 Prometheus Team
//	Licensed under the Apache License, Version 2.0 (the "License");
//	you may not use this file except in compliance with the License.
//	You may obtain a copy of the License at
//
//	    http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//
// (fix-round 2, Minor #6: the port named the upstream file but originally
// carried no attribution — Apache-2.0 section 4 asks for the notice to be
// retained in derivative works.)

// unquoteMatcherValue applies prometheus/alertmanager's pkg/labels matcher
// value grammar verbatim — ported from ParseMatcher in
// github.com/prometheus/alertmanager@v0.34.0/pkg/labels/parse.go, the
// authority for this parser and pkg/configvalidator/matcher.Parse alike
// (alertmanager-parity wave-5 item 5, FU-PARSEARGUMENT-QUOTE-HANDLING).
//
// fix-round finding I-3: the first pass here was a simplified "strip a
// matched outer quote pair, unescape only \" and \\", which diverged from
// upstream on four verified points: `\n` was never unescaped to a line
// feed; escaping was skipped entirely for an unquoted value (upstream
// applies it either way); an unescaped inner `"` was silently accepted
// instead of rejected; and an unterminated/unmatched quote failed open
// (kept the literal, mangled value) instead of erroring. This port fixes
// all four by matching upstream's algorithm exactly rather than
// approximating it.
//
// Rules (see upstream's ParseMatcher doc comment for the canonical
// wording):
//   - A single leading '"' switches on "expect a trailing quote" and is
//     stripped; a value need not be quoted at all.
//   - Escaping applies regardless of quoting: '\n' -> LF, '\"' -> '"',
//     '\\' -> '\\'. Any other '\x' is a "spurious escape" and is kept
//     literally as the two characters '\' and 'x' — not an error, and not
//     just the backslash dropped.
//   - A lone trailing '\' with nothing after it is a literal backslash.
//   - An unescaped '"' is only valid as the very last character, and only
//     when a leading '"' was present; anywhere else (mid-value, or in a
//     value that never had a leading quote) it is an error. A leading '"'
//     with no matching trailing '"' at the end is also an error.
//   - The input must be valid UTF-8.
//
// Returns ok=false (not an error type) to match this file's existing
// "malformed matchers: entries are skipped" posture (parseMatchers' doc
// comment) — parseMatcherExpr folds this into its own ok=false return.
func unquoteMatcherValue(rawValue string) (string, bool) {
	var expectTrailingQuote bool
	if after, hasQuote := strings.CutPrefix(rawValue, `"`); hasQuote {
		rawValue = after
		expectTrailingQuote = true
	}

	if !utf8.ValidString(rawValue) {
		return "", false
	}

	var value strings.Builder
	var escaped bool
	for i := 0; i < len(rawValue); i++ {
		c := rawValue[i]

		if escaped {
			escaped = false
			switch c {
			case 'n':
				value.WriteByte('\n')
			case '"', '\\':
				value.WriteByte(c)
			default:
				// Spurious escape: keep the backslash literal.
				value.WriteByte('\\')
				value.WriteByte(c)
			}
			continue
		}

		switch c {
		case '\\':
			if i < len(rawValue)-1 {
				escaped = true
				continue
			}
			// Trailing lone backslash: literal.
			value.WriteByte('\\')
		case '"':
			if !expectTrailingQuote || i < len(rawValue)-1 {
				return "", false
			}
			expectTrailingQuote = false
		default:
			value.WriteByte(c)
		}
	}

	if expectTrailingQuote {
		return "", false
	}

	return value.String(), true
}

// parseMatcherExpr parses one `matchers:` list entry into a Matcher.
//
// Supported forms (whitespace around the operator is optional):
//
//	label=value
//	label="value"
//	label="va\"lue"   (escaped quote, unescaped to `va"lue`)
//	label="line\nbreak" (escaped LF, unescaped to a real line feed)
//	label!=value
//	label=~"regex"
//	label!~"regex"
//
// Value quoting/escaping follows unquoteMatcherValue's ported upstream
// grammar — see that function's doc comment. Returns ok=false if expr
// doesn't match the expected grammar, INCLUDING a value that violates the
// quote grammar (unescaped inner quote, unterminated quote, invalid
// UTF-8): this is one more thing that can make a `matchers:` entry
// malformed, on top of "no operator found" — both fold into the same
// ok=false, matching parseMatchers' "malformed entries are skipped" posture.
func parseMatcherExpr(expr string) (Matcher, bool) {
	groups := matcherExprPattern.FindStringSubmatch(expr)
	if groups == nil {
		return Matcher{}, false
	}

	name := groups[1]
	op := groups[2]
	value, ok := unquoteMatcherValue(strings.TrimSpace(groups[3]))
	if !ok {
		return Matcher{}, false
	}

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

// inheritGroupBy applies inheritance logic for group_by parameter.
//
// Priority:
//  1. Route's own group_by (if set)
//  2. Parent's group_by (if exists)
//  3. global.group_by (alertmanager-parity wave-5, FU-GLOB-DEFAULT-VALUES
//     — restored on infraroute.GlobalConfig; see that type's doc comment
//     for why this is an AMP-only convenience, not an upstream field)
//  4. EMPTY — upstream Alertmanager's actual default.
//
// Step 4 used to return ["alertname"], which is NOT upstream's default and is
// the post-merge regression this fix closes (FU-RECEIVERS-INTEGRATION's
// blackhole-receiver change made a config with no group_by loadable, which
// unmasked it). Upstream's DefaultRouteOpts leaves GroupBy empty: a route
// without group_by aggregates every alert it matches into ONE group with an
// EMPTY label set — `amtool`/`GET /api/v2/alerts/groups` show `labels: {}` and
// one notification per receiver, not one per alertname.
//
// An empty result is already the well-defined "global group" everywhere it is
// consumed, so nothing downstream needs a special case:
//   - grouping.GroupKeyGenerator.GenerateKey returns GlobalGroupKey ("{global}")
//     for an empty groupBy, so AlertProcessor.groupKeyFor produces exactly one
//     key per receiver;
//   - grouping.Route.IsGlobalGroup() already documents len(GroupBy) == 0 as
//     "all alerts go into one group";
//   - the alert-store group assembler builds one group with empty labels.
//
// Operators who WANT per-alertname grouping must say so — `group_by:
// [alertname]` — exactly as upstream requires.
func (b *TreeBuilder) inheritGroupBy(parent *RouteNode, route *grouping.Route) []string {
	if len(route.GroupBy) > 0 {
		return route.GroupBy
	}

	if parent != nil && len(parent.GroupBy) > 0 {
		return parent.GroupBy
	}

	if b.config != nil && b.config.Global != nil && len(b.config.Global.GroupBy) > 0 {
		return b.config.Global.GroupBy
	}

	return nil
}

// inheritDuration applies inheritance logic for duration parameters.
//
// Priority, mirroring upstream dispatch.newRoute (opts start as the parent's
// resolved opts, or DefaultRouteOpts at the root, then a field the route sets
// overrides them):
//  1. Route's own value, if set (non-nil) — 0s included: group_wait: 0s
//     means "notify immediately" (ROUTE-EXPLICIT-ZERO-GROUP-WAIT).
//  2. Parent's resolved value. Every non-root node has one, so a nested
//     route without its own timings takes its nearest ancestor's
//     (ROUTE-TIMINGS-NOT-INHERITED) — never the hardcoded defaults.
//  3. global.<field> (alertmanager-parity wave-5, FU-GLOB-DEFAULT-VALUES
//     — restored on infraroute.GlobalConfig; see that type's doc comment
//     for why this is an AMP-only convenience, not an upstream field).
//     Reached only at the root; nested routes see it through the root.
//  4. Default value (based on field name) — upstream's DefaultRouteOpts.
//
// Zero group_interval/repeat_interval are not rejected here but by
// RouteTree.validateDurations, as upstream rejects them at config load.
func (b *TreeBuilder) inheritDuration(
	parent *RouteNode,
	routeValue *grouping.Duration,
	fieldName string,
) time.Duration {
	if routeValue != nil {
		return routeValue.Duration
	}

	if parent != nil {
		switch fieldName {
		case "group_wait":
			return parent.GroupWait
		case "group_interval":
			return parent.GroupInterval
		case "repeat_interval":
			return parent.RepeatInterval
		}
	}

	if global, ok := b.globalDuration(fieldName); ok {
		return global
	}

	return b.getDefaultDuration(fieldName)
}

// globalDuration reads the global.<fieldName> fallback (see inheritDuration's
// priority list above). ok is false when config/Global is nil or the field
// itself is unset — "nothing to fall back to here"; a set field is returned
// as is, 0s included.
//
// b.config.Global's duration fields are *infraroute.Duration (a defined
// time.Duration type, not the grouping.Duration struct used on routes) —
// hence the explicit conversion.
func (b *TreeBuilder) globalDuration(fieldName string) (time.Duration, bool) {
	if b.config == nil || b.config.Global == nil {
		return 0, false
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
		return 0, false
	}
	return time.Duration(*d), true
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
