# NEXT

Queue и WIP — источник правды для выбора задачи. Закрытое сюда не пишется: отчёт — в `DONE.md`, история — в git.

## Flow Rules

<!-- Единственное место для чисел потока. Команды и другие документы ссылаются сюда, а не копируют. -->

- **WIP limit:** 2 — одна основная задача плюс один срочный фикс. Меняется только здесь.
- **Прод-блокеры первыми:** секция «Production Readiness — блокеры» в `BACKLOG.md` приоритетнее всей Queue; порядок — в самой секции. Берутся в WIP напрямую из BACKLOG.
- **Replenish** Queue, когда в ней меньше трёх задач. Кандидата сначала проверить по `DONE.md` и `archive/DONE-*.md`, чтобы закрытое не вернулось.
- **Balance** maintenance (`BUGS`, `TECH-DEBT`) и roadmap (`ROADMAP`, `BACKLOG`) 50/50: по последним пяти закрытиям — если одной стороны больше трёх, следующую задачу брать с другой.
- **Owner inbox first:** на `start-task` разблокированное действие владельца (`Trigger: owner ready`, `Blocked-by:`, `Waiting-on:`) важнее первой позиции Queue. Больше 5 разблокированных — новые roadmap-задачи не берём.
- **Slice** задачи больше двух дней до старта.
- **Live state only:** ушедшая из WIP или Queue запись удаляется, а не зачёркивается и не комментируется.

## WIP


## Queue

> Сначала — P0 из `BACKLOG.md` § «Production Readiness», по порядку (правило «Прод-блокеры первыми»): `PROD-RELEASE-V010` (отложен владельцем 2026-10-07). Задачи ниже берутся, когда P0 закрыты или заблокированы. Пересинхронизировано 2026-10-07 по аудиту 2026-10-06; `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16, `HELM-EXTERNAL-DATABASE` закрыт 2026-10-16; `PG-CONFIG-FIX` закрыт 2026-10-18; `PROD-POSTGRES-HA-DECISION` закрыт 2026-10-15; `HELM-SINGLE-NODE-DEFAULTS` закрыт 2026-10-19. `AMP-CACHE-NOAUTH` закрыт 2026-10-20. `SLACK-WEBHOOK-PLAIN-OK-RESPONSE` закрыт 2026-10-20.

### Owner

- [ ] **PRIVATE-VULN-REPORTING** — включить Private vulnerability reporting в `ipiton/AMP` (Settings → Code security). `SECURITY.md` с 2026-10-05 направляет сообщения об уязвимостях туда, а сейчас `gh api repos/ipiton/AMP/private-vulnerability-reporting` → `{"enabled":false}`: канал не работает. Проверка — та же команда, `enabled: true`. `~1min`
  Trigger: owner ready.
- [ ] **MAIN-BRANCH-PROTECTION** — включить branch protection на `main` с required checks из `docs/CI.md`; до этого «зелёный CI обязателен» не enforced. Репозиторий пересоздан 2026-09-29, настроек защиты нет. `PROD-DEPS-OTEL-145` закрыт 2026-10-08 (локально и на `go1.26.8` `govulncheck` чистый): включать, когда после push `main` CI на нём зелёный, включая `govulncheck`. С 2026-10-10 `govulncheck` на `main` был красный (GO-2026-6599…6617); `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16 — локально `govulncheck` на `go1.26.9` чистый, release-gate PASS. Осталось: push `main`, дождаться зелёного CI (включая `govulncheck`), затем включить защиту. `~5min`
  Waiting-on: push `main` и зелёный CI на нём.

### ADR-018 — AMP перед Alertmanager

- [ ] **ADR-018-V3-PROXY-SLICING** — нарезать эпик перехода на прокси перед upstream AM (`DECISIONS.md` § ADR-018) в `BACKLOG.md`. Срезы и оценки (~12–18d; удаление кода оценено по объёму, без пробного удаления):
  1. PoC — форвардер во все пиры AM (ретраи, TLS/basic auth), классификация → annotations `amp_*` с бюджетом < `group_wait`, fail-open по LLM/PG/кешу; e2e на k3d Prometheus → AMP → AM → webhook-приёмник видит annotations в сгруппированном уведомлении. 4–5d, первым;
  2. дроп шума за флагом (default off), метрика и лог дропов (`filter_engine.go`). 0.5–1d;
  3. удаление паритетного ядра (routing, dispatch, notify chain, nflog, таймеры, silences, inhibition, publishing/интеграции, шаблоны, K8s-Secret таргеты, Redis HA/lock), разбор wiring `service_registry.go` и конфига. 3–5d;
  4. API и UI: убрать AM API кроме приёма алертов, UI читает состояние из AM API v2. 1.5–2.5d;
  5. чарт: адрес AM обязателен, Valkey опционален, реплики AMP без состояния кроме PG. 1.5–2.5d;
  6. доки и миграция: README, `ALERTMANAGER_COMPATIBILITY.md` → гайд интеграции, CHANGELOG breaking, ревизия ADR-002/006/009/016/017. 1.5–2d.

  Заодно пересмотреть `REVIEW-PIPELINE-2026-10-21.md`, `BUGS.md`, `TECH-DEBT.md`: паритетные позиции закрыть ссылкой на ADR-018, оставить security/critical по коду, который переживает переход; `PROD-LLM-ALERT-PATH-ISOLATION` влить в PoC. Lightweight, docs-only, ~0.5d.

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d. Брать после `PROD-LLM-ALERT-PATH-ISOLATION`: пока классификация синхронна в ingest-пути, fallback-цепочка удлиняет приём алерта.

### 2. Code review 2026-10-21 — конвейер
> Приостановлен 2026-10-23 по ADR-018: паритетные позиции (`ROUTE-CONTINUE-NO-FANOUT`, `DURATION-UPSTREAM-UNITS`, `CONFIG-RELOAD-BUNDLE` и следующие) относятся к удаляемому ядру и не берутся. Записи остаются в `BUGS.md`/`TECH-DEBT.md`/`REVIEW-PIPELINE-2026-10-21.md` до `ADR-018-V3-PROXY-SLICING`; security/critical по переживающему переход коду возвращаются в Queue оттуда.

`CONFIG-RELOADER-SIDECAR` снят из Queue 2026-10-07: заблокирован `PROD-RELEASE-V010` и `CONFIG-RELOADER-AUTH`, статус — в BACKLOG § Near-term.

## Ссылки

- Прод-блокеры: `BACKLOG.md` § «Production Readiness — блокеры».
- Parity Phase B/C и Intelligence Phase 6C/6D/7 — в `BACKLOG.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
