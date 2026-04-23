# BACKLOG

Не в активной очереди, но учтено и перенесено из `.plans`.

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

- [ ] **PARITY-B1-MUTE-TIME-INTERVALS** — maintenance windows:
  - `mute_time_intervals` / `active_time_intervals` — 0% реализации
  - Alertmanager: поддерживает с v0.22+
  - **Config формат** (Alertmanager-compatible):
    ```yaml
    time_intervals:
      - name: business-hours
        time_intervals:
          - weekdays: ['monday:friday']
            times:
              - start_time: '09:00'
                end_time: '17:00'
      - name: maintenance-window
        time_intervals:
          - weekdays: ['saturday']
            times:
              - start_time: '02:00'
                end_time: '06:00'
    route:
      routes:
        - receiver: oncall
          mute_time_intervals: [maintenance-window]
          active_time_intervals: [business-hours]
    ```
  - **Реализация (5 компонентов)**:
    1. **Time interval parser** — структуры `TimeInterval`, `TimePeriod`:
       - fields: `weekdays` (monday:friday range), `days_of_month` (1-31, negative for end-of-month), `months` (january:march range), `years` (2026:2028 range), `times` (start_time/end_time HH:MM)
       - Парсинг range-нотации: `monday:friday`, `1:15`, `january:march`
    2. **Timezone support** — `location: Europe/Moscow` per interval, time.LoadLocation()
    3. **Matcher** — `func (ti *TimeInterval) IsActive(t time.Time) bool`:
       - Проверяет все условия (weekday AND time AND month AND day AND year)
       - Empty field = always matches (как в Alertmanager)
    4. **Route integration** — два новых поля в route config:
       - `mute_time_intervals: []string` — ссылки на named intervals, нотификация подавляется
       - `active_time_intervals: []string` — ссылки на named intervals, нотификация ТОЛЬКО в это время
    5. **Wiring в routing evaluator** — перед отправкой нотификации:
       - Если любой mute interval active → suppress
       - Если есть active intervals и ни один не active → suppress
  - Оценка: ~5d

- [ ] **PARITY-B2-OPSGENIE-PUBLISHER** — enterprise receiver:
  - `OpsGenieConfig` определён (api_key, api_url, message, description, responders, tags)
  - Нет publisher implementation
  - Оценка: ~1-2d

- [ ] **PARITY-B3-TELEGRAM-PUBLISHER** — популярен в СНГ:
  - Полностью отсутствует (ни config, ни publisher)
  - Нужно: TelegramConfig + TelegramPublisher (Bot API)
  - Оценка: ~1-2d

- [ ] **PARITY-B6-WEB-ROUTE-PREFIX** — reverse proxy:
  - Alertmanager: `--web.route-prefix` для prefix routing за reverse proxy
  - AMP: отсутствует
  - Оценка: ~0.5d

## Alertmanager Full Parity — Phase C (enterprise HA)

- [ ] **PARITY-C1-CLUSTERING** — высокая доступность:
  - Нет gossip/memberlist/peer sync — single-instance only
  - Потеря in-memory state при crash (Redis спасает partially)
  - **Что синхронизируется в Alertmanager**:
    1. **Silences** — создание/удаление реплицируется на все ноды
    2. **Notification log (nflog)** — кто какой alert group отправил → предотвращает дубли
    3. **Alert state** — resolved/firing статус
  - **Вариант A: Hashicorp memberlist** (как Alertmanager) ~15d:
    - Плюс: совместимость с `--cluster.peer` флагами
    - Минус: сложный gossip протокол, UDP/TCP, flaky в K8s (mesh networking)
    - Минус: нужен service discovery для peers
  - **Вариант B: Redis-based state sync** (рекомендуется для AMP) ~10d:
    - Плюс: AMP уже использует Redis (group state, locks, cache) — инфраструктура есть
    - Плюс: проще в K8s (нет gossip, нет UDP)
    - Минус: Redis = SPOF (нужен Redis Sentinel или Cluster)
    - **Реализация**:
      1. **Notification log в Redis** — key: `nflog:{group_key}:{receiver}`, value: timestamp последней отправки. TTL = repeat_interval * 2. Перед отправкой: проверить nflog → если другой инстанс уже отправил → skip
      2. **Silences** — уже в PostgreSQL, реплицируются на уровне DB. In-memory cache синхронизируется через Redis pub/sub: `silence:created`, `silence:deleted` events
      3. **Alert state** — firing/resolved уже в PostgreSQL. In-memory sync через Redis pub/sub канал `alert:state`
      4. **Leader election для group timers** — Redis distributed lock на group_key. Только leader триггерит group_wait/group_interval. При потере лидера — automatic failover через lock expiry
      5. **Health check** — каждый инстанс пишет heartbeat в Redis (`instance:{id}:heartbeat`, TTL 30s). Readiness probe учитывает peer count
  - **Решение**: Вариант B — Redis-based. Естественно ложится на существующую архитектуру AMP
  - Оценка: ~10d

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

- [ ] **PHASE-6A-BUILTIN-TOOLS** — Infrastructure tools для LLM-агента:
  - `internal/investigation/tools/prometheus.go` — PromQL queries:
    - Query метрик вокруг alert time window (±15min)
    - Rate, increase, histogram_quantile для SLI/SLO
    - Auto-suggest related metrics по alert labels
  - `internal/investigation/tools/loki.go` — LogQL queries:
    - Логи related pods/services за time window
    - Error/warning filtering
    - Pattern detection
  - `internal/investigation/tools/kubernetes.go` — расширить existing K8s client:
    - Pod status, restarts, OOMKills
    - Recent events (FailedScheduling, ImagePullBackOff, etc.)
    - Container logs (last N lines)
    - Recent deployments/rollouts (что менялось?)
    - Resource usage vs limits
  - `internal/investigation/tools/database.go` — PostgreSQL diagnostics:
    - `pg_stat_activity` — active queries, locks
    - `pg_stat_statements` — slow queries
    - Replication lag
    - Connection pool stats
  - Оценка: ~7d (Prometheus 2d, Loki 2d, K8s 1.5d, DB 1.5d)

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
