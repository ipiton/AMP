# Deep Review Findings: быстрые исправления publishing (бандл)

**Trigger classification:** mandatory (сигнал `S`; три сигнала `S X R` — Full tier)
**Reviewer perspective:** independent agent (не автор кода); линзы security (утечки URL/токенов/тел ответов, SMTP-ключ), cross-boundary (wiring общего кеша, слои), runtime (half-open под конкуренцией, Stop/drain/Submit, recover и completion, Rootly-хранилище при ошибках Redis/TTL), correctness, premises (сверка с upstream `alertmanager@v0.32.0`), maintainability
**Reviewed at:** 2026-10-23
**Reviewed tree:** раунд 1 — bugfix/publish-quick-fix-bundle @ 3e3d7f7 (diff `6c1813d..3e3d7f7`); раунд 2 — @ ee44f0d (diff `3e3d7f7..ee44f0d`, плюс сверка всего `6c1813d..ee44f0d` по линзе секретов); раунд 3 — @ bb21bbb (diff `ee44f0d..bb21bbb`, плюс полный проход publishing по линзе секретов)
**Verdict:** раунд 1 — fix_required; раунд 2 — fix_required; раунд 3 — pass (see `review-verdict.json`)

## Проверено (без находок)

- **Premise 1** (PD trigger шлёт `dedup_key = fingerprint`, resolve с тем же ключом закрывает инцидент): верна. `pagerduty_publisher_enhanced.go:85` (`DedupKey: alert.Fingerprint`), upstream `notify/pagerduty/pagerduty.go:239` шлёт `key.Hash()` и на trigger, и на resolve без хранения. Отличие от upstream (ключ — fingerprint алерта, а не хеш group key) — существующее до задачи и согласованное: PD — не batch-таргет, `publishJob` вызывает `Publish` на каждый алерт. Класс `code-read` заслужен (семантика Events API v2 — dedup_key, переданный в запросе, возвращается в ответе).
- **Premise 2** (`EventKeyCache`/`ErrEventNotTracked` только у PD-паблишера и фабрики): верна, в prod-коде ссылок не осталось; упоминания только в планировочных `.md`.
- **Premise 3** (`r.cache` не nil до `initializePublishing`): верна — `service_registry.go:1244/1275/1346` (Redis или `MemoryCache`); `initializePublishingRuntime` вызывается один раз (`publishing_runtime.go:45`), фабрика на reload не пересоздаётся — повторного `SetRootlyIncidentStore` нет. `cacheIncidentIDCache.Stop` — no-op, общий кеш фабрика не закрывает. Слои: `infrastructure/publishing` → `infrastructure/cache`, цикла нет.
- **Premise 4** (breaker используется только `processJob`): верна — единственный вызов `CanAttempt` `queue.go:1075`; `NewCircuitBreaker*` в других пакетах — свои типы.
- **Premise 5** (abandoned с причиной ≠ `Unconfirmed` не пишет результат): верна (`queue.go:1153-1166`); тайм-аут пробы закрывает этот путь. Все прочие выходы после `CanAttempt == true` пишут результат: ошибка фабрики (`:1099`), panic (`:1047`), unconfirmed (`:1164`), ошибка/успех (`:1192`, `RecordSuccess`).
- **Premise 6** (поздний `Submit` возможен, assumed): защита не зависит от порядка shutdown — проверка `closed` под `RLock`, send неблокирующий, `close` под `Lock` в `stopOnce`. Повторный `Stop` только ждёт `wg`. Класс `assumed` честный, вывод от него не зависит.
- **Premise 7** (тела ответов ≤64 KiB): лимит стоит на путях ошибок (PD/Rootly `parseError`) и на Slack/Telegram/webhook; успешный ответ Rootly create (самый крупный JSON:API) лимитом не затронут — дубли инцидентов из-за обрезанного JSON не возникают.
- **Очередь:** воркер обнуляет закрытые каналы и выходит, когда закрыты все три; самый внутренний `select` с `time.After(100ms)`, поэтому воркер не зависает на `nil`-канале. `q.ctx` — от `context.Background()`, отменяется только по тайм-ауту `Stop`, так что дренаж реально выполняется. Других отправок в каналы, кроме `submitJob`, нет.
- **recover:** defer регистрируется после defer с `completion.signal` и выполняется раньше него; `deliveryOutcome` захвачен замыканием — ожидающий получает ошибку паники. `cb` объявлен до defer, `nil`-проверка покрывает панику до `getCircuitBreaker`.
- **Breaker:** переходы под write-lock; `Open → HalfOpen` ровно у одного конкурентного вызова; провал пробы → `Open`, освобождение слота; удаление ветки `RecordSuccess` в `Open` корректно. Поведение под `-race` — проверить в testing (3.5).
- **SMTP-ключ:** покрывает все 6 полей `SMTPConfig` (`email_models.go:7-14`); пароль — только sha256, ключ не логируется.
- **Изоляция Slack/Rootly:** все обращения к кешам (`slack_publisher_enhanced.go:57,133`, `rootly_publisher_enhanced.go:71,118,168,181,204,245,256`) идут через `correlationKey`.
- **Маскировка:** `core.RedactURL` убирает путь, query, fragment, userinfo, opaque; `redactURLError` правит `*url.Error` на месте — цепочка `errors.Is/As` (классификация `net.OpError`/timeout в `queue_error_classification.go`) сохраняется. Telegram и Slack маскировали свои ошибки и раньше; PD шлёт ключ в теле, URL фиксированный.
- **git log** затронутых файлов: `circuit_breaker.go`, `rootly_*` не менялись по существу с initial release — у удалённой ветки `RecordSuccess`-в-`Open` и `return nil` на промахе resolve нет истории, которую фикс мог бы сломать.

