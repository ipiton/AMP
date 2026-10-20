# BUGS

Код, нарушающий свой контракт (граница с долгом — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt). Исправленный баг удаляется отсюда; отчёт — в `DONE.md`.

## Open

<!-- Записи в виде списка (`- [ ] **SLUG** — ...`) — формат до Solo Kanban 1.1. Переводятся в Entry Format при взятии в работу. -->

### [high][Publishing][~0.25d] SLACK-WEBHOOK-PLAIN-OK-RESPONSE
- **Title:** успешная доставка в Slack Incoming Webhook считается ошибкой, группа переотправляется на каждом `group_interval`
- **Problem:** `HTTPSlackWebhookClient` (`go-app/internal/infrastructure/publishing/slack_client.go:263-267`) на HTTP 200 разбирает тело как JSON `SlackResponse{ok,...}` — так отвечает API `chat.postMessage`. Incoming Webhook (`hooks.slack.com/services/...`, путь `slack_api_url`/`api_url`) отвечает телом `ok` в `text/plain`, поэтому `json.Unmarshal` падает: `failed to parse response: invalid character 'o'` → `Non-retryable error` → `Group publishing delivery not confirmed ... no dedup entry will be recorded`. Тесты (`queue_target_publisher_test.go:388`) мокают Slack JSON-ответом и этого не ловят.
- **Impact:** сообщение в канал фактически доставлено, но nflog его не записывает: каждая горящая группа уходит повторно на каждом `group_interval` (5m) вместо `repeat_interval`, метрики доставки показывают ошибки. Блокирует параллельный запуск рядом с Alertmanager в sema-prod (`errors-slack`, канал `#sema`). Воспроизведено 2026-10-20 на бинаре `main` с конфигом sema и мок-приёмником, отвечающим как Slack: повтор в 11:54:09 после первой отправки в 11:49:09.
- **Fix:** на 2xx принимать тело `ok` (и пустое) как успех; JSON разбирать, только если он есть; не-2xx с текстовым телом (`invalid_payload`, `no_service`, …) — ошибка с этим текстом. Тест на `text/plain` `ok` и на текстовую ошибку; мок в существующих тестах привести к реальному ответу вебхука.
- **Refs:** `slack_client.go:263`; upstream `notify/slack` проверяет статус, а тело вебхука не разбирает как JSON.
- **Status:** open

### [medium][Config][~0.5d] TEMPLATED-FIELDS-NOT-RENDERED
- **Title:** `email_configs[].to` и `slack_configs[].actions[]` upstream шаблонизирует, AMP не рендерит и при загрузке валидирует как литерал
- **Problem:** `EmailConfig.To` — `validate:"required,email"` (`go-app/internal/infrastructure/routing/receiver.go`), в target уходит как есть (`config_targets.go:645`, заголовок `to`). Upstream рендерит `to` шаблоном и принимает список адресов через запятую — у AMP не грузится ни `to: '{{ .CommonLabels.owner }}@example.com'`, ни `to: 'a@example.com, b@example.com'`. `SlackAction` (`text`/`url`/`style`, теги `required`/`url`/`oneof`) — upstream шаблонизирует все поля action, AMP actions не отправляет вовсе (`slackTarget`: «not expressible»), но шаблонный `url` валит загрузку.
- **Impact:** upstream-конфиг с несколькими получателями письма или шаблонным `to` не загружается; с шаблонной Slack-кнопкой — тоже, хотя кнопка всё равно потерялась бы.
- **Fix:** `to` — разрешить список адресов (`net/mail.ParseAddressList`) и шаблон (`template_expr|…`, как в SLACK-COLOR-TEMPLATE-REJECTED), рендерить `to` в `emailTemplateFields` и брать отрендеренный адрес в email publisher. `actions` — `template_expr|…` на полях, пока actions не отправляются; рендер — вместе с поддержкой actions. Просто ослабить валидацию `to` без рендера нельзя: письмо уйдёт на литерал `{{ … }}`.
- **Refs:** найдено при SLACK-COLOR-TEMPLATE-REJECTED (2026-10-20), аудит валидаторов шаблонных полей.
- **Status:** open

### [medium][Routing][~0.25d] ROUTE-EXPLICIT-ZERO-GROUP-WAIT
- **Title:** явный `group_wait: 0s` у route трактуется как «не задан» и наследуется от родителя
- **Problem:** `TreeBuilder.inheritDuration` (`go-app/internal/business/routing/tree_builder.go:514`) берёт значение route только при `routeValue > 0`, иначе — родителя; `validateDurations` (`internal/business/routing/tree.go:498`) вдобавок считает `group_wait <= 0` ошибкой. В upstream `0s` — валидное «отправить сразу».
- **Impact:** route `signal="panic"` с `group_wait: 0s` под корнем с `group_wait: 10s` (sema-prod) нотифицирует через 10s, а не сразу. Родственно `ROUTE-TIMINGS-NOT-INHERITED` (тот же механизм наследования), но отдельный случай: там лишние дефолты, здесь потерянный явный ноль.
- **Fix:** различать «не задан» и «задан 0» (указатель/флаг из парсера), разрешить `group_wait: 0`; тест на route с `0s` под корнем с ненулевым `group_wait`. Удобно чинить вместе с `ROUTE-TIMINGS-NOT-INHERITED`.
- **Refs:** проверка 2026-10-20 на конфиге sema: PostgresDown (`group_wait: 0s`) ушёл через 10s вместе с алертом корневого route.
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

