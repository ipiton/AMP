# Changelog

All notable changes to Alertmanager++ (AMP) will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **AMP-PARITY-WAVE4** (2026-08-19): wave-4 follow-ups on top of the parity epic.
  - **Per-alert outcome tracking for non-batch publishers** (`FU-PER-ALERT-OUTCOMES`, the wave-3 M4 residual): a partial per-alert failure no longer re-sends the alerts that already landed. Slack/Telegram/PagerDuty/Email have no array-payload wire shape, so one group job loops `Publish` once per alert; before this, alert 3 of 5 failing made the whole `(group, target)` pair unconfirmed, no nflog entry was written, and the group's next fire re-sent all five — duplicating the four that had been delivered. Exactly-once per `(alert, target)` within a notification cycle is now the goal, with at-least-once still the floor.
    - **The job reports which alerts landed**: `PublishingQueue.publishJob` tracks accepted alerts per job and skips them when the same job is retried (previously every in-job retry resent the whole set), publishing an immutable snapshot readable through `GroupPublishHandle.DeliveredAlerts` *while the job is still running* — so even a job whose confirmation wait expired hands back the progress it made. `PublishingResult.DeliveredAlerts` / `grouping.TargetPublishOutcome.DeliveredAlerts` carry it up the chain, and are empty for confirmed targets (the full nflog entry supersedes them) and for batch targets (one atomic POST has no partial state).
    - **The chain stores it and narrows the retry**: `GroupNotifyLog` gains `DeliveredAlerts` / `RecordPartialDelivery`, backed by a new Redis key family `nflog:delivered:{groupKey}:{target}` (HASH `{alert fingerprint → delivered status}`, TTL = `repeat_interval` + 60s grace, capped at 500 alerts, additive across fires, written by a single Lua script so the cap check, the writes and the TTL are atomic) and by an expiring map in the in-memory log. `RecordSent` and `Forget` delete it. `publishGroupAlerts` filters each target's alert list against its delivered state (`alertsStillOwed`), so the retry fire carries only the alerts still owed — and the per-target `skipTarget func(string) bool` callback becomes `targetAlerts func(string, []*core.Alert) []*core.Alert`, whose empty result is the old "skip this target".
    - **Compared by `core.Alert.DeliveryKey` (`fingerprint:status`)**, hoisted out of `grouping.alertSetSignature` so the delivered state and the nflog signature are built from the same atoms: matching is never positional (a group legitimately gains and loses alerts between fires) and an alert that flipped `firing`↔`resolved` counts as new and is re-sent. **Stored one status per fingerprint**, so recording an alert's new status replaces the previous one — an alert flapping `firing→resolved→firing` must be delivered again, and keeping both statuses would have suppressed the re-fire for up to a `repeat_interval`. Cross-replica by construction, so a replica adopting the group mid-recovery re-sends the same remainder.
    - **Fails open in the resend direction**: a delivered-state read/write error, or hitting the cap, degrades to the pre-wave-4 behaviour (re-send everything) rather than suppressing an alert; cap refusals are counted as `amp_group_operations_total{operation="delivered_state",status="capped"}` so the reversion is visible. Upstream honesty: upstream Alertmanager has no per-alert send at all (one wire message per `group × integration`), so this whole mechanism is an AMP extension, documented as such in `docs/ALERTMANAGER_COMPATIBILITY.md`.
