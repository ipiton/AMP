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

- [x] **FU-RECORDSENT-DELIVERY-CONFIRMATION** _(closed by wave 3, 2026-08-19)_ — nflog `RecordSent` now follows CONFIRMED delivery: queue jobs carry a completion channel, `PublishGroupToTargets` blocks per target (bounded by `CoordinatorConfig.DeliveryConfirmationTimeout`, 45s), so a 500/timeout leaves no nflog entry and is retried on the next scheduled fire. `notifyLogClaimTTL` raised 30s→60s to cover the now-blocking publish. ~2-3d
- [ ] **FU-WAVE3-RELIABILITY** — wave-3 candidates from wave-2 reviews: duplicate metrics collector panic in queue metrics v2 on repeated package runs; goroutine leaks in pagerduty/slack/rootly_cache.go (drown full-pkg -race); TestHealthMonitor_ConcurrentStarts single-flight race; postgres_history_test.go 8 unguarded testcontainers tests; compile-guard vs runtime-validator boundary-equality mismatch for reconciliation grace. ~2d
- [x] **FU-WEBHOOK-BATCHING** _(closed by wave 2, 2026-08-18)_ — wire-level webhook batching: one POST with alerts array vs N per-alert jobs. Interface-level ONE notification satisfied; follow-up optimizes delivery. ~2d
- [x] **FU-NFLOG-DEDUP** _(closed by wave 2, 2026-08-18)_ — — per-target nflog dedup granularity (current: per-group-receiver); enable finer deduplication at publisher target level. ~1d
- [x] **FU-TELEGRAM-RATE-LIMIT** _(closed by wave 2, 2026-08-18)_ — — per-chat rate limit for Telegram (~1msg/s per chat vs global 30/s). Operational risk noted during Phase 7.1. ~1-2d
- [x] **FU-MIGRATION-ADVISORY-LOCK** _(shipped 2026-08-18, goose Provider session lock)_ — — migration advisory lock mechanism. In progress on sdd/fu-miglock; track coordination. See final-review blocking #2. ~2d
- [x] **FU-ROUTING-METRICS** _(shipped 2026-08-18, injected singleton metrics)_ — — routing metrics restoration (currently disabled due to promauto double-registration). Per-evaluator custom registry. In progress on sdd/fu-routing-metrics. ~2d
- [ ] **FU-RECEIVERS-INTEGRATION** — receivers: integration auto-provisioning (data-plane follow-up; current state: control-plane parity only, delivery via K8s Secrets). See final-review #5. ~5-7d
- [ ] **FU-FINGERPRINT-HEX-FORMAT** — fingerprint 16-hex upstream format (F2 compatibility). ~0.5d
- [ ] **FU-SILENCES-EXPIRED-QUERY** — silences --expired query support. ~0.5d
- [ ] **FU-GET-ALERTS-V1** — GET /api/v1/alerts endpoint (parity gap, brief asked POST alias only). ~0.5d
- [ ] **FU-SILENCE-SYNC-INTERVALS** — configurable silence-sync intervals (currently: 2s backoff / 5min resync constants hardcoded). ~0.5d
- [ ] **FU-STORAGEMANAGER-FAILBACK** — StorageManager runtime Redis failback (startup-only decision currently; potential graceful degradation). ~1-2d
- [ ] **FU-SLACK-PAGERDUTY-QUEUE-PATH** — enhanced slack/pagerduty publishers unreachable via queue path (same class as telegram fix in Phase 7.2). Mutex guards for client maps. ~1d
- [ ] **FU-PARSEARGUMENT-QUOTE-HANDLING** — parseMatcherExpr quote handling edge cases (third matcher grammar divergence vs configvalidator). ~0.5d
- [ ] **FU-GLOB-DEFAULT-VALUES** — GlobalConfig fallback fields for group_by/duration. ~0.5d
- [ ] **FU-DOUBLE-NORMALIZE-ROUTES** — double NormalizeRoutePrefix call cleanup. ~0.25d
- [ ] **FU-PARSEBOOL-EMPTY-DEFAULT** — parseBoolQueryStrict silently defaults on empty param value. ~0.25d
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
