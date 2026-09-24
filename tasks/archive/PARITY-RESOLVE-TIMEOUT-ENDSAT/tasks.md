# Implementation Checklist: PARITY-RESOLVE-TIMEOUT-ENDSAT

Ветка `claude/determined-meitner-bn0ncl`. Источник: `Spec.md` (решения D1-D6, критерии AC1-AC12). Пути — относительно `go-app/`.

Слайс один, резать не нужно: ~0.5d, три файла кода, хендлеры не меняются. Порядок шагов выбран так, чтобы после каждого из них сборка и существующие тесты оставались зелёными.

## Research & Spec
- [x] Research — `research.md` (F1: баг в фолбэке `ToGettableAlert`; F2: upstream считает `receivedAt + rt`; F4: нельзя штамповать до dedup; F7: авто-резолва нет → follow-up)
- [x] Spec — `Spec.md` (D1-D6). Поправка research на `/spec`: rehydration из БД существует, поэтому штамповка идёт в стор (D2)
- [x] Follow-up `RESOLVE-TIMEOUT-AUTO-RESOLVE` заведён в BACKLOG

## Implementation

- [x] **S1. Константа и защитный фолбэк** — `internal/core/alertconv/alertconv.go`:
  - `const DefaultResolveTimeout = 5 * time.Minute`, в комментарии — upstream-смысл и ссылка на `routing.GlobalConfig.Defaults()`;
  - `ToGettableAlert` (D6): если `EndsAt` пуст, firing ⇒ `UpdatedAt + DefaultResolveTimeout` (разобрать RFC3339-строку `UpdatedAt`; если она не парсится — оставить старое поведение, не паниковать); resolved ⇒ `UpdatedAt`, как сейчас;
  - обновить doc-комментарий функции.
- [x] **S2. Провайдер таймаута в сторе** — `internal/infrastructure/storage/memory/alert_store.go`:
  - поле `resolveTimeout func() time.Duration` под тем же `mu`;
  - `SetResolveTimeout(fn func() time.Duration)` в стиле `SetOnChange`;
  - приватный `currentResolveTimeout()`: `nil` или `<= 0` ⇒ `alertconv.DefaultResolveTimeout`. Читать провайдер **до** взятия `mu` на запись в `apply`, чтобы не держать лок, пока вызывается чужой код.
- [x] **S3. Штамповка при нормализации (D2)**:
  - `normalizeIngestInput` и `storedStateFromAlert` сейчас свободные функции. Передать в них `timeout time.Duration` параметром (не делать методами на `*AlertStore`, так они проще тестируются), значение берётся один раз на батч в `ingestBatchInternal` / `RestoreFromPersistence`;
  - строго **после** `NormalizeStatus`: `if status == "firing" && endsAt == nil { t := now.Add(timeout); endsAt = &t }`;
  - `resolveAlertLocked` и resolved-ветки не трогать (AC5).
- [x] **S4. Проводка из конфига (D3)** — `internal/application/service_registry.go`:
  - хелпер `resolveTimeoutFromConfig(cfg *appconfig.Config) time.Duration`: `cfg == nil`, `Routing == nil`, `Global == nil`, `ResolveTimeout == nil` или `<= 0` ⇒ `alertconv.DefaultResolveTimeout`;
  - сразу после `memory.NewAlertStore()` (`initializeInfrastructure`, ~`:463`): `r.alertStore.SetResolveTimeout(func() time.Duration { return resolveTimeoutFromConfig(r.config) })`. Замыкание читает `r.config` на каждый вызов, поэтому `/-/reload` подхватывается без дополнительной проводки;
  - проверить, что `rehydrateAlertStore` вызывается после этой строки (провайдер должен быть на месте к рестору).
- [x] **S5. Проверка «хендлеры не меняются».** `git diff --stat` не содержит `internal/application/handlers/alerts.go`; `core.Alert.EndsAt` до `ProcessAlert` остаётся `nil` (закрывается тестом T6).

### Результат `/implement` (2026-09-24)

