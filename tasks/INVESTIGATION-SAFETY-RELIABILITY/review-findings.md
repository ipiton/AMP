# Deep Review Findings: надёжность LLM-вызовов и очереди расследований

**Trigger classification:** mandatory
**Reviewer perspective:** independent general-purpose agent
**Reviewed at:** 2026-10-28
**Reviewed tree:** bugfix/investigation-safety-reliability @ 0c48423
**Verdict:** fix_required (see `review-verdict.json`)

Линзы: S (тело ответа LLM в логах/БД, вход от LLM в tools), X (llm ↔ resilience ↔ queue ↔ registry ↔ dedup ядра), R (ingest `POST /api/v2/alerts`, hot reload), correctness, premises, maintainability.

Проверка Design Premises: 1 (`WithRetryFunc` — один потребитель) подтверждена grep по всему репозиторию, класс заслужен; 2 (незаданный ключ → 3) верна для старта, но hot reload с явным `0` не покрыт (F2); 3 (`classifyError` по тексту `HTTP %d`) верна; 4 (статус `queued` читает только `GET /api/v1/alerts/{fp}/investigation`) подтверждена (router.go:37, handlers/investigation.go:37; UI-потребителей нет); 5 (кеш до investigation) верна. 6 (resend сохраняет `StartsAt`) в таблице сформулирована не той стороной: стабильность resend обеспечивает БД (кроме точности, F6), а обратная, неявная предпосылка «новый инцидент получает новый `StartsAt`» ложна (F1).

## Findings

