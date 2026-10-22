---
id: INHIBIT-SOURCE-CACHE-EVICTION
slug: inhibit-source-cache-eviction
stream: Inhibition
type: bug
priority: critical
status: active
created_at: 2026-10-22
updated_at: 2026-10-22
---

# Requirements: источник inhibition выпадает из кеша через 5 минут горения

## Problem Framing

- **Symptom:** цель inhibit-правила перестаёт подавляться, если источник горит дольше 5 минут. Для webhook-источников без `endsAt` — навсегда; для Prometheus — «дырами» до минуты. На HA-развёртывании источник, принятый другой репликой, не подавляет цель, пока в локальном кеше есть хоть один алерт.
- **Root Cause:** три дефекта `TwoTierAlertCache` и ingest-пути (CODE-REVIEW-2026-10-21 C-1, проверено чтением кода):
  - (a) `cleanup` (`internal/infrastructure/inhibition/cache.go:426-445`) вытесняет из L1 по `StartsAt + ttl(5m)`, а не по окончанию алерта;
  - (b) точный дубликат (`ProcessActionIgnored`) делает `return nil` в `AlertProcessor.ProcessAlert` (`internal/core/services/alert_processor.go:537-541`) до обновления кеша: источник не возвращается, Redis-ключ (TTL 5m) истекает;
  - (c) `GetFiringAlerts` (`cache.go:233-262`) читает Redis только при пустом L1.
- **Why Now:** базовая функция Alertmanager работает неверно; голова конвейера ревью (`REVIEW-PIPELINE-2026-10-21.md` №1), блокер production-ready.
- **How We Measure:** источник горит 10+ минут с resend каждые 30s (с новым `endsAt` и без него) — цель подавлена всё время, и на ingest, и в `filterInhibited` при отправке; источник, принятый одной репликой, подавляет цель на другой.

## Risk Profile

- **Signals:** `X R`
  - `X` — `internal/infrastructure/inhibition` (кеш), `internal/core/services` (ingest-путь), `internal/application` (wiring `resolve_timeout`).
  - `R` — меняется рантайм-поведение подавления уведомлений; горячий путь ingest и отправки группы.
- **Tier:** Full
- **Notes:** по сигналам — Standard, но правило конвейера ревью (`REVIEW-PIPELINE-2026-10-21.md`): семантика Alertmanager — Full; diff с тестами ожидаемо больше ~200 строк. Deep-review обязателен.

## User Stories

1. Как дежурный, я хочу, чтобы `NodeDown` подавлял `InstanceDown` той же ноды всё время, пока `NodeDown` горит, чтобы не получать каскад уведомлений через 5 минут после начала инцидента.
2. Как оператор HA-установки AMP, я хочу, чтобы inhibition работал одинаково независимо от того, на какую реплику балансировщик отправил источник.

## Success Criteria

- [ ] Источник активен, пока его `EndsAt` в будущем; без `endsAt` — `EndsAt = время приёма + global.resolve_timeout`, каждый resend продлевает окно (как upstream `api/v2.insertAlerts` + `types.Alert.ResolvedAt`).
- [ ] Возраст источника (`StartsAt`) на вытеснение не влияет.
- [ ] Точный дубликат firing-алерта обновляет запись источника в кеше (и окно, и Redis-ключ).
- [ ] Источник, записанный в Redis одной репликой, виден `GetFiringAlerts` другой реплики не позже интервала синхронизации, даже когда её локальный L1 не пуст; resolve на одной реплике снимает источник на другой не позже того же интервала.
- [ ] Источник с истёкшим `EndsAt` не подавляет цель даже до очередного прохода очистки (проверка в момент запроса, как upstream `findEqualSourceAlert`).
- [ ] Тест: источник горит 10 минут (фейковые часы) с resend каждые 30s — с новым `endsAt` и без — цель подавлена всё время; вариант с двумя экземплярами кеша на общем Redis.

## Non-Goals

- Индекс источников по правилу и один снапшот на `filterInhibited` — `INHIBIT-MATCH-PERF` (TECH-DEBT).
- Жизненный цикл `InhibitionState`, `/api/v2/inhibitions` — `INHIBITION-LIFECYCLE-BUNDLE`.
- Silence до ingest (`SILENCED-ALERTS-INGEST`) — отдельная задача.
- Смена хранилища источников на `memory.AlertStore` (память процесса, в HA не общая).

## Constraints

- **Scope:** `inhibition/cache.go`, `services/alert_processor.go`, wiring в `application/service_registry.go`, доки совместимости и CHANGELOG.
- **Security:** не применимо (нет auth/PII; в логах только fingerprint и alertname, как сейчас).
- **Compatibility:** формат Redis-ключей `inhibition:active_alerts:*` (JSON `core.Alert`) и SET сохраняются; записи старой версии читаются. Конфиг не меняется. Поведение меняется только в сторону upstream (дольше подавляет — так и должно быть).

## Discovery Notes

- Similar tasks: `tasks/archive/parity-a2-inhibition-pipeline/` (ввод кеша и Step 0.5), `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/`; `PARITY-RESOLVE-TIMEOUT-ENDSAT` (окно `resolve_timeout` в `memory.AlertStore`).
- Relevant patterns: `memory.AlertStore.SetResolveTimeout` + `ServiceRegistry.newAlertStore` (провайдер `resolve_timeout`, читаемый на каждый ingest); upstream `alertmanager@v0.32.0/inhibit/inhibit.go`, `api/v2/api.go:335-360`.
- Open unknowns: способ видимости источников другой реплики без Redis-запросов на горячем пути — в `research.md`.