- Диф: `alertconv.go` +19, `alert_store.go` +60/−5, `service_registry.go` +18. `handlers/alerts.go` не тронут (S5): штамповка идёт в нормализации стора на копии, `core.Alert` уходит в `ProcessAlert` раньше и без `endsAt`.
- Отклонение от плана в S3: хелпер `stampResolveTimeout(status, endsAt, now, timeout)` вынесен отдельной функцией, общей для обоих путей нормализации. В `storedStateFromAlert` статус теперь считается один раз до штамповки (раньше вычислялся прямо в литерале), значение то же.
- Уже прогнано: `go build ./...`; `go vet` на трёх пакетах; `go test` на `alertconv`, `storage/memory`, `internal/application/...` — зелёные, существующие ассерты не пришлось править; `go test ./cmd/server -tags futureparity` — зелёный. `gofmt -l` показывает только 5 чужих файлов из `QUALITY-GATES-DIRTIES-TREE`, новых нет.
- Временная sanity-проверка (тест-файл удалён): повторный ingest в 10:01 ⇒ `endsAt=10:06`, `updatedAt=10:01`; rehydration с провайдером 1h ⇒ `now+1h`, у ранее сохранённого алерта `endsAt` не пересчитан.

## Testing

Тесты пишутся на `/write-tests`, здесь их состав. Отдельного `alert_store_test.go` сейчас нет (есть только `alert_store_bench_test.go`), создаём новый файл.

- [x] **T1. Стор, дефолт (AC1)** — `memory/alert_store_test.go`: `NewAlertStore()` без провайдера, `IngestBatch` firing без `endsAt` ⇒ `List` отдаёт `EndsAt == now + 5m`.
- [x] **T2. Стор, кастомный провайдер и reload (AC2, AC9)**: `SetResolveTimeout(1h)` ⇒ `now + 1h`; сменить провайдер на 10m, снова ingest другого алерта ⇒ 10m, у первого остаётся 1h (без ретроактивного пересчёта). Провайдер `0`/отрицательный ⇒ дефолт.
- [x] **T3. Явный `endsAt` и resolved (AC3, AC5)**: явный будущий `endsAt` сохраняется, в том числе при повторном POST; `status: resolved` без `endsAt` ⇒ время резолва; `endsAt` в прошлом ⇒ resolved, не перештампован.
- [x] **T4. Продление (AC4)**: два ingest одного алерта без `endsAt` с `now1 < now2` ⇒ `EndsAt == now2 + rt`, `UpdatedAt == now2`.
- [x] **T5. Rehydration (AC6)**: `RestoreFromPersistence([]*core.Alert{firing, EndsAt: nil}, now)` ⇒ `now + rt`; исходный `*core.Alert` не мутирован.
- [x] **T6. HTTP-уровень (AC1, AC7, AC8)** — `handlers/alerts_test.go` через `fakeRegistry`: POST postable-массива без `endsAt` ⇒ `GET /api/v2/alerts`, `GET /api/v1/alerts`, `GET /api/v2/alerts/groups` отдают один и тот же `endsAt > updatedAt`, ≈ `receivedAt + 5m`. Если у `fakeRegistry` есть наблюдаемый `AlertProcessor`/dedup, проверить, что в процессор ушёл `EndsAt == nil`; если нет — зафиксировать AC8 unit-тестом на `toAlertIngestInput`/порядок вызовов и отметить здесь, чем именно закрыт.
- [x] **T7. Проводка конфига (AC2, AC9)** — `internal/application`: табличный тест `resolveTimeoutFromConfig` (nil cfg / nil Routing / nil Global / nil ResolveTimeout / `1h`); тест, что после подмены `r.config` провайдер стора отдаёт новое значение.
- [x] **T8. `alertconv` (AC10, AC11)** — `alertconv_test.go`: фолбэк firing ⇒ `UpdatedAt + 5m`, resolved ⇒ `UpdatedAt`; `DefaultResolveTimeout == 5m` и совпадает с `routing.GlobalConfig{}.Defaults()` (если импорт `routing` из `alertconv_test` даёт цикл — тест кладём в `internal/application`).
- [x] **T9. Негативная проверка.** Временно убрать штамповку из S3 и убедиться, что T1/T4/T5/T6 краснеют; вернуть. Тест, который не ловит регресс, бесполезен.
- [x] **T10. Регрессы существующих тестов.** Прогнать `go test ./internal/... -count=1`; ассерты вида «firing ⇒ `EndsAt == nil`» или «`endsAt == updatedAt`» править под новый контракт с комментарием-ссылкой на задачу, не ослаблять.
- [x] **T11. Гейты (AC12)**: `go build ./...`, `go vet ./...`, `go test ./... -count=1`; `-race` на `internal/infrastructure/storage/memory` и `internal/application/handlers`; `go test ./cmd/server -tags futureparity -count=1` без новых падений; `git diff --check`. Флейк `PUBLISHING-WARMUP-TEST-FLAKY` не считается регрессом, но фиксируется, если выстрелит.
- [x] **T12. Живая проверка (по возможности)**: lite-инстанс, `curl -XPOST /api/v2/alerts` без `endsAt` → `GET` показывает `endsAt ≈ now + 5m`; повторный POST сдвигает окно. Если поднять не получится — записать, что не проверено вживую.

