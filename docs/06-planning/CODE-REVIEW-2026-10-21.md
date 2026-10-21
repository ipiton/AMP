# Code Review 2026-10-21 — remediation roadmap

> Срез: `main` @ `68d749f`. Объём: `go-app` — 431 prod-файл / ~128k строк, тесты ~123k строк.
> Метод: шесть параллельных read-only ревью по областям (publishing; grouping/inhibition/silencing; config/validators; HTTP/application; routing/templates/LLM; platform/storage/CI), ключевые находки перепроверены чтением кода. Каждая находка сверена с `BUGS.md`, `TECH-DEBT.md`, `BACKLOG.md`: поле **tracked** указывает slug, если запись уже есть.
> Предыдущий срез — аудит 2026-10-06 (`fa682d4`), см. § «Анализ диффа с прошлым аудитом».
> Документ — вход для backlog: находки отсюда переносятся в `BUGS.md` (нарушение контракта) или `TECH-DEBT.md` (дорогое сопровождение) по правилам `artifact-contract.md`, после чего этот файл можно архивировать.

Обозначения: `hypothesis` — вывод не подтверждён до конца (нужна проверка тестом/на стенде). Пути — от `go-app/`.

---

## 1. Executive summary

**Общая оценка: функционально зрелое ядро в оболочке из мёртвого и дублирующего кода.** Живой путь (ingest → dedup → grouping → notify chain → publishing) проработан глубоко: fail-safe подтверждение доставки, защита от двойных срабатываний таймеров, атомарный swap дерева маршрутов, параметризованный SQL, образцовый CI (actions по SHA, минимальные permissions, govulncheck). Тестов почти столько же, сколько кода.

Главные проблемы — в трёх плоскостях:

1. **Семантика Alertmanager нарушена в нескольких ключевых местах**, которые тесты закрепляют как норму: источник inhibition выпадает из кеша через 5 минут горения; алерты под silence не сохраняются (нет `suppressed`); `repeat_interval: 1d` не парсится; валидация silences расходится с upstream; resolve в PagerDuty/Rootly теряется после рестарта или на другой реплике.
2. **≈25k строк мёртвого кода (~20% prod) слинковано в бинарь или лежит рядом** (`cmd/server/handlers`, `internal/realtime`, `internal/ui`, `pkg/templatevalidator`, три пакета шаблонов, DLQ, enhanced webhook stack, `pkg/aiclassifier`, `pkg/telemetry`, половина `internal/config`). Это удваивает поверхность ревью и `govulncheck`, создаёт ложные гарантии («audit log», «signature verification», «YAML bomb protection») и провоцирует случайное «оживление» небезопасного кода (WebSocket без auth, health без проверок).
3. **Вредное дублирование одного решения в N местах**: 5 реализаций семантики label-matcher, 3 парсера matcher-грамматики, 3 копии валидации silences (с одним и тем же багом), 3 модели Alertmanager-конфига и 5 типов inhibit-правила, 3 типа `Duration`, 3 системы метрик, 5 способов ретраев, 3 circuit breaker'а. Комментарии в коде сами фиксируют, что одно и то же расхождение исправлялось 3–5 раз.

Дополнительно: ресурсы без верхней границы (alert store, silence store, inhibition state, client maps), reload без сериализации, отсутствие HTTP-метрик и access-лога, prompt injection в LLM-пути.

**Вердикт:** как и в аудите 2026-10-06 — **pilot-ready, не production-ready**. Блокеры прод-готовности из этого ревью — раздел 9.

Статистика находок (после дедупликации между областями):

| Severity | Кол-во |
|---|---|
| critical | 2 |
| high | 11 |
| medium | 29 |
| low | 29 |
| **итого** | **71**, из них ~60 новых (не было в трекере даже частично) |

---

## 2. Critical issues

### C-1 · Источник inhibition выпадает из кеша через 5 минут горения
- **severity:** critical · **effort:** medium · **tracked:** new · **kind:** bug
- **location:** `internal/infrastructure/inhibition/cache.go:426-445` (`cleanup`), `:233-262` (`GetFiringAlerts`); `internal/core/services/alert_processor.go:537-541`
- **issue:**
  - (a) `cleanup` вытесняет из L1 по `StartsAt + ttl(5m)` — любой источник, горящий дольше 5 минут, удаляется каждую минуту (проверено: `cache.go:439`).
  - (b) точный дубликат (`ProcessActionIgnored` — resend без изменения `EndsAt`, типично для webhook-источников без `endsAt`) делает `return nil` **до** `AddFiringAlert` ⇒ источник не возвращается в кеш никогда, Redis-ключ (TTL 5m) тоже истекает.
  - (c) `GetFiringAlerts` идёт в Redis только при **пустом** L1 ⇒ в HA источники с другой реплики невидимы, пока локальный L1 не пуст.
- **impact:** долгоживущий источник перестаёт подавлять цели: для не-Prometheus источников навсегда, для Prometheus — «дырами» до минуты. Утечка и на ingest, и в `filterInhibited` на отправке. Это прямое нарушение базовой функции Alertmanager.
- **recommendation:** вытеснять только по `EndsAt` (с `resolve_timeout`); обновлять кеш на каждом ingest, включая ignored-дубликаты (перенести Step 0.5 выше раннего `return`); убрать shortcut «L1 не пуст ⇒ без Redis» или брать источники из активного набора `memory.AlertStore` как единого источника правды. Тест: источник горит 10 минут с resend каждые 30s ⇒ цель подавлена всё время; две реплики.

### C-2 · Resolve в PagerDuty/Rootly теряется после рестарта, на другой реплике и через 24h
- **severity:** critical · **effort:** low (PD) / medium (Rootly) · **tracked:** new · **kind:** bug
- **location:** `internal/infrastructure/publishing/pagerduty_publisher_enhanced.go:127-138` (`resolveEvent`), `:89,107`; `rootly_publisher_enhanced.go:196-202`; `publisher.go:383-388`
- **issue:** trigger шлёт `DedupKey: alert.Fingerprint`, а resolve берёт ключ только из in-memory `EventKeyCache(24h)` и при промахе возвращает `ErrEventNotTracked` (проверено). У Rootly промах ⇒ молчаливый `nil`.
- **impact:** после каждого rollout/рестарта, в HA (resolve пришёл на другую реплику) и для инцидентов старше 24h инциденты не закрываются автоматически. Дежурные получают «вечные» инциденты — регрессия относительно upstream Alertmanager, где dedup-ключ детерминирован.
- **recommendation:** PD — в resolve использовать `DedupKey: alert.Fingerprint` (ключ детерминирован, кеш для PD не нужен). Rootly — детерминированный внешний ключ (lookup по label/title) или персистентное хранение (Redis, как nflog). Тест: resolve без предварительного trigger в том же процессе.

---

## 3. High / Medium / Low issues

### 3.1 High

**H-1 · Firing-алерт под активным silence отбрасывается до ingest** — `internal/application/handlers/alerts.go:431-436` (проверено)
- **issue:** фильтр `silences.HasActiveMatch` в хендлере выкидывает алерт до `ProcessAlert`/`IngestBatch`; контрактный тест `cmd/server/main_phase0_contract_test.go:1453` закрепляет это как норму.
- **impact:** алерт не виден в `GET /api/v2/alerts` со `status.state=suppressed` (karma, `amtool alert query -s`); не становится источником inhibition (в upstream становится); нет истории.
- **recommendation:** убрать фильтр из хендлера — подавление уже делает notify chain (`filterSilenced`, `grouping/manager_impl.go:1215`); переписать тест на «алерт есть, `suppressed`».
- **effort:** medium · **tracked:** new · **kind:** bug (compat)

**H-2 · Длительности `d`/`w`/`y` не парсятся** — `internal/infrastructure/routing/global.go:486`, `internal/infrastructure/grouping/config.go:125` (проверено)
- **issue:** три типа `Duration` на `time.ParseDuration`; upstream использует `model.Duration` (`1d`, `1w`).
- **impact:** немодифицированный upstream-конфиг с `repeat_interval: 1d` не загружается — нарушение главного обещания «drop-in замена».
- **recommendation:** один тип `Duration` на `model.ParseDuration` из `prometheus/common` (уже прямая зависимость); тест на `1d`, `1w`, `90s`.
- **effort:** low · **tracked:** new · **kind:** bug (compat)

**H-3 · Email fallback рендерит HTML через `text/template`** — `internal/infrastructure/publishing/email_publisher_enhanced.go:10,159-163,315-325`; шаблоны `internal/notification/template/defaults/email.go:236-258` (проверено)
- **issue:** без экранирования: `{{ .Annotations.description }}` вставляется как есть, `href="{{ .CommonAnnotations.runbook_url }}"` пропускает `javascript:` и фишинговые URL. Путь активен при `publishing.templates.enabled=false`, у email-target без шаблонных полей и при ошибке `RenderEmailContent`.
- **impact:** тот, кто может прислать алерт, внедряет произвольный HTML/ссылки в письмо дежурному.
- **recommendation:** убрать путь, всегда рендерить через `business/templating` (`html/template`, `email.default.html`); минимум — `html/template` для HTML-части.
- **effort:** low · **tracked:** new · **kind:** security