- **AMP-PARITY-WAVE3** (2026-08-19): wave-3 reliability follow-ups on top of the parity epic.
  - **`RecordSent` follows CONFIRMED delivery** (`FU-RECORDSENT-DELIVERY-CONFIRMATION`): a publishing-queue job can now carry a completion channel signalled exactly once with its final outcome (`PublishingQueue.SubmitGroupWithConfirmation`), and `PublishingCoordinator.PublishGroupToTargets` blocks on it per target — so `TargetPublishOutcome.Success`, and therefore the per-`(group, receiver, target)` nflog entry, means "the target's HTTP call succeeded" instead of "a job was enqueued". A webhook answering 500 after enqueue no longer gets a dedup entry and is re-published on the group's next scheduled fire instead of staying suppressed for a whole `repeat_interval`. Metrics-only mode, an open circuit breaker and a publisher that cannot be built all report `ErrDeliveryNotAttempted` (no entry, retried); a wait that exceeds the confirmation timeout reports `ErrDeliveryWaitTimeout`, is also treated as unconfirmed (keeping the pipeline at-least-once), and **abandons** the job so a hanging endpoint cannot pin workers.
    - **One time budget, one knob**: new `publishing.queue.delivery_confirmation_timeout` (45s default, 2m maximum) drives the per-target wait, and the grouping side derives its timer-callback deadline (60s), cross-replica publish-claim TTL (65s) and orphan-adoption grace (90s, was 20s) from it (`grouping.TimerCallbackTimeoutFor` / `NotifyLogClaimTTLFor` / `ReconciliationGraceFor`). `ServiceRegistry.validateNotifyTimingBudget` refuses to start on an inconsistent set and logs the whole budget, including the deliberately-shorter timer lock TTL. Previously these were independent literals and the shortest (a hardcoded 30s timer-callback context) silently capped every delivery; the too-short adoption grace additionally let a live fire be adopted, whose `DeleteTimer` raced the publisher's continuation `SaveTimer`.
    - **Post-delivery bookkeeping survives a long fire**: `RecordSent`, the claim release and resolved-alert pruning run on contexts detached from the fire's own, each created *after* the delivery wait returns (a detached context created before the wait has its deadline consumed by the wait — the same bug in disguise). `RedisNotifyLog.TryClaim` and `RedisTimerStorage.AcquireLock` release closures likewise derive their own contexts at release time instead of capturing the acquiring one.
    - **Abandonment is classified**: `GroupPublishHandle.Abandon(reason)` — a waiter-timeout abandonment counts as a circuit-breaker failure (so a target that *hangs*, and therefore never produces a failed job, still opens its breaker) and is exported as `jobsProcessedTotal{status="abandoned_unconfirmed"}`; shutdown-driven cancellation counts against nothing. Abandoned jobs are never written to the DLQ, since the notify chain re-publishes to unconfirmed targets.
    - **Per-group publish lock**: the notify chain's `publishGroupAlerts` lock is a refcounted per-`GroupKey` entry instead of a 256-way striped mutex — with a blocking delivery inside the critical section, two hash-colliding group keys would otherwise serialize for a full confirmation timeout.
    - Also fixes a pre-existing data race in `PublishingQueue.submitJob`, which called `JobTrackingStore.Add` (reading `job.State`/`job.StartedAt`) after handing the job to a worker.
