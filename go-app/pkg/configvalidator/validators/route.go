package validators

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	realrouting "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/ipiton/AMP/pkg/configvalidator/matcher"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// MaxRouteDepth is sourced directly from the real loader
// (internal/infrastructure/routing/parser.go), not copied, so the two
// can never drift apart again: a config this validator calls "valid"
// must also be a config the loader that runs at startup actually
// accepts. A previous version of this file hardcoded 100 here against
// a comment claiming it mirrored the loader, while the loader's actual
// limit was 10 - meaning routes at depth 11-100 validated clean here
// and were then rejected at startup. Fix round 1 (Phase 5 review).
const MaxRouteDepth = realrouting.MaxRouteDepth

// RouteValidator validates the routing tree: matcher syntax (legacy
// match/match_re maps and the AMP matchers: list syntax), receiver
// references, timing fields, and tree shape (depth/cycles).
type RouteValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewRouteValidator creates a new RouteValidator.
func NewRouteValidator(opts types.Options, logger *slog.Logger) *RouteValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &RouteValidator{options: opts, logger: logger}
}

// Validate performs route validation.
func (v *RouteValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil || cfg.Route == nil {
		// Absence of a root route is already reported as E100 by
		// StructuralValidator; nothing further to check here.
		return
	}

	receiverIndex := make(map[string]bool, len(cfg.Receivers))
	for _, r := range cfg.Receivers {
		if r != nil && r.Name != "" {
			receiverIndex[r.Name] = true
		}
	}

	// E103: the root route cannot inherit a receiver from anywhere, so it
	// must set one explicitly.
	if cfg.Route.Receiver == "" {
		result.AddError(newError(
			"E103", "route", "route.receiver",
			"root route must have a receiver",
			"Add 'receiver' field to the root route",
		))
	}

	visited := make(map[*config.Route]bool)
	v.validateNode(cfg.Route, "route", 1, receiverIndex, visited, result)
}

// validateNode recursively validates a single route node and its children.
//
// visited tracks nodes on the current DFS path (not globally) so it can
// detect a true cycle: a node reachable from itself via Routes pointers.
// A tree built from YAML unmarshalling can never produce this (each node
// is freshly allocated), but the same Route type can be constructed and
// wired by hand elsewhere (tests, future programmatic config assembly),
// so the check is kept as a cheap defensive guard - see route_test.go for
// a hand-built cycle exercising it.
func (v *RouteValidator) validateNode(
	route *config.Route,
	path string,
	depth int,
	receiverIndex map[string]bool,
	visited map[*config.Route]bool,
	result *types.Result,
) {
	if route == nil {
		return
	}

	if visited[route] {
		result.AddError(newError(
			"E160", "route", path,
			"route tree contains a cycle",
			"Ensure no route.routes entry points back to an ancestor route",
		))
		return
	}
	visited[route] = true
	defer delete(visited, route)

	if depth > MaxRouteDepth {
		result.AddError(newError(
			"E101", "route", path,
			fmt.Sprintf("route nesting too deep: exceeds maximum depth (%d)", MaxRouteDepth),
			"Flatten the route tree",
		))
		return
	}

	v.validateReceiverRef(route, path, receiverIndex, result)
	v.validateDurations(route, path, result)
	v.validateGroupBy(route, path, result)
	v.validateMatchers(route, path, result)
	v.noteTimeIntervals(route, path, result)

	for i, child := range route.Routes {
		v.validateNode(child, fmt.Sprintf("%s.routes[%d]", path, i), depth+1, receiverIndex, visited, result)
	}
}

// validateReceiverRef checks E102: a non-empty receiver must exist in the
// receivers section. An empty receiver is valid (inherits from parent).
func (v *RouteValidator) validateReceiverRef(route *config.Route, path string, receiverIndex map[string]bool, result *types.Result) {
	if route.Receiver == "" || receiverIndex[route.Receiver] {
		return
	}
	result.AddError(newError(
		"E102", "route", path+".receiver",
		fmt.Sprintf("receiver '%s' not found", route.Receiver),
		"Define the receiver in the 'receivers' section or fix the typo",
	))
}