**H-4 · Кеши корреляции и SMTP-клиенты общие между таргетами** — `publisher.go:383-388`, `slack_publisher_enhanced.go:56,131`, `rootly_publisher_enhanced.go:71,117`, `publisher.go:856-869`
- **issue:** Slack/Rootly/PD-кеши ключуются только fingerprint; SMTP-клиент — только `host:port`, хотя внутри зашиты user/password/TLS/From.
- **impact:** второй Slack-таргет отвечает в тред чужого канала; второй Rootly-аккаунт обновляет чужой инцидент; после reload с новым SMTP-паролем или `require_tls` работает старый клиент; два таргета на одном SMTP-хосте шлют с кредами первого.
- **recommendation:** ключ кеша `target.Name|fingerprint`; ключ SMTP-клиента `host|port|user|sha256(pass)|tls`; тест на два таргета одного типа.
- **effort:** low · **tracked:** new · **kind:** bug/security

**H-5 · Reload не сериализован** — `internal/application/service_registry.go:2654` (`ReloadConfig`), `internal/config/reload_coordinator.go:163`, `internal/config/config.go:666`
- **issue:** SIGHUP, `POST /-/reload` и sidecar могут запустить конвейер параллельно; внутри — глобальный viper (не goroutine-safe), `r.config = newConfig` без синхронизации; распределённый lock в `atomicApply` недостижим (`lockManager == nil`, `service_registry.go:337`).
- **impact:** два reload применяются вперемешку, откат возвращает не тот снимок; `hypothesis`: `concurrent map writes` в viper.
- **recommendation:** `reloadMu sync.Mutex` в `ServiceRegistry.ReloadConfig`; для HTTP — `TryLock` ⇒ `409`; тест двух параллельных reload под `-race`.
- **effort:** low · **tracked:** new · **kind:** bug

**H-6 · Hot reload на lite+production всегда падает** — `internal/config/update_validator.go:589-596`, `config.go:1147`
- **issue:** `DefaultConfigValidator` работает только на reload, правила расходятся с `Config.Validate` (старт). Lite + `app.environment=production` (дефолт чарта) без `DATABASE_PASSWORD`: старт проходит, каждый reload — «database password should not be empty».
- **impact:** на lite-prod hot reload не работает никогда; sidecar в бесконечном backoff.
- **recommendation:** один валидатор на оба пути; проверки БД гейтить `RequiresPostgres()`.
- **effort:** low · **tracked:** new · **kind:** bug

**H-7 · Ретраи LLM: 429/5xx не ретраятся, `max_retries: 0` → 3** — `internal/infrastructure/llm/errors.go:26`, `pkg/httperror/error.go:445`, `internal/core/resilience/resilience.go:55-57`
- **issue:** `llm.HTTPError` не распознаётся `IsRetryable` ⇒ 429/503 падают сразу, ретраятся только таймауты (худший случай 30s × 4 = 120s внутри `POST /api/v2/alerts`); `MaxRetries==0` подменяется на 3.
- **impact:** максимальная латентность ingest при минимальной надёжности классификации.
- **recommendation:** `errors.As(err, &he) && (he.StatusCode==429 || he.StatusCode>=500)`; различать «не задано» и `0`; тест на httptest 503→200.
- **effort:** low · **tracked:** частично `PROD-LLM-ALERT-PATH-ISOLATION` (бюджет), дефект классификатора — new · **kind:** bug

**H-8 · Расследования без дедупа, синхронный INSERT в ingest-пути** — `internal/core/services/alert_processor.go:787-788`, `internal/infrastructure/investigation/queue.go:122-160`
- **issue:** `Submit` делает `repo.Create` в горутине запроса без таймаута; дедупа нет, а смена `EndsAt` на каждом resend считается апдейтом.
- **impact:** новое LLM-расследование на каждый resend (~1/мин на горящий алерт) — неограниченная стоимость LLM; медленная БД задерживает ingest.
- **recommendation:** `Submit` — только неблокирующая отправка в канал, `Create` в воркере; дедуп по `fingerprint+StartsAt` с TTL (Redis `SETNX`).
- **effort:** medium · **tracked:** дедуп — `PHASE-5A-INVESTIGATION-PIPELINE`; синхронный INSERT — new · **kind:** bug/perf

**H-9 · Prompt injection и неограниченный доступ investigation tools** — `internal/infrastructure/llm/investigate_with_tools.go:165`, `internal/infrastructure/investigation/tools/kubernetes.go:165`, `llm/client.go:385-394`
- **issue:** JSON алерта (недоверенные labels/annotations) кладётся в роль `system`; k8s tool читает логи/поды/события в любом namespace по выбору LLM; логи уходят внешнему провайдеру и в БД; инъекция в аннотацию может выставить `severity=noise` ⇒ `filter_engine` дропнет алерт.
- **impact:** атакующий с правом слать алерты управляет агентом и вытягивает логи чужих namespace во внешний LLM.
- **recommendation:** данные алерта — в `user`-сообщении в явном разделителе; `investigation.tools.kubernetes.allowed_namespaces`, дефолт — namespace из лейбла алерта; редактирование секретов в выводе tools.
- **effort:** medium · **tracked:** дроп по LLM — `PROD-LLM-ALERT-PATH-ISOLATION`, RBAC — `INVESTIGATION-K8S-TOOL-HELM`; injection/allowlist — new · **kind:** security

**H-10 · `continue: true` не даёт fan-out** — `internal/business/routing/evaluator.go` (`Evaluate` берёт `matchResult.First()`)
- **effort:** medium · **tracked:** `ROUTE-CONTINUE-NO-FANOUT` · **kind:** bug (compat). Подтверждено: матчер `continue` обрабатывает верно, теряется выше по пайплайну.

**H-11 · SQL `PostgresSilenceRepository` не покрыт ни одним тестом на реальной БД** — `internal/infrastructure/silencing/postgres_silence_repository_test.go` (30 безусловных `t.Skip`), `bulk_ops_test.go` (7), `ttl_test.go` (6)
- **impact:** регрессии в хранении silences в HA ловятся только в e2e или в проде.
- **recommendation:** testcontainers-сьют по образцу `repository/postgres_history_test.go` (CRUD, фильтры, TTL, bulk); безусловные `t.Skip` удалить.
- **effort:** medium · **tracked:** new · **kind:** test

### 3.2 Medium

