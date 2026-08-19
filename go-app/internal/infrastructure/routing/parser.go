package routing

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
	"gopkg.in/yaml.v3"
)

// Parser limits and thresholds
const (
	// MaxConfigSize is the maximum allowed configuration file size (10 MB)
	// Protects against YAML bombs
	MaxConfigSize = 10 * 1024 * 1024 // 10 MB

	// MaxRouteDepth is the maximum nesting depth for routes
	// Protects against stack overflow and cycle detection complexity
	MaxRouteDepth = 10

	// MaxRoutes is the maximum number of routes in the configuration
	// Protects against memory exhaustion
	MaxRoutes = 10000

	// MaxReceivers is the maximum number of receivers
	MaxReceivers = 5000

	// MaxMatchersPerRoute is the maximum number of matchers per route
	MaxMatchersPerRoute = 100
)

// RouteConfigParser parses Alertmanager-compatible route configurations.
// Implements 4-layer validation:
//  1. YAML syntax validation
//  2. Structural validation (validator tags)
//  3. Semantic validation (business rules)
//  4. Security validation (YAML bombs, SSRF)
type RouteConfigParser struct {
	validator *validator.Validate
}

// NewRouteConfigParser creates a new parser with validation.
func NewRouteConfigParser() *RouteConfigParser {
	v := validator.New()

	// Register custom validators
	_ = v.RegisterValidation("alphanum_hyphen", validateAlphanumHyphen)
	_ = v.RegisterValidation("receiver_name", validateReceiverName)
	_ = v.RegisterValidation("https_production", validateHTTPSProduction)
	_ = v.RegisterValidation("slack_channel", validateSlackChannel)
	_ = v.RegisterValidation("emoji", validateEmoji)
	_ = v.RegisterValidation("slack_color", validateSlackColor)
	_ = v.RegisterValidation("telegram_chat_id", validateTelegramChatID)

	return &RouteConfigParser{
		validator: v,
	}
}

// Parse parses route configuration from bytes.
//
// Steps:
//  1. YAML unmarshaling
//  2. Required fields validation
//  3. Resolve `*_file` secret references (FU7-B), `global:` endpoint
//     fallbacks, then apply defaults recursively
//  4. Structural validation
//  5. Semantic validation (incl. time interval name/reference checks)
//  6. Build time interval index (merges deprecated top-level alias)
//  7. Compile regex patterns
//  8. Build receiver index
//  9. Set metadata
//
// Returns ValidationErrors if validation fails.
func (p *RouteConfigParser) Parse(data []byte) (*RouteConfig, error) {
	started := time.Now()

	// Step 1: YAML unmarshaling
	var config RouteConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	// Step 2: Validate required fields
	if config.Route == nil {
		return nil, fmt.Errorf("route is required")
	}
	if len(config.Receivers) == 0 {
		return nil, fmt.Errorf("at least one receiver is required")
	}

	// Step 3: Resolve `*_file` secret references (FU7-B) FIRST, then `global:`
	// endpoint fallbacks, THEN apply defaults. Order matters twice over:
	// resolveFileSecrets must run before resolveGlobalFallbacks (a
	// file-resolved per-integration value has to be in the inline field
	// before the empty-string check that decides whether `global:` fills the
	// gap runs), and PagerDutyConfig/TelegramConfig.Defaults() fill in the
	// public endpoint for an empty URL, which would mask global.pagerduty_url
	// / global.telegram_api_url if it ran first.
	if err := resolveFileSecrets(&config); err != nil {
		return nil, err
	}
	resolveGlobalFallbacks(&config)
	p.applyDefaults(&config)

	// Step 4: Structural validation (validator tags)
	if err := p.validator.Struct(&config); err != nil {
		return nil, p.formatValidationErrors(err)
	}

	// Step 5: Semantic validation (business rules)
	if err := p.validateSemantics(&config); err != nil {
		return nil, err
	}

	// Step 6: Build time interval index (merges deprecated top-level
	// mute_time_intervals: alias into time_intervals:, warns on its use)
	p.buildTimeIntervalIndex(&config)

	// Step 7: Compile regex patterns
	if err := p.compileRegexPatterns(&config); err != nil {
		return nil, err
	}

	// Step 8: Build receiver index
	p.buildReceiverIndex(&config)

	// Step 9: Set metadata
	config.Version = 1
	config.LoadedAt = time.Now()

	duration := time.Since(started)
	slog.Info("config parsed successfully",
		"routes", countRoutes(config.Route),
		"receivers", len(config.Receivers),
		"duration_ms", duration.Milliseconds(),
	)

	return &config, nil
}

