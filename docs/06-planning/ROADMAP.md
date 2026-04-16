# Стратегический план (ROADMAP)

Общие цели и стримы развития проекта.

## Stream: Runtime/API Stabilization
- [x] **PHASE-0: Baseline and Contract Lock** — Тестовая база для фиксации текущего поведения активного runtime.
- [x] **PHASE-1: API Unstabbing** — Активный runtime переведен на реальные обработчики core API (`status`, `alerts`, `silences`, `webhook`) и закреплен тестами.
- [ ] **PHASE-2: Bootstrap Consolidation** — Единый путь инициализации, удаление `main.go.full`.

## Stream: Storage & Reliability
- [x] **PHASE-3: Storage Hardening** — Стабильный startup/shutdown, migrations, health decomposition.

## Stream: Delivery & Publishing
- [x] **PHASE-4: Production Publishing Path** — Реальный publisher path, retries/rate limits и метрики.

## Stream: Operations & Hot Reload
> AMP уже имеет ReloadCoordinator (TN-152, 27KB) с 6-фазным pipeline. Задачи ниже расширяют его per-component graceful reload и K8s sidecar.
- [ ] **PHASE-4.5: Per-Component Reloadable + K8s Sidecar** — Интерфейс `config.Reloadable` для каждого infra-компонента (database, redis, LLM, logger, metrics) + config-reloader sidecar для ConfigMap-driven SIGHUP. Источник: AMP-OSS.
- [ ] **PHASE-4.6: Production Helm & Release Process** — `values-production.yaml` (PG cluster, DragonflyDB, sidecar) + шаблон release notes. Источник: AMP-OSS.

## Stream: Alertmanager Full Parity
> Задачи для полноценной замены Alertmanager без deprecated методов. Три фазы по приоритету.

### Phase A: Production-Viable Replacement (~2 недели)
- [ ] **PARITY-A1: Notification Triggering** — `group_interval` и `repeat_interval` таймеры не триггерят нотификации. `manager_impl.go:804,825,870` — TODO "will be implemented in TN-125". Без этого повторные и обновлённые алерты не доставляются. ~3d
- [ ] **PARITY-A2: Inhibition Pipeline Integration** — `InhibitionMatcher` реализован (<500µs), но `IsInhibited()` не вызывается в alert processing pipeline. Нужно wiring в `AlertProcessor`. ~2d
- [ ] **PARITY-A3: Email Publisher (SMTP)** — `EmailConfig` + email templates уже есть, нужен `EmailPublisher` в factory + SMTP client. ~2-3d
- [ ] **PARITY-A4: Advanced Alert/Silence Filtering** — `GET /api/v2/alerts` и `GET /api/v2/silences` — только простой list. Нужна фильтрация по matchers/regex (`filter` query param) как в Alertmanager. ~3d
- [ ] **PARITY-A5: web.external-url** — Alertmanager использует для callback-ссылок в нотификациях. Без этого ссылки в alert templates битые. ~0.5d

### Phase B: Feature Parity (~2-3 недели)
- [ ] **PARITY-B1: Mute Time Intervals** — Maintenance windows: time interval parser (weekday/time/month/year ranges), timezone support, `mute_time_intervals` и `active_time_intervals` в route config, wiring в routing evaluator. Alertmanager-compatible формат. ~5d
- [ ] **PARITY-B2: OpsGenie Publisher** — Config определён (`OpsGenieConfig`), нужен publisher. Популярен в enterprise. ~1-2d
- [ ] **PARITY-B3: Telegram Publisher** — Полностью отсутствует. Популярен в СНГ-сегменте. ~1-2d
- [ ] **PARITY-B4: Reloadable Components + Sidecar** — из AMP-OSS (см. PHASE-4.5). ~3d
- [ ] **PARITY-B5: Production Helm + Release Notes** — из AMP-OSS (см. PHASE-4.6). ~0.5d
- [ ] **PARITY-B6: web.route-prefix** — Для reverse proxy сценариев. ~0.5d

### Phase C: Enterprise HA (~3-4 недели)
- [ ] **PARITY-C1: Clustering (Redis-based)** — Redis-based state sync (рекомендовано вместо gossip/memberlist): notification log в Redis для dedup нотификаций, silence sync через Redis pub/sub, leader election для group timers через distributed locks, instance heartbeat. PostgreSQL уже реплицирует silences и alert state. ~10d
- [ ] **PARITY-C2: Remaining Receivers** — VictorOps/Splunk On-Call, WeChat, Pushover, SNS, Webex. Config определён для VictorOps/WeChat. ~5-7d (по 1-2d каждый)

## Stream: Intelligence (ML/LLM/MCP)
> Вдохновлено: [SherlockOps](https://github.com/Duops/SherlockOps) (двухфазный pipeline, agentic investigation), [Robusta+HolmesGPT](https://github.com/robusta-dev/holmesgpt) (K8s enrichment, AI RCA), [Keep](https://github.com/keephq/keep) (AIOps workflows, AI correlation).
> AMP уже имеет: LLM client (OpenAI/Claude/Azure + circuit breaker), K8s client, Classification service (2-tier cache), publishing path.

- [ ] **PHASE-5: Two-Phase Alert Pipeline + LLM Investigation** — Двухфазная обработка алертов по модели SherlockOps:
  - Phase 1 (<100ms): classify → route → publish (текущий flow, без изменений)
  - Phase 2 (5-30s): async investigation — LLM agentic loop с infrastructure tools
  - Доставка результата: thread reply (Slack), message edit (Telegram), card update (Teams)
- [ ] **PHASE-6: Investigation Toolset + Runbooks** — Infrastructure tools для LLM-агента + knowledge base:
  - Built-in tools: Prometheus (PromQL), Loki (LogQL), Kubernetes (pods/events/logs), PostgreSQL (active queries/locks)
  - Cloud tools: AWS CloudWatch, GCP Monitoring (опционально)
  - Runbook engine: markdown knowledge base с YAML frontmatter, автоматический match по alert labels
  - MCP server support: расширяемые custom tools через MCP protocol
  - Environment routing: per-environment tool endpoints (prod/staging/dev)
- [ ] **PHASE-7: UI/UX Workflow + Human-in-the-Loop** — Интеграция расследований в интерфейс:
  - Dashboard: timeline расследования (какие tools вызваны, что найдено)
  - Human approval: для auto-remediation actions (restart pod, scale deployment)
  - Feedback loop: operator подтверждает/отклоняет RCA → обучение runbooks

## Stream: Release
- [ ] **PHASE-8: Release & Rollout** — Quality gates, documentation, canary rollout, release notes процесс.

## Notes
- Статус синхронизирован с кодом и тестами по состоянию на 2026-04-16.
- PHASE-3 и PHASE-4 завершены как production slices (см. DONE.md).
- PHASE-4.5 и PHASE-4.6 добавлены из AMP-OSS — hot reload инфраструктура и production values.
- Stream "Alertmanager Full Parity" добавлен 2026-04-16 по результатам полного аудита фич AMP vs Alertmanager API v2.
- Phase A — минимум для production замены (без maintenance windows и HA).
- Phase B — полная feature parity с Alertmanager (без HA).
- Phase C — enterprise HA, нишевые receivers.
- Discord и MS Teams уже работают через webhook publisher с кастомными templates.
- Источники: gap analysis (`ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`), аудит кодовой базы 2026-04-16, Alertmanager API v2 spec.
