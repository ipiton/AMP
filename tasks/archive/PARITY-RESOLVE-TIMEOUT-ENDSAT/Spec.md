# Spec: PARITY-RESOLVE-TIMEOUT-ENDSAT

Дата: 2026-09-24. Ветка `claude/determined-meitner-bn0ncl`. Источники: `requirements.md`, `research.md`. Пути ниже — относительно `go-app/`.

## Проблема

Алерт, пришедший без `endsAt`, отдаётся из API с `endsAt == startsAt == updatedAt`. Причина — фолбэк в `alertconv.ToGettableAlert` (`internal/core/alertconv/alertconv.go:176-179`): пустой `endsAt` подменяется на `updatedAt`. Любой потребитель, который, как сам Alertmanager, считает «`endsAt` в прошлом» признаком resolved, видит активный алерт отгоревшим сразу после POST.

Upstream в этой ситуации ставит `endsAt = время приёма + global.resolve_timeout` и заново продлевает окно на каждом повторном POST (research F2).

## Цели

1. Горящий алерт в memory store **никогда** не хранится с пустым `endsAt`: если его не прислали, стор ставит `receivedAt + resolve_timeout`.
2. Повторный POST того же алерта без `endsAt` продлевает окно.
3. Явно присланный `endsAt` сохраняется как есть.
4. `resolve_timeout` берётся из активного конфига (`global.resolve_timeout`, дефолт `5m`) и подхватывается после `/-/reload` на следующих POST.
5. `GET /api/v2/alerts`, `GET /api/v1/alerts` и `GET /api/v2/alerts/groups` отдают это значение.

## Не-цели

- **Авто-резолв по истечении таймаута** (research F7): смена `state` на resolved в сторе и resolved-нотификация по таймауту. Это жизненный цикл алерта и dispatcher; выносится в BACKLOG как `RESOLVE-TIMEOUT-AUTO-RESOLVE`. После этого слайса алерт, который перестали слать, через `resolve_timeout` отдаётся с прошедшим `endsAt` при `status.state = active`.
- Таймаутный `endsAt` в `core.Alert`, в БД и в payload receivers (решение D1).
- Выравнивание `updatedAt` для точных дубликатов с явным `endsAt` (research F5).
- Upstream-нюанс `Merge` с флагом `Timeout` (явный `endsAt` старше таймаутного) — known limitation, D5.
- Параллельная модель конфига `internal/alertmanager/config` (research F6).
- `PARITY-GATE-DOES-NOT-GATE`, `QUALITY-GATES-DIRTIES-TREE`.

## Ключевые решения

### D1. Таймаут — значение API-слоя: штампует memory store, а не ingest-пайплайн

`core.Alert`, dedup, запись в БД, inhibition, классификация и publishing получают алерт как раньше, с `EndsAt = nil`.

Почему не раньше, в хендлере до `ProcessAlert`: dedup считает изменение `EndsAt` поводом для update (`internal/core/services/deduplication.go:346-365`), и каждый повторный POST перестал бы быть `Ignored`, прогоняя БД, классификацию (LLM) и grouping (research F4). К тому же upstream сам обнуляет `endsAt` у горящих алертов перед отправкой нотификаций, так что в payload receivers таймаутному значению делать нечего.

Следствие: в БД у таймаутного алерта `ends_at = NULL`, а в API — `receivedAt + rt`. Это осознанно: БД хранит то, что прислал отправитель, а таймаут — производное. Фиксируется в `DECISIONS.md` (ADR-010).

### D2. Точка штамповки — нормализация внутри `AlertStore`

Стор получает провайдер таймаута:

```go
// memory.AlertStore
func (s *AlertStore) SetResolveTimeout(fn func() time.Duration)
```

Обе функции нормализации — `normalizeIngestInput` (путь POST) и `storedStateFromAlert` (путь rehydration из БД после рестарта, `service_registry.go:506`, `alert_store.go:309`) — **после** вычисления статуса делают:

```
если status == "firing" и endsAt == nil ⇒ endsAt = now + timeout()
```

`now` — время приёма, которое уже передаётся в стор (`IngestBatch(inputs, now)` / `RestoreFromPersistence(alerts, now)`).

Почему в сторе, а не в хендлере: одна точка покрывает и POST, и rehydration (иначе после рестарта стандартного профиля алерты из БД снова отдавались бы с `endsAt == updatedAt`). Штамповка после `NormalizeStatus` гарантирует, что таймаут не влияет на вычисление статуса.

Провайдер не задан (`nil`, либо вернул `<= 0`) ⇒ `alertconv.DefaultResolveTimeout` (5m). Поэтому `NewAlertStore()` в тестах и в lite-сборке без конфига ведёт себя как upstream по умолчанию.

### D3. Источник значения и reload

`ServiceRegistry` при создании стора (`service_registry.go:463`) регистрирует провайдер, который на каждый вызов читает `r.config` → `Routing.Global.ResolveTimeout`; если `Routing == nil`, `Global == nil` или `ResolveTimeout == nil`, возвращает `alertconv.DefaultResolveTimeout`. Чтение на вызов, а не захват при старте, даёт upstream-поведение на `/-/reload`: новое значение применяется к следующим POST, уже выставленные `endsAt` не пересчитываются.

Rehydration идёт на старте, до любого reload, и использует значение из стартового конфига. Rehydrated-алерт получает свежее окно `restartTime + rt`: если отправитель ещё шлёт алерт, окно продлится, иначе истечёт так же, как у любого замолчавшего алерта.

### D4. Продление — через существующий `apply`

