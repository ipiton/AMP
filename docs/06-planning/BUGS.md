# BUGS

Код, нарушающий свой контракт (граница с долгом — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt). Исправленный баг удаляется отсюда; отчёт — в `DONE.md`.

## Open

<!-- Записи в виде списка (`- [ ] **SLUG** — ...`) — формат до Solo Kanban 1.1. Переводятся в Entry Format при взятии в работу. -->

### [medium][Gate][~0.25d] LINT-ERRCHECK-CHANNEL-RECEIVE
- **Title:** release-gate `lint` красный на `main`: golangci-lint 2.15.0 ловит errcheck на приёме из канала в тестах
- **Problem:** локальный golangci-lint обновился до 2.15.0 (go1.27.1, бинарь от 2026-10-21 21:54) и даёт 7 `errcheck` «Error return value is not checked» на голом приёме из канала, где элемент — `error`: `go-app/internal/infrastructure/publishing/delivery_confirmation_test.go:559,602,645` (`case <-handle.Done():`), `go-app/internal/infrastructure/silencing/redis_event_bus_test.go:202,246,268,282` (`<-done`). Код не менялся с 2026-08-19; release-gate 2026-10-20 на прежней версии линтера был зелёным. Версия линтера нигде не закреплена (`go-app/Makefile:158` ставит `@latest`, release-gate берёт бинарь из `PATH`).
- **Impact:** release-gate каждой задачи — `RESULT: FAIL` на шаге `lint` независимо от диффа; настоящая регрессия lint на этом фоне не видна.
- **Fix:** в тестах принять значение (`_ = <-done` / `case err := <-handle.Done(): _ = err` или проверить его); закрепить версию golangci-lint для гейта и CI (`.golangci` / скрипт установки), чтобы обновление линтера было отдельным изменением.
- **Refs:** найдено при HELM-VCT-CHART-LABELS (2026-10-22), `tasks/archive/HELM-VCT-CHART-LABELS/evidence/release-gate.txt`. Смежно `QUALITY-GATES-ALIGN`.
- **Status:** open

### [medium][Config][~0.5d] TEMPLATED-FIELDS-NOT-RENDERED
- **Title:** `email_configs[].to` и `slack_configs[].actions[]` upstream шаблонизирует, AMP не рендерит и при загрузке валидирует как литерал
- **Problem:** `EmailConfig.To` — `validate:"required,email"` (`go-app/internal/infrastructure/routing/receiver.go`), в target уходит как есть (`config_targets.go:645`, заголовок `to`). Upstream рендерит `to` шаблоном и принимает список адресов через запятую — у AMP не грузится ни `to: '{{ .CommonLabels.owner }}@example.com'`, ни `to: 'a@example.com, b@example.com'`. `SlackAction` (`text`/`url`/`style`, теги `required`/`url`/`oneof`) — upstream шаблонизирует все поля action, AMP actions не отправляет вовсе (`slackTarget`: «not expressible»), но шаблонный `url` валит загрузку.
- **Impact:** upstream-конфиг с несколькими получателями письма или шаблонным `to` не загружается; с шаблонной Slack-кнопкой — тоже, хотя кнопка всё равно потерялась бы.
- **Fix:** `to` — разрешить список адресов (`net/mail.ParseAddressList`) и шаблон (`template_expr|…`, как в SLACK-COLOR-TEMPLATE-REJECTED), рендерить `to` в `emailTemplateFields` и брать отрендеренный адрес в email publisher. `actions` — `template_expr|…` на полях, пока actions не отправляются; рендер — вместе с поддержкой actions. Просто ослабить валидацию `to` без рендера нельзя: письмо уйдёт на литерал `{{ … }}`.
- **Refs:** найдено при SLACK-COLOR-TEMPLATE-REJECTED (2026-10-20), аудит валидаторов шаблонных полей.
- **Status:** open

### [low][Grouping][~0.5d] GROUPING-NEW-GROUP-CREATE-RACE
- **Title:** два конкурентных алерта одной новой группы создают две группы и два таймера `group_wait`
- **Problem:** `DefaultGroupManager.AddAlertToGroup` (`go-app/internal/infrastructure/grouping/manager_impl.go:246`) делает `storage.Load` → `createNewGroupUnsafe` → `storage.Store` без сериализации по ключу группы, хотя `createNewGroupUnsafe` документирован «Caller must hold write lock». Оба вызова получают `not found`, каждый создаёт группу, последний `Store` затирает первый (алерт пропадает из storage до следующего resend), оба стартуют `group_wait`.
- **Impact:** с `group_wait > 0` маскируется (второй таймер заменяет первый); с `group_wait: 0s` (разрешён с ROUTE-TIMINGS-BUNDLE) возможны две отдельные нотификации вместо одной и потеря алерта в первой. Предсуществующее, найдено deep-review ROUTE-TIMINGS-BUNDLE (F3).
- **Fix:** per-group-key сериализация создания (keyed mutex / singleflight) или условная запись в storage; тест на конкурентный `AddAlertToGroup` новой группы с `group_wait: 0`.
- **Refs:** `tasks/archive/ROUTE-TIMINGS-BUNDLE/review-findings.md` § F3.
- **Status:** open

### [high][Routing][~1d] ROUTE-CONTINUE-NO-FANOUT
- **Title:** `continue: true` не даёт доставку во второй receiver — алерт уходит только в первый совпавший route
- **Problem:** путь публикации берёт одно решение: `AlertProcessor.evaluateRoute` (`go-app/internal/core/services/alert_processor.go:271`) → `routeTreeEvaluator.Evaluate` (`internal/application/route_evaluator.go:100`) → `businessrouting.RouteEvaluator.Evaluate` — первый матч; `services.RoutingDecision` хранит один `Receiver`. Мультиматч по `continue` есть в `RouteMatcher.FindMatchingRoutes` и `RouteEvaluator.EvaluateWithAlternatives` (`internal/business/routing/evaluator.go:277`), но у `EvaluateWithAlternatives` нет ни одного не-тестового вызова.
- **Impact:** конфиг «critical → дежурный канал **и** общий канал» через `continue` молча теряет вторую доставку; `docs/ALERTMANAGER_COMPATIBILITY.md:41` заявляет `continue` как 🟢 Supported. Для параллельного запуска рядом с Alertmanager в sema-prod — расхождение по составу получателей.
- **Fix:** решение маршрутизации — список (primary + alternatives); группировка и публикация по каждому receiver'у отдельно (ключ группы уже содержит receiver), dedup/nflog — на пару (группа, receiver). Тест через `LoadConfig` + `ProcessAlert`: два sibling-route с `continue: true` → обе группы и обе публикации. Поправить строку в `ALERTMANAGER_COMPATIBILITY.md`.
- **Refs:** k3d HELM-EXTRA-VOLUMES 2026-10-20 (`tasks/archive/HELM-EXTRA-VOLUMES/evidence/k3d-extra-volumes.md` §3): `severity=critical` → только `telegram-panic` (`matched_route=route.routes[0]`), sibling `slack` пуст.
- **Status:** open

### [low][Test][~0.1d] FLAKE-GROUP-INTERVAL-CHAIN-RESOLVE
- **Title:** `TestGroupIntervalChain_ResolveGoesOutAndTheGroupIsGone` флейкает
- **Problem:** `go-app/internal/infrastructure/grouping/group_interval_chain_test.go:176` — `no further fires for the removed group`: после удаления полностью разрешённой группы счётчик сработавших таймеров ещё растёт. Замер 2026-10-20, `-count=300`: 4 падения на `main` @ `28007e8` и 4 на `bugfix/route-timings` — предсуществующий.
- **Impact:** шаг `test` гейта может краснеть случайно.
- **Fix:** разобрать, доходит ли до fire таймер, взведённый до удаления группы (тогда это продуктовая гонка), или тест снимает `expiredTimers` раньше, чем завершилась уже начатая обработка; во втором случае — ждать стабилизации счётчика.
- **Refs:** замер в сессии ROUTE-TIMINGS-BUNDLE.
- **Status:** open

### [medium][Test][~0.25d] FUZZ-RANDOM-ALERTS-RACE-TIMEOUT
- **Title:** `TestFuzz_RandomAlerts` под `-race` съедает больше половины 10-минутного таймаута пакета
- **Problem:** `go-app/internal/infrastructure/publishing/formatter_fuzz_test.go` в одиночном прогоне `go test -race -run 'TestFuzz_RandomAlerts$'` идёт 5m21s (2026-10-20, M-series). В шаге `race` release-gate под нагрузкой пакет `internal/infrastructure/publishing` упал `panic: test timed out after 10m0s` с этим тестом в `running tests`.
- **Impact:** required-шаг `race` гейта краснеет по таймауту без реальной регрессии; на shared runner'ах CI вероятнее.
- **Fix:** уменьшить число итераций под `-race` (`testing.Short()`/build tag `race` или env), либо перенести в отдельный fuzz-таргет `go test -fuzz` вне гейта; цель — секунды, не минуты.
- **Refs:** release-gate 2026-10-20 на `main` @ `7958ef8` (шаг `race`).
- **Status:** open

### [low][Publishing][~0.25d] SLACK-EXTRA-FIELDS-NOT-UPSTREAM
- **Title:** Slack-сообщение с операторскими `title`/`text` несёт лишний блок `fields` (Status/Started/Namespace)
- **Problem:** `template_formatter.go:328` берёт colour/fields фиксированного форматтера (`formatter.go:552-566`) как базу, поверх которой накладываются отрендеренные поля оператора. Upstream без `fields:` в конфиге поля не шлёт.
- **Impact:** в Slack дублируется информация, которую оператор уже вывел в `text`; расхождение с обещанием «нетронутый upstream-конфиг даёт upstream-вывод». Плюс `title_link` относителен (`/#/alerts?...`), если не задан `server.externalUrl`, — это настройка, не баг.
- **Fix:** при наличии операторских шаблонов не переносить `fields` фиксированного форматтера (переносить только явно заданные); тест на payload без `fields`.
- **Refs:** проверка 2026-10-20 на конфиге sema (`errors-slack`).
- **Status:** open

### [low][Test][~0.1d] REFRESH-WORKER-WARMUP-STOP-FLAKY
- **Title:** `TestBackgroundWorker_CancellationDuringWarmup` падает под нагрузкой
- **Problem:** тест (`go-app/internal/business/publishing/refresh_worker_test.go:116`) ждёт `Start()` через `time.Sleep(2ms)` и вызывает `Stop`, рассчитывая успеть до конца `WarmupPeriod: 10ms` (`refresh_test_utils.go:295`). Под нагрузкой шага `test` release-gate между `Start` и `Stop` проходит больше 10 мс, первый refresh срабатывает: `expected: 0, actual: 1`.
- **Impact:** required-шаг `test` гейта краснеет без регрессии; изолированно `-count=30` зелёный.
- **Fix:** в тесте warmup с запасом (секунды) либо синхронизация через канал/фейковые часы вместо sleep.
- **Refs:** release-gate 2026-10-20 на `feature/helm-extra-volumes` @ `0c3156d` (пакет веткой не менялся).
- **Status:** open