- **AMP-PARITY-WAVE4** (2026-08-19): wave-4 hygiene tails from wave-3 review (track B — 6 independent items; see the epic's wave-4 track A entry for the rest of this wave).
  - **`AbandonReasonShutdown` is now reachable** (review finding M-b): `PublishGroupToTargets` distinguishes an explicitly-cancelled context (`context.Canceled` — shutdown, or a caller giving up for a reason unrelated to the target) from the caller's own bounded deadline expiring (`context.DeadlineExceeded`, still `AbandonReasonUnconfirmed`). Previously a real `SIGTERM` cancelling the grouping context made every in-flight target's abandonment default to `Unconfirmed`, tripping one spurious circuit-breaker failure per target on every shutdown.
  - **A genuine failure racing the confirmation-wait timeout keeps its DLQ entry** (review finding M-c): the queue's abandon-branch check now requires the job's own returned error to wrap `context.Canceled` (extracted into `jobWasAbandoned`), not merely that `job.ctx.Err() != nil` — a job whose final HTTP attempt settles (success, or a real 500/refused failure) at essentially the same instant the waiter's timeout fires no longer gets silently reclassified as "abandoned" and losing its DLQ write.
  - **`grouping.reconciliation_grace` derives from the actual delivery-confirmation timeout at wiring time** (review finding M-a): `service_registry.go`'s hardcoded viper `"90s"` default is gone; when the operator leaves the key unset, `ServiceRegistry.initializeGrouping` now derives it via `grouping.ReconciliationGraceFor(deliveryConfirmationTimeout)` using the real configured value, so raising `publishing.queue.delivery_confirmation_timeout` alone no longer fails startup until `reconciliation_grace` is also raised by hand. An explicit operator value still wins, and `validateNotifyTimingBudget` still guards the result.
  - **Layering + style nit** (review finding M-f): `MaxDeliveryConfirmationTimeout` moved from `infrastructure/publishing` to the `core` leaf package, so `internal/config` no longer imports an infrastructure package for one constant (`publishing.MaxDeliveryConfirmationTimeout` re-exports it for existing callers). `PublishingCoordinator.awaitDelivery` now returns `(settled bool, err error)` instead of `(err error, settled bool)` (ST1008: error-last).
  - **`DefaultHealthMonitor`'s `Stop()`-timeout reentrancy gap closed** (fu3-rel review, disclosed Minor #1): `Start()` now blocks on the previous worker generation's completion signal (`workerDone`, an `atomic.Pointer[chan struct{}]`) before spawning a new one, so a `Stop(timeout)` that returns `ErrShutdownTimeout` while the old worker is still draining can no longer result in two worker goroutines running concurrently.
  - **Test hygiene**: remaining `publishing` package tests that constructed a `PublisherFactory` without calling `Shutdown()` (leaking its Slack/PagerDuty/Rootly cache-cleanup goroutines) now do, via `t.Cleanup` — no production change.
- **AMP-PARITY** (2026-08-18): Alertmanager parity epic delivered on `feat/alertmanager-parity` — 7 phases (routing tree, grouping/publishing, time intervals, route prefix, config validation/reload, HA clustering, publishers/docs) plus two fix waves. AMP now implements the *mechanics* of upstream Alertmanager's pipeline (route matching → grouping → notify chain → dedup), not just a compatible API shape.
  - **Routing tree**: recursive matcher with `continue`, all 4 matcher operators (`=`, `!=`, `=~`, `!~`), `matchers:` list syntax alongside legacy `match`/`match_re`, anchored regex (`^(?:re)$`), `time_intervals:`/`mute_time_intervals:`/`active_time_intervals:` ported against upstream's own fixture corpus.
  - **Grouping/dispatch**: per-route `group_by`/`group_wait`/`group_interval`/`repeat_interval`, notify chain ordered Inhibit → Silence → TimeMute → Dedup, one logical `PublishGroup` call per group per fire.
  - **HA clustering**: Redis-backed cross-replica notification dedup (nflog + send-claim), distributed timer liveness/reconciliation, silence cache sync via Redis pub/sub, leader-elected silence GC, peer heartbeat + `cluster` status field. 2-replica e2e in `deploy/e2e-ha/` (manual script, not CI-gated).
  - **Config**: `pkg/configvalidator` (E-codes/W-warnings) wired into startup and `/-/reload`; hot reload via both `SIGHUP` and `POST /-/reload`, including routing-only edits.
  - **Publishers**: Telegram (bot API, per-target credentials via `CreatePublisherForTarget`), Email (SMTP), advanced alert/silence filtering, `--web.external-url` callback links.
  - **Wave 2 — wire-level group batching**: webhook/alertmanager targets now deliver one HTTP POST per `(group, target)` with an upstream-v4-shaped `alerts` JSON array (`BatchAlertPublisher.PublishBatch` / `GroupAlertFormatter.FormatGroup`), `groupLabels` resolved from the route's `group_by` against the group's shared label values. Publishers without a batch wire shape (Slack, Telegram, PagerDuty, Email) still get one job per target but iterate `Publish` once per alert within it.
  - **Wave 2 — per-target nflog dedup**: `GroupNotifyLog` keys move from `group:receiver` to `group:receiver:target`; a partial delivery failure now retries only the target(s) that didn't confirm, instead of the whole group. (Wave 2 limitation `TargetPublishOutcome.Success`/`RecordSent` == queue *enqueue*: closed in wave 3, see below.)
  - **Wave 2 — Telegram per-chat rate limiting**: a bounded (LRU, cap 1000), per-chat `rate.Limiter` (1 msg/s, burst 3) is waited on before the existing global 30/s limiter, preventing alert storms to one chat from tripping Telegram's per-chat 429s.
  - **Wave 2 — reliability fixes**: `LRUJobTrackingStore.Get` no longer races on `container/list` internals under `-race`; `DefaultRefreshManager`'s single-flight `RefreshNow()` slot is now acquired synchronously (root-caused the chronic `TestRefreshNow` flake) and `inProgress` clears atomically with `updateState`.
  - **Wave 2 — micro-cleanups**: `matcher.ParseError` carries a typed `ErrorKind` instead of substring-matching error text; `SilenceMetrics` tracks GC/sync stats via atomics (closing 3 TODO placeholders); `warnGroupingFallback` rate-limited to one `Warn`/minute under load; nil-`AlertGroup.Metadata` guards added where storage-rehydrated/test-built groups could panic mid-background-loop; dead code removed (`internal/infrastructure/migrations`, `routing.MultiReceiverPublisher`, `publishing.DefaultFormatRegistry`); testcontainers-backed tests now `t.Skip()` (not fail) when Docker is unreachable.
  - **Wave 3 — reliability tails** (`FU-WAVE3-RELIABILITY`, closed 2026-08-19): five independent fixes ledgered from wave-2 review. `NewPublishingQueue`'s nil-`Metrics` fallback now reuses the `v2.Global()` singleton instead of calling `v2.NewRegistry()` on every construction, fixing a duplicate-collector panic when a second queue was built in the same process. `eventKeyCacheImpl` (PagerDuty) gained a `Stop()`/stopChan and `inMemoryIncidentCache`'s (Rootly) existing `Stop()` was exposed on the `IncidentIDCache` interface — both now wired into `PublisherFactory.Shutdown()` alongside the Slack cache worker, closing two per-factory goroutine leaks that drowned full-package `-race` runs. `DefaultHealthMonitor.Start()`'s single-flight gate changed from a `Load()`-then-`Store(true)` pair to one `CompareAndSwap`, fixing a genuine data race (and duplicate-start logic bug) reproduced under `-race` in `TestHealthMonitor_ConcurrentStarts`. `postgres_history_test.go`'s 8 testcontainers-backed tests gained the same `requireDocker`/`testing.Short()` guard already used elsewhere, via the shared `setupTestDB` helper. The compile-time guard on `grouping.timerTTLGracePeriod` was tightened (trailing `- 1`) to reject the exact equality boundary at build time, matching `ValidateReconciliationGrace`'s already-strict runtime check — previously the compile guard alone allowed a zero-width reconciliation adoption window (the finding-2 bug) to pass at equality.

### Breaking changes / migration notes
- Receiver names must not contain `/` (reserved as the group-key separator, `receiver=<name>/<group-key>`); any other non-empty name is accepted.
- `GET /api/v2/status`'s `config.original` now redacts secret-named fields (PagerDuty `routing_key`, Slack/webhook URLs, Telegram `bot_token`, etc.) with upstream's own `<secret>` placeholder instead of the raw config.
- `PagerDutyConfig.RoutingKey` validation relaxed from `validate:"len=32"` to `validate:"required"`, matching upstream's plain non-empty `Secret` type — any previously-32-char-enforced config still validates, but non-32-char keys that were wrongly rejected before now pass too.
- `DELETE /api/v2/silence/{id}` expires the silence in place (upstream-like semantics) rather than a hard delete; treat it as "mark expired now," not row removal.
- Old bare group-level nflog Redis entries (`nflog:entry:{groupKey}`) are never read by the new target-suffixed lookup (`nflog:entry:{groupKey}:{target}`) and simply expire on their original TTL — no migration step needed.
- `grouping.reconciliation_grace` default changed 20s → 90s (derived from `delivery_confirmation_timeout`), and startup now **fails** on a pinned value that is not strictly greater than the publish-claim TTL (65s at defaults): a config carrying the old `reconciliation_grace: 20s` must raise it (or drop it to inherit the derived default).
- HA failover recovery time grew accordingly: a crashed replica's groups are adopted after up to ~90s grace + one reconciliation tick (was ~20s + tick) — the price of closing the mid-flight adoption race (`DeleteTimer` vs the live publisher's `SaveTimer`).
- **PHASE-6A-BUILTIN-TOOLS** (2026-05-08): four built-in investigation tools wired into the agentic loop so the LLM can inspect production data instead of only seeing alert labels.
  - `prometheus_query_range` (`internal/infrastructure/investigation/tools/prometheus.go`): PromQL via Prometheus HTTP API; time window resolved from `alert_time` carried through `context.Context` (default `-15m`/`+15m`/`step=1m`).
  - `loki_query_range` (`tools/loki.go`): LogQL via Loki HTTP API with optional Basic Auth; nanosecond stream timestamps normalised to ISO-8601.
  - `kubernetes` (`tools/kubernetes.go`): single tool dispatched by `action`: `list_pods`, `get_pod`, `get_events`, `get_logs`, `get_deployments`; wraps the existing `infrastructure/k8s` client.
  - `database_diagnostics` (`tools/database.go`): read-only PostgreSQL diagnostics via `stdlib.OpenDBFromPool` on the existing pgx pool — `query_type` ∈ {`active_queries`, `slow_queries`, `replication_lag`, `connection_stats`}.
  - All tools return JSON in `ToolResult.Content` so the LLM can reference specific fields during function calling.
  - `investigation.tools.*` configuration block added to `config.yaml.example` (`prometheus`, `loki`, `kubernetes`, `database` with per-tool `enabled` / `endpoint` / `timeout` / optional auth).
  - `ServiceRegistry.initializeInvestigation` registers each tool conditionally based on config; the `*sql.DB` returned by `stdlib.OpenDBFromPool` is retained on the registry and closed in `Shutdown` to avoid leaking on hot-reload.
  - New helpers: `internal/core/investigation/context.go` (`WithAlertTime` / `AlertTimeFromCtx`), `internal/core/investigation/README.md` documenting the contract, smoke test (`tools/all_tools_test.go`) asserting `ToolRegistry.Definitions()` exposes each enabled tool.
  - Test fixtures hardened: `tools/database_test.go` replaces package-level `globalFakeCols`/`globalFakeRows` with a per-call `fakeConnector` (race-safe under `t.Parallel`); `silencing/manager_alert_test.go` swaps the testify mock for a zero-overhead stub matcher so the 100-silences perf threshold reflects algorithm cost rather than mock plumbing.
