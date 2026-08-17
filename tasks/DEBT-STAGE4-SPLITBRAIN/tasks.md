# Tasks — DEBT-STAGE4 (архитектурный долг)

- [x] 1. SPLIT-BRAIN слайс 1: DB-first alert ingest + rehydration (acab489)
- [x] 2. SPLIT-BRAIN слайс 2: DB-first silences + rehydration + gc-инвалидация (891edd5)
- [x] 3. DEDUP-STATE-STUB: Rule 7 dedup-фильтр (faed7e6)
- [x] 4. CORS-TODO: server.cors.* + middleware (d1cd982)
- [x] 5. SIMPLE-PUBLISHER-PANIC: stub удалён (c5e2acd)
- [x] 6. ERROR-REINVENTION: миграция на pkg/httperror (70e9279)
- [x] 7. DUPLICATED-DB-ADAPTERS: мёртвый PG-адаптер удалён, sentinel/SQL-фиксы (d89ef16)
- [x] 8. GLOBAL-LOCK-CONTENTION: закрыт бенчмарком, шардирование не нужно (c4a9a90)
- [x] 9. DTO-FRAGMENTATION: конверсии консолидированы в `core/alertconv`, единый fingerprint, groups видит сайленсы, −3004 строк

## Follow-ups вне scope (в BACKLOG.md)
- Runtime gaps из futureparity-закрытия (receivers JSON case, matcher value, method enforcement, silencedBy null, GroupAlerts receiver)
- Wiring SilenceManager/sync_worker; grouping/inhibition consistency (модули не в рантайме)
- Миграция deprecated pkg/metrics → v2 (нужны новые группы метрик)