### [low][Config][~0.25d] PAGERDUTY-SERVICE-KEY-ONLY-REJECTED
- **Title:** PagerDuty только с `service_key` / `service_key_file` не загружается, хотя валидатор его пропускает
- **Problem:** `routing.PagerDutyConfig.RoutingKey` помечен `validate:"required"` (`go-app/internal/infrastructure/routing/receiver.go:324`), поэтому `routing.Parse` отвергает конфиг без `routing_key` (`Field validation for 'RoutingKey' failed on the 'required' tag`), и фолбэк на `ServiceKey` в `config_targets.go` недостижим. Валидатор (E122) после HELM-EXTRA-VOLUMES принимает `service_key`/`service_key_file`, как и upstream.
- **Impact:** конфиг с устаревшим `service_key` (upstream-совместимый) не стартует с непонятной ошибкой struct-валидации; W116 на `service_key_file` не выдаётся.
- **Fix:** `required` → «`routing_key` или `service_key`» (кастомная проверка после `resolveFileSecrets`); тест через `LoadConfig` с `service_key` и с `service_key_file`.
- **Refs:** `tasks/archive/HELM-EXTRA-VOLUMES/review-findings.md` R2-F1; предсуществующее с FU7-B.
- **Status:** open

### [medium][Grouping][~0.5d] GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN
- **Title:** транзиентная ошибка чтения группы в timer-callback обрывает цепочку таймеров
- **Problem:** `onGroupWaitExpired` (`go-app/internal/infrastructure/grouping/manager_impl.go:1866-1873`) на любой ошибке `storage.Load` возвращает `nil` без continuation, а `onTimerExpired` после callback'ов удаляет запись таймера (`timer_manager_impl.go`, cleanup после callback'ов). При транзиентной ошибке Redis группа после этого больше не нотифицирует: таймера нет ни локально, ни в storage, reconciliation подобрать нечего. Предсуществующий дефект; раньше его иногда маскировало повторное срабатывание опоздавшей реплики, после `GROUPING-TIMER-LOCK-FIX` она отсеивается по `not_found`.
- **Impact:** в HA (Redis) группа может замолчать до следующего нового алерта в ней. _(Уточнено PROD-GROUPING-DEFAULT, 2026-10-10: «до следующего алерта» теперь гарантировано — `AddAlertToGroup` через `ensureGroupTimer` перевзводит таймер группе без таймера; раньше новый алерт в такой группе ждал `repeat_interval`.)_
- **Fix:** в callback различать not-found группы и транзиентную ошибку; при транзиентной возвращать ошибку и не удалять запись таймера, чтобы reconciliation повторил. Тест с `loadFailingGroupStorage`.
- **Refs:** `tasks/archive/GROUPING-TIMER-LOCK-FIX/review-findings.md` F7.
- **Status:** open

- [ ] **ALERT-STORE-DEDUP-KEY-STARTSAT** — повторный `POST /api/v2/alerts` без `startsAt` создаёт в memory store **новую копию** алерта, а не обновляет существующую: ключ дедупликации стора — `fingerprint|startsAt` (`go-app/internal/infrastructure/storage/memory/alert_store.go`, `dedupKey`), а пустой `startsAt` на каждом POST превращается в `now`. В итоге `GET /api/v2/alerts` отдаёт один и тот же алерт N раз с разными `startsAt`. Upstream сливает по fingerprint и сохраняет самый ранний `startsAt`. Воспроизведено вживую на `main` и на ветке `PARITY-RESOLVE-TIMEOUT-ENDSAT` (2026-09-24, lite, два POST `{"labels":{"alertname":"X"}}`). Prometheus и `amtool` шлют `startsAt` сами, поэтому затронуты в основном curl и самописные клиенты. Из-за этого для них не работает и продление окна `resolve_timeout`. Не регресс. Найдено на `/testing` PARITY-RESOLVE-TIMEOUT-ENDSAT. Чинить отдельной задачей: для отсутствующего `startsAt` брать `startsAt` уже горящего алерта с тем же fingerprint. Смотреть осторожно: dedup в БД (`deduplication.go`) и resolved-путь стора завязаны на тот же ключ.

- [ ] **INVESTIGATION-ALERT-TIME-NOT-SET** — `investigation.WithAlertTime` (`go-app/internal/core/investigation/context.go`) нигде в рабочем коде не вызывается: `queue.processJobWithAgent` передаёт в `AgentLoop.Run` голый `q.ctx`. Tools (`prometheus_query_range`, `loki_query_range`) через `AlertTimeFromCtx` падают на фолбэк `time.Now()` и якорят окно ±15 мин на момент расследования, а не срабатывания алерта. Для ретраев (backoff до 60s × `max_retries`) и очереди под нагрузкой окно уезжает от события. Фикс: `ctx = investigation.WithAlertTime(ctx, job.Alert.StartsAt)` перед `Run` (и в 5A-пути, если tools появятся там) + тест, что tool видит `StartsAt`. README пакета уже помечает это как known gap. Не регресс. Найдено на `/research` PHASE-6B-RUNBOOK-ENGINE (2026-09-27).

- [ ] **AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT** — `AgentLoop.Run` (`go-app/internal/core/investigation/agent_loop.go`, ветка `default` в `switch resp.Kind`) возвращает `(nil, err)`, а `InvestigationQueue.processJobWithAgent` (`go-app/internal/infrastructure/investigation/queue.go`) на ошибке сразу читает `agentRun.TerminationKind` ⇒ nil-pointer panic в воркере. Сейчас недостижимо из прод-клиента (`parseOpenAIAgentResponse` отдаёт только два известных kind), но любой новый `AgentLLMClient` или мок с иным kind роняет воркер. Фикс: возвращать `&AgentRunResult{TerminationKind: "error", ...}` и/или nil-guard в queue + тест. Не регресс. Найдено на `/implement` PHASE-6B-RUNBOOK-ENGINE (2026-09-27).

### [medium][Inhibition][~0.25d] INHIBIT-RELOAD-ROLLBACK-KEEPS-RULES
- **Title:** откат reload после коммита оставляет новые inhibit-правила
- **Problem:** `ServiceRegistry.ReloadConfig` применяет правила матчеру (`UpdateRules`, `go-app/internal/application/service_registry.go:2702-2717`) до `reloadRoutingTree`; если тот падает, `rollbackPostCommit` восстанавливает config, routing, шаблоны и targets, но не правила матчера.
- **Impact:** отклонённый `/-/reload` (ошибка дерева маршрутов) оставляет в работе inhibit-правила из отвергнутого конфига — до следующего успешного reload или рестарта. Было и до `FU-TOPLEVEL-INHIBIT-RULES`.
- **Fix:** применять ингибицию после `reloadRoutingTree` либо в откате звать `UpdateRules(previousConfig.InhibitionRules())`; тест с падающим `reloadRoutingTree`.
- **Refs:** `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/review-findings.md` F3.
- **Status:** open

### [medium][Helm][~0.25d] ESO-SECRET-OWNERSHIP-CONFLICT
- **Title:** при `externalSecrets.enabled` Helm и External Secrets Operator претендуют на один Secret
- **Problem:** `helm/amp/templates/secret.yaml` рендерит `<fullname>-secrets` безусловно, а `templates/externalsecret.yaml` при `externalSecrets.enabled` целится в тот же Secret (`target.name: <fullname>-secrets`, `creationPolicy: Owner`). Ожидаемо: ESO перезаписывает данные, Helm на `upgrade` возвращает свои (в том числе пустые ключи), владелец объекта неоднозначен.
- **Impact:** не воспроизводилось — найдено чтением шаблонов. Затрагивает только установки с ESO; дефолт и `values-production.yaml` его не включают.
- **Fix:** не рендерить `secret.yaml` (или его ключи, которые даёт ESO) при `externalSecrets.enabled`; render-тест; проверить на кластере с ESO.
- _(2026-10-14, HELM-DEFAULTS-VALIDATE review F2/R3-1)_ путь стал рекомендуемым: `fail` в `secret.yaml` при `llm.enabled=true` без ключа советует `externalSecrets.enabled`. Рядом второй, нерабочий путь: `helm/amp/DEPLOYMENT.md:84` предлагает `llm.externalSecrets.*` (`templates/llm-secret.yaml`), но Deployment всегда читает `<fullname>-secrets/llm-api-key`, а guard принимает только глобальный `externalSecrets.enabled` ⇒ рендер падает (до задачи — `CreateContainerConfigError`). В фиксе оставить один путь ESO для ключа LLM и поправить DEPLOYMENT.md.
- **Refs:** `tasks/archive/HELM-DEFAULTS-VALIDATE/research.md` § Findings (линза operational), `review-findings.md` F2, R3-1.
- **Status:** open

### [low][Test][~0.25d] FLAKE-REFRESH-IN-PROGRESS
- **Title:** `TestRefreshNow_RefreshInProgress` флейкает под нагрузкой
- **Problem:** `go-app/internal/business/publishing/refresh_manager_impl_test.go:312` — `Target error should be in err chain: expected: "refresh already in progress"`. В логе перед этим `Manual refresh rate limit exceeded`: второй `RefreshNow` получает ошибку rate limit (50ms), а не «уже идёт» — первый refresh (~200ms) завершился раньше, чем тест сделал второй вызов. Изолированно `-count=20` — 20/20 ok; в шаге `race` того же прогона — ok. Предсуществующий: тест в baseline-падениях апрельских прогонов (`tasks/archive/*/forge-events.jsonl`).
- **Impact:** шаг `test` required-гейта `gate` краснеет случайно (release-gate 2026-10-07, ветка `bugfix/prod-config-file-fallback`, пакет веткой не тронут).
- **Fix:** держать первый refresh занятым явно (блокирующий фейковый discovery до сигнала теста), а не полагаться на его длительность.
- **Refs:** `tasks/archive/PROD-CONFIG-FILE-FALLBACK/tasks.md` § Testing notes.
- **Status:** open

### [medium][Test][~0.25d] GROUPING-ORPHAN-ADOPTION-TEST-FLAKY
- **Title:** `TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica` флейкает
- **Problem:** `go-app/internal/application/service_registry_grouping_test.go:585` — `continuation timer type = "group_wait", want "group_interval"`. Замер 2026-10-07 под нагрузкой (параллельно с release-gate), `-count=80`: 6 падений на `main` @ `fa682d4`, 7 на ветке с runbook engine — предсуществующий, не от слияния. Без нагрузки на `main` 0/40. Гипотеза: тест ждёт `stats.ReconciledTimers >= 1` и сразу читает storage, а счётчик растёт раньше, чем `onGroupWaitExpired` записывает continuation `group_interval`, — тест читает ещё старую запись `group_wait`.
- **Impact:** шаг `test` required-гейта `gate` краснеет случайно; после `GROUPING-TIMER-LOCK-FIX` (2026-10-10) — последний известный флейк в grouping.
- **Fix:** в тесте ждать опросом, пока тип записи станет `group_interval` (с тем же дедлайном), а не читать один раз; если запись `group_wait` держится дольше дедлайна — это уже продуктовый дефект.
- **Refs:** release-gate 2026-10-07 на `feature/phase-6b-runbook-engine`, `~/amp-audit/gate-runbook.log`.
- **Status:** open