- **Restored operational Alertmanager-compatible endpoints (2026-03-09)**:
  - `GET /api/v2/status`: returns current config text, version metadata, and runtime start time.
  - `GET /api/v2/receivers`: returns receiver names from the active config snapshot.
  - `GET /api/v2/alerts/groups`: exposes grouped alert responses for the current Grafana-compatible path.
  - `POST /-/reload`: triggers config reload via `ReloadCoordinator`.
- `ServiceRegistry` now tracks `startTime` and manages `ReloadCoordinator` for runtime configuration updates.
- New `StatusAPIHandler`, `ReceiversHandler`, `AlertGroupsHandler`, and `ReloadHandler` implemented in `internal/application/handlers`.

### Changed
- **PHASE-5A-TAIL** (2026-04-24): investigation pipeline config surface finalized.
  - `InvestigationConfig` struct in `internal/config/config.go` (`enabled`, `worker_count`, `queue_size`, `max_retries`, `retry_interval`, `llm_timeout`, `only_firing`) + viper defaults.
  - `OnlyFiring` added to `infrastructure/investigation.QueueConfig`; `Submit` drops resolved alerts when set.
  - `ServiceRegistry.initializeInvestigation` now reads config-driven queue params instead of hardcoded defaults.
  - Sample `investigation:` section added to `config.yaml.example` and `helm/amp/values.yaml`.