| ID | location | issue → impact | recommendation | effort | tracked |
|---|---|---|---|---|---|
| M-1 | `grouping/manager_impl.go:298-305`, `redis_group_storage.go:204` | `Load→mutate→Store` при конкурентном апдейте даёт `ErrVersionMismatch`, ошибка только логируется, возвращается незаписанный `Clone()`; `Version++` до EXEC → молчаливая потеря алерта/resolve в группе | retry с re-`Load`, локальная сериализация по ключу через `groupPublishLocks`, `Version++` на копии после EXEC | medium | частично `REDIS-GROUP-STORAGE-UNLOCKED-WRITES`, `GROUPING-NEW-GROUP-CREATE-RACE` |
| M-2 | `application/handlers/silence_convert.go:79`, `storage/memory/silence_store.go:457`, `core/silencing/validator.go:76` | три копии валидации требуют непустое значение **каждого** matcher'а → отклоняются валидные `env=""`, принимается match-all `alertname=~".*"` | один валидатор: «хотя бы один matcher не матчит пустую строку», как upstream | low | new |
| M-3 | `storage/memory/alert_store.go:113-193` | `AlertStore.all` никогда не чистится → рост памяти, `List`/`Stats` и снапшота с аптаймом | периодический GC resolved с `EndsAt < now - retention` | low | new (смежно `ALERT-STORE-DEDUP-KEY-STARTSAT`) |
| M-4 | `storage/memory/silence_store.go:287-303,365-385`, `core/silencing/matcher_cache.go` | истёкшие silences не удаляются; `HasActiveMatch` — линейный проход с аллокацией matcher'ов на каждый silence; глобальный `RegexCache` полностью сбрасывается на 1000 записях под write-lock | компилировать matcher'ы при сохранении; retention GC (upstream 120h) | medium | new |
| M-5 | `inhibition/state_manager.go:120-260`, `alert_processor.go:600,830-850` | state удаляется только на resolve источника полным сканом; `ExpiresAt` всегда nil; `StartCleanupWorker` не вызывается; `RecordInhibition` на каждом resend (Redis SET 24h + O(n) count) → неограниченный рост | удалять на resolve цели, `ExpiresAt = source.EndsAt`, обратный индекс, gauge через GaugeFunc | low-medium | new |
| M-6 | `inhibition/matcher_impl.go:73-169`, `grouping/manager_impl.go:1166-1201` | O(R·F) с копией всего L1 на каждый `ShouldInhibit`, вызывается для каждого алерта группы; совпадения на INFO | индекс источников по правилу при `AddFiringAlert`, один снапшот на `filterInhibited`, логи → DEBUG | medium | new |
| M-7 | `publishing/queue.go:570-600,764,815-850` | `Stop` закрывает три канала: воркер выходит по закрытому high, бросая medium/low; `submitJob` после `Stop` и повторный `Stop` — panic | atomic `closing` + отказ в submit, drain всех каналов, `close` под `sync.Once` | medium | new |
| M-8 | `slack_client.go:217-311`, `telegram_client.go:319-418`, `pagerduty_client.go:379-472`, `rootly_client.go:303-364`, `webhook_client.go:142-267`, `queue.go:1401` | вложенные ретраи (до 16 HTTP на job), `time.Sleep` без ctx, `defer Body.Close()` в цикле, неограниченный `Retry-After` → воркер блокирован, abandon открывает breaker здоровому таргету | один уровень ретраев (очередь), backoff через `select` на ctx, cap `Retry-After` 30s | medium | new |
| M-9 | `publisher.go:118-130,176-178,801-803`, `business/publishing/discovery_impl.go:479-483` | `*url.Error` с токеном в URL (Teams/Discord/Telegram fallback) и полное тело ответа уходят в лог/ошибку | маскировать URL (`webhook_auth.go:153`), тело ≤512 байт | low | new |
| M-10 | `io.ReadAll` без лимита: `publisher.go:125,182`, `slack_client.go:257`, `telegram_client.go:296,355`, `pagerduty_client.go:529`, `rootly_client.go:380`, `webhook_client.go:198`, `llm/client.go:282`, `investigation/tools/*` | гигантский ответ таргета/tool раздувает память | общий `readLimited(r, 64<<10)`; clamp `tail_lines`/`limit` tools | low | частично `PHASE-5A-INVESTIGATION-PIPELINE` |
| M-11 | `publishing/circuit_breaker.go:69-84,107` | после timeout `CanAttempt` пускает весь поток, состояние не переходит в HalfOpen | переход Open→HalfOpen с одним пробным слотом | low | new |
| M-12 | `business/publishing/health_checker.go:107-120` | HTTP GET на `smtp://` → email-таргет навсегда `unhealthy`; `hypothesis`: GET на Slack/PD тоже не 2xx | пропускать email или TCP-probe для API-типов | low | new |
| M-13 | `application/publishing_runtime.go:210`, `refresh_manager_impl.go:121` | метрики refresh регистрируются во временном реестре, который никто не скрейпит | `v2.Global().Publishing` | low | new |
| M-14 | `cmd/server/main.go:136-155` | нет access-лога, request-id, HTTP-метрик (готовые `pkg/logger.LoggingMiddleware` и `pkg/metrics` не подключены) → не видно латентности и 5xx ingest | `promhttp.InstrumentHandlerDuration` со статической меткой handler + access-лог | low-medium | new |
| M-15 | `application/handlers/alerts.go:76,309,425`, `silences.go:95-144`, `status.go:51` | ошибки v2 как `{"error":…}`, upstream swagger — JSON-строка; `hypothesis`: amtool не покажет причину | `writeV2Error` с `json.Marshal(msg)` | low | new |
| M-16 | `application/handlers/matchers.go:28` vs `business/routing/tree_builder.go:237,411` vs `pkg/configvalidator/matcher/matcher.go:256` | три грамматики matcher'ов: `?filter=alertname=Foo` → 400, тот же matcher в `route.matchers` работает | один парсер (см. R-2) | medium | new |
| M-17 | `application/handlers/alerts.go:482-503`, `webhook/validator.go:186-196,554` | Prometheus-парсер требует `severity ∈ {…}`; при ошибке молча fallback на legacy, который проверяет только `alertname` → валидация зависит от severity, 400 без причины | выбор парсера по формату; один валидатор по смыслу upstream `PostableAlert.Validate` | medium | new |
| M-18 | `cmd/server/templates/legacy/shared.html:9,16-22`, `dashboard-overview.html:49-52` | абсолютные `href="/static/..."` при `route_prefix` → дашборд без CSS, навигация 404 | `{{.RoutePrefix}}` в ссылках + проверка в тесте | low | new |
| M-19 | `internal/config/update_reloader.go:262`, `handlers/status_api.go:105` | reload/rollback на `r.Context()`: обрыв клиента посреди Phase 5 → незавершённый откат (W610) | `context.WithoutCancel` + свой таймаут, как в `watchReloadSignal` | low | new |
| M-20 | `internal/config/alertmanager_validation.go:66-71,93-95`, `tree_builder.go:274-278` | fail-open: ошибка unmarshal → вся проверка configvalidator пропускается; tree builder молча выкидывает битый matcher → маршрут шире (особенно для `!=`) | возвращать ошибку; builder вызывает `matcher.Parse` и падает | low | new |
| M-21 | `internal/alertmanager/config/config.go`, `infrastructure/routing/{config,receiver,global}.go`, `internal/config/config.go:193` | схема Alertmanager описана трижды, inhibit-правило — пятью типами; уже даёт баги (`PAGERDUTY-SERVICE-KEY-ONLY-REJECTED`, E024 telegram, W300 проверяет недоставляемые интеграции) | минимум — reflection-тест паритета yaml-тегов; цель — валидатор по `routing.RouteConfig` | medium | частично `PAGERDUTY-SERVICE-KEY-ONLY-REJECTED`, `CONFIG-GLOBAL-VIPER-STATE` |
| M-22 | `internal/config/config.go:671-672,978-982` | `AutomaticEnv` не видит ключи без default/`BindEnv`: `database.url`, `telemetry.insecure`, `llm.agent_mode`, `inhibition.config_file`, `publishing.templates.render_timeout`… → env молча игнорируется | `BindStruct`/reflection-bind всех путей + тест набора ключей | low | new (корень — `CONFIG-GLOBAL-VIPER-STATE`) |
| M-23 | `internal/config/config.go:271-530`, `cmd/server/main.go:149-155` | ключи парсятся/валидируются, но не читаются: `server.*_timeout` (захардкожено), `webhook.signature/rate_limiting/cors/max_*`, `app.max_workers`, `metrics.port/path`, `cache.*`, `lock.*`, `retry.*`, `telemetry.*`, `http_client.*` → ложное ощущение настройки; хуже всего `webhook.signature.enabled` | удалить или подключить; минимум WARN «not enforced» | medium | частично `DEAD-WEBHOOK-SECURITY-CONFIG` |
| M-24 | `internal/application/service_registry.go` (2763 строк), `grouping/manager_impl.go` (2175, `publishGroupAlerts` — 438 строк) | god-objects: wiring всех подсистем; grouping-пакет содержит DSL конфига, keygen, два storage, таймеры, nflog и весь notify chain; ~44% строк — комментарии с историей fix-round'ов | см. L-T этапа 7 | high | частично `AMP-CORE-IMPORTS-INFRASTRUCTURE-NO-LAYER-GATE` |
| M-25 | `business/routing/tree_builder.go:180` → `infrastructure/grouping.Route`; `business/silencing` → `infrastructure/silencing` | инверсия слоёв: business зависит от infrastructure | перенести `Route`/`Duration` и интерфейс репозитория в core/business | medium | частично (то же) |
| M-26 | `pkg/metrics/metrics.go`, `pkg/metrics/v2/registry.go:82-121`, `internal/database/postgres/{metrics,prometheus}.go` | три системы метрик; v2 Database/Cache-сеттеры не вызываются → `/metrics` отдаёт вечные нули; v1 и v2 регистрируют одинаковые `alert_history_database_*` → panic при создании v1 | перенести Business/Filter/Webhook в v2, удалить мёртвые группы, пул — `GaugeFunc` от `pgxpool.Stat()` | medium | new |
| M-27 | `internal/infrastructure/sqlite_adapter.go:57-85`, `service_registry.go:1148-1154` | `PRAGMA foreign_keys` через пул действует на одно соединение; `MaxOpenConns` из настроек Postgres; нет `busy_timeout` → недетерминированный CASCADE, `SQLITE_BUSY` в lite | pragma в DSN (`_pragma=foreign_keys(1)&busy_timeout(5000)`), `SetMaxOpenConns(1)` для писателя | low | new |
| M-28 | `go-app/Makefile:21,80-87` | `make quality-gates` гоняет только `./cmd/server/... ./internal/ui` без lint/race, `go fmt` переписывает файлы → ложно-зелёный гейт | `quality-gates` → `../scripts/release-gate.sh`; `gofmt -l` с ненулевым exit | low | new |
| M-29 | `go.mod:38-40` | `k8s.io/client-go v0.29.0` (2023, вне поддержки); go.mod не tidy | поднять client-go отдельным PR; `go mod tidy -diff` в гейт | medium | частично `GO-MOD-TIDY-CHECK` |

### 3.3 Low

