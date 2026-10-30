# Deep Review Findings: AI-классификация на upstream-ядре (срез 2a ADR-019)

**Trigger classification:** mandatory (3+ сигнала: `C X R`; дополнительно discretionary novel-pattern — первая точка расширения порта upstream)
**Reviewer perspective:** independent agent (general-purpose)
**Reviewed at:** 2026-10-30
**Reviewed tree:** feature/upstream-ai-classify @ f1c3d77
**Verdict:** fix_required (see `review-verdict.json`)

Объём: `git diff main...HEAD -- go-app config.yaml.example` (код — коммит `f1c3d77`), `requirements.md`, `research.md`, `Spec.md`, `tasks.md`. Премисы сверены с исходниками `github.com/prometheus/alertmanager@v0.34.1` из module cache. `go build ./...` и `go vet` по затронутым пакетам — зелёные. Тесты не запускались (фаза write-tests ещё впереди).

## Проверка Design Premises

| Premise | Результат |
|---|---|
| `Put` вызывает `PostStore` под `a.mtx` | Верно: `provider/mem/mem.go:302-348` (`a.mtx.Lock()` на весь цикл, `PreStore` → `Set` → `PostStore`). `gc()` (`mem.go:176-195`) зовёт `PostDelete` по каждому алерту, затем `PostGC`. Класс `code-read` заслужен. |
| `Silencer` как колбэк работает только в `PostGC` | Верно: `silence/silence.go:325-332`. `Silencer.Mutes` (`:165`) без marker в контексте побочных эффектов, кроме кеша silencer'а, не имеет — вызов из воркера безопасен. |
| `RoutingStage` — map, dispatcher принимает любую `notify.Stage` | Верно: `notify/notify.go:163-230`, `dispatch/dispatch.go:104-107`. |
| flush отдаёт копии с общими map аннотаций | Верно: `dispatch/dispatch.go:922-931` (`a := *alert`). Клон аннотаций в `annotated` обязателен и сделан. |
| nflog/dedup не зависят от аннотаций | Верно по сути, но ключ — xxhash по labels (`notify/notify.go:341-365` `hashAlert`), а не fingerprint. Следствие для «поздней» классификации см. F4. |
| `MultiStage` останавливается на пустом списке | Верно: `notify/notify.go:253-263`. |
| Классификация обычно готова до первого flush | `assumed`, класс честный. Но при дефолтном `EnableFallback` «готова» почти всегда — часто с rule-based результатом (F2). |

Конкурентность: порядок локов `a.mtx → c.mtx` (PostStore) единственный; `lookup`/`forget`/ошибка воркера берут только `c.mtx`; `Mutes` вызывается без `c.mtx`. Запись `result` до `close(done)` и чтение после `isDone` — корректный happens-before. `c.mutes` пишется в `core.New` до запуска HTTP-сервера, читается после приёма job — гонки нет. Порядок остановки в `cmd/amp-upstream/main.go` верный (defer `classifier.Stop` регистрируется раньше `core.Stop`, выполняется позже; при ошибке `core.New` воркеры тоже останавливаются). Блокеров не найдено.

## Findings