### Результат `/write-tests` (2026-09-24)

Новые тесты (13 тест-функций), все зелёные, в том числе под `-race`:

| Файл | Тесты | Закрывает |
|---|---|---|
| `internal/infrastructure/storage/memory/alert_store_test.go` (новый) | `TestAlertStore_FiringWithoutEndsAt_DefaultResolveTimeout`, `_ResolveTimeoutProvider`, `_ResolveTimeoutProvider_NonPositiveFallsBackToDefault`, `_ResendExtendsResolveTimeoutWindow`, `_ExplicitEndsAtIsKept`, `_ResolvedAlertsAreNotStamped`, `_RestoreFromPersistence_StampsFiringWithoutEndsAt` | T1-T5; AC1-AC6, AC9 |
| `internal/core/alertconv/alertconv_test.go` | `TestToGettableAlert_EmptyEndsAtFallback` | T8; AC10 |
| `internal/application/resolve_timeout_test.go` (новый) | `TestResolveTimeoutFromConfig`, `TestDefaultResolveTimeout_MatchesGlobalConfigDefaults`, `TestNewAlertStore_FollowsConfigReload`, `TestRehydrateAlertStore_StampsResolveTimeoutFromConfig` | T7, T8; AC2, AC6, AC9, AC11 |
| `internal/application/handlers/alerts_endsat_test.go` (новый) | `TestAlertsHandler_PostWithoutEndsAt_ServesResolveTimeout`, `TestAlertsHandler_PostWithExplicitEndsAt_IsKept` | T6; AC1, AC3, AC7, AC8 |

- **Изменение кода ради тестируемости:** создание стора с проводкой вынесено из `initializeInfrastructure` в `ServiceRegistry.newAlertStore()`. Поведение то же, но проводку можно проверить без БД.
- **Как закрыт AC8 (открытый вопрос из плана).** `fakePublisher` в HTTP-тесте получает ровно тот `*core.Alert`, что прошёл через `ProcessAlert`; тест проверяет, что у него `EndsAt == nil`. Поведение dedup (`Ignored` на повторный POST) отдельно не тестируется: тестовый процессор собран без dedup-сервиса. Но dedup сравнивает только `core.Alert.EndsAt`, а он не меняется, поэтому этого достаточно. Сознательно не расширяли.
- **T9, негативная проверка:**
  - Вариант 1, штамповка в сторе выключена: краснеют 5 тестов стора и 2 теста `application` (reload и rehydration). HTTP-тест остаётся зелёным, и это ожидаемо: защитный фолбэк `ToGettableAlert` (`updatedAt + 5m`) даёт то же значение. Защита в два слоя, каждый слой покрыт своим тестом.
  - Вариант 2, исходное поведение целиком (без штамповки и со старым фолбэком): краснеют `TestToGettableAlert_EmptyEndsAtFallback` и HTTP-тест `…PostWithoutEndsAt_ServesResolveTimeout`.
  - Код восстановлен, диф с коммитом `ba8aac6` пуст.
- **T10.** Существующие тесты править не пришлось: `go test -race` по `alertconv`, `storage/memory` и `internal/application/...` зелёный.
- **Отложено на `/testing`:** T11 (полный `go test ./...`, `futureparity`, `git diff --check`) и T12 (живая проверка).

### Результат `/testing` (2026-09-24)

**Зелёное:**
- `go build ./...`, `go vet ./...` — чисто.
- `go test ./... -count=1` — 50 пакетов `ok`, красные только 3 пакета из-за внешнего блокера (ниже).
- `go test -race` по `alertconv`, `storage/memory`, `internal/application/...` — зелёный.
- `go test ./cmd/server -tags futureparity -count=1` — зелёный.
- `git diff main --check` — чисто; `gofmt -l` по изменённым Go-файлам — пусто.
- Флейк `PUBLISHING-WARMUP-TEST-FLAKY` в этом прогоне не выстрелил.

