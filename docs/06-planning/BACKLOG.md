# BACKLOG

Не в активной очереди, но учтено и перенесено из `.plans`.

## Runtime gaps (найдены при закрытии FUTUREPARITY-GAP, 2026-08-17) — ВСЕ ЗАКРЫТЫ 2026-08-17

- [x] ~~**RECEIVERS-JSON-CASE**~~ — json-тег на `ReceiverConfig`, `/api/v2/receivers` отдаёт `{"name":...}`.
- [x] ~~**SILENCE-MATCHER-VALUE-IGNORED**~~ — `MatchesSilenceMatchers` матчит значение silence-matcher'а оператором фильтра (upstream-семантика); отсутствующее имя = пустое значение.
- [x] ~~**NO-METHOD-ENFORCEMENT**~~ — `getOnly`-guard: status/receivers/groups/healthy/ready отвечают 405 на не-GET/HEAD.
- [x] ~~**SILENCEDBY-NULL**~~ — закрыт консолидацией alertconv: `SilencedBy/InhibitedBy/MutedBy` всегда `[]`.
- [x] ~~**GROUPALERTS-HARDCODED-RECEIVER**~~ — receiver из конфига (первый, fallback "default"), resolved-алерты в группы не входят.
- Persistence/rehydration gap закрыт в SPLIT-BRAIN-RISK (tasks/DEBT-STAGE4-SPLITBRAIN/research.md).

## Near-term (из AMP-OSS)
> Фичи, реализованные в AMP-OSS и отсутствующие в AMP.
> Примечание: AMP уже имеет ReloadCoordinator (TN-152) с полным 6-фазным pipeline (load → validate → diff → apply → reload → health check). Задачи ниже — дополнения к существующей инфраструктуре.

- [ ] **RELOADABLE-COMPONENT-INTERFACES** — Интерфейс `config.Reloadable` + per-component реализации поверх существующего ReloadCoordinator:
  - DatabaseReloadable — graceful connection pool recreation, 5s drain period
  - RedisReloadable — dynamic pool resizing, PING verification before swap
  - LLMReloadable — atomic model swap (gpt-4 ↔ gpt-4-turbo), RWMutex protection
  - LoggerReloadable — dynamic log level/format (json ↔ text)
  - MetricsReloadable — enable/disable metrics collection
  - Интеграция: подключить к ReloadCoordinator фазе "reload" для per-component graceful swap
  - Источник: AMP-OSS `go-app/internal/infrastructure/*/reloadable.go`, `go-app/pkg/logger/reloadable.go`, `go-app/pkg/metrics/reloadable.go`
  - Оценка: ~2d (портирование + wiring в Service Registry)

- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven reload (~223 LOC Go):
  - SHA256 file change detection → SIGHUP signal to PID 1
  - Health check verification (`/health/reload`)
  - Prometheus metrics (port 9091)
  - Dockerfile (distroless, non-root, read-only fs)
  - Helm integration: `configReloader` секция в values.yaml + sidecar template
  - Зависимость: RELOADABLE-COMPONENT-INTERFACES (sidecar бесполезен без per-component reload)
  - Источник: AMP-OSS `go-app/cmd/config-reloader/`
  - Оценка: ~1d (портирование + Helm templates)

- [ ] **HELM-PRODUCTION-VALUES** — Production-ready Helm values:
  - PostgreSQL cluster (3 instances)
  - DragonflyDB cache (вместо Redis)
  - Publishing targets preset
  - configReloader sidecar integration
  - Источник: AMP-OSS `helm/amp/values-production.yaml` (207 строк)
  - Оценка: ~0.5d (адаптация под текущий values.yaml)

- [ ] **RELEASE-NOTES-PROCESS** — Шаблон и процесс для release notes:
  - Формат: changelog-compatible markdown
  - Секции: features, performance, breaking changes, backward compatibility
  - Источник: AMP-OSS `RELEASE_NOTES_v0.0.3.md` как шаблон
  - Оценка: ~0.5d

## Alertmanager Full Parity — Phase A (production-viable)
> Критичные gaps, блокирующие использование AMP как замены Alertmanager. После Phase A — AMP пригоден для production (без maintenance windows и HA).

