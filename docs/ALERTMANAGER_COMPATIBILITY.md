# Alertmanager API Compatibility Matrix

**Date**: 2026-08-18
**Status**: 🟢 **ROUTING/GROUPING/HA PARITY LANDED** (branch `feat/alertmanager-parity`) — final amtool live-audit half of task 7.4 still pending
**Alertmanager Version**: v0.27+ (API v2)
**Alertmanager++ Version**: v0.0.1

---

## Executive Summary

This revision replaces the 2026-03-09 "controlled replacement slice" baseline. Since then, `feat/alertmanager-parity`
landed a real routing tree, dispatcher/grouping semantics, mute/active time intervals, HA clustering, and config
validation wired into startup + `/-/reload`. AMP now implements the *mechanics* of upstream Alertmanager's core
pipeline (route matching → grouping → notify chain → dedup), not just a compatible-shaped API surface over a flat
alert store. The [Feature Parity Matrix](#feature-parity-matrix) below is the new top-level view; the endpoint and
receiver tables that follow it are kept for API-surface detail.

Every claim below is traceable to code on this branch. Where a claim only partially holds, the notes column says so
explicitly rather than rounding up. See [Known Gaps](#known-gaps-honesty-notes) for the sharp edges.

Source of truth:
- `go-app/internal/business/routing/` (route tree, matcher, evaluator)
- `go-app/internal/infrastructure/grouping/` (dispatcher, timers, notify chain)
- `go-app/internal/infrastructure/cluster/`, `go-app/internal/infrastructure/silencing/` (HA)
- `go-app/internal/application/router.go`, `go-app/internal/application/handlers/`
- `go-app/pkg/configvalidator/`

---

## Feature Parity Matrix

| Upstream feature | AMP status | Notes |
|---|---|---|
| **Routing tree** — recursive matcher, `continue`, nested routes | 🟢 Supported | `internal/business/routing/matcher.go` `RouteMatcher.FindMatchingRoutes`: recursive descent, children take precedence over parent, `continue: true` keeps evaluating siblings for multi-match. (Previously misattributed here to `evaluator.go`, which only wraps it.) |
| Matcher operators `=`, `!=`, `=~`, `!~` incl. `matchers:` list syntax | 🟢 Supported | `internal/business/routing/tree_builder.go` `parseMatchers`/`parseMatcherExpr` parse `match`, `match_re`, and the newer `matchers:` free-form syntax into all 4 operators. |
| Regex matcher anchoring (`^(?:re)$`) | 🟢 Supported | Anchored consistently across all four paths that evaluate a user-supplied regex matcher against a live alert: `internal/business/routing/matcher.go`'s `anchorRegex` (route `matchers:`/`match_re`); `internal/infrastructure/inhibition/matchers_list.go`'s `anchorMatcherRegex` (inhibit rule `matchers:`/`match_re`, wave-7 fix rounds 1-2); `internal/core/silencing/matcher_cache.go`'s `anchorSilenceRegex` (silence `=~`/`!~` evaluation itself, wave-7 fix round 2); and `internal/infrastructure/storage/memory/silence_store.go`'s `silenceMatchesLabels` — the live suppression path (notify-chain Step 2 `filterSilenced`, `status.silencedBy`, `?silenced=`) — which delegates to that SAME `core/silencing` evaluator rather than duplicating it a fourth time (wave-7 fix round 3, R6). Before round 3, silencing's own evaluator (backing `POST /api/v2/silences/check`) was fixed but this fourth path was not, so a silence on `job=~"prod"` still suppressed `job="preprod-2"` in production while the preview endpoint had already stopped agreeing — the two disagreed for exactly the shapes that matter. Unanchored substring matching would be a parity bug at any of the four; the three independent anchoring helpers (routing/inhibition/silencing) are each a duplicated copy of the same one-line helper, by design (see each site's own doc comment for why it isn't a shared import) — storage/memory intentionally has no fourth copy, by construction. `internal/infrastructure/routing`'s own `RouteConfigParser.compileRegexPatterns` (`config.CompiledRegex`) also anchors as of the same fix round, though nothing consumes that path in production today (see Known Gap #11 on the dead `RegexCache.Preload` wiring). |
| `route:`/`receivers:` config parsing | 🟢 Supported | `internal/infrastructure/routing.Parse()`, gated on a top-level `route:` section existing (`internal/config.loadRouteConfig`); legacy single-receiver configs skip this path entirely. |
| `receivers[].*_configs` **auto-provisioning delivery** | 🟢 Supported (AMP-PARITY-WAVE6-EPIC) | An untouched upstream config delivers with **zero Kubernetes Secrets**: every `webhook_configs`/`slack_configs`/`pagerduty_configs`/`telegram_configs`/`email_configs` block becomes an in-memory publishing target named `cfg:<receiver>/<type><idx>`, scoped to its receiver, rebuilt on every config load/reload (`internal/business/publishing/config_targets.go`). Secrets still work and are unchanged — the two sources merge into one view. See [Receiver Integration Compatibility Matrix](#receiver-integration-compatibility-matrix) for the per-field fidelity table. |
| Receiver-name charset | 🟢 Supported (one reserved character) | Any non-empty name except `/`, which is reserved as the group-key separator (`receiver=<name>/<group-key>`). Upstream has no restriction at all; dotted/colon/spaced/non-ASCII names (`team.dba`, `email:sre`, `ops team`) all work — they were rejected before the final fix wave. |
| Route evaluation wired into ingest | 🟢 Supported | `internal/core/services/alert_processor.go` `evaluateRoute` calls `RouteEvaluator.Evaluate` per alert when configured; nil (no-op) in lite/legacy mode. |
| Receiver-scoped delivery targets (`amp.receiver` annotation/label) | 🟢 Supported (AMP-native mechanism) | `internal/business/publishing/discovery_parse.go`: annotation = comma-separated list, label = single name fallback. Not an upstream concept — AMP's own target-discovery scoping, functionally equivalent. |
| **Grouping** by route's `group_by` | 🟢 Supported | `internal/core/services/alert_processor.go` `groupKeyFor` derives group keys from the `RoutingDecision.GroupBy` computed per alert. A route with NO `group_by` (at any level, and no `global.group_by`) follows upstream exactly: one group per receiver with an empty label set — AMP used to substitute `["alertname"]` there, fixed post-merge in wave 6. `group_by: ['...']` (group by all labels) is also supported. |
| Per-route `group_wait` / `group_interval` / `repeat_interval` | 🟢 Supported | Timings come from the matched route via `RoutingDecision`, not global defaults only — `internal/infrastructure/grouping/manager_impl.go`. |
| Notify-chain order: Inhibit → Silence → TimeMute → Dedup | 🟢 Supported | `manager_impl.go` `publishGroupAlerts`, explicitly ordered and commented as matching upstream's pipeline; send-time evaluation, not ingest-time. |
| One notification per group per fire, wire-level batching for webhook/alertmanager targets | 🟢 Supported | `publishGroupAlerts` makes exactly one `publisher.PublishGroup` call per group per successful fire. Webhook/alertmanager targets now deliver that as a single HTTP POST per `(group, target)` carrying an upstream-v4-shaped `alerts` JSON array (`BatchAlertPublisher.PublishBatch` / `GroupAlertFormatter.FormatGroup`, `internal/infrastructure/publishing/{publisher,formatter}.go`), with `groupLabels` resolved from the route's `group_by`. Publishers without a batch wire shape (Slack, Telegram, PagerDuty, Email) still get one job per target and iterate `Publish` once per alert within it. **Enqueue-vs-delivery caveat**: see [Known Gaps](#known-gaps-honesty-notes) #2. |
| Cross-replica notification dedup | 🟢 Supported | Redis-backed `notifyLog.TryClaim` + `IsDuplicate` (task 6.1); in-memory fallback is a same-process-only no-op claim. Fail-open on Redis errors (documented trade-off). |
| `time_intervals:` / `mute_time_intervals:` / `active_time_intervals:` | 🟢 Supported | `internal/infrastructure/routing/timeinterval/` + `upstream_fixtures_test.go` — ported upstream's own fixture corpus. Route-level names resolved via `routeTreeTimeIntervalLookup` at send time (whole-group suppression, not per-alert). |
| `GET /api/v2/alerts` upstream query params (`active`/`silenced`/`inhibited`/`unprocessed`/`receiver`/`filter`) | 🟢 Supported | `internal/application/handlers/alerts.go` `parseAlertStateFilters`/`parseReceiverFilter`; legacy `status=`/`resolved=` kept as aliases. `inhibited` param is structurally implemented but currently a no-op — inhibition isn't wired into `alertconv.ToGettableAlert`'s `InhibitedBy` yet. |
| `GET /api/v2/alerts/groups` upstream params | 🟢 Supported | Same state/receiver filters as above. Group labels come from the matched route's `group_by` and the receiver from the live route tree, per group (`alertGroupingResolver` in `internal/application/handlers/alerts.go`). AMP additionally accepts a `?group_by=` override, which upstream does not have. |
| `GET /api/v2/status` nested `versionInfo` + `cluster` | 🟢 Supported | `internal/application/handlers/status_api.go`; `versionInfo` from ldflags-injected build vars, `cluster` from the Redis-heartbeat `ClusterStatus`. |
| `GET /api/v2/status` `config.original` | 🟢 Supported, **secret-redacted** | Emits the Alertmanager-shaped section only (`route`/`receivers`/`inhibit_rules`/`time_intervals`/`global`), so `amtool config routes show` can re-parse it, with every secret-named field replaced by upstream's own `<secret>` placeholder (`AlertmanagerConfigYAML`). It is deliberately **not** the raw config file: this endpoint is unauthenticated, and the AMP config file also holds database/Redis/LLM credentials that are never exposed here. |
| `POST /api/v1/alerts` alias | 🟢 Supported (POST only) | `internal/application/handlers/alerts.go` `V1AlertsHandler` delegates straight into the v2 ingest path (same payload shape). GET intentionally not restored — different legacy DTO, out of scope. |
| `--web.route-prefix` / external-URL path inheritance | 🟢 Supported | `internal/application/route_prefix.go` `ResolveRoutePrefix` — explicit prefix wins; otherwise inherits from `external_url`'s path, matching upstream's own fallback rule. |
| Config validation (E-codes/W-warnings) wired into startup + `/-/reload` | 🟢 Supported | `pkg/configvalidator` wired into `internal/config.LoadConfig`, used by both process start and `/-/reload` (same function). Only fires for configs with a top-level `route:` section. |
| Hot reload triggers: `SIGHUP` **and** `POST /-/reload` | 🟢 Supported | Both, as upstream. `SIGHUP` is handled by `watchReloadSignal` in `cmd/server/main.go`; it is repeatable and survives a failed reload (the previous config stays active). Routing-only edits (`route:`/`receivers:`/`time_intervals:` and nothing else) are applied — they were silently discarded before the final fix wave, with `/-/reload` still answering 200 OK. |
| Resolved notification sent once, then the group is dropped | 🟢 Supported | `pruneResolvedAlerts` in `internal/infrastructure/grouping/manager_impl.go` removes resolved alerts after a *confirmed* delivery, matching upstream `aggrGroup.flush`; the group is deleted (and its timers cancelled) once that empties it. Alerts suppressed by inhibition/silence are kept, since they were never announced. |
| `inhibit_rules[].source_matchers` / `target_matchers` (`matchers:` list syntax) | 🟢 Supported | Wave 7 (`FU-INHIBIT-MATCHERS`): the inhibition engine (`internal/infrastructure/inhibition`) parses and evaluates the matchers-form list, reusing the wave-5 upstream-verbatim grammar port (`pkg/configvalidator/matcher.Parse`) and anchoring `=~`/`!~` the same way `internal/business/routing` does (`^(?:pattern)$`). Coexists with the legacy `source_match`/`source_match_re`/`target_match`/`target_match_re` maps on the same rule (both forms AND together, matching upstream), and applies on hot-reload like legacy rules. **Fix round 1** (first review round found the initial cut REJECT-worthy) closed: the config-loader bridge that dropped these fields before `pkg/configvalidator` ever saw them (`route:`-gated configs refused to load at all); upstream's `excludeTwoSidedMatch` mutual-inhibition guard (two alerts each matching both sides no longer silently mute each other); absent-label evaluation now matches upstream's no-presence-check semantics exactly for every operator (`=`/`!=`/`=~`/`!~`); `equal:` treats a label absent on both alerts as equal; and legacy `*_match_re` is now anchored the same as the matchers-form `=~`/`!~` (also fixed: an inline `source_match_re`/`target_match_re` rule, with no matchers-form fields at all, used to never get its regex compiled at all - a pre-existing, silent no-op bug the fix round closed in the same pass). See Known Gap #9 for one narrower divergence left open (label-name charset). |
| HA: Redis nflog + send-claim for cross-replica dedup | 🟢 Supported | `internal/infrastructure/grouping/redis_notify_log.go` (task 6.1). |
| HA: distributed timer liveness / reconciliation | 🟢 Supported | `internal/infrastructure/grouping/timer_manager_impl.go` + `manager_impl.go` reconciliation loop (task 6.2, hardened in fix round 1: targeted overdue-timer scan, no leftover-timer error-log spam). Final fix wave: the adoption window was effectively 0s (`reconciliation_grace` equalled the timer record's Redis TTL grace, so a timer became adoptable exactly as its key expired), and three early returns in `onTimerExpired` left a dead local handle that made reconciliation skip the group forever. Both fixed and covered by regression tests plus a live e2e scenario. |
| HA: silence cross-replica cache sync | 🟢 Supported | Redis pub/sub invalidation (task 6.3), `internal/infrastructure/silencing`. |
| HA: leader-elected silence GC | 🟢 Supported | `internal/infrastructure/lock/election.go` + `internal/business/silencing/manager_impl.go` (task 6.4). |
| HA: peer heartbeat + `cluster` status field | 🟢 Supported | `internal/infrastructure/cluster/heartbeat.go` (task 6.5). |
| HA: 2-replica end-to-end (exactly-once delivery, failover, in-flight adoption) | 🟡 Verified via standalone script, not CI-gated | `deploy/e2e-ha/` (`docker-compose.yml` + `run.sh`) exercises six steps, including a genuine cross-replica concurrent fire (replica B restarted after ingest so `RestoreTimers` arms a local timer for a group replica A also holds one for) and orphan adoption (replica A killed mid-`group_wait`, replica B's reconciliation loop must pick the timer up). Still **not** wired into `go test ./...` or any build tag — must be run explicitly. |
| Inhibition (`inhibit_rules:`) | 🟢 Supported | `internal/infrastructure/inhibition/` matcher/parser/cache, wired into the notify chain's Inhibit step and hot-reloadable. `GET /api/v2/inhibitions` (read-only, AMP-native diagnostic endpoint, not upstream API) reflects active inhibitions. |
| Config write API (`POST/PUT /api/v2/config*`) | 🔴 Not implemented | Explicitly out of scope for task 7.4 (see brief). |
| `/history*` API | 🔴 Not implemented | Explicitly out of scope for task 7.4. |
| **Lite profile restart durability** (`--storage.path` equivalent) | 🟢 Supported, opt-in | `storage.path` + `storage.snapshot_interval` (wave 6, `FU-LITE-FILE-SNAPSHOT`, `internal/infrastructure/snapshot/`) — silences (`memory.SilenceStore`) and the notification log's dedup entries + per-target delivered-state (`grouping.notifyDedupLog`) are written atomically (tmp file + rename + fsync, mode 0600) to `storage.path` on a periodic timer and on graceful shutdown, and reloaded at startup before the HTTP server starts serving. Format is plain versioned JSON (stdlib `encoding/json`), not upstream's protobuf+snappy — deliberately kept simple. TTL semantics are respected at load: an entry/delivered-state whose freshness window has already elapsed (computed from the snapshot's own timestamps vs now) is dropped, not resurrected. **Opt-in, unlike upstream**: `storage.path` defaults to empty (snapshotting OFF), not upstream's `data/` — an AMP upgrade must never start writing files as a side effect. **File, not directory**: upstream's `--storage.path` is a directory holding separate `nflog`/`silences` files; AMP's `storage.path` names a single combined snapshot file — point it at a file path, not a directory, when migrating a runbook from upstream. Standard profile: Postgres (`PostgresSilenceRepository`)/Redis (`RedisNotifyLog`) already own durability; setting `storage.path` there is logged and ignored, not wired in. Groups/alerts are NOT snapshotted, matching upstream — alerts re-arrive via Prometheus's resend behavior and groups rebuild from there. |

---

## Current Active Runtime Surface

| Endpoint | Alertmanager | Alertmanager++ today | Status | Notes |
|----------|--------------|----------------------|--------|-------|
| `GET /api/v2/alerts` | ✅ | ✅ | 🟢 | Full upstream query params: `active`/`silenced`/`inhibited`/`unprocessed`/`receiver`/`filter`, plus legacy `status=`/`resolved=` aliases |
| `POST /api/v2/alerts` | ✅ | ✅ | 🟢 | Ingest path wired through `AlertProcessor` → routing tree → grouping/dispatcher |
| `POST /api/v1/alerts` | ✅ (deprecated upstream) | ✅ | 🟢 | Alias into the v2 ingest path (POST only; GET not restored — different legacy DTO) |
| `GET /api/v2/alerts/groups` | ✅ | ✅ | 🟢 | Group labels from the matched route's `group_by`, receiver resolved per group from the live route tree; same state/receiver filters as `/alerts`, plus an AMP-only `?group_by=` override |
| `GET /api/v2/silences` | ✅ | ✅ | 🟢 | |
| `POST /api/v2/silences` | ✅ | ✅ | 🟢 | Create/update |
| `GET /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | |
| `DELETE /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | |
| `GET /api/v2/status` | ✅ | ✅ | 🟢 | Nested `versionInfo` (ldflags-injected) + `cluster` (Redis heartbeat) |
| `GET /api/v2/receivers` | ✅ | ✅ | 🟢 | |
| `GET /api/v2/inhibitions` | ❌ (no upstream equivalent) | ✅ | 🟢 | AMP-native diagnostic endpoint over the active inhibition state |
| `POST /-/reload` | ✅ | ✅ | 🟢 | Hot reload; runs the same `LoadConfig` path (incl. configvalidator) as startup |
| `GET /health`, `GET /healthz`, `GET /ready`, `GET /readyz` | N/A | ✅ | 🟢 | State-aware health/readiness routes |
| `GET /-/healthy`, `GET /-/ready` | ✅ | ✅ | 🟢 | Alertmanager-style liveness/readiness routes |
| `GET /metrics` | ✅ | ✅ | 🟢 | |
| `--web.route-prefix` | ✅ | ✅ | 🟢 | Explicit prefix, or inherited from `external_url`'s path when unset |

---

## Not Current Active Runtime Surface

The endpoints below are still **not** implemented (explicitly out of scope for task 7.4 per the parity plan):

| Endpoint / Surface | Current status | Notes |
|--------------------|----------------|-------|
| `GET/POST /api/v2/config*` | Backlog | Full config management/write API |
| `GET /history*` | Backlog | Extended alert history API |
| `GET/POST /api/v2/inhibition/*` (upstream write API) | Backlog | AMP has a read-only `/api/v2/inhibitions` diagnostic instead |
| `GET /api/v2/classification/*` | N/A | AMP-specific investigation feature, not an upstream API — not a parity gap |
| wider dashboard/UI surface | Partial | Work in progress, not part of Alertmanager API parity |

---

## Method Matrix For Current Slice

| Endpoint | Allowed methods | Notes |
|----------|------------------|-------|
| `/api/v2/alerts` | `GET`, `POST` | |
| `/api/v1/alerts` | `POST` | Alias |
| `/api/v2/alerts/groups` | `GET` | |
| `/api/v2/silences` | `GET`, `POST` | |
| `/api/v2/silence/{id}` | `GET`, `DELETE` | |
| `/api/v2/status` | `GET` | |
| `/api/v2/receivers` | `GET` | |
| `/api/v2/inhibitions` | `GET` | |
| `/-/reload` | `POST` | |
| `/health`, `/healthz`, `/ready`, `/readyz` | `GET` | |
| `/-/healthy`, `/-/ready` | `GET` | |
| `/metrics` | `GET` | |

---

## Receiver Integration Compatibility Matrix

### Read this first: two target sources, one view

Since **AMP-PARITY-WAVE6-EPIC** (`FU-RECEIVERS-INTEGRATION`) receivers are a
data plane, not only a control plane:

- **Control plane — full parity.** `route:`, nested routes, matchers,
  `group_by`, `group_wait`/`group_interval`/`repeat_interval`,
  `mute_time_intervals`/`active_time_intervals`, `inhibit_rules:` and
  receiver *names* behave as upstream does.
- **Data plane — two sources, merged.** Every integration block of every
  receiver is auto-provisioned into a `core.PublishingTarget`
  (`internal/business/publishing/config_targets.go`), and Kubernetes-Secret
  targets keep working exactly as before
  (`internal/business/publishing/discovery_parse.go`). Both land in the SAME
  discovery view — `ListTargets`/`GetTarget`/`GetTargetsByType` return the
  union — and the publishing layer is untouched by the change.

Config-provisioned targets:

| Property | Behaviour |
|---|---|
| Name | `cfg:<receiver>/<type><idx>` (e.g. `cfg:team-x/slack0`). The `cfg:` prefix cannot collide with a Secret-sourced target — those are DNS-1123 validated, so `:` and `/` are illegal there. No precedence rules exist because no collision can. |
| Receiver scoping | Always exactly `[<receiver name>]`, by construction. An empty `Receivers` list still means "all receivers" and remains reserved for legacy unscoped Secrets. |
| Lifecycle | In memory only, never written to Kubernetes. Rebuilt from scratch on every config load and on every reload whose routing fingerprint changed (a receivers-only edit counts) — the swap is atomic, so a reload never exposes a window with zero targets. |
| Ordering | Deterministic: receivers in config order, and inside a receiver the types in the order webhook → slack → pagerduty → telegram → email, each in its own declared order. |
| Observability | `alert_history_publishing_discovery_targets_total{type,enabled,source}` (`source="config"` vs `"k8s"`) plus `targets_by_source{...}`/`targets_config` on the publishing metrics collector. |
| Profiles | Works in the **lite** profile too: with receiver integrations present, a lite deployment runs the real publishing stack in config-only mode (no K8s client, no Secret discovery, no periodic refresh). |

> **Migration note.** Copying an upstream `alertmanager.yml` into AMP now
> routes AND delivers. Kubernetes Secrets are no longer required; they are
> still supported, still receiver-scoped via `amp.receiver`, and a Secret
> without that annotation/label still receives everything (backward
> compatibility). If you previously hand-created a Secret per integration,
> you can delete it once the same endpoint is declared in `receivers:` — but
> check for double delivery first: a `cfg:` target and an unscoped Secret
> target both match the same receiver, so both will fire.

### `global:` endpoint fallbacks

Upstream lets an integration omit its endpoint and inherit it from `global:`;
AMP resolves this at parse time, per-integration value always winning:

| Global key | Fills in | Behaviour when neither is set |
|---|---|---|
| `global.slack_api_url` | `slack_configs[].api_url` | **Load error** naming both places |
| `global.pagerduty_url` | `pagerduty_configs[].url` | Public Events API endpoint (`https://events.pagerduty.com`) |
| `global.telegram_api_url` | `telegram_configs[].api_url` | Public Bot API base (`https://api.telegram.org`) |
| `global.smtp_smarthost`, `global.smtp_from`, `global.smtp_auth_username`, `global.smtp_auth_password`, `global.smtp_require_tls` | the whole SMTP endpoint for `email_configs` | **Load error** — AMP models no per-`email_config` SMTP fields at all (see Known Gaps) |

### `send_resolved`

Supported per integration, upstream default `true`. `send_resolved: false`
suppresses RESOLVED notifications for that target only: it is recorded as
`filter_config.send_resolved` on the target and applied during target
resolution in `PublishingCoordinator`, so a suppressed notification never
enters the publishing queue at all (no job, no retry, no circuit-breaker
effect). Suppressions are counted as
`alert_history_publishing_resolved_suppressed_total{receiver}`.

A Kubernetes-Secret target can set the same key in its `filter_config` JSON.
Accepted false values: the boolean `false`, any string `strconv.ParseBool`
reads as false (`"false"`, `"FALSE"`, `"0"`, `"f"`), and the number `0`.
Anything else — including an unparseable string — means `true`, upstream's
default: an unreadable value must never silently suppress notifications.

A group whose remaining alerts are ALL resolved, for a receiver whose targets
all decline them, delivers nothing but still **settles**: the fire records
against a synthetic `suppressed:<receiver>` pseudo-target (never a real one, so
no real notification is suppressed) and the group's resolved alerts are pruned,
tearing the group down. Without that, the group would keep its resolved alerts
and re-arm its `repeat_interval` timer forever — one silent no-op fire per
interval, one undead group per key. Upstream reaches the same end state: its
retry stage filters the resolved alerts out, succeeds, and `aggrGroup.flush`
prunes.

### Blackhole receivers

A receiver with **no** integrations (upstream's classic `- name: 'null'`) is
valid and drops everything routed to it: the notification is treated as sent
(the group settles, no re-fire loop), counted as
`alert_history_publishing_blackhole_drops_total{receiver}` and logged at
Debug. A receiver whose ONLY integration is one AMP cannot deliver
(`opsgenie_configs`, `victorops_configs`, `wechat_configs`) behaves the same
way, and its unsupported blocks are named in a load-time WARNING. A route
pointing at a receiver the config does not declare remains a loud error.

`blackhole_drops_total` is a "is this receiver dropping" signal, not delivery
volume: it counts once per alert on the non-grouped path and once per group
fire (at most once per `repeat_interval` per group) on the grouped one.

### Matrix

"Config accepted" means
`internal/alertmanager/config.Receiver` (parsed by `pkg/configvalidator`)
has a matching field; "runtime wired" means
`internal/infrastructure/routing.Receiver` and the publishing factory
(`internal/infrastructure/publishing.PublisherFactory.CreatePublisher*`)
actually dispatch notifications for it. A receiver can validate cleanly
and still send zero notifications if it is config-accepted but not
runtime-wired — see `pkg/configvalidator/doc.go` for the authoritative
statement of this divergence, which this table mirrors. Note also that,
as of Task 5.4, `pkg/configvalidator` is wired into
`internal/config.LoadConfig` (and therefore both process startup and the
running server's `/-/reload` path, which reloads via the same function) —
but only when the config file has a top-level `route:` section, matching
`internal/config.loadRouteConfig`'s existing gate. "Config accepted" below
now reflects what the live server actually enforces for such configs; a
config with no `route:` section (legacy single-receiver mode) still skips
this validation.

| Integration | Config accepted | Runtime wired | Status | Notes |
|-------------|:---:|:---:|--------|-------|
| `webhook_configs` | ✅ | ✅ | 🟢 Supported | Generic HTTP POST; also the delivery path for Discord/Teams (see below) |
| `email_configs` | ✅ | ✅ | 🟢 Supported | SMTP, enhanced publisher |
| `pagerduty_configs` | ✅ | ✅ | 🟢 Supported | Events API v2 |
| `slack_configs` | ✅ | ✅ | 🟢 Supported | Incoming Webhooks, message threading cache |
| `telegram_configs` | ✅ | ✅ | 🟢 Supported | Config side fixed in Task 5.4 (`internal/alertmanager/config.Receiver` carries a `TelegramConfig`, `hasAnyIntegration()` checks it — the "no integrations" finding it feeds became the non-blocking `W024` in AMP-PARITY-WAVE6-EPIC). Runtime side fixed in the final fix wave: the publishing queue built publishers with `CreatePublisher(type)`, which cannot pass per-target credentials, so `EnhancedTelegramPublisher` (bot token, `chat_id`, `message_thread_id`, Bot API `sendMessage`) was **unreachable at runtime** despite being implemented. Integration types now go through `CreatePublisherForTarget` — see `createPublisherForJob` in `internal/infrastructure/publishing/queue.go`. Wave 2: added a per-chat rate limiter (bounded LRU, cap 1000, 1 msg/s burst 3) waited on before the existing global 30/s limiter in `SendMessage`, so an alert storm to one chat no longer trips Telegram's per-chat 429s (`internal/infrastructure/publishing/telegram_client.go`). |
| Rootly (`alertmanager`/`rootly` target type) | N/A (AMP-native target, not an upstream receiver type) | ✅ | 🟢 Supported | AMP-specific addition, not part of upstream Alertmanager |
| `opsgenie_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E126-E129); no `OpsGenieConfigs` field on the runtime receiver and no publisher target type — zero notifications sent |
| `victorops_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E130-E134); same gap as OpsGenie. Deferred "по потребности" (on demand) — build only if a concrete need arises |
| `wechat_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E138-E141); same gap. Deferred "по потребности" |
| Discord (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | No dedicated `discord_configs`; ships as a built-in Discord-embed template (`internal/notification/template/defaults/webhook.go`) rendered through `webhook_configs`. Not a blocker |
| Microsoft Teams (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | Same pattern: built-in Adaptive Card template via `webhook_configs`, not a dedicated receiver type. Not a blocker |
| Pushover | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| AWS SNS | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| Webex | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |

### Per-integration field fidelity

Auto-provisioning maps every field the publisher layer actually consumes. The
fields below are parsed and validated but **not** delivered — tracked as
`FU-INTEGRATION-FIELD-FIDELITY` in `docs/06-planning/BACKLOG.md`, and the
reason is the same for all of them: the enhanced publishers render their
message entirely through the shared `AlertFormatter` and read nothing else off
the target.

| Integration | Mapped (delivered) | Parsed but NOT delivered |
|---|---|---|
| `webhook_configs` | `url`, `http_headers`, `send_resolved` | `http_method` (always POST), `max_alerts`, `http_config` |
| `slack_configs` | `api_url`, `send_resolved` | `channel`, `username`, `icon_emoji`/`icon_url`, `title`, `title_link`, `pretext`, `text`, `fields`, `actions`, `color`, `short_fields`, `http_config` |
| `pagerduty_configs` | `routing_key` (or legacy `service_key`), `url`, `send_resolved` | `severity`, `class`, `component`, `group`, `description`, `details`, `http_config` — severity/summary are derived from the alert itself |
| `telegram_configs` | `bot_token`, `chat_id`, `message_thread_id`, `disable_notifications`, `api_url`, `send_resolved` | `parse_mode` (always the formatter's own), `message`, `http_config` |
| `email_configs` | `to`, `from`, `subject`, `html`, `text`, `send_resolved` + the global SMTP settings | `headers`; upstream's per-`email_config` `smarthost`/`auth_*`/`require_tls` are not modelled at all |

### Still NOT supported for receivers

- **`*_file` secret variants** (`api_key_file`, `bot_token_file`,
  `routing_key_file`, `auth_password_file`, …): not modelled by
  `internal/infrastructure/routing`, so an upstream config using them loads
  (the keys are dropped) and that integration provisions no credential. Use
  the inline value or an env-substituted value for now.
- **Per-integration `http_config`** (proxy, TLS, custom bearer/basic auth,
  `follow_redirects`): parsed and validated, never applied — every publisher
  uses its own HTTP client. Tracked (not started) as
  `FU-INTEGRATION-FIELD-FIDELITY` in `docs/06-planning/BACKLOG.md`.
- **Per-`email_config` SMTP settings**: see the fidelity table; only the
  `global.smtp_*` block reaches the SMTP dialer, so two receivers cannot use
  different SMTP servers. A config that sets SMTP per `email_config` and
  nothing globally fails to load rather than silently sending nothing.
- **`opsgenie_configs` / `victorops_configs` / `wechat_configs`**: config
  accepted, no publisher — a receiver carrying only these behaves as a
  blackhole and says so in a load-time WARNING.

**Decision (Task 7.2)**: VictorOps and WeChat publishers are not built in
this task. Their config types and validators (Phase 5) already exist and
are kept as-is; wiring a runtime publisher is deferred until a concrete
need appears ("по потребности"). Pushover/SNS/Webex are fixed as
unsupported and are not a blocker, since most third-party chat/incident
tools (including Discord and Teams) integrate through the generic
`webhook_configs` receiver instead of a bespoke native integration.

---

## Known Gaps (Honesty Notes)

These are the sharp edges behind the 🟡/🔴 markers above — stated plainly rather than buried in a footnote:

1. **`receivers[].*_configs` ARE delivery endpoints now — with field-level gaps.** Closed in
   AMP-PARITY-WAVE6-EPIC (`FU-RECEIVERS-INTEGRATION`): every integration block auto-provisions a publishing
   target, so a migrated `alertmanager.yml` delivers with zero Kubernetes Secrets, and Secret-sourced targets are
   unchanged and merge into the same view. What is still NOT upstream-equal is *field* fidelity, not existence —
   see [Per-integration field fidelity](#per-integration-field-fidelity) and
   [Still NOT supported for receivers](#still-not-supported-for-receivers): Slack channel/title/color, PagerDuty
   severity/class/details, Telegram `parse_mode`, per-integration `http_config` and all `*_file` secret variants
   are parsed and validated but not delivered. Two other honest edges: a receiver declared in the config with no
   deliverable integration silently drops (by design, counted), and if you keep an UNSCOPED legacy Secret target
   while also declaring the same endpoint in `receivers:`, both fire — one notification each.
   *(Previously this list claimed `GET /api/v2/alerts/groups` had a hardcoded receiver — that gap is now closed:
   group labels come from the matched route's `group_by` and the receiver is resolved per group from the live route
   tree. The remaining AMP-only behaviour there is the extra `?group_by=` query override.)*
2. **Per-target dedup now records on *confirmed delivery* — with a bounded confirmation wait.** Closed in wave 3
   (`FU-RECORDSENT-DELIVERY-CONFIRMATION`): `TargetPublishOutcome.Success`, and therefore `RecordSent`, is set only
   after the publisher's HTTP call for that `(group, target)` pair actually succeeded. `PublishGroupToTargets`
   submits each target's job with a completion channel and blocks on it, so a webhook returning 500 gets **no**
   nflog entry and is re-published on the group's next scheduled fire instead of going quiet until
   `repeat_interval`. Wire-level batching (one POST per `(group, target)` with an upstream-v4 `alerts` array) and
   per-`(group, receiver, target)` nflog keys are unchanged from wave 2.
   **The notify-fire time budget** is one derived chain, driven by a single knob,
   `publishing.queue.delivery_confirmation_timeout` (45s default):

   | Duration | Value at the default | Role |
   |---|---|---|
   | delivery-confirmation wait | 45s (max 2m) | per-target wait in `PublishGroupToTargets` |
   | timer-callback deadline | 60s (= wait + 15s) | bounds one whole notify fire (`TimerManagerConfig.CallbackTimeout`) |
   | cross-replica publish-claim TTL | 65s (= callback + 5s) | must cover the fire *and* its post-delivery bookkeeping (`GroupNotifyLog.TryClaim`) |
   | orphan-adoption grace | 90s (= claim + 25s) | a fire still delivering must never look abandoned (`grouping.reconciliation_grace`) |

   All three grouping-side values are derived from the wait at wiring time (`grouping.TimerCallbackTimeoutFor` /
   `NotifyLogClaimTTLFor` / `ReconciliationGraceFor`) and re-checked at startup
   (`ServiceRegistry.validateNotifyTimingBudget`) — AMP refuses to start on an inconsistent set, because every
   failure mode is invisible at runtime: a callback deadline below the wait silently truncates every delivery; a
   claim TTL below it lets the claim expire mid-publish and reopens the double-publish window; an adoption grace
   below it makes a live fire adoptable, and the adopting replica deletes the group's shared timer record while the
   publishing replica is still using it.

   Post-delivery bookkeeping (`RecordSent`, claim release, resolved-alert pruning) runs on a **detached** 5s context
   that is created *after* the delivery wait returns — a detached context built before the wait would have had its
   deadline consumed by the wait itself, which is the same failure in disguise.

   One relationship is deliberately *not* satisfied: the distributed per-group timer lock (`lockTTL`, 30s, never
   renewed) is shorter than a fire, so a long fire runs on with that lock expired. The publish claim, not the lock,
   is what stops a second replica notifying the same group in that window — it is load-bearing, not a backstop, and
   the startup log line reports both values.

   Residual sharp edges, all deliberate:
   - The wait is *shorter* than the queue's worst-case retry budget (~2min). A target still retrying past the
     deadline is reported as unconfirmed, so a delivery that succeeds afterwards is re-sent on the next fire — a
     duplicate notification, never a dropped one. The pipeline is at-least-once, same as upstream. Giving up also
     **abandons** the job (its context is cancelled), so one hanging endpoint cannot pin workers and starve healthy
     targets into false "unconfirmed" results.
   - After the `group_interval` fire, AMP's timer chain moves to `repeat_interval`, so an endpoint that is down for
     a long time gets one fast retry and then retries at `repeat_interval` cadence (upstream keeps flushing at
     `group_interval`). Independent of this fix; not tracked as a parity blocker.
   - The timer manager's own distributed lock (`lockTTL`, 30s, no renewal) can now expire mid-fire, so a second
     replica's timer for the same group may fire while the first is still publishing. The nflog publish claim — not
     that lock — is what prevents the double publish in that window; it went from backstop to load-bearing.
   - For integrations with no batch wire shape (Slack, Telegram, PagerDuty, Email) one job covers the group by
     looping `Publish` per alert, so a single failing alert still makes the whole `(group, target)` unconfirmed.
     Since wave 4 (`FU-PER-ALERT-OUTCOMES`) that no longer re-sends the alerts that already landed: the job reports
     which individual alerts the target accepted, and the chain stores that as per-`(group, target)`
     **delivered state** — `nflog:delivered:{groupKey}:{target}`, a Redis HASH `{alert fingerprint → delivered
     status}`, TTL = `repeat_interval` + 60s grace, capped at 500 alerts — so the next fire carries only the alerts
     still owed. Comparison is by `fingerprint:status`, the same atoms the nflog signature is built from, so an alert
     that flipped `firing`↔`resolved` counts as new and is re-sent, and a group that gained or lost alerts between
     fires still filters correctly. **One status per fingerprint**: recording an alert's new status *replaces* the
     previous one, because an alert flapping `firing→resolved→firing` must be delivered again — accumulating both
     statuses would suppress the re-fire, and suppression is the one outcome this design never accepts. Retries
     *inside* one job skip the already-accepted alerts too. When the remainder lands, the target gets its normal full
     nflog entry and the delivered state is deleted; it exists only while a target is partially delivered, so the
     happy path costs no extra key. Webhook/alertmanager targets are unaffected (one batched POST, one outcome, never
     any delivered state).
     Residual: this is an **AMP extension with no upstream counterpart** — upstream Alertmanager sends exactly one
     wire message per `group × integration` and has no per-alert send to be partial about. If the delivered state
     cannot be read or written (Redis unreachable, cap reached — the latter counted as
     `amp_group_operations_total{operation="delivered_state",status="capped"}`), AMP falls back to re-sending the
     whole set: the floor stays at-least-once, exactly-once per `(alert, target)` is the refinement that is
     forfeited. One exotic edge is not handled: the notify chain cannot tell which targets are batch-capable, so if a
     target's `type` is changed from a non-batch integration to `webhook`/`alertmanager` (same target name) *while*
     it has delivered state, the first batched POST after the change carries only the remainder and the full-signature
     nflog entry then suppresses the filtered alerts for one `repeat_interval`. Renaming the target, or letting the
     state expire before the type change, avoids it.
   - On queue shutdown, in-flight confirmed-delivery jobs are cancelled and deliberately **not** written to the DLQ:
     the notify chain re-publishes to any unconfirmed target after restart, so a DLQ replay would double-deliver.
3. **Repeat/group-interval notification continuation: P0 self-cancel bug found and fixed on this branch.**
   The original timer-continuation code cancelled its own context when arming the next interval timer
   (group_wait→group_interval transition), so repeat notifications never fired. Fixed in `c6cfadc` (contexts
   rooted in the manager lifetime, continuation-handle identity check) with red→green regression tests including
   a full wait→interval→repeat chain (`≥3` publishes asserted) and no-`context canceled` log assertions; hardened
   further in `985abb4`. Task 6.2's reconciliation loop (`84df74f`, `dec49e7`) covers replica-crash recovery.
4. **`inhibited` query param on `/api/v2/alerts` is structurally present but currently a no-op.** Inhibition state
   isn't yet threaded into `alertconv.ToGettableAlert`'s `InhibitedBy` field, so filtering on it can't yet change
   results — the notify-chain's own Inhibit step (which actually suppresses notifications) is unaffected by this.
5. **2-replica HA e2e (`deploy/e2e-ha/`) is a standalone script, not a CI gate.** It demonstrates exactly-once
   delivery and failover when run manually (`./deploy/e2e-ha/run.sh`); it does not run in `go test ./...` and so
   provides no regression protection against a future change silently breaking HA behavior.
6. **Group storage now survives a runtime Redis outage — but recovery is a clean cutover, not a state merge.**
   Closed in wave 5 (`FU-STORAGEMANAGER-FAILBACK`): the standard profile's `GroupStorage` used to be chosen once at
   startup (`ServiceRegistry.newGroupingStorage`) with no runtime revisit — a Redis outage after boot surfaced raw
   Redis errors on every group read/write until the process restarted. `grouping.StorageManager` (built in
   2025-11-04, never actually wired in until this task) now wraps it: a health probe on `Ping` switches to an
   in-memory fallback on loss and back on recovery, with a `backend_active` gauge (labeled `redis`/`memory`) and a
   loud log line on every switch, on top of the pre-existing per-call fallback (a failing `Store`/`Delete`/`StoreAll`
   switches immediately, without waiting for the next probe tick).
   **Fix round (same wave): hysteresis, error classification, and a write-through + deletion-replay reconciliation
   pass.** The first cut flipped on a single failed/succeeded `Ping` and on ANY per-call error — so a flapping Redis
   oscillated every probe tick, and one `ErrVersionMismatch` (an expected outcome of two concurrent updates) looked
   identical to Redis being down. Now: the probe requires `degradeThreshold` (default 3) consecutive failures
   before degrading, and BOTH `recoverThreshold` (default 3) consecutive successes AND `minHoldDuration` (default
   30s) since the last transition before failing forward; per-call `Store`/`Delete`/`StoreAll` only degrade for
   `isConnectivityError(err)` (a real transport/timeout failure) — any other error is returned to the caller
   unchanged, exactly as it would be with no wrapper at all. Separately, recovery used to flip `sm.current` back to
   primary with **no reconciliation whatsoever**, which had two silent-loss directions, only one of which the
   original text here admitted:
   - *(originally documented)* a group created fresh in the fallback during the outage was invisible to primary
     after the flip (`ErrNotFound`, a new group started — "duplicate notification possible, not silent data loss").
   - *(found in review, strictly worse)* for a group that already existed in Redis **before** the outage — the
     common case — the stale pre-outage copy resurfaced after the flip and every alert added during the outage was
     silently dropped on the next `Store`; and a `Delete` issued while degraded only ever reached the fallback, so
     the pre-outage Redis copy came back as a **zombie** group capable of firing a notification for alerts that had
     already resolved.

   `reconcileFallbackIntoPrimary` now runs before every failforward: every group in the fallback store is written
   through to primary (its `Version` aligned to primary's current value first, so `RedisGroupStorage.Store`'s
   optimistic-lock check doesn't reject the write-through as a spurious "concurrent update" — this actually makes
   the fallback's fresher copy win over the stale one), and every key deleted while degraded (tracked, bounded at
   500) is replayed as a `Delete` against primary. If the write-through itself fails, the flip is skipped for
   another probe cycle rather than failing forward onto a half-reconciled primary.

   **This is still not full state-merge machinery — three real limits remain, stated plainly:**
   - The write-through's read-then-write runs *without* the manager's lock held (holding it for the whole pass
     would block every `Store`/`Load`/`Delete` call for as long as reconciliation takes — an availability
     regression of its own). This leaves a narrow race: a write landing in the fallback *after* the write-through's
     snapshot of it but *before* the flip is not part of the write-through and can be shadowed once primary is
     current again — bounded by one reconciliation pass, not by the whole outage, but not zero.
   - **Multi-replica HA is NOT covered.** `MemoryGroupStorage` is per-process. If two replicas both degraded and
     both received writes for the SAME `GroupKey` during the outage (possible when a load balancer does not pin a
     given alert's requests to one replica), each replica's fallback holds a different view of that group, and
     whichever replica's reconciliation runs last simply overwrites the other's outage-window data — no merge
     between the two replicas' views. This is the same last-writer-wins limitation the brief already excludes full
     state-merge machinery for, now exercised concretely rather than left abstract; the fix above removes it for
     the single-writer-per-key case (one replica, or a load balancer pinning by `GroupKey`/alert), which is the
     common case, but does not extend to genuinely concurrent cross-replica writes during a shared outage.
   - This is NOT the same convergence story as wave 4's per-alert delivered state: that data is a TTL'd dedup marker
     where the worst case of "never made it to Redis" is a harmless resend, while a `GroupStorage` entry *is* the
     alert membership, so a residual gap here has real consequences, not just a delayed resend.
   - **Degrade detection latency roughly tripled for read-only workloads.** With `degradeThreshold`'s default of 3,
     the periodic probe now needs 3 consecutive failed `Ping`s (up to `3 × healthCheckInterval`, ~90s at the 30s
     default) before degrading, versus a single failed `Ping` before the hysteresis fix. `Load` has no per-call
     fallback (by design — see its own doc comment), so a read-heavy replica can surface raw Redis errors for up
     to that whole window instead of the previous ~30s. The trade-off (flap suppression vs. detection latency) is
     deliberate, not an oversight.

   **Fix round 2: the fallback is pruned after every successful reconcile, and deletion replay runs first.**
   The reconciliation pass above initially had two further defects, both reproduced and closed:
   - **A second outage could serve, and then overwrite, the first outage's stale data (finding C-1, Critical).**
     `reconcileFallbackIntoPrimary` wrote through everything `fallback.LoadAll()` returned but never removed
     anything from the fallback afterward — `MemoryGroupStorage` has no TTL/eviction of its own, so every outage's
     groups accumulated for the life of the process. A SECOND, later outage then (1) could serve a degraded `Load`
     from the FIRST outage's leftover instead of `ErrNotFound`, and (2) its own recovery would write-through that
     leftover too — its `Version` aligned to primary's current value on purpose, so the fresh Redis copy was
     silently replaced by stale data with no error, only the ordinary recovery log line. Reconciliation cost also
     grew with every outage, risking a permanently-blocked failforward if it ever exceeded `reconcileTimeout`.
     Fixed: every key written through is now `Delete`d from the fallback immediately after a successful
     write-through, so the next degraded window starts (almost) empty. One narrow exception survives: a write
     landing in the snapshot-to-flip window is not part of the reconciled snapshot, is therefore not pruned,
     and can carry into a future outage as a fallback leftover.
   - **A group deleted and re-created under the same `GroupKey` during ONE outage could be deleted again on
     recovery (finding I-5, Important).** Deletion replay ran *after* the write-through, so a stale "still
     deleted" entry for a key that a later `Store` had already re-created deleted the just-written-through, fresh
     group right back out of primary. Fixed: replay now runs *before* the write-through, and a successful
     `Store`/`StoreAll` of a key removes it from the pending-deletion list immediately, so the list only ever
     reflects what is genuinely still absent as of the most recent fallback write.
   - **A caller-cancelled or caller-timed-out context no longer looks like a Redis outage (finding I-6,
     Important).** `isConnectivityError` treated `context.Canceled`/`DeadlineExceeded` as connectivity failures
     unconditionally — but the alert-ingest path passes a request-scoped context all the way down to `Store`, so
     one client disconnect or client-side timeout degraded the WHOLE process to memory for roughly
     `minHoldDuration + recoverThreshold` probes (~2 minutes at the defaults), followed by a full reconcile, over
     an event that said nothing about Redis. Fixed: a cancellation/deadline only counts when the CALLER's own
     context is still live at the time of the check — meaning some OTHER, Redis-call-scoped deadline fired.

   `TimerStorage` is not wrapped by the same mechanism — it has its own, separate reconciliation loop (task 6.2,
   `grouping.TimerManagerConfig.ReconciliationInterval`) for distributed timer liveness, which this task did not
   extend.
7. **`global.group_by`/`group_wait`/`group_interval`/`repeat_interval` are an AMP-only convenience, not an
   upstream `global:` field.** Closed in wave 5 (`FU-GLOB-DEFAULT-VALUES`): `TreeBuilder.inheritGroupBy`/
   `inheritDuration` now consult `infraroute.GlobalConfig`'s matching field when neither the route itself nor any
   ancestor route set one, before falling back to the hardcoded upstream default (`["alertname"]` / 30s / 5m / 4h).
   Upstream Alertmanager's actual `global:` section carries no grouping fields at all — its equivalent mechanism is
   simply setting these on the root `route:`, which already cascades to every descendant via the same
   parent-chain inheritance this layer sits below. AMP had this fallback layer once (a pre-dedup, package-local
   `GlobalConfig`), lost it when that type was deleted in favor of the canonical `infrastructure/routing` one
   (`3f8d69d`, TN-137), and this task put it back on the canonical type rather than reintroducing the duplicate.
8. **`matchers:` list quote handling and grammar: aligned to upstream `pkg/labels`, not just internally consistent.**
   Closed in wave 5 (`FU-PARSEARGUMENT-QUOTE-HANDLING`): `pkg/configvalidator/matcher.Parse` never stripped quotes
   at all — for a regex matcher the quote-included literal was fed straight into `regexp.Compile`, so a config
   could pass/fail E-code validation based on a DIFFERENT compiled pattern than the one `business/routing.
   parseMatcherExpr` actually builds the runtime tree with, for the identical YAML `matchers:` entry.
   **Fix round (same wave): the first pass's quote handling was only internally consistent, not upstream-aligned —
   review found four verified divergences from `github.com/prometheus/alertmanager@v0.34.0/pkg/labels/parse.go`**
   (`ParseMatcher`): `\n` was never unescaped to a line feed; escaping was skipped entirely for an unquoted value
   (upstream applies `\n`/`\"`/`\\` unescaping regardless of quoting); an unescaped inner `"` was silently accepted
   instead of rejected; and an unterminated/mismatched quote failed OPEN — kept the literal, visibly-wrong value —
   instead of erroring, which is the wrong direction for a config validator to be silent in. Both parsers'
   `unquoteMatcherValue` are now a verbatim, independently-duplicated port of upstream's ~25-line loop (not a
   shared import — `pkg/` stays leaf-level), table-tested against the same cases in both packages, including a
   direct (non-regex-routed) invalid-UTF-8 check upstream's grammar also requires.
   **Also found in the same review pass and fixed: a quoted value containing an operator token** (`summary="a!=b"`)
   used to split *inside* the quotes in `pkg/configvalidator/matcher.Parse` (a plain `strings.Index` over the
   whole string) and hard-fail startup with a nonsensical E104, while `business/routing.parseMatcherExpr`'s
   anchored `^label(op)value$` regex parsed the identical YAML entry fine — the same divergence class the item was
   opened for, just not covered by the first pass's table. `Parse` now locates the operator with the same anchored
   shape, verified through a real `LoadConfig` regression test, not just a unit test of the parser in isolation.
   **Fix round 2: one more residual divergence closed, and license attribution added.** `label=` (nothing after
   the operator) still diverged — `parseMatcherExpr` accepted it (`Value: ""`), `Parse` rejected it with "value is
   empty", and upstream accepts it outright (`ParseMatcher`'s own doc comment: "The 3rd token may be the empty
   string"). The round-1 guard in `Parse` contradicted both parsers' "verbatim upstream grammar" claim; dropped so
   `label=` now parses the same way in both, matching upstream. Both `unquoteMatcherValue` ports also gained an
   explicit copyright/license attribution comment for the upstream file they port from (Apache-2.0 §4).
9. **A top-level `inhibit_rules:` key (upstream's own placement) is parsed and then silently ignored.**
   `internal/infrastructure/routing.RouteConfig.InhibitRules` accepts it, but no code path ever reads that field -
   the only inhibition rules that reach `internal/infrastructure/inhibition` are AMP's own
   `inhibition.inhibit_rules` wrapper (`internal/config.InhibitionConfig`, wired through `ToInhibitionRules`). A
   verbatim-upstream config that puts `inhibit_rules:` at the top level (not nested under `inhibition:`) loads
   cleanly and inhibits nothing, with no error and no warning; that `InhibitRule` type also lacks
   `source_matchers`/`target_matchers`, so those keys are dropped silently too. Found by review during the wave-7
   fix round (S2); deliberately **not** wired in that round - see `BACKLOG.md`'s `FU-TOPLEVEL-INHIBIT-RULES` line.
10. **AMP scans every firing alert as a candidate inhibitor; upstream v0.34's `sindex` keeps one representative per
    `equal:`-group.** `DefaultInhibitionMatcher.ShouldInhibit`/`FindInhibitors` iterate all firing alerts and test
    each against a rule; upstream's `InhibitRule.sindex` (`inhibit.go`) instead indexes source alerts by their
    `equal:`-label fingerprint and keeps only one (the one with the latest `EndsAt`) per fingerprint. Consequence
    (found by review, wave-7 fix round 2, R4): where a two-sided candidate (matches both source and target sides of
    a rule) and a separate pure-source candidate share the same `equal:`-group, AMP still inhibits via the
    pure-source candidate (`excludeTwoSidedMatch` only excludes the two-sided one), while upstream v0.34's index may
    have already been overwritten by whichever alert of the two arrived/re-fired last, so it does not always
    inhibit in that scenario. This is arguably in AMP's favour (it inhibits in a case upstream sometimes misses,
    never the reverse), and it matches how AMP already behaved for the legacy map form before wave 7 - not a new
    divergence introduced by the matchers-form work, just newly visible once `excludeTwoSidedMatch` (C2) made the
    two packages' inhibitor-selection strategies worth comparing directly. No code change; noted for honesty only.
11. **`internal/infrastructure/routing`'s `config.CompiledRegex` (route `match_re`) has no production consumer.**
    `RouteConfigParser.compileRegexPatterns` builds it, and it's now anchored (wave-7 fix round 3, R7), but nothing
    reads it at match time: production wires `businessrouting.NewRouteMatcher(nil, matcherOpts)`
    (`internal/application/service_registry.go`), so `RegexCache.Preload` is never called with this map, and the
    bridging function `matcher.go`'s own doc comment names (`ExtractCompiledPatterns(config)`) doesn't exist
    anywhere in the codebase. Harmless as dead code; the anchoring fix is defense-in-depth against a future
    `Preload` call poisoning `RouteMatcher`'s cache with an unanchored entry (the cache is keyed by raw pattern, the
    same way `RouteMatcher.regexMatch`'s own anchored cache-miss compile is). No behavior change for any running
    deployment - this doesn't route real traffic.

Wave 7 (`FU-INHIBIT-MATCHERS`) fix round 1 also closed four matchers-form-specific gaps a first review round found:
mutual inhibition between two alerts each matching both sides of a rule (ported upstream's `excludeTwoSidedMatch`
guard); `matchesAll`'s absent-label handling now matches upstream's no-presence-check semantics exactly (an absent
label reads as `""`, not as an automatic pass for `!=`/`!~` or an automatic fail for `=`/`=~`) - the same fix was
also required, and applied, to `internal/business/routing.MatchesNode`, which had copied the same divergence since
wave 5; `equal:` now treats a label absent on BOTH alerts as equal, matching upstream's fingerprint hashing; and
legacy `source_match_re`/`target_match_re` are now anchored `^(?:pattern)$` like the matchers-form `=~`/`!~`, not
left as an unanchored substring search. One narrower gap from that same review round is deliberately left open:
`pkg/configvalidator/matcher.Parse`'s label-name charset (`[a-zA-Z_][a-zA-Z0-9_]*`) is narrower than upstream's
classic-matcher grammar (which also allows `:`), so a `matchers:` entry using a colon in the label name now fails
config loading outright instead of the pre-fix-round behavior of logging an ineffective-rule warning. Left
unfixed this round because the charset is a two-parser-wide, multiply-tested grammar constant (shared with
`internal/business/routing.parseMatcherExpr` and pinned by wave-5's `FU-PARSEARGUMENT-QUOTE-HANDLING` test suite),
not something specific to inhibit rules - widening it belongs to its own reviewed change, not a drive-by inside
this fix round.

Fix round 2 (same wave, re-review "ACCEPT, residuals need one more round") found the absent-label fix (I1) had
only reached 2 of 3 tables that carried the divergence, and one more site outside FU7-A's original scope entirely:

- **`internal/core/silencing`'s own copy, R1 (fixed).** `DefaultSilenceMatcher.matchSingle` presence-gated the
  same way, AND `RegexCache.Get` compiled silence regexes **unanchored** - for silences both point the same
  direction, over-silencing (`job=~"prod"` silenced `job="preprod-2"` too). Both fixed the same way as the other
  two sites; see the anchoring matrix row above and `docs/06-planning/BACKLOG.md` for the full writeup. Two
  existing test assertions were upstream-incorrect as a direct result of the anchoring fix and are now inverted:
  `TestMatcherRegex_Anchors`'s `{"^start", "start-middle-end"}` and `{"end$", "start-middle-end"}` cases, both in
  `internal/core/silencing/matcher_test.go` - upstream's `labels.NewMatcher` unconditionally double-anchors even a
  pattern that already carries its own `^`/`$`, so "starts with"/"ends with" over a longer value never matched
  upstream in the first place; the test now documents that gotcha and adds the correct idiom (`^start.*`, `.*end$`)
  as new coverage.
- **Inhibition's own legacy map form, R2 (fixed).** I1's fix (fix round 1) reached `matchesAll` (the
  matchers-form) but not `ruleMatchesSourceSide`/`ruleMatchesTargetSide`'s inline handling of
  `source_match`/`source_match_re` (and the target twins) - upstream turns both legacy forms into
  `labels.Matcher{MatchEqual}`/`{MatchRegexp}` exactly like the matchers-form, so they get the identical
  absent-as-`""` treatment. I1 is complete in 3 of 3 tables only as of fix round 2.
- **R4, sindex strategy difference** - see Known Gap #10 above; documented, no code change.

Fix round 3 (same wave, re-review "ACCEPT for the inhibition work, R6 Important residual") found round 2's
silencing fix had landed in the wrong half of the silence stack:

- **R6 (Important, fixed).** `internal/core/silencing.DefaultSilenceMatcher` (fixed in round 2) backs
  `POST /api/v2/silences/check` only. The evaluator actually wired into the LIVE suppression path -
  `internal/infrastructure/storage/memory/silence_store.go`'s `silenceMatchesLabels`, read by notify-chain Step 2
  (`filterSilenced` -> `SilenceStore.HasActiveMatch`), `status.silencedBy`, and the `?silenced=` filter
  (`ActiveMatchingSilenceIDs`) - was a SECOND, independent evaluator that stayed unanchored through round 2. A
  silence on `job=~"prod"` kept suppressing `job="preprod-2"` in production even after the preview endpoint had
  already stopped agreeing - the two disagreed for exactly the shapes that matter. Fixed by routing
  `silenceMatchesLabels` through the SAME `*silencing.DefaultSilenceMatcher` instance rather than duplicating the
  fix a fourth time, so a fifth copy of this divergence class is now structurally impossible. See the anchoring
  matrix row above; regression coverage includes a preview-vs-pipeline agreement test (same silence, same alert,
  both evaluators asked directly, asserted to never disagree).
- **R7 (Minor, fixed).** `internal/infrastructure/routing`'s dead `config.CompiledRegex` path (see Known Gap #11)
  also compiled unanchored; anchored as defense-in-depth even though nothing consumes it in production today.
- **R8 (Minor, documented).** The inhibition engine's `hasRE` guard (fails closed, no match, when a `*_match_re`
  key has no compiled regex) now has an explicit comment naming `InhibitionRule.Compile()` as the sole legal
  construction path and warning that a future third construction path skipping it would silently reproduce S1.
- **R9 (Minor, fixed by this section's own rewrite).** The upgrade-impact bullet below used to claim silence
  regexes were uniformly anchored as of round 2 - true of the `core/silencing` evaluator alone, not yet of the
  live pipeline (that was exactly R6). It is accurate now that R6 has landed.

**Upgrade impact** (fix rounds 1-3 together change firing/suppression behavior on upgrade, not just "more
correct" in the abstract - operators should know before deploying):

1. Route matchers `!=`/`!~` against a label an alert doesn't carry now evaluate against `""` instead of
   auto-passing; a route relying on the old "missing label always satisfies `!=`/`!~`" behavior with an
   empty-string comparison value (e.g. `env!=""`, previously a no-op that matched everything) can now send an
   alert to a different receiver/branch than before.
2. Inline (non-`config_file`) `source_match_re`/`target_match_re` inhibit rules, previously a **silent no-op**
   (S1), now actually suppress notifications - an operator who added such a rule expecting it to work, gave up,
   and left it in the config will see it start working (as intended) but this is new suppression appearing with
   no config change on their end.
3. Regex anchoring tightened at every path that evaluates a user-supplied regex matcher against a live alert:
   routing (`match_re`/`matchers:`), inhibition (`*_match_re`/`matchers:`), and - as of fix round 3, covering both
   the preview endpoint AND the live notify-chain suppression path - silences (`=~`/`!~`). A route, inhibit rule,
   or **silence** whose regex was written assuming "contains" semantics may stop matching (or, for silences
   specifically, stop suppressing) after upgrade; the fix direction is correctness (upstream parity), but the
   practical effect for an existing deployment is "this used to match/suppress and now doesn't" until the pattern
   is rewritten with explicit `.*` wildcards. For silences specifically: this closes an over-silencing bug (a
   substring-style silence suppressing more than intended), so the practical effect for most operators is
   fewer/narrower silences taking effect than before, which is the safe direction to tighten in.

---

## Replacement Guidance

AMP's **control plane** (routing, grouping, dispatch, silences, inhibition, time-based muting, config validation, HA
clustering) mirrors upstream Alertmanager's mechanics, not just its API shape. Since AMP-PARITY-WAVE6-EPIC its
**data plane** follows the config too: `receivers[].*_configs` auto-provision delivery endpoints, so a copied
`alertmanager.yml` both routes and delivers. What is still not upstream-equal is per-integration FIELD fidelity
(Slack channel/title/color, PagerDuty severity/details, Telegram `parse_mode`, per-integration `http_config`,
`*_file` secret variants) — see [Per-integration field fidelity](#per-integration-field-fidelity).

Concretely, a migration is:
1. Copy your `route:` / `receivers:` / `inhibit_rules:` / `time_intervals:` / `global:` across — semantics carry
   over, and the receivers' integrations become live delivery targets on load. No Kubernetes Secrets required.
2. Check the field-fidelity table for anything you rely on that AMP parses but does not deliver (message
   formatting, PagerDuty categorisation, per-integration HTTP settings, `*_file` credentials).
3. If you are migrating FROM an earlier AMP that used `amp.receiver`-scoped Secrets: those keep working, but an
   endpoint declared in BOTH a Secret and `receivers:` now delivers twice. Delete the Secret, or scope it away.
4. Re-verify the items in "Still validate" below.

Treat AMP as a strong replacement candidate if you rely on:
- Prometheus-style alert ingestion with a real `route:`/`receivers:` routing tree (grouping, timing, matchers, time
  windows all evaluated per-alert)
- standard silence and inhibition management
- Grafana dashboard integration (via `/api/v2/alerts/groups`, now routing-tree-driven per group)
- HA/multi-replica deployment (Redis-backed dedup, leader election, peer heartbeat, in-flight timer adoption after
  a replica dies)
- standard health/readiness monitoring and hot configuration reload (`POST /-/reload` **and** `SIGHUP`)

Still validate before a blanket swap:
- that every field you depend on inside a `*_configs` block is in the "Mapped (delivered)" column of the
  field-fidelity table, not the "Parsed but NOT delivered" one
- exact wire-level webhook payload shape if a downstream integration depends on upstream's single alerts-array POST
- any receiver type not in the 🟢 rows of the receiver matrix above
- receiver names containing `/` — the one character AMP reserves (group-key separator); rename them
- config write API / `/history*` if your workflow depends on them (out of scope for this task)
- if you consume `GET /api/v2/status`'s `config.original`: it is the Alertmanager-shaped, secret-redacted subset,
  not a byte copy of your config file

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