// validateDurations checks E026: timing fields must not be negative. Zero
// is treated as "unset, inherit default" (see isNonNegativeDuration).
func (v *RouteValidator) validateDurations(route *config.Route, path string, result *types.Result) {
	fields := []struct {
		name  string
		value int64
	}{
		{"group_wait", int64(route.GroupWait)},
		{"group_interval", int64(route.GroupInterval)},
		{"repeat_interval", int64(route.RepeatInterval)},
	}
	for _, f := range fields {
		if !isNonNegativeDuration(f.value) {
			result.AddError(newError(
				"E026", "route", path+"."+f.name,
				fmt.Sprintf("%s must not be negative", f.name),
				"Use a non-negative duration (e.g. '30s', '5m') or omit the field",
			))
		}
	}
}

// validateGroupBy checks E106: group_by entries must be valid Prometheus
// label names, except the "..." sentinel meaning "group by all labels".
func (v *RouteValidator) validateGroupBy(route *config.Route, path string, result *types.Result) {
	for i, label := range route.GroupBy {
		if label == "..." {
			continue
		}
		if !isValidLabelName(label) {
			result.AddError(newError(
				"E106", "route", fmt.Sprintf("%s.group_by[%d]", path, i),
				fmt.Sprintf("invalid label name '%s' in group_by", label),
				"Label names must match [a-zA-Z_][a-zA-Z0-9_]*, or use '...' to group by all labels",
			))
		}
	}
}

// validateMatchers checks matcher syntax across the three supported forms:
// legacy match (equality map), legacy match_re (regex map), and the AMP
// matchers: list syntax (added since Phase 1). All three funnel through
// the shared pkg/configvalidator/matcher grammar so error messages stay
// consistent regardless of which form a config uses.
func (v *RouteValidator) validateMatchers(route *config.Route, path string, result *types.Result) {
	if len(route.Match) > 0 {
		result.AddWarning(newWarning(
			"W100", "route", path+".match",
			"'match' is deprecated in favor of 'matchers'",
			`Migrate to matchers with format: ["label=value"]`,
		))
		for label := range route.Match {
			if !isValidLabelName(label) {
				result.AddError(newError(
					"E106", "route", path+".match",
					fmt.Sprintf("invalid label name '%s' in match", label),
					"Label names must match [a-zA-Z_][a-zA-Z0-9_]*",
				))
			}
		}
	}

	if len(route.MatchRE) > 0 {
		result.AddWarning(newWarning(
			"W101", "route", path+".match_re",
			"'match_re' is deprecated in favor of 'matchers'",
			`Migrate to matchers with a regex operator: ["label=~regex"]`,
		))
		for label, pattern := range route.MatchRE {
			if !isValidLabelName(label) {
				result.AddError(newError(
					"E106", "route", path+".match_re",
					fmt.Sprintf("invalid label name '%s' in match_re", label),
					"Label names must match [a-zA-Z_][a-zA-Z0-9_]*",
				))
				continue
			}
			if !isValidRegex(pattern) {
				result.AddError(newError(
					"E105", "route", path+".match_re",
					fmt.Sprintf("invalid regex '%s' for label '%s' in match_re", pattern, label),
					"Check regex syntax and escaping",
				))
			}
		}
	}

	if len(route.Matchers) > 0 {
		_, errs := matcher.ParseMatchers(route.Matchers)
		for _, err := range errs {
			result.AddError(newError(
				matcherErrorCode(err), "route", path+".matchers",
				err.Error(),
				"Use format: label=value, label!=value, label=~regex, or label!~regex",
			))
		}
	}
}

// noteTimeIntervals surfaces an informational note when a route references
// mute_time_intervals/active_time_intervals. Resolving those names against
// the top-level time_intervals/mute_time_intervals sections is planned for
// a later phase (same ACCEPT decision as StructuralValidator).
func (v *RouteValidator) noteTimeIntervals(route *config.Route, path string, result *types.Result) {
	if !v.options.IncludeInfo {
		return
	}
	if len(route.MuteTimeIntervals) == 0 && len(route.ActiveTimeIntervals) == 0 {
		return
	}
	result.AddInfo(types.Info{
		Type:     types.InfoTypeCompatibility,
		Code:     "I001",
		Message:  "mute_time_intervals/active_time_intervals are accepted but not yet resolved against time_intervals sections (planned for a later phase)",
		Location: types.Location{Section: "route", Field: path},
		DocsURL:  docsURL,
	})
}
