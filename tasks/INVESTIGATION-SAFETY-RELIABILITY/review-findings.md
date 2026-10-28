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