| ID | location | issue | recommendation | effort | tracked |
|---|---|---|---|---|---|
| L-1 | `publishing/email_client.go:282` | `Subject` в ASCII не проходит `sanitizeHeaderValue` (проверено) → `hypothesis`: header injection через `alertname` с CR/LF | `mime2047Subject(sanitizeHeaderValue(msg.Subject))` | low | new |
| L-2 | `internal/config/config.go:1468` | ошибка «weak database password detected: '%s'» печатает пароль (из словаря слабых) | убрать значение из текста | low | new |
| L-3 | `cmd/server/main.go:149-155,166-180,194-197` | нет `ReadHeaderTimeout`/`MaxHeaderBytes`; `signal.Notify(SIGHUP)` после `Initialize` — ранний SIGHUP убивает процесс; ошибка bind → `os.Exit(1)` без `Shutdown` | `ReadHeaderTimeout: 10s`; Notify до Initialize; `net.Listen` до Initialize | low | new |
| L-4 | `application/webauth.go:155-156` | все bcrypt-сравнения под одним мьютексом, неверные пароли кешируются → DoS легитимных клиентов без кредов | семафор `GOMAXPROCS`, отдельный лимит на отказы | low | new |
| L-5 | `application/handlers/alerts.go:57` | `external_url` захватывается при сборке роутера, reload его не обновляет | читать `registry.Config()` на запрос или W6xx | low | new |
| L-6 | `alerts.go:68`, `silences.go:46,73`, `status_api.go:101`, … vs `method_guard.go` | два способа проверки метода, 405 без `Allow`; любая ошибка `ReadAll` → 413 | `allowMethods(...)`; `errors.As(*http.MaxBytesError)` | low | new |
| L-7 | `llm/client.go:521`, `investigate_with_tools.go` | расследование игнорирует `provider` и circuit breaker; 4 копии HTTP-обвязки | один `doChat` с лимитом тела и CB | low | new |
| L-8 | `llm/client.go:310-317,440,584` | тело ответа LLM целиком в ERROR-лог и в БД (`SaveError`) | `truncate(body, 512)` | low | new |
| L-9 | `investigation/tools/database.go`, `service_registry.go:2226` | `database_query` ходит в БД самого AMP → неверные выводы по алертам на чужие БД, утечка `pg_stat_activity` AMP провайдеру | отдельный DSN, без него tool не регистрировать | low | new |
| L-10 | `business/templating/template.go:380-420` | Clone+Parse на каждое поле каждого алерта | LRU `expression → *Template`, сброс на reload | medium | new |
| L-11 | `business/routing/matcher_cache.go:128-140` | write-lock на каждом hit LRU; гонка в статистике hits | прекомпиляция regex при build дерева, убрать LRU | low | new |
| L-12 | `grouping/redis_group_storage.go:222,385,419-590`; CAS Lua ×4 | ZSET-индекс групп не чистит призраков (`Size()` врёт, `LoadAll` ошибки); CAS-скрипт скопирован 4 раза, шлётся текстом; ключи без префикса `amp:` | `ZRem` на not-found; общий `redis.NewScript`; префикс в миграции | low | new |
| L-13 | `grouping/redis_notify_log.go:283-330` | 4 последовательных round trip на таргет без атомарности | `TxPipelined` | low | new |
| L-14 | `lock/distributed.go:86-125` | `Acquire` = 4 SETNX со `time.Sleep` без ctx; `defer cancel()` в цикле | одна попытка, ctx-aware sleep | low | new |
| L-15 | `cluster/heartbeat.go:174,244` | `registered` остаётся true после неудачных refresh; `Peers` — SCAN всего keyspace на каждый status | ZSET peers со score=expiry | low | new |
| L-16 | `publishing/queue.go:815-850` | вложенные `select` с `time.After(100ms)`: high-задание ждёт до 100ms; таймер на каждой итерации | неблокирующие проверки по приоритету + один блокирующий select | low | new |
| L-17 | `publisher.go:342-361`, `queue.go:1171-1206` | client maps и `circuitBreakers` не чистятся → рост на каждом reload | GC по живому набору в `applyConfigTargets` | medium | смежно `FU-HTTP-FILE-ROTATION` |
| L-18 | `mode_manager.go:281`, `service_registry_reload.go:184` | reload с первыми таргетами — до 5s задания отбрасываются как metrics-only | `CheckModeTransition()` после `applyConfigTargets` | low | new |
| L-19 | `publishing/queue.go:802` | воркер без `recover`; непроверенные type assertions (`rootly_publisher_enhanced.go:89-91`) → паника роняет процесс | `recover` в `processJob` | low | new |
| L-20 | `publisher.go:592-606` vs `pagerduty_publisher_enhanced.go:215-231` | разный приоритет источников ключа PD | одна `credentialFromHeaders` | low | new |
| L-21 | `slack_client.go:89`, `telegram_client.go:200`, `pagerduty_client.go:172`, `rootly_client.go:104` | свои `http.Transport` без `Proxy` — поведение прокси зависит от типа таргета | явная политика, одинаково для всех | low | new |
| L-22 | `infrastructure/routing/file_secrets.go:207` | `*_file`-секреты относительно CWD (а `http_config/*_file` — от каталога конфига), без лимита размера | `rebasePath` + `io.LimitReader(64KiB)` | low | смежно `FILE-SECRET-READ-ERROR-HINT` |
| L-23 | `cmd/config-reloader/hash.go:17` | sidecar хеширует только основной конфиг: смена шаблонов, `*_file`, web config не триггерит reload | `--watch-file` (повторяемый) | medium | new |
| L-24 | `reload_coordinator.go:641-655` vs `update_diff.go:295-322` | две карты «секция → компонент», `affected_components` пуст для правок маршрутов | одна функция маппинга | low | расширение `INHIBIT-CONFIG-LOUDNESS` |
| L-25 | `pkg/configvalidator/validators/security.go:73-84` | W300 не проверяет секреты 5 доставляемых интеграций | добавить `routing_key`, slack `api_url`, `bot_token`, basic_auth, bearer | low | new |
| L-26 | `cache/redis.go:367-378`, `:59` | `Flush()` = `FlushAll` (все DB инстанса); ctx игнорируется | `FlushDB` или убрать из интерфейса | low | new / `AMP-CACHE-NO-RECONNECT` |
| L-27 | `k8s/client.go:150-158,225-240` | `Health` не применяет таймаут; `ListSecrets` молча режет на 1000; гонка `Close` vs читатели | RESTClient с ctx; пагинация `Continue` | low | new |
| L-28 | `helm/amp/values.yaml:51`, `templates/serviceaccount.yaml:13` | нет `seccompProfile: RuntimeDefault` у AMP; `automountServiceAccountToken: true` даже без RBAC | seccomp; automount только при `rbac.create && publishing.enabled` | low | new (digest-пины — `CI-SUPPLY-CHAIN`) |
| L-29 | `internal/database/migrations.go:108-140,190-215` | `RunMigrationsDown(steps)` передаёт steps как целевую версию (`steps=1` откатит всё, вызовов нет); миграции ищутся в FS по CWD/`runtime.Caller` | удалить Down; `//go:embed *.sql` в `goose.NewProvider` | low | new |

---

## 4. Duplication report

### 4.1 Вредное дублирование (одно решение, меняется целиком) — объединять

| # | Что | Где (копии) | Подтверждённый вред | Цель |
|---|---|---|---|---|
| D-1 | Семантика label-matcher (match, anchoring `^(?:…)$`, absent label) | `core/silencing/matcher_impl.go:242`, `inhibition/matchers_list.go:111` + `matcher_impl.go:309-365`, `business/routing/matcher.go:240-283`, `application/handlers/matchers.go`, `pkg/configvalidator/matcher` | комментарии «third copy», «FIFTH copy» — расхождение чинилось 3–5 раз | `pkg/labelmatch` + общий conformance-тест |
| D-2 | Грамматика matcher-выражений | `business/routing/tree_builder.go:237,336,411`, `pkg/configvalidator/matcher/matcher.go:116,168,256`, `application/handlers/matchers.go:28`, заглушка `cmd/server/handlers/stubs.go` | API требует кавычки, routing — нет (M-16); finding I-4 `summary="a!=b"` | один `matcher.Parse` |
| D-3 | Валидация silence | `silence_convert.go:79`, `memory/silence_store.go:457`, `core/silencing/validator.go:76` | одинаковый баг во всех трёх (M-2) | один валидатор в `core/silencing` |
| D-4 | Решение «алерт под silence» | хендлер ingest + notify chain | копия в хендлере ошибочна (H-1) | только notify chain |
| D-5 | Модель Alertmanager-конфига | `internal/alertmanager/config`, `infrastructure/routing`, `internal/config` (+ 5 типов inhibit-правила) | `PAGERDUTY-SERVICE-KEY-ONLY-REJECTED`, E024 | валидатор по рантайм-модели; parity-тест |
| D-6 | `Duration` | `routing.Duration`, `grouping.Duration`, `time.Duration` в `amcfg` | `1d` не парсится (H-2) | один тип на `model.ParseDuration` |
| D-7 | Валидаторы `Config` | `Config.Validate` (старт) vs `DefaultConfigValidator` (reload) | H-6 | один валидатор |
| D-8 | Retry/backoff | `pkg/retry`, `pkg/httperror/retry.go` (мёртв), `postgres/retry.go` (мёртв), ручные циклы в 5 publishing-клиентах, `queue_retry.go` | вложенные ретраи ×16 (M-8) | `pkg/retry` на уровне очереди |
| D-9 | Чтение тела ответа и маскирование URL | 8+ `io.ReadAll`, маскер только в Slack/Telegram | M-9, M-10 | `readLimited` + `redactURL` |
| D-10 | Метрики | `pkg/metrics` v1, `pkg/metrics/v2`, `postgres.PoolMetrics` | вечные нули, скрытый panic (M-26) | только v2 |
| D-11 | Кеш silences | `memory.SilenceStore` (живой) + `business/silencing` cache + sync worker (мёртвые) | дрейф состояний, `SILENCE-SYNC-LIMIT-EXCEEDS-FILTER` | только `SilenceStore` |
| D-12 | ZSET-индекс в Redis | `redis_group_storage.go` vs `redis_timer_storage.go` — разная очистка | призраки в `group:index` (L-12) | общий helper |
| D-13 | `GroupStorage` memory vs Redis | разная семантика конкуренции (last-writer-wins vs optimistic version) | M-1 | одинаковый контракт + общий contract-тест |
| D-14 | Карта «секция → компонент» | coordinator vs comparator | L-24 | одна функция |
| D-15 | Извлечение кредов из headers | `publisher.go:525,592-606,729`, `pagerduty_publisher_enhanced.go:215-231` | разный приоритет (L-20) | `credentialFromHeaders` |

### 4.2 Дешёвые механические дубли

