# NEXT

Queue и WIP — источник правды для выбора задачи. Закрытое сюда не пишется: отчёт — в `DONE.md`, история — в git.

## Flow Rules

<!-- Единственное место для чисел потока. Команды и другие документы ссылаются сюда, а не копируют. -->

- **WIP limit:** 2 — одна основная задача плюс один срочный фикс. Меняется только здесь.- **Прод-блокеры первыми:** секция «Production Readiness — блокеры» в `BACKLOG.md` приоритетнее всей Queue; порядок — в самой секции. Берутся в WIP напрямую из BACKLOG.
- **Replenish** Queue, когда в ней меньше трёх задач. Кандидата сначала проверить по `DONE.md` и `archive/DONE-*.md`, чтобы закрытое не вернулось.
- **Balance** maintenance (`BUGS`, `TECH-DEBT`) и roadmap (`ROADMAP`, `BACKLOG`) 50/50: по последним пяти закрытиям — если одной стороны больше трёх, следующую задачу брать с другой.
- **Owner inbox first:** на `start-task` разблокированное действие владельца (`Trigger: owner ready`, `Blocked-by:`, `Waiting-on:`) важнее первой позиции Queue. Больше 5 разблокированных — новые roadmap-задачи не берём.
- **Slice** задачи больше двух дней до старта.
- **Live state only:** ушедшая из WIP или Queue запись удаляется, а не зачёркивается и не комментируется.

## WIP

## Queue

> Сначала — P0 из `BACKLOG.md` § «Production Readiness», по порядку (правило «Прод-блокеры первыми»); открытых P0 нет: `PROD-RELEASE-V010` закрыт 2026-11-09 (`v0.1.0`). Пересинхронизировано 2026-10-07 по аудиту 2026-10-06; `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16, `HELM-EXTERNAL-DATABASE` закрыт 2026-10-16; `PG-CONFIG-FIX` закрыт 2026-10-18; `PROD-POSTGRES-HA-DECISION` закрыт 2026-10-15; `HELM-SINGLE-NODE-DEFAULTS` закрыт 2026-10-19. `AMP-CACHE-NOAUTH` закрыт 2026-10-20. `SLACK-WEBHOOK-PLAIN-OK-RESPONSE` закрыт 2026-10-20.

### ADR-019 — AMP на пакетах upstream Alertmanager

> Вердикт PoC принят владельцем 2026-10-24 (`DECISIONS.md` § ADR-019). Все семь срезов смержены 2026-11-09, `v0.1.0` выпущен 2026-11-09 (`PROD-RELEASE-V010`). Остаток эпика — `BACKLOG.md` § ADR-019.

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d. Брать после `UPSTREAM-AI-HOOKS` (срез 2 ADR-019, закрывает `PROD-LLM-ALERT-PATH-ISOLATION`): пока классификация синхронна в ingest-пути, fallback-цепочка удлиняет приём алерта.

### 2. Code review 2026-10-21 — конвейер
> Пересмотрен 2026-10-24 (`ADR-019-V2-SLICING`): позиции по удаляемому ядру сняты (`[-]` в `REVIEW-PIPELINE-2026-10-21.md`, записи — в секциях «Снимается переходом на upstream (ADR-019)» `BUGS.md`/`TECH-DEBT.md`). Голова живой части — №8. Остальные живые позиции берутся из `REVIEW-PIPELINE-2026-10-21.md` по порядку.

`CONFIG-RELOADER-SIDECAR` снят из Queue 2026-10-07: заблокирован `PROD-RELEASE-V010` и `CONFIG-RELOADER-AUTH`, статус — в BACKLOG § Near-term.

## Ссылки

- Прод-блокеры: `BACKLOG.md` § «Production Readiness — блокеры».
- Parity Phase B/C и Intelligence Phase 6C/6D/7 — в `BACKLOG.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
