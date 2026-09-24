# Research: PARITY-RESOLVE-TIMEOUT-ENDSAT

Дата: 2026-09-24. Пути ниже — относительно `go-app/`.

## Вопросы

1. Откуда берётся `endsAt == startsAt == updatedAt` в `GET /api/v2/alerts`?
2. Как именно ведёт себя upstream (формула, продление, что уходит в нотификации)?
3. Какие ingest-пути затронуты и где правильно ставить таймаут?
4. Как `resolve_timeout` достаётся в рантайме (lite-профиль, отсутствие `global:`, `/-/reload`)?
5. Есть ли у AMP авто-резолв по истечении `endsAt`?

## Findings

### F1. Корень бага — фолбэк на отдаче, а не парсер

- Ingest ничего не выдумывает: оба парсера оставляют `EndsAt = nil`, если поле не пришло (`internal/infrastructure/webhook/parser.go:133-136`, `prometheus_parser.go:288-291`; legacy-путь `internal/application/handlers/alerts.go:531` → `ParseOptionalAlertTime`). Memory store хранит `nil` (`internal/infrastructure/storage/memory/alert_store.go:366-394`), `toAPIAlert` отдаёт `EndsAt: nil` (`:409-428`).
- Подмену делает `alertconv.ToGettableAlert` (`internal/core/alertconv/alertconv.go:176-179`): `endsAt := alert.UpdatedAt`, если `EndsAt` пуст. Для свежего алерта `updatedAt == startsAt` (оба = `now`), отсюда наблюдаемое `startsAt=endsAt=updatedAt`.
- Эту же функцию используют `GET /api/v2/alerts`, `GET /api/v1/alerts` (через `ToV1Alert`) и `GET /api/v2/alerts/groups` (`alert_store.go:281`) — фикс в одном месте закрывает все три.

### F2. Upstream: `now + resolve_timeout`, а не `startsAt + resolve_timeout`

`api/v2/api.go` `postAlertsHandler` (Alertmanager v0.2x+):

```go
alert.UpdatedAt = now
if alert.StartsAt.IsZero() { alert.StartsAt = now (или EndsAt, если он задан) }
if alert.EndsAt.IsZero() {
    alert.Timeout = true
    alert.EndsAt = now.Add(resolveTimeout)
}
```

- База — **время приёма**, не `startsAt`. Формулировка в `requirements.md`/BACKLOG (`startsAt + resolve_timeout`) неточна: для алерта, который горит час и переотправляется, `startsAt + 5m` давно в прошлом ⇒ ровно тот же симптом «отгорел». Исправить в `/spec`.
- Продление: каждый POST без `endsAt` заново ставит `now + resolve_timeout`; `types.Alert.Merge` берёт более поздний `endsAt`, но только у алерта без флага `Timeout` (явный `endsAt` «главнее» таймаутного).
- Нотификации: dispatcher обнуляет `EndsAt` у ещё не резолвнутых алертов перед отправкой (`0001-01-01T00:00:00Z` в webhook firing-алерта). То есть таймаутный `endsAt` — это факт **API/стора**, а не payload'а receivers.
- Реальный Prometheus всегда шлёт `endsAt` сам (`now + 4×resend_delay`), так что путь с таймаутом — это `amtool alert add`, curl, самописные клиенты, тестовые стенды.

### F3. Ingest-пути

- `POST /api/v2/alerts` и `POST /api/v1/alerts` → `handleAlertsPost` (`alerts.go:404`). Payload пробуется сначала как Prometheus-формат (`state`/`activeAt` или `groups`, детектор `webhook/detector.go:373`), иначе — legacy/Alertmanager-postable (`parseLegacyAlerts`). Обычный postable-массив идёт по второй ветке.
- Цепочка: parse → silence-фильтр → `AlertProcessor.ProcessAlert` (dedup + БД + inhibition + classification + grouping/publishing) → `toAlertIngestInput` → `AlertStore.IngestBatch`.
- `AlertStore.IngestBatch` вызывается **только** из `handleAlertsPost` (`alerts.go:453`). Восстановления стора из БД при старте нет — стор эфемерный.

