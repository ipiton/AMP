# Очередь (Queue) и WIP

## Queue

### 0. Parity quick wins (из разбора karma, 2026-09-23)
> Перенесено из BACKLOG (секция «UI и экосистема — идеи из karma»). Обе задачи — про совместимость с экосистемой Alertmanager, друг от друга не зависят, вместе ~1d.

- (KARMA-COMPAT закрыт 2026-09-23, см. DONE.md)
- (PARITY-RESOLVE-TIMEOUT-ENDSAT → WIP 2026-09-24)

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Phase 5A/5B закрыты, осталось наполнить агента реальными tools.
> Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base с auto-matching по alert labels. ~2d
- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d

### 2. Operations (из AMP-OSS)
- [x] ~~**RELOADABLE-COMPONENT-INTERFACES**~~ — закрыто 2026-08-20 (INF-A slice 1), см. BACKLOG.
- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven SIGHUP. Частично сделано INF-B (values-шейп есть, нет Go-кода sidecar + Dockerfile + template). ~1d
- [x] ~~**HELM-PRODUCTION-VALUES**~~ — закрыто 2026-08-20 (INF-B), см. BACKLOG; остаточные гэпы чарта — в `TECH-DEBT.md` (`HELM-CHART-GAPS`).

### 3. Alertmanager Parity — Phase B (feature parity)
> Необязательно для controlled replacement, но закрывает полный feature set Alertmanager.
> DELIVERED via feat/alertmanager-parity (Phase 1-7, 2026-08-18): PARITY-B1/B3/B6 shipped; deferred follow-ups in BACKLOG.

- (absorbed by AMP-PARITY, see DONE.md 2026-08-18)

## WIP (Max 2)

- [ ] **PARITY-RESOLVE-TIMEOUT-ENDSAT** (старт 2026-09-24) — POST без `endsAt` должен давать `endsAt = startsAt + global.resolve_timeout` (сейчас `endsAt == startsAt` ⇒ потребитель считает активный алерт отгоревшим). ~0.5d. Ветка `claude/determined-meitner-bn0ncl` (назначена сессией вместо `bugfix/parity-resolve-timeout-endsat`), workspace `tasks/PARITY-RESOLVE-TIMEOUT-ENDSAT/`.

- [x] **AMP-PARITY** (завершено 2026-08-18, см. DONE.md) — все фазы + финальная fix-волна и follow-ups влиты в main. Drop-in замена Alertmanager (routing tree, dispatcher/grouping, mute_time_intervals, API parity, config validation, Redis HA clustering, receivers). 29 task slices Phases 1-7 delivered; e2e+HA green. Plan: `docs/plans/alertmanager-parity.md`, ветка `feat/alertmanager-parity`, task workspace `tasks/AMP-PARITY/`. Follow-ups: BACKLOG «AMP-PARITY Follow-ups».

## Notes
- 2026-09-23, по ходу KARMA-COMPAT заведено: `PUBLISHING-WARMUP-TEST-FLAKY` (BUGS.md — флейк `TestBackgroundWorker_WarmupPeriod` под полным прогоном) и два гейт-дефекта в BACKLOG: `PARITY-GATE-DOES-NOT-GATE` (`make test-upstream-parity` гоняет сьют без его build-тега ⇒ «no tests to run» и ложное зелёное) и `QUALITY-GATES-DIRTIES-TREE` (`make quality-gates` переписывает 6 чужих неотформатированных файлов).
- Очередь обновлена 2026-09-23: перенесены KARMA-COMPAT и PARITY-RESOLVE-TIMEOUT-ENDSAT (группа 0), синхронизирован статус группы 2 (RELOADABLE-COMPONENT-INTERFACES и HELM-PRODUCTION-VALUES закрыты 2026-08-20, в очереди висели как открытые).
- 🔴 **Блокеры прод-релиза живут в BACKLOG**, секция «Production Readiness — блокеры (аудит 2026-09-21)»: P0 по security (нет аутентификации на API), delivery (нет CI и опубликованных образов) и reliability (порядок graceful shutdown). Они приоритетнее всего, что ниже в этой очереди; в Queue не перенесены сознательно — при WIP max 2 берём их отдельным решением.
- Предыдущее обновление 2026-05-08 после закрытия PHASE-6A (built-in tools для investigation-агента). Parity Phase A и Intelligence Phase 5A/5B/6A закрыты.
- **Приоритет 1**: PHASE-6B-RUNBOOK-ENGINE — markdown KB с auto-matching по alert labels, дополняет 6A tools и завершает Investigation Toolset.
- **Приоритет 2**: PHASE-5C-PROVIDER-FALLBACK — primary→fallback chain для LLM, повышает устойчивость 5B/6A.
- **Приоритет 3**: Operations (reloadable + sidecar) — закрывает hot reload story.
- **Приоритет 4**: Parity Phase B — по запросу, не критично.
- Parity Phase C (clustering, remaining receivers) и Intelligence Phase 6C/6D/7 остаются в BACKLOG.
- Завершённые задачи: см. `DONE.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
