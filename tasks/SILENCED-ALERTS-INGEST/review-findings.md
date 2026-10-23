# Deep Review Findings: заглушенный алерт проходит ingest

**Trigger classification:** mandatory (сигналы `C X R`, Full tier: `C` + 3 сигнала)
**Reviewer perspective:** independent agent (не автор кода); линзы correctness (путь публикации заглушенного/незаглушенного/resolved алерта в прямом режиме и в группировке, dedup, кеш inhibition), premises, contract (`C`: `GET /api/v2/alerts`, `/api/v2/alerts/groups`, v1), cross-boundary (`X`: хендлер → `AlertProcessor` → `GroupManager`/notify chain, сборка в `service_registry`), `R` (горячий путь `HasActiveMatch`, логи/метрики), maintainability
**Reviewed at:** 2026-10-23
**Reviewed tree:** раунд 1 — bugfix/silenced-alerts-ingest @ 336aeb5 (diff `622e33d..336aeb5`); раунд 2 — @ bc9a0be (diff `b19ea92..bc9a0be`)
**Verdict:** раунд 1 — fix_required; раунд 2 — fix_required (see `review-verdict.json`)

## Проверено (без находок)

Поведение подтверждено не только чтением: временный тест в `internal/core/services` (удалён, дерево чистое) прогнал `ProcessAlert` с реальным `DeduplicationService` поверх `newMockAlertStorage` и переключаемым `SilenceChecker` (см. F1).

- **Premise 1 (прямая публикация не проверяет silence)** — верна: `processTransparentWithRecommendations`/`processTransparent`/`processEnriched` вызывают `PublishToReceiver*` без silence; без проверки в процессоре удаление фильтра из хендлера начало бы публиковать заглушенные алерты в прямом режиме.
- **Premise 2 (notify chain группы режет по silence)** — верна: `flushGroup` → Step 2 `filterSilenced` (`grouping/manager_impl.go:1207,1373`) на момент отправки; checker группы и процессора — один и тот же `r.silenceStore` (`service_registry.go:1798-1800`, `:2416-2418`). `r.silenceStore` создаётся в `initializeInfrastructure` (`:482`) до `initializeAlertProcessor` (`:452` вызывается позже в `Initialize`) — nil в проде не бывает; nil-проверка сохраняет nil-интерфейс корректно.
- **Premise 3 (`GET` показывает `suppressed`)** — верна: контрактные тесты `TestPhase0SilenceAffectsAlertIngest`, `TestPhase0E2ESmoke_IngestSilenceAndHistoryRecent` и `TestAlertsHandler_SilencedAlertIsSuppressed` зелёные; `?silenced=false` отрезает заглушенный.
- **Premise 4 (кеш inhibition до проверки silence)** — верна: Step 0.5 `AddFiringAlert` выполняется до проверки; на `Ignored`-дубликате кеш тоже обновляется (`616aeef`), так что заглушенный алерт без `endsAt` остаётся источником inhibition при resend.
- **Нет пути, где заглушенный firing публикуется напрямую:** проверка стоит после inhibition/`evaluateRoute` и до выбора режима; при `shouldGroup` результат `routeAlertToGroup` игнорируется, fail-open в прямую публикацию нет; `return nil` — без LLM, investigation, фильтров. Незаглушенный (или `SilenceChecker == nil`) идёт прежним путём без изменений.
- **Resolved:** `alert.Status == core.StatusFiring` — resolved не затронут, идёт прежним путём (как и раньше: старый фильтр тоже пропускал resolved).
- **Upstream-семантика:** в upstream silence — только этап `MuteStage` notify pipeline; алерт хранится, виден как `suppressed`, участвует в inhibition как источник, лежит в aggrGroup. Фикс приводит к этому в режиме группировки; прямой режим upstream-аналога не имеет, отказ от публикации — разумная замена.
- **Конкурентность/производительность `HasActiveMatch`:** `RLock` + линейный проход по silences с общим кешированным матчером (`storage/memory/silence_store.go:287`). Число вызовов на алерт то же, что раньше в хендлере (один на firing-алерт); добавилась только запись заглушенных алертов в dedup-хранилище/историю — задокументировано в Spec (Risks) и CHANGELOG.
- **Другие входы `ProcessAlert`** (`cmd/server/handlers/webhook.go`, `prometheus_alerts.go`, `infrastructure/webhook/handler.go`) в роутере не подключены; `POST /api/v1/alerts` проксирует в v2-хендлер (`handlers/alerts.go:287-292`), `GET /api/v1/alerts` считает silence так же — утверждение CHANGELOG про v1 верно.
- **Сборка и тесты:** `go build ./...`, `go vet` (services, application/..., cmd/server), `go test ./internal/core/services/ ./internal/application/...` — 606 passed; `go test -tags futureparity ./cmd/server/` — 202 passed.
- **Не регрессия, для сведения (Phase 2):** resolved по алерту, о котором из-за silence не уведомляли, в прямом режиме публикуется (так было и до фикса — старый фильтр пропускал resolved); в группировке resolved-only набор без прошлой записи nflog отправляется после снятия silence — известное отличие от upstream `DedupStage`, задокументировано в `grouping/dedup.go:136`.

