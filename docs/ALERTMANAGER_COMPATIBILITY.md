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
| Regex matcher anchoring (`^(?:re)$`) | 🟢 Supported | `internal/business/routing/matcher.go` `anchorRegex` — matches upstream; unanchored substring matching would be a parity bug (fixed here). |
| `route:`/`receivers:` config parsing | 🟢 Supported | `internal/infrastructure/routing.Parse()`, gated on a top-level `route:` section existing (`internal/config.loadRouteConfig`); legacy single-receiver configs skip this path entirely. |
| `receivers[].*_configs` **auto-provisioning delivery** | 🔴 Not implemented — control-plane only | **Read this before migrating.** AMP parses and validates every receiver's integration blocks, and routes alerts to a receiver *by name*, but it does NOT build delivery endpoints from them. Delivery targets come exclusively from Kubernetes Secrets scoped with the `amp.receiver` annotation/label (`internal/business/publishing/discovery_parse.go`). See [Receiver Integration Compatibility Matrix](#receiver-integration-compatibility-matrix) and the migration note there. |
| Receiver-name charset | 🟢 Supported (one reserved character) | Any non-empty name except `/`, which is reserved as the group-key separator (`receiver=<name>/<group-key>`). Upstream has no restriction at all; dotted/colon/spaced/non-ASCII names (`team.dba`, `email:sre`, `ops team`) all work — they were rejected before the final fix wave. |
| Route evaluation wired into ingest | 🟢 Supported | `internal/core/services/alert_processor.go` `evaluateRoute` calls `RouteEvaluator.Evaluate` per alert when configured; nil (no-op) in lite/legacy mode. |
| Receiver-scoped delivery targets (`amp.receiver` annotation/label) | 🟢 Supported (AMP-native mechanism) | `internal/business/publishing/discovery_parse.go`: annotation = comma-separated list, label = single name fallback. Not an upstream concept — AMP's own target-discovery scoping, functionally equivalent. |
| **Grouping** by route's `group_by` | 🟢 Supported | `internal/core/services/alert_processor.go` `groupKeyFor` derives group keys from the `RoutingDecision.GroupBy` computed per alert. |
| Per-route `group_wait` / `group_interval` / `repeat_interval` | 🟢 Supported | Timings come from the matched route via `RoutingDecision`, not global defaults only — `internal/infrastructure/grouping/manager_impl.go`. |
| Notify-chain order: Inhibit → Silence → TimeMute → Dedup | 🟢 Supported | `manager_impl.go` `publishGroupAlerts`, explicitly ordered and commented as matching upstream's pipeline; send-time evaluation, not ingest-time. |
| One notification per group per fire | 🟢 Supported (logical); see gap note | `publishGroupAlerts` makes exactly one `publisher.PublishGroup` call per group per successful fire. **Wire-level caveat**: see [Known Gaps](#known-gaps-honesty-notes) — this is not yet one HTTP POST per target. |
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
| `inhibit_rules[].source_matchers` / `target_matchers` (`matchers:` list syntax) | 🔴 Not implemented, loudly | Only the `source_match`/`source_match_re`/`target_match`/`target_match_re` map form is evaluated. A rule using the list syntax is loaded but inhibits nothing — the config validator warns `W155`, and the running server now logs an `ERROR` naming the affected rule at load and on every reload. Rewrite such rules in the map form. |
| HA: Redis nflog + send-claim for cross-replica dedup | 🟢 Supported | `internal/infrastructure/grouping/redis_notify_log.go` (task 6.1). |
| HA: distributed timer liveness / reconciliation | 🟢 Supported | `internal/infrastructure/grouping/timer_manager_impl.go` + `manager_impl.go` reconciliation loop (task 6.2, hardened in fix round 1: targeted overdue-timer scan, no leftover-timer error-log spam). Final fix wave: the adoption window was effectively 0s (`reconciliation_grace` equalled the timer record's Redis TTL grace, so a timer became adoptable exactly as its key expired), and three early returns in `onTimerExpired` left a dead local handle that made reconciliation skip the group forever. Both fixed and covered by regression tests plus a live e2e scenario. |
| HA: silence cross-replica cache sync | 🟢 Supported | Redis pub/sub invalidation (task 6.3), `internal/infrastructure/silencing`. |
| HA: leader-elected silence GC | 🟢 Supported | `internal/infrastructure/lock/election.go` + `internal/business/silencing/manager_impl.go` (task 6.4). |
| HA: peer heartbeat + `cluster` status field | 🟢 Supported | `internal/infrastructure/cluster/heartbeat.go` (task 6.5). |
| HA: 2-replica end-to-end (exactly-once delivery, failover, in-flight adoption) | 🟡 Verified via standalone script, not CI-gated | `deploy/e2e-ha/` (`docker-compose.yml` + `run.sh`) exercises six steps, including a genuine cross-replica concurrent fire (replica B restarted after ingest so `RestoreTimers` arms a local timer for a group replica A also holds one for) and orphan adoption (replica A killed mid-`group_wait`, replica B's reconciliation loop must pick the timer up). Still **not** wired into `go test ./...` or any build tag — must be run explicitly. |
| Inhibition (`inhibit_rules:`) | 🟢 Supported | `internal/infrastructure/inhibition/` matcher/parser/cache, wired into the notify chain's Inhibit step and hot-reloadable. `GET /api/v2/inhibitions` (read-only, AMP-native diagnostic endpoint, not upstream API) reflects active inhibitions. |
| Config write API (`POST/PUT /api/v2/config*`) | 🔴 Not implemented | Explicitly out of scope for task 7.4 (see brief). |
| `/history*` API | 🔴 Not implemented | Explicitly out of scope for task 7.4. |

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

### Read this first: control plane vs data plane

AMP's receiver parity splits in two, and conflating them is the single
easiest way to mis-plan a migration:

- **Control plane — full parity.** `route:`, nested routes, matchers,
  `group_by`, `group_wait`/`group_interval`/`repeat_interval`,
  `mute_time_intervals`/`active_time_intervals`, `inhibit_rules:` and
  receiver *names* behave as upstream does. Your routing tree decides which
  **receiver name** an alert belongs to, exactly as upstream.
- **Data plane — different mechanism.** Upstream builds a delivery endpoint
  from each `receivers[].*_configs` block. **AMP does not.** No code path
  constructs a `PublishingTarget` from `routing.Receiver`; targets are
  discovered exclusively from Kubernetes Secrets, scoped to a receiver name
  by the `amp.receiver` annotation (comma-separated list) or label (single
  name) — `internal/business/publishing/discovery_parse.go`. The
  `*_configs` blocks are **parsed and validated, but never
  auto-provisioned**.

> **Migration requirement.** Copying an upstream `alertmanager.yml` into AMP
> gives you correct routing and **zero deliveries** until you create a
> Kubernetes Secret per delivery endpoint, each carrying
> `amp.receiver: <receiver-name>` matching the receiver names in your
> `receivers:` list. Budget for this step explicitly — the config will
> validate clean, start clean, and route clean without it.

The "Runtime wired" column below therefore means: *when a Secret-sourced
target of this type exists, the publishing factory has a working publisher
for it* — **not** that the `*_configs` block provisions anything.

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
| `telegram_configs` | ✅ | ✅ | 🟢 Supported | Config side fixed in Task 5.4 (`internal/alertmanager/config.Receiver` carries a `TelegramConfig`, `hasAnyIntegration()`/E024 check it). Runtime side fixed in the final fix wave: the publishing queue built publishers with `CreatePublisher(type)`, which cannot pass per-target credentials, so `EnhancedTelegramPublisher` (bot token, `chat_id`, `message_thread_id`, Bot API `sendMessage`) was **unreachable at runtime** despite being implemented. Integration types now go through `CreatePublisherForTarget` — see `createPublisherForJob` in `internal/infrastructure/publishing/queue.go` |
| Rootly (`alertmanager`/`rootly` target type) | N/A (AMP-native target, not an upstream receiver type) | ✅ | 🟢 Supported | AMP-specific addition, not part of upstream Alertmanager |
| `opsgenie_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E126-E129); no `OpsGenieConfigs` field on the runtime receiver and no publisher target type — zero notifications sent |
| `victorops_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E130-E134); same gap as OpsGenie. Deferred "по потребности" (on demand) — build only if a concrete need arises |
| `wechat_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E138-E141); same gap. Deferred "по потребности" |
| Discord (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | No dedicated `discord_configs`; ships as a built-in Discord-embed template (`internal/notification/template/defaults/webhook.go`) rendered through `webhook_configs`. Not a blocker |
| Microsoft Teams (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | Same pattern: built-in Adaptive Card template via `webhook_configs`, not a dedicated receiver type. Not a blocker |
| Pushover | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| AWS SNS | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| Webex | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |

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

1. **`receivers[].*_configs` are not delivery endpoints.** This is the biggest single divergence from upstream and
   it is a *design* difference, not a bug: AMP's data plane is Kubernetes-Secret-driven target discovery scoped by
   the `amp.receiver` annotation/label, while the `*_configs` blocks are parsed and validated only. A migrated
   `alertmanager.yml` routes correctly and delivers nothing until the matching Secrets exist. See
   [Read this first: control plane vs data plane](#read-this-first-control-plane-vs-data-plane). Building
   `PublishingTarget`s directly from `routing.Receiver` is a future epic, not a shipped feature.
   *(Previously this list claimed `GET /api/v2/alerts/groups` had a hardcoded receiver — that gap is now closed:
   group labels come from the matched route's `group_by` and the receiver is resolved per group from the live route
   tree. The remaining AMP-only behaviour there is the extra `?group_by=` query override.)*
2. **Notification is one *logical* group send, not one *wire-level* POST per group.** `publishGroupAlerts` makes
   exactly one `PublishGroup` call per group per fire, matching upstream's semantic grouping. But
   `PublishingCoordinator.PublishGroupToTargets` (`internal/infrastructure/publishing/coordinator.go`) then fans
   that out into one goroutine — and one outbound HTTP request — per `(target × alert)` pair, not upstream's single
   HTTP POST carrying an `alerts` JSON array per target. Functionally each alert is still delivered, but wire
   behavior (request count, webhook payload shape) differs from upstream and from what "one notification" implies.
   Tracked as a known deferred item (see gap-analysis doc).
3. **Repeat/group-interval notification continuation is implemented but not exhaustively proven under restart.**
   `PARITY-A1-NOTIFICATION-TRIGGERING` (group_wait/group_interval/repeat_interval firing) is closed, and task 6.2
   added a distributed-timer reconciliation loop specifically to recover in-flight timers after a replica restart
   or crash (commits `84df74f`, `dec49e7`). No commit in this branch's history is framed as fixing a
   context-cancellation-shaped "timer stops after first fire" bug specifically — if such a P0 was reported
   elsewhere, it is not separately identifiable in `git log` as of this writing. Treat repeat-notification
   continuation as implemented and hardened, but re-confirm empirically (e.g. amtool `silence add` + wait past
   `group_interval`, watch for the second notification) during the live-audit half of task 7.4 rather than as a
   fully closed loop backed by a long-duration regression test.
4. **`inhibited` query param on `/api/v2/alerts` is structurally present but currently a no-op.** Inhibition state
   isn't yet threaded into `alertconv.ToGettableAlert`'s `InhibitedBy` field, so filtering on it can't yet change
   results — the notify-chain's own Inhibit step (which actually suppresses notifications) is unaffected by this.
5. **2-replica HA e2e (`deploy/e2e-ha/`) is a standalone script, not a CI gate.** It demonstrates exactly-once
   delivery and failover when run manually (`./deploy/e2e-ha/run.sh`); it does not run in `go test ./...` and so
   provides no regression protection against a future change silently breaking HA behavior.

---

## Replacement Guidance

AMP's **control plane** (routing, grouping, dispatch, silences, inhibition, time-based muting, config validation, HA
clustering) mirrors upstream Alertmanager's mechanics, not just its API shape. Its **data plane** does not: delivery
endpoints come from `amp.receiver`-scoped Kubernetes Secrets, never from `receivers[].*_configs`. AMP is therefore
**not a config-level drop-in replacement** — it is a behaviour-level replacement that needs its delivery targets
provisioned separately.

Concretely, a migration is:
1. Copy your `route:` / `receivers:` / `inhibit_rules:` / `time_intervals:` across — semantics carry over.
2. Create one Kubernetes Secret per delivery endpoint, annotated
   `amp.receiver: <receiver-name>` to match the receiver names you just copied. **Without step 2 nothing is
   delivered**, and nothing in the config, the logs at startup, or the validator will tell you so.
3. Re-verify the items in "Still validate" below.

Treat AMP as a strong replacement candidate if you rely on:
- Prometheus-style alert ingestion with a real `route:`/`receivers:` routing tree (grouping, timing, matchers, time
  windows all evaluated per-alert)
- standard silence and inhibition management
- Grafana dashboard integration (via `/api/v2/alerts/groups`, now routing-tree-driven per group)
- HA/multi-replica deployment (Redis-backed dedup, leader election, peer heartbeat, in-flight timer adoption after
  a replica dies)
- standard health/readiness monitoring and hot configuration reload (`POST /-/reload` **and** `SIGHUP`)

Still validate before a blanket swap:
- that you have provisioned an `amp.receiver`-scoped Secret for every receiver you route to (step 2 above)
- exact wire-level webhook payload shape if a downstream integration depends on upstream's single alerts-array POST
- any receiver type not in the 🟢 rows of the receiver matrix above
- receiver names containing `/` — the one character AMP reserves (group-key separator); rename them
- config write API / `/history*` if your workflow depends on them (out of scope for this task)
- if you consume `GET /api/v2/status`'s `config.original`: it is the Alertmanager-shaped, secret-redacted subset,
  not a byte copy of your config file

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