// ParseFile parses route configuration from a file.
func (p *RouteConfigParser) ParseFile(path string) (*RouteConfig, error) {
	// Check file size (YAML bomb protection)
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if stat.Size() > MaxConfigSize {
		return nil, fmt.Errorf(
			"config file too large: %d bytes (max: %d bytes)",
			stat.Size(),
			MaxConfigSize,
		)
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Parse
	config, err := p.Parse(data)
	if err != nil {
		return nil, err
	}

	// Set source file
	config.SourceFile = path

	return config, nil
}

// ParseString parses route configuration from a YAML string.
func (p *RouteConfigParser) ParseString(yamlStr string) (*RouteConfig, error) {
	return p.Parse([]byte(yamlStr))
}

// ValidateConfig validates an already-parsed configuration.
// Useful for testing and hot reload scenarios.
func (p *RouteConfigParser) ValidateConfig(config *RouteConfig) error {
	// Structural validation
	if err := p.validator.Struct(config); err != nil {
		return p.formatValidationErrors(err)
	}

	// Semantic validation
	return p.validateSemantics(config)
}

// resolveGlobalFallbacks copies `global:` endpoint defaults into every
// integration that omitted its own (FU-RECEIVERS-INTEGRATION slice 2).
//
// Upstream Alertmanager semantics: the per-integration value always wins, and
// `global:` only fills a gap. Resolved once, here, so no downstream consumer
// (route tree, publishing-target builder, status API) has to know about the
// fallback — they all see a single resolved endpoint.
//
// SMTP is deliberately NOT resolved into the email configs: routing.EmailConfig
// has no smarthost/auth fields to copy into, so the publishing-target builder
// reads global directly (and validateSemantics requires it whenever any
// email_configs exist).
func resolveGlobalFallbacks(config *RouteConfig) {
	if config == nil || config.Global == nil {
		return
	}
	global := config.Global

	for _, receiver := range config.Receivers {
		if receiver == nil {
			continue
		}
		for _, cfg := range receiver.SlackConfigs {
			if cfg != nil && cfg.APIURL == "" {
				cfg.APIURL = global.SlackAPIURL
			}
		}
		for _, cfg := range receiver.PagerDutyConfigs {
			if cfg != nil && cfg.URL == "" {
				cfg.URL = global.PagerDutyURL
			}
		}
		for _, cfg := range receiver.TelegramConfigs {
			if cfg != nil && cfg.APIURL == "" {
				cfg.APIURL = global.TelegramAPIURL
			}
		}
	}
}

// applyDefaults applies default values recursively.
func (p *RouteConfigParser) applyDefaults(config *RouteConfig) {
	// Apply global defaults
	if config.Global != nil {
		config.Global.Defaults()
	}

	// Apply route defaults (via TN-121)
	if config.Route != nil {
		applyRouteDefaultsRecursive(config.Route)
	}

	// Apply receiver defaults
	for _, receiver := range config.Receivers {
		for _, cfg := range receiver.WebhookConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.PagerDutyConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.SlackConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.EmailConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.TelegramConfigs {
			cfg.Defaults()
		}
	}
}

// validateSemantics performs semantic validation.
func (p *RouteConfigParser) validateSemantics(config *RouteConfig) error {
	var errors ValidationErrors

	// Build receiver index (for reference checking) and enforce name
	// UNIQUENESS the way upstream Alertmanager does (slice-1 review finding I3
	// of FU-RECEIVERS-INTEGRATION).
	//
	// Why it matters beyond upstream parity: this index is keyed by name, so a
	// duplicate silently shadowed the earlier receiver's integrations, and
	// config-provisioned publishing target names are derived from
	// receiver name + integration kind + index — two receivers sharing a name
	// therefore produce IDENTICAL target names, and the target cache (keyed by
	// name) keeps only the last one. One receiver's integrations would never
	// deliver, with nothing in the logs to say so.
	receiverIndex := make(map[string]bool)
	for i, receiver := range config.Receivers {
		if receiver == nil {
			continue
		}
		if receiverIndex[receiver.Name] {
			errors.Add(
				fmt.Sprintf("receivers[%d].name", i),
				fmt.Sprintf("duplicate receiver name %q", receiver.Name),
				"Receiver names must be unique - rename the duplicate (upstream Alertmanager rejects duplicates too)",
			)
			continue
		}
		receiverIndex[receiver.Name] = true
	}

	// Build time interval name set (for route reference checking) and
	// validate names are non-empty and unique across both the primary
	// `time_intervals:` section and the deprecated top-level
	// `mute_time_intervals:` alias.
	timeIntervalNames := make(map[string]bool)
	validateTimeIntervalNames := func(groups []timeinterval.TimeInterval, field string) {
		for i, group := range groups {
			if group.Name == "" {
				errors.Add(
					fmt.Sprintf("%s[%d]", field, i),
					"time interval group is missing required 'name' field",
					"Add a unique 'name' to this time_intervals entry",
				)
				continue
			}
			if timeIntervalNames[group.Name] {
				errors.Add(
					fmt.Sprintf("%s[%d]", field, i),
					fmt.Sprintf("duplicate time interval name %q", group.Name),
					"Time interval names must be unique across time_intervals and mute_time_intervals sections",
				)
				continue
			}
			timeIntervalNames[group.Name] = true
		}
	}
	validateTimeIntervalNames(config.TimeIntervals, "time_intervals")
	validateTimeIntervalNames(config.MuteTimeIntervalsSection, "mute_time_intervals")

	// Validate route tree
	if err := p.validateRouteTree(config.Route, receiverIndex, timeIntervalNames, &errors); err != nil {
		return err
	}

	// Validate receivers
	for i, receiver := range config.Receivers {
		// Endpoint completeness after the `global:` fallback pass
		// (FU-RECEIVERS-INTEGRATION slice 2): an integration with no endpoint
		// anywhere can never deliver, and upstream refuses such a config at
		// load rather than discovering it at notify time.
		validateReceiverEndpoints(receiver, i, config.Global, &errors)

		// Check at least one config type
		if err := receiver.Validate(); err != nil {
			errors.Add(
				fmt.Sprintf("receivers[%d]", i),
				err.Error(),
				"Add at least one config: webhook_configs, pagerduty_configs, slack_configs, email_configs, or telegram_configs",
			)
		}
	}

	return errors.ErrType()
}

// validateReceiverEndpoints reports integrations left without a usable endpoint
// once `global:` fallbacks have been applied (FU-RECEIVERS-INTEGRATION slice 2).
//
// Messages name BOTH places the endpoint could have come from, because "api_url
// is required" on a config that deliberately relies on `global.slack_api_url` is
// exactly the kind of error that sends an operator looking in the wrong file.
//
// Email is the odd one out: routing.EmailConfig carries no SMTP fields at all,
// so `global.smtp_smarthost`/`global.smtp_from` are the ONLY possible sources
// and both are required as soon as any email_configs exist — mirroring
// upstream's "no global SMTP smarthost set" / "no global SMTP from set".
func validateReceiverEndpoints(receiver *Receiver, index int, global *GlobalConfig, errors *ValidationErrors) {
	if receiver == nil {
		return
	}

	for i, cfg := range receiver.SlackConfigs {
		if cfg != nil && strings.TrimSpace(cfg.APIURL) == "" {
			errors.Add(
				fmt.Sprintf("receivers[%d].slack_configs[%d].api_url", index, i),
				fmt.Sprintf("receiver %q has a slack_config with no api_url and no global slack_api_url fallback", receiver.Name),
				"Set api_url on this slack_config, or set global.slack_api_url for all of them",
			)
		}
	}

	if len(receiver.EmailConfigs) == 0 {
		return
	}
	smarthost, from := "", ""
	if global != nil {
		smarthost = strings.TrimSpace(global.SMTPSmartHost)
		from = strings.TrimSpace(global.SMTPFrom)
	}
	if smarthost == "" {
		errors.Add(
			fmt.Sprintf("receivers[%d].email_configs", index),
			fmt.Sprintf("receiver %q declares email_configs but no SMTP smarthost is configured", receiver.Name),
			"Set global.smtp_smarthost (AMP has no per-email_config smarthost field — see docs/ALERTMANAGER_COMPATIBILITY.md)",
		)
	}
	if from == "" {
		allHaveFrom := true
		for _, cfg := range receiver.EmailConfigs {
			if cfg == nil || strings.TrimSpace(cfg.From) == "" {
				allHaveFrom = false
				break
			}
		}
		if !allHaveFrom {
			errors.Add(
				fmt.Sprintf("receivers[%d].email_configs", index),
				fmt.Sprintf("receiver %q has an email_config with no from address and no global smtp_from fallback", receiver.Name),
				"Set from on every email_config, or set global.smtp_from",
			)
		}
	}
}

// validateRouteTree recursively validates the route tree.
func (p *RouteConfigParser) validateRouteTree(
	route *grouping.Route,
	receiverIndex map[string]bool,
	timeIntervalNames map[string]bool,
	errors *ValidationErrors,
) error {
	if route == nil {
		return nil
	}

	// Check nesting depth
	depth := calculateRouteDepth(route)
	if depth > MaxRouteDepth {
		errors.Add(
			"route",
			fmt.Sprintf("route nesting too deep: %d (max: %d)", depth, MaxRouteDepth),
			"Flatten the route tree or increase MaxRouteDepth",
		)
		return errors.ErrType()
	}

	// Validate receiver reference
	if route.Receiver != "" && !receiverIndex[route.Receiver] {
		errors.Add(
			fmt.Sprintf("route[receiver=%s]", route.Receiver),
			fmt.Sprintf("receiver '%s' not found", route.Receiver),
			"Define the receiver in the 'receivers' section",
		)
	}

	// Validate mute_time_intervals / active_time_intervals references
	for _, name := range route.MuteTimeIntervals {
		if !timeIntervalNames[name] {
			errors.Add(
				fmt.Sprintf("route[mute_time_intervals=%s]", name),
				fmt.Sprintf("time interval '%s' not found", name),
				"Define it in the 'time_intervals' section",
			)
		}
	}
	for _, name := range route.ActiveTimeIntervals {
		if !timeIntervalNames[name] {
			errors.Add(
				fmt.Sprintf("route[active_time_intervals=%s]", name),
				fmt.Sprintf("time interval '%s' not found", name),
				"Define it in the 'time_intervals' section",
			)
		}
	}

	// Recursively validate child routes
	for i, child := range route.Routes {
		if err := p.validateRouteTree(child, receiverIndex, timeIntervalNames, errors); err != nil {
			return err
		}

		// Validate child has receiver (inherited or explicit)
		if child.Receiver == "" && route.Receiver == "" {
			errors.Add(
				fmt.Sprintf("route.routes[%d]", i),
				"child route has no receiver (not inherited from parent)",
				"Add 'receiver' field or set in parent route",
			)
		}
	}

	return errors.ErrType()
}

// anchorMatchREPattern mirrors internal/business/routing.anchorRegex (and
// its independently-duplicated twins in
// internal/infrastructure/inhibition.anchorMatcherRegex and
// internal/core/silencing.anchorSilenceRegex): a label value must match
// the WHOLE pattern, not merely contain a substring matching it —
// upstream Alertmanager semantics. Cannot import business/routing's copy
// directly: business/routing already imports this package
// (infrastructure/routing), so the reverse import would cycle.
//
// Review fix round 3 (R7, latent): config.CompiledRegex (built here) is
// NOT on any live match path today — RouteMatcher.regexMatch (the actual
// evaluator) has its own independently-anchored cache-miss compile, and
// production wires `businessrouting.NewRouteMatcher(nil, matcherOpts)`
// (service_registry.go), i.e. never calls RegexCache.Preload with this
// map at all; the doc-comment's own `ExtractCompiledPatterns(config)`
// bridging function referenced in matcher.go doesn't exist anywhere in
// the codebase. But RegexCache.Preload/Put exist and are keyed by the
// RAW pattern the same way regexMatch's Get is — if this map is ever
// wired up in the future without this fix, an unanchored entry inserted
// via Preload would silently win over regexMatch's own anchored
// cache-miss compile for that pattern's every subsequent hit (a
// unanchored entry, once cached, is never re-compiled). Anchoring here
// now, while this path is unused, means whoever eventually builds that
// bridge inherits a correct cache rather than a latent poisoning bug.
func anchorMatchREPattern(pattern string) string {
	return "^(?:" + pattern + ")$"
}

// compileRegexPatterns compiles all MatchRE patterns for performance.
func (p *RouteConfigParser) compileRegexPatterns(config *RouteConfig) error {
	config.CompiledRegex = make(map[*grouping.Route]map[string]*regexp.Regexp)

	var compileRoute func(*grouping.Route) error
	compileRoute = func(route *grouping.Route) error {
		if route == nil {
			return nil
		}

		// Compile MatchRE patterns, anchored ^(?:pattern)$ — see
		// anchorMatchREPattern's doc comment for why this matters even
		// though nothing consumes config.CompiledRegex today.
		if len(route.MatchRE) > 0 {
			patterns := make(map[string]*regexp.Regexp)
			for key, pattern := range route.MatchRE {
				regex, err := regexp.Compile(anchorMatchREPattern(pattern))
				if err != nil {
					return fmt.Errorf(
						"invalid regex for route.match_re[%s]: %w",
						key,
						err,
					)
				}
				patterns[key] = regex
			}
			config.CompiledRegex[route] = patterns
		}

		// Recursively compile child routes
		for _, child := range route.Routes {
			if err := compileRoute(child); err != nil {
				return err
			}
		}

		return nil
	}

	return compileRoute(config.Route)
}

// buildReceiverIndex builds O(1) receiver lookup map.
func (p *RouteConfigParser) buildReceiverIndex(config *RouteConfig) {
	config.ReceiverIndex = make(map[string]*Receiver, len(config.Receivers))
	for _, receiver := range config.Receivers {
		config.ReceiverIndex[receiver.Name] = receiver
	}
}

// buildTimeIntervalIndex builds an O(1) name lookup map for named time
// interval groups, merging the primary `time_intervals:` section with the
// deprecated top-level `mute_time_intervals:` alias.
//
// Must run after validateSemantics, which already guarantees names are
// non-empty and unique across both sections - so overwriting on collision
// here is unreachable in practice, just defensive.
func (p *RouteConfigParser) buildTimeIntervalIndex(config *RouteConfig) {
	if len(config.MuteTimeIntervalsSection) > 0 {
		slog.Warn("config uses deprecated top-level 'mute_time_intervals:' section; rename it to 'time_intervals:'")
	}

	config.TimeIntervalIndex = make(
		map[string]timeinterval.TimeInterval,
		len(config.TimeIntervals)+len(config.MuteTimeIntervalsSection),
	)

	for _, group := range config.TimeIntervals {
		config.TimeIntervalIndex[group.Name] = group
	}
	for _, group := range config.MuteTimeIntervalsSection {
		config.TimeIntervalIndex[group.Name] = group
	}
}

// formatValidationErrors converts validator errors to ValidationErrors.
func (p *RouteConfigParser) formatValidationErrors(err error) error {
	var errors ValidationErrors

	validationErrors, ok := err.(validator.ValidationErrors)
	if !ok {
		return err
	}

	for _, fieldErr := range validationErrors {
		errors.Add(
			fieldErr.Namespace(),
			fieldErr.Error(),
			suggestionForTag(fieldErr.Tag()),
		)
	}

	return errors.ErrType()
}

// suggestionForTag returns an actionable hint for a struct-tag validation
// failure, or "" when the tag's own message is already self-explanatory.
//
// receiver_name is the case that needs one (final review finding 7): the raw
// validator message is "Key: 'RouteConfig.Receivers[0].Name' Error:Field
// validation for 'Name' failed on the 'receiver_name' tag", which tells the
// operator nothing about WHICH character is the problem — and '/' is the only
// one.
func suggestionForTag(tag string) string {
	switch tag {
	case "receiver_name":
		return fmt.Sprintf("receiver names may contain any character except %q, which is reserved as the group-key separator (\"receiver=<name>/<group-key>\"); rename the receiver", string(ReceiverNameReservedChar))
	default:
		return ""
	}
}

// Custom validators

// ReceiverNameReservedChar is the single character a receiver name may not
// contain. Upstream Alertmanager places no restriction at all on receiver
// names, so this is AMP's only divergence — and it is a hard implementation
// constraint, not style: group keys are formatted as
// "receiver=<name>/<generated-key>" (see AlertProcessor.groupKeyFor) and the
// receiver is recovered by splitting on the FIRST '/' (see
// grouping.receiverFromGroupKey). A '/' inside the name would truncate it and
// route the group to the wrong receiver — or to none.
const ReceiverNameReservedChar = '/'

// validateReceiverName accepts any non-empty receiver name that does not
// contain ReceiverNameReservedChar.
//
// Final review finding 7: receiver names were validated with
// validateAlphanumHyphen, which rejects names upstream Alertmanager accepts
// and that real configs use — "team.dba", "email:sre", "ops team",
// "équipe-réseau". A config migrated from upstream failed to load with a
// validation error, which reads as "AMP doesn't support my config" rather
// than the cosmetic restriction it was.
//
// Emptiness is enforced separately by the `required` tag on the same field;
// returning true for "" here keeps the two error messages from duplicating.
func validateReceiverName(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}
	return !strings.ContainsRune(value, ReceiverNameReservedChar)
}

