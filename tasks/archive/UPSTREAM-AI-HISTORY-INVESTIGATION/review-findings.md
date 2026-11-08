# Deep Review Findings: история и investigation на upstream-ядре (срез 2b ADR-019)

**Trigger classification:** mandatory (3 сигнала `C X R`, Full tier)
**Reviewer perspective:** independent agent (не автор; код и premises проверены по исходникам, включая alertmanager@v0.34.1 из module cache)
**Reviewed at:** 2026-11-08
**Reviewed tree:** feature/upstream-ai-history-investigation @ e9a9d11 (diff `14fc041..e9a9d11`)
**Verdict:** fix_required (предложено; `review-verdict.json` не пишется этим ревьюером)

`go build` / `go vet` для `./cmd/amp-upstream/ ./internal/upstreamai/ ./internal/upstreamcore/` — чисто.

## Premises

| Premise | Проверка | Итог |
|---|---|---|
| `PostStore` под `a.mtx`, синхронно | `provider/mem/mem.go:302-348` | верно |
| История `cmd/server` — `DeduplicationService.ProcessAlert` | `deduplication.go:203-420` | верно |
| Ключ БД `cmd/server` — `alertconv.Fingerprint` | `webhook/parser.go:123-126`, `prometheus_parser.go:277-281`, `handlers/alerts.go:538-541`: ключ клиента, если он передан | верно условно — F6 |
| FNV upstream == `UpstreamFingerprint` | тот же `model.LabelSet.Fingerprint()` | верно |
| `Submit` неблокирующий, без IO, dedup по инциденту | `queue.go:146-178` | верно, но очередь рассчитывает на повтор `Submit` на resend — F2 |
| `inhibitor` установлен до первого `Put` | `core.go` initial `ApplyConfig` до `Handler`; HB: присвоение `r` → serve → Put → канал → воркер | верно |
| Источник виден inhibitor'у к проверке | assumed, честно | ok |
| Prometheus resend | assumed, честно; но это же создаёт нагрузку F1 | ok |

Побочные эффекты `Mutes` из воркера: marker берётся только из `ctx` (`inhibit.go:188-194`, `silence.go:177-183`) — в воркере его нет; `eventRec` — zero value, `RecordEvent` no-op (`recorder.go:306-309`). Сейчас эффектов нет (см. F10).

## Findings

