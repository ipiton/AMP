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

- [ ] **SLACK-WEBHOOK-PLAIN-OK-RESPONSE** — Slack-клиент принимает ответ `ok` Incoming Webhook (text/plain) как успех, nflog для Slack-групп пишется; BUGS, high. Взят 2026-10-20, ветка `bugfix/slack-webhook-plain-ok`, workspace `tasks/SLACK-WEBHOOK-PLAIN-OK-RESPONSE/`.
## Queue

> Сначала — P0 из `BACKLOG.md` § «Production Readiness», по порядку (правило «Прод-блокеры первыми»): `PROD-RELEASE-V010` (отложен владельцем 2026-10-07). Задачи ниже берутся, когда P0 закрыты или заблокированы. Пересинхронизировано 2026-10-07 по аудиту 2026-10-06; `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16, `HELM-EXTERNAL-DATABASE` закрыт 2026-10-16; `PG-CONFIG-FIX` закрыт 2026-10-18; `PROD-POSTGRES-HA-DECISION` закрыт 2026-10-15; `HELM-SINGLE-NODE-DEFAULTS` закрыт 2026-10-19. `AMP-CACHE-NOAUTH` закрыт 2026-10-20.

### Owner

- [ ] **PRIVATE-VULN-REPORTING** — включить Private vulnerability reporting в `ipiton/AMP` (Settings → Code security). `SECURITY.md` с 2026-10-05 направляет сообщения об уязвимостях туда, а сейчас `gh api repos/ipiton/AMP/private-vulnerability-reporting` → `{"enabled":false}`: канал не работает. Проверка — та же команда, `enabled: true`. `~1min`
  Trigger: owner ready.
- [ ] **MAIN-BRANCH-PROTECTION** — включить branch protection на `main` с required checks из `docs/CI.md`; до этого «зелёный CI обязателен» не enforced. Репозиторий пересоздан 2026-09-29, настроек защиты нет. `PROD-DEPS-OTEL-145` закрыт 2026-10-08 (локально и на `go1.26.8` `govulncheck` чистый): включать, когда после push `main` CI на нём зелёный, включая `govulncheck`. С 2026-10-10 `govulncheck` на `main` был красный (GO-2026-6599…6617); `PROD-DEPS-NET-HTTP2` закрыт 2026-10-16 — локально `govulncheck` на `go1.26.9` чистый, release-gate PASS. Осталось: push `main`, дождаться зелёного CI (включая `govulncheck`), затем включить защиту. `~5min`
  Waiting-on: push `main` и зелёный CI на нём.

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d. Брать после `PROD-LLM-ALERT-PATH-ISOLATION`: пока классификация синхронна в ingest-пути, fallback-цепочка удлиняет приём алерта.

### 2. Operations

> Предпосылки запуска рядом с Alertmanager в sema-prod (параллельные нотификации), переоценка 2026-10-20.

- [ ] **SLACK-COLOR-TEMPLATE-REJECTED** — шаблонный `slack_configs[].color` валит загрузку конфига sema. BUGS high, ~0.25d.

- [ ] **HELM-VCT-CHART-LABELS** — метки VCT с версией чарта: первый бамп версии сломает `helm upgrade`; сделать до `PROD-RELEASE-V010`. BUGS, ~0.25d. Пополнено 2026-10-19.
- [ ] **AMP-CACHE-NO-RECONNECT** — AMP, не заставший кеш на старте, остаётся на in-memory до рестарта; после `AMP-CACHE-NOAUTH` воспроизводится на первом `helm upgrade` (k3d 2026-10-20). BUGS, medium, ~0.5d. Пополнено 2026-10-20.

`CONFIG-RELOADER-SIDECAR` снят из Queue 2026-10-07: заблокирован `PROD-RELEASE-V010` и `CONFIG-RELOADER-AUTH`, статус — в BACKLOG § Near-term.

## Ссылки

- Прод-блокеры: `BACKLOG.md` § «Production Readiness — блокеры».
- Parity Phase B/C и Intelligence Phase 6C/6D/7 — в `BACKLOG.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
