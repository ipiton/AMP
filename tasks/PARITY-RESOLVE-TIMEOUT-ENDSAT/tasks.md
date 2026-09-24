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
- [ ] **T11. Гейты (AC12)**: `go build ./...`, `go vet ./...`, `go test ./... -count=1`; `-race` на `internal/infrastructure/storage/memory` и `internal/application/handlers`; `go test ./cmd/server -tags futureparity -count=1` без новых падений; `git diff --check`. Флейк `PUBLISHING-WARMUP-TEST-FLAKY` не считается регрессом, но фиксируется, если выстрелит.
- [ ] **T12. Живая проверка (по возможности)**: lite-инстанс, `curl -XPOST /api/v2/alerts` без `endsAt` → `GET` показывает `endsAt ≈ now + 5m`; повторный POST сдвигает окно. Если поднять не получится — записать, что не проверено вживую.

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

## Documentation

- [ ] **D1.** `docs/ALERTMANAGER_COMPATIBILITY.md`: поведение `endsAt`/`global.resolve_timeout` (время приёма + таймаут, продление, reload); known limitations: нет авто-резолва по таймауту (`RESOLVE-TIMEOUT-AUTO-RESOLVE`), смешанные клиенты (Spec D5), в БД `ends_at` пуст (ADR-010).
- [ ] **D2.** `CHANGELOG.md` `[Unreleased]` → `### Fixed`: алерты без `endsAt` больше не отдаются как отгоревшие.
- [ ] **D3.** `docs/06-planning/DECISIONS.md`: ADR-010 «Таймаутный `endsAt` — значение API-слоя, БД хранит присланное» (Spec D1).
- [ ] **D4.** Doc-комментарии у `SetResolveTimeout`, `DefaultResolveTimeout` и изменённого фолбэка.

## Finalization (`/end-task`)

- [ ] Ветка не `main`; `requirements.md`, `research.md`, `Spec.md`, `tasks.md` на месте.
- [ ] `DONE.md` — запись; `NEXT.md` — WIP очищен; `BACKLOG.md` — `PARITY-RESOLVE-TIMEOUT-ENDSAT` закрыт, `RESOLVE-TIMEOUT-AUTO-RESOLVE` остаётся открытым.
- [ ] `BUGS.md` — дописать, если на тестах всплыло новое.
- [ ] Архив `tasks/archive/PARITY-RESOLVE-TIMEOUT-ENDSAT/`.

## Blockers & Assumptions

- ~~**Допущение A1**~~ — **подтверждено на `/plan`**: `rehydrateAlertStore` вызывается внутри `initializeInfrastructure` (`service_registry.go:480`), после `memory.NewAlertStore()` (`:463`). Провайдер, поставленный сразу после создания стора, будет на месте к рестору.
- **Допущение A2:** точный дубликат со стабильным явным `endsAt` по-прежнему не обновляет `UpdatedAt` (research F5). Это не цель задачи; T3 не должен его «чинить».
- **Допущение A3:** `fakeRegistry` в `handlers/alerts_test.go` использует настоящий `memory.NewAlertStore()`, так что HTTP-тест видит дефолт 5m без проводки. Если это не так — провайдер ставится в тестовой фикстуре.
- **Открытый вопрос (не блокер):** как закрыть AC8 на HTTP-уровне зависит от того, можно ли наблюдать вход `ProcessAlert` в тестах; решается на `/write-tests`, результат записывается в T6.
- Блокеров нет. `QUALITY-GATES-DIRTIES-TREE`: `make quality-gates` не гонять (он переформатирует чужие файлы) — запускать `go vet`/`go test`/`gofmt -l` по отдельности.