### F4. Dedup: штамповать до `ProcessAlert` опасно

`deduplicationService.alertNeedsUpdate` (`internal/core/services/deduplication.go:346-365`) считает изменение `EndsAt` поводом для update. Сейчас повторный POST без `endsAt` — `ProcessActionIgnored`, и `ProcessAlert` сразу выходит (`alert_processor.go:534-539`). Если проставить `now + rt` на `core.Alert` до процессора, **каждый** повторный POST станет update и пройдёт весь пайплайн дальше: запись в БД, inhibition-кэш, классификация (потенциально LLM-вызов), grouping. Для Prometheus-трафика это уже так (он шлёт меняющийся `endsAt`), но расширять это на таймаутный путь в рамках парити-фикса не нужно — и upstream в нотификации таймаутный `endsAt` не отдаёт (F2).

### F5. Стор сам продлит окно

`AlertStore.apply` (`alert_store.go:71-100`): `isSameAlertPayload` сравнивает `EndsAt`, поэтому новый `now + rt` ≠ старому ⇒ обновляются `EndsAt` и `UpdatedAt`. Продление при повторном POST получается бесплатно и совпадает с upstream. (Попутно: сегодня точный дубликат **не** обновляет `UpdatedAt`, upstream обновляет всегда — после фикса для таймаутных алертов это само выровняется, для алертов с явным `endsAt` расхождение остаётся; вне скоупа.)

### F6. Доступ к `resolve_timeout`

- Живёт в `cfg.Routing.Global.ResolveTimeout` (`internal/config/config.go:70`, `internal/infrastructure/routing/config.go:68`, дефолт 5m в `GlobalConfig.Defaults()` `routing/global.go:117-121`).
- `Defaults()` вызывается только если `global:` есть в YAML (`routing/parser.go:300-303`). Без `global:` — `Global == nil`; в lite-профиле без `route:` — `Routing == nil`. Нужен собственный фолбэк 5m.
- `registry.Config()` возвращает актуальный `r.config`, который `/-/reload` подменяет (`service_registry.go:2607`). Если читать таймаут на каждый запрос (а не захватывать при построении хендлера, как сейчас `externalURL`), новое значение применяется к следующим POST — ровно как upstream (таймаут фиксируется в момент приёма, ретроактивно не пересчитывается).
- Отдельный `internal/alertmanager/config/config.go:25` (`ResolveTimeout time.Duration`) — параллельная модель, в ingest не участвует; не трогать.

### F7. Авто-резолва по времени у AMP нет

Статус в сторе фиксируется на ingest (`NormalizeStatus` смотрит на `endsAt` только когда `status` не задан, `alertconv.go:59-70`) и дальше меняется лишь явным resolved-POST. Фонового «таймаут истёк ⇒ resolved» нет: ни в сторе, ни в grouping (нотификаций о резолве по таймауту не будет). После фикса алерт, который перестали слать, через 5 минут будет отдаваться с `endsAt` в прошлом, но `status.state = active`. Для внешнего потребителя (он, как и upstream, судит по `endsAt`) это уже правильно; внутренняя модель AMP — нет.

## Options

**A. Read-side: `ToGettableAlert` считает `UpdatedAt + rt`.**
+ Одна точка, без изменения данных.
− `alertconv` не знает конфига (протаскивать параметр во все три вызова); точный дубликат не двигает `UpdatedAt` ⇒ окно не продлевается без правки стора; `/-/reload` пересчитывает ретроактивно (не как upstream).

**B. Ingest-side до процессора: штамповать `core.Alert.EndsAt` в парсинге/хендлере.**
+ Значение единое для БД, стора, publishing.
− F4: каждый повторный POST становится update и гонит весь пайплайн (БД, классификация); receivers получат `endsAt` у firing-алерта, чего upstream не делает (F2). Шире, чем нужно.