## Findings

### F1 — Промах Rootly resolve — вечный повтор: группа не очищается, breaker считает промах отказом
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/publishing/rootly_publisher_enhanced.go:198-217`, `go-app/internal/infrastructure/publishing/rootly_incident_store.go:57-66`, `go-app/internal/infrastructure/publishing/queue.go:1192`, `go-app/internal/infrastructure/grouping/manager_impl.go:1619,1736,1772`
- **Issue:** `ErrIncidentNotTracked` — постоянный исход: ID не появится, сколько ни повторяй. Но для конвейера это обычная неудачная доставка:
  1. Цель не подтверждена, поэтому notify chain не делает `RecordSent` и не вызывает `pruneResolvedAlerts` (`manager_impl.go:1736` → prune только при `allSucceeded`, `:1772`). Resolved-алерт остаётся в группе и уходит в Rootly на каждом flush, пока живёт группа: Redis TTL группы 24h продлевается сохранениями, `CleanupExpiredGroups` в prod не вызывается. На каждом цикле — WARN, `RecordAPIError(not_tracked)`, запись в DLQ (ошибка `Unknown`, без ретраев внутри очереди) и `cb.RecordFailure()`.
  2. Breaker таргета (`FailureThreshold: 5`) открывается сериями таких промахов, и тогда настоящие firing-уведомления в Rootly получают `ErrDeliveryNotAttempted` и откладываются.
  Spec (§ Impact Analysis / Risks) считает окно «одной волной resolve после апгрейда» — это неверно, окно бессрочное. Источников промаха больше, чем апгрейд: lite-профиль (`MemoryCache`) при каждом рестарте, вытеснение ключей Valkey при `maxmemory`, ошибка `Set` при create, переименование таргета или перестановка `rootly_configs` (см. F6). Кроме того, `Get` сводит ошибку хранилища к промаху (`rootly_incident_store.go:62-65`). Повтор нужен как раз для недоступного Redis, а сейчас временная ошибка и постоянный промах не различаются.
  До фикса (`return nil`) группа очищалась. Новое поведение видно, но не ограничено по времени.
- **Recommendation:** разделить два исхода. (a) Ошибка хранилища — временная ошибка (retryable или обычная неудача: повтор на следующем flush полезен). (b) Настоящий промах — терминальный: WARN + метрика `not_tracked` один раз, breaker не трогать, DLQ не писать, и notify chain должна считать цель покрытой. Самая простая форма — вернуть `nil` после WARN и метрики (видимый, но не повторяемый исход). Альтернатива — отдельный sentinel, который очередь и coordinator трактуют как «доставлять нечего». Обновить Success Criterion «resolve при промахе возвращает ошибку» и § Risks в Spec, строку в CHANGELOG поправить. Тест: resolve с промахом ⇒ breaker не меняет счётчик, повторный flush группы не шлёт resolve снова (для testing).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — Health monitor пишет URL таргета с токеном в статус и DEBUG-лог
- **Severity:** minor
- **Location:** `go-app/internal/business/publishing/health_checker.go:108-123` (`sanitizeErrorMessage(fmt.Sprintf("HTTP request failed: %s", err))`), `health_errors.go:166-200`, логи `health_worker.go:138-141`, `health_status.go:99-103`
- **Issue:** health monitor включён по умолчанию (`config.go:1113`) и делает GET на `target.URL`. При транспортной ошибке (DNS, тайм-аут, TLS) текст `*url.Error` содержит полный URL — для Slack/Teams/Discord/Google Chat/Telegram это токен в пути. `sanitizeErrorMessage` вырезает только `token=`/`api_key=`/`Bearer`, не путь. Сообщение уходит в `TargetHealthStatus.ErrorMessage` и в DEBUG-логи. То же у `http.NewRequestWithContext` (`:108-111`, без sanitize). Вне перечня `PUBLISH-SECRETS-IN-LOGS`, но строка CHANGELOG «publishing no longer writes target credentials to the log» сейчас шире, чем фикс.
- **Recommendation:** в `health_checker.go` пропускать ошибку через `core.RedactURL` для `*url.Error` (тот же приём, что `redactURLError`), либо сузить формулировку CHANGELOG до затронутых путей и завести bug. Удобно объединить с `PUBLISH-HEALTH-PROBES`, который и так меняет health checker.
- **Disposition:** defer-bug
- **Follow-up:** BUGS.md (finalize): новая запись или дополнение к `PUBLISH-HEALTH-PROBES` — «health checker: URL таргета с токеном в ErrorMessage/DEBUG».

### F3 — PD/Rootly кладут в ошибку до 64 KiB сырого тела ответа
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/publishing/pagerduty_client.go:542`, `go-app/internal/infrastructure/publishing/rootly_client.go:395,399`
- **Issue:** Success Criterion требует «тело ответа в ошибке ≤512 байт», а Spec § 6 сузил это до `HTTPPublisher`. Если ответ не JSON (HTML-страница прокси или WAF, 502 балансировщика), PD и Rootly копируют всё прочитанное тело — до 64 KiB — в текст ошибки. Дальше оно попадает в `Failed to publish after retries` и в DLQ.
- **Recommendation:** `errorBody(body)` в этих трёх местах (одна строка на место).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — Тайм-аут пробы равен `Timeout` breaker'а (30s) и короче худшей длительности задания
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/publishing/circuit_breaker.go:99`, `queue.go:1264-1266`
- **Issue:** проба, которая честно выполняется дольше 30s (ретраи клиента и очереди, `MaxDelay` 30s, `DeliveryConfirmationTimeout` 45s), считается зависшей, и слот выдаётся второй пробе, пока первая ещё идёт. Для «висящего» таргета — ровно тот случай, ради которого есть breaker — получается две конкурентные пробы вместо одной. Кроме того, результат попытки, допущенной ещё в `Closed`, но завершившейся в `HalfOpen`, засчитывается как результат пробы и освобождает слот. Масштаб ограничен (≤2 пробы), критерий «зависшая проба не блокирует навсегда» выполнен.
- **Recommendation:** отдельный тайм-аут пробы ≥ максимальной длительности задания (например, `DeliveryConfirmationTimeout` + запас) либо токен пробы (`CanAttempt` возвращает поколение, `Record*` учитывает только своё). Зафиксировать как техдолг вместе с `PUBLISH-SINGLE-RETRY-LAYER`, который меняет длительность задания.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT.md (finalize): «breaker: тайм-аут пробы = Timeout; результат не-пробы засчитывается в half-open».

### F5 — Ошибка Redis на firing создаёт второй инцидент и затирает ID первого — первый не закрывается никогда
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/publishing/rootly_incident_store.go:57-66`, `rootly_publisher_enhanced.go:71-80,118`
- **Issue:** Spec Edge Case 2 называет это «дубль лучше потерянного алерта». Но если `Get` упал по ошибке Redis, а `Set` после create прошёл, ключ указывает на новый инцидент, а старый остаётся открытым в Rootly навсегда: его ID больше нигде не хранится. Это не только дубль, а осиротевший инцидент.
- **Recommendation:** при ошибке хранилища (не `ErrNotFound`) на firing вернуть временную ошибку: доставка повторится на следующем flush, когда Redis доступен. Это ложится на то же разделение «ошибка ≠ промах», что в F1. Иначе — явно описать сиротство в Spec и CHANGELOG.
- **Disposition:** fix-here (вместе с F1, тот же сигнатурный шаг `Get` → `(id, found, err)`)
- **Follow-up:** n/a