## Findings

### F1 — Прямой режим: заглушенный алерт без `endsAt` не уведомит и после снятия silence — dedup «съедает» его на ingest
- **Severity:** major
- **Location:** `go-app/internal/core/services/alert_processor.go:528-566` (ранний `return` на `ProcessActionIgnored`) в связке с `:661-671`; `go-app/internal/core/services/deduplication.go:349-370` (`alertNeedsUpdate`); `tasks/SILENCED-ALERTS-INGEST/Spec.md` Edge Case 2
- **Issue:** теперь заглушенный firing-алерт проходит dedup и пишется в хранилище (`created`). Dedup считает повтор «точным дубликатом» (`ignored`), если не изменились `status` и `endsAt`; `ProcessAlert` на `ignored` возвращается до проверки silence и до публикации. У алерта без `endsAt` (`EndsAt == nil`: Prometheus-API-формат `state/activeAt` — `webhook/prometheus_parser.go:217` всегда ставит нулевой `EndsAt`; legacy-формат и `amtool alert add` без `--end`; самописные отправители) каждый resend — `ignored`. Итог в прямом режиме: после истечения silence алерт не публикуется никогда (до resolve). Воспроизведено временным тестом: без `endsAt` — 0 публикаций после снятия silence, с меняющимся `endsAt` — 1. **Это регрессия диффа:** раньше заглушенный алерт не доходил до dedup, и первый resend после истечения silence был `created` → публиковался. Edge Case 2 в Spec («следующий resend публикуется обычным путём») верен только для отправителей, меняющих `endsAt` (Prometheus в Alertmanager-формате). Тот же класс в группировке, если заглушенный алерт не попал в группу (ошибка `AddAlertToGroup`, Edge Case 4) или группа была удалена: `ignored`-повторы не вернут его в группу.
- **Recommendation:** не давать dedup-`ignored` закрывать путь алерту, публикацию которого пропустили из-за silence. Варианты: (a) в процессоре помнить fingerprint-ы, пропущенные по silence в прямом режиме/при неудачной группировке (`sync.Map`, удалять при публикации/resolve), и на `ignored` для такого fingerprint без активного silence продолжать обработку; (b) проверять silence до раннего `return` на `ignored` и для «бывших заглушенных» не считать повтор дубликатом. Минимум, если фикс откладывается: исправить Edge Case 2 в Spec, добавить ограничение в CHANGELOG и завести запись в `BUGS.md` (`defer-bug`). Тест Phase 2: прямой режим, алерт без `endsAt` → silence → истечение → resend → одна публикация.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — В группировке заглушенный алерт обходит `filterEngine` и LLM-фильтр и остаётся в группе после снятия silence
- **Severity:** minor
- **Location:** `go-app/internal/core/services/alert_processor.go:661-671`; ср. `processTransparent` (`:766-775`) и `processEnriched` (`:812-830`)
- **Issue:** обычный путь применяет `filterEngine.ShouldBlock` до `routeAlertToGroup`: заблокированный алерт в группу не попадает (`empty_alert_name`; в enriched-режиме — `noise`/`low_confidence` по классификации LLM). Заглушенный идёт в группу без фильтров и без классификации. После снятия silence последующие resend-ы такого алерта фильтр отрежет, но алерт уже лежит в группе — notify chain уведомит о нём. Раньше (фильтр в хендлере) такой алерт в группу не попадал вообще. Для `noise`-классификации это AMP-специфичная функция без upstream-аналога, так что расхождение небольшое, но не задокументированное.
- **Recommendation:** в ветке silence перед `routeAlertToGroup` вызывать `filterEngine.ShouldBlock(alert, nil)` (правила без классификации; Rule 7 на групповом пути безвреден) — или явно задокументировать в Spec (Edge Cases) и в `docs/ALERTMANAGER_COMPATIBILITY.md` (Known Gap про классификацию в группах), что LLM-фильтр к заглушенному алерту не применяется. Тест Phase 2: заглушенный алерт с пустым `alertname` не попадает в группу (если выбран фикс).
- **Disposition:** fix-here (не блокирует; допустим `defer-tech-debt` с записью)
- **Follow-up:** n/a