func validateAlphanumHyphen(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	for _, r := range value {
		if (r < 'a' || r > 'z') &&
			(r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') &&
			r != '-' && r != '_' {
			return false
		}
	}

	return true
}

func validateHTTPSProduction(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// In development mode, allow HTTP (check env var)
	if os.Getenv("ENVIRONMENT") == "development" {
		return true
	}

	// In production, require HTTPS
	return len(value) >= 8 && value[:8] == "https://"
}

func validateSlackChannel(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Format: #channel or @user
	return len(value) >= 2 && (value[0] == '#' || value[0] == '@')
}

func validateEmoji(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Format: :emoji:
	return len(value) >= 3 && value[0] == ':' && value[len(value)-1] == ':'
}

func validateSlackColor(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Predefined colors
	if value == "good" || value == "warning" || value == "danger" {
		return true
	}

	// Hex color
	if len(value) == 7 && value[0] == '#' {
		for _, r := range value[1:] {
			if (r < '0' || r > '9') &&
				(r < 'a' || r > 'f') &&
				(r < 'A' || r > 'F') {
				return false
			}
		}
		return true
	}

	return false
}

// validateTelegramChatID validates a Telegram chat_id.
// Accepted formats: "@channelusername" or a numeric id (optionally negative
// for groups/channels, e.g. "-1001234567890").
func validateTelegramChatID(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	if value[0] == '@' {
		return len(value) > 1
	}

	start := 0
	if value[0] == '-' {
		start = 1
	}
	if start >= len(value) {
		return false
	}
	for _, r := range value[start:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Helper functions

func countRoutes(route *grouping.Route) int {
	if route == nil {
		return 0
	}

	count := 1
	for _, child := range route.Routes {
		count += countRoutes(child)
	}

	return count
}

func calculateRouteDepth(route *grouping.Route) int {
	if route == nil || len(route.Routes) == 0 {
		return 1
	}

	maxDepth := 0
	for _, child := range route.Routes {
		depth := calculateRouteDepth(child)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	return maxDepth + 1
}

// applyRouteDefaultsRecursive applies defaults to route tree recursively.
func applyRouteDefaultsRecursive(route *grouping.Route) {
	if route == nil {
		return
	}

	// Apply defaults to current route
	route.Defaults()

	// Recursively apply to children
	for _, child := range route.Routes {
		applyRouteDefaultsRecursive(child)
	}
}