**Красное — внешний блокер среды, не этот диф:**
- `internal/infrastructure/repository` (8 тестов `postgres_history`), `internal/infrastructure/inhibition` (2 теста `…_Redis`), `internal/database` (`TestRunMigrations_ConcurrentReplicas_FreshDB`). Все — testcontainers.
- Первый прогон: `rootless Docker not found` (демон не был запущен). После ручного `dockerd` — `429 Too Many Requests` от Docker Hub на `postgres:16-alpine` и `redis:7-alpine`, прямой `docker pull` падает так же. Дальше не ретраили (правило «гейт упал дважды — стоп»).
- Эти пакеты (Postgres-репозиторий истории, Redis-кэш inhibition, миграции) диф не затрагивает: он меняет memory store, `alertconv` и проводку в `ServiceRegistry`. Вывод «не регресс» сделан по коду, а не прогоном: прогнать их в этой среде не удалось.

**Живая проверка (T12)** — lite-профиль на `127.0.0.1:19093`, сборка с этой ветки:

| Сценарий | Результат |
|---|---|
| POST без `endsAt`, конфиг без `global:` | `startsAt=updatedAt=17:51:14`, `endsAt=17:56:14`, `state=active` |
| Повторный POST с тем же `startsAt` | одна запись, `updatedAt 17:51:34→37`, `endsAt` сдвинулся до `17:56:37` |
| POST с `endsAt: 2099-01-01` | отдаётся как есть |
| `/api/v1/alerts`, `/api/v2/alerts/groups` | те же `endsAt`, что в `/api/v2/alerts` |
| `global.resolve_timeout: 1h` | `endsAt = приём + 1h` |
| `/-/reload` на `2m`, новый POST | новый алерт `+2m`, старый остался `+1h` |
| рестарт (rehydration из SQLite) | оба алерта получили `рестарт + 2m` — текущий таймаут, свежее окно (Spec D3) |

Контроль на сборке `main`: POST без `endsAt` ⇒ `startsAt=updatedAt=endsAt` — исходный баг воспроизведён вживую, фикс его закрывает.

**Новая находка, вне скоупа (записана в BUGS.md как `ALERT-STORE-DEDUP-KEY-STARTSAT`).** Повторный POST **без `startsAt`** не продлевает окно, а создаёт вторую копию алерта. Ключ дедупликации стора — `fingerprint|startsAt`, а пустой `startsAt` каждый раз становится `now`. Воспроизводится и на `main` (две записи `MainNoStart` с разными `startsAt`), то есть это не регресс. Upstream сливает такие алерты по fingerprint и берёт самый ранний `startsAt`. Prometheus и `amtool` шлют `startsAt` сами, поэтому затронуты в основном curl-клиенты. Для них продление из этой задачи не срабатывает: окно продлевает только новая копия.

## Documentation

- [x] **D1.** `docs/ALERTMANAGER_COMPATIBILITY.md`: поведение `endsAt`/`global.resolve_timeout` (время приёма + таймаут, продление, reload); known limitations: нет авто-резолва по таймауту (`RESOLVE-TIMEOUT-AUTO-RESOLVE`), смешанные клиенты (Spec D5), в БД `ends_at` пуст (ADR-010).
- [x] **D2.** `CHANGELOG.md` `[Unreleased]` → `### Fixed`: алерты без `endsAt` больше не отдаются как отгоревшие.
- [x] **D3.** `docs/06-planning/DECISIONS.md`: ADR-010 «Таймаутный `endsAt` — значение API-слоя, БД хранит присланное» (Spec D1).
- [x] **D4.** Doc-комментарии у `SetResolveTimeout`, `DefaultResolveTimeout` и изменённого фолбэка.

### Результат `/write-doc` (2026-09-24)

