# BUGS

Код, нарушающий свой контракт (граница с долгом — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt). Исправленный баг удаляется отсюда; отчёт — в `DONE.md`.

## Open

<!-- Записи в виде списка (`- [ ] **SLUG** — ...`) — формат до Solo Kanban 1.1. Переводятся в Entry Format при взятии в работу. -->

### [high][Helm][~1d] PG-CONFIG-NOT-APPLIED
- **Title:** встроенный PostgreSQL игнорирует конфиг чарта: `max_connections=20`, `pg_hba` и `init.sql` не те, что задуманы
- **Problem:** `postgresql.conf` и `pg_hba.conf` из ConfigMap монтируются в `/docker-entrypoint-initdb.d/` (`helm/amp/templates/postgresql-statefulset.yaml:91-97`), а entrypoint образа исполняет оттуда только `*.sh`/`*.sql`; `/etc/postgresql` — пустой `emptyDir`, `POSTGRES_INITDB_ARGS="-c config_file=…"` (`:82`) ничего не подключает. Измерено на k3d 2026-10-15: `config_file` дефолтный, `archive_mode=off`, `max_connections=20` (чарт задумывал 250), `shared_buffers=400kB`, `pg_hba` — дефолт образа. `init.sql` падает на строке 56 (`ALTER SYSTEM SET pg_stat_statements.max` без preload) ⇒ первый старт контейнера с ошибкой, после рестарта init пропускается, скрипт применён частично. Образ `postgres:16` без patch-пина.
- **Impact:** пул AMP 25 соединений на pod (`go-app/internal/config/config.go:897`) × `minReplicas: 2` против 17 доступных соединений ⇒ под нагрузкой ошибки записи ⇒ ingest отвечает 500, алерты не маршрутизируются. Значение 20 измерено на k3d; на других нодах initdb может выбрать иное. Бэкап и PITR из-за этого тоже не работали (см. ADR-016, `PROD-POSTGRES-HA-DECISION`).
- **Fix:** конфиг — в `args: ["-c", "config_file=…"]` / `-c` параметрами, `pg_hba` через `hba_file`; `init.sql` без `ALTER SYSTEM` для непредзагруженных расширений; пин образа. Поведение существующих установок изменится при рестарте (лимиты памяти против `shared_buffers`, новый `pg_hba`) — migration note и проверка на k3d с уже инициализированным PVC.
- **Refs:** `tasks/PROD-POSTGRES-HA-DECISION/research.md` § Findings 2, `evidence/k3d-probe-effective-config.txt`.
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
- **Impact:** редкая конфигурация (`namespace` задан явно), но тихо: AMP теряет Redis при `valkey.networkPolicy.enabled`.
- **Fix:** один источник namespace для всех шаблонов (хелпер `amp.namespace`).
- **Refs:** deep-review PROD-INGRESS-HARDENING R16, `tasks/archive/PROD-INGRESS-HARDENING/review-findings.md`; 2026-10-01.
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
