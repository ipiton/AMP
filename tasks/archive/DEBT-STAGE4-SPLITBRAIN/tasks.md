# Tasks — DEBT-STAGE4 (архитектурный долг)

- [x] 1. SPLIT-BRAIN слайс 1: DB-first alert ingest + rehydration (0b3de1d)
- [x] 2. SPLIT-BRAIN слайс 2: DB-first silences + rehydration + gc-инвалидация (636348f)
- [x] 3. DEDUP-STATE-STUB: Rule 7 dedup-фильтр (a5bb201)
- [x] 4. CORS-TODO: server.cors.* + middleware (f809a32)
- [x] 5. SIMPLE-PUBLISHER-PANIC: stub удалён (a34222a)
- [x] 6. ERROR-REINVENTION: миграция на pkg/httperror (3a34e05)
- [x] 7. DUPLICATED-DB-ADAPTERS: мёртвый PG-адаптер удалён, sentinel/SQL-фиксы (ce2f5f1)
- [x] 8. GLOBAL-LOCK-CONTENTION: закрыт бенчмарком, шардирование не нужно (9746a93)
- [x] 9. DTO-FRAGMENTATION: конверсии консолидированы в `core/alertconv`, единый fingerprint, groups видит сайленсы, −3004 строк

## Follow-ups вне scope (в BACKLOG.md)
- Runtime gaps из futureparity-закрытия (receivers JSON case, matcher value, method enforcement, silencedBy null, GroupAlerts receiver)
- Wiring SilenceManager/sync_worker; grouping/inhibition consistency (модули не в рантайме)
- Миграция deprecated pkg/metrics → v2 (нужны новые группы метрик)