- `docs/ALERTMANAGER_COMPATIBILITY.md`: строка в Feature Parity Matrix (🟡 — `endsAt` есть, авто-резолва нет) и Known Gap #12. В #12 четыре пункта: нет авто-резолва, БД хранит `NULL` / rehydration даёт свежее окно, повторы без `startsAt` дублируются (`ALERT-STORE-DEDUP-KEY-STARTSAT`), смешанные клиенты (Spec D5).
- `CHANGELOG.md` `[Unreleased]`: новая секция `### Fixed` (до/после, скоуп, что не равно upstream).
- `docs/06-planning/DECISIONS.md`: ADR-010.
- `docs/CONFIGURATION_GUIDE.md`: комментарий к `global.resolve_timeout` в примере конфига. Сверх плана, но это единственное место, где оператор видит это поле, а смысл там не был описан.
- Doc-комментарии в коде (D4) написаны ещё на `/implement`: `DefaultResolveTimeout`, `ToGettableAlert`, `SetResolveTimeout`, `stampResolveTimeout`, `RestoreFromPersistence`, `newAlertStore`, `resolveTimeoutFromConfig`.
- Задачные документы: формулу (`receivedAt`, а не `startsAt`) исправили ещё на `/spec`. Реализация от Spec не отошла, кроме двух мелочей: вынесен `stampResolveTimeout` и добавлен `newAlertStore` ради тестируемости. Обе отмечены выше в этом файле, Spec не правили.
- `git diff --check` — чисто.

## Finalization (`/end-task`)

- [x] Ветка не `main`; `requirements.md`, `research.md`, `Spec.md`, `tasks.md` на месте.
- [x] `DONE.md` — запись; `NEXT.md` — WIP очищен; `BACKLOG.md` — `PARITY-RESOLVE-TIMEOUT-ENDSAT` закрыт, `RESOLVE-TIMEOUT-AUTO-RESOLVE` остаётся открытым.
- [x] `BUGS.md` — дописать, если на тестах всплыло новое.
- [x] Архив `tasks/archive/PARITY-RESOLVE-TIMEOUT-ENDSAT/`.

## Blockers & Assumptions

- ~~**Допущение A1**~~ — **подтверждено на `/plan`**: `rehydrateAlertStore` вызывается внутри `initializeInfrastructure` (`service_registry.go:480`), после `memory.NewAlertStore()` (`:463`). Провайдер, поставленный сразу после создания стора, будет на месте к рестору.
- **Допущение A2:** точный дубликат со стабильным явным `endsAt` по-прежнему не обновляет `UpdatedAt` (research F5). Это не цель задачи; T3 не должен его «чинить».
- **Допущение A3:** `fakeRegistry` в `handlers/alerts_test.go` использует настоящий `memory.NewAlertStore()`, так что HTTP-тест видит дефолт 5m без проводки. Если это не так — провайдер ставится в тестовой фикстуре.
- **Открытый вопрос (не блокер):** как закрыть AC8 на HTTP-уровне зависит от того, можно ли наблюдать вход `ProcessAlert` в тестах; решается на `/write-tests`, результат записывается в T6.
- Блокеров нет. `QUALITY-GATES-DIRTIES-TREE`: `make quality-gates` не гонять (он переформатирует чужие файлы) — запускать `go vet`/`go test`/`gofmt -l` по отдельности.

## Final Status (2026-09-24)

**DONE.** Все критерии Spec AC1-AC11 закрыты тестами и живой проверкой. AC12 выполнен частично (см. ниже).

- Код: `alertconv.go`, `alert_store.go`, `service_registry.go`. Тесты: 14 новых в 4 файлах. Документация: compat-дока (матрица + Known Gap #12), CHANGELOG, ADR-010, CONFIGURATION_GUIDE.
- 🔴 **AC12, полный `go test ./...`, не зелёный** по внешней причине: 11 testcontainers-тестов в 3 пакетах не запустились (Docker Hub `429`). Пакеты дифом не затронуты; вывод «не регресс» сделан по коду. Перепрогнать при доступе к образам: `go test ./internal/infrastructure/repository/ ./internal/infrastructure/inhibition/ ./internal/database/ -count=1`.
- Остаточные ограничения и follow-ups:
  - `RESOLVE-TIMEOUT-AUTO-RESOLVE` (BACKLOG): статус не истекает, resolved-нотификации по таймауту нет.
  - `ALERT-STORE-DEDUP-KEY-STARTSAT` (BUGS.md): повтор без `startsAt` создаёт копию алерта, окно для таких клиентов не продлевается. Предсуществующий баг.
  - Смешанные клиенты (Spec D5): берётся последний присланный `endsAt`. Не чиним.
- Процессное отклонение: работа шла в назначенной сессией ветке `claude/determined-meitner-bn0ncl`, а не в `bugfix/<slug>` по `AGENTS.md`.