### F6 — Ключ Rootly привязан к позиционному имени таргета
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/publishing/publisher.go:926-928` (`correlationKey`), `go-app/internal/business/publishing/config_targets.go:114-115` (`ConfigTargetName(receiver, kind, index)`)
- **Issue:** имя конфиг-таргета содержит индекс в списке `rootly_configs`. Вставка конфига перед существующим или переименование receiver'а меняет ключ, и ID открытых инцидентов теряются, что ведёт в F1. Это та же идентичность, что у nflog, поэтому решение согласовано с остальной системой; для персистентного ключа с TTL 7d эффект заметнее, чем для in-memory кеша.
- **Recommendation:** упомянуть в CHANGELOG и доках Rootly (reorder или rename ⇒ закрыть открытые инциденты вручную). Стабильный идентификатор таргета — в бэклог.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG.md (finalize): «стабильный ID конфиг-таргета для ключей корреляции».

### F7 — Ошибка `http.NewRequestWithContext` в `HTTPPublisher` не маскируется
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/publishing/publisher.go:110,170`
- **Issue:** для неразбираемого URL `url.Parse` возвращает `*url.Error` с полным исходным URL, а эта ветка `redactURLError` не проходит. Случай редкий (дискавери отсекает невалидные URL, но не конфиг-таргеты), и токен в опечатанном URL остаётся токеном.
- **Recommendation:** `redactURLError(err)` и в этих двух ветках.
- **Disposition:** fix-here
- **Follow-up:** n/a