- CAS-release Lua ×4 (`lock/distributed.go:139,180`, `redis_timer_storage.go:540`, `redis_notify_log.go:605`) → `var casDelete = redis.NewScript(...)`.
- `isValidLabelName` ×6 (core/silencing, grouping, inhibition, configvalidator ×2, publishing) → `pkg/labelmatch`.
- `RegexCache` ×2 (`core/silencing`, `business/routing`), `TemplateCache` ×2 — уходят вместе с D-1 и удалением мёртвых шаблонов.
- get-or-create с double-checked locking для 5 client maps → generic `cachedClient[T]` (только после H-4, чтобы ключи стали корректными).
- Проверка HTTP-метода inline vs `method_guard.go` (L-6).
- Пары интерфейсов infra/business: `TargetDiscoveryManager`, `HealthMonitor`, `RefreshManager` (+ мост `DiscoveryAdapter`); три `CircuitBreaker` (publishing, `core/resilience`, `llm`).

### 4.3 Допустимое повторение — оставить

- Payload-билдеры и разбор ошибок по провайдерам (`parseSlackError`, Rootly JSON:API, PD Events v2) — разные API, разные причины изменения.
- Rate-лимиты по провайдерам — у каждого свои квоты.
- Lifecycle-семантика по провайдерам (Slack thread, PD dedup, Rootly incident) — но состояние должно быть per-target (H-4) и не терять resolve (C-2).
- Пара memory/Redis `TimerStorage` — test/lite double за одним контрактом.
- memory/Redis nflog с fallback-обёрткой — осознанная деградация (`NOTIFY-LOG-FALLBACK-LIMITS`).
- `cluster` heartbeat рядом с `lock` — разная семантика.
- `routing` (модель конфига) vs `business/routing` (дерево и матчинг) — разделение ответственности корректное, двух деревьев нет.

### 4.4 Мёртвый код (кандидаты на удаление)

Оценка ≥25k prod-строк (`hypothesis` по точной цифре — пересчитать `deadcode ./...` с build-тегами `futureparity,integration`). Проверено grep'ом и `go list -deps ./cmd/...`.

| Блок | Prod LOC (≈) | В бинаре | tracked |
|---|---|---|---|
| `cmd/server/handlers` (+ `stubs.go`, WS/SSE), `internal/realtime`, `internal/ui`, `pkg/middleware`, `application.go`/`middleware.go`/`handler_registry.go`, мёртвые `templates/*` и `static/js/*` в embed | ~10k | да (через `HandlerRegistry`) | частично `DEAD-APPLICATION-MIDDLEWARE`, `SILENCES-CHECK-404` |
| `internal/infrastructure/webhook/{handler,validation,parser}.go`, `NewWebhookDetector`, `EnhancedValidator` | ~1.5k | частично | new |
| `pkg/templatevalidator` (+ подпакеты) | 4.6k | нет | new |
| `internal/business/template`, `internal/infrastructure/template`, `internal/notification/template` (кроме `defaults/email.go`), миграция таблиц `templates`, прямая зависимость sprig | ~4.5k | частично | new |
| `pkg/aiclassifier` | 549 | нет | new |
| `pkg/telemetry` | 322 | нет | `DEAD-PKG-TELEMETRY` |
| `internal/config`: `update_service.go`, `update_storage.go`, `service.go`, `sanitizer.go`, `example.go`, `LoadConfigFromEnv`, ветки lock/backup/audit в `atomicApply`; `pkg/configvalidator/{parser,result,options}` | ~3k | частично | new |
| publishing: enhanced webhook stack, DLQ (`queue_dlq.go`), `middleware.go`, `middleware_cache.go`, `tracing.go`, `lru_cache.go`, `validator.go`, `metrics.go`, второй `refresh.go`, `trends_detector.go`, `timeseries_storage.go`, `stats_collector_parallel.go`, `refresh_test_utils.go` (импорт `testing` в prod) | ~7.3k | частично | частично `FU-UNWIRED-PUBLISH-SURFACES` |
| `business/silencing` cache + sync worker + `IsAlertSilenced` | ~1.5k | да | частично `SILENCE-SYNC-LIMIT-EXCEEDS-FILTER` |
| `internal/database/postgres`: `retry.go`, `prometheus.go`, `example.go`, `errors.go`, `CircuitBreakerHealthChecker`, `LoadFromEnv`, `ConnectionString`, `PeriodicHealthChecker` (с гонкой); `RunMigrationsDown`, `GetMigrationStatus` | ~0.6k | частично | частично `DB-CONN-STRING-UNESCAPED` |
| `lock/example.go`, `LockManager`, `grouping/parser.go`, `ValidateConfig`/`SanitizeConfig`, `fingerprintIndex`; `core.Cache`; `internal/storage` (placeholder); `k8s.GetSecret`; `httperror.RetryStrategy`; группы v1-метрик | ~1k | частично | new |
| Таблицы БД без читателей/писателей: `filter_rules` (с seed), `alert_classifications`, `alert_publishing_history`, `system_metrics`, `migration_history`, `config_versions`/`audit_log`/`backups`/`locks` | — | — | new; текст `HISTORY-API` в BACKLOG неверен |

---

## 5. Refactoring recommendations

**R-1. Удаление мёртвого кода — первым, отдельными `refactor:`-PR без изменения поведения.** Сокращает объём для всех последующих шагов (например, D-1 из пяти копий становится трёх). Порядок: пакеты без импортёров (`pkg/templatevalidator`, `pkg/aiclassifier`, `pkg/telemetry`, `pkg/middleware`, `internal/business/template`) → мёртвая HTTP-поверхность (`HandlerRegistry` и всё, что тянет) → мёртвые файлы внутри живых пакетов → `go mod tidy`. Перед удалением `publishing/middleware.go` и `webhook_auth.go` вынести используемые символы (`ValidationError`, `maskURL`). Критерий каждого PR: `go list -deps ./cmd/...` уменьшился, release-gate зелёный, `govulncheck` не хуже.

**R-2. `pkg/labelmatch` — единая семантика matcher'ов.**

```go
package labelmatch

type Type int // Equal, NotEqual, Regexp, NotRegexp

type Matcher struct {
    Name  string
    Type  Type
    Value string
    re    *regexp.Regexp // anchored ^(?:Value)$, compiled once
}

func New(t Type, name, value string) (*Matcher, error)
func Parse(expr string) (*Matcher, error)      // upstream grammar, quoted and unquoted
func (m *Matcher) Matches(v string) bool        // absent label == ""
func MatchAll(ms []*Matcher, labels map[string]string) bool
func IsValidLabelName(s string) bool
```

Переводить потребителей по одному: configvalidator (сделать его `Parse` фасадом) → routing tree builder (с ошибкой вместо пропуска — закрывает M-20) → inhibition → silencing (компиляция при сохранении — закрывает часть M-4) → API `filter=`. Общий table-test на conformance с upstream — первым коммитом.

**R-3. Один слой ретраев в publishing.** Клиенты делают одну попытку и возвращают классифицированную ошибку (`retryable`, `Retry-After`); ретраит только очередь через `pkg/retry` с ctx-aware backoff. Уходят 5 ручных циклов, `time.Sleep` без ctx и вложенность ×16.

**R-4. Единый валидатор и модель конфига.** Валидатор работает по распарсенной рантайм-модели (`routing.RouteConfig`), `amcfg` остаётся только для warning'ов о недоставляемых интеграциях. Один `Duration` (`model.ParseDuration`). Один валидатор `Config` на старт и reload. Свой экземпляр viper на загрузку (`CONFIG-GLOBAL-VIPER-STATE`) + bind всех ключей.

**R-5. Метрики — только v2.** Перенести Business/Filter/Webhook, удалить мёртвые группы v1, пул БД — через `GaugeFunc`, не регистрировать неподключённые группы. Добавить HTTP-метрики на уровне регистрации маршрута:

```go
func instrument(name string, h http.Handler) http.Handler {
    return promhttp.InstrumentHandlerDuration(
        httpDuration.MustCurryWith(prometheus.Labels{"handler": name}), h)
}
mux.Handle("/api/v2/alerts", instrument("/api/v2/alerts", handlers.AlertsHandler(reg)))
```

**R-6. Notify chain — отдельный тип.** Вынести `filterInhibited`/`filterSilenced`/`isTimeMuted`/`publishGroupAlerts` (438 строк) из `DefaultGroupManager` в `notify.Pipeline` со стадиями, как upstream `notify.Stage`. `DefaultGroupManager` остаётся владельцем групп и таймеров. Делать после R-2 и C-1/M-6 — меньше движущихся частей.

**R-7. Разрезать `ServiceRegistry`.** 2763 строки wiring'а: выделить `initializeX` в per-subsystem builder'ы (publishing уже вынесен в `publishing_runtime.go` — продолжить паттерн для grouping, silencing, inhibition, investigation). Reload — отдельный тип с собственным `reloadMu` (H-5).

**R-8. Границы слоёв.** `Route`/`Duration` → `core/routing`; интерфейс `SilenceRepository` → `core`; depguard-правило в `.golangci.yml` (business/core не импортируют infrastructure). Сейчас линтеры — только `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`; добавить `depguard`, `bodyclose`, `gosec` (G112 и др.), `noctx`.

**R-9. История fix-round'ов — из комментариев в ADR.** 81 ссылка «fix round / review finding / task rec» в grouping; плотность комментариев ~44%. Оставлять в коде «почему», переносить «как мы к этому пришли» в `DECISIONS.md`/task-архив. Делать попутно при R-6, не отдельной задачей.

---

## 6. Quick wins (≤0.25d каждый, высокий эффект)