### F3 — Breaking-запись CHANGELOG описывает не все видимые изменения
- **Severity:** minor
- **Location:** `CHANGELOG.md` (Breaking changes, `SILENCED-ALERTS-INGEST`)
- **Issue:** запись называет `GET /api/v2/alerts` и `GET /api/v1/alerts`. Заглушенные алерты теперь также появляются в `GET /api/v2/alerts/groups` (`AlertGroupsHandler` читает тот же `alertStore`, `handlers/alerts.go:318-319`) и в счётчиках legacy-дашборда (`application/legacy_dashboard.go:162,185` — `alertStore.Stats()`: `FiringAlerts` теперь включает заглушенные). Клиент, считающий «активные» алерты по группам или по дашборду, увидит рост без объяснения.
- **Recommendation:** дополнить запись: `GET /api/v2/alerts/groups` (фильтр `?silenced=false`) и firing-счётчики дашборда теперь включают заглушенные алерты.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — Лог `routeAlertToGroup` при ошибке обещает прямую публикацию, которой в ветке silence нет
- **Severity:** nit
- **Location:** `go-app/internal/core/services/alert_processor.go:509-515` (сообщение), вызов `:667-670`
- **Issue:** при ошибке `AddAlertToGroup` для заглушенного алерта пишется `Error "Failed to add alert to group, falling back to direct publish (fail-open)"`, хотя публикации нет (Spec Edge Case 4 ссылается именно на этот лог). Оператор, разбирающий инцидент, прочтёт ложное.
- **Recommendation:** логировать fail-open в вызывающих местах или в ветке silence писать собственный `Warn` («silenced alert not added to group; not published»), а сообщение в `routeAlertToGroup` сделать нейтральным.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — Пропуск по silence виден только в Info-логе на каждый resend, без метрики
- **Severity:** nit
- **Location:** `go-app/internal/core/services/alert_processor.go:662-664`
- **Issue:** inhibition-ветка пишет `businessMetrics.RecordInhibitionCheck("inhibited")`; silence-ветка — только `Info` на каждый resend каждого заглушенного алерта (при maintenance-silence на сотни алертов — заметный объём логов при интервале Prometheus в 1 мин). Счётчика пропущенных по silence публикаций нет. Spec (Observability) выбрал только лог осознанно.
- **Recommendation:** понизить до `Debug` или добавить счётчик (по аналогии с inhibition); не блокирует.
- **Disposition:** defer-backlog
- **Follow-up:** запись в `docs/06-planning/BACKLOG.md` на `finalize`

## Что должны покрыть тесты Phase 2 (2.1)

- Прямой режим: заглушенный firing — 0 публикаций, 0 вызовов LLM/investigation, алерт в кеше inhibition; незаглушенный — 1 публикация; resolved заглушенного — публикуется.
- Группировка: заглушенный firing — ровно один `AddAlertToGroup`, 0 публикаций и 0 вызовов LLM; ошибка группировки — 0 публикаций (без fail-open).
- F1: алерт без `endsAt` → silence → истечение → resend → публикация (после фикса).
- `SilenceChecker == nil` — поведение прежнее.

## Раунд 2 (2026-10-23, @ bc9a0be)

Дифф `b19ea92..bc9a0be`: `silenceSkipped` (`sync.Map`) и исключение для dedup-`ignored` в `ProcessAlert`, `ShouldBlock(alert, nil)` и Warn в ветке silence, CHANGELOG (Fixed, Breaking), Spec (Design 2/2a, Edge Cases 2/4, Risks). Проверено временным тестом в `internal/core/services` с настоящим `DeduplicationService` (удалён, дерево чистое); `go build ./...`, `go vet`, `go test -race ./internal/core/services/ ./internal/application/...` — 606 passed; `go test -tags futureparity ./cmd/server/` — 202 passed.

### Статус находок раунда 1

