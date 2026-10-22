---
id: INHIBIT-SOURCE-CACHE-EVICTION
slug: inhibit-source-cache-eviction
stream: Inhibition
type: bug
status: active
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: источник inhibition выпадает из кеша через 5 минут горения

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-22

## Touched Files

- `go-app/internal/infrastructure/inhibition/cache.go` — время жизни по `EndsAt`, синк L2→L1, ёмкость
- `go-app/internal/core/services/alert_processor.go` — обновление кеша на ignored-дубликате
- `go-app/internal/application/service_registry.go` — провайдер `resolve_timeout`
- `go-app/internal/infrastructure/inhibition/CACHE_README.md`, `docs/ALERTMANAGER_COMPATIBILITY.md`, `CHANGELOG.md`
- `go-app/internal/infrastructure/inhibition/cache_test.go`, `integration_test.go`, `go-app/internal/core/services/*_test.go` — Phase 3

## Phase 1: Кеш источников

- [x] **1.1** `cacheEntry{alert, writtenAt, inRedis}`, `stampExpiry` (копия алерта, `EndsAt` или `now+resolveTimeout()`), `GetFiringAlerts` только из L1 с проверкой `EndsAt.After(now)`; `cleanup` только по `EndsAt`; опции `ResolveTimeout`, `SyncInterval`, удалить `TTL` <!-- verify: cd go-app && go build ./... -->
- [x] **1.2** Redis: TTL ключа до `EndsAt`, не писать истёкшее; `RemoveAlert` ставит tombstone <!-- depends: 1.1 | verify: cd go-app && go build ./... -->
- [x] **1.3** `syncFromRedis` + воркер (первый проход при старте, тикер `SyncInterval`, очистка тем же воркером); лог чтения Redis → DEBUG <!-- depends: 1.2 | verify: cd go-app && go build ./... && go vet ./internal/infrastructure/inhibition/ (vet компилирует тесты — красный до Phase 3 ожидаем, см. Notes) -->
- [x] **1.4** Ёмкость: сначала истёкшие, затем самый ранний `EndsAt` <!-- depends: 1.1 | verify: cd go-app && go build ./... -->

**Phase verification:** `cd go-app && go build ./...`

## Phase 2: Ingest и wiring

- [x] **2.1** `AlertProcessor.ProcessAlert`: ignored firing-дубликат → `AddFiringAlert(alert)` перед `return` <!-- verify: cd go-app && go build ./... && go test ./internal/core/services/... -->
- [x] **2.2** `initializeInhibition`: `NewTwoTierAlertCacheWithOptions` с `ResolveTimeout: resolveTimeoutFromConfig(r.config)` (читается на каждый вызов) <!-- verify: cd go-app && go build ./... && go test ./internal/application/... -->
- [x] **2.3** Доки: `CACHE_README.md`, `ALERTMANAGER_COMPATIBILITY.md` (Inhibition: время жизни источника, HA-синк ≤5s), `CHANGELOG.md` [Unreleased] Fixed <!-- verify: git diff --check -->

**Phase verification:** `cd go-app && go build ./... && go test ./internal/core/services/... ./internal/application/...`

## Phase 3: Тесты (после deep-review `pass`)

- [ ] **3.1** Переписать `cache_test.go`/`integration_test.go` под новую модель (Redis-путь `GetFiringAlerts` удалён, тип L1 изменён) <!-- verify: cd go-app && go test ./internal/infrastructure/inhibition/... -->
- [ ] **3.2** Источник горит 10 минут на фейковых часах, resend каждые 30s с новым `endsAt` и без — `ShouldInhibit` true на каждом шаге; на коде `main` — false после 5 минут <!-- verify: go test -run SourceLongLived ./internal/infrastructure/inhibition/... -->
- [ ] **3.3** Два экземпляра кеша на общем `MemoryCache`/miniredis: источник на A виден B после синка при непустом L1 B; resolve на A снимает на B; tombstone-гонка <!-- verify: go test -run Sync ./internal/infrastructure/inhibition/... -->
- [ ] **3.4** `AlertProcessor`: ignored-дубликат вызывает `AddFiringAlert` <!-- verify: go test -run Ignored ./internal/core/services/... -->
- [ ] **3.5** Ёмкость: вытесняется ближайший к resolve, истёкшие первыми <!-- verify: go test -run Capacity ./internal/infrastructure/inhibition/... -->

**Phase verification:** `cd go-app && go test -race ./internal/infrastructure/inhibition/... ./internal/core/services/...`

## Phase 4: Testing

- [ ] **4.1** Гейты `WORKFLOW.md` § Гейты AMP: затронутые пакеты, `make -C go-app quality-gates-fast`, `scripts/release-gate.sh` (один прогон; `lint` красный из-за предсуществующего `LINT-ERRCHECK-CHANNEL-RECEIVE`) <!-- verify: evidence/release-gate.txt -->

## Notes

- Deep-review @ `616aeef` → `fix_required` (`review-findings.md`). Fix-here: F1 — синк не вытесняет активные записи при полном L1, пропущенные — одним WARN за проход; WARN вытеснения на `AddFiringAlert` сведён в один за проход очистки (`capacityEvictions`); F2 — `inRedis` заменён на `redisConfirmedAt`, удаление «пропавших» — только подтверждённых до начала прохода; F3 — повторный tombstone и удаление записи из L1 после `Delete`/`SRem` (флаг `fromSync`, чтобы не тронуть более новый локальный add); F5 — premise переклассифицирована в `assumed`. Отложено в finalize: F1-корень, F4, F7 → TECH-DEBT; F5 (pub/sub), F6 → BACKLOG. Проверка: `go build ./...`; временная проба на копии `cache.go` (`go test -race -count=3`: долгоживущий источник, resolve на другой реплике, ёмкость при синке, гонка F3) — зелёная, удалена; тесты — в Phase 3 после `pass`.

- Implement 2026-10-22: все шаги Phase 1–2 выполнены; `go build ./...` OK, `go test ./internal/core/services/... ./internal/application/...` — 603 passed; `go vet ./...` красный только на `inhibition/cache_test.go` (тип `l1Cache`) — ожидаемо до Phase 3. Отклонений от Spec нет; дополнительно: `getFromRedis` возвращает ошибку вместо пустого списка (иначе синк принял бы недоступный Redis за пустой и снял чужие источники), транзиентная ошибка GET больше не удаляет член SET; лог «Inhibition cache full» на WARN при вытеснении активного алерта.

- Пакетные тесты `inhibition` обращаются к `l1Cache` как к `map[string]*core.Alert` и к удаляемому Redis-пути — до Phase 3 они не компилируются; поэтому verify Phase 1 — `go build`, а не `go test`/`go vet` пакета.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