| # | Что | Находка | Эффект |
|---|---|---|---|
| QW-1 | PD resolve по `alert.Fingerprint` | C-2 (PD-часть) | инциденты PD закрываются после рестарта и в HA |
| QW-2 | `html/template` для email fallback (или удалить путь) | H-3 | закрыта HTML-инъекция |
| QW-3 | `sanitizeHeaderValue` для Subject | L-1 | закрыта header injection |
| QW-4 | Ключи кешей `target|fingerprint`, SMTP-ключ с кредами | H-4 | нет ответов в чужие треды/инциденты, reload SMTP работает |
| QW-5 | `reloadMu` в `ReloadConfig` + `WithoutCancel` для HTTP reload/rollback | H-5, M-19 | нет split-state при параллельном/оборванном reload |
| QW-6 | `model.ParseDuration` в двух `UnmarshalYAML` | H-2 | upstream-конфиги с `1d` грузятся |
| QW-7 | LLM `IsRetryable` для `llm.HTTPError`, различать `max_retries: 0` | H-7 | предсказуемая латентность ingest |
| QW-8 | Вытеснение inhibition L1 только по `EndsAt` | C-1 (a) | закрыта основная «дыра» inhibition (части b, c — в этапе 2) |
| QW-9 | Убрать пароль из текста ошибки | L-2 | нет пароля в логах |
| QW-10 | `ReadHeaderTimeout`, Notify до Initialize | L-3 | slowloris, ранний SIGHUP |
| QW-11 | `recover` в `processJob`; `sync.Once` на `close` в `Stop` | L-19, M-7 (часть) | паника паблишера не роняет процесс |
| QW-12 | Метрики refresh в глобальный реестр | M-13 | видимость refresh |
| QW-13 | `make quality-gates` → release-gate | M-28 | глобальное правило «`make quality-gates`» перестаёт давать ложно-зелёный |
| QW-14 | `seccompProfile`, условный automount | L-28 | hardening чарта |
| QW-15 | Удалить пакеты без импортёров (`pkg/templatevalidator`, `pkg/aiclassifier`, `pkg/telemetry`, `pkg/middleware`, `internal/business/template`, `internal/storage`) | 4.4 | −~7k строк без риска |

---

## 7. Longer-term improvements

- **Единый источник правды для активных алертов.** Сейчас активный набор живёт в `memory.AlertStore`, inhibition L1/L2, state manager inhibition, группах. C-1, M-3, M-5 — следствия расхождения. Цель: inhibition и API читают из одного индекса с GC; Redis — репликация, а не второй источник.
- **INGEST-WITHOUT-SYNC-PG и асинхронный LLM** (`PROD-LLM-ALERT-PATH-ISOLATION`): ingest не зависит синхронно ни от Postgres, ни от LLM, ни от investigation INSERT (H-8).
- **Notify pipeline по образцу upstream** (R-6) — стадии с явным контрактом, отдельный contract-тест.
- **Персистентное состояние корреляции провайдеров** (Rootly incident ID, Slack thread TS) в Redis рядом с nflog — для HA и рестартов.
- **Hot reload БД/Redis** фактически не существует (`SharePool`/`ShareClient` всегда выданы наружу — `FU-DB-LIVE-POOL-HANDLE`, `FU-REDIS-LIVE-CLIENT-HANDLE`): либо честно объявить restart-required, либо перейти на live handle.
- **Схема БД:** миграция, удаляющая таблицы-сироты; SQLite-схема под версионирование (сейчас DDL захардкожен в `sqlite_adapter.go:179-268`).
- **client-go** на поддерживаемую minor; регулярный bump зависимостей с карантином 7 дней.
- **Integration-тесты под build-tag** `integration` с отдельным шагом гейта; замена 157 `time.Sleep` в тестах на `require.Eventually`/fake clock — корень flaky-класса в `BUGS.md`.

---

## 8. Testing recommendations (в порядке приоритета)

1. **Inhibition long-firing** (C-1): источник горит 10+ минут с resend (Prometheus-стиль с новым `endsAt` и webhook-стиль без `endsAt`) ⇒ цель подавлена всё время; вариант с двумя репликами и общим Redis.
2. **Provider resolve без локального состояния** (C-2): resolve в новом процессе / на другой реплике / через >24h ⇒ PD получает `resolve` с тем же `dedup_key`.
3. **Два таргета одного типа** (H-4): Slack ×2 разные каналы, Rootly ×2, SMTP ×2 на одном хосте с разными кредами; reload SMTP-пароля.
4. **Silenced ingest** (H-1): POST под активным silence ⇒ `GET /api/v2/alerts?silenced=true` содержит алерт со `state=suppressed`; заглушенный алерт остаётся источником inhibition. Переписать `main_phase0_contract_test.go:1453`.
5. **Conformance matcher'ов** (R-2): единая таблица upstream-кейсов (кавычки, escape, UTF-8, пустое значение, `!~`) для всех потребителей.
6. **Silence validation parity** (M-2): `env=""`, `env!=""`, match-all `=~".*"` — ответы как у upstream.
7. **Upstream config corpus**: набор реальных `alertmanager.yml` (с `1d`, `continue: true`, `matchers:` без кавычек, top-level `inhibit_rules`) — загрузка без ошибок и ожидаемое дерево (H-2, H-10, M-20).
8. **Concurrent reload** под `-race` (H-5) и reload с оборванным клиентом (M-19).
9. **`PostgresSilenceRepository` на testcontainers** (H-11): CRUD, фильтры, TTL, bulk.
10. **Publishing queue**: `Stop` с заданиями во всех приоритетах ⇒ все завершены или явно abandoned; `Submit` после `Stop` ⇒ ошибка, не panic (M-7); 503→200 в LLM-клиенте (H-7).
11. **Resource bounds**: alert store, silence store, inhibition state не растут на долгом прогоне с ротацией алертов (M-3, M-4, M-5) — benchmark/soak с проверкой размера.
12. **Email rendering**: аннотация с `<script>`/`javascript:` в fallback-пути экранирована (H-3); alertname с `\r\n` не создаёт заголовок (L-1).

---

## 9. Final verdict — что мешает считать код production-ready

Блокеры из этого ревью (в дополнение к открытому P0 `PROD-RELEASE-V010` и P1 из `BACKLOG.md`):

1. **C-1** — inhibition не держит долгоживущие источники: базовая функция Alertmanager работает неверно.
2. **C-2** — инциденты PagerDuty/Rootly не закрываются после рестарта и в HA.
3. **H-1, H-2, H-10** — расхождения с upstream, ломающие обещание drop-in замены (silenced-алерты, `1d`, `continue`).
4. **H-3, H-9** — инъекции: HTML в email, prompt injection с чтением логов чужих namespace.
5. **H-5** — reload без сериализации при штатных трёх источниках reload (SIGHUP, HTTP, sidecar).
6. **M-3, M-4, M-5** — неограниченный рост памяти в долгоживущем процессе.
7. **M-14** — нет HTTP-метрик и access-лога: инциденты на ingest нельзя диагностировать.

Не блокеры, но риск сопровождения: ≥25k строк мёртвого кода и 15 зон вредного дублирования — каждое следующее исправление семантики придётся делать в N местах.

---

## 10. Анализ диффа с прошлым аудитом (2026-10-06, `fa682d4` → `68d749f`)

Отдельного документа-плана по code review раньше не было: аудит 2026-10-06 был ops/deploy-срезом (release-gate 11/11, `govulncheck`, рендер чарта, k3s), его находки разнесены в `BACKLOG.md` § «Production Readiness». Этот документ — первый сквозной code-level review; сравнение — по состоянию кода и трекера.

**Изменения кода между срезами:** 269 коммитов; в `go-app`, `helm`, `scripts`, `.github` — 149 файлов, +8992 / −2139. Основная масса — grouping (`manager_impl.go`, `timer_manager_impl.go`, nflog fallback), config (top-level `inhibit_rules`, `*_file`-секреты, загрузка без файла), routing (наследование таймингов), helm (внешняя БД, single-node defaults, Valkey, extraVolumes), investigation runbooks (PHASE-6B).

**Закрыто после аудита 2026-10-06** (по `BACKLOG.md`/`DONE.md`): `PROD-CONFIG-FILE-FALLBACK`, `PROD-DEPS-OTEL-145`, `GROUPING-TIMER-LOCK-FIX`, `PROD-GROUPING-DEFAULT`, `FU-TOPLEVEL-INHIBIT-RULES`, `PROD-HARDCODED-FILTER`, `DEAD-VALUES-FILTERS`, `HELM-DEFAULTS-VALIDATE`, `PROD-POSTGRES-HA-DECISION`, `PROD-DEPS-NET-HTTP2`, `HELM-EXTERNAL-DATABASE`, `PG-CONFIG-FIX`, `HELM-SINGLE-NODE-DEFAULTS`, `AMP-CACHE-NOAUTH`, `SLACK-WEBHOOK-PLAIN-OK-RESPONSE`, `ROUTE-TIMINGS-BUNDLE`, `HELM-EXTRA-VOLUMES`.