- **PARITY-B2-OPSGENIE-PUBLISHER** marked skipped (2026-04-24): Atlassian announced OpsGenie EOL — no longer accepting new customers. Publisher is not a goal; existing `OpsGenieConfig` struct left as a no-op.
- **PHASE-5B-LLM-AGENT**: Agentic investigation loop с tool calling. ~5d _(forge)_
- **PARITY-A5-WEB-EXTERNAL-URL**: callback-ссылки в нотификациях. ~0.5d _(forge)_
- **PARITY-A4-ADVANCED-FILTERING**: `filter` query param для alerts и silences. ~3d _(forge)_
- **PARITY-A1-NOTIFICATION-TRIGGERING**: `group_interval`/`repeat_interval` таймеры не триггерят нотификации. ~3d _(forge)_
- **REPOSITORY-FLAPPING-TRANSITIONS-DRIFT**: REPOSITORY-FLAPPING-TRANSITIONS-DRIFT _(forge)_
- **PUBLISHING-HEALTH-REFRESH-DRIFT**: PUBLISHING-HEALTH-REFRESH-DRIFT _(forge)_
- **PARITY-A3-EMAIL-PUBLISHER**: SMTP client + EmailPublisher в factory. ~2-3d _(forge)_
- **PARITY-A2-INHIBITION-PIPELINE**: InhibitionMatcher не подключён в AlertProcessor pipeline. ~2d _(forge)_
- **Go Toolchain Baseline Updated** - Project runtime/build baseline raised to Go 1.26 (2026-02-27)
  - `go-app/go.mod` now declares `go 1.26.0`
  - Docker builder image updated to `golang:1.26-alpine`
