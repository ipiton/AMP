# BUGS

Код, нарушающий свой контракт (граница с долгом — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt). Исправленный баг удаляется отсюда; отчёт — в `DONE.md`.

## Open

<!-- Записи в виде списка (`- [ ] **SLUG** — ...`) — формат до Solo Kanban 1.1. Переводятся в Entry Format при взятии в работу. -->

<!-- 2026-11-08, UPSTREAM-CORE-REMOVAL (срез 5 ADR-019): удалены как «obsolete: code removed in UPSTREAM-CORE-REMOVAL» — Open: SERVICE-METRICS-PORT-MISROUTED, LITE-REDIS-CONNECT-ERROR-ON-START, UI-ROUTE-PREFIX-LINKS, SQLITE-LITE-HARDENING, REDIS-FLUSH-ALL; вся секция «Снимается переходом на upstream (ADR-019)» — 40 записей среза 5 и 5 записей среза 6 (API-ALERTS-RECEIVER-INHIBITED-STATUS, API-V2-ERROR-SHAPE, API-FILTER-MATCHER-GRAMMAR, INGEST-PARSE-VALIDATE, SILENCES-CHECK-404: хендлеры `cmd/server`/`internal/application` удалены, `amp-upstream` отдаёт upstream `api/v2`); INVESTIGATION-ALERT-TIME-NOT-SET слит в INVESTIGATION-ALERT-TIME-UNSET; AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT (код жив) перенесён в Open. Строка — в `DONE.md` среза. -->

### [high][Gate][~0.1d] LINT-ERRCHECK-CHANNEL-RECEIVE
- **Title:** версия golangci-lint не закреплена: CI на старом пине не собирается с go1.27, локальный гейт берёт что есть в `PATH`
- **Problem:** CI (2026-10-28): job `gate` на `main` красный с `PROD-DEPS-GO-127` (run 40086939282): `.github/workflows/ci.yml:44` ставит golangci-lint `v2.13.2`, собранный старым Go, а toolchain уже `go1.27.3` — `typecheck`: «could not import internal/goarch … export data version 5 is greater than maximum supported version 4». Локально версия нигде не закреплена: `go-app/Makefile:164` ставит `@latest`, `scripts/release-gate.sh` берёт бинарь из `PATH`. Так 2026-10-22 обновление линтера до 2.15.0 без изменения кода покраснило гейт на 7 `errcheck` (приём из канала `error` в тестах).
- _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ часть про код закрыта: все 7 `errcheck` были в удалённых тестах (`infrastructure/publishing/delivery_confirmation_test.go`, `infrastructure/silencing/redis_event_bus_test.go`), шаг `lint` release-gate на golangci-lint 2.15.0 зелёный. Остался пин версии; бамп CI-пина до ≥ 2.15.0 больше не упирается в `errcheck`.
- **Impact:** CI `gate` на `main` красный, пока пин не поднят; обновление линтера на машине владельца по-прежнему может покраснить гейт без изменения кода.
- **Fix:** поднять `version:` в `ci.yml` до версии, собранной go1.27 (≥ 2.15.0); одна закреплённая версия для CI, `make lint`/`install-tools` и release-gate (проверка `golangci-lint version` в гейте или установка по пину), чтобы обновление линтера было отдельным изменением.
- **Refs:** найдено при HELM-VCT-CHART-LABELS (2026-10-22), `tasks/archive/HELM-VCT-CHART-LABELS/evidence/release-gate.txt`. Блокирует Owner `MAIN-BRANCH-PROTECTION` (ждёт зелёного CI на `main`). Смежно `QUALITY-GATES-ALIGN`.
- **Status:** open