### F1 — `drop_noise` выкидывает и resolved-алерты: инциденты у получателя не закрываются
- **Severity:** major
- **Location:** `go-app/internal/upstreamai/stage.go:35-43`
- **Issue:** Стадия дропает алерт с `severity=noise` независимо от статуса. Сценарий (выведен из `dedup_stage.go:52-95`): первая отправка группы ушла до готовности классификации (таймаут бюджета) → nflog записал алерт в `FiringAlerts`, получатель (PagerDuty/Opsgenie/webhook) открыл инцидент. Затем классификация = noise → алерт исключается из всех следующих flush, включая resolved. Если в группе только он — `MultiStage` останавливается, `ReasonAllAlertsResolved` никогда не наступает; если не только он — `ReasonNewResolvedAlerts` для него тоже не наступает. Инцидент висит открытым навсегда. Тот же эффект, если запись классификации появилась после первой отправки по любой причине (очередь была полна, повтор после ошибки).
- **Recommendation:** дропать только firing-алерты (`!a.Resolved()` на копии из flush — dispatcher обнуляет `EndsAt` у firing), resolved пропускать (с аннотациями). Для алерта, о котором получатель не знал, upstream сам не отправит resolve, пока в nflog нет firing (`entry == nil` → `ReasonDoNotNotify`); при `send_resolved` в смешанной группе получатель может увидеть resolve неизвестного ему алерта — приемлемо. Добавить кейс в тест 3.1.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — rule-based fallback выдаётся за LLM-классификацию и не отличим в метриках; Edge Case 4 фактически мёртв
- **Severity:** major
- **Location:** `go-app/internal/upstreamai/classifier.go:206-221`, `go-app/cmd/amp-upstream/main.go:161-166`, `Spec.md` Edge Cases 4, Observability
- **Issue:** `services.DefaultClassificationConfig()` включает `EnableFallback: true`, а `ClassificationService.ClassifyAlert` при любой ошибке LLM (outage, открытый CB, `Retry-After` выше порога, отмена контекста при Stop) возвращает результат `FallbackEngine` с `err == nil` (`classification.go:176-262`). В итоге: (а) метрика `outcome="classified"` растёт и при полном outage провайдера — оператор не видит деградацию в метриках AI-слоя; (б) ветка `error` + удаление записи + «следующий `Put` повторит» (Edge Case 4) в `amp-upstream` недостижима; (в) уведомления получают `llm_severity`/`llm_reasoning` от правил (признак `Metadata["fallback"]` теряется). Метрики самого `ClassificationService` не подключены (`BusinessMetrics` nil).
- **Recommendation:** в `classify` различать `res.Metadata["fallback"] == true` → `outcome="fallback"` (и Warn-лог); исправить Edge Case 4 и Observability в `Spec.md` под реальное поведение. Решить явно, нужна ли пометка fallback в аннотациях (в `cmd/server` её тоже нет — можно отложить).
- **Disposition:** fix-here (метрика + правка Spec)
- **Follow-up:** n/a

### F3 — после outage долгоживущий алерт навсегда остаётся с fallback-классификацией
- **Severity:** minor
- **Location:** `go-app/internal/upstreamai/classifier.go:138-155`
- **Issue:** Запись создаётся один раз на fingerprint и живёт до GC провайдера (только после resolve). Fallback-результат, полученный во время outage, считается финальным; после восстановления LLM алерт, горящий часами/днями, не переклассифицируется. Кроме того, сервис кеширует fallback на `CacheTTL` (1h), так что даже повтор в течение часа вернул бы его же. Аналогично `skipped_silenced` (уже записано как known gap).
- **Recommendation:** зафиксировать как tech debt вместе с «переклассификацией после снятия silence» (срез 2b): fallback-запись помечать как непостоянную (TTL/повтор при следующем `Put` после восстановления CB) и не кешировать fallback в L1/L2 на полный TTL.
- **Disposition:** defer-tech-debt
- **Follow-up:** `docs/06-planning/TECH-DEBT.md` (завести на finalize)

### F4 — Edge Case 5 неверен: поздняя классификация не попадает к получателю на «следующем flush»
- **Severity:** minor
- **Location:** `Spec.md` Edge Cases 5; `go-app/internal/upstreamai/stage.go`
- **Issue:** Dedup считает только хеши labels (`hashAlert`) и наборы firing/resolved. Если первое уведомление ушло без `llm_*`, следующий flush с появившимися аннотациями подавляется `DedupStage` (`ReasonDoNotNotify`) до нового алерта в группе, resolve или `repeat_interval` (по умолчанию 4h). Поведение приемлемо, но Spec обещает другое, и метрика `timeout` — единственный сигнал.
- **Recommendation:** переписать Edge Case 5 («классификация появится в следующем уведомлении группы, которое пропустит dedup: новый алерт, resolve, repeat_interval»); при желании упомянуть в `docs/UPSTREAM_UPGRADE.md`/CHANGELOG.
- **Disposition:** fix-here (документ)
- **Follow-up:** n/a

