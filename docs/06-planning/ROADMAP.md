# Стратегический план (ROADMAP)

Общие цели и стримы развития проекта. Статусы сверены с кодом, `DONE.md`/`archive/DONE-*.md` и `BACKLOG.md` **2026-10-07** (аудит 2026-10-06). Детали и текущие блокеры — в `BACKLOG.md` § «Production Readiness».

## Stream: Runtime/API Stabilization
- [x] **PHASE-0: Baseline and Contract Lock** — Тестовая база для фиксации текущего поведения активного runtime.
- [x] **PHASE-1: API Unstabbing** — Активный runtime переведен на реальные обработчики core API (`status`, `alerts`, `silences`, `webhook`) и закреплен тестами.
- [x] **PHASE-2: Bootstrap Consolidation** — Единый путь инициализации (`ServiceRegistry`, `Router`, `handlers`), `main.go.full` удалён, `main.go` ~200 строк.

## Stream: Storage & Reliability
- [x] **PHASE-3: Storage Hardening** — Стабильный startup/shutdown, migrations, health decomposition.
- [ ] **PROD-READINESS** _(добавлен 2026-10-07)_ — путь от pilot-ready к production-ready: P0 из `BACKLOG.md` § «Production Readiness» (фолбэк конфига без файла, красный `govulncheck`, флейк таймера группировки, группировка по умолчанию, верхнеуровневый `inhibit_rules:`, жёсткий фильтр, релиз, решение по HA Postgres). Security- и shutdown-блокеры аудита 2026-09-21 закрыты (PROD-AUTH, -INGRESS-HARDENING, -RBAC-SCOPE, -SECURITY-MD, -CI-IMAGES, -DEPS-VULN, -GRACEFUL-SHUTDOWN).

## Stream: Delivery & Publishing
- [x] **PHASE-4: Production Publishing Path** — Реальный publisher path, retries/rate limits и метрики.

## Stream: Operations & Hot Reload
> AMP уже имеет ReloadCoordinator (TN-152) с 6-фазным pipeline.
- [~] **PHASE-4.5: Per-Component Reloadable + K8s Sidecar** — интерфейсы `config.Reloadable` (logger, metrics, LLM — живые; database/redis — честный restart-required W600/W601) сделаны в PROD-INFRA (2026-08-20); sidecar `cmd/config-reloader` + шаблон + публикация образа — сделаны. Остаётся: первый релиз образа, `CONFIG-RELOADER-AUTH`, включение в production values (`CONFIG-RELOADER-SIDECAR` в BACKLOG); живые хэндлы Postgres/Redis — `FU-DB-LIVE-POOL-HANDLE`, `FU-REDIS-LIVE-CLIENT-HANDLE`.
- [~] **PHASE-4.6: Production Helm & Release Process** — `values-production.yaml` переаудирован, шаблон release notes и релизный процесс (`WORKFLOW.md`) есть (PROD-INFRA, PROD-RELEASE-V010-PREP). Остаётся: выпуск `v0.1.0` (`PROD-RELEASE-V010`, ждёт решения владельца), дефолты под одну ноду (`HELM-SINGLE-NODE-DEFAULTS`), HA Postgres (`PROD-POSTGRES-HA-DECISION`).

## Stream: Alertmanager Full Parity
> Задачи для полноценной замены Alertmanager без deprecated методов. Эпик `AMP-PARITY` (7 фаз, 29 срезов) закрыт 2026-08-18; хвосты — в BACKLOG § «Follow-ups from Phase 1-7» и в P0/P1 «Production Readiness».

### Phase A: Production-Viable Replacement — ✅ закрыта
- [x] **PARITY-A1: Notification Triggering** — `group_interval` и `repeat_interval` таймеры триггерят нотификации.
- [x] **PARITY-A2: Inhibition Pipeline Integration** — `ShouldInhibit` подключён в `AlertProcessor`.
- [x] **PARITY-A3: Email Publisher (SMTP)** — `EmailPublisher` + SMTP client зарегистрированы в factory.
- [x] **PARITY-A4: Advanced Alert/Silence Filtering** — `filter` query param для alerts/silences.
- [x] **PARITY-A5: web.external-url** — проброшен через `PrometheusParser` и `helm values`.