### [low][Test][~0.1d] HELM-TEST-SIGPIPE-PIPEFAIL
- **Title:** render-тесты чарта случайно падают на `printf: Broken pipe`
- **Problem:** `helm/amp/tests/render-networkpolicy.sh` (`assert_render_fails`, стр. 59) под `set -euo pipefail` делает `printf '%s' "${output}" | grep -q -F -e "${expected}"`. `grep -q` выходит на первом совпадении, `printf` получает SIGPIPE, и `pipefail` превращает успешное совпадение в провал: `FAIL ingressController only, Ingress off (wrong error: …)`, хотя текст ошибки ожидаемый. Поймано release-gate 2026-10-07; три повторных прогона скрипта — зелёные.
- **Impact:** шаг `helm-tests` required-гейта `gate` краснеет случайно на длинных выводах `helm template`.
- **Fix:** `grep -q -F -e "${expected}" <<<"${output}"` (here-string вместо пайпа) во всех `tests/*.sh`.
- **Refs:** `~/amp-audit/gate-runbook.log`, 2026-10-07.
- **Status:** open

### [medium][Pipeline][~0.5d] DIRECT-PUBLISH-RENOTIFIES-ON-RESEND
- **Title:** на direct-publish пути каждая повторная отправка алерта старше минуты уходит ресиверу заново
- **Problem:** без `route:` или с `grouping.enabled: false` алерт публикуется через `PublishToReceiver` → `coordinator.PublishToTargets`, где нет nflog (он есть только у группового пути, `coordinator.go:619+`). Prometheus переотправляет горящий алерт каждые `resend_delay` (1m) с новым `endsAt`, дедуп-сервис видит `Updated`, и от повторного уведомления защищает только Rule 7 фильтра с окном 1 мин (`DefaultDedupWindow`). Повтор, пришедший позже окна, уходит ресиверу. Существовало до `PROD-HARDCODED-FILTER`, та задача это не меняла.
- **Impact:** конфиги без дерева route (или с выключенной группировкой) получают уведомление на каждую переотправку вместо одного на `repeat_interval`, как в upstream.
- **Fix:** nflog (или его аналог по fingerprint + status + `repeat_interval`) для direct-publish пути, либо направить такой путь через группировку с тривиальным ключом. После этого пересмотреть, нужен ли Rule 7.
- **Refs:** `tasks/archive/PROD-HARDCODED-FILTER/Spec.md` § Impact Analysis, design premises; `research.md` § Option B.
- **Status:** open

### [low][Helm][~0.2d] SERVICE-METRICS-PORT-MISROUTED
- **Title:** порт `metrics` Service AMP ведёт на экспортёры postgres/redis, а не на AMP
- **Problem:** `templates/service.yaml` открывает `metrics` (`service.metricsPort`, 9090) с `targetPort: metrics` и селектором `amp.selectorLabels`. Под этот селектор попадают и pod'ы postgres/redis, у которых тоже есть порт `metrics` (экспортёры). AMP на 9090 ничего не слушает: метрики отдаются на `http` (`/metrics`), контейнерный порт `metrics` в `deployment.yaml` мёртвый. Запрос на `<release>-amp:9090` уходит экспортёру.
- **Impact:** всё, что скрейпит `metrics` Service AMP (старый ServiceMonitor, ручные конфиги Prometheus), получает метрики БД под видом AMP. Ловушка для оператора, не для приёма алертов.
- **Fix:** убрать порт `metrics` из Service и `containerPort` из Deployment (или ограничить селектор Service компонентом — только отдельным релизом, D3 в Spec PROD-INGRESS-HARDENING).
- **Refs:** ADR-015; `tasks/archive/PROD-INGRESS-HARDENING/Spec.md` D3; подтверждено deep-review (R10), 2026-10-01.
- **Status:** open

### [low][Helm][~0.2d] MONITORING-CRD-DEFAULT
- **Title:** дефолты чарта требуют CRD prometheus-operator
- **Problem:** `monitoring.prometheusEnabled: true` по умолчанию, поэтому `helm install` с дефолтами рендерит `ServiceMonitor` (AMP и redis) и `PrometheusRule` redis. В кластере без CRD `monitoring.coreos.com` install падает на `no matches for kind "ServiceMonitor"`.
- **Impact:** первый install в чистый кластер требует знать про `--set monitoring.prometheusEnabled=false`.
- **Fix:** гейтить на `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"` или сменить дефолт на `false` (breaking для тех, кто на него полагается).
- **Refs:** ADR-015; `tasks/archive/PROD-INGRESS-HARDENING/Spec.md` D7; 2026-10-01.
- **Status:** open

### [low][Helm][~0.25d] HELM-NAMESPACE-OVERRIDE-SPLIT
- **Title:** `namespace` разносит объекты AMP и redis по разным namespace
- **Problem:** шаблоны приложения ставят `namespace: {{ .Values.namespace | default .Release.Namespace }}`, а `redis-*.yaml` — `.Release.Namespace`. При `namespace` ≠ namespace релиза Redis, его Service и NetworkPolicy оказываются в другом namespace, чем AMP: политика redis выбирает AMP pod'ы через `podSelector` без `namespaceSelector` и их не видит, а `cache.host` указывает на Service не там.
- **Impact:** редкая конфигурация (`namespace` задан явно), но тихо: AMP теряет Redis при `valkey.networkPolicy.enabled`. С 2026-10-20 (`AMP-CACHE-NOAUTH`) AMP на дефолтах читает `REDIS_PASSWORD` из `<fullname>-redis-secret`, который лежит в namespace релиза, — при `namespace` ≠ релиза под AMP не стартует (`CreateContainerConfigError`, по коду на ревью, на кластере не прогонялось) вместо тихой деградации; `amp.cache.hostIsValkey` тоже сравнивает с `.Release.Namespace`.
- **Fix:** один источник namespace для всех шаблонов (хелпер `amp.namespace`).
- **Refs:** deep-review PROD-INGRESS-HARDENING R16, `tasks/archive/PROD-INGRESS-HARDENING/review-findings.md`; 2026-10-01; deep-review AMP-CACHE-NOAUTH A2 (`tasks/archive/AMP-CACHE-NOAUTH/review-findings.md`), 2026-10-20.
- **Status:** open

### [low][Grouping][~0.5d] GROUPING-RESOLVED-ONLY-FOR-UNKNOWN-GROUP
- **Title:** resolved-нотификация для группы, о которой получатель не знал
- **Problem:** алерт сработал и разрешился до первого flush (в пределах `group_wait`) — получателю уходит одно сообщение «resolved». Upstream Alertmanager при отсутствии записи в nflog шлёт только при непустом firing-наборе. Проба: AMP 1 нотификация, upstream 0. Не регрессия PROD-GROUPING-DEFAULT, но с группировкой по умолчанию путь стал общим.
- **Impact:** лишнее «resolved» без предшествующего «firing» у получателей с `send_resolved: true`. Расхождение названо в `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13.
- **Fix:** отличать «записи в nflog нет» от «запись не покрывает набор» и при отсутствии записи слать только при непустом firing. Учесть обратную сторону: resolve после простоя получателя дольше TTL записи тогда теряется.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H5; `go-app/internal/infrastructure/grouping/manager_impl.go` (`publishGroupAlerts`), `dedup.go`.
- **Status:** open

### [low][API][~0.25d] FINGERPRINT-PIPE-BREAKS-SIGNATURE
- **Title:** символ `|` в fingerprint из API ломает разбор сигнатуры nflog
- **Problem:** сигнатура группы собирается из пар `fingerprint:status` через разделитель, а `signatureCovers` разбирает её обратно. Fingerprint, пришедший из API как есть (`go-app/internal/application/handlers/alerts.go`, приём `fingerprint` из тела), может содержать `|` — разбор даёт другие элементы, проверка «подмножество» отвечает неверно. По чтению кода, запуском не проверялось.
- **Impact:** для такого алерта возможна лишняя или пропущенная нотификация группы. Штатные клиенты (Prometheus) fingerprint не передают — он вычисляется.
- **Fix:** валидировать fingerprint на входе (hex) либо экранировать элементы сигнатуры.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H10; `go-app/internal/infrastructure/grouping/dedup.go` (`signatureCovers`).
- **Status:** open

### [low][Test][~0.1d] FLAKE-SYNC-WORKER-PERIODIC
- **Title:** `TestSyncWorker_PeriodicExecution` флейкает под `-race`
- **Problem:** `go-app/internal/business/silencing/sync_worker_test.go:204` — мок `ListSilences` ждёт ровно `Times(3)` при тике 100 мс и `time.Sleep(250ms)`. Под нагрузкой сон затягивается до четвёртого тика, мок паникует `The method has been called over 3 times` и роняет весь пакет. Изолированно `-race -count=1` 6/6 зелёный.
- **Impact:** шаг `race` в `scripts/release-gate.sh` краснеет случайно (2026-10-10, ветка `bugfix/prod-grouping-default`, пакет веткой не тронут).
- **Fix:** считать вызовы счётчиком и ждать «не меньше двух тиков» через `Eventually`, без точного `Times`.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/tasks.md` п. 5.1.
- **Status:** open

### [low][Test][~0.1d] MIGRATIONS-TEST-PANICS-ON-SLOW-DOCKER
- **Title:** `TestRunMigrations_ConcurrentReplicas_FreshDB` паникует вместо skip, когда Docker отвечает медленно
- **Problem:** `requireDocker` (`go-app/internal/database/migrations_concurrent_test.go:34`) вызывает `testcontainers.NewDockerClientWithOpts`, а тот при таймауте `docker info` (2 с) паникует в `MustExtractDockerHost` — до ветки `t.Skip`. Воспроизвелось, когда параллельно шла сборка: `context deadline exceeded`. В одиночку тест проходит.
- **Impact:** шаг `test` в `scripts/release-gate.sh` краснеет на загруженной машине (2026-10-10).
- _(2026-10-14, HELM-DEFAULTS-VALIDATE testing)_ тот же механизм в `go-app/internal/infrastructure/repository/postgres_history_test.go:34` (`TestGetTopAlerts_EmptyDatabase`): `docker info: context deadline exceeded` → паника. Фикс — общий хелпер для обоих пакетов.
- **Fix:** в `requireDocker` перехватывать панику (`recover`) и делать `t.Skip`, либо проверять доступность Docker своим вызовом с бóльшим таймаутом.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/tasks.md` п. 5.1.
- **Status:** open

### [low][Lite][~0.1d] LITE-REDIS-CONNECT-ERROR-ON-START
- **Title:** `lite` без Redis пишет ERROR при каждом старте
- **Problem:** профиль `lite` без настроенного Redis всё равно пробует `localhost:6379`; `go-app/internal/infrastructure/cache/redis.go:96` пишет `ERROR Failed to connect to Redis`, затем штатный `WARN Redis cache unavailable, falling back to in-memory cache`. Профиль по документации Redis не требует.
- **Impact:** строка уровня ERROR в логе здоровой установки; ложные срабатывания алертов на ERROR-логи.
- **Fix:** в `lite` без явного адреса Redis не подключаться вовсе; иначе понизить уровень до WARN (ошибку уже несёт следующая строка).
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/evidence/startup-logs.md`.
- **Status:** open

### [low][Test][~0.25d] TESTCONTAINERS-FLAKY-UNDER-LOAD
- **Title:** интеграционные тесты на testcontainers падают на загруженной машине
- **Problem:** 2026-10-14, release-gate при load average ~247: `internal/infrastructure/inhibition/integration_test.go:187` (`TestIntegration_TwoTierAlertCache_Redis`) — `create container: reaper: … unexpected container status "removing"`; `internal/database/postgres/pool_reload_test.go:42` (`startPostgres`) не дождался контейнера и упал по таймауту пакета 10m. Изолированный перезапуск — оба `ok`.
- **Impact:** шаг `test` в `scripts/release-gate.sh` краснеет случайно; таймаут `postgres` стоит 10 минут прогона.
- **Fix:** дедлайн на старт контейнера (`context.WithTimeout`) и `t.Skip`/понятная ошибка вместо висящего теста; для reaper — ретрай старта или `TESTCONTAINERS_RYUK_DISABLED` в гейте с явной очисткой.
- **Refs:** `tasks/archive/HELM-DEFAULTS-VALIDATE/tasks.md` п. 5.4; связано с `MIGRATIONS-TEST-PANICS-ON-SLOW-DOCKER`.
- **Status:** open