### [high][Routing][~0.5d] ROUTE-TIMINGS-NOT-INHERITED
- **Title:** вложенный route не наследует `group_wait`/`group_interval`/`repeat_interval` родителя
- **Problem:** `applyRouteDefaults` (`go-app/internal/infrastructure/grouping/parser.go`) рекурсивно вызывает `Route.Defaults()` и проставляет 30s/5m/4h каждому вложенному route без своих таймингов. `TreeBuilder.inheritDuration` (`internal/business/routing/tree_builder.go`) наследует от родителя, только если у route значение не задано, а после парсера оно задано всегда. Воспроизведено на бинаре: корень `group_wait: 3s`, `group_interval: 10s`; дочерний route без таймингов → `"Alert routed to group" ... "group_wait":30000000000`, следующий таймер — `group_interval` 5m. Есть с первого коммита (`d096644`).
- **Impact:** verbatim `alertmanager.yml` с таймингами у корня и дочерними route без своих таймингов (типичная форма) работает иначе, чем upstream: первая нотификация ждёт 30s вместо заданного, повторы идут по `repeat_interval` 4h вместо, например, 1h. `global.*`-фолбэк из `FU-GLOB-DEFAULT-VALUES` для вложенных route тоже не срабатывает.
- **Fix:** дефолты ставить только корню (или не ставить вовсе, оставив их `inheritDuration`); тест на дерево корень → ребёнок без таймингов → внук, плюс проверка, что `/api/v2/status` `config.original` не показывает лишних полей. Сверить, не опирается ли кто-то ещё на `Defaults()` у вложенных route.
- **Refs:** `tasks/archive/PROD-HARDCODED-FILTER/evidence/smoke.md` § Находка.
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

### [medium][Cache][~0.5d] AMP-CACHE-NO-RECONNECT
- **Title:** AMP не переподключается к кешу, недоступному на старте
- **Problem:** `go-app/internal/application/service_registry.go:1204-1212`: если Redis/Valkey не ответил при инициализации, AMP переходит на in-memory кеш до конца жизни процесса. `helm upgrade` рестартует AMP и под кеша одновременно, порядок не гарантирован.
- **Impact:** после upgrade или рестарта кеша AMP молча работает без общего состояния; лечится только `kubectl rollout restart` (migration note в `CHANGELOG.md`). Воспроизведено на k3d 2026-10-20 (`AMP-CACHE-NOAUTH` A2): первый `helm upgrade` с `main` рестартует Valkey (новые `items` volume) и AMP; AMP стартовал на 3 с раньше, `dial tcp …:6379: i/o timeout` → in-memory, под Ready. С `AMP-CACHE-NOAUTH` AMP на дефолтах ходит в кеш, поэтому гонка теперь бьёт по каждому upgrade, который рестартует оба пода; NOTES на upgrade подсказывают `rollout restart`.
- **Fix:** ленивое подключение с ретраем (backoff) и переход с in-memory на кеш при восстановлении, либо readiness AMP не готов, пока кеш недоступен; метрика/лог текущего бэкенда кеша.
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/review-findings.md` R7; `tasks/archive/AMP-CACHE-NOAUTH/evidence/k3d-acceptance.md` § A2, `review-findings.md` A4.
- **Status:** open

### [medium][Helm][~0.25d] HELM-VCT-CHART-LABELS
- **Title:** метки `volumeClaimTemplates` содержат версию чарта — первый бамп версии сломает `helm upgrade`
- **Problem:** `templates/redis-statefulset.yaml` (и проверить `postgresql-statefulset.yaml`) кладёт в `volumeClaimTemplates.metadata.labels` полный `amp.labels`, включая `helm.sh/chart` и `app.kubernetes.io/version`. Поле StatefulSet неизменяемое: смена `version`/`appVersion` в `Chart.yaml` даёт `StatefulSet.apps is invalid: spec: Forbidden`. Сейчас не проявляется — версия не менялась с первого релиза чарта. По спецификации k8s, на кластере не прогонялось.
- **Impact:** первый релиз с бампом версии (`PROD-RELEASE-V010`) не обновит существующие установки без удаления StatefulSet (`--cascade=orphan`).
- **Fix:** в VCT только selector labels + `component`; migration note (одноразовое `kubectl delete sts --cascade=orphan` перед upgrade, т. к. сама смена меток тоже меняет VCT); render-тест «в VCT нет `helm.sh/chart`». Сделать до `PROD-RELEASE-V010`.
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/review-findings.md` R14.
- **Status:** open

### [low][Test][~0.1d] FLAKE-TIMER-CONTINUATION-GROUP-INTERVAL
- **Title:** `TestTimerContinuation_GroupWaitFireCreatesGroupIntervalTimer` флейкает в шаге `race`
- **Problem:** `go-app/internal/infrastructure/grouping/timer_continuation_regression_test.go:198` — ожидался таймер `group_interval`, прочитан `group_wait`: тест читает storage раньше, чем срабатывание записало continuation. Release-gate 2026-10-19 (ветка `feature/helm-single-node-defaults`, Go-код не менялся), шаг `race`; перезапуск шага — 7/7 ok, тест отдельно `-race -count=30` — 30/30.
- **Impact:** шаг `race` release-gate краснеет случайно.
- **Fix:** ждать опросом, пока тип записи станет `group_interval` (как предложено в `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` — тот же класс, чинить вместе).
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/release-gate.txt` (FAIL и перезапуск в конце файла).
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
