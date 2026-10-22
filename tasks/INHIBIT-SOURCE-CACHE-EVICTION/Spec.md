---
id: INHIBIT-SOURCE-CACHE-EVICTION
slug: inhibit-source-cache-eviction
stream: Inhibition
type: bug
status: approved
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
  - research.md
---

# Specification: источник inhibition выпадает из кеша через 5 минут горения

**Version:** 1.0  
**Status:** Approved (Full tier, deep-review после implement)

## Summary

Источник inhibition живёт в кеше, пока активен по upstream-правилу (`EndsAt` в будущем, без `endsAt` — `resolve_timeout` от последнего приёма), обновляется на каждом ingest, включая точные дубликаты, и виден другим репликам через периодическую синхронизацию с Redis вместо «Redis только при пустом L1».

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Активен ⇔ `EndsAt` в будущем; без `endsAt` — `resolve_timeout` | Target Design §1, `stampExpiry`, `isActive` |
| Возраст (`StartsAt`) не вытесняет | Target Design §1, `cleanup` |
| Дубликат обновляет кеш | Target Design §2, `AlertProcessor.ProcessAlert` |
| Чужие источники видны ≤ интервала синка; resolve снимается | Target Design §3, `syncFromRedis` |
| Проверка активности в момент запроса | `GetFiringAlerts` фильтрует по `EndsAt.After(now)` |
| Тест 10 минут / две реплики | `tasks.md` Phase 3 |

## Current State

- **Code:** `internal/infrastructure/inhibition/cache.go` (`TwoTierAlertCache`: L1 `map[fp]*core.Alert`, L2 Redis `inhibition:active_alerts:<fp>` JSON + SET `inhibition:active_alerts:set`, TTL 5m; `cleanup` раз в минуту по `EndsAt` и `StartsAt+5m`; `GetFiringAlerts` — L1, Redis только при пустом L1); `internal/core/services/alert_processor.go:518-569` (Step 0 dedup → ранний `return` на ignored → Step 0.5 кеш); `internal/application/service_registry.go:1411` (`NewTwoTierAlertCache(r.cache, r.logger)`).
- **Data:** Redis-ключи выше; схема не меняется.
- **Tests:** `inhibition/cache_test.go` (часть тестов обнуляет `l1Cache`, чтобы пройти Redis-путь `GetFiringAlerts`, — этот путь уходит, тесты переписываются в write-tests), `inhibition/integration_test.go`, `services/alert_processor_*_test.go`.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md` (Inhibition), `inhibition/CACHE_README.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Upstream: источник активен ⇔ `!ResolvedAt(now)`, т.е. `EndsAt` после `now`; без `endsAt` API ставит `now + resolve_timeout` на каждом приёме | `alertmanager@v0.32.0/inhibit/inhibit.go:362-379`, `api/v2/api.go:350-356`, `prometheus/common/model/alert.go:69` | code-read | семантика разойдётся с upstream |
| Upstream обновляет `scache` на каждый Put алерта, включая неизменённый resend | `inhibit/inhibit.go:109-130` (подписка `SlurpAndSubscribe`, `processAlert` без сравнения) | code-read | дубликат не должен продлевать окно |
| Единственный писатель кеша источников — `AlertProcessor.ProcessAlert`; читатели — `DefaultInhibitionMatcher.ShouldInhibit/FindInhibitors` (ingest и `filterInhibited`) | `grep -rn "AddFiringAlert\|RemoveAlert(\|GetFiringAlerts"` по `go-app/` без тестов | call-path-traced | нужно обновлять кеш ещё где-то |
| В ingest-пути `core.Alert.EndsAt` у алерта без `endsAt` — `nil` (окно `resolve_timeout` ставит только `memory.AlertStore`) | `handlers/alerts.go:523-563`, `webhook/prometheus_parser.go:288-292`, `memory/alert_store.go:92-100` | code-read | двойная простановка окна |
| `r.cache` в standard — Redis или `MemoryCache` (оба реализуют SET-операции); общий для реплик только Redis | `service_registry.go:1218-1280`, `cache/memory.go:186-230` | code-read | синк без Redis — лишняя работа в своём процессе, но корректна |
| Чтение Redis (SMEMBERS + N×GET) на каждый `ShouldInhibit` недопустимо: вызов на каждый ingest и на каждый алерт группы | `grouping/manager_impl.go:1166-1201`, `cache/interface.go` (нет MGET) | code-read | можно было бы читать Redis напрямую (вариант A) |
| Задержка видимости чужого источника ≤ 5s приемлема: notify-chain проверяет inhibition на отправке, после `group_wait` (дефолт 30s) | `grouping/manager_impl.go:1166`; дефолт `group_wait` 30s | code-read | при `group_wait: 0` цель на другой реплике может уйти в первые ≤5s после источника — риск, Open Questions |