## Раунд 2 (2026-10-23, @ ee44f0d)

**Reviewer perspective:** независимый агент (не автор фикса и не ревьюер раунда 1). Проверены дифф `3e3d7f7..ee44f0d`, все вызовы `IncidentIDCache.Get`, классификация ошибки хранилища в очереди и notify chain, согласованность доков; по линзе `S` повторно пройден весь путь доставки webhook-таргетов. `go build ./...` проходит; `go vet` пакета publishing падает только на тестах (`pagerduty_cache_test.go: undefined: NewEventKeyCache`), это ожидаемо до Phase 3.

### Статус находок раунда 1

| ID | Статус | Обоснование |
|---|---|---|
| F1 | **resolved** | `resolveIncident`: при промахе WARN, `RecordAPIError(rootly,"incidents","not_tracked")` и `return nil` (`rootly_publisher_enhanced.go:215-230`). Очередь видит успех: breaker не получает `RecordFailure`, DLQ не пишется, notify chain засчитывает цель и делает prune. Ошибка хранилища отделена от промаха: `cacheIncidentIDCache.Get` возвращает `found=false, err=nil` только для `cache.IsNotFound`. `IsNotFound` проверяет тип через type assertion, а не через `errors.As`, но обе реализации `cache.Cache` (`RedisCache`, `MemoryCache`) возвращают голый `ErrNotFound`, обёрток над `r.cache` нет (`service_registry.go:1244/1275`). Поэтому обычный промах не превращается в ошибку. `ErrIncidentNotTracked` удалён, ссылок в prod-коде нет. |
| F3 | **resolved** | PD `parseError` и обе ветки Rootly `parseError` идут через `errorBody` (≤512 + `...(truncated)`). Ветки с разобранным JSON (`errorResp.Message`, `errors[0]`) берут короткое поле провайдера. Тот же класс дефекта остаётся в Slack, Telegram и webhook, см. F8/F9. |
| F5 | **resolved** | `createOrUpdateIncident` возвращает ошибку `Get` до create (`:73-76`). Повтор: `CacheError` разворачивается через `Unwrap`, поэтому `net.OpError` от Redis классифицируется как transient и даёт ретрай в очереди; `ErrConnectionFailed`/прочее — Unknown (DLQ, повтор на следующем flush). Оба исхода дают повтор без create. Путь 404 на update → `Delete` → `createIncident` не вызывает `Get` и не изменился. Путь update (`Set` с продлением TTL) не изменился. Других вызовов `IncidentIDCache.Get` в prod-коде нет (только строки 73 и 209). |
| F7 | **resolved** | `publisher.go:110,170`: `redactURLError(err)` до `%w`; цепочка `*url.Error` сохраняется. Тот же дефект остаётся в `WebhookHTTPClient`, см. F8. |