- **LLM Provider API Baseline** - LLM HTTP client now supports provider-aware endpoints (2026-02-27)
  - added `llm.provider` routing in client config (`proxy` and `openai`)
  - `provider=proxy` keeps legacy endpoints (`POST /classify`, `GET /health`)
  - `provider=openai` uses OpenAI-compatible endpoints (`POST /chat/completions`, `GET /models`) and parses JSON classification payload from chat response
  - service registry classification bootstrap now passes `llm.provider`, `llm.max_tokens`, `llm.temperature` into LLM client config
  - added in-memory cache fallback for classification service bootstrap when Redis is unavailable
  - added runtime endpoint `GET /api/v2/classification/stats` with active provider/API endpoints and supported-provider matrix
  - added runtime endpoint `GET /api/v2/classification/health` with live health probe of active provider endpoint (`/health` or `/models`)
  - `GET /api/dashboard/overview` now includes LLM provider and health snapshot fields (`llm_provider`, `llm_health_status`, `llm_health_endpoint`, `llm_healthy`)
  - runtime config reload/apply/rollback now refreshes LLM provider snapshot used by classification stats endpoint
  - added unit coverage for provider endpoint/header behavior and backward compatibility path
- **Alertmanager Core Endpoint Matrix Gate** - Added explicit parity matrix test for non-deprecated endpoint surface (2026-02-28)
  - added `TestUpstreamParity_CoreEndpointMethodMatrix` in `go-app/cmd/server/main_upstream_parity_regression_test.go`
  - locks method/route contracts for `/api/v2/status`, `/api/v2/receivers`, `/api/v2/alerts`, `/api/v2/alerts/groups`, `/api/v2/silences`, `/api/v2/silence/{id}`, `/-/healthy`, `/-/ready`, `/-/reload`
  - verifies allowed methods and disallowed `405` behavior on the same runtime matrix
- **Metrics System v2 Migration** - Complete migration of Health and Refresh metrics to unified `pkg/metrics/v2` (2024-12-08)
  - Added 8 new Prometheus metrics for health and refresh monitoring
  - Removed deprecated stub metrics files
  - Unified API for all publishing metrics
  - Full documentation: `tasks/metrics-v2-full-migration/`