### [medium][Helm][~0.25d] ESO-SECRET-OWNERSHIP-CONFLICT
- **Title:** при `externalSecrets.enabled` Helm и External Secrets Operator претендуют на один Secret
- **Problem:** `helm/amp/templates/secret.yaml` рендерит `<fullname>-secrets` безусловно, а `templates/externalsecret.yaml` при `externalSecrets.enabled` целится в тот же Secret (`target.name: <fullname>-secrets`, `creationPolicy: Owner`). Ожидаемо: ESO перезаписывает данные, Helm на `upgrade` возвращает свои (в том числе пустые ключи), владелец объекта неоднозначен.
- **Impact:** не воспроизводилось — найдено чтением шаблонов. Затрагивает только установки с ESO; дефолт и `values-production.yaml` его не включают.
- **Fix:** не рендерить `secret.yaml` (или его ключи, которые даёт ESO) при `externalSecrets.enabled`; render-тест; проверить на кластере с ESO.
- _(2026-10-14, HELM-DEFAULTS-VALIDATE review F2/R3-1)_ путь стал рекомендуемым: `fail` в `secret.yaml` при `llm.enabled=true` без ключа советует `externalSecrets.enabled`. Рядом второй, нерабочий путь: `helm/amp/DEPLOYMENT.md:84` предлагает `llm.externalSecrets.*` (`templates/llm-secret.yaml`), но под AMP (с UPSTREAM-CORE-REMOVAL — StatefulSet, `templates/statefulset.yaml`) всегда читает `<fullname>-secrets/llm-api-key`, а guard принимает только глобальный `externalSecrets.enabled` ⇒ рендер падает (до задачи — `CreateContainerConfigError`). В фиксе оставить один путь ESO для ключа LLM и поправить DEPLOYMENT.md.
- **Refs:** `tasks/archive/HELM-DEFAULTS-VALIDATE/research.md` § Findings (линза operational), `review-findings.md` F2, R3-1.
- **Status:** open

### [low][Test][~0.1d] HELM-TEST-SIGPIPE-PIPEFAIL
- **Title:** render-тесты чарта случайно падают на `printf: Broken pipe`
- **Problem:** `helm/amp/tests/render-networkpolicy.sh` (`assert_render_fails`, стр. 59) под `set -euo pipefail` делает `printf '%s' "${output}" | grep -q -F -e "${expected}"`. `grep -q` выходит на первом совпадении, `printf` получает SIGPIPE, и `pipefail` превращает успешное совпадение в провал: `FAIL ingressController only, Ingress off (wrong error: …)`, хотя текст ошибки ожидаемый. Поймано release-gate 2026-10-07; три повторных прогона скрипта — зелёные.
- **Impact:** шаг `helm-tests` required-гейта `gate` краснеет случайно на длинных выводах `helm template`.
- **Fix:** `grep -q -F -e "${expected}" <<<"${output}"` (here-string вместо пайпа) во всех `tests/*.sh`.
- **Refs:** `~/amp-audit/gate-runbook.log`, 2026-10-07.
- **Status:** open

### [low][Helm][~0.1d] VALKEY-REQUIREPASS-QUOTING
- **Title:** пароль Valkey с пробелом или спецсимволами ломает старт Valkey
- **Problem:** init-контейнер `templates/redis-statefulset.yaml` (`echo "requirepass $(cat /tmp/secret/password)" >> /data/redis.conf`) пишет пароль без кавычек и экранирования. С паролем `s3cr et` Valkey 8.1.10 не стартует: `FATAL CONFIG FILE ERROR ... wrong number of arguments`. Сгенерированный чартом пароль безопасен, проблема — со своим `valkey.password.existingSecret`.
- **Impact:** под Valkey в CrashLoop при «сильном» пароле оператора; Valkey по умолчанию выключен (`valkey.enabled: false`).
- **Fix:** писать `requirepass "<пароль>"` с экранированием `\` и `"` (формат redis.conf), либо передавать `--requirepass` через env в `command` sh с кавычками; render-тест + прогон на образе с паролем с пробелом/кавычкой.
- **Refs:** `tasks/archive/P2-HYGIENE-BUNDLE/review-findings.md` F2; 2026-11-14.
- **Status:** open

