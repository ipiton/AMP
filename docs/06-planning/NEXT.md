# Очередь (Queue) и WIP

## Queue

### 0. Parity quick wins (из разбора karma, 2026-09-23)
> Перенесено из BACKLOG (секция «UI и экосистема — идеи из karma»). Обе задачи — про совместимость с экосистемой Alertmanager, друг от друга не зависят, вместе ~1d.

- (KARMA-COMPAT закрыт 2026-09-23, см. DONE.md)
- (PARITY-RESOLVE-TIMEOUT-ENDSAT закрыт 2026-09-24, см. DONE.md)

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Phase 5A/5B закрыты, осталось наполнить агента реальными tools.
> Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base с auto-matching по alert labels. ~2d
- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d

### 2. Operations (из AMP-OSS)
- [x] ~~**RELOADABLE-COMPONENT-INTERFACES**~~ — закрыто 2026-08-20 (INF-A slice 1), см. BACKLOG.
- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven SIGHUP. Частично сделано INF-B (values-шейп есть, нет Go-кода sidecar + Dockerfile + template). ~1d _(2026-09-25, замечено на PROD-AUTH: запись устарела — `cmd/config-reloader`, `Dockerfile.config-reloader` и шаблон в `deployment.yaml` уже есть (`7cf223a`…`e1038ea`); не хватает образа в CI (`PROD-CI-IMAGES`) и auth (`CONFIG-RELOADER-AUTH`). Статус пересмотреть.)_ _(2026-09-28, PROD-CI-IMAGES: образ собирается в CI и публикуется `release.yml` в `ghcr.io/ipiton/amp-config-reloader` по тегу `v*`, `configReloader.image.repository` указывает туда. Остаётся: первый релиз (`PROD-RELEASE-V010`), `CONFIG-RELOADER-AUTH`, затем `enabled: true` в production values.)_
- [x] ~~**HELM-PRODUCTION-VALUES**~~ — закрыто 2026-08-20 (INF-B), см. BACKLOG; остаточные гэпы чарта — в `TECH-DEBT.md` (`HELM-CHART-GAPS`).

### 3. Alertmanager Parity — Phase B (feature parity)
> Необязательно для controlled replacement, но закрывает полный feature set Alertmanager.
> DELIVERED via feat/alertmanager-parity (Phase 1-7, 2026-08-18): PARITY-B1/B3/B6 shipped; deferred follow-ups in BACKLOG.

- (absorbed by AMP-PARITY, see DONE.md 2026-08-18)

## WIP (Max 2)

- [ ] **PROD-CI-IMAGES** (взят 2026-09-28) — GitHub Actions (build, vet, `test -race`, release-gate, govulncheck) + публикация образов `amp` и `amp-config-reloader` (multi-arch, semver + sha). Ветка `feature/prod-ci-images`, workspace `tasks/PROD-CI-IMAGES/`. Источник: BACKLOG «Production Readiness — блокеры», P0 Delivery. _(2026-09-28: `/implement`, `/testing` и `/write-doc` пройдены; PR ipiton/AMP#12 — CI зелёный, кроме ожидаемо красного `govulncheck`. Осталось `/end-task`, `/merge-to-main`.)_

- [x] **AMP-PARITY** (завершено 2026-08-18, см. DONE.md) — все фазы + финальная fix-волна и follow-ups влиты в main. Drop-in замена Alertmanager (routing tree, dispatcher/grouping, mute_time_intervals, API parity, config validation, Redis HA clustering, receivers). 29 task slices Phases 1-7 delivered; e2e+HA green. Plan: `docs/plans/alertmanager-parity.md`, ветка `feat/alertmanager-parity`, task workspace `tasks/AMP-PARITY/`. Follow-ups: BACKLOG «AMP-PARITY Follow-ups».

## Notes
- 2026-09-28: по ходу PROD-CI-IMAGES в BACKLOG заведены `PROD-DEPS-VULN` (P0, 9 уязвимостей от govulncheck — следующий прод-блокер, до `PROD-RELEASE-V010`), `CI-E2E-HA-REQUIRED`, `CI-SUPPLY-CHAIN`, `HELM-RENDER-TEST-IN-GATE`, `GROUPING-TIMER-LOCK-FIX` (флейк `-race`, краснит required `gate` ~1/15). `PROD-HELM-CLEAN-CHECKOUT` закрыт в том же срезе. Branch protection на `main` (required checks — `docs/CI.md`) включает владелец репозитория вручную.
- 2026-09-28: в WIP взят PROD-CI-IMAGES напрямую из BACKLOG (следующий по рекомендованному порядку прод-блокеров). Зацеплен с `PROD-HELM-CLEAN-CHECKOUT`: release-gate на чистом checkout CI упадёт без `helm dependency build` — решить на `/research`/`/spec`, втягивать ли его в срез.
- 2026-09-28: PROD-RBAC-SCOPE закрыт (RBAC чарта = одна Role `list secrets`, ADR-012), WIP свободен. Следующий прод-блокер по рекомендованному порядку BACKLOG — `PROD-CI-IMAGES`. По ходу заведён `INVESTIGATION-K8S-TOOL-HELM` (BACKLOG). Замечено: `helm/amp/README.md` называет `lite` профилем по умолчанию, в `values.yaml` — `standard` (не правили). PHASE-6B-RUNBOOK-ENGINE, судя по коммиту `ea7900e` («close task, archive workspace»), закрыт на невлитой ветке `origin/claude/start-task-pyym8f` — в Queue числится открытым, проверить и влить.
- 2026-09-28: в WIP взят PROD-RBAC-SCOPE напрямую из BACKLOG (следующий по рекомендованному порядку прод-блокеров), в обход верха Queue.
- 2026-09-25: PROD-AUTH закрыт (basic auth по upstream `--web.config.file`, ADR-011), WIP свободен. Следующий прод-блокер по рекомендованному порядку BACKLOG — `PROD-RBAC-SCOPE`, затем `PROD-CI-IMAGES`. Попутно созрел `PROD-SECURITY-MD` (auth теперь есть — SECURITY.md можно переписать).
- 2026-09-25: в WIP взят PROD-AUTH напрямую из BACKLOG (отдельное решение, как предписано заметкой о прод-блокерах ниже), в обход PHASE-6B-RUNBOOK-ENGINE наверху Queue.
- 2026-09-24, по ходу PARITY-RESOLVE-TIMEOUT-ENDSAT заведено: `RESOLVE-TIMEOUT-AUTO-RESOLVE` (BACKLOG — авто-резолв и resolved-нотификация по истечении `endsAt`, ~1d+) и `ALERT-STORE-DEDUP-KEY-STARTSAT` (BUGS.md — повторный POST без `startsAt` создаёт копию алерта в memory store; предсуществующий). Группа 0 очереди исчерпана.
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