- **Alertmanager Ops Compatibility Hardening** - Runtime contract aligned with upstream behavior (2026-02-26)
  - `POST /-/reload` returns `200` with empty body on success
  - `POST /-/reload` returns `500` on config reload/parse failures
  - `/debug/*` switched from JSON stub to pprof-backed proxy behavior
  - Added static compatibility routes: `/script.js`, `/favicon.ico`, `/lib/*`
  - `GET /api/v2/status` cluster payload now follows upstream-like mode semantics:
    - default runtime returns active single-node cluster shape with startup settling window (`status=settling` -> `status=ready`, self peer + name)
    - default generated `cluster.name` now uses upstream-like ULID format when `AMP_CLUSTER_NAME` is not set
    - `AMP_CLUSTER_LISTEN_ADDRESS=` (empty value) forces disabled shape (`status=disabled`, empty peers)
  - `GET /api/v2/receivers` now returns only configured `receivers[*].name` values (no route-name expansion, no alert-label discovery fallback)
  - `GET /api/v2/receivers` preserves runtime config receiver order (aligned with upstream response ordering)
  - `GET /api/v2/alerts` and `GET /api/v2/alerts/groups` query parsing aligned closer to upstream runtime behavior:
    - invalid state-flag bool values (`active/silenced/inhibited/unprocessed/muted`) now fall back to `false` when parameter is present
    - invalid `status`/`resolved` query values no longer return `400` and are ignored (`200` response)
    - `receiver` regex now uses upstream-like full-match semantics (`^(?:<query>)$`), not substring matching
    - receiver is now always resolved via runtime route tree matchers (`match`/`match_re`/`matchers`) with `continue` support and fallback to root `route.receiver`; `labels.receiver` no longer overrides routing; multi-match routes produce multiple receivers in `GET /api/v2/alerts` and duplicated receiver groups in `GET /api/v2/alerts/groups`
    - `GET /api/v2/alerts/groups` nested alert `receivers[]` is now sorted by receiver name (upstream-like), while top-level `GET /api/v2/alerts` keeps route-evaluation order
    - invalid `receiver`/`filter` query errors now return upstream-like JSON string payloads on `400` (instead of object-wrapped errors)
    - invalid `receiver` / `filter` error message text now matches upstream wording (`failed to parse receiver param: ...`, `bad matcher format: ...`)
    - `GET /api/v2/alerts/groups` grouping labels now respect runtime `route.group_by` (including upstream-like empty `labels: {}` when `group_by` is omitted/empty in config)
    - added upstream parity regression coverage for `route.group_by: ["..."]` (full-label grouping semantics)
    - `GET /api/v2/alerts` list ordering aligned with upstream behavior (`fingerprint` ascending)
  - API timestamps now use upstream-like millisecond precision (`.000Z`) for core runtime responses (`/api/v2/status` uptime, alerts/silences list payloads)
  - `POST /api/v2/alerts` error contracts aligned closer to upstream runtime behavior:
    - invalid JSON/time payloads return `{code:400,message}` on `400`
    - invalid JSON object parse message now uses upstream-like payload type wording (`models.PostableAlerts`)
    - missing `labels` returns `{code:602,message}` on `422`
    - invalid `generatorURL` returns `{code:601,message}` on `422`
    - empty `labels` returns upstream-like JSON string message on `400`
    - date-only timestamps (`YYYY-MM-DD`) for `startsAt`/`endsAt` are now accepted (upstream-like ingest behavior)
  - `DELETE /api/v2/silence/{id}` now returns `200` with empty body on success (upstream-like)
  - `POST /api/v2/silences` error contracts moved closer to upstream runtime behavior:
    - schema/required validation errors return `422` with `{code,message}` (for example `code=602/612`)
    - update with unknown/invalid `id` returns `404` with JSON string payload (`"silence not found"`)
    - create-time semantic validation keeps upstream-like JSON string payloads on `400` (e.g. invalid matcher regex, invalid timing)
  - `GET /api/v2/silences?filter=...` now returns upstream-like JSON string payload for invalid matcher errors (`400`)
  - `GET|DELETE /api/v2/silence/{id}` now return `422` + `{code,message}` for invalid UUID path values and `404` with empty body for unknown valid UUID (closer to upstream runtime behavior)
  - `GET /api/v2/silences` and `GET /api/v2/silence/{id}` now always include `matchers[].isRegex` (including `false`)
  - Added upstream parity regression coverage for reload/debug/static compatibility