### [low][Helm][~0.25d] HELM-NAMESPACE-OVERRIDE-SPLIT
- **Title:** `namespace` разносит объекты AMP и redis по разным namespace
- **Problem:** шаблоны приложения ставят `namespace: {{ .Values.namespace | default .Release.Namespace }}` (или хелпер `amp.namespace` — `statefulset.yaml`, `service.yaml`), а `redis-*.yaml` — `.Release.Namespace`. При `namespace` ≠ namespace релиза Valkey, его Services, Secret, NetworkPolicy и ServiceMonitor оказываются в другом namespace, чем AMP: политика redis выбирает AMP pod'ы через `podSelector` без `namespaceSelector` и их не видит.
- _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ `amp-upstream` к Valkey не подключается (ключи `cache.*` удалены, `REDIS_PASSWORD` и `amp.cache.hostIsValkey` ушли вместе с ними), поэтому `CreateContainerConfigError` и потеря кеша больше не грозят. Остался сам разнос объектов релиза по двум namespace; вернётся как дефект доступа, когда AMP снова пойдёт в Valkey (`UPSTREAM-AI-HA-DEDUP`).
- **Impact:** редкая конфигурация (`namespace` задан явно): объекты одного релиза в двух namespace, NetworkPolicy Valkey не пустит AMP, когда тот начнёт им пользоваться.
- **Fix:** хелпер `amp.namespace` (уже есть в `_helpers.tpl`) во всех шаблонах, включая `redis-*.yaml` и `rbac.yaml`; render-тест с `namespace` ≠ релиза.
- **Refs:** deep-review PROD-INGRESS-HARDENING R16, `tasks/archive/PROD-INGRESS-HARDENING/review-findings.md`; 2026-10-01; deep-review AMP-CACHE-NOAUTH A2 (`tasks/archive/AMP-CACHE-NOAUTH/review-findings.md`), 2026-10-20.
- **Status:** open