### F5 — переполнение очереди: нет лога, метрика считает попытки, разовые отправители не классифицируются никогда
- **Severity:** minor
- **Location:** `go-app/internal/upstreamai/classifier.go:146-154`
- **Issue:** Spec Observability обещает Warn при переполнении — его нет (только счётчик). Счётчик `dropped_queue_full` инкрементируется на каждый `Put` того же алерта, пока очередь полна, — это число попыток, а не алертов. Повтор опирается на resend Prometheus; клиенты, которые шлют алерт один раз (вебхук-адаптеры, ручной `amtool`), при полной очереди классификацию не получат.
- **Recommendation:** либо добавить rate-limited Warn (например, не чаще раза в минуту с числом дропов), либо поправить Spec; в Help метрики уточнить, что это попытки. Ограничение «нужен повторный `Put`» записать в Spec Edge Case 3.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — `cmd/server`: короткий `Retry-After` удлиняет синхронный ingest; Impact Analysis говорит только «быстрее»
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/llm/client.go:228-267`, `Spec.md` Impact Analysis
- **Issue:** В `cmd/server` классификация синхронна в `POST /api/v2/alerts`. Раньше 429 ретраился с backoff 1s/2s/4s; теперь при `Retry-After` ≤ 10s (порог `RetryDelay*10` при дефолтном `RetryDelay=1s`) каждая попытка ждёт до 10s, и ingest одного алерта под 429 может дойти до `LLMTimeout` (30s) вместо ~7s + запросы. Верхняя граница прежняя, но типичная задержка под rate-limit растёт — это прямо касается `PROD-LLM-ALERT-PATH-ISOLATION`. `investigate_with_tools.go` (checker с нулевым порогом) и `callInvestigation` поведение не меняют — проверено.
- **Recommendation:** отразить в Spec Impact Analysis и CHANGELOG оба направления (длинный hint — быстрее fallback, короткий — дольше ожидание в `cmd/server`). Код менять не нужно.
- **Disposition:** fix-here (документ)
- **Follow-up:** n/a

### F7 — нет регрессионного стража для премис, на которых держится шов при бампе upstream
- **Severity:** minor
- **Location:** `go-app/internal/upstreamcore/doc.go` (AMP extensions), `tasks.md` 3.2
- **Issue:** Сборка поймает изменение интерфейса `mem.AlertStoreCallback`, но не поведенческие сдвиги: начнёт ли dedup учитывать аннотации (тогда `llm_*` дадут повторные уведомления), перестанет ли flush копировать алерт, появится ли у upstream второй колбэк помимо silencer. `upstream-bump.sh` диффит только upstream-файлы.
- **Recommendation:** в 3.2 добавить сквозные кейсы: (1) группа уведомлена без `llm_*`, затем классификация готова — повторного уведомления до `repeat_interval` нет; (2) store-алерт после стадии не содержит `llm_*`; (3) `drop_noise` + resolve доходит до webhook (F1). Упомянуть эти тесты в `doc.go` как стражи шва.
- **Disposition:** fix-here (в write-tests)
- **Follow-up:** n/a

### F8 — переполнение `time.Duration` на огромном `Retry-After`
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/llm/errors.go:44-49`
- **Issue:** `time.Duration(secs) * time.Second` при `secs` > ~9.2e9 переполняется в отрицательное значение → hint игнорируется, и вместо «не ретраить» идёт обычный backoff. Только от некорректного/враждебного провайдера.
- **Recommendation:** ограничить `secs` (например, `> 1e6` → трактовать как очень большой, вернуть `time.Duration(math.MaxInt64)` или 24h).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F9 — проверка `notify_wait` вставлена под комментарий про `redis.startup_wait`
- **Severity:** nit
- **Location:** `go-app/internal/config/config.go:1189-1192`
- **Issue:** Комментарий «startup_wait applies whether or not redis.addr is set.» теперь стоит над проверкой `llm.notify_wait`, а не над своей.
- **Recommendation:** перенести проверку `notify_wait` выше комментария (или к прочим LLM-проверкам).
- **Disposition:** fix-here
- **Follow-up:** n/a

## Skip Register

- Inhibition до классификации и ожидание в стадии для подавленных групп — заявленный known gap (Spec Open Questions, срез 2b/5), отдельной находкой не оформлялся.
- Hot reload LLM-конфига в `amp-upstream` — Non-Goal.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