## Target Design

1. **Время жизни.** `AddFiringAlert` кладёт в L1 копию алерта: `EndsAt` сохраняется, если задан, иначе `now + resolveTimeout()` (провайдер из опций, по умолчанию `alertconv.DefaultResolveTimeout`; в реестре — `resolveTimeoutFromConfig(r.config)`, как у `memory.AlertStore`). `GetFiringAlerts` возвращает записи `firing` с `EndsAt.After(now)`. `cleanup` удаляет записи с `EndsAt` не позже `now`; правило `StartsAt + ttl` удаляется. Redis-ключ пишется с TTL до `EndsAt` (запись с уже прошедшим `EndsAt` в Redis не пишется).
2. **Дубликаты.** В `ProcessAlert` при `ProcessActionIgnored` для firing-алерта вызывается `AddFiringAlert(incoming)` перед ранним `return` — окно продлевается, Redis-ключ обновляется. Resolved-дубликат ничего не делает (источник уже снят первым resolve).
3. **HA-видимость.** `GetFiringAlerts` больше не ходит в Redis. Фоновый воркер раз в `SyncInterval` (дефолт 5s; первый проход сразу при старте) читает Redis-набор и сливает в L1: добавляет/обновляет записи, удаляет из L1 записи, подтверждённые в Redis (`inRedis`), которых там больше нет (resolve или истечение на другой реплике). Локальная запись или удаление, сделанные после начала прохода, побеждают прочитанное (метка `writtenAt` у записи, tombstone удалённого fingerprint до конца прохода). Записи, которые не удалось записать в Redis, синк не удаляет — они живут до своего `EndsAt`.
4. **Ёмкость.** При заполнении L1 сначала выбрасываются истёкшие, затем запись с самым ранним `EndsAt` (ближайшая к resolve), а не самая старая по `StartsAt`. Синк не добавляет записи сверх ёмкости.

## Component Architecture

- `go-app/internal/infrastructure/inhibition/cache.go` — запись `cacheEntry{alert, writtenAt, inRedis}`, `stampExpiry`, `syncFromRedis`, новые опции `ResolveTimeout func() time.Duration`, `SyncInterval`; опция `TTL` удаляется (TTL Redis теперь от `EndsAt`); часы `now` — поле структуры для тестов.
- `go-app/internal/core/services/alert_processor.go` — обновление кеша на ignored-дубликате.
- `go-app/internal/application/service_registry.go` — `NewTwoTierAlertCacheWithOptions(..., &AlertCacheOptions{ResolveTimeout: ...})`.
- Доки: `docs/ALERTMANAGER_COMPATIBILITY.md`, `go-app/internal/infrastructure/inhibition/CACHE_README.md`, `CHANGELOG.md` (Fixed).

## Security Design

