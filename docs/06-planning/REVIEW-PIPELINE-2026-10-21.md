# Конвейер задач по ревью 2026-10-21

Упорядоченный список claimable-задач из `CODE-REVIEW-2026-10-21.md`: от critical к low, внутри приоритета — с учётом зависимостей (задача не стоит раньше того, от чего зависит). Полные Problem/Fix — в записях `BUGS.md` / `TECH-DEBT.md` / `BACKLOG.md`; здесь только порядок.

Правила конвейера:
- Брать сверху вниз первую задачу, у которой закрыты все «После». Бандл берётся одним claim'ом (`TECH-DEBT.md` § Bundles); участника можно взять отдельно — тогда убрать его из списка бандла.
- Конвейер не отменяет `NEXT.md`: P0 из `BACKLOG.md` § «Production Readiness» и задачи Owner идут первыми; в Queue `NEXT.md` переносится только голова конвейера по мере освобождения WIP.
- Tier — по Risk Profile (`docs/solo-kanban/workflow.md` § Step Matrix): security и семантика Alertmanager — Full; refactor без изменения поведения — Standard; механика — Lightweight. Финальный tier подтверждает `start-task`.
- `fix:` и `refactor:` не смешивать в одном PR (CODE-REVIEW § 11, принципы).
- Закрытая задача: `[x]` здесь, запись удаляется из источника, строка в `DONE.md`.
- В Queue `NEXT.md` (§ 2) на 2026-10-24: №8; №1 закрыт 2026-10-22, №2–4 закрыты 2026-10-23.
- **Пересмотр 2026-10-24 (`ADR-019-V2-SLICING`).** AMP переходит на пакеты upstream Alertmanager (`DECISIONS.md` § ADR-019, эпик в `BACKLOG.md`). Позиции `[-]` относятся к удаляемому ядру: не берутся, их записи перенесены в секции «Снимается переходом на upstream (ADR-019)» `BUGS.md` / `TECH-DEBT.md` с номером среза. «После» на снятые позиции убраны. Живые позиции — security, investigation, Helm, гейты и код, который переживает переход; идут в прежнем порядке.
- `[-]` — снята решением, не закрыта: в `DONE.md` не пишется, закрывается вместе со срезом эпика ADR-019.

Обозначения источника: **B** — `BUGS.md`, **TD** — `TECH-DEBT.md`, **TD/Bundle** — `TECH-DEBT.md` § Bundles, **BL** — `BACKLOG.md`.

## Critical (~3d)

| № | | Задача | Источник | Tier | Оценка | После | Находки |
|---|---|---|---|---|---|---|---|
| 1 | [x] | `INHIBIT-SOURCE-CACHE-EVICTION` | B | Full | 1d | — | C-1 |
| 2 | [x] | `PUBLISH-QUICK-FIX-BUNDLE` | TD/Bundle | Full | 2d | — | C-2, H-4, M-7, M-9, M-10, M-11, L-2, L-19 |

## High (~6.5d)

| № | | Задача | Источник | Tier | Оценка | После | Находки |
|---|---|---|---|---|---|---|---|
| 3 | [x] | `EMAIL-RENDER-ESCAPING` | B | Full | 0.25d | — | H-3, L-1 |
| 4 | [x] | `SILENCED-ALERTS-INGEST` | B | Full | 0.5d | — | H-1 (breaking) |
| 5 | [-] | `ROUTE-CONTINUE-NO-FANOUT` | B (существовал) | Full | 1d | — | H-10; снят ADR-019, срез 5 |
| 6 | [-] | `DURATION-UPSTREAM-UNITS` | B | Standard | 0.25d | — | H-2; снят ADR-019, срез 5 |
| 7 | [-] | `CONFIG-RELOAD-BUNDLE` | TD/Bundle | Standard | 1.25d | — | H-5, H-6, M-19, M-20, M-22, L-5 (breaking: битые matchers); снят ADR-019, срез 5 (`CONFIG-ENV-KEYS-UNBOUND` остаётся в `BUGS.md` standalone) |
| 8 | [ ] | `INVESTIGATION-SAFETY-BUNDLE` | TD/Bundle | Full | 2.25d | — | H-7, H-8, H-9, L-7, L-8, L-9 |
| 9 | [-] | `SILENCE-REPO-INTEGRATION-TESTS` | B | Standard | 1d | — | H-11; снят ADR-019, срез 5 |

