// Package configvalidator validates Alertmanager-style configuration
// files (route/receivers/inhibit_rules/global, plus AMP extensions since
// Phase 1 such as the "matchers:" list syntax) independently of the AMP
// server runtime.
//
// # "valid" is not the same claim as "fully wired at runtime"
//
// A config this package reports as valid is guaranteed to satisfy the
// grammar and semantic rules this package implements. It is NOT a
// guarantee that every field it accepts is actually consumed by the AMP
// runtime loader. Known divergences, as of Phase 5 (fix round 1):
//
//   - Receiver types: OpsGenieConfigs, VictorOpsConfigs, and WeChatConfigs
//     validate here (internal/alertmanager/config.Receiver models them)
//     but have no runtime wiring at all - the runtime receiver type
//     (internal/infrastructure/routing.Receiver) only supports Webhook,
//     PagerDuty, Slack, Email, and Telegram configs. A config using only
//     an OpsGenie/VictorOps/WeChat receiver will validate clean here and
//     then send zero notifications at runtime.
//   - (Fixed, task 5.4): TelegramConfigs used to have no matching field on
//     internal/alertmanager/config.Receiver, so hasAnyIntegration() (the
//     E024 "no integrations configured" check) rejected a telegram-only
//     receiver the runtime would happily serve. Receiver now carries a
//     minimal TelegramConfig (see config.go) mirroring the runtime-parity
//     fields, and hasAnyIntegration() checks it - a telegram-only receiver
//     validates clean, matching runtime support.
//   - Inhibition matchers-list syntax: InhibitRule.SourceMatchers/
//     TargetMatchers validate for syntax (and get a W155 warning when
//     present) but internal/infrastructure/inhibition.InhibitionRule has
//     no such fields, so the runtime loader silently drops them. Only
//     source_match/source_match_re (target_match/target_match_re) are
//     required here for E150/E151, matching what the runtime actually
//     loads.
//   - time_intervals / mute_time_intervals (top-level) and route-level
//     mute_time_intervals / active_time_intervals: accepted structurally
//     (an Info note, code I001, is added when present) but not resolved
//     against each other, and not yet consumed by any runtime component -
//     this is a planned-but-not-yet-built AMP feature, not a divergence
//     from an existing runtime behavior.
//   - Route tree depth limit (E101) is sourced directly from
//     internal/infrastructure/routing.MaxRouteDepth, so that one no
//     longer diverges (fixed in Phase 5 review round 1); listed here as a
//     reminder that any other constant borrowed from a runtime package
//     should be referenced, not copied, for exactly this reason.
//
// Task 5.4 wires this package into internal/config.LoadConfig (and,
// transitively, the server's /-/reload path, which calls LoadConfig on
// every reload) - see internal/config/alertmanager_validation.go. The
// check only runs when the config file has a top-level `route:` section
// (the same gate internal/config.loadRouteConfig already uses for
// infrastructure/routing.Parse()), so it does not fire for legacy
// single-receiver configs that never adopted the route tree. Errors
// (E-codes) reject the load/reload with details; warnings (W-codes) are
// logged, not blocking.
package configvalidator