### [low][Helm][~0.25d] HELM-LITE-RENDERS-POSTGRES
- **Title:** профиль `lite` рендерит встроенный PostgreSQL, которым AMP не пользуется
- **Problem:** шаблоны `helm/amp/templates/postgresql-*.yaml` гейтятся только `postgresql.enabled` (по умолчанию `true`), комментарий в `values.yaml` («Overridden by profile») не соответствует шаблонам: `helm template --set profile=lite` даёт StatefulSet, Service, Secret и PVC Postgres. Дефект существовал до PROD-POSTGRES-HA-DECISION; README предупреждает «set `false` for the lite profile».
- **Impact:** lite-установка тянет лишний под Postgres и PVC, запрашивает ресурсы ноды впустую.
- **Fix:** гейт `and .Values.postgresql.enabled (eq .Values.profile "standard")` во всех `postgresql-*.yaml` (и в env БД в `deployment.yaml`) либо `fail` на `lite` + `postgresql.enabled`; render-тест.
- **Refs:** `tasks/archive/PROD-POSTGRES-HA-DECISION/review-findings.md` R11.
- **Status:** open

### [medium][Silencing][~0.25d] SILENCE-SYNC-LIMIT-EXCEEDS-FILTER
- **Title:** фоновый воркер silence manager не стартует: `limit must be <= 1000, got 10000`
- **Problem:** на каждом старте AMP с PostgreSQL: `initial cache sync failed: invalid filter parameters: limit must be <= 1000, got 10000`. `go-app/internal/business/silencing/manager_impl.go:479`, `:713` и `sync_worker.go:162` запрашивают `Limit: 10000`, а валидатор фильтра репозитория пропускает не больше 1000 (`go-app/internal/core/errors.go:12`). AMP продолжает работу без GC/stats-воркера.
- **Impact:** истёкшие silences не собираются фоновым GC, кэш активных silences не прогревается на старте, статистика не считается; молча — только строка в логе.
- **Fix:** выровнять лимит (пагинация по 1000 или отдельный внутренний путь без пользовательского лимита) и тест на старт менеджера с реальным репозиторием.
- **Refs:** `tasks/archive/PROD-POSTGRES-HA-DECISION/evidence/restore-drill.md` § Side findings; повторно 2026-10-19 — `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/cache-noauth.txt` (R26).
- **Status:** open

### [low][API][~0.25d] SILENCES-CHECK-404
- **Title:** `POST /api/v2/silences/check` отвечает 404
- **Problem:** хендлер есть (`go-app/cmd/server/handlers/silence_advanced.go`, `CheckAlert`), и `docs/ALERTMANAGER_COMPATIBILITY.md` ссылается на эндпоинт, но в собранном сервере на k3d 2026-10-15 запрос даёт 404 — маршрут не зарегистрирован.
- **Impact:** превью «заглушит ли silence этот алерт» недоступно; документация расходится с поведением. Upstream такого эндпоинта нет, совместимость с Alertmanager не затронута.
- **Fix:** зарегистрировать маршрут или убрать хендлер и упоминания из доков; тест на роутинг.
- **Refs:** `tasks/archive/PROD-POSTGRES-HA-DECISION/tasks.md` (Implementation notes, Phase 2).
- **Status:** open

### [low][Test][~0.1d] FLAKE-INHIBITION-PREFILTER-TIMING
- **Title:** `TestShouldInhibit_PrefilterOptimization` падает по wall-clock порогу
- **Problem:** `go-app/internal/infrastructure/inhibition/matcher_test.go:597` требует, чтобы `ShouldInhibit` на 51 алерте уложился в 500 µs. 2026-10-15 release-gate: 992 µs → FAIL; изолированно 5 прогонов — 3 FAIL. Тест меряет время, а не то, что пре-фильтр отсёк нерелевантные алерты; в замер попадает логирование.
- **Impact:** шаг `test` в `scripts/release-gate.sh` краснеет случайно на ветках без изменений в `go-app`.
- **Fix:** проверять число проверенных кандидатов (счётчик/хук) вместо времени, либо вынести замер в бенчмарк. Тот же приём — у порога в `matcher_test.go:448` (DONE 2026-10-14 HELM-DEFAULTS-VALIDATE, риск).
- **Refs:** `tasks/archive/PROD-POSTGRES-HA-DECISION/evidence/release-gate.txt`.
- **Status:** open

### [medium][Helm][~0.5d] PG-ALERT-RULES-METRICS
- **Title:** часть правил `postgresql.rules` ссылается на метрики, которых exporter не отдаёт, а `PostgreSQLVacuumOld` шумит
- **Problem:** с PG-CONFIG-FIX группа правил парсится и exporter подключён, но сверка со scrape `postgres-exporter:v0.15.0` (k3d 2026-10-17) показала: connection-saturation (2 правила) без `on(instance)` между `pg_stat_activity_count` и `pg_settings_max_connections`; replication-lag ссылается на `pg_replication_lag` (есть `pg_replication_lag_seconds`); wraparound — на `pg_database_age_seconds` (нет); slow-query — на `pg_stat_statements_mean_exec_time_seconds` (коллектор `stat_statements` выключен по умолчанию); bgwriter (2) — на имена без `_total`. Эти правила молчат. `PostgreSQLVacuumOld` срабатывает на таблицах, куда почти не пишут: autovacuum их не трогает, пока не накопится порог изменений, и warning висит бесконечно.
- **Impact:** операторы считают, что насыщение соединений, wraparound и медленные запросы под алертами, а их нет; плюс постоянный шум VacuumOld на тихих таблицах.
- **Fix:** сверить каждое правило с `/metrics` exporter'а (или включить нужные коллекторы), для VacuumOld учитывать `n_dead_tup`/`n_mod_since_analyze` или убрать; `promtool test rules` на каждое правило — вместе с BACKLOG `PG-MONITORING-CHECKS`.
- **Refs:** `tasks/archive/PG-CONFIG-FIX/review-findings.md` R3, R13; `evidence/k3d-acceptance.md` K1.3.
- **Status:** open

### [low][App][~0.25d] AMP-NO-DB-WAIT
- **Title:** AMP выходит с кодом 1, пока PostgreSQL стартует
- **Problem:** на свежей установке AMP стартует раньше встроенного PG, получает `dial tcp …:5432: connect: connection refused` и завершается; kubelet перезапускает — 3–8 рестартов на install (k3d 2026-10-17, то же на `main`).
- **Impact:** шум `CrashLoopBackOff` и рестарты в метриках на каждой установке/рестарте БД; с `--wait` дольше выход в Ready.
- **Fix:** ретрай подключения к БД на старте с backoff до таймаута (или initContainer `pg_isready` для встроенного PG); тест на старт без БД.
- **Refs:** `tasks/archive/PG-CONFIG-FIX/evidence/k3d-acceptance.md` (Known, outside the task).
- **Status:** open

### [low][Helm][~0.1d] PG-PRESTOP-SU-FAILS
- **Title:** preStop встроенного PostgreSQL не работает: `su - postgres` под `runAsUser: 999`
- **Problem:** `templates/postgresql-statefulset.yaml` preStop — `su - postgres -c "pg_ctl stop -D $PGDATA -m fast -w -t 60" || true`; контейнер уже работает под uid 999 без root, `su` падает, `|| true` глушит ошибку. Остановку делает SIGTERM от kubelet (smart shutdown), а не задуманный fast.
- **Impact:** остановка ждёт закрытия клиентских сессий AMP вместо `-m fast`; при долгих сессиях упирается в `terminationGracePeriodSeconds` и SIGKILL (recovery на старте).
- **Fix:** `pg_ctl stop -D "$PGDATA" -m fast -w -t 60` без `su`; проверка на k3d (лог `fast shutdown request`).
- **Refs:** `tasks/archive/PG-CONFIG-FIX/research.md` (code-read, вне scope).
- **Status:** open