### [low][Test][~0.1d] MIGRATIONS-TEST-PANICS-ON-SLOW-DOCKER
- **Title:** `TestRunMigrations_ConcurrentReplicas_FreshDB` паникует вместо skip, когда Docker отвечает медленно
- **Problem:** `requireDocker` (`go-app/internal/database/migrations_concurrent_test.go:34`) вызывает `testcontainers.NewDockerClientWithOpts`, а тот при таймауте `docker info` (2 с) паникует в `MustExtractDockerHost` — до ветки `t.Skip`. Воспроизвелось, когда параллельно шла сборка: `context deadline exceeded`. В одиночку тест проходит.
- **Impact:** шаг `test` в `scripts/release-gate.sh` краснеет на загруженной машине (2026-10-10).
- _(2026-10-14, HELM-DEFAULTS-VALIDATE testing)_ тот же механизм в `go-app/internal/infrastructure/repository/postgres_history_test.go:34` (`TestGetTopAlerts_EmptyDatabase`): `docker info: context deadline exceeded` → паника. Фикс — общий хелпер для обоих пакетов.
- **Fix:** в `requireDocker` перехватывать панику (`recover`) и делать `t.Skip`, либо проверять доступность Docker своим вызовом с бóльшим таймаутом.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/tasks.md` п. 5.1.
- **Status:** open

### [low][Test][~0.25d] TESTCONTAINERS-FLAKY-UNDER-LOAD
- **Title:** интеграционные тесты на testcontainers падают на загруженной машине
- **Problem:** 2026-10-14, release-gate при load average ~247: `internal/infrastructure/inhibition/integration_test.go:187` (`TestIntegration_TwoTierAlertCache_Redis`) — `create container: reaper: … unexpected container status "removing"`; `internal/database/postgres/pool_reload_test.go:42` (`startPostgres`) не дождался контейнера и упал по таймауту пакета 10m. Изолированный перезапуск — оба `ok`. _(2026-11-08, UPSTREAM-CORE-REMOVAL: тест inhibition удалён вместе с пакетом; живые testcontainers-тесты — `database/postgres/pool_reload_test.go` (`startPostgres`, `:38`), `core/services/deduplication_integration_test.go`, `upstreamai/integration_test.go`, `infrastructure/repository/postgres_history_test.go`.)_
- **Impact:** шаг `test` в `scripts/release-gate.sh` краснеет случайно; таймаут `postgres` стоит 10 минут прогона.
- **Fix:** дедлайн на старт контейнера (`context.WithTimeout`) и `t.Skip`/понятная ошибка вместо висящего теста; для reaper — ретрай старта или `TESTCONTAINERS_RYUK_DISABLED` в гейте с явной очисткой.
- **Refs:** `tasks/archive/HELM-DEFAULTS-VALIDATE/tasks.md` п. 5.4; связано с `MIGRATIONS-TEST-PANICS-ON-SLOW-DOCKER`.
- **Status:** open

### [low][Helm][~0.25d] HELM-LITE-RENDERS-POSTGRES
- **Title:** профиль `lite` рендерит встроенный PostgreSQL, которым AMP не пользуется
- **Problem:** шаблоны `helm/amp/templates/postgresql-*.yaml` гейтятся только `postgresql.enabled` (по умолчанию `true`), комментарий в `values.yaml` («Overridden by profile») не соответствует шаблонам: `helm template --set profile=lite` даёт StatefulSet, Service, Secret и PVC Postgres. Дефект существовал до PROD-POSTGRES-HA-DECISION; README предупреждает «set `false` for the lite profile».
- **Impact:** lite-установка тянет лишний под Postgres и PVC, запрашивает ресурсы ноды впустую.
- _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ проверено по текущим шаблонам: дефект на месте — `postgresql-*.yaml` по-прежнему гейтятся только `postgresql.enabled`. Сторона AMP уже гейтится профилем: `DATABASE_PASSWORD` в `templates/statefulset.yaml` и блок `database` в `templates/amp-config.yaml` — только при `profile: standard`. Шаблона `deployment.yaml` больше нет.
- **Fix:** гейт `and .Values.postgresql.enabled (eq .Values.profile "standard")` во всех `postgresql-*.yaml` (включая `secret.yaml`, ветку пароля PG) либо `fail` на `lite` + `postgresql.enabled`; render-тест.
- **Refs:** `tasks/archive/PROD-POSTGRES-HA-DECISION/review-findings.md` R11; `tasks/archive/UPSTREAM-CORE-REMOVAL/review-findings.md` C11.
- **Status:** open

### [medium][Helm][~0.25d] PG-EXPORTER-NO-SERVICEMONITOR
- **Title:** метрики встроенного PostgreSQL никто не скрейпит — правила `amp-postgres` слепые
- **Problem:** у Service `<fullname>-postgres-exporter` только аннотации `prometheus.io/scrape|port|path` (`templates/postgresql-exporter-service.yaml`), а prometheus-operator их не читает; ServiceMonitor для exporter'а чарт не рендерит (AMP-ServiceMonitor выбирает только `component: application`). На пилоте (sema-prod, 2026-11-09…14) в Prometheus нет ни одной метрики `pg_*`, все 11 правил `PrometheusRule amp-postgres` не могут сработать — `PostgreSQLDown` молчит и при упавшем PG.
- **Impact:** мониторинг встроенного PostgreSQL выглядит включённым, но не работает; находки `PG-ALERT-RULES-METRICS` на живом кластере даже не проявляются.
- **Fix:** ServiceMonitor для exporter'а под `postgresql.exporter.enabled` + гейт `amp.monitoringCRDs`, `monitoring.serviceMonitor.labels`, порт `metrics`; NetworkPolicy PG пропускает `networkPolicy.metricsScrapers` на 9187; render-тест; проверка на k3s, что `pg_up` появился и правила вычисляются.
- **Refs:** промежуточный срез пилота 2026-11-14 (`AMP-PILOT-WEEK-REVIEW`); связано с `PG-ALERT-RULES-METRICS`, BACKLOG `PG-MONITORING-CHECKS`.
- **Status:** open

### [medium][Helm][~0.5d] PG-ALERT-RULES-METRICS
- **Title:** часть правил `postgresql.rules` ссылается на метрики, которых exporter не отдаёт, а `PostgreSQLVacuumOld` шумит
- **Problem:** с PG-CONFIG-FIX группа правил парсится и exporter подключён, но сверка со scrape `postgres-exporter:v0.15.0` (k3d 2026-10-17) показала: connection-saturation (2 правила) без `on(instance)` между `pg_stat_activity_count` и `pg_settings_max_connections`; replication-lag ссылается на `pg_replication_lag` (есть `pg_replication_lag_seconds`); wraparound — на `pg_database_age_seconds` (нет); slow-query — на `pg_stat_statements_mean_exec_time_seconds` (коллектор `stat_statements` выключен по умолчанию); bgwriter (2) — на имена без `_total`. Эти правила молчат. `PostgreSQLVacuumOld` срабатывает на таблицах, куда почти не пишут: autovacuum их не трогает, пока не накопится порог изменений, и warning висит бесконечно.
- **Impact:** операторы считают, что насыщение соединений, wraparound и медленные запросы под алертами, а их нет; плюс постоянный шум VacuumOld на тихих таблицах.
- **Fix:** сверить каждое правило с `/metrics` exporter'а (или включить нужные коллекторы), для VacuumOld учитывать `n_dead_tup`/`n_mod_since_analyze` или убрать; `promtool test rules` на каждое правило — вместе с BACKLOG `PG-MONITORING-CHECKS`.
- **Refs:** `tasks/archive/PG-CONFIG-FIX/review-findings.md` R3, R13; `evidence/k3d-acceptance.md` K1.3.
- **Status:** open

### [medium][App][~0.25d] AMP-NO-DB-WAIT
- **Title:** AMP, стартовавший раньше PostgreSQL, работает без истории и investigation до рестарта
- **Problem:** на свежей установке AMP стартует раньше встроенного PG (k3d 2026-10-17: `dial tcp …:5432: connect: connection refused`). Старый `cmd/server` на этом завершался — 3–8 рестартов на install, и в итоге подключался. _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ `amp-upstream` не выходит: `connectDatabase` (`go-app/cmd/amp-upstream/amp.go`) делает одну попытку (`pool.Connect` → `Ping` в пределах `database.connect_timeout`, `internal/database/postgres/pool.go:189-205`), на ошибке пишет Error «alert history and investigation disabled until restart» и продолжает без БД; ретрая нет, initContainer в `templates/statefulset.yaml` нет. По коду, на k3d с новым чартом не прогонялось.
- **Impact:** `profile: standard` со встроенным PG на первой установке (и после рестарта БД, если AMP перезапустился раньше неё) тихо остаётся без истории и расследований: под Ready, доставка работает, видно только по Error в логе старта. Хуже прежних рестартов — те хотя бы сходились.
- **Fix:** ретрай подключения с backoff на старте (до таймаута, затем фон — TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT`) или initContainer `pg_isready` для встроенного PG; тест на старт без БД.
- **Refs:** `tasks/archive/PG-CONFIG-FIX/evidence/k3d-acceptance.md` (Known, outside the task); TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT`.
- **Status:** open

### [low][Helm][~0.1d] PG-PRESTOP-SU-FAILS
- **Title:** preStop встроенного PostgreSQL не работает: `su - postgres` под `runAsUser: 999`
- **Problem:** `templates/postgresql-statefulset.yaml` preStop — `su - postgres -c "pg_ctl stop -D $PGDATA -m fast -w -t 60" || true`; контейнер уже работает под uid 999 без root, `su` падает, `|| true` глушит ошибку. Остановку делает SIGTERM от kubelet (smart shutdown), а не задуманный fast.
- **Impact:** остановка ждёт закрытия клиентских сессий AMP вместо `-m fast`; при долгих сессиях упирается в `terminationGracePeriodSeconds` и SIGKILL (recovery на старте).
- **Fix:** `pg_ctl stop -D "$PGDATA" -m fast -w -t 60` без `su`; проверка на k3d (лог `fast shutdown request`).
- **Refs:** `tasks/archive/PG-CONFIG-FIX/research.md` (code-read, вне scope).
- **Status:** open

### [low][Gate][~0.1d] RELEASE-GATE-SMOKE-PORT-CONFLICT
- **Title:** шаг `amtool-compat` краснеет, если порт хоста 18080 занят
- **Problem:** `deploy/smoke/docker-compose.yml:50` публикует amp на фиксированном `18080` (`18080:9093`), проба `scripts/release-gate.sh:302` ходит в `localhost:18080/-/ready`. На машине владельца этот порт занят локальным `agent-memory-mcp` (`127.0.0.1`) и `kubectl port-forward` (`[::1]`): проба получает 404 от чужого процесса, хотя контейнер `healthy`. Сам amtool ходит по docker-сети к `amp:9093` и порт хоста не использует. _(Пути и порты сверены 2026-11-08 после UPSTREAM-CORE-REMOVAL: smoke-стек на `amp-upstream`.)_
- **Impact:** release-gate — `RESULT: FAIL` на `amtool-compat` без регрессии; шаг приходится прогонять вручную.
- **Fix:** проба через `docker inspect -f '{{.State.Health.Status}}'` (или `docker compose exec`) вместо порта хоста; либо порт из переменной окружения с проверкой занятости до `up`.
- **Refs:** `tasks/archive/INVESTIGATION-SAFETY-RELIABILITY/evidence/testing.md` (2026-10-28).
- **Status:** open

### [low][Config][~0.1d] INVESTIGATION-MAX-RETRIES-ZERO
- **Title:** `investigation.max_retries: 0` молча превращается в 3
- **Problem:** `newInvestigation` (`go-app/cmd/amp-upstream/amp.go:186`, `if inv.MaxRetries > 0`) копирует `Investigation.MaxRetries` в очередь только при `> 0`, иначе остаётся дефолт `DefaultQueueConfig().MaxRetries` (3). Тот же класс, что исправленный для `llm.max_retries` в `INVESTIGATION-SAFETY-RELIABILITY`. _(2026-11-08: переехало из `internal/application/service_registry.go` (`initializeInvestigation`), удалённого UPSTREAM-CORE-REMOVAL, без изменения поведения.)_
- **Impact:** ретраи расследований нельзя выключить; оператор, поставивший 0 ради бюджета LLM, получает до 4 вызовов на job.
- **Fix:** различать «не задано» и `0` (viper `SetDefault` + `>= 0`), тест на wiring; строка в CHANGELOG.
- **Refs:** найдено на deep review `INVESTIGATION-SAFETY-RELIABILITY` (2026-10-28), `tasks/archive/INVESTIGATION-SAFETY-RELIABILITY/review-findings.md`.
- **Status:** open

### [low][Investigation][~0.25d] INVESTIGATION-ALERT-TIME-UNSET
- **Title:** Prometheus и Loki tools всегда якорятся на `time.Now()`, а не на времени алерта
- **Problem:** `core/investigation/context.go` несёт `WithAlertTime`/`AlertTimeFromCtx`, `infrastructure/investigation/tools/prometheus.go` и `loki.go` его читают, но production-вызова `WithAlertTime` нет (`grep -rn WithAlertTime go-app/internal` — только определение и чтение; README пакета называет это known gap). `queue.processJobWithAgent` (`go-app/internal/infrastructure/investigation/queue.go:369`) передаёт в `AgentLoop.Run` голый `q.ctx`, `AgentLoop.Run` выставляет на контекст tools только namespace (`WithAlertNamespace`). Сверено 2026-11-08 после UPSTREAM-CORE-REMOVAL: код живой, путь `amp-upstream` тот же.
- **Impact:** расследование, взятое из очереди с задержкой или повторно (backoff до 60s × `max_retries`), смотрит метрики и логи «сейчас», а не окно ±15 мин вокруг `StartsAt`: выводы по уже прошедшему инциденту неверны.
- **Fix:** `WithAlertTime(ctx, alert.StartsAt)` рядом с `WithAlertNamespace` в `AgentLoop.Run` (или в `processJobWithAgent` перед `Run`), тест, что tool видит `StartsAt`.
- **Refs:** `tasks/archive/INVESTIGATION-PROMPT-ISOLATION/research.md` F1 (2026-10-29). Поглощает дубль `INVESTIGATION-ALERT-TIME-NOT-SET` (список, `/research` PHASE-6B-RUNBOOK-ENGINE, 2026-09-27).
- **Status:** open

### [medium][Config][~0.25d] CONFIG-ENV-KEYS-UNBOUND
- **Title:** env молча игнорируется для ключей без default/`BindEnv`
- **Problem:** `AutomaticEnv` (`go-app/internal/config/config.go:160-161`, признано комментарием у `BindEnv` в `setDefaults`, `:227-230`) резолвит только ключи, известные viper. _(Пересчитано 2026-11-08 после UPSTREAM-CORE-REMOVAL: конфиг сократился до 58 mapstructure-ключей; `llm.agent_mode` теперь с default и биндится.)_ Без default и `BindEnv` — 12 ключей `investigation.tools.*`: `prometheus.{endpoint,timeout,username,password}`, `loki.{endpoint,timeout,username,password}`, `kubernetes.{enabled,kubeconfig,allowed_namespaces}`, `database.enabled` (`database.dsn` забинден). Ещё `server.web_config_file` — без default, но `SERVER_WEB_CONFIG_FILE` читает напрямую `cmd/amp-upstream/auth.go` (`os.Getenv`), так что env работает. Блоки tools — указатели (`InvestigationToolsConfig`), из одного env они бы и не создались.
- **Impact:** оператор задаёт `INVESTIGATION_TOOLS_PROMETHEUS_PASSWORD` из Secret через `extraEnv` — пароль не подхватывается, без ошибки; секреты tools остаются только в ConfigMap `amp.yaml` (так и записано в `values.yaml`, deep-review UPSTREAM-CORE-REMOVAL C4).
- **Fix:** bind всех mapstructure-путей (reflection или `viper.ExperimentalBindStruct()`), тест, сверяющий набор ключей структуры и bound-ключей. Корень общий с `CONFIG-GLOBAL-VIPER-STATE` — делать вместе.
- **Refs:** CODE-REVIEW-2026-10-21 M-22.
- **Status:** open

### [medium][Gate][~0.25d] QUALITY-GATES-ALIGN
- **Title:** `make quality-gates` даёт ложно-зелёный результат
- **Problem:** `go-app/Makefile:21,87-93`: цель гоняет только `TEST_TARGETS_MVP` (с UPSTREAM-CORE-REMOVAL — `./cmd/... ./internal/upstreamcore/... ./internal/upstreamai/... ./internal/upstreamtargets/...`) без lint и `-race`; `go fmt` переписывает файлы вместо проверки. Настоящий гейт — `scripts/release-gate.sh`.
- _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ шаг `race` release-gate гоняет ручной список `RACE_PKGS` (`scripts/release-gate.sh:108-120`); deep-review F5 расширил его (`infrastructure/k8s`, `infrastructure/llm`, `core/resilience`, `core/services`, `webauth`, `cmd/amp-upstream`). Список по-прежнему ручной: исчезнувший пакет шаг ловит («race package … is gone»), новый конкурентный пакет без правки списка в `-race` не попадает.
- **Impact:** глобальные правила агентов предписывают `make quality-gates` — гейт проходит при красном release-gate; новый пакет с горутинами молча идёт без `-race`.
- **Fix:** `quality-gates: ; ../scripts/release-gate.sh`; fmt-проверка через `gofmt -l` с ненулевым кодом выхода; `quality-gates-fast` оставить как есть. `RACE_PKGS` — `go test -race ./...` (если по времени укладывается после удаления ядра) или проверка в гейте, что каждый пакет с `go `-горутинами/`sync` есть в списке.
- **Refs:** CODE-REVIEW-2026-10-21 M-28. Поглощает остаток `QUALITY-GATES-DIRTIES-TREE` (BACKLOG); смежно `PARITY-GATE-DOES-NOT-GATE`.
- **Status:** open

### [low][Security][~0.25d] SERVER-HARDENING
- **Title:** bcrypt basic auth под глобальным мьютексом, ранний SIGHUP убивает процесс
- **Problem:** _(переадресовано 2026-11-08: `cmd/server` и `internal/application` удалены UPSTREAM-CORE-REMOVAL; пункты про `ReadHeaderTimeout`/`MaxHeaderBytes` и `os.Exit` без shutdown устарели — у `amp-upstream` `http.Server{ReadHeaderTimeout: 10s}` (`go-app/cmd/amp-upstream/main.go:156`), ошибка `net.Listen` возвращается из `run` через defer'ы.)_
  - `go-app/internal/webauth/webauth.go:155-156,279-284` (`authenticate`): все bcrypt-сравнения под одним `bcryptMu`, неверные пароли кешируются вместе с верными (`cacheSet`) ⇒ поток неверных Basic-auth (каждая новая пара — промах кеша) выстраивает в очередь проверки легитимных клиентов.
  - `signal.Notify(SIGHUP)` (`cmd/amp-upstream/main.go:153`) после `newAMPLayer` (подключение к PG до `database.connect_timeout`, миграции до 1m) и `upstreamcore.New` — SIGHUP во время долгого старта завершает процесс действием по умолчанию.
- **Impact:** DoS auth без кредов; падение при раннем reload (sidecar/оператор шлёт SIGHUP, пока идут миграции).
- **Fix:** семафор `GOMAXPROCS` вместо мьютекса и отдельный лимит на отказы (кеш отказов не должен вытеснять верные вердикты); `signal.Notify(hup)` в начале `run`, сигналы до готовности ядра — игнорировать или откладывать.
- **Refs:** CODE-REVIEW-2026-10-21 L-3, L-4; `tasks/archive/UPSTREAM-CORE-REMOVAL/review-findings.md` F10.
- **Status:** open

### [low][Tests][~0.1d] UPSTREAMCORE-TEST-GROUPS-RACE
- **Title:** `TestCoreEndToEnd` читает `/api/v2/alerts/groups` сразу после POST — латентный флейк
- **Problem:** `go-app/internal/upstreamcore/core_test.go` (`TestCoreEndToEnd`, проверка группы) не ждёт dispatcher: upstream получает алерты через подписку на провайдер, группы появляются ~через 1ms. Локально 100/100 проходит (промежуточный GET `/alerts` даёт время).
- **Impact:** на медленном CI тест может упасть без регрессии. С UPSTREAM-CORE-REMOVAL `./internal/upstreamcore/` в ручном `RACE_PKGS` шага `race` (`scripts/release-gate.sh:108-120`, список расширен deep-review F5, остаётся ручным — `QUALITY-GATES-ALIGN`): под `-race` окно между POST и появлением группы шире, флейк вероятнее.
- **Fix:** `require.Eventually` вокруг чтения групп (как ожидание групп в харнессе `internal/upstreamcore/contract/harness_test.go:140-165`; прежний харнесс `cmd/server/upstreamcore_compat.go` удалён).
- **Refs:** deep-review `UPSTREAM-CORE-POC-GATE` F9, `tasks/archive/UPSTREAM-CORE-POC-GATE/review-findings.md`; deep-review `UPSTREAM-CORE-REMOVAL` F5.
- **Status:** open

- [ ] **AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT** — `AgentLoop.Run` (`go-app/internal/core/investigation/agent_loop.go:232-233`, ветка `default` в `switch resp.Kind`) возвращает `(nil, err)`, а `InvestigationQueue.processJobWithAgent` (`go-app/internal/infrastructure/investigation/queue.go:369-373`) на ошибке сразу читает `agentRun.TerminationKind` ⇒ nil-pointer panic в воркере. Сейчас недостижимо из прод-клиента (`parseOpenAIAgentResponse` отдаёт только два известных kind), но любой новый `AgentLLMClient` или мок с иным kind роняет воркер. Фикс: возвращать `&AgentRunResult{TerminationKind: "error", ...}` и/или nil-guard в queue + тест. Не регресс. Найдено на `/implement` PHASE-6B-RUNBOOK-ENGINE (2026-09-27). _(2026-11-08: стояла в секции ADR-019 без пометки среза; код живой в `amp-upstream` (agent mode), перенесена в Open.)_

### [low][DB][~0.1d] MIGRATIONS-DOWN-STEPS-IS-VERSION
- **Title:** `RunMigrationsDown(steps)` откатывает до версии `steps`, а не на `steps` шагов
- **Problem:** `go-app/internal/database/migrations.go` (`RunMigrationsDown`) вызывает `goose.DownTo(db, dir, int64(steps))`, а `DownTo` принимает целевую версию. `steps=1` откатывает все миграции, то есть удаляет все таблицы.
- **Impact:** сейчас функцию никто не вызывает. Первый вызов «на один шаг» уничтожит схему.
- **Fix:** удалить функцию (runbook запрещает `goose down`) или считать целевую версию из `provider.Status`; добавить тест.
- **Refs:** deep-review `UPSTREAM-API-UI` F2, `tasks/archive/UPSTREAM-API-UI/review-findings.md`.
- **Status:** open

### [low][History][~0.1d] HISTORY-NULL-STARTS-AT-SCAN
- **Title:** `GetAlertByFingerprint` падает на строке `alerts` с `starts_at = NULL`
- **Problem:** `go-app/internal/infrastructure/postgres_storage_adapter.go` (`GetAlertByFingerprint`) сканирует nullable `starts_at` в `time.Time`. На legacy-строке с NULL дедупликация получает ошибку, и запись истории этого алерта падает на каждом событии. Нынешний писатель NULL не пишет: zero time сохраняется как `0001-01-01`. Новые ридеры `AlertRecordBy*` NULL читают.
- **Impact:** только БД со строками от старых писателей; история таких алертов не обновляется.
- **Fix:** сканировать в `*time.Time`, как `alertRecord` в `postgres_alert_records.go`; тест с NULL.
- **Refs:** deep-review `UPSTREAM-API-UI` F11; smoke `tasks/archive/UPSTREAM-API-UI/evidence/manual-smoke.md`.
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