**Подтверждено этим ревью (уже в трекере, не переоткрываю):** `ROUTE-CONTINUE-NO-FANOUT`, `PROD-LLM-ALERT-PATH-ISOLATION`, `DEAD-PKG-TELEMETRY`, `DEAD-APPLICATION-MIDDLEWARE`, `DEAD-WEBHOOK-SECURITY-CONFIG`, `GO-MOD-TIDY-CHECK`, `CONFIG-GLOBAL-VIPER-STATE`, `CONFIG-VALIDATION-ERROR-REDACTION` (`receiver.go:124-332` по-прежнему печатает URL), `CONFIG-PATH-RESOLUTION-DUP` (найден третий источник — `ReloadCoordinator.configPath`), `DB-CONN-STRING-UNESCAPED`, `INHIBIT-RELOAD-ROLLBACK-KEEPS-RULES`, `AMP-CACHE-NO-RECONNECT` (+ ctx игнорируется), `TIMER-ACTIVE-GAUGE-DRIFT` (уточнён путь), `GROUP-CLEANUP-NOT-WIRED`, `REDIS-GROUP-STORAGE-UNLOCKED-WRITES`, `GROUPING-NEW-GROUP-CREATE-RACE` (уточнён), `SILENCES-CHECK-404` (причина — мёртвый хендлер), `SILENCE-SYNC-LIMIT-EXCEEDS-FILTER` (sync worker мёртв), `PAGERDUTY-SERVICE-KEY-ONLY-REJECTED` (корень — D-5).

**Уточнения к существующим записям:**
- `HISTORY-API` (BACKLOG): утверждение «история уже лежит в Postgres … `alert_classifications`, `alert_publishing_history`» неверно — в эти таблицы никто не пишет.
- `DEAD-APPLICATION-MIDDLEWARE`: реальный объём мёртвой HTTP-поверхности ~10k строк (`cmd/server/handlers`, `internal/realtime`, `internal/ui`, `pkg/middleware`), и она слинкована в бинарь через `HandlerRegistry` — расширить запись.

**Новое относительно прошлого среза:** ~60 из 71 находки — new. Связь с изменениями после 2026-10-06:
- M-1 (lost update группы в Redis) — в коде, активно менявшемся в grouping-волне; новые тесты (`notify_per_target_test.go`, `timer_fire_dedup_test.go`) покрывают таймеры и dedup, но не конкурентный `AddAlertToGroup`.
- H-8, H-9 частично касаются нового runbook-кода PHASE-6B (prompt собирается из недоверенных данных).
- M-20 (fail-open tree builder) и L-24 связаны с `ROUTE-TIMINGS-BUNDLE`/`FU-TOPLEVEL-INHIBIT-RULES`, но не внесены ими — существовали раньше.
- Остальные находки существовали до `fa682d4` и не были в фокусе ops-аудита.

**Вердикт не изменился:** pilot-ready, не production-ready. Прогресс по P0/P1 из прошлого аудита значительный (16 закрытий за 15 дней), но code-level блокеры (раздел 9) — новый пласт, в прошлом плане не учтённый.

---

## 11. Remediation roadmap

Принципы:
- **Refactor отдельно от behavior.** Удаление мёртвого кода и консолидация — `refactor:`-PR без изменения поведения; багфиксы — `fix:`-PR со своим тестом. Не смешивать: иначе ревью и bisect теряют смысл.
- **Сначала безопасность и семантика, потом чистка, потом архитектура.** Но удаление мёртвых пакетов без импортёров можно делать параллельно с самого начала — оно ни с чем не конфликтует.
- **Тест до фикса** для всех C/H-находок (red → green), это же закрывает `hypothesis`.
- Tier по Solo Kanban: security/семантика Alertmanager — Full (с `deep-review`); refactor без поведения — Standard; механика — Lightweight.

### Этап 0 — Triage (0.25d, Lightweight)
- Завести записи в `BUGS.md` (C-*, H-*, bug-находки M/L) и `TECH-DEBT.md` (duplication/dead-code/design); бандлы — по подсистемам (см. ниже). Обновить `HISTORY-API`, `DEAD-APPLICATION-MIDDLEWARE` по § 10.
- Пополнить `NEXT.md` § Queue по правилу баланса.
- **Результат:** backlog отражает ревью, этот документ можно архивировать.
- **Статус (2026-10-21): выполнен**, кроме пополнения `NEXT.md` Queue. Порядок взятия — `REVIEW-PIPELINE-2026-10-21.md` (49 позиций, critical → low). Slug-кандидаты ниже частично переименованы при заведении:

| Кандидат в этом документе | Фактическая запись |
|---|---|
| `QUEUE-PANIC-SAFETY` | `QUEUE-GRACEFUL-STOP` (B, вместе с M-7) |
| `DEAD-HTTP-SURFACE` | `DEAD-APPLICATION-MIDDLEWARE` (BL, расширен) |
| `PUBLISH-CLIENT-GC` | `PUBLISH-RELOAD-CLIENT-LIFECYCLE` (B) |
| `CLUSTER-PEERS-ZSET` | `CLUSTER-HEARTBEAT-STATE` (B) |
| `CONFIG-VIPER-INSTANCE` | `CONFIG-GLOBAL-VIPER-STATE` (TD, существовал) + `CONFIG-ENV-KEYS-UNBOUND` (B) |
| M-6 в `INHIBIT-STATE-LIFECYCLE` | отдельно `INHIBIT-MATCH-PERF` (TD) |
| M-11, L-16 в `PUBLISH-SINGLE-RETRY-LAYER` | `CIRCUIT-BREAKER-HALF-OPEN` (B), `PUBLISH-CLIENT-PLUMBING` (TD) |
| L-24 | дописан в `INHIBIT-CONFIG-LOUDNESS` (TD) |
| `QUALITY-GATES-ALIGN` | B; поглощает остаток `QUALITY-GATES-DIRTIES-TREE` (BL) |

Бандлы (`TECH-DEBT.md` § Bundles): `PUBLISH-QUICK-FIX-BUNDLE`, `CONFIG-RELOAD-BUNDLE`, `INVESTIGATION-SAFETY-BUNDLE`, `API-COMPAT-BUNDLE`, `INHIBITION-LIFECYCLE-BUNDLE`, `REDIS-STORAGE-BUNDLE`.

### Этап 1 — Security & correctness quick wins (~2d суммарно, параллелится)
| Задача (slug-кандидат) | Находки | Tier | Оценка | Зависимости |
|---|---|---|---|---|
| `EMAIL-RENDER-ESCAPING` | H-3, L-1 | Full | 0.25d | — |
| `PROVIDER-RESOLVE-STATELESS-PD` | C-2 (PD) | Standard | 0.25d | — |
| `PUBLISH-PER-TARGET-STATE` | H-4 | Standard | 0.5d | — |
| `RELOAD-SERIALIZE` | H-5, M-19, L-5 | Standard | 0.5d | — |
| `LLM-RETRY-CLASSIFY` | H-7, L-8 | Lightweight | 0.25d | — |
| `PUBLISH-SECRETS-IN-LOGS` | M-9, M-10, L-2 | Standard | 0.5d | — |
| `SERVER-HARDENING` | L-3, L-4 | Lightweight | 0.25d | — |
| `QUEUE-PANIC-SAFETY` | L-19, M-7 (`sync.Once`) | Lightweight | 0.1d | — |
- **Результат:** закрыты инъекции и утечки секретов; PD закрывает инциденты; reload безопасен при параллельных вызовах; процесс не падает от паники паблишера.

### Этап 2 — Alertmanager semantics (~5d, последовательно по подсистемам)
| Задача | Находки | Tier | Оценка | Зависимости |
|---|---|---|---|---|
| `INHIBIT-SOURCE-CACHE-EVICTION` | C-1 (a, b, c) | Full | 1d | — |
| `SILENCED-ALERTS-INGEST` | H-1 | Full | 0.5d | — (меняет контрактный тест) |
| `DURATION-UPSTREAM-UNITS` | H-2, D-6 | Standard | 0.25d | — |
| `ROUTE-CONTINUE-NO-FANOUT` (существует) | H-10 | Full | 1d | — |
| `SILENCE-VALIDATION-PARITY` | M-2, D-3 | Standard | 0.25d | желательно после R-2 шага 1 (conformance-тест) |
| `CONFIG-VALIDATION-FAIL-CLOSED` | M-20, H-6, D-7 | Standard | 0.5d | — |
| `API-V2-ERROR-SHAPE` | M-15, L-6 | Lightweight | 0.25d | — |
| `INGEST-PARSE-VALIDATE` | M-17 | Standard | 0.5d | — |
| `GROUP-STORE-LOST-UPDATE` | M-1 (+ `GROUPING-NEW-GROUP-CREATE-RACE`, `REDIS-GROUP-STORAGE-UNLOCKED-WRITES`) | Full | 1d | — |
| `PROVIDER-RESOLVE-ROOTLY` | C-2 (Rootly) | Standard | 0.5d | после `PUBLISH-PER-TARGET-STATE` |
- **Результат:** upstream-конфиги и клиенты (amtool, karma) работают без оговорок по этим пунктам; обновлён `ALERTMANAGER_COMPATIBILITY.md`.

