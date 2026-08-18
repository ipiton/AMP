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
| **Routing tree** — recursive matcher, `continue`, nested routes | 🟢 Supported | `internal/business/routing/evaluator.go` `FindMatchingRoutes`: recursive descent, children take precedence over parent, `continue: true` keeps evaluating siblings for multi-match. |
| Matcher operators `=`, `!=`, `=~`, `!~` incl. `matchers:` list syntax | 🟢 Supported | `internal/business/routing/tree_builder.go` `parseMatchers`/`parseMatcherExpr` parse `match`, `match_re`, and the newer `matchers:` free-form syntax into all 4 operators. |
| Regex matcher anchoring (`^(?:re)$`) | 🟢 Supported | `internal/business/routing/matcher.go` `anchorRegex` — matches upstream; unanchored substring matching would be a parity bug (fixed here). |
| `route:`/`receivers:` config parsing | 🟢 Supported | `internal/infrastructure/routing.Parse()`, gated on a top-level `route:` section existing (`internal/config.loadRouteConfig`); legacy single-receiver configs skip this path entirely. |
| Route evaluation wired into ingest | 🟢 Supported | `internal/core/services/alert_processor.go` `evaluateRoute` calls `RouteEvaluator.Evaluate` per alert when configured; nil (no-op) in lite/legacy mode. |
| Receiver-scoped delivery targets (`amp.receiver` annotation/label) | 🟢 Supported (AMP-native mechanism) | `internal/business/publishing/discovery_parse.go`: annotation = comma-separated list, label = single name fallback. Not an upstream concept — AMP's own target-discovery scoping, functionally equivalent. |
| **Grouping** by route's `group_by` | 🟢 Supported | `internal/core/services/alert_processor.go` `groupKeyFor` derives group keys from the `RoutingDecision.GroupBy` computed per alert. |
| Per-route `group_wait` / `group_interval` / `repeat_interval` | 🟢 Supported | Timings come from the matched route via `RoutingDecision`, not global defaults only — `internal/infrastructure/grouping/manager_impl.go`. |
| Notify-chain order: Inhibit → Silence → TimeMute → Dedup | 🟢 Supported | `manager_impl.go` `publishGroupAlerts`, explicitly ordered and commented as matching upstream's pipeline; send-time evaluation, not ingest-time. |
| One notification per group per fire | 🟢 Supported (logical); see gap note | `publishGroupAlerts` makes exactly one `publisher.PublishGroup` call per group per successful fire. **Wire-level caveat**: see [Known Gaps](#known-gaps-honesty-notes) — this is not yet one HTTP POST per target. |
| Cross-replica notification dedup | 🟢 Supported | Redis-backed `notifyLog.TryClaim` + `IsDuplicate` (task 6.1); in-memory fallback is a same-process-only no-op claim. Fail-open on Redis errors (documented trade-off). |
| `time_intervals:` / `mute_time_intervals:` / `active_time_intervals:` | 🟢 Supported | `internal/infrastructure/routing/timeinterval/` + `upstream_fixtures_test.go` — ported upstream's own fixture corpus. Route-level names resolved via `routeTreeTimeIntervalLookup` at send time (whole-group suppression, not per-alert). |
| `GET /api/v2/alerts` upstream query params (`active`/`silenced`/`inhibited`/`unprocessed`/`receiver`/`filter`) | 🟢 Supported | `internal/application/handlers/alerts.go` `parseAlertStateFilters`/`parseReceiverFilter`; legacy `status=`/`resolved=` kept as aliases. `inhibited` param is structurally implemented but currently a no-op — inhibition isn't wired into `alertconv.ToGettableAlert`'s `InhibitedBy` yet. |
| `GET /api/v2/alerts/groups` upstream params | 🟡 Partial | Same state/receiver filters as above, but **group→receiver assignment does not use the routing tree** — see [Known Gaps](#known-gaps-honesty-notes). |
| `GET /api/v2/status` nested `versionInfo` + `cluster` | 🟢 Supported | `internal/application/handlers/status_api.go`; `versionInfo` from ldflags-injected build vars, `cluster` from the Redis-heartbeat `ClusterStatus`. |
| `POST /api/v1/alerts` alias | 🟢 Supported (POST only) | `internal/application/handlers/alerts.go` `V1AlertsHandler` delegates straight into the v2 ingest path (same payload shape). GET intentionally not restored — different legacy DTO, out of scope. |
| `--web.route-prefix` / external-URL path inheritance | 🟢 Supported | `internal/application/route_prefix.go` `ResolveRoutePrefix` — explicit prefix wins; otherwise inherits from `external_url`'s path, matching upstream's own fallback rule. |
| Config validation (E-codes/W-warnings) wired into startup + `/-/reload` | 🟢 Supported | `pkg/configvalidator` wired into `internal/config.LoadConfig`, used by both process start and `/-/reload` (same function). Only fires for configs with a top-level `route:` section. |
| HA: Redis nflog + send-claim for cross-replica dedup | 🟢 Supported | `internal/infrastructure/grouping/redis_notify_log.go` (task 6.1). |
| HA: distributed timer liveness / reconciliation | 🟢 Supported | `internal/infrastructure/grouping/timer_manager_impl.go` + `manager_impl.go` reconciliation loop (task 6.2, hardened in fix round 1: targeted overdue-timer scan, no leftover-timer error-log spam). |
| HA: silence cross-replica cache sync | 🟢 Supported | Redis pub/sub invalidation (task 6.3), `internal/infrastructure/silencing`. |
| HA: leader-elected silence GC | 🟢 Supported | `internal/infrastructure/lock/election.go` + `internal/business/silencing/manager_impl.go` (task 6.4). |
| HA: peer heartbeat + `cluster` status field | 🟢 Supported | `internal/infrastructure/cluster/heartbeat.go` (task 6.5). |
| HA: 2-replica end-to-end (exactly-once delivery, failover) | 🟡 Verified via standalone script, not CI-gated | `deploy/e2e-ha/` (`docker-compose.yml` + `run.sh`, added in commit `ff6accc`) exercises this; it is **not** wired into `go test ./...` or any build tag — must be run explicitly. |
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
| `GET /api/v2/alerts/groups` | ✅ | ✅ | 🟡 | Supports `group_by` + the same state/receiver filters as `/alerts`, but receiver assignment is not routing-tree-driven — see Known Gaps |
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

This matrix covers `receivers[].*_configs` notification integrations, as
distinct from the HTTP API surface above. "Config accepted" means
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
| `telegram_configs` | ✅ | ✅ | 🟢 Supported | Fixed in Task 5.4: `internal/alertmanager/config.Receiver` now carries a minimal `TelegramConfig` and `hasAnyIntegration()`/E024 checks it, so a telegram-only receiver validates clean, matching `internal/infrastructure/routing.Receiver` and the publisher's existing runtime support |
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

1. **`GET /api/v2/alerts/groups` doesn't use the routing tree for receiver assignment.** The real ingest→notify
   pipeline (`AlertProcessor` → `RouteEvaluator` → grouping/dispatcher) *does* use the routing tree end-to-end.
   But this read-only query endpoint (`AlertGroupsHandler` in
   `internal/application/handlers/alerts.go`) groups over `memory.AlertStore` independently and assigns every
   group the first configured receiver (or `"default"`) — this was a known gap
   (`GROUPALERTS-HARDCODED-RECEIVER` in `docs/06-planning/BACKLOG.md`) closed by accepting this behavior, not by
   wiring the tree into the query path. A client reading `/api/v2/alerts/groups` to learn "which receiver will this
   alert notify" gets the wrong answer for any config with more than one receiver.
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

AMP's core notification pipeline (routing, grouping, dispatch, silences, inhibition, time-based muting, config
validation, HA clustering) now mirrors upstream Alertmanager's mechanics, not just its API shape. The remaining gaps
are at the edges: a handful of read-query endpoints not yet routing-tree-aware, wire-level webhook batching, and a
short list of niche receiver integrations (see the receiver matrix above and the gap-analysis doc for the full list).

Treat AMP as a strong replacement candidate if you rely on:
- Prometheus-style alert ingestion with a real `route:`/`receivers:` routing tree (grouping, timing, matchers, time
  windows all evaluated per-alert)
- standard silence and inhibition management
- Grafana dashboard integration (via `/api/v2/alerts/groups`) — but see gap #1 above if your config has more than
  one receiver
- HA/multi-replica deployment (Redis-backed dedup, leader election, peer heartbeat)
- standard health/readiness monitoring and hot configuration reload

Still validate before a blanket swap:
- exact wire-level webhook payload shape if a downstream integration depends on upstream's single alerts-array POST
- any receiver type not in the 🟢 rows of the receiver matrix below
- config write API / `/history*` if your workflow depends on them (out of scope for this task)

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