Доки согласованы: requirements (Success Criterion и Compatibility), Spec (§3, Edge Cases 2–3, Impact/Risks), tasks (1.4, 3.4, Notes) и CHANGELOG описывают новое поведение одинаково. В Spec Risks добавлены источники промаха и цена недоступного Redis (доставка в Rootly блокируется, breaker считает), это совпадает с кодом. `research.md:70,82` ещё упоминает `ErrIncidentNotTracked`, но это исторический документ, править не нужно.

### Отложенные находки раунда 1 (проверено, что отложены корректно)

- **F2** (defer-bug, health checker). Код не трогали, отложить правильно: health checker — это вне пути доставки, а задача `PUBLISH-HEALTH-PROBES` и так меняет этот файл. CHANGELOG теперь прямо говорит «Target health checks still record the full URL on a transport error (tracked separately)», так что формулировка больше не шире фикса. На finalize нужна запись в `BUGS.md`.
- **F4** (defer-tech-debt, тайм-аут пробы). Критерий «зависшая проба не блокирует breaker» выполнен, проб максимум две. На finalize нужна запись в `TECH-DEBT.md`; туда же отнести F10.
- **F6** (defer-backlog, позиционный ключ таргета). Spec Risks теперь называет «перестановку `rootly_configs`» источником промаха. В CHANGELOG об этом не сказано; рекомендация из F6 (одна фраза про reorder/rename) остаётся в силе для finalize, на gate не влияет.

### Новые находки