- **Runtime Config API Baseline** - Added minimal config read/write path in active runtime (2026-02-26)
  - Added `GET /api/v2/config` (`format=json` default, `format=yaml`)
  - Added `POST /api/v2/config` (payload validation, atomic file write, runtime apply of inhibition/receivers)
  - Added `GET /api/v2/config/status` (last apply/reload result + source + timestamp + error + runtime counters)
  - Added `GET /api/v2/config/history` (newest-first runtime apply timeline with `limit` and config hash)
  - Added `POST /api/v2/config/rollback` (rollback to previous successful runtime revision; `409` when no previous revision exists)
  - Extended rollback with target hash selection: `POST /api/v2/config/rollback?configHash=<sha256>` (`400` invalid hash, `404` unknown hash, `409` when target already active)
  - Extended config history with filters: `GET /api/v2/config/history?status=ok|failed&source=<...>` for targeted audit and rollback prep
  - Added `GET /api/v2/config/revisions` (unique successful revision catalog with `isCurrent` for rollback target selection)
  - Added `DELETE /api/v2/config/revisions/prune?keep=<n>` to trim stale revision targets while keeping current active revision
  - Added non-mutating preview mode: `dryRun=true` for `POST /api/v2/config/rollback` and `DELETE /api/v2/config/revisions/prune`
  - `POST /api/v2/config` returns `400` for invalid payload, `413` for oversized payload, `405` for unsupported methods
  - Added Phase0 contract coverage for route inventory, format handling, method contracts and runtime-apply semantics

### Improved
- **Code Quality Refactoring** - Comprehensive refactoring achieving 160% quality target (2024-12-05)
  - Unified error handling with `pkg/httperror`
  - Optimized string formatting (50% less allocations)
  - Consolidated metrics to v2 architecture
  - Full documentation: `tasks/code-quality-refactoring/`

## [0.0.1] - 2024-12-04

### Added

#### Core Features
- 100% Alertmanager API v2 compatibility
- Alert grouping engine (33 files, group_by, group_wait, group_interval)
- Alert routing engine (19 files, route tree, multi-receiver support)
- Silencing system (14 files, CRUD, matchers, expiration)
- Inhibition rules (14 files, source/target matchers, state tracking)
- Deduplication service

#### LLM Classification (BYOK)
- Support for OpenAI (GPT-4, GPT-3.5)
- Support for Anthropic (Claude 3)
- Support for Azure OpenAI
- Support for custom LLM proxies
- Circuit breaker with fail-fast protection
- L1/L2 cache for classification results

#### Publishing
- Rootly integration (incidents create/update/resolve)
- Slack integration (messages, threads, rate limiting)
- PagerDuty integration (events, change events)
- Generic webhook publishing
- Parallel publishing with configurable concurrency

#### Web Dashboard
- Alert list with filtering and sorting
- Dashboard overview with stats
- Silences management (CRUD, bulk operations)
- LLM classification display (severity, confidence, recommendations)
- Real-time updates via WebSocket/SSE
- WCAG 2.1 AA accessibility

#### Observability
- 101 Prometheus metrics
- Grafana dashboard
- Health check endpoints
- Structured logging (slog)

#### Storage
- PostgreSQL support
- SQLite support (embedded)
- Redis caching

#### Deployment
- Dockerfile (multi-stage, Alpine, non-root)
- Helm chart with dev/production values
- Kubernetes examples

#### Documentation
- Alertmanager compatibility guide
- Migration quick start
- Migration comparison
- Extension examples (custom classifier, custom publisher)
- API documentation

### Performance
- Sub-5ms p95 latency (10x faster than Alertmanager)
- 5K req/s throughput (10x higher)
- 50MB memory footprint (4x less)
- 100m CPU usage (5x less)

### License
- AGPL-3.0 (copyleft for network services)

[Unreleased]: https://github.com/ipiton/AMP/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/ipiton/AMP/releases/tag/v0.0.1
