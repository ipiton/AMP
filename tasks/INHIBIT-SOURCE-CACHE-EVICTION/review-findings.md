# Deep Review Findings: источник inhibition выпадает из кеша через 5 минут горения

**Trigger classification:** mandatory (Full tier по правилу конвейера ревью: семантика Alertmanager; сигналы `X R`; novel pattern — фоновый синк L2→L1 с tombstone)
**Reviewer perspective:** независимый агент-ревьюер (не автор кода); линзы correctness/гонки синка, premises (сверка с upstream `alertmanager@v0.32.0`), X, R, совместимость Redis, HA-задержка, maintainability
**Reviewed at:** 2026-10-22
**Reviewed tree:** bugfix/inhibit-source-cache-eviction @ 616aeef (раунд 1), @ f326a0d (раунд 2)
**Verdict:** раунд 1 — fix_required; раунд 2 — pass (see `review-verdict.json`)

## Проверено (без находок)

- **Premise 1** (активен ⇔ `!ResolvedAt(now)`; без `endsAt` — `now + resolve_timeout` на каждом приёме): верна — `inhibit/inhibit.go:362-379` (`findEqualSourceAlert` → `alert.ResolvedAt(now)`), `api/v2/api.go:350-356`. Класс `code-read` заслужен. `isActive` проверяет в момент запроса — совпадает.
- **Premise 2** (upstream обновляет `scache` на каждый Put, включая неизменённый resend): верна — `provider/mem/mem.go:303-360` шлёт алерт всем listener'ам безусловно после `Set`, `inhibit.go:109-130` пишет в `scache` без сравнения. Заслужен.
- **Premise 3** (единственный писатель — `AlertProcessor.ProcessAlert`): верна. `AddFiringAlert`/`RemoveAlert` в prod-коде вызываются только из `alert_processor.go:543,565,573`; все ingest-пути (`handlers/alerts.go:442`, `webhook/handler.go:85,222`, `cmd/server/handlers/prometheus_alerts.go:555`, `webhook.go:157`) идут через `ProcessAlert`; экземпляр кеша один (`service_registry.go:1413`, `:2393`). Класс `call-path-traced` заслужен.
- **Premise 4** (`EndsAt == nil` на ingest без `endsAt`): верна — `alertconv.ParseOptionalAlertTime` (`handlers/alerts.go:532`), `webhook/parser.go:133-136`, `prometheus_parser.go:288-291`; окно `resolve_timeout` ставит только `memory.AlertStore.stampResolveTimeout` на своей копии. Даже если бы `EndsAt` был проставлен, дубликат пошёл бы как `updated` и всё равно обновил кеш — двойного окна нет.
- **Premise 5** (`r.cache` — Redis или `MemoryCache`; оба отдают `cache.ErrNotFound` на отсутствующий ключ): верна (`cache/redis.go:217-219`, `cache/memory.go:47,54`) — `getFromRedis` не примет истёкший ключ за ошибку Redis.
- **X, `alert_processor.go`**: ignored-путь только добавляет `AddFiringAlert` (ошибка логируется, `return nil` сохранён); дедупликация, публикация и `InhibitionState` не затронуты. `alert.Fingerprint` к этому моменту уже проставлен `deduplicationService.ProcessAlert:213`. `ProcessActionIgnored` возвращается только при равных статусе и `EndsAt` (`deduplication.go:347-366`) — resolved-дубликат кеш не трогает, как в Spec. Для Prometheus (новый `endsAt` на каждом resend → `updated`) нагрузка на горячий путь не меняется; для webhook без `endsAt` добавляется Set+SAdd в Redis на дубликат — приемлемо.
- **X, wiring**: `resolveTimeoutFromConfig(r.config)` читается на каждый add — тот же паттерн, что `newAlertStore`; hot reload `resolve_timeout` применяется к следующему ingest. `Stop()` вызывается на shutdown (`service_registry.go:2577-2579`); `initializeInhibition` на reload не пересоздаёт кеш — утечки горутины синка нет. Первый синк идёт в горутине воркера, `Stop` ждёт его не дольше `syncInterval` (таймаут контекста).
- **Недоступный Redis**: `getFromRedis` возвращает ошибку на SMEMBERS и на любой GET, кроме not-found → синк не трогает L1 (не принимает недоступный Redis за пустой). Верно.
- **Совместимость Redis**: формат ключей/SET и JSON `core.Alert` не меняется. Запись старой версии без `EndsAt` при синке получает `passStart + resolve_timeout` и продлевается на каждом проходе, пока жив ключ (старый TTL ≤5m), затем снимается — ограничено старым поведением. Старая версия читает новые записи (с проставленным `endsAt`) корректно; откат безопасен.
- **Слияние «больший `EndsAt` побеждает»**: refresh на другой реплике продлевает окно здесь; локальная запись, сделанная после начала прохода, не перетирается (`writtenAt`). Верно для основных сценариев (ср. F6).
- **Потокобезопасность**: `inRedis`, `tombstones`, `l1Cache` меняются только под `l1Mutex`; синк заменяет записи, а не мутирует `entry.alert`, поэтому указатели, отданные `GetFiringAlerts`, не гоняются. `go test -race` на временных пробах ниже — без data race.
- **git log** затронутых файлов: `cache.go` не менялся с initial release (`d096644`, `13b6edf`) — 5-минутный `StartsAt + ttl` был там с начала, а не внесён позже по причине, которую фикс мог бы сломать.

