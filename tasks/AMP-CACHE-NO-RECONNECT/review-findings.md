# Deep Review Findings: AMP не переподключается к кешу, недоступному на старте

**Trigger classification:** mandatory (Full tier: сигналы `C X R`)
**Reviewer perspective:** независимый агент-ревьюер (не автор кода), линзы correctness, premises, C, X, R, maintainability
**Reviewed at:** 2026-10-22
**Reviewed tree:** bugfix/amp-cache-no-reconnect @ eb86457
**Verdict:** pass (see `review-verdict.json`)

## Проверено (без находок)

- **Цикл ретрая** (`service_registry.go:1218-1278`): `wait = 0` или lite → одна попытка, сразу fallback; окно проверяется до сна (`time.Until(deadline) > backoff`), пауза 1s→×2→≤5s; после окна текст Warn и degraded reason — как на `main`. Утечки клиентов нет: `dialRedis` закрывает клиент при неудачном PING (`cache/redis.go:97-101`). Гонок нет: `initializeCache` идёт последовательно внутри `Initialize`, до него никто не читает `r.cache`.
- **Premise 1** (HTTP и пробы только после `Initialize`): верна, `cmd/server/main.go:119` → `ListenAndServe` в конце. Класс `code-read` заслужен.
- **Premise 2** (`startupProbe` ≥ 60s): верна, `values.yaml:183-188` + `deployment.yaml:314-323` дают 10s + 30×10s = 310s; `values-production.yaml` пробы не переопределяет. `initializeDatabase` не ретраит (ошибка → `os.Exit`), так что `Initialize` укладывается в ~65s худшего случая. Rolling update при `replicaCount: 1` (maxUnavailable 25% → 0, maxSurge → 1) держит старый под, пока новый ждёт: окно не добавляет простоя.
- **Premise 3** (все потребители берут клиент из `r.cache`): верна — `NewRedisCache` в prod-коде вызывается только из `initializeCache` (`lock/example.go` — примеры, `redis.go:553` — фабрика без вызовов из registry); type-assert'ы на `:788`, `:847`, `:911`, `:1938`, `:2040`, `service_registry_reload.go:226`. Класс `call-path-traced` заслужен.
- **Premise 5, 6**: верны (`deployment.yaml:234-236`; `AutomaticEnv` без `AllowEmptyEnv`). `REDIS_STARTUP_WAIT` резолвится, потому что ключ заведён через `SetDefault` (`config.go:922`).
- **C**: ключ аддитивный; `Validate` вызывается и из `LoadConfig`, которым пользуется reload coordinator (`reload_coordinator.go:365`) → отрицательное значение отвергается и при hot reload.
- **X**: `helm template` против merge-base: дефолты standard и `values-production.yaml` — `deployment.yaml` без изменений; `cache.enabled=false` и `profile=lite` — только `REDIS_STARTUP_WAIT: "0s"`. `helm lint` чист. NOTES-подсказка рендерится только для бандлового Valkey на upgrade — верно, внешний кеш upgrade не рестартует.
- `go build ./...`, `go vet` (config, application), `go test ./internal/config/` (311), `go test ./internal/application/ -run 'Cache|Cluster|Grouping|Storage'` (34) — зелёные.

## Findings

### F1 — Постоянные ошибки (WRONGPASS/NOAUTH, невалидный `CacheConfig`) ретраятся всё окно
- **Severity:** minor
- **Location:** `go-app/internal/application/service_registry.go:1241-1266`; `go-app/internal/infrastructure/cache/redis.go:51` (`config.Validate()`)
- **Issue:** цикл не различает транзиентные (dial refused/i/o timeout, `LOADING`) и постоянные ошибки. Неверный пароль или ошибка `CacheConfig.Validate` дают +60s к старту с тем же итогом (fallback). Класс ошибки уже случался в проде чарта (`AMP-CACHE-NOAUTH`, evidence A2.0: `NOAUTH Authentication required`). Spec Edge Case 1 это принимает; `startupProbe` (310s) выдерживает.
- **Recommendation:** follow-up: не ретраить ошибки аутентификации (`WRONGPASS`/`NOAUTH`) и ошибки валидации конфига — сразу fallback.
- **Disposition:** defer-backlog
- **Follow-up:** `docs/06-planning/BACKLOG.md` (на finalize): «startup cache retry: fail fast on auth/validation errors»