`isSameAlertPayload` сравнивает `EndsAt`, поэтому повторный POST с новым `now + rt` обновляет `EndsAt` и `UpdatedAt` (`alert_store.go:71-100`). Новый код для продления не нужен; фиксируется тестом. `onChange` у стора не имеет подписчиков, лишних побочных эффектов нет.

### D5. Смешанные клиенты — known limitation

Upstream при слиянии предпочитает явный `endsAt` таймаутному, если явный позже. Стор AMP пишет последнее присланное значение: алерт, который сначала пришёл с явным `endsAt`, а затем без него, получит `now + rt`. Сценарий (разные клиенты шлют один алерт по-разному) редкий; не чиним, упоминаем в `docs/ALERTMANAGER_COMPATIBILITY.md`.

### D6. Фолбэк в `ToGettableAlert` становится защитным и корректным

После D2 пустой `endsAt` у горящего алерта в сторе недостижим. Фолбэк остаётся, но перестаёт порождать «отгоревший» алерт:

- `status == "firing"` и `EndsAt` пуст ⇒ `UpdatedAt + DefaultResolveTimeout`;
- иначе (resolved без `endsAt`) ⇒ `UpdatedAt`, как сейчас. Стор и так проставляет `now` при резолве (`alert_store.go:123-127`), так что это время резолва.

Константа `DefaultResolveTimeout = 5 * time.Minute` живёт в `alertconv` (там же, где `NormalizeStatus`), и её используют стор и registry. Дефолт в `routing.GlobalConfig.Defaults()` не трогаем: это отдельный слой, тест сверяет совпадение значений.

## Затрагиваемые места

| Файл | Изменение |
|---|---|
| `internal/core/alertconv/alertconv.go` | `DefaultResolveTimeout`; защитный фолбэк в `ToGettableAlert` (D6) |
| `internal/infrastructure/storage/memory/alert_store.go` | поле-провайдер + `SetResolveTimeout`; штамповка в `normalizeIngestInput` и `storedStateFromAlert` (D2) |
| `internal/application/service_registry.go` | регистрация провайдера при создании стора; хелпер `resolveTimeoutFromConfig` (D3) |
| `docs/ALERTMANAGER_COMPATIBILITY.md` | поведение `endsAt`/`resolve_timeout`, known limitations (F7, D5) |
| `CHANGELOG.md` `[Unreleased]` | `### Fixed` |
| `docs/06-planning/DECISIONS.md` | ADR-010 (D1) |
| `docs/06-planning/BACKLOG.md` | `RESOLVE-TIMEOUT-AUTO-RESOLVE` |

Хендлеры `internal/application/handlers/alerts.go` не меняются.

## Acceptance Criteria

1. **Дефолт.** POST без `endsAt` (legacy/postable-массив) при конфиге без `global:` ⇒ в `GET /api/v2/alerts` `endsAt == receivedAt + 5m` (±1s) и `endsAt > updatedAt`.
2. **Кастомный таймаут.** `global.resolve_timeout: 1h` ⇒ `endsAt == receivedAt + 1h`.
3. **Явный `endsAt`** не перетирается ни при первом POST, ни при повторном с тем же значением.
4. **Продление.** Два POST одного алерта без `endsAt` с разными `now` ⇒ `endsAt` и `updatedAt` соответствуют второму.
5. **Resolved не затронут.** POST с `status: resolved` или `endsAt` в прошлом ⇒ поведение как до изменения (`endsAt` — присланный или время резолва).
6. **Rehydration.** Горящий алерт из персистентности с `EndsAt == nil` после `RestoreFromPersistence(…, now)` ⇒ `endsAt == now + rt`.
7. **Groups и v1.** `GET /api/v2/alerts/groups` и `GET /api/v1/alerts` отдают тот же `endsAt`, что и `/api/v2/alerts`.
8. **Пайплайн не трогается.** Повторный POST того же алерта без `endsAt` по-прежнему `Ignored` в dedup: `core.Alert.EndsAt`, переданный в `ProcessAlert`, остаётся `nil`.
9. **Reload.** Смена провайдера или значения в конфиге применяется к следующему POST без пересчёта уже сохранённых алертов (unit-уровень: провайдер читается на каждый ingest).
10. **Защитный фолбэк.** Unit-тест `ToGettableAlert`: firing с пустым `EndsAt` ⇒ `UpdatedAt + 5m`; resolved с пустым ⇒ `UpdatedAt`.
11. Дефолт `alertconv.DefaultResolveTimeout` совпадает с дефолтом `routing.GlobalConfig.Defaults()` (тест).
12. `go vet ./...`, `go test ./...` зелёные (с учётом известного флейка `PUBLISHING-WARMUP-TEST-FLAKY`, BUGS.md), `git diff --check` чистый. Suite `futureparity` (`go test ./cmd/server -tags futureparity`) не регрессирует.

## Risks

- **Существующие тесты стора/хендлеров** могут ассертить `EndsAt == nil` у горящего алерта. Правим ожидания под новый контракт, с комментарием; не ослабляем.
- **Частичная парность (F7).** Без авто-резолва `state` не истекает. Задокументировать явно в compat-доке, а не замалчивать; follow-up в BACKLOG.
- **Гонка чтения `r.config` при reload.** Существующий паттерн (`AlertsHandler` уже читает `Config()` на запрос), новый риск не вносится.
- **Расхождение БД и API по `endsAt`** (D1) может удивить при отладке через SQL; снимается ADR-010 и строкой в compat-доке.

## Оценка

~0.5d. Три файла кода, остальное — тесты и документация.