#### F8 — `WebhookHTTPClient` пишет URL таргета с токеном в лог и в ошибку (основной путь Teams/Discord/Google Chat)
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/publishing/webhook_client.go:99-101` (`failed to create request: %v`), `:175-184` (`HTTP request failed: %v` + `c.logger.ErrorContext("Network error", "error", err.Error())`), `:227-229` (`HTTP %d: %s` с `string(body)`); дальше `webhook_publisher_enhanced.go:128-133,246-249` (`Failed to publish alert`, `error`) и `queue.go` (`Failed to publish after retries`)
- **Issue:** конфиг-таргеты из `webhook_configs` (туда попадают Teams, Discord, Google Chat с токеном в пути URL) и тип по умолчанию собираются через `CreatePublisherForTarget` → `createEnhancedWebhookPublisher` → `WebhookHTTPClient.Post`. `HTTPPublisher` здесь не участвует, он только basic-fallback. При транспортной ошибке (DNS, connect refused, TLS, тайм-аут) `http.Client.Do` возвращает `*url.Error`, а его текст — `Post "https://<host>/<токен>": ...`. Этот текст через `%v` попадает (1) в ERROR-лог `Network error` на каждой попытке, (2) в сообщение `HTTPAPIError`, (3) в ERROR `Failed to publish alert` и `Failed to publish after retries`, (4) в DLQ. Маскировка `url` соседним полем (`maskURL(target.URL)`) это не перекрывает. Это ровно симптом `PUBLISH-SECRETS-IN-LOGS` («токены Teams/Discord/Google Chat из URL попадают в логи»). Запись в `CHANGELOG` § Security утверждает, что дефект исправлен, но на основном пути он остался. Исследование (`research.md:40`) перечислило немаскированные места без `webhook_client.go`, и раунд 1 эту дыру тоже не увидел. Вдобавок ветка HTTP-ошибки цитирует всё тело ответа (до 64 KiB), а Success Criterion требует ≤512.
- **Recommendation:** в `webhook_client.go` вызвать `redactURLError(err)` до форматирования в обеих ветках (`NewRequestWithContext` и `Do`). Функция правит `*url.Error` на месте, поэтому и `%v`, и `err.Error()` в логе, и `cause` получат маскированный URL, а классификация через `errors.As` сохранится. В `:228` заменить `string(body)` на `errorBody(body)`. Тест в 3.7: транспортная ошибка webhook-клиента на URL с токеном ⇒ ни лог, ни ошибка не содержат токена.
- **Disposition:** fix-here
- **Follow-up:** n/a

#### F9 — Slack и Telegram цитируют сырое тело ответа без лимита 512 в fallback-ветке
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/publishing/slack_errors.go:124`, `telegram_errors.go:103`
- **Issue:** если тело ответа не JSON (HTML-страница прокси, WAF или балансировщика), `apiErr.Message = string(body)` копирует до 64 KiB в ошибку. Это нарушает тот же Success Criterion «тело ответа в ошибке ≤512 байт», что и F3. Токен в URL не утекает: Slack маскирует ошибки через `maskedError`, Telegram — через `maskBotToken`. Поэтому severity minor.
- **Recommendation:** `errorBody(body)` в обоих местах, по одной строке. Удобно сделать вместе с F8.
- **Disposition:** fix-here
- **Follow-up:** n/a

#### F10 — Промах Rootly resolve засчитывается breaker'у как успех
- **Severity:** nit
- **Location:** `rootly_publisher_enhanced.go:229` (`return nil`) → `queue.go:1230` (`cb.RecordSuccess()`)
- **Issue:** промах не обращается к Rootly, но в `HalfOpen` засчитывается как успешная проба и может закрыть breaker без живого ответа API. До фикса поведение было таким же (`return nil`). Масштаб маленький: нужна серия промахов именно в окне half-open.
- **Recommendation:** учесть вместе с F4 (токен пробы или «попытка без сети не считается пробой»).
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT.md (finalize), та же запись, что F4.

### Итог раунда 2

F1, F3, F5 и F7 закрыты по существу, регрессий в путях create, update, 404→recreate и resolve нет. Gate — `fix_required` из-за F8 (major, fix-here); F9 исправляется заодно. После исправления нужен короткий третий раунд по диффу F8/F9.

## Раунд 3 (2026-10-23, @ bb21bbb)

**Reviewer perspective:** тот же независимый агент, что в раунде 2. Проверен дифф `ee44f0d..bb21bbb`, затем пройден заново по линзе `S` весь пакет `infrastructure/publishing` и соседние места: `business/publishing/discovery*`, `infrastructure/llm`. Искал все `http.Client.Do`, `NewRequestWithContext`, `string(body)`, `io.ReadAll` и поля `url`/`endpoint` в логах. Health checker (F2) исключён, он отложен. `go build ./...` проходит.

### Статус находок раунда 2

| ID | Статус | Обоснование |
|---|---|---|
| F8 | **resolved** | `webhook_client.go:101` и `:179`: `err = redactURLError(err)` стоит до `Sprintf("%v")` и до `NewHTTPErrorWithCause(..., err)`. Функция правит `*url.Error` на месте, поэтому маскированный URL попадает во все последующие места: в сообщение `lastErr`, в `cause` (дальше `Failed to publish alert`, `Failed to publish after retries`, DLQ) и в ERROR `Network error` (`err.Error()` на той же переменной). Ретраи не сломаны: `httperror.IsRetryable(lastErr)` смотрит на тип и cause, а цепочка `errors.As` до `net.OpError` сохраняется (`redactURLError` возвращает тот же объект). `:231`: тело ответа идёт через `errorBody`. |
| F9 | **resolved** | `slack_errors.go:124` и `telegram_errors.go:103` используют `errorBody(body)`. Ветки с разобранным JSON (`slackResp.Error`, `tgResp.Description`) берут короткое поле провайдера. |