Поведенческие гипотезы F1–F3 проверены временными тестами на копии `cache.go` во временном пакете (`go test -race`, вне коммита, удалены).

## Findings

### F1 — Синк вытесняет активные записи при заполненном L1: WARN-флуд и перетасовка источников
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:572-575` (`syncFromRedis` → `evictLocked`), `:462` (WARN на каждое вытеснение), `:218` (`l1Max = 1000`)
- **Issue:** кеш хранит **все** firing-алерты (`alert_processor.go:563-566`), а не только источники (upstream хранит в `scache` только алерты, прошедшие `SourceMatchers`). До фикса каждая реплика держала в L1 свою долю алертов; теперь синк сливает в L1 весь кластер, так что лимит 1000 достигается в R раз раньше. При заполнении синк на каждый отсутствующий удалённый алерт вызывает `evictLocked`, а не пропускает его — вопреки Target Design §4 Spec («Синк не добавляет записи сверх ёмкости»). Замер на копии кода: 3000 firing-алертов в Redis → **2000 WARN `Inhibition cache full, evicted an active alert` за каждый проход**, т.е. ~400 WARN/s на реплику постоянно; на каждом проходе L1 перетасовывается (вытесняется запись с самым ранним `EndsAt`, затем следующая удалённая вытесняет только что вставленную), всё это под write-lock с O(N·l1Max) сканами — `GetFiringAlerts` на горячем пути ждёт. Какие источники остаются видимыми — определяется порядком итерации, inhibition «мигает». В доках совместимости лимит 1000 как отличие от upstream не указан.
- **Recommendation:** в этой задаче — привести синк к Spec §4: при `len(l1Cache) >= l1Max` после удаления истёкших не добавлять удалённую запись (локальные записи и запись на ingest остаются приоритетнее), счётчик пропущенных — одним WARN на проход + метрика; WARN в `evictLocked` агрегировать/понизить. Корневое решение (кешировать только алерты, совпадающие с source-матчерами хоть одного правила, как upstream; это же снимает N из F4) — отдельной задачей, вместе с ёмкостью из Open Questions; лимит упомянуть в `ALERTMANAGER_COMPATIBILITY.md`.
- **Disposition:** fix-here (синк не вытесняет активные записи + агрегированный WARN); корневое — defer-tech-debt
- **Follow-up:** TECH-DEBT (finalize): «кеш inhibition хранит все firing-алерты, а не источники; ёмкость l1Max», связать с `INHIBIT-MATCH-PERF`

### F2 — Гонка: новый локальный источник, записанный на стыке с началом прохода, удаляется синком
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:350-353` (`inRedis = true` после SAdd), `:579` (условие удаления)
- **Issue:** `AddFiringAlert` нового fingerprint пишет L1 (`writtenAt = t0`), затем Set и SAdd. Если проход начался после `t0`, но SMEMBERS прочитан до SAdd, а `inRedis = true` выставлен до того, как синк взял lock, то запись не в `seen`, `inRedis`, `writtenAt < passStart` → синк её удаляет. Воспроизведено временным тестом (`X visible after straddling sync: false`). Свежий источник пропадает на реплике, которая его приняла, до следующего прохода (≤5s); ingest-проверки целей в это окно не подавляют. Окно узкое (Redis RTT, умноженный на вероятность попасть на начало прохода), но защита `writtenAt/inRedis` задумывалась именно против такого.
- **Recommendation:** хранить момент подтверждения в Redis (`redisConfirmedAt = now()` после успешного SAdd) и удалять только записи с `redisConfirmedAt.Before(passStart)`; либо не выставлять `inRedis`, если между `writtenAt` и подтверждением начался проход.
- **Disposition:** fix-here
- **Follow-up:** n/a; тест на гонку — в write-tests (3.3)

