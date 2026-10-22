---
id: INHIBIT-SOURCE-CACHE-EVICTION
slug: inhibit-source-cache-eviction
stream: Inhibition
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-22
updated_at: 2026-10-22
---

# Research Pack - видимость и время жизни источников inhibition

## Goal

Уровень 2 (триггеры: несколько вариантов для HA-видимости, производительность горячего пути). Решить: (1) по какому правилу источник перестаёт быть активным; (2) как реплика видит источники других реплик без запросов в Redis на каждый `ShouldInhibit`.

## Sources

- upstream `alertmanager@v0.32.0`: `api/v2/api.go:335-360` (без `endsAt` ⇒ `EndsAt = now + resolve_timeout`), `inhibit/inhibit.go:109-130` (каждый Put алерта обновляет `scache`), `:362-379` (`findEqualSourceAlert` — `ResolvedAt(now)` в момент проверки), `scache.Run(15m)` — GC только resolved; `prometheus/common/model/alert.go:69` (`ResolvedAt`).
- AMP: `inhibition/cache.go`, `matcher_impl.go:87,186`, `grouping/manager_impl.go:1166-1201` (`filterInhibited` на каждый алерт группы), `cache/interface.go` (нет MGET/hash-операций), `service_registry.go:1218-1280` (`r.cache` — Redis или `MemoryCache`-fallback).

## Findings

- Upstream не вытесняет источник по возрасту: активен ⇔ `EndsAt` после `now`; дубликат тоже обновляет `scache`.
- `ShouldInhibit` вызывается на каждый ingest firing-алерта и на каждый алерт группы при отправке ⇒ чтение Redis (SMEMBERS + N×GET, MGET в интерфейсе нет) на каждый вызов — N сетевых запросов на алерт: неприемлемо.
- В HA upstream каждая реплика получает все алерты от Prometheus; в AMP балансировщик отдаёт алерт одной реплике ⇒ общий Redis — единственный канал.

## Options

Generation: один проход (уровень 2; minimal-diff и reuse-first совпадают — донора с периодическим пуллом L2 в репозитории нет, silence-sync идёт через pub/sub).

- **A. Читать Redis на каждый `GetFiringAlerts`.** Просто, но N запросов на вызов; отклонено (производительность).
- **B. Фоновая синхронизация L2→L1 каждые `SyncInterval` (5s) + write-through локальных изменений.** Горячий путь — только память; задержка видимости чужих источников ≤ интервала; при ошибке Redis L1 сохраняется. Нужна защита от гонки «синк прочитал Redis до локального remove» — tombstone на время прохода.
- **C. Pub/sub-инвалидация по образцу silence-sync.** Мгновенно, но новый канал, подписка, переподключения — больше 1d; отложено.
- **D. Источники из `memory.AlertStore`.** Память процесса, в HA не общая — (c) не закрывает; отклонено.

## Decision

- Время жизни: запись кеша хранит копию алерта с проставленным `EndsAt` (явный или `now + resolve_timeout`); активна ⇔ `firing` и `EndsAt.After(now)` в момент запроса; очистка удаляет истёкшие. TTL Redis-ключа = до `EndsAt`.
- Дубликат: в `ProcessAlert` обновить кеш для `ProcessActionIgnored` firing-алерта до раннего `return`.
- HA: вариант B, интервал 5s; первый синк — сразу при старте (восстановление после рестарта, как раньше давал L2-путь).

## Follow-ups

- [ ] Ёмкость L1 (`l1Max` 1000) считает все firing-алерты, не только источники: при >1000 алертов источник может быть вытеснен — проверить и завести BUG/TECH-DEBT в finalize (связано с `INHIBIT-MATCH-PERF`).
- [ ] Вариант C (pub/sub) — если задержки 5s окажется мало (`group_wait: 0`).