### F2 — Каждая неудачная попытка пишет ERROR `Failed to connect to Redis`
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/cache/redis.go:99` (вызывается из цикла через `NewRedisCache`)
- **Issue:** ожидаемая гонка на upgrade теперь даёт по ERROR-строке на каждую попытку (при `i/o timeout` попытка ~5s → ~7–8 ERROR за окно) плюс Warn `not reachable yet, retrying`. Алерты по ERROR-логам будут срабатывать на штатный upgrade, хотя итог — успешное подключение. Раньше ERROR был один и означал реальный fallback.
- **Recommendation:** понизить лог в `dialRedis` до Debug/Warn для стартового пути (Warn цикла уже несёт `error`) либо явно задокументировать. `dialRedis` общий с `Reload`, поэтому не в этой задаче.
- **Disposition:** defer-tech-debt
- **Follow-up:** `docs/06-planning/TECH-DEBT.md` (на finalize)

### F3 — Числа в Premise 4 и Edge Case 4 не совпадают с evidence/кодом
- **Severity:** minor
- **Location:** `tasks/AMP-CACHE-NO-RECONNECT/Spec.md` (Design Premises, Edge Cases §4), `requirements.md` (Symptom), `tasks.md` (Implementation Notes)
- **Issue:** (а) `AMP-CACHE-NOAUTH/evidence/k3d-acceptance.md` § A2.1: AMP стартовал 08:55:40, под AMP старше пода Valkey на ~6s, а ошибка — `dial tcp …: i/o timeout` (5s на попытку, не мгновенный refuse). Spec говорит «3 s». (б) Edge Case 4 оценивает перебег окна в ≤ ~10s (`dial_timeout` + 5s PING), но dial идёт внутри `Ping(pingCtx)` с таймаутом 5s (`redis.go:94`), так что попытка ограничена ~5s. (в) Класс `measured` заслужен только для k3d с локальными образами и local-path PV на одном узле: в многоузловом кластере (reattach RWO PV, pull образа, загрузка большого AOF) разрыв может превысить 60s. Итог тогда не хуже `main`, но премиса сформулирована шире, чем измерено.
- **Recommendation:** поправить числа в Spec/requirements; в k3d-приёмке (testing) зафиксировать фактическое число попыток и `waited`; в Premise 4 сузить формулировку до «single-node k3d».
- **Disposition:** fix-here (только task-артефакты; на gate не влияет)
- **Follow-up:** n/a

### F4 — `redis.startup_wait` участвует в drift `RedisReloadable`
- **Severity:** minor
- **Location:** `go-app/internal/config/reloadable_redis.go:127-131`, `:145-170`
- **Issue:** `changedFields` берёт все поля `RedisConfig`, поэтому изменение только `startup_wait` (параметр старта, клиент не трогает — `CacheConfigFrom` его не переносит) в standard даёт W601 с причиной про «split claim TTLs and delivered-state across two keyspaces» — неверное объяснение для этого ключа; если клиент не расшарен, вызовет лишний re-dial и swap идентичного клиента. Spec (API Contracts) это заявляет как намеренное, поведение безопасно.
- **Recommendation:** исключить `startup_wait` из drift (или отдельная причина «applies on next start»).
- **Disposition:** defer-tech-debt
- **Follow-up:** `docs/06-planning/TECH-DEBT.md` (на finalize)

### F5 — Проверка `startup_wait` встала под чужой комментарий в `Validate`
- **Severity:** nit
- **Location:** `go-app/internal/config/config.go:1172-1178`
- **Issue:** комментарий «Redis is optional… Validation only if Redis addr is provided» теперь стоит над проверкой `StartupWait < 0`, которая от addr не зависит.
- **Recommendation:** однострочный комментарий над проверкой, исходный оставить отдельно.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — Ветка `ctx.Done()` в проде недостижима
- **Severity:** nit
- **Location:** `go-app/internal/application/service_registry.go:1260-1264`; `cmd/server/main.go:97-121,163-170`
- **Issue:** `ctx` из `main` отменяется только `defer cancel()`, а `signal.Notify` ставится после `Initialize`: SIGTERM во время ожидания убивает процесс действием по умолчанию, ветка срабатывает только в тестах. Поведение приемлемо (дренировать нечего), но Spec Edge Case 2 описывает его неточно.
- **Recommendation:** в Spec пометить, что это контракт для тестов/встраивания; код не менять.
- **Disposition:** reject (reason: кодовых изменений не требует; поведение при SIGTERM корректно)
- **Follow-up:** n/a

### F7 — `REDIS_STARTUP_WAIT=60` без единицы молча становится 60ns
- **Severity:** nit
- **Location:** `go-app/internal/config/config.go:368` (viper/cast `ToDuration`)
- **Issue:** число без суффикса cast трактует как наносекунды → одна попытка без предупреждения.
- **Recommendation:** —
- **Disposition:** reject (reason: так ведут себя все duration-ключи конфига; доки и пример используют `60s`/`0s`)
- **Follow-up:** n/a

### F8 — NOTES/README жёстко пишут «60s»
- **Severity:** nit
- **Location:** `helm/amp/templates/NOTES.txt:45-46`, `helm/amp/README.md:117`
- **Issue:** у чарта нет value для окна; оператор, переопределивший `redis.startup_wait` через `configFile.content`, увидит неверную цифру.
- **Recommendation:** —
- **Disposition:** reject (reason: дефолт документирован как дефолт; отдельный value — расширение скоупа)
- **Follow-up:** n/a

## Deferred to testing

- Поздний старт Redis → `*RedisCache` без degraded; недоступный адрес → fallback с `attempts`/`waited`; lite и `0s` → одна попытка без задержки; отменённый ctx → быстрый fallback (Phase 3.1).
- k3d upgrade с рестартом AMP и Valkey: `Redis cache initialized` с `attempts > 1` без ручного рестарта, плюс фактические `waited` и число ERROR-строк (F2, F3).

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
