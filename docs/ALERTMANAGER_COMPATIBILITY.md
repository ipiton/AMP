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
| `telegram_configs` | ✅ | ✅ | 🟢 Supported | Config side fixed in Task 5.4 (`internal/alertmanager/config.Receiver` carries a `TelegramConfig`, `hasAnyIntegration()`/E024 check it). Runtime side fixed in the final fix wave: the publishing queue built publishers with `CreatePublisher(type)`, which cannot pass per-target credentials, so `EnhancedTelegramPublisher` (bot token, `chat_id`, `message_thread_id`, Bot API `sendMessage`) was **unreachable at runtime** despite being implemented. Integration types now go through `CreatePublisherForTarget` — see `createPublisherForJob` in `internal/infrastructure/publishing/queue.go`. Wave 2: added a per-chat rate limiter (bounded LRU, cap 1000, 1 msg/s burst 3) waited on before the existing global 30/s limiter in `SendMessage`, so an alert storm to one chat no longer trips Telegram's per-chat 429s (`internal/infrastructure/publishing/telegram_client.go`). |
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