### F1 — Заглушённые алерты переставляются в общую очередь классификации на каждый `Put`
- **Severity:** major
- **Location:** `go-app/internal/upstreamai/classifier.go:221-226` (`forgetEntry` на mute), `:160-185` (`PostStore`)
- **Issue:** В 2a заглушённый алерт ставился в очередь один раз за жизнь записи. Теперь запись удаляется, и каждый resend каждого заглушённого алерта (раз в `resend_delay`, ~1m) кладёт job в ту же очередь (1024) с теми же 4 воркерами, что ждут LLM. При большом silence (maintenance) или inhibition-шторме очередь забивается mute-проверками за спинами LLM-вызовов → новые незаглушённые алерты получают `dropped_queue_full`, NotifyStage уходит в timeout без классификации. Регрессия против 2a именно в сценарии, ради которого делался срез.
- **Recommendation:** не удалять запись, а помечать её muted с временем проверки и переставлять в очередь на `Put` не чаще интервала (например, 1m) — или отдельная лёгкая очередь/путь для mute-перепроверок, не конкурирующий с LLM. Количественно подтвердить в testing (N заглушённых + медленный fake LLM → доля `dropped_queue_full`).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — Investigation ставится один раз за жизнь записи классификатора
- **Severity:** major
- **Location:** `go-app/internal/upstreamai/classifier.go:236-239`; ср. `internal/core/services/alert_processor.go:851-854`, `internal/infrastructure/investigation/queue.go:146-178`, `dedup.go:52`
- **Issue:** `cmd/server` вызывает `Submit` на каждый ingest, и очередь на это опирается: при переполнении она «releases the claim so a later resend may try again». В `amp-upstream` `Submit` вызывается только из `classify`, т.е. один раз на запись `entries[fp]`. Следствия: (а) дроп по `queue full` теряет investigation инцидента навсегда (до GC записи); (б) повторный firing после resolve в окне GC (`DefaultAlertGCInterval` 30m) — новый `StartsAt`, новый `dedupKey`, но запись есть → ни новой классификации, ни investigation нового инцидента. Invariant/User Story 2 («по firing-алерту запускался investigation») выполняется не для каждого инцидента.
- **Recommendation:** вызывать `Submit` на каждый firing `PostStore`, если результат записи уже готов (`Submit` без IO — допустимо под локом, `cmd/server` вызывает его в ingest-горутине), плюс из воркера при первом результате; или сбрасывать запись при resolved `Put` / смене `StartsAt`. Минимум — (б); (а) уходит вместе с повтором `Submit`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — Старт доставки ждёт PostgreSQL (Invariant 2 «ни на старте»)
- **Severity:** major
- **Location:** `go-app/cmd/amp-upstream/amp.go:115-134` (`connectDatabase`), вызов до `net.Listen`/`upstreamcore.New` в `main.go:67-75`
- **Issue:** `newAMPLayer` выполняется до создания ядра и listener'а. `pool.Connect` ограничен `connect_timeout` (30s по умолчанию; blackhole-хост — полные 30s), затем `RunMigrations` с `context.Background()`: ожидание advisory-lock до 5s×36=180s (если мигрирует `cmd/server`) и сама миграция без ограничения. Всё это время `amp-upstream` не принимает алерты. Spec Target Design 6 обещал «таймаут 30s» на подключение; Invariant 2 декларирует независимость и на старте.
- **Recommendation:** минимально — общий ограниченный ctx на connect+migrate и переформулировать Invariant 2 («старт задерживается не более чем на X»); правильнее — подключать PG в фоне после старта ядра и включать историю/investigation по готовности (это же закрывает TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT`).
- **Disposition:** fix-here (ограниченный ctx + текст инварианта); фоновое подключение — defer-tech-debt
- **Follow-up:** TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT` (расширить)

### F4 — Warn о переполнении истории спамит на пределе
- **Severity:** minor
- **Location:** `go-app/internal/upstreamai/history.go:111-121`
- **Issue:** `queueFull` один на все шарды и сбрасывается на каждом успешном enqueue. На пределе (или когда полон один шард, а остальные свободны) флаг переключается на каждом событии → Warn на каждый дроп. Классификатор именно эту проблему решает re-arm'ом на половине очереди — здесь она возвращена.
- **Recommendation:** re-arm как в `Classifier` (по заполненности шарда < половины), флаг на шард; в Warn — индекс шарда.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — Info-лог на каждую запись истории
- **Severity:** minor
- **Location:** `go-app/cmd/amp-upstream/amp.go:139-143`; `internal/core/services/deduplication.go` (`s.logger.Info("Alert processed", ...)`)
- **Issue:** `History` пишет на каждый `Put` (каждый resend каждого алерта, включая заглушённые), `ProcessAlert` логирует Info на каждый вызов; логгер `amp-upstream` — JSON на уровне Info. Строка лога на алерт в минуту.
- **Recommendation:** передать в `DeduplicationService` логгер с уровнем Warn (или `Debug` в dedup, если это не ломает `cmd/server`); зафиксировать выбор.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — Premise о ключе `cmd/server` завышен по классу
- **Severity:** minor
- **Location:** `Spec.md` § Design Premises (строка 3), Invariant 6; `internal/infrastructure/webhook/parser.go:123-126`
- **Issue:** парсеры `cmd/server` берут `fingerprint` из payload, если он есть, и только иначе — `alertconv.Fingerprint`. Webhook upstream Alertmanager несёт FNV-fingerprint, так что при приёме `cmd/server` через AM-webhook ключи строк — FNV, а не SHA-256. Равенство ключей двух бинарей верно только для отправителей без fingerprint (Prometheus `/api/v2/alerts`). Класс `call-path-traced` не учёл ветку.
- **Recommendation:** сузить premise и Invariant 6 до «для алертов без fingerprint в payload (Prometheus push)»; отметить ветку в Open Questions среза 6.
- **Disposition:** fix-here (docs)
- **Follow-up:** n/a

### F7 — Investigation на fallback-результате расходится с `cmd/server`
- **Severity:** minor
- **Location:** `go-app/internal/upstreamai/classifier.go:236-243`; ср. `alert_processor.go:836-854`
- **Issue:** `cmd/server` ставит investigation только при успешной LLM-классификации; здесь — и на fallback (LLM недоступен). Во время LLM-аварии очередь investigation наполняется заданиями, которые идут в тот же лежащий LLM, тратят `MaxRetries`, и `claimLocal` держит ключ инцидента `dedupTTL` — после восстановления LLM этот инцидент не расследуется (вместе с F2 — точно).
- **Recommendation:** не вызывать `Submit` на fallback (паритет с `cmd/server`) либо явно обосновать в Spec и сбрасывать claim при провале.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F8 — Неявный resolve (истёк `EndsAt`) не попадает в историю
- **Severity:** minor
- **Location:** `go-app/internal/upstreamai/history.go:124-128` (`PostDelete` no-op)
- **Issue:** алерт, разрешившийся по `EndsAt` без явного resolved-`Put` (отправитель умер, `resolve_timeout`), остаётся в строке `firing`. `PostDelete` вызывается из GC (`mem.go:190-193`) с полным алертом, у которого `Resolved()` истинно, — событие доступно, но выброшено. Edge Case 3 покрывает только переполнение очереди.
- **Recommendation:** в `PostDelete` ставить resolved-событие (тот же неблокирующий путь); dedup отбросит дубль, если resolved уже записан. Проверить паритет с `cmd/server` (там, похоже, тот же пробел) — тогда допустим defer.
- **Disposition:** defer-backlog (или fix-here, это 3 строки)
- **Follow-up:** BACKLOG, если defer

### F9 — `investigation.only_firing: false` молча не работает
- **Severity:** nit
- **Location:** `go-app/cmd/amp-upstream/amp.go:183`; `classifier.go:160-163`
- **Issue:** `qCfg.OnlyFiring` передаётся, но resolved-алерты не классифицируются, значит и не отправляются в `Submit`; в `cmd/server` resolve расследуется при `only_firing: false`.
- **Recommendation:** Info/Warn на старте при `only_firing=false` и строка в Non-Goals/Spec.
- **Disposition:** fix-here (лог + docs)
- **Follow-up:** n/a

### F10 — Хрупкость правки порта: параллельный срез и скрытые зависимости `mutes`
- **Severity:** nit
- **Location:** `go-app/internal/upstreamcore/core.go:233-245`, `reloader.go:89-94`, `doc.go`
- **Issue:** (а) `feature/upstream-ha-memberlist` (52cb3e0) правит те же `core.go`/`reloader.go`/`doc.go`/`main.go` — конфликт при merge неизбежен (в том числе `run()` в `main.go`). (б) Безопасность вызова `Mutes` из воркера держится на том, что `eventRec` — zero value, а marker в `ctx` отсутствует; если включить event recorder, каждая mute-проверка воркера (с F1 — на каждый resend) запишет `InhibitionMutedAlertEvent`/silence-событие. Сама правка порта минимальна (≈10 строк) и задокументирована — это ок.
- **Recommendation:** одной строкой в `doc.go`/комментарии `mutes`: «вызывается вне pipeline, ctx без marker; при включении event recorder — передавать ctx с отключённой записью»; мерж-порядок со срезом 4 согласовать заранее.
- **Disposition:** fix-here (комментарий)
- **Follow-up:** n/a

### F11 — Конвертация в `core.Alert` под локом провайдера на каждый `Put`
- **Severity:** nit
- **Location:** `go-app/internal/upstreamai/history.go:111-112`
- **Issue:** `toCoreAlert` (две map + SHA-256) выполняется под `a.mtx` для каждого события; IO нет, но это CPU под глобальным локом ingest, вдобавок к классификатору.
- **Recommendation:** класть в шард `*alert.Alert` (в store алерты заменяются, не мутируют) и конвертировать в воркере; либо оставить и отметить замером в testing.
- **Disposition:** defer-tech-debt (если замер в testing не покажет проблему — reject)
- **Follow-up:** n/a

## Проверено без замечаний

- Порядок `stop`: классификатор → очередь investigation → история → пул; `Classifier.Stop` ждёт воркеров, поэтому `Submit` после `close(q.jobs)` невозможен. `stopAMP` отложен первым — выполняется после `core.Stop`.
- Typed-nil: `investigations` присваивается только при `queue != nil`; `history`/`classifier` проверяются на nil перед `Chain`.
- `History.Stop` не закрывает каналы — поздний `PostStore` не паникует.
- Порядок событий одного fingerprint (Invariant 4): `Put` сериализован `a.mtx`, один воркер на шард.
- `PreStore` всех AMP-колбэков — `nil` (Invariant 3).

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.

## Author Response (2026-11-08)

| Finding | Итог | Что сделано |
|---|---|---|
| F1 | fixed | Пропуск по mute запоминается с `recheckAt` (`DefaultMuteRecheck` = 1m); `Put` до него не занимает слот очереди. Spec §4, Edge Case 6, метрика. |
| F2 | fixed | `Put` классифицированного алерта снова вызывает `Submit` (dedup очереди — одно investigation на инцидент, новый `StartsAt` — новое). |
| F3 | fixed (частично) + TECH-DEBT | Миграции под контекстом 1m (advisory lock включён); connect ограничен `database.connect_timeout` (Background нужен health-checker'у). Invariant 2 переписан честно. Переподключение — TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT`. |
| F4 | fixed | Warn истории перевзводится, только когда шард опустел до половины. |
| F5 | fixed | Dedup-сервис получает обёртку `warnOnly` над основным handler'ом. |
| F6 | fixed | Premise в Spec: SHA-256 совпадает для Prometheus push; webhook-парсеры берут fingerprint из payload; класс `code-read`. |
| F7 | rejected | Premise неверен: `cmd/server` передаёт в `AlertProcessor` как `LLMClient` `r.classificationSvc` (`service_registry.go:2410`), который на недоступном LLM отвечает fallback'ом без ошибки (`fallback.go:72,115`), и `Submit` вызывается на fallback тоже (`alert_processor.go:851`). Поведение `amp-upstream` — паритет. |
| F8 | fixed | `History.PostDelete` ставит событие для `a.Resolved()`. |
| F9 | fixed | Info на старте при `only_firing: false`; Edge Case 8. |
| F10 | fixed | Комментарий в `Classifier.classify`: статус алерта меняется только через marker из ctx, у воркера его нет; кеш silencer'а под своим локом. Конфликт с `feature/upstream-ha-memberlist` — в отчёте эпику. |
| F11 | deferred | TECH-DEBT, замер — в testing. |

## Re-review (2026-11-08, 2aac410)

**Reviewer perspective:** independent agent, повторный проход по `git diff b5caaac..2aac410` и коду (alertmanager@v0.34.1 из module cache).

**Проверки:** `go build ./...` — ok; `go vet ./internal/upstreamai/ ./internal/upstreamcore/ ./cmd/amp-upstream/` — ok; `go test -race ./internal/upstreamai/ ./internal/upstreamcore/` — 24 passed, 1 failed: `TestSilencedAlertIsSkipped` (ждёт старый outcome `skipped_silenced`, чинится на write-tests; гонок race detector не нашёл).

### Диспозиции первого прохода

| Finding | Итог | Комментарий |
|---|---|---|
| F1 | confirmed fixed (с оговоркой N1) | `recheckAt` пишется в `classify` под `c.mtx` до `close(done)` (defer), читается в `PostStore` под `c.mtx` — гонки нет. Окно «`recheckAt` выставлен, `done` ещё открыт»: `PostStore` возвращается (`now < recheckAt`), а при истёкшем `recheckAt` замена записи безопасна — `done` старой закроет defer. Замена после recheck при полной очереди не происходит, запись с прошедшим `recheckAt` остаётся, следующий `Put` повторяет. NotifyStage для muted-записи: `isDone` → `result == nil` → алерт без аннотаций, без ожидания; для свежей записи после recheck — ждёт, как для новой. `PostDelete`/`PostGC` удаляют запись независимо от состояния. |
| F2 | confirmed fixed | `Submit` неблокирующий и без IO (`queue.go:146-178`: `claimLocal` под своим `d.mu`, `select` с `default`), допустим под `a.mtx`+`c.mtx`. `e.result` читается после `isDone` — happens-before через `close(done)`. Отброшенное по `queue full` повторяется (claim освобождён), новый `StartsAt` — новый `dedupKey`. Оговорки — N2 (shutdown) и N5 (результат прошлого инцидента). |
| F3 | confirmed fixed (частично, как заявлено) | Connect ограничен `connect_timeout` (`pool.go` `buildPool`: `WithTimeout(ctx, cfg.ConnectTimeout)`), `Background` нужен health-checker'у — верно. Миграции под `WithTimeout(1m)`; `RunMigrations` передаёт ctx в `provider.Up`, go-retry advisory lock его уважает. Invariant 2 переписан честно. Текст ошибки — N4. |
| F4 | partially fixed (nit) | На пределе всех шардов спам устранён. Флаг по-прежнему один на все шарды: при одном полном шарде событие в другой (< половины) перевзводит Warn, и следующий дроп в полном снова логирует. Рекомендация «флаг на шард» не применена; сценарий «один горячий шард» маловероятен (шарды по хешу, PG общий) — допустимо оставить. |
| F5 | confirmed fixed | `warnOnly`: `Enabled` = `l >= Warn && inner.Enabled`; `Handle` унаследован (Logger вызывает его только после `Enabled`); `WithAttrs`/`WithGroup` оборачивают результат — фильтр сохраняется на `logger.With(...)`. Корректно. |
| F6 | confirmed fixed | Premise сужен до Prometheus push, ветка webhook описана, класс `code-read`. |
| F7 | rejection confirmed | `service_registry.go:2410`: `LLMClient: r.classificationSvc`; `classification.go:228-242` при ошибке LLM и `EnableFallback` отдаёт результат `RuleBasedFallback` без ошибки (`fallback.go`, `Metadata["fallback"]=true`); `processEnriched` (`alert_processor.go:836-854`) зовёт `Submit` на любой результат без ошибки. Паритет есть, premise F7 был неверен. |
| F8 | confirmed fixed (с оговоркой N3) | `PostDelete` вызывается из `gc()` **вне** `a.mtx` (`mem.go:176-195`: лок только в `gcAlerts`), но `enqueue` неблокирующий — Invariant 1 соблюдён. `a.Resolved()` в GC всегда истинно — условие лишнее, безвредно. |
| F9 | confirmed fixed | Info на старте при `only_firing=false`, Edge Case 8. |
| F10 | confirmed fixed | Комментарий в `classify` по существу; конфликт со срезом 4 вынесен в отчёт эпику. |
| F11 | deferred — ok | Замер в testing. С F2 под локом добавился `toCoreAlert` на каждый `Put` классифицированного алерта (плюс тот, что в истории) — включить в тот же замер. |

### Новые findings

**N1 — minor. Перепроверка mute ≈ resend снижает нагрузку F1 лишь ~вдвое.**
`DefaultMuteRecheck` = 1m, Prometheus `resend_delay` = 1m: `recheckAt = Put₀ + d + 60s` (d — задержка в очереди), поэтому `Put₀+60s` пропускается, `Put₀+120s` снова ставит job. Заглушённый алерт по-прежнему занимает слот общей очереди (4 воркера, ждущих LLM) раз в ~2 resend'а вместо одного раза за жизнь записи в 2a. При тысячах заглушённых (maintenance) `dropped_queue_full` для новых алертов остаётся возможным. Рекомендация: в testing количественно проверить (N заглушённых + медленный fake LLM, как требовал F1); если доля дропов заметна — увеличить default (например 5m, ценой задержки классификации после снятия silence) или проверять mute без очереди. Не блокирует: значение 1m совпадает с рекомендацией первого прохода.

**N2 — minor. `Submit` из `PostStore` нарушает порядок остановки «классификатор — единственный отправитель».**
`amp.go:73-75` («Stopped after the classifier, the queue's only sender») и Spec Edge Case 9 («новых `Submit` нет») больше неверны: `Submit` теперь зовётся из `PostStore` на ingest-горутине. Безопасность держится только на том, что `core.Stop` (а до него `server.Shutdown`) завершает все `Put` до `stopAMP`. На ветке `server.Shutdown` timeout → `server.Close()` (`main.go`) handler с незавершённым `Put` может дойти до `Submit` после `queue.Stop` → `close(q.jobs)` → panic «send on closed channel». Окно узкое (процесс и так завершается), но исправление дешёвое: в `Classifier.Stop` после `wg.Wait()` под `c.mtx` обнулить `c.inv` (или флаг stopped, проверяемый в `PostStore`), и поправить комментарий и Edge Case 9.

**N3 — nit. `PostDelete` вне лока провайдера может переставить события одного fingerprint (Invariant 4).**
`gc()` удаляет алерт под `a.mtx`, отпускает лок и затем вызывает `PostDelete`. Если между ними пришёл `Put` нового firing того же fingerprint, в шард сначала попадёт firing нового инцидента, затем resolved старого; `updateExistingAlert` (`deduplication.go:368-385`) выставит `status=resolved`, `EndsAt` старого. Самоисправляется следующим resend'ом firing. Достаточно оговорки в Invariant 4 / Edge Cases.

**N4 — nit. Сообщение об ошибке миграций при дедлайне 1m говорит о бюджете lock'а 3m.**
`RunMigrations` на `ctx.Err() != nil` возвращает «timed out waiting for migration lock … after 3m0s», хотя оборвал внешний ctx через 1m (и, возможно, во время самой миграции, а не ожидания lock'а). В логе `amp-upstream` это вводит в заблуждение; поправить можно в `amp.go` (обернуть ошибку с `migrationTimeout`), не трогая `cmd/server`.

**N5 — nit (предсуществующее с 2a). Повторный firing до GC использует классификацию прошлого инцидента.**
Resolved-`Put` запись классификатора не сбрасывает, поэтому новый инцидент с тем же fingerprint (новый `StartsAt`, до GC ~30m) получает в NotifyStage аннотации прошлого, а новое investigation — его `ClassificationResult`. Для того же labelset это обычно приемлемо; стоит одной строкой в Edge Cases.

### Итог

Открытых blocker/major нет. N1 — обязательный замер в testing; N2 — желательно закрыть до finalize (2 строки + комментарий/Spec).

**Verdict: pass**

## Author Response to Re-review

| Finding | Итог | Что сделано |
|---|---|---|
| N1 | deferred to testing | Замер `skipped_muted`/`dropped_queue_full` при resend — в testing; при проблеме поднять default. |
| N2 | fixed | `Classifier.Stop` ставит `stopped` под `mtx`; `PostStore` после него не вызывает `Submit`. Комментарий в `amp.go` обновлён. |
| N3 | accepted | Resolved-событие из GC против гонки со свежим `Put` — следующий resend чинит строку. |
| N4 | accepted | Текст ошибки миграций; не влияет на поведение. |
| N5 | defer-backlog | Поведение 2a (re-fire до GC берёт классификацию прошлого инцидента) — в BACKLOG. |