### Этап 3 — Resource bounds & resilience (~4d)
| Задача | Находки | Tier | Оценка | Зависимости |
|---|---|---|---|---|
| `STORE-RETENTION-GC` | M-3, M-4 (retention), GRP-store gauges (L-15 часть) | Standard | 1d | — |
| `INHIBIT-STATE-LIFECYCLE` | M-5, M-6 | Standard | 1d | после `INHIBIT-SOURCE-CACHE-EVICTION` |
| `PUBLISH-SINGLE-RETRY-LAYER` | M-8, D-8, M-11, L-16 | Full | 1.5d | после этапа 1 |
| `QUEUE-GRACEFUL-STOP` | M-7 | Standard | 0.5d | вместе с предыдущим (тот же файл) |
| `INVESTIGATION-ASYNC-DEDUP` | H-8, M-10 (tools) | Standard | 1d | смежно `PROD-LLM-ALERT-PATH-ISOLATION`, `PHASE-5A-INVESTIGATION-PIPELINE` |
| `INVESTIGATION-PROMPT-ISOLATION` | H-9, L-7, L-9 | Full | 1d | после `INVESTIGATION-ASYNC-DEDUP` |
| `PUBLISH-CLIENT-GC` | L-17, L-18 | Lightweight | 0.5d | после `PUBLISH-PER-TARGET-STATE` |
- **Результат:** память ограничена на долгом аптайме; медленный таргет не блокирует очередь и не открывает breaker здоровым; LLM-путь с бюджетом и изоляцией данных.

### Этап 4 — Dead code removal (~3d, только `refactor:`, можно начать сразу параллельно этапу 1)
Последовательность (каждый шаг — отдельный PR, release-gate + `go list -deps` дифф):
1. `DEAD-PKGS-NO-IMPORTERS` — `pkg/templatevalidator`, `pkg/aiclassifier`, `pkg/telemetry` (закрывает `DEAD-PKG-TELEMETRY`), `pkg/middleware`, `internal/business/template`, `internal/storage`; `go mod tidy` (закрывает `GO-MOD-TIDY-CHECK` частично). 0.5d.
2. `DEAD-HTTP-SURFACE` — `application.go`, `middleware.go`, `handler_registry.go`, `cmd/server/handlers`, `internal/realtime`, `internal/ui`, мёртвые webhook-файлы, сужение embed (закрывает `DEAD-APPLICATION-MIDDLEWARE`, `SILENCES-CHECK-404` через удаление). 1d. Зависит от решения, нужен ли CORS (`server.cors.*`) — если да, сначала подключить живой CORS-middleware в `main.go`.
3. `DEAD-TEMPLATE-STACK` — `infrastructure/template`, `notification/template` (кроме `defaults/email.go` до этапа 1 `EMAIL-RENDER-ESCAPING`), sprig. 0.5d. Зависит от `EMAIL-RENDER-ESCAPING`.
4. `DEAD-PUBLISHING-SURFACE` — enhanced webhook stack, DLQ, мёртвые infra/business-файлы (закрывает `FU-UNWIRED-PUBLISH-SURFACES` частично). 0.5d.
5. `DEAD-CONFIG-SUBSYSTEMS` — `update_service/storage`, `service.go`, `sanitizer.go`, `example.go`, `LoadConfigFromEnv`, мёртвые ветки `atomicApply`, `configvalidator/{parser,result,options}`; мёртвые ключи конфига — удалить или WARN (M-23, `DEAD-WEBHOOK-SECURITY-CONFIG`). 0.5d. Ключи конфига — breaking для пользователей, указавших их: запись в `CHANGELOG` с migration note.
6. `DEAD-SILENCING-CACHE` + мелочь (`database/postgres` мёртвые файлы, `lock/example.go`→`_test.go`, `core.Cache`, `RunMigrationsDown`). 0.5d.
7. `DB-ORPHAN-TABLES` — drop-миграция таблиц-сирот. Отдельно, Standard (схема БД). 0.25d.
- **Результат:** −≥25k строк, бинарь и граф `govulncheck` меньше, нет ложных гарантий в конфиге и доках.

### Этап 5 — Duplication consolidation (~5d, `refactor:` + тесты)
| Задача | Находки | Оценка | Зависимости |
|---|---|---|---|
| `LABELMATCH-PKG` (R-2) — шаг 1: пакет + conformance-тест; шаги 2–5: перевод потребителей по одному PR | D-1, D-2, M-16, L-11, cheap dups | 2d | после этапа 4 (меньше копий); `SILENCE-VALIDATION-PARITY` и `CONFIG-VALIDATION-FAIL-CLOSED` используют его, если идут позже |
| `METRICS-V2-ONLY` (R-5) | M-26, D-10 | 1d | после `DEAD-PKGS-NO-IMPORTERS` |
| `CONFIG-SCHEMA-PARITY` (R-4) | M-21, D-5, L-25, L-24 | 1.5d | после `DURATION-UPSTREAM-UNITS`, `DEAD-CONFIG-SUBSYSTEMS` |
| `CONFIG-VIPER-INSTANCE` | `CONFIG-GLOBAL-VIPER-STATE`, M-22 | 1d | после `RELOAD-SERIALIZE` |
| `REDIS-HELPERS` | D-12, L-12, L-13, L-14, CAS Lua | 0.5d | — |
| `PUBLISH-CLIENT-PLUMBING` | D-9, D-15, L-20, L-21, generic client cache | 0.5d | после `PUBLISH-SINGLE-RETRY-LAYER` |
- **Результат:** каждое семантическое решение реализовано один раз; изменение matcher-семантики — одна правка + один тест.

### Этап 6 — Observability (~2d, можно параллельно этапам 3–5)
| Задача | Находки | Оценка |
|---|---|---|
| `HTTP-METRICS-ACCESS-LOG` | M-14 | 0.5d |
| `PUBLISH-HEALTH-PROBES` | M-12, M-13 | 0.5d |
| `CLUSTER-PEERS-ZSET` | L-15 | 0.25d |
| `TIMER-FIRE-OUTCOME-METRIC` (существует) | — | 0.25d |
| `UI-ROUTE-PREFIX-LINKS` | M-18 | 0.25d |
- **Результат:** латентность/5xx ingest, исходы доставки и здоровье таргетов видны в `/metrics`; дашборд работает под префиксом.

### Этап 7 — Architecture (~6–8d, нарезать на слайсы ≤2d)
| Задача | Находки | Оценка | Зависимости |
|---|---|---|---|
| `LAYER-GATE-DEPGUARD` (R-8) + расширение линтеров (`bodyclose`, `gosec`, `noctx`) | M-25 | 1d | после этапа 4 (меньше ложных срабатываний) |
| `NOTIFY-PIPELINE-EXTRACT` (R-6, R-9) | M-24 (grouping) | 2–3d | после этапов 2–3 по grouping/inhibition и `LABELMATCH-PKG` |
| `SERVICE-REGISTRY-SPLIT` (R-7) | M-24 (registry) | 2d | после `RELOAD-SERIALIZE`, `DEAD-HTTP-SURFACE` |
| `QUALITY-GATES-ALIGN` | M-28, L-29 (embed миграций), integration build-tag | 0.5d | — (можно сразу, quick win) |
| `DEPS-CLIENT-GO-BUMP` | M-29 | 0.5d | отдельный PR, карантин 7 дней |
| `SQLITE-LITE-HARDENING` | M-27 | 0.25d | — |
- **Результат:** границы слоёв проверяются линтером; notify chain и wiring читаемы и тестируемы по частям.

### Этап 8 — Test debt (сквозной)
- `SILENCE-REPO-INTEGRATION-TESTS` (H-11) — 1d, можно сразу.
- `TESTS-INTEGRATION-TAG` + замена `time.Sleep` в flaky-тестах — 1d, закрывает часть flaky-записей `BUGS.md`.
- Тесты § 8 пп. 1–4 пишутся внутри соответствующих задач этапов 1–2 (red → green).

### Сводка последовательности и параллелизма

```text
Этап 0 ─┬─> Этап 1 (quick wins, параллельно) ─> Этап 2 (семантика) ─> Этап 3 (ресурсы)
        │                                                              │
        ├─> Этап 4 (dead code, параллельно с 1) ───────────────────────┴─> Этап 5 (консолидация) ─> Этап 7 (архитектура)
        │
        └─> Этап 6 (наблюдаемость) и Этап 8 (тесты) — в любой момент, по WIP
```

| Категория | Задачи |
|---|---|
| Быстро (≤0.5d) | все задачи этапа 1, `DURATION-UPSTREAM-UNITS`, `API-V2-ERROR-SHAPE`, `SILENCE-VALIDATION-PARITY`, `QUALITY-GATES-ALIGN`, `SQLITE-LITE-HARDENING`, большинство шагов этапа 4, этап 6 |
| Архитектурный рефакторинг | `LABELMATCH-PKG`, `CONFIG-SCHEMA-PARITY`, `CONFIG-VIPER-INSTANCE`, `NOTIFY-PIPELINE-EXTRACT`, `SERVICE-REGISTRY-SPLIT`, `LAYER-GATE-DEPGUARD`, `PUBLISH-SINGLE-RETRY-LAYER` |
| Строго отдельно от функциональных изменений | весь этап 4, `LABELMATCH-PKG` (шаги перевода), `METRICS-V2-ONLY`, `REDIS-HELPERS`, `NOTIFY-PIPELINE-EXTRACT`, `SERVICE-REGISTRY-SPLIT`, `DEPS-CLIENT-GO-BUMP`, `DB-ORPHAN-TABLES` |
| Breaking для пользователей (CHANGELOG + migration notes) | `SILENCED-ALERTS-INGEST` (видимость suppressed-алертов в API), `SILENCE-VALIDATION-PARITY` (match-all отклоняется), `CONFIG-VALIDATION-FAIL-CLOSED` (битые matchers больше не проходят), `DEAD-CONFIG-SUBSYSTEMS` (удалённые ключи), `API-V2-ERROR-SHAPE` |

Суммарная оценка: ~30–35 дней работы при WIP 1; критический путь до снятия блокеров § 9 — этапы 1–3 (~11d).