### Phase B: Feature Parity — ✅ закрыта (кроме сознательно пропущенного)
- [x] **PARITY-B1: Mute Time Intervals** — `time_intervals`, `mute_time_intervals`/`active_time_intervals` в маршрутах (AMP-PARITY, 2026-08-18).
- [~] **PARITY-B2: OpsGenie Publisher** — SKIPPED: Atlassian объявил EOL OpsGenie (April 2027). Конфиг-структура остаётся как non-goal.
- [x] **PARITY-B3: Telegram Publisher** — `telegram_configs` доставляются (AMP-PARITY, 2026-08-18). `parse_mode` — в списке частичной поддержки полей.
- [~] **PARITY-B4: Reloadable Components + Sidecar** — см. PHASE-4.5.
- [~] **PARITY-B5: Production Helm + Release Notes** — см. PHASE-4.6.
- [x] **PARITY-B6: web.route-prefix** — `--web.route-prefix` и наследование из `external_url` (AMP-PARITY, 2026-08-18).

### Phase C: Enterprise HA
- [x] **PARITY-C1: Clustering (Redis-based)** — Redis nflog + send-claim, распределённые таймеры, pub/sub для silences, leader election, heartbeat (AMP-PARITY Phase 6, 2026-08-18). Открыто: `GROUPING-TIMER-LOCK-FIX` (P0), `CI-E2E-HA-REQUIRED`.
- [ ] **PARITY-C2: Remaining Receivers** — VictorOps/Splunk On-Call, WeChat, Pushover, SNS, Webex. Config определён для VictorOps/WeChat. ~5-7d (по 1-2d каждый)

## Stream: Intelligence (ML/LLM/MCP)
> Вдохновлено: [SherlockOps](https://github.com/Duops/SherlockOps) (двухфазный pipeline, agentic investigation), [Robusta+HolmesGPT](https://github.com/robusta-dev/holmesgpt) (K8s enrichment, AI RCA), [Keep](https://github.com/keephq/keep) (AIOps).

- [~] **PHASE-5: Two-Phase Alert Pipeline + LLM Investigation** — сделано: async investigation (очередь, workers, retry, сохранение, `GET /api/v1/alerts/{fingerprint}/investigation`; PHASE-5A, 5A-TAIL) и agentic loop с tool calling (PHASE-5B). Не сделано: доставка результата в Slack/Telegram/Teams; fallback провайдеров (PHASE-5C). **Расхождение с замыслом:** Phase 1 не «<100ms без изменений» — при `llm.enabled` классификация синхронна в `POST /api/v2/alerts` и может дропнуть алерт (`PROD-LLM-ALERT-PATH-ISOLATION`, P1).
- [~] **PHASE-6: Investigation Toolset + Runbooks** — built-in tools Prometheus/Loki/Kubernetes/PostgreSQL сделаны (PHASE-6A, 2026-05-08); runbook engine (PHASE-6B) сделан 2026-09-27 и влит в `main` 2026-10-07; MCP tools (6C) и environment routing (6D) — не начаты. Kubernetes tool чартом не проводится (`INVESTIGATION-K8S-TOOL-HELM`).
- [ ] **PHASE-7: UI/UX Workflow + Human-in-the-Loop** — timeline расследования в dashboard, human approval для remediation, feedback loop.

## Stream: Release
- [x] **PHASE-8: Release & Rollout** — quality gate (`scripts/release-gate.sh`), smoke e2e, rollback runbook, rollout plan, CI и публикация образов по тегу. Первый тег `v0.1.0` — отдельно, `PROD-RELEASE-V010`.

## Notes
- 2026-10-07: статусы пересинхронизированы с кодом (раньше — на 2026-04-16). Phase B и C1 закрыты эпиком AMP-PARITY ещё 2026-08-18, но здесь оставались открытыми.
- PHASE-4.5 и PHASE-4.6 пришли из AMP-OSS — hot reload инфраструктура и production values.
- Stream "Alertmanager Full Parity" добавлен 2026-04-16 по результатам аудита фич AMP vs Alertmanager API v2. Не входящие в 🟢 расхождения — `docs/ALERTMANAGER_COMPATIBILITY.md` § Known Gaps.
- Discord и MS Teams работают через webhook publisher с кастомными templates.
- `[~]` — частично сделано или сознательно пропущено; что осталось — в строке пункта.
- Источники: `ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`, аудиты 2026-04-16, 2026-09-21, 2026-10-06, Alertmanager API v2 spec.
