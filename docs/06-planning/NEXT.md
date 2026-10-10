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

> Сначала — P0 из `BACKLOG.md` § «Production Readiness», по порядку (правило «Прод-блокеры первыми»): `HELM-DEFAULTS-VALIDATE` (Waiting-on: решение владельца) → `PROD-GROUPING-DEFAULT` → `FU-TOPLEVEL-INHIBIT-RULES` → `PROD-HARDCODED-FILTER` → `PROD-RELEASE-V010` (отложен владельцем 2026-10-07) → `PROD-POSTGRES-HA-DECISION`. Задачи ниже берутся, когда P0 закрыты или заблокированы. Пересинхронизировано 2026-10-07 по аудиту 2026-10-06.

### Owner

- [ ] **PRIVATE-VULN-REPORTING** — включить Private vulnerability reporting в `ipiton/AMP` (Settings → Code security). `SECURITY.md` с 2026-10-05 направляет сообщения об уязвимостях туда, а сейчас `gh api repos/ipiton/AMP/private-vulnerability-reporting` → `{"enabled":false}`: канал не работает. Проверка — та же команда, `enabled: true`. `~1min`
  Trigger: owner ready.
- [ ] **MAIN-BRANCH-PROTECTION** — включить branch protection на `main` с required checks из `docs/CI.md`; до этого «зелёный CI обязателен» не enforced. Репозиторий пересоздан 2026-09-29, настроек защиты нет. `PROD-DEPS-OTEL-145` закрыт 2026-10-08 (локально и на `go1.26.8` `govulncheck` чистый): включать, когда после push `main` CI на нём зелёный, включая `govulncheck`. `~5min`
  Trigger: owner ready.

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d. Брать после `PROD-LLM-ALERT-PATH-ISOLATION`: пока классификация синхронна в ingest-пути, fallback-цепочка удлиняет приём алерта.

### 2. Operations

- [ ] **HELM-SINGLE-NODE-DEFAULTS** — дефолты чарта под одну ноду (HPA выкл., requests, PDB, пример `values-small.yaml`); первая P1 из BACKLOG, нужна для внедрения на однонодовый k3s. ~0.5–1d.

`CONFIG-RELOADER-SIDECAR` снят из Queue 2026-10-07: заблокирован `PROD-RELEASE-V010` и `CONFIG-RELOADER-AUTH`, статус — в BACKLOG § Near-term.

## Ссылки

- Прод-блокеры: `BACKLOG.md` § «Production Readiness — блокеры».
- Parity Phase B/C и Intelligence Phase 6C/6D/7 — в `BACKLOG.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