### F3 — Tombstone ставится до удаления из Redis: разрешённый источник воскресает до 5s
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:373` (tombstone с `now()`), `:384` (Delete после), `:583-587` (очистка tombstone)
- **Issue:** `RemoveAlert` ставит tombstone с моментом `tr`, затем делает Delete/SRem. Проход, начавшийся между `tr` и Delete, видит запись в Redis, tombstone `tr < passStart` его не останавливает → запись возвращается в L1 с `inRedis = true`, а tombstone в конце прохода удаляется. Воспроизведено временным тестом (`X visible after resolve: true`). Источник продолжает подавлять до следующего прохода (≤5s) после resolve. Spec Edge Case 5 обещает обратное.
- **Recommendation:** обновлять метку tombstone после завершения Delete+SRem (или ставить её как `max(tr, момент после Redis-операций)`), чтобы любой проход, прочитавший Redis до фактического удаления, её уважал.
- **Disposition:** fix-here
- **Follow-up:** n/a; тест — в write-tests (3.3)

### F4 — Нагрузка синка: N последовательных GET каждые 5s на реплику, таймаут прохода = интервалу
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:539` (`WithTimeout(syncInterval)`), `:615-626` (цикл GET)
- **Issue:** N — все firing-алерты кластера (F1), GET последовательные, MGET/pipeline в `cache.Cache` нет. Нагрузка R·N/5 GET/s на Redis; при RTT ~0.5ms и N ≳ 10k проход упирается в таймаут 5s, любой GET с ошибкой контекста валит весь проход → чужие источники не видны никогда, WARN каждые 5s. Одна транзиентная ошибка GET тоже обнуляет проход (консервативно, но без частичного прогресса). До N ~1000 (реальный потолок из-за F1) — приемлемо.
- **Recommendation:** зафиксировать в TECH-DEBT вместе с F1 (фильтрация по source-матчерам, MGET/pipeline или Redis hash); добавить метрику/лог длительности прохода против интервала.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT (finalize), связать с `INHIBIT-MATCH-PERF`

### F5 — HA-задержка ≤5s: класс premise не заслужен, оценка риска
- **Severity:** minor
- **Location:** `tasks/INHIBIT-SOURCE-CACHE-EVICTION/Spec.md` Design Premises, строка 7; Open Questions 1
- **Issue:** premise «задержка ≤5s приемлема» помечена `code-read`, но это суждение (в Open Questions сам автор пишет «assumed приемлемой») — класс должен быть `assumed`. По существу: upstream inhibition работает на локальном provider без gossip, и в HA каждая реплика получает все алерты от Prometheus — задержка там ~0 (асинхронная подписка, микросекунды); у upstream есть свой race «цель пришла раньше источника», он не ретроактивен. В AMP цель, пришедшая на реплику A в первые ≤5s после источника на B, не подавляется на ingest, но попадает в группу, и `filterInhibited` на flush после `group_wait` её подавит. Утечка уведомления возможна только при `group_wait` < ~5s для новой группы (дефолт 30s; для существующей группы ждёт `group_interval`). Это хуже upstream, но узкий, документированный случай — не major. До фикса HA-видимости не было вовсе, т.е. фикс строго улучшает.
- **Recommendation:** переклассифицировать premise в `assumed`; в `ALERTMANAGER_COMPATIBILITY.md` уже сказано «within 5 s» — добавить «при `group_wait` < 5s цель может уйти до синка». Вариант C (pub/sub) — в BACKLOG.
- **Disposition:** defer-backlog (pub/sub-инвалидация); переклассификация premise — в рамках fix-here-итерации
- **Follow-up:** BACKLOG (finalize): «pub/sub-инвалидация кеша inhibition для HA (вариант C research)»