`tasks.md` дополнен: задача 3.7 теперь покрывает и `WebhookHTTPClient`, в Notes добавлена запись о фиксе F8/F9.

### Повторный проход по линзе секретов (publishing)

Без находок:
- **Транспортные ошибки `Do`.** HTTPPublisher, webhook, Slack (`maskedError`), Telegram (`maskBotToken` в SendMessage и Health) маскируют URL. Для PagerDuty (`pagerduty_client.go:411-416`) и Rootly (`rootly_client.go:318`) URL — это API endpoint без секрета: ключ PD едет в теле, токен Rootly — в заголовке.
- **Тела ответов в ошибках.** Все ветки сырого тела в publishing (`publisher.go`, PD, Rootly, Slack, Telegram, webhook) идут через `errorBody`. Все чтения ответа ограничены `readLimited`. Оставшийся `io.ReadAll` в `rootly_client.go:313` читает тело собственного запроса для ретрая, это не ответ.
- **Поля `url` в логах.** `webhook_*`: `maskURL` (делегирует в `core.RedactURL`). Фабрика и discovery: `core.RedactURL`. Discovery не логирует `ValidationError.Value` (`discovery_impl.go:456-466`).
- **`Authorization`, `routing_key`, SMTP-пароль** в логах не встречаются.
- **`PublishingHandlers.GetTarget`** (`handlers.go:283`) отдаёт `URL` и `Headers` по API, но `NewPublishingHandlers` нигде в prod не вызывается, маршрут не смонтирован. Это мёртвый код, вне задачи.

### Новые находки

#### F11 — Ошибка `NewRequestWithContext` в Slack- и Telegram-клиентах не маскируется
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/publishing/slack_client.go:122-124`, `telegram_client.go:264-266,282-284`
- **Issue:** тот же класс, что F7: если URL не разбирается, `url.Parse` возвращает `*url.Error` с полным URL, а для Slack токен и есть URL, для Telegram в URL лежит bot token. Эти ветки, в отличие от `Do`, через `maskedError`/`maskBotToken` не проходят. Практически почти недостижимо: Telegram-endpoint собирается из фиксированного base и токена, а Slack URL с опечаткой должен пройти валидацию конфига.
- **Recommendation:** обернуть `redactURLError(err)` (Slack) и `maskBotToken` (Telegram), по одной строке. Удобно сделать вместе с тестами 3.7. Gate не блокирует.
- **Disposition:** fix-here (nit, не блокирует gate)
- **Follow-up:** n/a

#### F12 — LLM-клиент пишет тело ошибки провайдера (до 1 MiB) в ERROR-лог и в ошибку
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/llm/client.go:317-323,419,608`, `investigate_with_tools.go:126`
- **Issue:** Success Criterion «тело в ошибке ≤512 байт» Spec § 6 сузил до publishing, а LLM в задаче получил только `LimitReader(1 MiB)`. Поэтому это вне scope бандла. Это не секрет таргета, но ошибки провайдера раздувают лог до 1 MiB на строку.
- **Recommendation:** обрезать тело до 512 в этих четырёх местах (свой хелпер в `llm` или вынести `errorBody` в общий пакет). Отдельной задачей.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG.md (finalize): «llm: тело ошибки провайдера в логе и ошибке без обрезки».

### Итог раунда 3

F8 и F9 закрыты. Открытых blocker и major с fix-here нет; F2, F4, F6, F10 и F12 отложены с follow-up на finalize; F11 — nit без влияния на gate. **Gate — `pass`.** Можно переходить к `write-tests` (Phase 3).

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