- [x] **PARITY-A1-NOTIFICATION-TRIGGERING** — `group_interval` и `repeat_interval` таймеры не триггерят нотификации: _(closed by forge)_
  - `manager_impl.go:804` — "Trigger notification here (will be implemented in TN-125)"
  - `manager_impl.go:825` — аналогичный TODO
  - `manager_impl.go:870` — "repeat_interval timer expired (not implemented)"
  - Без этого: первая нотификация уходит (group_wait), но повторные/обновлённые — нет
  - Оценка: ~3d

- [x] **PARITY-A2-INHIBITION-PIPELINE** — InhibitionMatcher реализован и работает (<500µs), но не подключён: _(closed by forge)_
  - `IsInhibited()` определён но не вызывается в AlertProcessor pipeline
  - `internal/infrastructure/inhibition/` — полный matcher + parser + cache
  - Нужно: wiring в `alert_processor.go` между classification и publishing
  - Связано: TN-126, TN-137 (упомянуты в TODO)
  - Оценка: ~2d

- [x] **PARITY-A3-EMAIL-PUBLISHER** — Config + templates есть, publisher нет: _(closed by forge)_
  - `EmailConfig` определён в `alertmanager/config/config.go`
  - `email.go` templates (Subject, HTML, Text) в `notification/template/defaults/`
  - Нет: `EmailPublisher` в `infrastructure/publishing/`, SMTP client
  - Нужно: SMTP client, EmailPublisher, регистрация в factory
  - Оценка: ~2-3d

- [x] **PARITY-A4-ADVANCED-FILTERING** — alert и silence filtering: _(closed by forge)_
  - `GET /api/v2/alerts` — только простой list, нет `filter` query param с matchers
  - `silences.go:61` — "Advanced filtering (regex, matchers) will be added later"
  - Alertmanager поддерживает: `filter=alertname="test"`, `filter=alertname=~".*foo.*"`
  - Оценка: ~3d

- [x] **PARITY-A5-WEB-EXTERNAL-URL** — для callback-ссылок в нотификациях: _(closed by forge)_
  - Alertmanager: `--web.external-url` → используется в templates как `{{ .ExternalURL }}`
  - AMP: отсутствует → ссылки в alert templates не работают (Silence link, Generator URL)
  - Оценка: ~0.5d

## Alertmanager Full Parity — Phase B (feature parity)

- [x] ~~**PARITY-B1-MUTE-TIME-INTERVALS**~~ — maintenance windows _(closed by feat/alertmanager-parity, 2026-08-18)_

- [~] **PARITY-B2-OPSGENIE-PUBLISHER** — SKIPPED (2026-04-24): Atlassian объявил EOL OpsGenie — April 2027, прием новых клиентов закрыт. Реализация publisher потеряла смысл. `OpsGenieConfig` в коде можно оставить как no-op или удалить в отдельной cleanup-задаче.

- [x] ~~**PARITY-B3-TELEGRAM-PUBLISHER**~~ — popularен в СНГ _(closed by feat/alertmanager-parity, 2026-08-18)_

- [x] ~~**PARITY-B6-WEB-ROUTE-PREFIX**~~ — reverse proxy _(closed by feat/alertmanager-parity, 2026-08-18)_

## Alertmanager Full Parity — Phase C (enterprise HA)

- [x] ~~**PARITY-C1-CLUSTERING**~~ — высокая доступность _(closed by feat/alertmanager-parity Phase 6, 2026-08-18)_

## Alertmanager Full Parity — Follow-ups from Phase 1-7 delivery (2026-08-18)
> Дефериты и потенциальные оптимизации из final-review и progress.md; не блокируют production deployment.