### F6 — Слияние EndsAt расходится с upstream `Alert.Merge`; запись с прошедшим EndsAt оставляет старую Redis-запись
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:318-333` (`AddFiringAlert` перезаписывает), `:332` (`ttl <= 0` — Redis не трогается), `:568`
- **Issue:** локальный `AddFiringAlert` всегда перезаписывает `EndsAt`, тогда как upstream `provider/mem` при перекрытии делает `Merge`, где более поздний явный (не timeout) `endsAt` сохраняется. Пример: источник пришёл с `endsAt = now+1h`, потом resend без `endsAt` → AMP сокращает окно до `resolve_timeout`. Отдельно: firing-алерт с уже прошедшим `endsAt` пишется в L1 неактивным, но старая Redis-запись с более поздним `EndsAt` остаётся — следующий синк (больший `EndsAt` побеждает) вернёт её. Оба сценария экзотические (статус нормализуется по `endsAt` на API-пути).
- **Recommendation:** при `ttl <= 0` удалять Redis-ключ и член SET; merge-семантику — по желанию.
- **Disposition:** defer-backlog
- **Follow-up:** n/a (заметка в BACKLOG при finalize, если сочтут нужным)

### F7 — Наблюдаемость синка и мелочи состояния
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:444-473` (`Evictions` считает и истечение, и вытеснение по ёмкости), `:583-587` (tombstones чистятся только успешным синком), `:539` (контекст синка не связан со `stopCh`)
- **Issue:** `evictions_total` смешивает штатное истечение и потерю активного источника по ёмкости — второе видно только по WARN (F1). Ошибку синка видно лишь как `misses_total{tier="l2"}`, длительности против интервала — нет. При долгой недоступности Redis `tombstones` растут на каждый resolve (синк выходит раньше очистки) — память ограничена числом resolve за аутедж. `Stop` может ждать до 5s зависший проход.
- **Recommendation:** label `reason="expired|capacity"` у `evictions_total` (или отдельный счётчик); чистить tombstones старше `syncInterval·2` и при ошибке синка; опционально отменять контекст синка по `stopCh`.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT (finalize), можно одной записью с F1/F4

## Раунд 2 (f326a0d)

**Reviewed at:** 2026-10-22. Дифф `1e38775..f326a0d`: `cache.go` (`redisConfirmedAt` вместо `inRedis`, флаг `fromSync`, повторный tombstone после `Delete`/`SRem`, синк без вытеснения при полном L1, агрегированные WARN), Spec v1.1, `tasks.md`, `CACHE_README.md`, `ALERTMANAGER_COMPATIBILITY.md`. `go build ./...` — OK. Поведение проверено временными тестами на копии `cache.go` (`go test -race`, вне коммита, удалены): F2, F3 (оба порядка: слияние прохода до и после повторного tombstone), повторный firing одного fingerprint (локально и с другой реплики), ёмкость при 3005 алертах в Redis — data race нет.

### Статусы находок раунда 1

| ID | Severity | Статус | Комментарий |
|---|---|---|---|
| F1 | major | **closed** (fix-here часть); корень — defer-tech-debt | Синк больше не вытесняет активные записи: при полном L1 после разовой очистки истёкших удалённые записи пропускаются (`cache.go:607-616`). Проба: 3005 алертов в Redis, L1Max 1000 — все 5 локальных источников остаются, один WARN `skipped_alerts=2005` за проход вместо 2000. Вытеснения на `AddFiringAlert` — DEBUG + счётчик `capacityEvictions`, один WARN за проход очистки (`:483`, `:538-547`). Лимит описан в `ALERTMANAGER_COMPATIBILITY.md`. Остаётся WARN раз в 5s на реплику, пока алертов больше 1000, — это сигнал оператору, а не флуд; корень (кешировать только источники) — TECH-DEBT в finalize. |
| F2 | minor | **closed** | `redisConfirmedAt = now()` после успешного SAdd (`:360`), удаление «пропавших» только при `redisConfirmedAt.Before(passStart)`. Так как `passStart` берётся до SMEMBERS, подтверждённая до него запись видна в SMEMBERS — ложного удаления нет. Проба на сценарии раунда 1: `X visible = true`. |
| F3 | minor | **closed** | Повторный tombstone и удаление записи из L1 после `Delete`/`SRem` (`:409-420`). Проба: проход, начатый между первым tombstone и `Delete`, — запись возвращается и снимается повторной простановкой; проход, слившийся после неё, — пропускает по tombstone; следующий проход запись не возвращает. |
| F4 | minor | open, defer-tech-debt | Без изменений — в finalize. |
| F5 | minor | **closed** (premise); pub/sub — defer-backlog | Premise переклассифицирована в `assumed`, риск `group_wait` < 5s — в Rollout, Open Questions и `ALERTMANAGER_COMPATIBILITY.md`. |
| F6 | nit | open, defer-backlog | Без изменений. |
| F7 | nit | open, defer-tech-debt | Частично: WARN по ёмкости агрегирован; label/счётчик у `evictions_total`, чистка tombstones при ошибке синка, контекст синка от `stopCh` — в finalize. |