- **F1 — не закрыт (механизм верен, но сломан правкой F2, см. F6).** С фильтром без состояния (`fakeFilterEngine`) сценарий «без `endsAt` → silence → два resend под silence → снятие → три resend» даёт ровно одну публикацию: исключение для `ignored` срабатывает, после публикации запись удаляется, повторных публикаций нет. Логика набора проверена:
  - `Load` выполняется после `deduplication.ProcessAlert`, который проставляет `Fingerprint`; при `ignored` используется входной алерт (тот же fingerprint), а не сохранённый — для дальнейшего пути безразлично.
  - Inhibition: помеченный `ignored`-повтор проходит Step 0.5 (кеш источников обновляется, как и в обычной `ignored`-ветке) и inhibition-проверку; если алерт подавлен — `return` до проверки silence, запись остаётся, и после снятия inhibition resend будет опубликован. Побочный эффект: для помеченных fingerprint-ов `RecordInhibition` и метрика `inhibited` пишутся на каждый resend, а не только на изменение — на корректность не влияет.
  - Гонки: операции `sync.Map` атомарны; два одновременных помеченных resend-а после снятия silence оба пройдут до публикации, второй отрежет Rule 7 `isDuplicate` (под мьютексом) — двойной публикации нет. `-race` чист.
  - Удаление записи стоит до mode-хендлеров: если публикация упала (`resolveReceiver`/publisher вернули ошибку), запись уже удалена и неизменный resend снова станет `ignored`. Это тот же класс, что у любого алерта без `endsAt` при ошибке публикации (dedup уже записал), не регрессия.
  - Утечка: запись живёт до resolve/публикации/группировки; алерт, который перестали слать без resolve, оставляет одну запись навсегда; набор не реплицируется. Оба ограничения задокументированы в Spec (Edge Case 2) и CHANGELOG — приемлемо.
- **F2 — resolved по существу, но реализация порождает F6.** `ShouldBlock(alert, nil)` отсекает `empty_alert_name` до группы; отказ от LLM-фильтра задокументирован комментарием.
- **F3 — resolved.** Breaking-запись называет `/api/v2/alerts/groups` и счётчики дашборда; Spec Risks дополнен.
- **F4 — resolved.** Добавлен Warn «Silenced alert not grouped, not publishing it directly». Error внутри `routeAlertToGroup` по-прежнему говорит «falling back to direct publish (fail-open)», но следующая строка лога это опровергает — достаточно.
- **F5 — defer-backlog**, без изменений.

### Новые находки

#### F6 — Rule 7 (`isDuplicate`) в ветке silence стирает пометку `silenceSkipped` и блокирует первый resend после снятия silence — F1 в прямом режиме не исправлен
- **Severity:** major
- **Location:** `go-app/internal/core/services/alert_processor.go` (ветка silence: `ShouldBlock(alert, nil)` → `silenceSkipped.Delete`; удаление записи перед mode-хендлерами); `go-app/internal/core/services/filter_engine.go:141-172` (`isDuplicate`)
- **Issue:** `SimpleFilterEngine.ShouldBlock` не чистая функция: Rule 7 записывает `fingerprint → (status, seenAt)` и блокирует повтор того же статуса в окне `DefaultDedupWindow` = 1 мин. В ветке silence это даёт две поломки (обе воспроизведены временным тестом с настоящими `DeduplicationService` и `SimpleFilterEngine`, ожидалась одна публикация):
  1. **Два resend-а под silence в пределах окна** (отправитель шлёт чаще раза в минуту): второй блокируется как `duplicate`, ветка «blocked by filter» удаляет запись из `silenceSkipped`. После снятия silence неизменный resend — `ignored` без пометки ⇒ **0 публикаций** до resolve (окно 50 мс, тот же сценарий).
  2. **Снятие silence в пределах окна от последнего resend-а под silence:** обычный путь удаляет запись до mode-хендлера, затем `processTransparent`/`processEnriched` вызывает `ShouldBlock` и Rule 7 видит `firing`, записанный веткой silence, ⇒ `duplicate`, публикации нет; следующий resend — `ignored` без пометки ⇒ **0 публикаций** (окно 1 мин, resend сразу после снятия).
  Для отправителей с меняющимся `endsAt` (Prometheus) последствие мягче — публикация откладывается до resend-а вне окна, но с `endsAt`-less алертом F1 воспроизводится как в раунде 1. В режиме группировки сценарий 1 бьёт по алерту, которому не удалась группировка (единственный случай, когда он помечен).
- **Recommendation:** не трогать состояние Rule 7 в ветке silence. Например: применять фильтр только перед `routeAlertToGroup` (в прямом режиме он не нужен — алерт не публикуется, а после снятия silence фильтр отработает на обычном пути) и не считать причину `duplicate` блокировкой в этой ветке; либо вынести в `FilterEngine` проверку без побочных эффектов (правила 5/6) и вызывать её. В любом варианте запись `silenceSkipped` не должна удаляться из-за `duplicate`. Тесты Phase 2 — с настоящим `SimpleFilterEngine`, а не `fakeFilterEngine`: оба сценария выше плюс «resend после окна».
- **Disposition:** fix-here
- **Follow-up:** n/a

### Итог раунда 2

F3 и F4 закрыты, F5 отложен. F1 остаётся открытым из-за F6 (major, fix-here): механизм `silenceSkipped` корректен, но Rule 7, вызванный в ветке silence, стирает пометку и съедает первый resend после снятия silence. Gate — `fix_required`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