### [low][Test][~0.1d] FLAKE-TIMER-CONTINUATION-GROUP-INTERVAL
- **Title:** `TestTimerContinuation_GroupWaitFireCreatesGroupIntervalTimer` флейкает в шаге `race`
- **Problem:** `go-app/internal/infrastructure/grouping/timer_continuation_regression_test.go:198` — ожидался таймер `group_interval`, прочитан `group_wait`: тест читает storage раньше, чем срабатывание записало continuation. Release-gate 2026-10-19 (ветка `feature/helm-single-node-defaults`, Go-код не менялся), шаг `race`; перезапуск шага — 7/7 ok, тест отдельно `-race -count=30` — 30/30.
- **Impact:** шаг `race` release-gate краснеет случайно.
- **Fix:** ждать опросом, пока тип записи станет `group_interval` (как предложено в `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` — тот же класс, чинить вместе).
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/release-gate.txt` (FAIL и перезапуск в конце файла).
- **Status:** open

### [medium][API][~0.5d] API-ALERTS-RECEIVER-INHIBITED-STATUS
- **Title:** `GET /api/v2/alerts` отдаёт `receivers: ["default"]` и `state: active` с пустым `inhibitedBy` для подавленного алерта
- **Problem:** два места.
  - **Receiver.** `toAPIAlert` (`go-app/internal/infrastructure/storage/memory/alert_store.go:460-473`) берёт receiver из метки `receiver` алерта, а при её отсутствии ставит `"default"`. Route tree не используется, хотя `/api/v2/alerts/groups` уже резолвит receiver через `alertGroupingResolver` (`internal/application/handlers/alerts.go:347`).
  - **Inhibition.** `alertconv.ToGettableAlert` (`internal/core/alertconv/alertconv.go:212`) всегда возвращает `InhibitedBy: []string{}`, а `state` считает только по silences. Результат inhibition (`alert_processor.go:587-595`) до API не доходит; ограничение описано в комментарии `handlers/alerts.go:173-177`. Поэтому `?inhibited=false` ничего не отфильтрует.
  - **Воспроизведение** на живом конфиге sema-prod (Secret `sema-monitoring/alertmanager-kube-prometheus-stack-alertmanager`, секреты заменены заглушками, Telegram/Slack — на `https://127.0.0.1`), бинарь `main` @ `9d41dc0`, `profile: lite`, `storage.backend: filesystem`:
    1. Запустить с `AMP_CONFIG_FILE=<копия конфига>`. Конфиг грузится: `routes=4 receivers=3`, `Inhibition subsystem initialized rules=3`.
    2. `POST /api/v2/alerts` с пятью алертами: `DiskFull` `severity=critical signal=panic`, `DiskFull` `severity=warning signal=error`, `HighLatency` `severity=warning signal=error` (у всех трёх `namespace=sema-prod service=api`), `Watchdog`, `Unmatched` без `signal`.
    3. Лог — правильно: `DiskFull/critical` → `panic-telegram` (`route.routes[1]`), `HighLatency` → `errors-slack` (`route.routes[2]`), `Watchdog` → `drop-unmatched`, `Unmatched` → `panic-telegram` (корень); `Alert inhibited by rule` — `DiskFull/warning` подавлен `DiskFull/critical` (правило `severity = critical` → `severity =~ warning|info`, `equal: [namespace, alertname]`).
    4. `GET /api/v2/alerts` — неправильно: у всех пяти `receivers: [{"name":"default"}]` (такого receiver'а в конфиге нет), у `DiskFull/warning` — `state: active`, `inhibitedBy: []`. Upstream вернул бы реальный receiver каждого алерта и `state: suppressed`, `inhibitedBy: [<fingerprint DiskFull/critical>]`.
- **Impact:** на доставку не влияет. Влияет на клиентов API: karma, Grafana Alertmanager datasource, `amtool alert query`. Подавленные алерты они показывают как горящие, фильтр по receiver'у (`?receiver=`) ничего не находит. При параллельном запуске рядом с Alertmanager в sema-prod картина в UI будет расходиться с Alertmanager. `/api/v2/alerts/groups` в этом прогоне не проверялся.
- **Fix:** receiver — через тот же резолвер, что и для groups (route tree, при `continue` — все совпавшие receiver'ы, см. `ROUTE-CONTINUE-NO-FANOUT`). Inhibition — хранить у алерта fingerprint'ы источников подавления (или спрашивать inhibition-кеш при выдаче) и заполнять `inhibitedBy`/`state: suppressed` в `ToGettableAlert`; то же для групп. Тест через `LoadConfig` + `POST`/`GET /api/v2/alerts`: route tree без метки `receiver` и пара source/target по inhibit-правилу.
- **Refs:** проверка 2026-10-21 на живом конфиге sema-prod (временный прогон, конфиг и логи не сохранены в репо: в конфиге креды).
- **Status:** open

<!-- Code review 2026-10-21 (`docs/06-planning/CODE-REVIEW-2026-10-21.md`, `main` @ `68d749f`). В Refs — ID находки из документа; там же — порядок этапов и зависимости. Пути — от `go-app/`. -->

### [low][Inhibition][~0.25d] INHIBIT-CACHE-RESOLVE-REFIRE-RACE
- **Title:** параллельные resolve и повторный firing одного fingerprint на одной реплике — источник снимается синком
- **Problem:** `RemoveAlert` (`internal/infrastructure/inhibition/cache.go`) делает `Delete`/`SRem` в Redis после удаления из L1. Если параллельно идёт `AddFiringAlert` того же fingerprint, его Set+SAdd могут попасть до `Delete`/`SRem` remove: в L1 лежит локальная запись с выставленным `redisConfirmedAt`, а в Redis её уже нет ⇒ следующий проход синка удаляет её как «пропавшую». Повторный tombstone верно пропускает локальную запись, но расхождение L1 и Redis остаётся.
- **Impact:** источник перестаёт подавлять на этой реплике до следующего resend (~1m у Prometheus, для webhook — до повтора). Реалистичный случай — HA-пара Prometheus с рассинхроном оценки (одна шлёт resolved, другая firing). Другие реплики согласованы (там победил resolve).
- **Fix:** в `RemoveAlert` после `Delete`/`SRem`, если в L1 локальная запись (`!fromSync`) с `writtenAt` позже первого tombstone, — повторно записать её в Redis (или сбросить ей `redisConfirmedAt`). Тест на `hookCache` из `cache_test.go`: add внутри `Delete` remove ⇒ после синка источник виден.
- **Refs:** `tasks/archive/INHIBIT-SOURCE-CACHE-EVICTION/review-findings.md` F8.
- **Status:** open

### [critical][Publishing][~0.25d] PROVIDER-RESOLVE-STATELESS-PD
- **Title:** resolve в PagerDuty теряется после рестарта, на другой реплике и через 24h
- **Problem:** trigger шлёт `DedupKey: alert.Fingerprint`, а `resolveEvent` (`internal/infrastructure/publishing/pagerduty_publisher_enhanced.go:127-138`) берёт ключ только из in-memory `EventKeyCache(24h)` (`publisher.go:383-388`) и при промахе возвращает `ErrEventNotTracked`. Кеш общий на фабрику: ключ может удалить и второй PD-таргет.
- **Impact:** после каждого rollout/рестарта, в HA и для инцидентов старше 24h инциденты PD не закрываются автоматически. В upstream dedup-ключ детерминирован.
- **Fix:** в resolve — `DedupKey: alert.Fingerprint`, кеш для PD убрать. Тест: resolve без предварительного trigger в том же процессе ⇒ запрос `event_action=resolve` с тем же `dedup_key`.
- **Refs:** CODE-REVIEW-2026-10-21 C-2.
- **Status:** open

### [high][Publishing][~0.5d] PROVIDER-RESOLVE-ROOTLY
- **Title:** resolve в Rootly молча пропадает без локального состояния
- **Problem:** `resolveIncident` (`internal/infrastructure/publishing/rootly_publisher_enhanced.go:196-202`) ищет ID инцидента в in-memory `IncidentIDCache(24h)`; при промахе возвращает `nil` — доставка считается успешной.
- **Impact:** как у `PROVIDER-RESOLVE-STATELESS-PD`, но без ошибки и метрики: инцидент Rootly остаётся открытым, а AMP считает resolve доставленным.
- **Fix:** детерминированный внешний ключ (lookup по label/title) или персистентное хранение ID в Redis рядом с nflog; при промахе — ошибка, а не `nil`. Делать после `PUBLISH-PER-TARGET-STATE` (ключ кеша).
- **Refs:** CODE-REVIEW-2026-10-21 C-2.
- **Status:** open

### [high][API][~0.5d] SILENCED-ALERTS-INGEST
- **Title:** firing-алерт под активным silence отбрасывается до ingest
- **Problem:** `handleAlertsPost` (`internal/application/handlers/alerts.go:431-436`) фильтрует `silences.HasActiveMatch` до `ProcessAlert`/`IngestBatch`. Контрактный тест `cmd/server/main_phase0_contract_test.go:1453` закрепляет это (0 алертов после POST заглушенного).
- **Impact:** алерт не виден в `GET /api/v2/alerts` со `status.state=suppressed` (karma, `amtool alert query -s`); не становится источником inhibition (в upstream становится); нет записи в истории.
- **Fix:** убрать фильтр из хендлера — подавление уже делает notify chain (`filterSilenced`, `grouping/manager_impl.go:1215`); переписать контрактный тест на «алерт есть, `suppressed`». Breaking для клиентов API — запись в CHANGELOG. Смежно `API-ALERTS-RECEIVER-INHIBITED-STATUS`.
- **Refs:** CODE-REVIEW-2026-10-21 H-1, D-4.
- **Status:** open

### [high][Config][~0.25d] DURATION-UPSTREAM-UNITS
- **Title:** длительности `d`/`w`/`y` в конфиге не парсятся
- **Problem:** `routing.Duration` (`internal/infrastructure/routing/global.go:486`) и `grouping.Duration` (`internal/infrastructure/grouping/config.go:125`) используют `time.ParseDuration`; upstream — `model.Duration`.
- **Impact:** немодифицированный upstream-конфиг с `repeat_interval: 1d` не загружается.
- **Fix:** один тип `Duration` на `model.ParseDuration` из `github.com/prometheus/common` (уже прямая зависимость); тест на `1d`, `1w`, `90s`, `0s`.
- **Refs:** CODE-REVIEW-2026-10-21 H-2, D-6.
- **Status:** open

### [high][Security][~0.25d] EMAIL-RENDER-ESCAPING
- **Title:** email fallback рендерит HTML без экранирования; Subject без санитизации CR/LF
- **Problem:**
  - `email_publisher_enhanced.go` (`internal/infrastructure/publishing/`, импорт `text/template`, `extractEmailConfig` `:159-163`, `renderTemplate` `:315-325`) рендерит HTML из `internal/notification/template/defaults/email.go:236-258` через `text/template`: `{{ .Annotations.description }}` вставляется как есть, `href="{{ .CommonAnnotations.runbook_url }}"` пропускает `javascript:`. Путь активен при `publishing.templates.enabled=false`, у email-target без шаблонных полей и при ошибке `RenderEmailContent`.
  - `email_client.go:282`: `Subject` в ASCII пишется без `sanitizeHeaderValue` (её применяют только к extra headers, `:304`). `hypothesis`: доходят ли CR/LF в лейблах через `/api/v2/alerts` — не проверено.
- **Impact:** тот, кто может прислать алерт, внедряет HTML/ссылки в письмо дежурному; возможна header injection через `alertname`.
- **Fix:** убрать fallback-путь, всегда рендерить через `business/templating` (`html/template`, `email.default.html`); минимум — `html/template` для HTML-части. `mime2047Subject(sanitizeHeaderValue(msg.Subject))`. Тесты: аннотация с `<script>`/`javascript:`, `alertname` с `\r\n`.
- **Refs:** CODE-REVIEW-2026-10-21 H-3, L-1.
- **Status:** open

### [high][Publishing][~0.5d] PUBLISH-PER-TARGET-STATE
- **Title:** кеши корреляции и SMTP-клиенты общие между таргетами
- **Problem:** `rootlyCache`, `pagerDutyCache`, `slackCache` создаются на фабрику (`internal/infrastructure/publishing/publisher.go:383-388`) и ключуются только fingerprint (`slack_publisher_enhanced.go:56,131`, `rootly_publisher_enhanced.go:71,117`). SMTP-клиент кешируется по `host:port` (`publisher.go:856-869`), хотя в `SMTPDialer` зашиты user/password/TLS/From (`email_client.go:126-161`).
- **Impact:** второй Slack-таргет отвечает в тред чужого канала; второй Rootly-аккаунт обновляет чужой инцидент; после reload с новым SMTP-паролем или `require_tls` работает старый клиент до рестарта; два таргета на одном SMTP-хосте шлют с кредами первого.
- **Fix:** ключ кеша `target.Name|fingerprint`; ключ SMTP-клиента `host|port|user|sha256(pass)|tls`. Тест на два таргета одного типа и reload SMTP-пароля.
- **Refs:** CODE-REVIEW-2026-10-21 H-4.
- **Status:** open

### [high][Config][~0.5d] RELOAD-SERIALIZE
- **Title:** reload конфига не сериализован и привязан к контексту HTTP-клиента
- **Problem:**
  - `ServiceRegistry.ReloadConfig` (`internal/application/service_registry.go:2654`) без мьютекса; SIGHUP (`cmd/server/main.go:298`), `POST /-/reload` (`handlers/status_api.go:105`) и sidecar запускают конвейер параллельно. Внутри — глобальный viper, `r.config = newConfig` без синхронизации; распределённый lock в `atomicApply` недостижим (`lockManager == nil`, `service_registry.go:337`); `rc.reloadVersion` читается без `mu` (`reload_coordinator.go:168,252`).
  - HTTP-reload и `RollbackAll` (`internal/config/update_reloader.go:262`) работают на `r.Context()`: обрыв клиента посреди Phase 5 оставляет незавершённый откат (W610). SIGHUP-путь уже использует свой контекст.
  - `external_url` захватывается при сборке роутера (`internal/application/handlers/alerts.go:57`) и reload его не обновляет.
- **Impact:** два reload применяются вперемешку, откат возвращает не тот снимок; `hypothesis`: `concurrent map writes` в viper. Обрыв клиента ⇒ split state.
- **Fix:** `reloadMu sync.Mutex` в `ReloadConfig`, для HTTP — `TryLock` ⇒ `409`; `context.WithoutCancel` + свой таймаут в `ReloadHandler` и `RollbackAll`; `external_url` читать из `registry.Config()` на запрос. Тест: два параллельных reload под `-race`, reload с отменённым клиентским ctx.
- **Refs:** CODE-REVIEW-2026-10-21 H-5, M-19, L-5.
- **Status:** open

### [high][Config][~0.5d] CONFIG-VALIDATION-FAIL-CLOSED
- **Title:** валидация конфига расходится между стартом и reload и пропускает ошибки
- **Problem:**
  - `DefaultConfigValidator` (`internal/config/update_validator.go:589-596`) работает только на reload, `Config.Validate` — только на старте (`config.go:1147`), правила разные. Lite + `app.environment=production` (дефолт чарта) без `DATABASE_PASSWORD`: старт проходит, каждый reload падает «database password should not be empty in production».
  - `validateAlertmanagerSubset` (`internal/config/alertmanager_validation.go:66-71,93-95`): ошибка `yaml.Unmarshal` ⇒ `return nil`, вся проверка configvalidator пропускается.
  - `parseMatchers` в tree builder (`internal/business/routing/tree_builder.go:274-278`) молча выбрасывает битый matcher («best-effort») ⇒ маршрут шире, особенно для `!=`/`!~`. `hypothesis`: конкретный вход, проходящий configvalidator и ломающий builder, не найден.
- **Impact:** на lite-prod hot reload не работает никогда (sidecar в бесконечном backoff); битый конфиг может отправлять алерты не тому ресиверу без единой ошибки.
- **Fix:** один валидатор на оба пути, проверки БД гейтить `RequiresPostgres()`; ошибка unmarshal — ошибка валидации; builder вызывает `matcher.Parse` и возвращает ошибку. Breaking: битые `matchers:` перестают загружаться — CHANGELOG.
- **Refs:** CODE-REVIEW-2026-10-21 H-6, M-20, D-7.
- **Status:** open

### [high][LLM][~0.25d] LLM-RETRY-CLASSIFY
- **Title:** ответы LLM 429/5xx не ретраятся, `max_retries: 0` превращается в 3
- **Problem:** `llm.HTTPError` (`internal/infrastructure/llm/errors.go:26`) не `httperror.HTTPAPIError`, поэтому `IsRetryable` (`pkg/httperror/error.go:445`) возвращает false — ретраятся только таймауты (худший случай 30s × 4 внутри `POST /api/v2/alerts`). `WithRetryFunc` (`internal/core/resilience/resilience.go:55-57`) подменяет `MaxRetries==0` на 3. Тело ответа LLM целиком уходит в ERROR-лог и в БД через `SaveError` (`llm/client.go:310-317,440,584`).
- **Impact:** максимальная латентность ingest при минимальной надёжности классификации; ретраи нельзя выключить; PII из промпта в логах.
- **Fix:** `errors.As(err, &he) && (he.StatusCode==429 || he.StatusCode>=500)`; различать «не задано» и `0`; `truncate(body, 512)`. Тест на httptest 503→200. Бюджет времени — в `PROD-LLM-ALERT-PATH-ISOLATION`.
- **Refs:** CODE-REVIEW-2026-10-21 H-7, L-8.
- **Status:** open

### [high][Investigation][~1d] INVESTIGATION-ASYNC-DEDUP
- **Title:** расследования без дедупа, синхронный INSERT в ingest-пути, вывод tools без лимитов
- **Problem:**
  - `Submit` (`internal/infrastructure/investigation/queue.go:122-160`) делает `repo.Create` на горутине запроса, без таймаута, хотя вызов из `alert_processor.go:787-788` подписан fire-and-forget.
  - Дедупа нет, а смена `EndsAt` на каждом resend считается апдейтом (`deduplication.go:352-357`) ⇒ новое расследование на каждый resend.
  - Вывод tools не ограничен: `kubernetes.go:185` (`io.Copy`, `tail_lines` от LLM), `loki.go:82,114`, `prometheus.go:107`, `agent_loop.go:204`.
- **Impact:** неограниченная стоимость LLM (~1 расследование/мин на горящий алерт); медленная БД задерживает ingest; OOM воркера и раздувание контекста.
- **Fix:** `Submit` — только неблокирующая отправка в канал, `Create` в воркере; дедуп по `fingerprint+StartsAt` с TTL (Redis `SETNX`); `io.LimitReader(256KiB)`, clamp `tail_lines`/`limit` ≤500, кэп символов перед записью в history.
- **Refs:** CODE-REVIEW-2026-10-21 H-8, M-10 (tools). Дедуп запланирован и в `PHASE-5A-INVESTIGATION-PIPELINE`.
- **Status:** open

### [high][Security][~1d] INVESTIGATION-PROMPT-ISOLATION
- **Title:** недоверенные данные алерта в system-промпте, investigation tools без allowlist namespace
- **Problem:**
  - `buildOpenAIMessages` (`internal/infrastructure/llm/investigate_with_tools.go:165`) кладёт JSON алерта (все labels/annotations) в роль `system`; классификация — так же (`llm/client.go:385-394`): инъекция в аннотацию может выставить `severity=noise`, после чего `filter_engine` дропнет алерт.
  - Kubernetes tool (`internal/infrastructure/investigation/tools/kubernetes.go:165`) читает логи/поды/события в любом namespace по выбору LLM; вывод уходит внешнему провайдеру и в БД.
  - `database_query` ходит в пул самого AMP (`service_registry.go:2226`) ⇒ неверные выводы по алертам на чужие БД, утечка `pg_stat_activity` AMP провайдеру.
  - `InvestigateAlert`/`InvestigateWithTools` (`llm/client.go:521`) игнорируют `provider` и circuit breaker.
- **Impact:** атакующий с правом слать алерты управляет агентом и вытягивает логи чужих namespace во внешний LLM.
- **Fix:** данные алерта — в `user`-сообщении в явном разделителе с инструкцией «это данные»; `investigation.tools.kubernetes.allowed_namespaces`, по умолчанию — namespace из лейбла алерта; редактирование секретов в выводе tools; отдельный DSN для `database_query`, без него tool не регистрировать; один `doChat` с CB и лимитом тела.
- **Refs:** CODE-REVIEW-2026-10-21 H-9, L-7, L-9. Смежно `PROD-LLM-ALERT-PATH-ISOLATION`, `INVESTIGATION-K8S-TOOL-HELM`.
- **Status:** open

### [high][Test][~1d] SILENCE-REPO-INTEGRATION-TESTS
- **Title:** тесты `PostgresSilenceRepository` безусловно пропускаются
- **Problem:** `internal/infrastructure/silencing/postgres_silence_repository_test.go` (30 `t.Skip`), `bulk_ops_test.go` (7), `ttl_test.go` (6) — «Requires database connection»; `NewPostgresSilenceRepository` не вызывается ни в одном тесте.
- **Impact:** SQL хранения silences в HA не проверяется ничем, кроме e2e.
- **Fix:** testcontainers-сьют по образцу `repository/postgres_history_test.go`: CRUD, фильтры `filter_builder`, TTL/cleanup, bulk; безусловные `t.Skip` удалить.
- **Refs:** CODE-REVIEW-2026-10-21 H-11.
- **Status:** open

### [medium][Grouping][~1d] GROUP-STORE-LOST-UPDATE
- **Title:** конкурентное обновление существующей группы в Redis теряет алерт молча
- **Problem:** `AddAlertToGroup` (`internal/infrastructure/grouping/manager_impl.go:298-305`) делает `Load → mutate → Store`; при конкурентном апдейте `RedisGroupStorage.Store` (`redis_group_storage.go:204`) возвращает `ErrVersionMismatch`/`TxFailedErr`, ошибка только логируется, вызывающему возвращается незаписанный `Clone()`. `group.Version++` выполняется до EXEC. `MemoryGroupStorage` версию не проверяет вовсе.
- **Impact:** алерт или resolve пропадает из группы до следующего resend; memory и Redis backend ведут себя по-разному.
- **Fix:** retry с повторным `Load` (N попыток, затем ошибка); локальная сериализация по ключу через `groupPublishLocks` (`publish_lock.go:72`) — заодно закрывает `GROUPING-NEW-GROUP-CREATE-RACE`; `Version++` на копии после EXEC; общий contract-тест для двух storage.
- **Refs:** CODE-REVIEW-2026-10-21 M-1, D-13. Смежно `REDIS-GROUP-STORAGE-UNLOCKED-WRITES` (TECH-DEBT), `GROUPING-NEW-GROUP-CREATE-RACE`.
- **Status:** open

### [medium][Silencing][~0.25d] SILENCE-VALIDATION-PARITY
- **Title:** валидация silence расходится с upstream: отклоняет пустые значения, принимает match-all
- **Problem:** три копии валидации (`internal/application/handlers/silence_convert.go:79`, `internal/infrastructure/storage/memory/silence_store.go:457`, `internal/core/silencing/validator.go:76`) требуют непустое значение каждого matcher'а. Upstream требует, чтобы хотя бы один matcher не матчил пустую строку.
- **Impact:** отклоняются валидные `env=""`, `env!=""`; принимается `alertname=~".*"`, который глушит всё.
- **Fix:** один валидатор в `core/silencing` по правилу upstream, обе точки входа вызывают его; парсинг StartsAt/EndsAt и regex-проверка — там же. Breaking (match-all отклоняется) — CHANGELOG.
- **Refs:** CODE-REVIEW-2026-10-21 M-2, D-3.
- **Status:** open

### [medium][Storage][~1d] STORE-RETENTION-GC
- **Title:** alert store и silence store растут неограниченно
- **Problem:**
  - `AlertStore.all` (`internal/infrastructure/storage/memory/alert_store.go:113-193`) никогда не чистится: resolved остаются навсегда, каждый новый `startsAt` — новый ключ.
  - `SilenceStore` не удаляет истёкшие silences (`silence_store.go`); `HasActiveMatch`/`ActiveMatchingSilenceIDs` (`:287-303,365-385`) проходят все silences, аллоцируя matcher'ы на каждый; глобальный `RegexCache` (`core/silencing/matcher_cache.go`) сбрасывается целиком на 1000 записях под write-lock.
- **Impact:** память, время `List`/`Stats`/`filterSilenced` и размер снапшота растут с аптаймом.
- **Fix:** периодический GC resolved с `EndsAt < now - retention`; retention GC silences (upstream 120h); компиляция matcher'ов при сохранении в `StoredSilenceState`; gauges размеров store. Soak-тест с ротацией алертов.
- **Refs:** CODE-REVIEW-2026-10-21 M-3, M-4. Смежно `ALERT-STORE-DEDUP-KEY-STARTSAT`.
- **Status:** open

### [medium][Inhibition][~0.5d] INHIBIT-STATE-LIFECYCLE
- **Title:** состояния inhibition не истекают, cleanup worker не запущен
- **Problem:** `inhibition/state_manager.go:120-260`: state удаляется только при resolve источника, полным сканом `GetActiveInhibitions` (`alert_processor.go:830-850`); resolve цели или исчезновение источника без resolve оставляют state навсегда; `ExpiresAt` всегда nil; `StartCleanupWorker` не вызывается; `RecordInhibition` на каждом resend — Redis SET с TTL 24h и O(n) `countActiveStates` (`alert_processor.go:600`).
- **Impact:** неограниченный рост `sync.Map` и Redis-ключей; лишняя O(n) работа в ingest-пути.
- **Fix:** удалять state на resolve цели; `ExpiresAt = source.EndsAt`; обратный индекс `source → targets`; gauge через `GaugeFunc`; запустить cleanup worker или удалить.
- **Refs:** CODE-REVIEW-2026-10-21 M-5.
- **Status:** open

### [medium][Publishing][~0.5d] QUEUE-GRACEFUL-STOP
- **Title:** остановка очереди публикации теряет задания и может паниковать
- **Problem:** `PublishingQueue.Stop` (`internal/infrastructure/publishing/queue.go:570-600`) закрывает три канала; воркер (`:815-850`) выходит, получив `nil` из закрытого high-канала, бросая medium/low без сигнала completion. `submitJob` (`:764`) после `Stop` и повторный `Stop` — panic. Воркер без `recover` (`:802`); источник паники — непроверенные type assertions (`rootly_publisher_enhanced.go:89-91`).
- **Impact:** graceful stop теряет задания; поздний `Submit` или паника паблишера роняет процесс.
- **Fix:** atomic `closing` + отказ в `submitJob`, drain всех каналов, `close` под `sync.Once`; `recover` в `processJob` с переводом паники в ошибку задания. Тест: `Stop` с заданиями во всех приоритетах; `Submit` после `Stop`.
- **Refs:** CODE-REVIEW-2026-10-21 M-7, L-19.
- **Status:** open

### [medium][Publishing][~1.5d] PUBLISH-SINGLE-RETRY-LAYER
- **Title:** вложенные ретраи в publishing блокируют воркер и открывают breaker здоровым таргетам
- **Problem:** клиенты (`slack_client.go:217-311`, `telegram_client.go:319-418`, `pagerduty_client.go:379-472`, `rootly_client.go:303-364`, `webhook_client.go:142-267`) делают 3–4 попытки, очередь (`queue.go:1401`, `retryPublish`) — ещё `maxRetries+1`: до 16 HTTP на задание. `time.Sleep` без ctx (`slack:247`, `telegram:345`, `pagerduty:427,460`) — `Abandon` не прерывает backoff; `defer Body.Close()` внутри цикла (`slack:254`, `telegram:352`); `Retry-After` не ограничен (`slack:288`, `webhook:241-244`).
- **Impact:** медленный или rate-limited таргет превышает `DeliveryConfirmationTimeout` (45s), abandon считается отказом для breaker'а ⇒ здоровый таргет может открыть breaker. `hypothesis`: группа из 45+ алертов в Slack не укладывается в 45s.
- **Fix:** клиенты делают одну попытку и возвращают классифицированную ошибку (`retryable`, `Retry-After`); ретраит только очередь через `pkg/retry` с ctx-aware backoff; cap `Retry-After` 30s.
- **Refs:** CODE-REVIEW-2026-10-21 M-8, D-8.
- **Status:** open

### [medium][Publishing][~0.25d] CIRCUIT-BREAKER-HALF-OPEN
- **Title:** circuit breaker publishing не переходит в half-open — после timeout пропускает весь поток
- **Problem:** `CanAttempt` (`internal/infrastructure/publishing/circuit_breaker.go:69-84`) по истечении timeout возвращает true всем вызывающим, но состояние в HalfOpen не переводит; `State()` показывает `open`.
- **Impact:** вместо одной пробы на таргет идёт весь поток заданий; состояние в метриках неверно.
- **Fix:** под write-lock переход Open→HalfOpen с одним пробным слотом (`probeInFlight`); тест на конкурентные `CanAttempt` после timeout.
- **Refs:** CODE-REVIEW-2026-10-21 M-11.
- **Status:** open

### [medium][Security][~0.5d] PUBLISH-SECRETS-IN-LOGS
- **Title:** токены из URL таргетов и тела ответов попадают в логи; ответы читаются без лимита
- **Problem:**
  - `"HTTP request failed: %w"` с `*url.Error` и полным URL (`internal/infrastructure/publishing/publisher.go:118-120,176-178`): токены Teams/Discord/Google Chat/Telegram-fallback уходят в лог `Failed to publish after retries`. Тело ответа целиком копируется в текст ошибки (`publisher.go:125-130`). Сырой `target.URL` на DEBUG (`business/publishing/discovery_impl.go:479-483`) и INFO (`publisher.go:801-803`).
  - `io.ReadAll` без лимита: `publisher.go:125,182`, `slack_client.go:257`, `telegram_client.go:296,355`, `pagerduty_client.go:529`, `rootly_client.go:380`, `webhook_client.go:198`, `llm/client.go:282`.
  - Ошибка `weak database password detected: '%s'` (`internal/config/config.go:1468`) печатает пароль (из словаря слабых).
- **Impact:** секреты в логах и агрегаторе; гигантский ответ таргета раздувает память воркера.
- **Fix:** маскировать URL (маскер из `webhook_auth.go:153` перенести в общий хелпер); тело в ошибках ≤512 байт; общий `readLimited(r, 64<<10)`; убрать значение из ошибки пароля. Тест по образцу `discovery_log_redaction_test.go`.
- **Refs:** CODE-REVIEW-2026-10-21 M-9, M-10, L-2, D-9. Смежно `CONFIG-VALIDATION-ERROR-REDACTION`.
- **Status:** open

### [medium][Publishing][~0.5d] PUBLISH-HEALTH-PROBES
- **Title:** email-таргет всегда unhealthy; метрики refresh не скрейпятся
- **Problem:** health monitor шлёт HTTP GET на каждый таргет (`internal/business/publishing/health_checker.go:107-120`); для `smtp://host:port` (`config_targets.go:669`) — всегда `unsupported protocol scheme`. `hypothesis`: GET на Slack webhook / PD `/v2/enqueue` тоже не 2xx. Метрики refresh регистрируются во временном `prometheus.NewRegistry()` (`internal/application/publishing_runtime.go:210`, `refresh_manager_impl.go:121`).
- **Impact:** ложные unhealthy-статусы в API/дашборде; refresh не наблюдаем.
- **Fix:** email — пропускать или TCP-probe; API-типы — TCP-only probe; refresh-метрики — `v2.Global().Publishing`.
- **Refs:** CODE-REVIEW-2026-10-21 M-12, M-13.
- **Status:** open

### [medium][API][~0.5d] API-V2-ERROR-SHAPE
- **Title:** ошибки API v2 в форме `{"error":…}` вместо JSON-строки; 405 без `Allow`; любая ошибка чтения тела — 413
- **Problem:** `writeJSON(..., map[string]string{"error": ...})` в `internal/application/handlers/alerts.go:76,309,425`, `silences.go:95,122,135,144`, `status.go:51`; upstream swagger описывает 400/500 как `type: string`. Проверка метода inline (`alerts.go:68`, `silences.go:46,73`, `status_api.go:101`, `investigation.go:19`, `inhibitions.go:29`) отдаёт 405 без `Allow`, в отличие от `method_guard.go`. В `handleAlertsPost`/`handleSilencePost` любая ошибка `io.ReadAll` (обрыв клиента) ⇒ 413.
- **Impact:** `hypothesis`: клиенты, сгенерированные по swagger (amtool), на 400 получают ошибку декодирования вместо причины.
- **Fix:** `writeV2Error(w, code, msg)` с `json.Marshal(msg)`; `allowMethods(...)` по образцу `getOnly`; `errors.As(err, *http.MaxBytesError)` ⇒ 413, остальное 400. Строка в `ALERTMANAGER_COMPATIBILITY.md`.
- **Refs:** CODE-REVIEW-2026-10-21 M-15, L-6.
- **Status:** open

### [medium][API][~0.25d] API-FILTER-MATCHER-GRAMMAR
- **Title:** `?filter=` в API принимает другую грамматику matcher'ов, чем конфиг и upstream
- **Problem:** `internal/application/handlers/matchers.go:28` требует кавычки (`"(.*)"`), не допускает пробелов и не снимает экранирование; парсер маршрутов (`business/routing/tree_builder.go:237,411`) и `pkg/configvalidator/matcher` принимают обе формы.
- **Impact:** `?filter=alertname=Foo` ⇒ 400, тот же matcher в `route.matchers` работает; upstream принимает обе формы.
- **Fix:** использовать общий парсер (`matcher.Parse`, затем `pkg/labelmatch` из `LABELMATCH-PKG`), регекс в handlers удалить.
- **Refs:** CODE-REVIEW-2026-10-21 M-16, D-2.
- **Status:** open

### [medium][API][~0.5d] INGEST-PARSE-VALIDATE
- **Title:** валидация входящих алертов зависит от `severity`, 400 без причины
- **Problem:** `parseAlertsForProcessing` (`internal/application/handlers/alerts.go:482-503`) сначала разбирает Prometheus-парсером, его валидатор требует `severity ∈ {critical,warning,info,debug}` (`webhook/validator.go:186-196,554`); при ошибке молча fallback на legacy-парсер, который проверяет только `alertname`, не валидирует имена меток и `generatorURL`, теряет `activeAt`/`value`. Наружу — общее «invalid alert payload».
- **Impact:** итоговая валидация зависит от значения `severity`; диагностика 400 бесполезна. Upstream валидирует имена/значения меток и `startsAt<=endsAt`, `alertname` не требует.
- **Fix:** выбор парсера по формату (детектор), один валидатор по смыслу upstream `PostableAlert.Validate`, ограничение по severity из ingest убрать, возвращать первую содержательную ошибку.
- **Refs:** CODE-REVIEW-2026-10-21 M-17.
- **Status:** open

### [medium][UI][~0.25d] UI-ROUTE-PREFIX-LINKS
- **Title:** legacy-дашборд под `route_prefix` без CSS и с битой навигацией
- **Problem:** абсолютные `href="/static/..."`, `href="/dashboard/..."` в `cmd/server/templates/legacy/shared.html:9,16-22`, `dashboard-overview.html:49-52`; `WithRoutePrefix` отдаёт 404 на всё вне префикса. `route_prefix_integration_test.go:117` проверяет только статус страницы.
- **Impact:** при `--web.route-prefix` или `external_url` с путём дашборд неработоспособен.
- **Fix:** `RoutePrefix` в `legacyDashboardPageData`, `href="{{.RoutePrefix}}/static/..."` (или относительные ссылки); проверка ссылок в тесте.
- **Refs:** CODE-REVIEW-2026-10-21 M-18.
- **Status:** open

### [medium][Config][~0.25d] CONFIG-ENV-KEYS-UNBOUND
- **Title:** env молча игнорируется для ключей без default/`BindEnv`
- **Problem:** `AutomaticEnv` (`internal/config/config.go:671-672`, признано комментарием у `:978-982`) резолвит только ключи, известные viper. Без default и `BindEnv`: `database.url`, `telemetry.insecure`, `telemetry.ca_cert_file`, `llm.agent_mode`, `inhibition.config_file`, `publishing.templates.render_timeout`, `publishing.templates.max_output_bytes` (сверено по списку из 162 ключей).
- **Impact:** оператор задаёт `LLM_AGENT_MODE=true` — режим не меняется, без ошибки.
- **Fix:** bind всех mapstructure-путей (reflection или `viper.ExperimentalBindStruct()`), тест, сверяющий набор ключей структуры и bound-ключей. Корень общий с `CONFIG-GLOBAL-VIPER-STATE` — делать вместе.
- **Refs:** CODE-REVIEW-2026-10-21 M-22.
- **Status:** open

### [medium][Lite][~0.25d] SQLITE-LITE-HARDENING
- **Title:** в lite-профиле `foreign_keys` включён на одном соединении, нет `busy_timeout`
- **Problem:** `PRAGMA foreign_keys = ON` и `journal_mode = WAL` выполняются через `db.ExecContext` на пуле (`internal/infrastructure/sqlite_adapter.go:57-85`) — действуют на одно соединение; `MaxOpenConns` берётся из настроек Postgres (`service_registry.go:1148-1154`); `busy_timeout` не задан.
- **Impact:** `ON DELETE CASCADE` срабатывает недетерминированно; `SQLITE_BUSY` при конкурентной записи.
- **Fix:** pragma в DSN (`_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`, modernc поддерживает); `SetMaxOpenConns(1)` для писателя.
- **Refs:** CODE-REVIEW-2026-10-21 M-27.
- **Status:** open

### [medium][Gate][~0.25d] QUALITY-GATES-ALIGN
- **Title:** `make quality-gates` даёт ложно-зелёный результат
- **Problem:** `go-app/Makefile:21,80-87`: цель гоняет только `./cmd/server/... ./internal/ui` без lint и `-race`; `go fmt` переписывает файлы вместо проверки. Настоящий гейт — `scripts/release-gate.sh`.
- **Impact:** глобальные правила агентов предписывают `make quality-gates` — гейт проходит при красном release-gate.
- **Fix:** `quality-gates: ; ../scripts/release-gate.sh`; fmt-проверка через `gofmt -l` с ненулевым кодом выхода; `quality-gates-fast` оставить как есть.
- **Refs:** CODE-REVIEW-2026-10-21 M-28. Поглощает остаток `QUALITY-GATES-DIRTIES-TREE` (BACKLOG); смежно `PARITY-GATE-DOES-NOT-GATE`.
- **Status:** open

### [low][Security][~0.25d] SERVER-HARDENING
- **Title:** HTTP-сервер без `ReadHeaderTimeout`, ранний SIGHUP убивает процесс, bcrypt под глобальным мьютексом
- **Problem:**
  - `cmd/server/main.go:149-155`: нет `ReadHeaderTimeout`/`MaxHeaderBytes` (gosec G112).
  - `signal.Notify(SIGHUP)` (`:166-180`) после `registry.Initialize` — SIGHUP во время долгого старта завершает процесс действием по умолчанию.
  - Ошибка bind в `ListenAndServe` (`:194-197`) ⇒ `os.Exit(1)` без `registry.Shutdown`.
  - `internal/application/webauth.go:155-156`: все bcrypt-сравнения под одним `bcryptMu`, неверные пароли кешируются ⇒ поток неверных Basic-auth выстраивает в очередь проверки легитимных клиентов.
- **Impact:** slowloris в пределах 30s; падение при раннем reload; DoS auth без кредов.
- **Fix:** `ReadHeaderTimeout: 10s`; Notify до `Initialize`; `net.Listen` до `Initialize`; семафор `GOMAXPROCS` вместо мьютекса и отдельный лимит на отказы.
- **Refs:** CODE-REVIEW-2026-10-21 L-3, L-4.
- **Status:** open

### [low][Grouping][~0.25d] REDIS-GROUP-INDEX-GHOSTS
- **Title:** ZSET-индекс групп в Redis копит удалённые по TTL группы
- **Problem:** `redis_group_storage.go:222,385,419-590`: после истечения ключа группы (TTL 24h) член `group:index` остаётся; timer storage такие члены лениво чистит (`redis_timer_storage.go:452`), group storage — нет.
- **Impact:** `Size()` (ZCARD) завышен, `LoadAll` считает призраков ошибками на каждом restore, индекс растёт.
- **Fix:** `ZRem` на not-found в `LoadAll`/`ListKeys`; общий helper с timer storage (`REDIS-HELPERS`).
- **Refs:** CODE-REVIEW-2026-10-21 L-12, D-12.
- **Status:** open

### [low][Grouping][~0.1d] LOCK-ACQUIRE-SLEEP
- **Title:** `Acquire` распределённого lock делает 4 попытки со `time.Sleep` без ctx
- **Problem:** `lock/distributed.go:86-125`: `Acquire()` = `AcquireWithRetry(ctx, 0)` ⇒ 3 ретрая по умолчанию; `time.Sleep` игнорирует ctx; `defer cancel()` внутри цикла; таймаут попытки = TTL, а не `AcquireTimeout`; успех логируется на INFO.
- **Impact:** follower делает 4 SETNX на poll, `Stop` задерживается.
- **Fix:** одна попытка SETNX на `Acquire`, ctx-aware sleep, `cancel()` на итерацию, лог на DEBUG.
- **Refs:** CODE-REVIEW-2026-10-21 L-14.
- **Status:** open

### [low][Cluster][~0.25d] CLUSTER-HEARTBEAT-STATE
- **Title:** heartbeat считает себя зарегистрированным после неудачных refresh; `Peers` сканирует весь keyspace
- **Problem:** `cluster/heartbeat.go:174`: `registered` остаётся true, даже когда ключ уже истёк; `Peers` (`:244`) делает SCAN по всему keyspace (группы, таймеры, nflog) на каждый status.
- **Impact:** неверный статус кластера в `/api/v2/status`; нагрузка на Redis растёт с числом групп.
- **Fix:** `registered=false` на неудаче refresh; peers в ZSET со score=expiry (`ZADD` + `ZRANGEBYSCORE now +inf`).
- **Refs:** CODE-REVIEW-2026-10-21 L-15.
- **Status:** open

### [low][Publishing][~0.5d] PUBLISH-RELOAD-CLIENT-LIFECYCLE
- **Title:** reload копит HTTP-клиенты и breaker'ы; первые таргеты после reload до 5s отбрасываются
- **Problem:** client maps фабрики (`publisher.go:342-361`) и `circuitBreakers` (`queue.go:1171-1206`) никогда не чистятся — каждый reload с новым ключом/URL добавляет клиента со своим `Transport`. Reload не вызывает `CheckModeTransition` (`service_registry_reload.go:184`; тикер 5s в `mode_manager.go:281`).
- **Impact:** рост idle-соединений и памяти с числом reload; если reload добавляет первые таргеты, прямой путь теряет алерты до 5s (групповой ретраит).
- **Fix:** GC по живому набору таргетов в `applyConfigTargets`; `CheckModeTransition()` после него.
- **Refs:** CODE-REVIEW-2026-10-21 L-17, L-18. Смежно `FU-HTTP-FILE-ROTATION`.
- **Status:** open

### [low][Publishing][~0.25d] PUBLISH-TRANSPORT-CONSISTENCY
- **Title:** приоритет источников ключа PD и поведение прокси зависят от пути
- **Problem:** `publisher.go:592-606` — `Authorization` перекрывает `routing_key`, `pagerduty_publisher_enhanced.go:215-231` — побеждает `routing_key`; Bearer-strip повторяется (`publisher.go:525,729`). Свои `http.Transport` без `Proxy` в `slack_client.go:89`, `telegram_client.go:200`, `pagerduty_client.go:172`, `rootly_client.go:104`, тогда как базовый путь через `DefaultTransport` учитывает `HTTPS_PROXY`.
- **Impact:** один и тот же таргет может уйти с разным ключом и через разный сетевой путь в зависимости от типа.
- **Fix:** одна `credentialFromHeaders(target, keys...)`; явная политика прокси (upstream env-прокси по умолчанию не использует), одинаковая для всех клиентов.
- **Refs:** CODE-REVIEW-2026-10-21 L-20, L-21, D-15.
- **Status:** open

### [low][Config][~0.25d] FILE-SECRET-PATH-BASE
- **Title:** `*_file`-секреты читаются относительно CWD и без лимита размера
- **Problem:** `internal/infrastructure/routing/file_secrets.go:207`; при этом `http_config/*_file` перебазируются на каталог конфига (`http_config_filepaths.go:62`). `hypothesis`: делает ли upstream rebase для этих полей — не проверено.
- **Impact:** относительный `api_url_file` работает иначе, чем `ca_file`; путь `/dev/zero` ⇒ OOM на reload (конфиг доверенный, не path traversal).
- **Fix:** `rebasePath` до чтения (параметр `baseDir` у `Parse`), `io.LimitReader(64KiB)`.
- **Refs:** CODE-REVIEW-2026-10-21 L-22. Смежно `FILE-SECRET-READ-ERROR-HINT`.
- **Status:** open

### [low][Security][~0.1d] W300-DELIVERABLE-SECRETS
- **Title:** предупреждение W300 «secret hardcoded» не проверяет секреты доставляемых интеграций
- **Problem:** `pkg/configvalidator/validators/security.go:73-84` проверяет OpsGenie/VictorOps/WeChat (AMP их не доставляет) и пропускает PD `routing_key`, Slack `api_url`, Telegram `bot_token`, `http_config.basic_auth.password`, `bearer_token`.
- **Impact:** предупреждение молчит ровно там, где секрет реально используется.
- **Fix:** добавить перечисленные поля; тест на каждое.
- **Refs:** CODE-REVIEW-2026-10-21 L-25.
- **Status:** open

### [low][Cache][~0.1d] REDIS-FLUSH-ALL
- **Title:** `RedisCache.Flush` стирает все DB инстанса
- **Problem:** `internal/infrastructure/cache/redis.go:367-378` вызывает `FlushAll`, а не `FlushDB`; метод в интерфейсе `Cache`, prod-вызовов нет. `NewRedisCache` игнорирует ctx (`:59`).
- **Impact:** сейчас нет; на shared Redis первый же вызов сотрёт чужие данные.
- **Fix:** `FlushDB` или убрать метод из интерфейса; прокинуть ctx в `NewRedisCache`/`dialRedis` (`AMP-CACHE-NO-RECONNECT` закрыт без этого: ожидание на старте прерывается своим ctx между попытками).
- **Refs:** CODE-REVIEW-2026-10-21 L-26.
- **Status:** open

### [low][K8s][~0.25d] K8S-CLIENT-HEALTH-TIMEOUT
- **Title:** k8s-клиент: таймаут health не применяется, список Secret'ов молча обрезается, гонка в `Close`
- **Problem:** `internal/infrastructure/k8s/client.go`: `Health` (`:225-240`) создаёт `healthCtx`, но `Discovery().ServerVersion()` его не принимает; `ListSecrets` (`:150-158`) режет на 1000 элементов с WARN; `Close` обнуляет `clientset` под `mu`, читатели берут без блокировки.
- **Impact:** зависший API-сервер вешает health; discovery теряет таргеты после 1000 Secret'ов; nil-разыменование при shutdown.
- **Fix:** `RESTClient().Get().AbsPath("/version").Do(healthCtx)`; пагинация через `Continue`; удалить `Close` или синхронизировать читателей.
- **Refs:** CODE-REVIEW-2026-10-21 L-27.
- **Status:** open

## Entry Format

```markdown
### [priority][area][estimate] BUG-SLUG
- **Title:** short title
- **Problem:** what is broken, with reproduction
- **Impact:** who is affected and how
- **Fix:** likely direction
- **Refs:** code, issue, task, or review links
- **Status:** open | in-progress | blocked
```