### F1 — повторное срабатывание после resolve получает старый `StartsAt` из БД и глушится дедупом на 24h
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/investigation/dedup.go:175`; `go-app/internal/core/services/deduplication.go:369-384`; `go-app/internal/core/services/alert_processor.go:574-576`
- **Issue:** `AlertProcessor` после дедупа ядра передаёт в `Submit` `dedupResult.Alert`, то есть `existing` из БД. `updateExistingAlert` переносит только `Status`/`EndsAt`/`Annotations`, `StartsAt` остаётся от первой записи (`UPDATE alerts SET starts_at = $6` пишет то же старое значение). Сценарий fire(T1) → resolve → fire(T2): второе срабатывание идёт через update-путь с `StartsAt = T1`, ключ `fp:T1` уже занят (локально и в Redis), расследования нет до истечения 24h. До изменения такой инцидент расследовался. CHANGELOG («a re-fire after resolve has a new `startsAt` and is investigated again») и инвариант Spec («новый инцидент — новый `StartsAt`») ложны. Для флапающих и ежедневных алертов расследования фактически выключаются. Строки удаляются только retention-очисткой (`starts_at < cutoff`), не на resolve.
- **Recommendation:** не выводить идентичность инцидента из `StartsAt` сохранённого алерта. Варианты: снимать claim по fingerprint, когда `Submit` видит resolved (до фильтра `OnlyFiring`), а shared-ключ удалять в воркере или асинхронно, без IO в `Submit`; или брать `StartsAt` входящего алерта до подмены на `existing`. Менять ядро алертинга (ADR-019) не нужно. Добавить предпосылку в Design Premises и тест fire → resolve → fire в пределах TTL ⇒ два расследования (на уровне `AlertProcessor` с реальным `deduplicationService`).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — `llm.max_retries: 0` при hot reload молча остаётся прежним
- **Severity:** major
- **Location:** `go-app/internal/config/reloadable_llm.go:182-184`; тест `go-app/internal/config/reloadable_llm_test.go:93-105`
- **Issue:** `llmClientConfigFrom` применяет `MaxRetries` только при `> 0`, так что смена `3 → 0` через SIGHUP/API не доходит до клиента. При этом `l.applied = newCfg.LLM`, drift пуст, restart-required warning не ставится, и reload сообщает об успехе. Получается тот же класс бага, что чинит задача (`0` превращается в «не задано»), только в другом пути. CHANGELOG обещает, что `0` отключает ретраи, без оговорок. Тест `TestLLMReloadable_ZeroTimeoutAndRetriesKeepPreviousValues` закрепляет старое поведение. Перезагрузка идёт через тот же viper с `SetDefault("llm.max_retries", 3)`, поэтому «не задано» сюда как `0` не приходит.
- **Recommendation:** присваивать `next.MaxRetries = max(cfg.MaxRetries, 0)` без условия (для `Timeout` условие можно оставить). Тест разделить: `Timeout: 0` сохраняет прежнее значение, `MaxRetries: 0` применяется. Затем hot reload `3 → 0` должен давать одну попытку.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — claim не снимается, когда job не дал результата: ошибка `Create` и остановка процесса глушат инцидент на 24h
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/investigation/queue.go:288-315` (claimAndCreate), `queue.go:216-218` (выход по `q.ctx.Done()`)
- **Issue:** `claimShared` делает `SETNX` до `repo.Create`. Если `Create` падает (кратковременный сбой PostgreSQL, таймаут), job отбрасывается, но локальный claim и Redis-ключ остаются на 24h. Записи расследования нет, нет и записи об ошибке, повторный resend отсекается: инцидент тихо не расследуется по всему кластеру. Это противоречит обоснованию для переполненной очереди («claim снят: следующий resend снова попробует», Edge Case 2). Тот же эффект при `Stop` по таймауту: прерванный в backoff или LLM-вызове job оставляет статус `processing` и Redis-claim, и после rolling restart другие реплики инцидент не берут. Edge Case 3 разрешает держать claim после неудачного расследования, но здесь расследование не состоялось.
- **Recommendation:** при ошибке `Create` вызвать `releaseLocal` и best-effort `DEL` shared-ключа (отдельный короткий ctx). При выходе по отмене ctx — то же. Покрыть тестами: `Create` → error ⇒ следующий `Submit` того же инцидента ставит job; miniredis-ключ удалён.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — ретраи 429/5xx удлиняют синхронный ingest на секунды на каждый алерт батча
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/llm/errors.go:46-49`, `go-app/internal/infrastructure/llm/client.go:222-237`; вызов: `go-app/internal/core/services/alert_processor.go:835`, `go-app/internal/application/handlers/alerts.go:436-441`
- **Issue:** `ClassifyAlert` вызывается синхронно, алерты батча `POST /api/v2/alerts` обрабатываются последовательно. Раньше 503/429 сразу давали fallback в transparent. Теперь, с дефолтами клиента (`RetryDelay` 1s, ×2, потолок 10s, 3 ретрая), на каждый алерт добавляется около 7s сна плюс RTT трёх попыток, пока circuit breaker не откроется (5 подряд неудачных вызовов ≈ 35s+, затем снова после half-open). Prometheus notifier по умолчанию ждёт 10s, так что батч из двух алертов во время outage провайдера превращается в таймаут отправителя и повторную отправку. На 429 `Retry-After` не учитывается, ретраи только усиливают давление на rate limit. Бюджет времени LLM в ingest — явный non-goal (`UPSTREAM-AI-HOOKS`), но Spec (Impact Analysis/Risks) и CHANGELOG этот регресс не называют.
- **Recommendation:** бюджет времени и вынос классификации из синхронного пути оставить в `UPSTREAM-AI-HOOKS`, туда же добавить учёт `Retry-After`. В этой задаче раскрыть компромисс в документации (F5).
- **Disposition:** defer-bug
- **Follow-up:** `docs/06-planning/BUGS.md` — завести на `finalize` (или дополнить `UPSTREAM-AI-HOOKS`) запись о латентности ingest при 429/5xx и о `Retry-After`

### F5 — CHANGELOG и Spec не раскрывают рост латентности ingest при ошибках провайдера
- **Severity:** minor
- **Location:** `CHANGELOG.md` (`[Unreleased]` → Fixed, «LLM rate limits and server errors are retried»); `tasks/INVESTIGATION-SAFETY-RELIABILITY/Spec.md` § Impact Analysis
- **Issue:** оператор узнаёт только, что 429/5xx теперь ретраятся. О том, что каждый алерт в `POST /api/v2/alerts` при outage провайдера ждёт секунды и что `llm.max_retries` — единственный рычаг, не сказано.
- **Recommendation:** одна фраза в CHANGELOG (Fixed или Breaking changes): при 429/5xx классификация ждёт backoff до `llm.max_retries` раз на алерт в ingest; для минимальной латентности поставить `0`. Добавить строку в Spec Risks.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — ключ дедупа на `UnixNano` расходится с точностью PostgreSQL: первый resend даёт второе расследование
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/investigation/dedup.go:175-177`
- **Issue:** первое срабатывание идёт через create-путь с `StartsAt` из payload (RFC3339 с наносекундами, Prometheus так и шлёт), последующие — через update-путь со `StartsAt` из `timestamptz`, у которого микросекундная точность. Ключи `fp:<ns>` различаются, на один инцидент приходится два расследования. Вывод сделан чтением кода; подтвердить на `testing`.
- **Recommendation:** нормализовать время в ключе (`StartsAt.Unix()` или `UnixMilli()`, как у Alertmanager). Тест: тот же инцидент с ns-`StartsAt` и с усечённым до µs даёт один ключ.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — при `investigation.only_firing: false` resolved-событие глушится claim'ом своего firing
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/investigation/queue.go:141-148`
- **Issue:** ключ не учитывает статус. Resolved-алерт того же инцидента (тот же fingerprint и `StartsAt`) попадает в `claimLocal` → dedup, хотя до изменения он расследовался. Это нарушает инвариант Spec «Resolved-алерты и `OnlyFiring` — как раньше».
- **Recommendation:** не применять дедуп к resolved либо включить статус в ключ. Решение согласовать с F1: resolved — естественная точка снятия claim.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F8 — `claimShared` и `Create` делят один 5-секундный ctx: «fail-open» при зависшем Redis может уронить job
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/investigation/queue.go:285-308`
- **Issue:** проверено запуском: с дефолтами AMP (`redis.read_timeout: 3s`, `max_retries: 3`) зависший Redis отвечает `i/o timeout` через ~3.0s, после чего на `Create` остаётся ~2s. При `read_timeout` ≥ 5s ctx истекает раньше, `claimShared` возвращает fail-open, а `Create(ctx)` падает на просроченном контексте. Job дропается, claim остаётся (F3). Кроме того, каждый job в такой ситуации держит воркер ~3s.
- **Recommendation:** отдельные таймауты для claim (короткий, например 1s) и для `Create`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F9 — кэп observation 16 KiB режет с головы: логи pod теряют самые свежие строки, лимиты tools становятся мёртвыми
- **Severity:** minor
- **Location:** `go-app/internal/core/investigation/agent_loop.go:209,274-286`; `go-app/internal/infrastructure/investigation/tools/kubernetes.go:455-459`
- **Issue:** `get_logs` возвращает хвост в хронологическом порядке, а `capObservation` сохраняет первые 16 KiB. При `tail_lines` 500 (около 75 KiB) LLM видит самые старые ~100 строк, а свежие, релевантные алерту, пропадают. JSON (`{"logs":…}`, массивы Loki и Prometheus) режется посередине. Ответ 16–256 KiB никогда не превращается в ошибку «narrow the query»: модель получает обрезанный документ. Ограничения 256 KiB и 500 строк задуманы как лимиты, но фактически режет кэп observation, а обоснование `readLimited` («truncated JSON would not decode anyway») этому противоречит.
- **Recommendation:** для логов k8s сохранять хвост (и при 256 KiB, и в кэпе), либо согласовать лимиты так, чтобы tool сам укладывался в бюджет observation. Как минимум, Loki/Prometheus с ответом больше бюджета observation должны получать ту же ошибку «narrow the query».
- **Disposition:** fix-here
- **Follow-up:** n/a

