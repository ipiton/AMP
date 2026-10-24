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
- [ ] **MAIN-BRANCH-PROTECTION** — включить branch protection на `main` с required checks из `docs/CI.md`; до этого «зелёный CI обязателен» не enforced. Репозиторий пересоздан 2026-09-29, настроек защиты нет. `PROD-DEPS-OTEL-145` закрыт 2026-10-08 (локально и на `go1.26.8` `govulncheck` чистый): включать, когда после push `main` CI на нём зелёный, включая `govulncheck`. С 2026-10-10 `govulncheck` на `main` был красный (GO-2026-6599…6617); `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16 — локально `govulncheck` на `go1.26.9` чистый, release-gate PASS. 2026-10-23 `govulncheck` на `main` снова красный (GO-2026-6809, stdlib `go1.26.9`); `PROD-DEPS-GO-127` поднял toolchain до `go1.27.3`, локально чисто. Осталось: push `main`, дождаться зелёного CI (включая `govulncheck`), затем включить защиту. `~5min`
  Waiting-on: push `main` и зелёный CI на нём.
- [ ] **ADR-019-POC-VERDICT** — принять вердикт PoC ADR-019 (`UPSTREAM-CORE-POC-GATE`, `DONE.md` 2026-10-24: проходит, расхождения — `tasks/archive/UPSTREAM-CORE-POC-GATE/divergences.md`) или остановить эпик; заодно решить `/debug/*` на upstream-ядре (pprof и expvar открыты: как upstream / вырезать в wiring / за флагом) — нужно к срезу 5. Принят — срезы 2–4 из Queue свободны. `~15min`
  Trigger: owner ready.

### ADR-019 — AMP на пакетах upstream Alertmanager

> После `ADR-019-POC-VERDICT`. Срезы 2–4 независимы; описание — `BACKLOG.md` § ADR-019.

- [ ] **UPSTREAM-AI-HOOKS** — срез 2: AI-слой в `AlertStoreCallback` и своей notify-стадии. Full, 2–3d.
  Blocked-by: ADR-019-POC-VERDICT.
- [ ] **UPSTREAM-NON-UPSTREAM-RECEIVERS** — срез 3: интеграции upstream `notify/*`, свои — Rootly и K8s-Secret таргеты. Standard, 2–3d.
  Blocked-by: ADR-019-POC-VERDICT.
- [ ] **UPSTREAM-HA-MEMBERLIST** — срез 4: `cluster.Peer`, gossip, снапшоты на PVC, StatefulSet. Full, 3–5d.
  Blocked-by: ADR-019-POC-VERDICT.


### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d. Брать после `UPSTREAM-AI-HOOKS` (срез 2 ADR-019, закрывает `PROD-LLM-ALERT-PATH-ISOLATION`): пока классификация синхронна в ingest-пути, fallback-цепочка удлиняет приём алерта.

### 2. Code review 2026-10-21 — конвейер
> Пересмотрен 2026-10-24 (`ADR-019-V2-SLICING`): позиции по удаляемому ядру сняты (`[-]` в `REVIEW-PIPELINE-2026-10-21.md`, записи — в секциях «Снимается переходом на upstream (ADR-019)» `BUGS.md`/`TECH-DEBT.md`). Голова живой части — №8. Остальные живые позиции берутся из `REVIEW-PIPELINE-2026-10-21.md` по порядку.

- [ ] **INVESTIGATION-SAFETY-BUNDLE** — `TECH-DEBT.md` § Bundles: `LLM-RETRY-CLASSIFY`, `INVESTIGATION-ASYNC-DEDUP`, `INVESTIGATION-PROMPT-ISOLATION`. Full, ~2.25d — нарезать на `start-task`. Код investigation переживает переход на upstream; с `UPSTREAM-AI-HOOKS` пересекается только точкой постановки investigation (`PostStore`).

`CONFIG-RELOADER-SIDECAR` снят из Queue 2026-10-07: заблокирован `PROD-RELEASE-V010` и `CONFIG-RELOADER-AUTH`, статус — в BACKLOG § Near-term.

## Ссылки

- Прод-блокеры: `BACKLOG.md` § «Production Readiness — блокеры».
- Parity Phase B/C и Intelligence Phase 6C/6D/7 — в `BACKLOG.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