**C. Ingest-side только для стора (рекомендую).** В `handleAlertsPost` при сборке `AlertIngestInput` для `IngestBatch`: если алерт firing и `EndsAt == nil` ⇒ `EndsAt = now + rt`, `rt` читается из `registry.Config()` на запрос с фолбэком 5m. `core.Alert`, dedup, БД и publishing не меняются. Плюс убрать/переписать фолбэк `UpdatedAt` в `ToGettableAlert`, чтобы он больше не порождал «отгоревший» алерт.
+ Совпадает с upstream в том, что видит API; продление — бесплатно (F5); reload — как upstream (F6); пайплайн не трогается.
− Стор и БД расходятся в `endsAt` для таймаутных алертов (в БД `NULL`). Это честно: БД хранит то, что прислали, а таймаут — производное значение API-слоя; зафиксировать в `/spec`/`DECISIONS.md` при необходимости.

Что делать с фолбэком в `ToGettableAlert` после C: `nil` там остаётся возможен только теоретически (resolved-снапшот без `endsAt` стор сам заполняет `now`, `alert_store.go:123-127`). Варианты для `/spec`: оставить фолбэк, но документировать, что он недостижим для firing; или сделать фолбэк `UpdatedAt + 5m` для firing. Решить в `/spec`, тестом закрыть инвариант «firing никогда не отдаётся с `endsAt <= updatedAt`».

## Recommendation

Вариант **C**. Формулу исправить на `endsAt = receivedAt + global.resolve_timeout` (как upstream). Не штамповать `core.Alert` до процессора.

## Risks

- **Статус не истекает (F7).** Без авто-резолва AMP после таймаута показывает `state=active` с прошедшим `endsAt`. Не регресс (сейчас хуже), но частичная парность. Вынести в BACKLOG отдельной задачей `RESOLVE-TIMEOUT-AUTO-RESOLVE` (истечение статуса в сторе + resolved-нотификация по таймауту — это уже жизненный цикл и dispatcher, ~1d+).
- **Тесты, завязанные на старый фолбэк.** Возможно, есть ассерты `endsAt == updatedAt` в `alertconv_test.go`/хендлерных тестах/`futureparity`-сьюте — проверить на `/implement` (`grep EndsAt` по тестам).
- **Гонка чтения `r.config` при reload** — существующий паттерн (`AlertsHandler` уже читает `Config()`), новый риск не вносится.
- **Upstream-нюанс `Merge` с флагом `Timeout`:** явный `endsAt` не перетирается таймаутным. В варианте C стор просто пишет последнее присланное: алерт, который сначала пришёл с явным `endsAt`, а потом без него, получит `now + rt`. Расхождение крайне редкое (смешанные клиенты для одного алерта) — зафиксировать как known limitation, не чинить.

## Implication for `/spec`

- Поправить критерий: `endsAt = время приёма + resolve_timeout`, не `startsAt + …`; синхронизировать формулировку в BACKLOG/NEXT.
- Контракт: где штампуется (хендлер → стор), источник `rt` (`Routing.Global.ResolveTimeout`, фолбэк 5m при `Routing == nil`/`Global == nil`), поведение на reload (применяется к следующим POST), судьба фолбэка в `ToGettableAlert`.
- Non-goals: авто-резолв по истечении таймаута, `endsAt` в payload receivers, выравнивание `UpdatedAt` для точных дубликатов с явным `endsAt`, модель `internal/alertmanager/config`.
- Тесты: дефолт 5m; кастомный `global.resolve_timeout`; явный `endsAt` не перетирается; повторный POST продлевает окно; lite-конфиг без `route:`/`global:`; `/api/v2/alerts/groups` отдаёт тот же `endsAt`; dedup не превращает повторный POST в update (пайплайн не гоняется).

## Scope

Скоуп **не вырос**: ~0.5d остаётся реалистичным. Изменения локализуются в `internal/application/handlers/alerts.go` (+ маленький хелпер чтения `rt`) и `internal/core/alertconv/alertconv.go`. Оценку уточнило другое: полная парность требует авто-резолва (F7), и это сознательно выносится отдельной задачей, а не втягивается сюда.