### F10 — 429/5xx в agent loop (`InvestigateWithTools`) не ретраятся, а 24h-claim не даёт повторить расследование
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/llm/investigate_with_tools.go:123-127`, `go-app/internal/infrastructure/investigation/queue.go:319-342`
- **Issue:** критерий «429 и 5xx ретраятся (classify и investigate)» закрыт только для `InvestigateAlert` (ретраи `processJob`). Шаги agent loop ретраев не имеют. После этой задачи кратковременный 503 посреди agentic-цикла даёт `failed`, и инцидент не расследуется повторно 24h (Edge Case 3). Цена ошибки выросла.
- **Recommendation:** ретраи или CB в investigate-пути — вместе со срезом 2 `INVESTIGATION-PROMPT-ISOLATION` (там уже стоит CB в investigate-пути). Записать follow-up.
- **Disposition:** defer-bug
- **Follow-up:** `docs/06-planning/BUGS.md` — запись на `finalize` (или пункт в `INVESTIGATION-PROMPT-ISOLATION`)

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.

---

## Раунд 2

**Reviewer perspective:** independent general-purpose agent (тот же ревьюер, что в раунде 1; код не писал)
**Reviewed at:** 2026-10-28
**Reviewed tree:** bugfix/investigation-safety-reliability @ e9991d9 (дифф раунда `0c48423..e9991d9`)
**Verdict:** fix_required (see `review-verdict.json`)

Запуском проверено: `go build ./...` и `go vet ./internal/...` проходят. `go test -race -count=1` по `llm/...`, `core/resilience/...`, `infrastructure/investigation/...`, `core/investigation/...`, `internal/config`, `core/services`, `internal/infrastructure`, `pkg/retry/...` падает ровно в двух тестах: `TestLLMReloadable_ZeroTimeoutAndRetriesKeepPreviousValues` (ожидаемо после F2, правка теста на write-tests) и `TestAlertProcessor_Silence_ResolvedPublished` (предсуществующий, см. ниже).

**`TestAlertProcessor_Silence_ResolvedPublished` — причина подтверждена.** Запуск на чистом `main` во временном worktree даёт тот же FAIL (expected 1, actual 0). Фикстура `silenceTestAlert` (`alert_processor_silence_test.go:87`) ставит `StartsAt = 2026-10-23T10:00Z` без `EndsAt`. Для resolved без `EndsAt` `isOldResolvedAlert` (`filter_engine.go:188`) проверяет `time.Since(StartsAt) > 48h`, и с 2026-10-25 10:00 UTC фильтр блокирует публикацию resolved. Тест — бомба замедленного действия с абсолютной датой, с этой веткой не связан (F1 не трогает переход firing→resolved). Чинить фикстуру относительным временем. Если правка не войдёт в эту задачу, нужна запись в `BUGS.md`.

### Статус находок раунда 1

| ID | Статус | Комментарий |
|---|---|---|
| F1 | closed | `updateExistingAlert` при resolved→firing берёт `new.StartsAt`. Побочные эффекты проверены: `UPDATE alerts SET starts_at` пишут и PostgreSQL, и SQLite; memory store (`IngestBatch`) получает входящий алерт и не затронут; публикация, группировка и inhibition получают корректный `StartsAt` нового инцидента — это исправление, а не регресс (раньше resolve повторного срабатывания со старым `StartsAt` > 48h мог отсекаться `isOldResolvedAlert`, а `StartsAtReasonableRule` — падать). Остаток — F12. |
| F2 | closed | `MaxRetries >= 0` применяется при reload. Тест закрепляет старое поведение и красный, ожидаемо, правка на write-tests. Отрицательное значение при reload сохраняет прежнее, при старте даёт 0 — расхождение уровня nit, не заводится. |
| F3 | partially | Ошибка `Create` теперь снимает оба claim — исправлено. Путь `Stop` закрыт только для окна backoff, основной путь не закрыт — F13. |
| F4 | open, defer-bug | Без изменений. Компромисс раскрыт в CHANGELOG и Spec Risks (F5). |
| F5 | closed | В CHANGELOG есть латентность ретраев (около 7s, CB, `llm.max_retries`) и пункт про `startsAt` после re-fire; в Spec 1.1 — Risks. |
| F6 | closed | Ключ использует `StartsAt.Unix()`. |
| F7 | closed | Статус входит в ключ. |
| F8 | closed | Claim и `Create` получают раздельные контексты по 5s. |
| F9 | closed | Логи pod сохраняют хвост 8 KiB, маркер кэпа просит сузить запрос. Loki/Prometheus 16–256 KiB по-прежнему режутся посреди JSON, но с подсказкой. Принято. Остаток — F11. |
| F10 | open, defer-bug | Без изменений, отражено в Edge Case 3. |

### F11 — `get_logs` читает только первые 256 KiB потока: при длинных строках «хвост» оказывается серединой
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/investigation/tools/kubernetes.go:184-195`
- **Issue:** `io.Copy(tb, io.LimitReader(stream, maxToolResponseBytes))` прекращает чтение после 256 KiB, и `tailBuffer` сохраняет последние 8 KiB этих 256 KiB, а не потока. При `tail_lines` 500 и структурированных JSON-логах (1–2 KiB на строку, итого 0.5–1 MiB) модель получает строки примерно из середины, а самые свежие не прочитаны вовсе. Маркер `...[N earlier bytes dropped]` при этом утверждает, что выброшено только более раннее. Это ровно тот дефект, который чинил F9, для частого формата логов.
- **Recommendation:** читать поток в `tailBuffer` целиком: память ограничена 8 KiB, объём сети ограничен `tail_lines` ≤ 500. Если нужен страховочный предел, сделать его заметно больше (например, 4 MiB) и при его срабатывании честно писать, что поток не дочитан. Тест: поток больше 256 KiB ⇒ в выводе последние байты потока.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F12 — re-fire распознаётся только по статусу `resolved` в БД; алерт, истёкший по `endsAt` без resolve-сообщения, остаётся со старым `StartsAt`
- **Severity:** minor
- **Location:** `go-app/internal/core/services/deduplication.go:373-375`
- **Issue:** условие `existing.Status == resolved && new.Status == firing` пропускает случай, когда отправитель не прислал resolve: рестарт Prometheus, отказ HA-пары, `resolve_timeout`. В БД остаётся `status=firing` с `ends_at` в прошлом. Новое срабатывание через часы приходит с новым `startsAt`, но идёт как firing→firing: `StartsAt` старый, ключ дедупа старый, расследование глушится до 24h. Upstream Alertmanager считает такой алерт resolved (`EndsAt` < now) и заменяет его новым.
- **Recommendation:** считать существующий алерт завершённым и при `existing.EndsAt != nil && existing.EndsAt.Before(now)`, то есть принять `new.StartsAt`, если `new.StartsAt` позже `existing.EndsAt`. Тест на этот переход.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F13 — прерывание на `Stop` снимает claim только в окне backoff; основной путь (LLM-вызов, agent loop) держит инцидент 24h
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/investigation/queue.go:216-221` (единственная точка `releaseClaims` на отмене), `queue.go:244-255`, `queue.go:319-342`; `go-app/internal/application/service_registry.go:2486` (`Stop(5 * time.Second)`)
- **Issue:** `Stop` ждёт 5s, а `LLMTimeout` равен 60s и agent loop идёт до 10 итераций, так что при rolling deploy job почти всегда прерывается внутри LLM-вызова или agent loop, а не в backoff. Отменённый вызов на последней попытке ведёт в `SaveError`/DLQ (ctx уже отменён, запись не пишется), в agent-режиме — в `failed`. В обоих случаях `releaseClaims` не вызывается, Redis-claim живёт 24h, статус застревает в `processing`. Кроме того, `Stop` после `cancel()` сразу возвращает управление, и shutdown может закрыть Redis-клиент раньше, чем воркер успеет выполнить `DEL`. SIGKILL/OOM без `Stop` дают тот же результат. До задачи следующий resend создал бы новое расследование, поэтому это регресс этой задачи. Утверждение автора «claims снимаются при прерывании на Stop» верно только для узкого окна.
- **Recommendation:** надёжнее не снимать claim, а не давать ему жить долго во время работы: `SETNX` с коротким «рабочим» TTL (например, `LLMTimeout × (MaxRetries+1)` + запас или фиксированные 15m), а после записи результата или ошибки (`SaveResult`/`SaveAgentResult`/`SaveError`) продлевать до 24h через `EXPIRE`. Это покрывает и Stop, и падение процесса. Минимум: на любом выходе из `processJob` при `q.ctx.Err() != nil` вызывать `releaseClaims`, а `Stop` должен дожидаться воркеров после `cancel()` хотя бы на короткий срок. Тест: отмена ctx во время `InvestigateAlert` на последней попытке и во время agent loop ⇒ ключ в miniredis удалён или живёт с коротким TTL.
- **Disposition:** fix-here
- **Follow-up:** n/a

---

## Раунд 3

**Reviewer perspective:** independent general-purpose agent (тот же ревьюер, что в раундах 1–2; код не писал)
**Reviewed at:** 2026-10-28
**Reviewed tree:** bugfix/investigation-safety-reliability @ 677169d (дифф раунда `e9991d9..677169d`)
**Verdict:** pass (see `review-verdict.json`)

Запуском проверено: `go build ./...` и `go vet ./internal/...` проходят. `go test -race -count=1` по тем же пакетам, что в раунде 2, по-прежнему падает ровно в двух тестах: `TestLLMReloadable_ZeroTimeoutAndRetriesKeepPreviousValues` (ожидаемо после F2) и `TestAlertProcessor_Silence_ResolvedPublished` (предсуществующий, абсолютная дата в фикстуре). Оба должны быть зелёными до `testing`.

### Статус находок

| ID | Статус | Комментарий |
|---|---|---|
| F1–F3, F5–F9 | closed | Без изменений с раунда 2. F3 в части `Stop` теперь закрывается через F13. |
| F4 | open, defer-bug | Follow-up в `BUGS.md` / `UPSTREAM-AI-HOOKS` на `finalize`. |
| F10 | open, defer-bug | Follow-up в `BUGS.md` / `INVESTIGATION-PROMPT-ISOLATION` на `finalize`. |
| F11 | closed | Поток читается до 4 MiB, `tailBuffer` держит последние 8 KiB; при 4 MiB честный маркер «newer lines may be missing». При `tail_lines` ≤ 500 предел 4 MiB — 8 KiB на строку, на практике недостижим; по времени чтение ограничено `PerToolTimeout` 30s. Память ограничена (8 KiB + чанк `io.Copy`). |
| F12 | closed | Условие `new` firing ∧ `new.StartsAt > existing.StartsAt` ∧ (`existing` resolved ∨ `existing.EndsAt < new.StartsAt`). Проверены сценарии. Обычный resend (тот же `StartsAt`) не меняет `StartsAt`. Задержанный resend с просроченным `EndsAt`: `StartsAt` тот же, guard срабатывает. HA-пара Prometheus с разными `ActiveAt` при живом `EndsAt` не меняет. Re-fire без resolve берёт новый `StartsAt`. Это совпадает с upstream (алерт с `EndsAt` < now считается resolved и заменяется). |
| F13 | closed | `SETNX` с `inFlightTTL` 15m, `EXPIRE` до 24h в defer после исхода, если `q.ctx` не отменён. Job, прерванный `Stop` или падением процесса, оставляет только 15m claim; локальный claim умирает вместе с процессом. Продление до 24h происходит при DLQ, permanent fail, agent без `final_answer` и при ошибке `SaveResult`/`SaveAgentResult`. Это соответствует Spec Edge Case 3 («job упал → claim держится TTL, бюджет важнее»). Джобы, вычитываемые из закрытого канала при остановке с уже отменённым ctx, получают ошибку `SETNX` (fail-open) и `Create` с отменённым ctx: в Redis ничего не остаётся. Худший путь с дефолтами укладывается в 15m: `Create` ≤ 5s, 4 × 60s LLM + backoff 5+10+20s ≈ 275s, agent loop ≤ 5m (`TotalTimeout`), итого ≈ 9.6 мин. Нестандартные настройки — F14. |

### F14 — `inFlightTTL` фиксирован, а длительность job задаётся конфигом; `EXPIRE` по истёкшему ключу молча ничего не делает
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/investigation/dedup.go:19-23,111-117`; `go-app/internal/infrastructure/investigation/queue.go:194-209`
- **Issue:** 15m покрывают дефолты (≈ 9.6 мин), но `investigation.llm_timeout`, `investigation.max_retries`, `investigation.retry_interval` настраиваются. Например, `max_retries: 5` и `llm_timeout: 120s` дают 6 × 120s + backoff 135s + agent 300s ≈ 19 мин. Кроме того, `UpdateStatus`, `SaveError`, `SaveResult` идут на `q.ctx` без таймаута (предсуществующее). Если job длится дольше 15m, claim истекает и вторая реплика берёт тот же инцидент на очередном resend. Получается дубль расследования, а не потеря, так что риск ограничен. В конце `extendShared` делает `EXPIRE`: по истёкшему ключу вызов возвращает 0 без ошибки, claim на 24h не восстанавливается, и следующие resend на других репликах снова запускают расследование. По чужому ключу (реплика B уже сделала `SETNX`) вызов продлевает claim B — это безвредно. Spec Edge Case 5 («межрепличный claim переживает рестарт») больше не уточняет, что claim прерванного job живёт лишь 15m.
- **Recommendation:** выводить `inFlightTTL` из конфига очереди (`LLMTimeout × (MaxRetries+1)` + сумма backoff + `TotalTimeout` agent loop + запас) или хотя бы брать максимум из этой оценки и 15m. Продлевать безусловным `SET key 1 EX 24h` вместо `EXPIRE`, чтобы истёкший claim восстанавливался. Обновить Edge Case 5 в Spec.
- **Disposition:** fix-here
- **Follow-up:** n/a

### Исправлено после вердикта

- **F14** — исправлено автором следом за `pass` @ `677169d`: in-flight TTL выводится из конфига очереди (`inFlightTTL(cfg)`), продление — безусловный `SET … EX 24h`, Spec Edge Case 5 обновлён. Покрывается тестами write-tests.