## Medium (~22.5d)

| № | | Задача | Источник | Tier | Оценка | После | Находки |
|---|---|---|---|---|---|---|---|
| 10 | [ ] | `QUALITY-GATES-ALIGN` | B | Lightweight | 0.25d | — | M-28 |
| 11 | [ ] | `DEAD-PKGS-NO-IMPORTERS` | TD | Standard | 0.5d | — | § 4.4 |
| 12 | [ ] | `DEAD-APPLICATION-MIDDLEWARE` | BL (расширен) | Standard | 1d | — | § 4.4, `SILENCES-CHECK-404` |
| 13 | [-] | `GROUP-STORE-LOST-UPDATE` | B | Full | 1d | — | M-1; снят ADR-019, срез 4 |
| 14 | [-] | `SILENCE-VALIDATION-PARITY` | B | Standard | 0.25d | — | M-2 (breaking); снят ADR-019, срез 5 |
| 15 | [-] | `API-COMPAT-BUNDLE` | TD/Bundle | Standard | 1.25d | — | M-15, M-17, L-3, L-4, L-6 (breaking: форма ошибок); снят ADR-019, срез 6 (`SERVER-HARDENING` остаётся в `BUGS.md` standalone) |
| 16 | [-] | `INHIBITION-LIFECYCLE-BUNDLE` | TD/Bundle | Standard | 1.5d | 1 | M-5, M-6; снят ADR-019, срез 5 |
| 17 | [-] | `STORE-RETENTION-GC` | B | Standard | 1d | — | M-3, M-4; снят ADR-019, срез 5 |
| 18 | [-] | `PUBLISH-SINGLE-RETRY-LAYER` | B | Full | 1.5d | 2 | M-8; снят ADR-019, срез 5 |
| 19 | [-] | `PUBLISH-HEALTH-PROBES` | B | Lightweight | 0.5d | — | M-12, M-13; снят ADR-019, срез 5 |
| 20 | [ ] | `HTTP-METRICS-ACCESS-LOG` | TD | Lightweight | 0.5d | — | M-14 |
| 21 | [ ] | `UI-ROUTE-PREFIX-LINKS` | B | Lightweight | 0.25d | — | M-18 |
| 22 | [ ] | `SQLITE-LITE-HARDENING` | B | Lightweight | 0.25d | — | M-27 |
| 23 | [-] | `DEAD-TEMPLATE-STACK` | TD | Standard | 0.5d | 3 | § 4.4; снят ADR-019, срез 5 |
| 24 | [-] | `DEAD-PUBLISHING-SURFACE` | TD | Standard | 0.5d | 2 | § 4.4; снят ADR-019, срез 5 |
| 25 | [ ] | `DEAD-CONFIG-SUBSYSTEMS` | TD | Standard | 0.5d | — | M-23 (breaking: удалённые ключи) |
| 26 | [-] | `LABELMATCH-PKG` | TD | Standard | 2d | 11, 12 | D-1, D-2, L-11; снят ADR-019, срез 5 |
| 27 | [-] | `API-FILTER-MATCHER-GRAMMAR` | B | Standard | 0.25d | 26 | M-16; снят ADR-019, срез 6 |
| 28 | [ ] | `METRICS-V2-ONLY` | TD | Standard | 1d | 11 | M-26 |
| 29 | [-] | `CONFIG-SCHEMA-PARITY` | TD | Standard | 1.5d | 6, 25 | M-21; снят ADR-019, срез 5 |
| 30 | [ ] | `CONFIG-GLOBAL-VIPER-STATE` | TD (существовал) | Standard | 0.5d | — | M-22 (корень) |
| 31 | [ ] | `LAYER-GATE-DEPGUARD` | TD | Standard | 1d | 11, 12, 25 | M-25 |
| 32 | [ ] | `DEPS-CLIENT-GO-BUMP` | TD | Standard | 0.5d | — | M-29 |
| 33 | [-] | `SERVICE-REGISTRY-SPLIT` | TD | Standard | 2d | 7, 12 | M-24; снят ADR-019, срез 5 |
| 34 | [-] | `NOTIFY-PIPELINE-EXTRACT` | TD | Standard | 2.5d (слайсы ≤1d) | 1, 13, 16, 26 | M-24; снят ADR-019, срез 5 |