### F8 — Параллельные resolve и повторный firing одного fingerprint на одной реплике: L1 и Redis расходятся
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:372-420` (`RemoveAlert`), `:415` (условие `fromSync`)
- **Issue:** `RemoveAlert` (`Delete` в Redis в процессе) и `AddFiringAlert` того же fingerprint идут параллельно: add пишет L1 (`fromSync = false`), Set+SAdd, затем `Delete`/`SRem` от remove стирают только что записанный ключ и член SET. Повторный tombstone верно пропускается (запись локальная), но в L1 источник есть, а в Redis нет, и `redisConfirmedAt` уже выставлен → следующий проход удаляет его как «пропавший». Проба: `refire local: X visible = true` → после синка `false`. Источник пропадает до следующего resend (до `resend_delay` ~1m у Prometheus; для webhook — до повтора). Реалистичный источник — HA-пара Prometheus, рассинхронизированная по оценке (одна шлёт resolved, другая firing). Дефект не внесён раундом 2: в раунде 1 тот же исход давал `inRedis`; в первом раунде я его пропустил. На других репликах исход согласован (там победил remove), расходится только L1 этой реплики.
- **Recommendation:** в `RemoveAlert` после `Delete`/`SRem`, если в L1 лежит локальная запись с `writtenAt` позже первого tombstone, — повторно записать её в Redis (или сбросить ей `redisConfirmedAt`, чтобы синк её не трогал, а при следующем add она снова попала в Redis). Тест — вместе с 3.3.
- **Disposition:** defer-bug
- **Follow-up:** BUGS.md (finalize): «inhibition cache: параллельные resolve/firing одного fingerprint — локальная запись снимается синком»

### F9 — `fromSync`: повторный firing на другой реплике, загруженный синком между первым удалением и повторным tombstone, снимается до 5s
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/inhibition/cache.go:415`
- **Issue:** если реплика B заново зажгла fingerprint после `Delete` на A, а проход A успел загрузить её запись (`fromSync`) до повторного tombstone, A удалит запись и поставит tombstone; следующий проход (после tombstone) вернёт её из Redis. Если же Set на B прошёл до `Delete` на A, ключ стёрт — согласованный исход «победил resolve», как у любого last-writer-wins. Нужны resolve на A и firing на B одного алерта в пределах одного RTT Redis и попадание прохода в это окно; самоисправляется за ≤5s. Согласен с автором: патология.
- **Recommendation:** нет; при желании — сравнивать `EndsAt`/`StartsAt` записи с моментом первого tombstone, но сложность не оправдана.
- **Disposition:** reject (reason: принятый риск — патологическое окно в один RTT Redis, самоисправление ≤5s)
- **Follow-up:** n/a

### F10 — Spec v1.1 местами описывает `inRedis`
- **Severity:** nit
- **Location:** `tasks/INHIBIT-SOURCE-CACHE-EVICTION/Spec.md:57` (Target Design §3 «подтверждённые в Redis (`inRedis`)», «tombstone … до конца прохода»), `:63` (Component Architecture `cacheEntry{alert, writtenAt, inRedis}`)
- **Issue:** код и §5 уже на `redisConfirmedAt`/`fromSync`; §3 и Component Architecture остались от v1.0 — Spec противоречит сам себе.
- **Recommendation:** заменить на `redisConfirmedAt`/`fromSync`, сослаться на §5.
- **Disposition:** fix-here (не блокирует gate; можно попутно в write-tests/finalize)
- **Follow-up:** n/a

### Вердикт раунда 2

Открытых blocker нет; единственный major (F1) закрыт в части fix-here, корень отложен с disposition. Новые находки — minor (defer-bug) и nit. **Gate: pass.**

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