- [x] **FU-RECORDSENT-DELIVERY-CONFIRMATION** _(closed by wave 3, 2026-08-19)_ — nflog `RecordSent` now follows CONFIRMED delivery: queue jobs carry a completion channel, `PublishGroupToTargets` blocks per target (bounded by the new `publishing.queue.delivery_confirmation_timeout`, 45s), so a 500/timeout leaves no nflog entry and is retried on the next scheduled fire. The timer-callback deadline and nflog claim TTL are derived from that knob (60s each) and validated at startup; post-delivery bookkeeping runs on a detached context; abandoning an unawaited job cancels its in-flight publish; the per-group publish lock is no longer striped. ~2-3d
- [x] **FU-WAVE3-RELIABILITY** _(closed by wave 3, 2026-08-19)_ — wave-3 candidates from wave-2 reviews: duplicate metrics collector panic in queue metrics v2 on repeated package runs; goroutine leaks in pagerduty/slack/rootly_cache.go (drown full-pkg -race); TestHealthMonitor_ConcurrentStarts single-flight race; postgres_history_test.go 8 unguarded testcontainers tests; compile-guard vs runtime-validator boundary-equality mismatch for reconciliation grace. ~2d
- [x] **FU-PER-ALERT-OUTCOMES** _(opened + closed by wave 4, 2026-08-19)_ — per-alert outcome tracking for non-batch publishers (the wave-3 M4 residual): Slack/Telegram/PagerDuty/Email loop `Publish` per alert inside one job, so alert 3 of 5 failing left the whole `(group, target)` unconfirmed and the next fire re-sent all five, duplicating the four that had landed. Jobs now report which alerts the target accepted (`GroupPublishHandle.DeliveredAlerts`, readable mid-flight), the chain stores them as per-`(group, target)` delivered state (`nflog:delivered:*`, Redis HASH fingerprint→status written atomically via Lua, TTL = repeat_interval + grace, capped at 500 alerts, cross-replica) and narrows the retry fire to the alerts still owed; in-job retries skip already-accepted alerts too. Compared by `core.Alert.DeliveryKey` (`fingerprint:status`) but stored one status per fingerprint, so a flapping alert is re-sent rather than suppressed. Fails open in the resend direction (cap refusals counted), batch targets untouched. Review round 1 closed 1 Critical (flap suppression) + 2 Important (in-memory state never expired; non-atomic Redis write could leave a TTL-less key). ~1.5d
- [x] **FU-WEBHOOK-BATCHING** _(closed by wave 2, 2026-08-18)_ — wire-level webhook batching: one POST with alerts array vs N per-alert jobs. Interface-level ONE notification satisfied; follow-up optimizes delivery. ~2d
- [x] **FU-NFLOG-DEDUP** _(closed by wave 2, 2026-08-18)_ — — per-target nflog dedup granularity (current: per-group-receiver); enable finer deduplication at publisher target level. ~1d
- [x] **FU-TELEGRAM-RATE-LIMIT** _(closed by wave 2, 2026-08-18)_ — — per-chat rate limit for Telegram (~1msg/s per chat vs global 30/s). Operational risk noted during Phase 7.1. ~1-2d
- [x] **FU-MIGRATION-ADVISORY-LOCK** _(shipped 2026-08-18, goose Provider session lock)_ — — migration advisory lock mechanism. In progress on sdd/fu-miglock; track coordination. See final-review blocking #2. ~2d
- [x] **FU-ROUTING-METRICS** _(shipped 2026-08-18, injected singleton metrics)_ — — routing metrics restoration (currently disabled due to promauto double-registration). Per-evaluator custom registry. In progress on sdd/fu-routing-metrics. ~2d
- [ ] **FU-RECEIVERS-INTEGRATION** — receivers: integration auto-provisioning (data-plane follow-up; current state: control-plane parity only, delivery via K8s Secrets). See final-review #5. ~5-7d
- [x] **FU-FINGERPRINT-HEX-FORMAT** _(verified already shipped, closed by wave 5, 2026-08-19)_ — fingerprint 16-hex upstream format (F2 compatibility). Boundary conversion: `alertconv.UpstreamFingerprint` (FNV-1a via `prometheus/common/model`) substituted into the API response only; internal SHA-256 dedup key untouched. ~0.5d
- [x] **FU-SILENCES-EXPIRED-QUERY** _(verified already shipped, closed by wave 5, 2026-08-19)_ — silences --expired query support. `GET /api/v2/silences` returns all states incl. expired with correct `status.state`; DELETE expires in place. ~0.5d
- [x] **FU-GET-ALERTS-V1** _(verified already shipped, closed by wave 5, 2026-08-19)_ — GET /api/v1/alerts endpoint (parity gap, brief asked POST alias only). Legacy v1 envelope via `alertconv.ToV1Alert`. ~0.5d
- [x] **FU-SILENCE-SYNC-INTERVALS** _(closed by wave 5, 2026-08-19)_ — configurable silence-sync intervals: `silencing.subscribe_retry_backoff` / `silencing.periodic_resync_interval` replace the hardcoded 2s backoff / 5min resync constants, defaults unchanged, validated (positive, backoff < resync). ~0.5d
- [x] **FU-STORAGEMANAGER-FAILBACK** _(closed by wave 5, 2026-08-19; fix round same day: hysteresis + write-through/deletion-replay reconciliation; fix round 2 same day: fallback pruned after reconcile (Critical), replay-before-write-through, caller-ctx-aware connectivity classification)_ — StorageManager runtime Redis failback/failforward: the existing (built 2025-11-04, never wired in) `grouping.StorageManager` now wraps the standard profile's Redis `GroupStorage`, with a health probe (consecutive-failure/success hysteresis + min-hold, not single-tick), connectivity-error classification on per-call fallback (excluding caller-side context cancellation), backend-active gauge, and loud logging on switch. Recovery reconciles fallback into primary (deletion replay first, then write-through, then prunes the fallback) before flipping — a second outage now starts from an empty fallback instead of accumulating and eventually overwriting fresh Redis state with stale leftovers. Still not full state-merge machinery (multi-replica HA and a narrow snapshot-to-flip race remain, documented) — see `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #6. ~1-2d
- [x] **FU-SLACK-PAGERDUTY-QUEUE-PATH** _(closed by wave 5, 2026-08-19 — fix round 2)_ — the routing (`createPublisherForJob` → `CreatePublisherForTarget` for slack/pagerduty) and the `clientMu` guard over all four per-target client maps had already landed as part of the telegram fix (ed8c864, wave 3). First pass of this item only added the missing httptest coverage (queue-path job reaches `EnhancedSlackPublisher`/`EnhancedPagerDutyPublisher`, hits the real endpoints) and closed this line on that basis — **but review found both enhanced integrations reached the provider endpoint carrying a payload the provider rejects**: see `FU-SLACK-BUILDMESSAGE-TYPE-MISMATCH` and `FU-PAGERDUTY-BUILDPAYLOAD-NESTING` below, both fixed in the same fix round, plus the cache-key collision in `FU-PAGERDUTY-ROOTLY-CACHE-KEY`. A second re-review round then found the Slack fix incomplete on its own terms — a context block with no `elements` (Block Kit requires 1-10) and legacy-attachment `fields` still dropped, both closed by fix round 2 (see `FU-SLACK-BLOCK-ELEMENTS-MISSING`). PagerDuty's payload is verified correct: both Events API v2 required fields (`summary`, `severity`) populated with valid values, checked directly. **Slack's payload is verified correct against the Block Kit schema and by httptest — not against a live Slack webhook**; no test in this repo can call the real API, so full acceptance is asserted at spec-conformance confidence, not empirically proven.
- [x] **FU-SLACK-BUILDMESSAGE-TYPE-MISMATCH** _(opened by wave 5 first pass, closed by wave 5 fix round, 2026-08-19)_ — `formatSlack` (formatter.go) returns `blocks`/`attachments` as `[]map[string]any` and never sets `"text"` at all, but `EnhancedSlackPublisher.buildMessage`'s type assertions expected `[]interface{}` — a different slice type, so they never matched. Every enhanced Slack notification shipped `{"text":""}`, which Slack's real webhook answers with **HTTP 400 `invalid_payload`** — a live wave-3 regression (`ed8c864`), not a dormant defect: the pre-wave-3 basic publisher JSON-marshaled the formatter map directly and worked. Fixed: `formatSlack` now sets `result["text"]` (Slack's required fallback), and `buildMessage`/`buildBlock` go through a new `toMapSlice` helper that accepts both `[]map[string]any` and `[]interface{}` shapes. `TestCreatePublisherForJob_SlackActuallyPostsToWebhook` now asserts the wire body's `Text`/`Blocks`/`Attachments` are non-empty and describe the alert; verified red against the pre-fix code before committing.
- [x] **FU-PAGERDUTY-BUILDPAYLOAD-NESTING** _(opened + closed by wave 5 fix round, 2026-08-19)_ — found by review, not by the implementer, despite the new httptest test having the decoded request in hand. `EnhancedPagerDutyPublisher.buildPayload` read `summary`/`severity`/`timestamp`/`source` at the TOP level of the formatted map, but `formatPagerDuty` nests exactly those four fields under `formattedData["payload"]` — only `custom_details` was ever read correctly, since that access already went through the nested map. Every enhanced PagerDuty trigger therefore shipped `payload.summary`/`payload.severity` empty, and the real Events API v2 requires both non-blank — **400 `payload.summary can't be blank`**, same live wave-3 regression window as Slack's. Fixed: `buildPayload` reads the nested `payload` map first, falling back to a flat top-level read for a differently-shaped formatter. `TestCreatePublisherForJob_PagerDutyActuallyCallsEventsAPI` now asserts `Payload.Summary`/`Payload.Severity`/`Payload.Source`; verified red against the pre-fix code before committing.
- [x] **FU-PAGERDUTY-ROOTLY-CACHE-KEY** _(opened + closed by wave 5 fix round, 2026-08-19)_ — found by review: `pagerDutyClientMap`/`rootlyClientMap` were keyed on the credential alone (`routing_key` / API key) while the cached client bakes in `target.URL` too — exactly the telegram cache-key defect (`clientKey := apiURL + "|" + botToken`) this item was briefed to carry over, and didn't. Two targets sharing a routing key (or API key) with different URLs (one direct, one via a proxy/regional endpoint) silently both went to whichever was constructed first, live since `ed8c864` made this the only publish path. Fixed with the same compound-key pattern (`baseURL + "|" + routingKey` / `target.URL + "|" + apiKey`), PagerDuty's base URL resolved to its public default before building the key so two empty-URL targets still correctly share one client. Added `TestCreatePublisherForTarget_PagerDutyCacheKeyIncludesBaseURL` + `TestCreatePublisherForJob_PagerDutyHonoursTargetURL` (shared-factory, the case the review noted the first-pass tests couldn't reach) and `TestCreatePublisherForTarget_RootlyCacheKeyIncludesBaseURL`; all three verified red against the pre-fix code before committing.
- [x] **FU-SLACK-BLOCK-ELEMENTS-MISSING** _(opened + closed by wave 5 fix round 2, 2026-08-19)_ — found by re-review (R1 Important + R2 Minor), same defect family as `FU-SLACK-BUILDMESSAGE-TYPE-MISMATCH`, missed by fix round 1: `formatSlack`'s context block carries `elements` (the fingerprint footer), but `Block` had no `Elements` field and `buildBlock` had no elements branch, so it shipped as bare `{"type":"context"}` — Block Kit **requires** 1-10 elements on a context block, and Slack validates the whole `blocks` array, answering `invalid_blocks`/400 rather than falling back to `text`. Same live-breakage class C1 was fixed to remove, just one block deeper. R2 (Minor): `buildAttachment` was the one caller of the blocks/attachments/fields trio never routed through `toMapSlice`, so `formatSlack`'s attachment `fields` (Status/Started/Namespace/AI-severity) also vanished — cosmetic, not a rejection risk, but same family. Fixed: added `Block.Elements []Text` + a `toMapSlice`-routed extraction branch in `buildBlock`; `buildMessage` now drops a `context` block with zero elements defensively rather than shipping one Block Kit would reject; added `Attachment.Fields []Field` + a matching branch in `buildAttachment`. `TestCreatePublisherForJob_SlackActuallyPostsToWebhook` now asserts the context block's `Elements` are non-empty and carry the alert's fingerprint, and the attachment's `Fields` are non-empty; verified red (test does not even compile without the new struct fields) against the pre-fix code before committing.
- [x] **FU-DELIVERED-STATE-POLISH** _(opened + closed by wave 5, 2026-08-19)_ — the three wave-4 re-review Minor findings parked for a later pass (`fu4-out-review.md` r2/r5/r6): r5 (delivered-state TTL slid forward on every partial write instead of anchoring to the first write, letting a persistently-failing target suppress delivered alerts past one `repeat_interval`), r2 (Redis Lua cap accounting counted per-occurrence instead of per-distinct-fingerprint, diverging from the memory implementation at the cap boundary), and r6 (memory `RecordPartialDelivery` planted an empty state on a refused/no-op call; Redis debug log mislabeled its `HLEN` return as `alerts_recorded`). All three fixed, `delivered_set_test.go`'s shared two-implementation contract suite extended with the r2/r5 regressions. ~0.5d
- [x] **FU-PARSEARGUMENT-QUOTE-HANDLING** _(closed by wave 5, 2026-08-19; fix round same day: upstream grammar alignment + operator-in-quoted-value fix; fix round 2 same day: `label=` empty-value alignment + Apache-2.0 attribution)_ — parseMatcherExpr quote handling edge cases (third matcher grammar divergence vs configvalidator): `pkg/configvalidator/matcher.Parse` never stripped quotes at all (a real bug — it fed the quote-included literal into `regexp.Compile` for `=~`/`!~` matchers); `business/routing.parseMatcherExpr` stripped quotes but never unescaped `\"`/`\\`. Both now run a verbatim, attributed port of upstream `pkg/labels/parse.go`'s escape/quote loop (duplicated, not imported, to keep `pkg/` leaf-level) — fixing 4 review-verified divergences (`\n`, unquoted-value escaping, unescaped inner quote, fail-open unterminated quote) plus a 5th (`label=` empty value, round 2) — and `Parse` locates the operator via the same anchored regex `parseMatcherExpr` uses, fixing a quoted-operator-token split that used to hard-fail startup validation. Table-tested in both packages plus a real-`LoadConfig` regression test. ~0.5d
- [x] **FU-GLOB-DEFAULT-VALUES** _(closed by wave 5, 2026-08-19)_ — GlobalConfig fallback fields for group_by/duration: `infraroute.GlobalConfig.GroupBy/GroupWait/GroupInterval/RepeatInterval` restored (dropped by the TN-137 dedup, 3f8d69d) as a fallback layer `TreeBuilder.inheritGroupBy`/`inheritDuration` consult below parent-route inheritance and above the hardcoded upstream defaults. AMP-only convenience, not upstream's actual `global:` schema — see `docs/ALERTMANAGER_COMPATIBILITY.md`. ~0.5d
- [x] **FU-DOUBLE-NORMALIZE-ROUTES** _(closed by wave 5, 2026-08-19)_ — double NormalizeRoutePrefix call cleanup: `cmd/server/main.go`'s startup log line called it twice on the identical `cfg.Server.RoutePrefix` value (log field + inline dashboard URL); computed once and reused. `NormalizeRoutePrefix` is pure/idempotent — added `TestNormalizeRoutePrefix_Idempotent` as the regression guard. ~0.25d
- [x] **FU-PARSEBOOL-EMPTY-DEFAULT** _(closed by wave 5, 2026-08-19)_ — parseBoolQueryStrict silently defaults on empty param value. Fixed: `query.Has(key)` distinguishes absent (keeps default) from present-but-empty (`?active=`, now 400, matching upstream). ~0.25d
- [x] **FU-MICRO-CLEANUPS** _(closed by wave 2, 2026-08-18)_ — — minor code/test hygiene from final-review backlog:
  - matcherErrorCode classification via error-string substring (fragile); clarify or fix
  - GetStats TODOs (GCLastRun, etc., pre-existing)
  - TimerManagerConfig dead config defaults (startup-only decision)
  - warnGroupingFallback per-alert log rate-limit at volume
  - copyMetadata shallow-copies timer pointers (pre-existing, flagged by re-reviewer)
  - DefaultFormatRegistry comment stale ("5 formats" now outdated)
  - sleep-poll e2e test flakiness (registry e2e uses poll)
  - TimeIntervalNames Redis round-trip test gap
  - telegram field-level validator missing in configvalidator (backstopped by routing.Parse)
  - configurable silence-sync intervals (2s backoff / 5min resync hardcoded constants)
  - ~0.25d each

- [x] **FU-STORAGE-RECONCILE-SIGNAL** _(closed by wave 6, 2026-08-19)_ — metric distinguishing "Redis down" from "reconciliation keeps failing" when failforward stays blocked (wave-5 review residual; today both look identical on the backend gauge). Added `amp_storage_reconcile_failures_total` (`BusinessMetrics.IncStorageReconcileFailure`), incremented in `checkHealthAndSwitch` only when `reconcileFallbackIntoPrimary` fails AFTER a successful `Ping` — never on the probe-still-failing path. Tests: reconcile-failure path increments and does not flip back to primary; probe-failure path leaves the counter untouched. ~0.5d
- [x] **FU-FLAKE-DUPLICATE-METRIC-KEYS** _(closed by wave 6, 2026-08-19)_ — `internal/business/publishing.TestEdgeCase_DuplicateMetricKeys` is an order-dependent flake (concurrent CollectAll vs "last writer wins" assertion), pre-existing, documented across waves 2-5 gates. Fixed the test's assertion to the order-independent property that always holds (winner is one of the two registered values, never a third; conflict-free keys match their own collector) instead of a scheduling-dependent winner. No production code changed. `-count=20 -race`: stable. ~0.25d
- [ ] **PARITY-C2-REMAINING-RECEIVERS** — нишевые:
  - VictorOps/Splunk On-Call — config определён (`VictorOpsConfig`)
  - WeChat — config определён (`WeChatConfig`)
  - Pushover, SNS, Webex — полностью отсутствуют
  - Discord, MS Teams — уже работают через webhook с templates
  - Оценка: ~5-7d суммарно

## Intelligence — PHASE-5: Two-Phase Pipeline + LLM Investigation
> Reference: [SherlockOps](https://github.com/Duops/SherlockOps) (Go, MIT), [HolmesGPT](https://github.com/robusta-dev/holmesgpt) (CNCF Sandbox), [Keep](https://github.com/keephq/keep) (AIOps).
> AMP уже имеет: LLM client (`infrastructure/llm/`), K8s client (`infrastructure/k8s/`), Classification service (`core/services/classification.go`, 2-tier cache), publishing path (Slack/PD/Webhook/Rootly).

- [ ] **PHASE-5A-INVESTIGATION-PIPELINE** — Двухфазный async pipeline:
  - **Phase 1** (<100ms): существующий flow — classify → route → publish (без изменений)
  - **Phase 2** (5-30s): async investigation, запускается параллельно после Phase 1
  - Новый компонент: `internal/investigation/pipeline.go`
    - Worker pool с configurable concurrency
    - SQLite/Redis cache для дедупликации расследований (TTL-based)
    - Timeout + circuit breaker на investigation (не блокирует alert flow)
  - Результат Phase 2 доставляется через existing publishers:
    - Slack: thread reply к оригинальному alert message
    - Telegram: edit оригинального сообщения (append RCA)
    - Teams: update adaptive card
    - Webhook: POST enriched payload
  - Оценка: ~3d

- [x] **PHASE-5B-LLM-AGENT** — Agentic investigation loop: _(closed by forge)_
  - Новый компонент: `internal/investigation/agent.go`
  - Использует existing `infrastructure/llm/` client (Claude/OpenAI/Azure + circuit breaker)
  - **Agentic loop** (как SherlockOps):
    1. LLM получает alert context (labels, annotations, status, timing)
    2. LLM решает какие tools вызвать (не статические правила)
    3. Tool results возвращаются в LLM context
    4. LLM формирует следующий запрос или финальный RCA
    5. Max iterations: configurable (default 5)
  - **Tool calling interface**: `type Tool interface { Name() string; Description() string; Execute(ctx, params) (string, error) }`
  - **System prompt**: environment-specific, включает available tools и runbook context
  - **Output format**: structured JSON — root_cause, confidence, evidence[], recommendations[], severity_assessment
  - Оценка: ~5d

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — LLM provider switch и fallback:
  - Primary → fallback chain (e.g. Claude → OpenAI → Ollama)
  - Per-environment provider config
  - Cost tracking (token usage per investigation)
  - Rate limiting per provider
  - Расширить existing circuit breaker в `infrastructure/llm/`
  - Оценка: ~2d

## Intelligence — PHASE-6: Investigation Toolset + Runbooks

- [x] **PHASE-6A-BUILTIN-TOOLS** — закрыт 2026-05-08. См. `DONE.md` и `CHANGELOG.md` (раздел Added). Реализация лежит в `go-app/internal/infrastructure/investigation/tools/` (prometheus, loki, kubernetes, database) + wiring через `investigation.tools.*` в `config.yaml.example`.

- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base:
  - `internal/investigation/runbooks/engine.go`
  - **Формат runbook** (как SherlockOps):
    ```yaml
    ---
    name: High Memory Usage
    match:
      alertname: HighMemoryUsage
      severity: critical
    tags: [memory, oom, kubernetes]
    ---
    ## Symptoms
    Pod memory usage exceeds 90% of limit.

    ## Common Causes
    1. Memory leak in application
    2. Insufficient memory limits
    3. Cache not bounded

    ## Investigation Steps
    1. Check `container_memory_working_set_bytes` trend
    2. Look for OOMKilled events
    3. Check recent deployments

    ## Remediation
    - Short-term: increase memory limit
    - Long-term: profile application memory usage
    ```
  - **Matching**: по alert labels (alertname, severity, namespace, etc.)
  - **Injection в LLM context**: matched runbooks добавляются в system prompt
  - **Storage**: filesystem directory (configurable path) или ConfigMap в K8s
  - Оценка: ~2d

- [ ] **PHASE-6C-MCP-TOOLS** — Extensible tools через MCP protocol:
  - MCP server support — custom tools без изменения core code
  - Регистрация external MCP servers в config.yaml
  - LLM видит MCP tools наравне с built-in tools
  - Use case: custom internal APIs, CMDB, deployment systems
  - Оценка: ~3d

- [ ] **PHASE-6D-ENVIRONMENT-ROUTING** — Per-environment tool config:
  - `environments` секция в config.yaml:
    ```yaml
    environments:
      prod:
        prometheus: http://prometheus.prod:9090
        loki: http://loki.prod:3100
        kubernetes: in-cluster
        runbooks: /etc/amp/runbooks/prod/
      staging:
        prometheus: http://prometheus.staging:9090
        loki: http://loki.staging:3100
    ```
  - Routing по header `X-Environment` (как SherlockOps) или alert label `environment`
  - Каждое environment — изолированный набор tools
  - Оценка: ~2d

## Intelligence — PHASE-7: UI/UX + Human-in-the-Loop

- [ ] **PHASE-7A-INVESTIGATION-DASHBOARD** — UI для расследований:
  - Timeline view: alert → tools called → findings → RCA
  - Evidence panel: метрики, логи, events собранные во время investigation
  - Confidence indicator (LLM certainty)
  - Link back to Prometheus/Grafana graphs
  - Оценка: ~5d

- [ ] **PHASE-7B-HUMAN-APPROVAL** — Approval workflow для actions:
  - Auto-remediation предлагается, но НЕ выполняется без approval
  - Slack interactive buttons: Approve / Reject / Investigate More
  - Audit trail: кто одобрил, когда, что было выполнено
  - Оценка: ~3d

- [ ] **PHASE-7C-FEEDBACK-LOOP** — Обучение на результатах:
  - Operator подтверждает/отклоняет RCA → сохраняется для будущих расследований
  - Similar incidents: при новом алерте — показать прошлые расследования с таким же fingerprint
  - Runbook suggestions: если operator часто выполняет одни и те же шаги → предложить создать runbook
  - Оценка: ~3d

## Release
- [ ] **PHASE-8-RELEASE-ROLLOUT** — полный quality gate, smoke e2e, rollback runbook и controlled rollout.