## Low (~6d)

| № | | Задача | Источник | Tier | Оценка | После | Находки |
|---|---|---|---|---|---|---|---|
| 35 | [-] | `REDIS-STORAGE-BUNDLE` | TD/Bundle | Standard | 1.1d | — | L-12, L-13, L-14, L-15; снят ADR-019, срез 4 |
| 36 | [-] | `PUBLISH-RELOAD-CLIENT-LIFECYCLE` | B | Lightweight | 0.5d | 2 | L-17, L-18; снят ADR-019, срез 5 |
| 37 | [-] | `PUBLISH-TRANSPORT-CONSISTENCY` | B | Lightweight | 0.25d | — | L-20, L-21; снят ADR-019, срез 5 |
| 38 | [-] | `PUBLISH-CLIENT-PLUMBING` | TD | Standard | 0.5d | 18 | D-9, D-15, L-16; снят ADR-019, срез 5 |
| 39 | [-] | `TEMPLATE-EXEC-CACHE` | TD | Lightweight | 0.5d | — | L-10; снят ADR-019, срез 5 |
| 40 | [-] | `FILE-SECRET-PATH-BASE` | B | Lightweight | 0.25d | — | L-22; снят ADR-019, срез 5 |
| 41 | [-] | `W300-DELIVERABLE-SECRETS` | B | Lightweight | 0.1d | — | L-25; снят ADR-019, срез 5 |
| 42 | [ ] | `REDIS-FLUSH-ALL` | B | Lightweight | 0.1d | — | L-26 |
| 43 | [ ] | `K8S-CLIENT-HEALTH-TIMEOUT` | B | Lightweight | 0.25d | — | L-27 |
| 44 | [ ] | `RELOADER-WATCH-DEPENDENT-FILES` | TD | Lightweight | 0.5d | — | L-23 |
| 45 | [ ] | `HELM-AMP-SECCOMP` | TD | Lightweight | 0.1d | — | L-28 |
| 46 | [-] | `DEAD-SILENCING-CACHE` | TD | Standard | 0.5d | — | D-11, § 4.4; снят ADR-019, срез 5 |
| 47 | [ ] | `MIGRATIONS-EMBED` | TD | Lightweight | 0.25d | — | L-29 |
| 48 | [ ] | `DB-ORPHAN-TABLES` | TD | Standard | 0.25d | решение по `HISTORY-API` | § 4.4 |
| 49 | [ ] | `TESTS-INTEGRATION-TAG` | TD | Standard | 1d | — | § 8 |

Итого ~38d при WIP 1 (до пересмотра 2026-10-24; живых позиций с `CONFIG-ENV-KEYS-UNBOUND` и `SERVER-HARDENING` ~11.5d). Критический путь до снятия блокеров production-ready (CODE-REVIEW § 9) — №1–9 (~9.5d); после пересмотра из него живой только №8, остальное закрывает эпик ADR-019. Задачи без «После» в medium/low (`QUALITY-GATES-ALIGN`, `DEAD-PKGS-NO-IMPORTERS`, наблюдаемость) можно поднимать раньше при свободном WIP — порядок приоритета от этого не меняется.