- [x] Ownership — не применимо.
- [x] Input validation — не меняется.
- [x] Sensitive data не логируется (fingerprint, alertname, счётчики).
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] Активный источник не исчезает из `GetFiringAlerts` ни по возрасту, ни по ёмкости, пока firing-алертов меньше `l1Max`.
- [ ] Источник с прошедшим `EndsAt` не возвращается, даже если запись ещё в L1.
- [ ] Ошибки Redis не роняют ingest и не очищают L1 (graceful degradation, как раньше).
- [ ] Формат Redis-значений — JSON `core.Alert`; значения старой версии читаются.
- [ ] Алерт, переданный в `AddFiringAlert`, не мутируется (кеш хранит копию).

## Edge Cases

1. Webhook-источник без `endsAt`, resend каждые 30s ⇒ окно сдвигается на каждом приёме (включая ignored-дубликаты), цель подавлена всё время.
2. Prometheus-источник с `endsAt = now+4m`, resend раз в минуту ⇒ dedup видит новый `EndsAt` (updated), окно сдвигается.
3. Источник перестал присылаться ⇒ неактивен с `EndsAt` (как upstream), удалится очисткой.
4. Resolve на реплике B ⇒ B удаляет ключ и SET-член; A снимает источник на следующем синке (≤5s), до этого — подавляет, как раньше бы подавлял до resolve.
5. Синк прочитал Redis, локально пришёл resolve во время прохода ⇒ tombstone не даёт вернуть запись.
6. Redis недоступен ⇒ синк логирует WARN и оставляет L1; локальные источники работают; чужие не видны (как при пустом Redis).
7. Запись старой версии в Redis без `EndsAt` ⇒ при синке получает окно `now + resolve_timeout`; ключ сам истечёт по старому TTL ≤5m, после чего синк удалит её из L1.
8. Рестарт пода ⇒ первый синк сразу при старте восстанавливает источники из Redis (раньше это делал L2-путь при пустом L1).
9. Lite / без Redis (`r.cache == nil`) ⇒ только L1; синк не запускается.
10. Локальный resolve, у которого не прошёл `Delete` в Redis ⇒ tombstone защищает только от прохода, начатого до удаления; следующий проход вернёт запись из Redis до истечения её ключа (`EndsAt`). Так же было и раньше (L2-путь при пустом L1) — принимается, ошибка Redis логируется WARN.

## Impact Analysis

- **Affected modules:** inhibition, core/services (ingest), application (wiring).
- **Breaking changes:** нет в API/конфиге; поведение: источники подавляют дольше (по upstream).
- **New dependencies:** нет.
- **Risks:** фоновый синк — N×GET каждые 5s на реплику (N — число firing-алертов в Redis): при 1000 алертах ~200 GET/s; приемлемо, оптимизация — `INHIBIT-MATCH-PERF`/хэш в Redis. Задержка видимости ≤5s — см. Open Questions.

## Rollout / Rollback

- **Rollout:** обычный релиз; CHANGELOG `[Unreleased]` Fixed.
- **Rollback:** откат образа; данные в Redis совместимы в обе стороны.
- **Feature flag:** не применимо.

## Observability

- **Logs:** синк — DEBUG на успешный проход, WARN на ошибку Redis; лог «Retrieved alerts from Redis L2 cache» понижается до DEBUG (иначе INFO каждые 5s).
- **Metrics:** существующие `alert_history_inhibition_cache_*`; `operations_total{operation="sync"}` и `operation_duration_seconds{operation="sync"}` — через те же векторы. `hits/misses_total{tier="l2"}` теперь считают проходы синка (hit — Redis прочитан, miss — ошибка Redis), а не обращения `GetFiringAlerts` к Redis; `evictions_total` больше не растёт от вытеснения по возрасту.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** правило конвейера ревью — семантика Alertmanager → Full.
- **Discretionary triggers present:** large diff (с тестами), novel pattern (фоновый синк L2→L1 с tombstone).
- **Decision:** required.

## Open Questions

- [ ] Задержка видимости ≤5s в HA при `group_wait: 0` (assumed приемлемой) — если нужно строже, вариант C (pub/sub) из research.
- [ ] Ёмкость `l1Max` 1000 считает все firing-алерты — follow-up в finalize.
