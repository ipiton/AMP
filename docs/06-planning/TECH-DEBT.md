# TECH-DEBT

Работающее по контракту, но дорогое в сопровождении: плохая структура, хрупкий дизайн, нет тестов на рабочий код, слабая наблюдаемость. Если код нарушает свой контракт — это баг, место ему в `BUGS.md` (граница — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt).

Погашенный долг удаляется; отчёт — в `DONE.md`.

## Bundles

<!-- Мелкие записи одной подсистемы (одних и тех же файлов) — в одного claimable-родителя.
     В Queue `NEXT.md` идёт только slug бандла. См. docs/solo-kanban/artifact-contract.md § Bundles. -->

<!-- Code review 2026-10-21: участники — записи BUGS.md / TECH-DEBT.md. Пути verify — от корня репозитория. -->

### PUBLISH-QUICK-FIX-BUNDLE
- [critical][Publishing][~2d] PUBLISH-QUICK-FIX-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/infrastructure/publishing/... ./internal/business/publishing/...`
- **Members (ordered):**
  - [ ] PROVIDER-RESOLVE-STATELESS-PD
  - [ ] PUBLISH-PER-TARGET-STATE
  - [ ] PROVIDER-RESOLVE-ROOTLY
  - [ ] CIRCUIT-BREAKER-HALF-OPEN
  - [ ] QUEUE-GRACEFUL-STOP
  - [ ] PUBLISH-SECRETS-IN-LOGS

### CONFIG-RELOAD-BUNDLE
- [high][Config][~1.25d] CONFIG-RELOAD-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/config/... ./internal/application/... ./cmd/server/...`
- **Members (ordered):**
  - [ ] RELOAD-SERIALIZE
  - [ ] CONFIG-VALIDATION-FAIL-CLOSED
  - [ ] CONFIG-ENV-KEYS-UNBOUND

### INVESTIGATION-SAFETY-BUNDLE
- [high][Investigation][~2.25d] INVESTIGATION-SAFETY-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/infrastructure/investigation/... ./internal/infrastructure/llm/... ./internal/core/resilience/...`
- **Members (ordered):**
  - [ ] LLM-RETRY-CLASSIFY
  - [ ] INVESTIGATION-ASYNC-DEDUP
  - [ ] INVESTIGATION-PROMPT-ISOLATION

### API-COMPAT-BUNDLE
- [medium][API][~1.25d] API-COMPAT-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/application/handlers/... ./cmd/server/...`
- **Members (ordered):**
  - [ ] API-V2-ERROR-SHAPE
  - [ ] INGEST-PARSE-VALIDATE
  - [ ] SERVER-HARDENING

### INHIBITION-LIFECYCLE-BUNDLE
- [medium][Inhibition][~1.5d] INHIBITION-LIFECYCLE-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/infrastructure/inhibition/... ./internal/core/services/...`
- **Members (ordered):**
  - [ ] INHIBIT-STATE-LIFECYCLE
  - [ ] INHIBIT-MATCH-PERF

### REDIS-STORAGE-BUNDLE
- [low][Grouping][~1.1d] REDIS-STORAGE-BUNDLE
- **Combined verify:** `cd go-app && go test -race ./internal/infrastructure/grouping/... ./internal/infrastructure/lock/... ./internal/infrastructure/cluster/...`
- **Members (ordered):**
  - [ ] REDIS-HELPERS
  - [ ] REDIS-GROUP-INDEX-GHOSTS
  - [ ] LOCK-ACQUIRE-SLEEP
  - [ ] CLUSTER-HEARTBEAT-STATE

## Critical

## High

## Medium

### [medium][Helm][~2d] HELM-CHART-GAPS
- **Title:** расхождения шаблонов чарта с values
- **Problem:** найдено при аудите `values-production.yaml` (INF-B, 2026-08-20), не исправлено (слишком большой темплейт-скоуп для values-аудита):
  - `valkey.enabled`/`cache.enabled` НЕ гейтят деплой `templates/redis-statefulset.yaml` — единственный гейт — `profile: standard`. Рабочий воркараунд — `valkey.replicas: 0` (заведён в values-production.yaml), но сами флаги мёртвые. `cache.enabled: false` ставит `REDIS_ADDR=localhost:6379`. _(2026-10-19, HELM-SINGLE-NODE-DEFAULTS: сабчарт `valkey` удалён, кеш один — `<fullname>-redis` на `valkey/valkey`; «два Redis на standard» больше нет. `valkey.replicas: 0` без `cache.host` теперь оставляет AMP без кеша.)_ _(2026-10-20, AMP-CACHE-NOAUTH: такой рендер падает (`fail`); при `valkey.replicas > 0` и внешнем `cache.host` Valkey чарта работает вхолостую — NOTES подсказывает `replicas: 0`.)_
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`DATABASE_PASSWORD`, `POSTGRES_PASSWORD` в StatefulSet) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION)_ HA встроенного PostgreSQL решён ADR-016: встроенный — dev/pilot, single instance, `postgresql.replicas != 1` падает на рендере; прод — внешний PostgreSQL. Реализации HA в чарте не будет.
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION R16)_ `migration.backupBeforeMigration` в `values.yaml` ни один шаблон не читает — мёртвое обещание бэкапа перед миграцией; удалить ключ (или реализовать, если понадобится).
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION R24)_ `NOTES.txt` (шаг 5) советует `kubectl port-forward svc/<fullname>`, но селектор Service захватывает и postgres/redis (BUGS `SERVICE-METRICS-PORT-MISROUTED`) — адресовать под AMP по меткам `component=application`, как runbook `POSTGRESQL_RESTORE_GUIDE.md` § 1.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` перекрывал его на `""`; с `HELM-EXTERNAL-DATABASE` (2026-10-16) блока `postgresql:` в нём нет (внешняя БД), и релиз, вернувший встроенный PG через `postgresql.enabled: true`, наследует этот литерал, если не задаёт пароль сам (CHANGELOG Breaking). Инконсистентность зафиксирована здесь, не исправлена.
  - _(2026-10-16, HELM-EXTERNAL-DATABASE R2)_ guard на слабый пароль нет: `postgresql.enabled: true` под `environment: production` с `postgresql.password` = дефолту `values.yaml` (`secure_password_123`) рендерится. AMP в production отсекает только короткие (< 12) и пароли из списка `weakPasswords` (`config.go:1466`), дефолт чарта (19 символов) проходит. Фикс: `fail` в `postgresql-secret.yaml` при `environment: production` и пароле, равном дефолту базового `values.yaml`; render-тест.
  - _(2026-10-01, PROD-INGRESS-HARDENING R17; NetworkPolicy сабчарта ушла вместе с ним 2026-10-19)_ политики postgres/redis хардкодят `namespaceSelector` `name: monitoring` / `name: kube-system` вместо автоматической метки `kubernetes.io/metadata.name`; `ingress.yaml` безусловно добавляет nginx sticky-аннотации (`affinity: cookie` и др.) к любым `ingress.annotations`.
  - _(2026-10-14, HELM-DEFAULTS-VALIDATE review F7)_ `postgresql.config.ssl` не bool-safe: `ssl: on` без кавычек или `--set postgresql.config.ssl=true` → `eq: incompatible types` на рендере; `ssl: "true"` даёт PostgreSQL `ssl = true`, а AMP — `DATABASE_SSL_MODE=disable` и флаг `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION` (`templates/deployment.yaml`). Фикс: сравнивать через `toString`, `fail` на всё, кроме `"on"`/`"off"`.
  - _(2026-10-18, PG-CONFIG-FIX R10)_ `checksum/config` pod'а PostgreSQL считается только от `postgresql-configmap.yaml`; ConfigMap exporter'а (`postgresql-exporter-configmap.yaml`) в него не входит — правка `queries.yaml` без смены конфига PG не рестартует sidecar (на k3d понадобился `kubectl rollout restart`).
  - _(2026-10-18, PG-CONFIG-FIX R12)_ AMP и exporter ходят во встроенный PG ролью `postgresql.username`, а она суперпользователь (её создаёт entrypoint образа) ⇒ `superuser_reserved_connections` ничего не резервирует: при насыщении пулов не войдут ни exporter, ни `kubectl exec psql`. Нужна отдельная непривилегированная роль для AMP и роль `pg_monitor` для exporter'а.
  - _(2026-10-18, PG-CONFIG-FIX)_ `postgresql.config: null` не рендерится (`nil pointer evaluating interface {}.maxConnections` в `postgresql-configmap.yaml`, `NOTES.txt`, `deployment.yaml`) — и на `main`; условия `if .Values.postgresql.config` в StatefulSet мёртвые. Либо guard'ы, либо убрать условия и объявить блок обязательным.
  - _(2026-10-18, PG-CONFIG-FIX)_ селектор Service `amp` захватывает pod `amp-redis-0` — `kubectl port-forward svc/amp` → `Pod 'amp-redis-0' does not have a named port 'http'` (тот же корень, что BUGS `SERVICE-METRICS-PORT-MISROUTED` и пункт R24 выше).
  - _(2026-10-20, AMP-CACHE-NOAUTH A3)_ пароль Valkey без `valkey.password.value`/`existingSecret` держится на `lookup` (`templates/redis-secret.yaml`); в рендерах без кластера (Argo CD, `helm template | kubectl apply`) каждый sync пишет новый пароль, а запущенный Valkey держит старый — после рестарта AMP или Valkey они расходятся (`WRONGPASS` → in-memory). Сейчас закрыто документацией (`values.yaml`, README § Cache). Вариант: генерировать пароль один раз pre-install Job'ом (рендер без кластера от рендера на новой установке шаблон не отличит).
  - _(2026-10-20, AMP-CACHE-NOAUTH B5)_ смена пароля Valkey через `helm upgrade` (`valkey.password.value` или содержимое `existingSecret`) не рестартует поды: checksum есть только на ConfigMap и `<fullname>-secrets`. Valkey держит старый пароль, новые поды AMP берут новый. README требует `rollout restart` обоих. Fix: аннотация `checksum/valkey-password` на pod template Valkey и AMP (для `value`; для `existingSecret` — только документация).
- **Impact:** операторы, включающие `existingSecret` или выключающие valkey флагом, получают не то, что заявлено в values.
- **Fix:** гейтить Redis по `valkey.enabled`, провести `existingSecret` через `amp.postgresql.secretName`; убрать `migration.backupBeforeMigration`; NOTES — port-forward на под по меткам. HA Postgres — не делаем (ADR-016).
- **Refs:** `helm/amp/templates/{redis-statefulset,secret,postgresql-secret,postgresql-statefulset}.yaml`
- **Status:** open

### [medium][Config][~0.5d] CONFIG-GLOBAL-VIPER-STATE
- **Title:** `LoadConfig` на глобальном viper держит значения прошлого файла
- **Problem:** `config.LoadConfig` работает на глобальном `viper`. Отсутствующий файл не ошибка (PROD-CONFIG-FILE-FALLBACK), но `ReadInConfig` при этом не сбрасывает прочитанное ранее: повторный `LoadConfig` после исчезновения файла вернёт старые значения и `Routing == nil` без ошибки. Старт не затронут; reload проверяет файл до вызова (`ReloadCoordinator.loadAndParse`), остаётся окно TOCTOU между `os.ReadFile` и `LoadConfig` — при атомарной смене ConfigMap на практике недостижимо. Инвариант записан комментарием в `LoadConfig`.
- **Impact:** любой новый вызов `LoadConfig` вне старта может молча получить смесь старого и нового конфига; тесты вынуждены звать `resetViper`.
- **Fix:** `viper.New()` на каждый вызов (или reload передаёт уже прочитанные байты).
- _(FU-TOPLEVEL-INHIBIT-RULES S1, 2026-10-12)_ тот же корень: один reload читает файл 3–4 раза (`reload_coordinator.loadAndParse`, viper, `loadInhibitRulesYAML`, `loadRouteConfig`); подмена symlink ConfigMap между чтениями может склеить две версии. Фикс «прочитать байты один раз и передать дальше» закрывает и это.
- **Refs:** `go-app/internal/config/config.go` (`LoadConfig`, `loadRouteConfig`, `loadInhibitRulesYAML`), `reload_coordinator.go`; `tasks/archive/PROD-CONFIG-FILE-FALLBACK/review-findings.md` F5; `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/review-findings.md` S1.
- **Status:** open

### [medium][Security][~0.5d] CONFIG-VALIDATION-ERROR-REDACTION
- **Title:** ошибки валидации конфига печатают URL с секретами
- **Problem:** сообщения валидаторов (E114/E116, проверка `external_url`) эхом печатают значение: Slack webhook URL (это credential), userinfo в `external_url`. С PROD-CONFIG-FILE-FALLBACK невалидный конфиг — ERROR `failed to load configuration` и exit 1, т. е. строка повторяется на каждом рестарте crash loop. Не регресс: раньше то же уходило в WARN. Ошибки YAML и mapstructure значений не печатают.
- **Impact:** секреты ресиверов попадают в логи пода и в агрегатор логов.
- **Fix:** редактировать userinfo, query и path URL в сообщениях валидаторов (показывать схему и хост).
- _(HELM-EXTRA-VOLUMES R2-F6, 2026-10-20)_ подтверждено на `pkg/configvalidator/validators/receiver.go`: `invalid Slack API URL: %s`, `invalid webhook URL: %s`, `invalid PagerDuty URL: %s` (E116/E114/E123) печатают inline-значение, в k3d — в строке `failed to load configuration`. Путь `*_file` эти проверки обходит (валидатор файл не читает).
- **Refs:** `go-app/internal/config`, `cmd/server/main.go`; review-findings F6.
- **Status:** open

### [medium][Grouping][~1d] TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE
- **Title:** пропажа ключа таймера в Redis глушит живой локальный таймер
- **Problem:** после `GROUPING-TIMER-LOCK-FIX` срабатывание сверяется с записью в timer storage, и отсутствие записи значит «уже обработано» (`fireStillDue`, `not_found`). Если ключ пропал не из-за обработчика — eviction (`allkeys-lru`), `FLUSHDB`, failover на отстающую реплику Redis, — локальный таймер тоже пропускает срабатывание, а reconciliation ключа не видит. Различить «удалён обработчиком» и «потерян» без отдельного маркера нельзя. У group storage есть failback (`StorageManager`), у timer storage — нет (`service_registry.go:1897-1911`).
- **Impact:** группа молчит до следующего алерта в ней; только HA-режим с Redis. _(PROD-GROUPING-DEFAULT, 2026-10-10: следующий алерт перевзводит таймер через `ensureGroupTimer`; группировка теперь включена по умолчанию, а Redis чарта по умолчанию `allkeys-lru` — см. `HELM-REDIS-NOEVICTION` в BACKLOG.)_
- **Fix:** маркер обработанного срабатывания (например, `fired:{groupKey}:{expiresAt}` с TTL) вместо отсутствия записи, либо требование `noeviction` для Redis AMP в чарте и доках.
- **Refs:** `go-app/internal/infrastructure/grouping/timer_manager_impl.go` (`fireStillDue`, `skipHandledFire`); `tasks/archive/GROUPING-TIMER-LOCK-FIX/review-findings.md` F6.
- **Status:** open

### [medium][Grouping][~1d] NOTIFY-LOG-FALLBACK-LIMITS
- **Title:** ограничения `resilientNotifyLog` при недоступном журнале нотификаций
- **Problem:** обёртка над `RedisNotifyLog` при ошибке чтения отвечает из локальной памяти реплики. (1) Локальная запись может быть устаревшей: другая реплика успела отправить более новый набор — ответ «дубликат» придерживает target, покрытым он не считается, prune не выполняется (это намеренно, Spec п.8, 10). (2) При сбое дольше `repeat_interval` группа с несколькими target'ами не оседает: локальные записи истекают в разное время, каждый раз один target получает fail-open отправку, другой придержан — resolved-алерт уходит каждому target'у раз в `repeat_interval`, пока журнал не читается. (3) Нет метрики состояния fallback — видно только по Warn-логу.
- **Impact:** при длительной недоступности Redis — повторы resolved раз в `repeat_interval` на target; после восстановления первый flush удаляет алерт. Оператор не видит по метрикам, что дедуп работает из локальной памяти.
- **Fix:** gauge/counter «nflog отвечает локально»; для (2) — общий срок годности локальных записей группы либо отказ от fail-open для resolved-only набора.
- **Refs:** `go-app/internal/infrastructure/grouping/notify_log_fallback.go`; `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` K-серия, M1, N1; `Spec.md` Edge Cases 19–20.
- **Status:** open

### [medium][Grouping][~0.5d] GROUP-CLEANUP-NOT-WIRED
- **Title:** `CleanupExpiredGroups` не вызывается в проде
- **Problem:** `CleanupExpiredGroups`/`RemoveAlertFromGroup` вне тестов не вызываются; авто-резолва нет. Группа, чьи resolved-алерты не удаётся доставить (или алерт без resolved), тикает каждый `group_interval` бессрочно — в HA ~10–12 round-trip'ов Redis на тик; алерт без resolved напоминает каждый `repeat_interval`. По чтению кода.
- **Impact:** рост числа групп и фоновой нагрузки на Redis со временем; с группировкой по умолчанию касается всех установок с `route:`.
- **Fix:** периодический вызов `CleanupExpiredGroups` из timer manager/registry с порогом по `UpdatedAt`; тест на снятие таймера вместе с группой.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` F8, G7.
- **Status:** open

### [medium][Helm][~0.5d] PG-LIVENESS-UNDER-CPU-SATURATION
- **Title:** liveness-проба встроенного PostgreSQL перезапускает его при насыщении CPU-лимита
- **Problem:** k3d 2026-10-19, `values-small.yaml` (PG limit 500m): 52 сессии с тяжёлыми сортировками упёрли PG в CPU-лимит, проба (`pg_isready`, таймаут чарта) не уложилась, kubelet перезапустил контейнер — crash recovery, AMP переподключился. Не OOM (`oom_kill=0`). Проба меряет «успел ли ответить под троттлингом», а не «жив ли процесс».
- **Impact:** пик нагрузки на маленькой ноде превращается в рестарт БД и окно ошибок ingest (BACKLOG `INGEST-WITHOUT-SYNC-PG`).
- **Fix:** liveness мягче readiness (больший `timeoutSeconds`/`failureThreshold`) или только readiness + startupProbe; замер тем же сценарием (`evidence/k3d-acceptance.md` § A4).
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/k3d-acceptance.md` § A4.
- **Status:** open

<!-- Code review 2026-10-21 (`docs/06-planning/CODE-REVIEW-2026-10-21.md`). Пути — от `go-app/`. Удаление мёртвого кода и консолидация — только `refactor:`-PR без изменения поведения. -->

### [medium][Pipeline][~2d] LABELMATCH-PKG
- **Title:** семантика и грамматика label-matcher'ов реализованы 3–5 раз
- **Problem:** match/anchoring `^(?:…)$`/absent label — в `core/silencing/matcher_impl.go:242`, `infrastructure/inhibition/matchers_list.go:111` и `matcher_impl.go:309-365`, `business/routing/matcher.go:240-283`, `application/handlers/matchers.go`, `pkg/configvalidator/matcher`; грамматика выражений — в `business/routing/tree_builder.go:237,336,411`, `pkg/configvalidator/matcher/matcher.go:116,168,256`, `application/handlers/matchers.go:28`. Комментарии «third copy», «FIFTH copy». `isValidLabelName` ×6, `RegexCache` ×2; LRU в `business/routing/matcher_cache.go:128-140` берёт write-lock на каждом hit.
- **Impact:** расхождение семантики чинилось 3–5 раз; `API-FILTER-MATCHER-GRAMMAR` — следствие.
- **Fix:** `pkg/labelmatch` (`Parse`, `Matcher.Matches`, `ValidLabelName`) + общий conformance-тест по кейсам upstream; потребителей переводить по одному PR; regex компилировать при build дерева, LRU убрать. Делать после `DEAD-PKGS-NO-IMPORTERS`/`DEAD-APPLICATION-MIDDLEWARE` (меньше копий).
- **Refs:** CODE-REVIEW-2026-10-21 D-1, D-2, L-11, R-2. Смежно `FU-MATCHER-LABEL-CHARSET`.
- **Status:** open

### [medium][Observability][~1d] METRICS-V2-ONLY
- **Title:** три системы метрик, часть `/metrics` — вечные нули
- **Problem:** `pkg/metrics/metrics.go` (v1), `pkg/metrics/v2/registry.go:82-121`, `internal/database/postgres/{metrics,prometheus}.go`. Сеттеры v2 Database/Cache не вызываются ⇒ нули; v1 и v2 регистрируют одинаковые `alert_history_database_*` ⇒ panic при создании v1 рядом с v2.
- **Impact:** дашборды по БД/кешу врут; скрытый panic для следующего, кто подключит v1.
- **Fix:** Business/Filter/Webhook перенести в v2, мёртвые группы удалить, пул — `GaugeFunc` от `pgxpool.Stat()`. После `DEAD-PKGS-NO-IMPORTERS`.
- **Refs:** CODE-REVIEW-2026-10-21 M-26, D-10, R-5.
- **Status:** open

### [medium][Config][~1.5d] CONFIG-SCHEMA-PARITY
- **Title:** схема Alertmanager-конфига описана трижды, inhibit-правило — пятью типами
- **Problem:** `internal/alertmanager/config/config.go`, `internal/infrastructure/routing/{config,receiver,global}.go`, `internal/config/config.go:193`; валидатор проверяет не ту модель, что исполняет рантайм.
- **Impact:** уже дало `PAGERDUTY-SERVICE-KEY-ONLY-REJECTED`, E024 telegram, `W300-DELIVERABLE-SECRETS`.
- **Fix:** минимум — reflection-тест паритета yaml-тегов; цель — валидатор по `routing.RouteConfig`, остальные модели удалить. После `DURATION-UPSTREAM-UNITS` и `DEAD-CONFIG-SUBSYSTEMS`.
- **Refs:** CODE-REVIEW-2026-10-21 M-21, D-5, R-4.
- **Status:** open

### [medium][Inhibition][~1d] INHIBIT-MATCH-PERF
- **Title:** `ShouldInhibit` — O(R·F) с копией всего L1 на каждый вызов
- **Problem:** `inhibition/matcher_impl.go:73-169` копирует все firing-источники на каждый вызов; `grouping/manager_impl.go:1166-1201` вызывает его для каждого алерта группы; совпадения логируются на INFO.
- **Impact:** стоимость отправки группы растёт как размер группы × число источников; шум в логах.
- **Fix:** индекс источников по правилу, обновляемый в `AddFiringAlert`; один снапшот на `filterInhibited`; логи → DEBUG. Бенчмарк до/после. После `INHIBIT-SOURCE-CACHE-EVICTION`.
- **Refs:** CODE-REVIEW-2026-10-21 M-6.
- **Status:** open

### [medium][Observability][~0.5d] HTTP-METRICS-ACCESS-LOG
- **Title:** у HTTP-сервера нет access-лога, request-id и метрик
- **Problem:** `cmd/server/main.go:136-155` собирает mux без middleware; готовые `pkg/logger.LoggingMiddleware` и HTTP-метрики не подключены.
- **Impact:** латентность и 5xx на ingest не видны.
- **Fix:** обёртка `instrument(handler, name)` на `promhttp.InstrumentHandlerDuration`/`Counter` со статической меткой handler + access-лог с request-id (sketch — CODE-REVIEW § 5).
- **Refs:** CODE-REVIEW-2026-10-21 M-14.
- **Status:** open

### [medium][Grouping][~2.5d] NOTIFY-PIPELINE-EXTRACT
- **Title:** пакет grouping содержит DSL конфига, keygen, два storage, таймеры, nflog и весь notify chain
- **Problem:** `internal/infrastructure/grouping/manager_impl.go` — 2175 строк, `publishGroupAlerts` — 438 строк; ~44% строк — комментарии с историей fix-round'ов.
- **Impact:** любая правка notify chain требует держать в голове весь файл; тестировать стадии по отдельности нельзя.
- **Fix:** вынести notify chain в стадии (`filterInhibited`, `filterSilenced`, dedup/nflog, send) с интерфейсом стадии, как в upstream `notify.Stage`; историю из комментариев — в git/ADR. Нарезать на слайсы ≤1d. После семантических багов grouping/inhibition и `LABELMATCH-PKG`.
- **Refs:** CODE-REVIEW-2026-10-21 M-24, R-6, R-9.
- **Status:** open

### [medium][App][~2d] SERVICE-REGISTRY-SPLIT
- **Title:** `ServiceRegistry` — god-object wiring всех подсистем
- **Problem:** `internal/application/service_registry.go` — 2763 строки: инициализация, reload, shutdown и health всех подсистем в одном типе.
- **Impact:** reload и порядок инициализации трудно рассуждать и тестировать (`RELOAD-SERIALIZE` — следствие).
- **Fix:** по файлу/типу на подсистему с явными `Init/Reload/Close`; registry — только композиция. После `RELOAD-SERIALIZE` и `DEAD-APPLICATION-MIDDLEWARE`.
- **Refs:** CODE-REVIEW-2026-10-21 M-24, R-7.
- **Status:** open

### [medium][Gate][~1d] LAYER-GATE-DEPGUARD
- **Title:** business-слой импортирует infrastructure; линтер этого не ловит
- **Problem:** `business/routing/tree_builder.go:180` → `infrastructure/grouping.Route`; `business/silencing` → `infrastructure/silencing`. В `.golangci` нет `depguard`, `bodyclose`, `gosec`, `noctx`.
- **Impact:** инверсия слоёв растёт незаметно; часть находок ревью (G112, тела ответов, запросы без ctx) поймал бы линтер.
- **Fix:** перенести `Route`/`Duration` и интерфейс репозитория в core/business; `depguard` с правилами слоёв + `bodyclose`/`gosec`/`noctx` в гейт. После удаления мёртвого кода (меньше ложных срабатываний).
- **Refs:** CODE-REVIEW-2026-10-21 M-25, R-8. Смежно `AMP-CORE-IMPORTS-INFRASTRUCTURE-NO-LAYER-GATE`.
- **Status:** open

### [medium][Deps][~0.5d] DEPS-CLIENT-GO-BUMP
- **Title:** `k8s.io/client-go v0.29.0` вне поддержки, go.mod не tidy
- **Problem:** `go.mod:38-40`.
- **Impact:** нет security-фиксов client-go; дрейф графа зависимостей.
- **Fix:** поднять client-go отдельным PR (карантин 7 дней — проверить дату версии), `go mod tidy -diff` в гейт.
- **Refs:** CODE-REVIEW-2026-10-21 M-29. Смежно `GO-MOD-TIDY-CHECK`.
- **Status:** open

### [medium][App][~0.5d] DEAD-PKGS-NO-IMPORTERS
- **Title:** пакеты без единого импортёра
- **Problem:** `pkg/templatevalidator` (+ подпакеты, 4.6k), `pkg/aiclassifier` (549), `pkg/telemetry` (322), `pkg/middleware`, `internal/business/template`, `internal/storage` (placeholder) — не входят в `go list -deps ./cmd/...`.
- **Impact:** ложная поверхность для ревью и поиска, лишние зависимости в go.mod.
- **Fix:** удалить, `go mod tidy`. Проверка: `go build ./... && go list -deps ./cmd/... | grep -E 'templatevalidator|aiclassifier|pkg/telemetry|pkg/middleware'` пусто. Можно начинать сразу.
- **Refs:** CODE-REVIEW-2026-10-21 § 4.4. Закрывает `DEAD-PKG-TELEMETRY`.
- **Status:** open

### [medium][Publishing][~0.5d] DEAD-TEMPLATE-STACK
- **Title:** второй, неподключённый стек шаблонов
- **Problem:** `internal/infrastructure/template`, `internal/notification/template` (живой только `defaults/email.go` в fallback-пути), миграция таблиц `templates`, прямая зависимость sprig (~4.5k строк).
- **Impact:** две системы шаблонов, одна мёртвая; fallback через неё — источник `EMAIL-RENDER-ESCAPING`.
- **Fix:** удалить после `EMAIL-RENDER-ESCAPING` (тот убирает fallback-путь).
- **Refs:** CODE-REVIEW-2026-10-21 § 4.4.
- **Status:** open

### [medium][Publishing][~0.5d] DEAD-PUBLISHING-SURFACE
- **Title:** ~7k строк неподключённого кода publishing
- **Problem:** enhanced webhook stack, DLQ (`queue_dlq.go`), `middleware.go`, `middleware_cache.go`, `tracing.go`, `lru_cache.go`, `validator.go`, `metrics.go`, второй `refresh.go`, `trends_detector.go`, `timeseries_storage.go`, `stats_collector_parallel.go`; `refresh_test_utils.go` импортирует `testing` в prod.
- **Impact:** непонятно, какой путь живой; `testing` в бинаре.
- **Fix:** удалить по `go list -deps` и `deadcode`; отдельный `refactor:`-PR.
- **Refs:** CODE-REVIEW-2026-10-21 § 4.4. Смежно `FU-UNWIRED-PUBLISH-SURFACES`.
- **Status:** open

### [medium][Config][~0.5d] DEAD-CONFIG-SUBSYSTEMS
- **Title:** мёртвые подсистемы конфига и ключи, которые парсятся, но не читаются
- **Problem:** `internal/config/{update_service,update_storage,service,sanitizer,example}.go`, `LoadConfigFromEnv`, ветки lock/backup/audit в `atomicApply`, `pkg/configvalidator/{parser,result,options}`. Ключи без читателей (`config.go:271-530`): `server.*_timeout` (таймауты захардкожены в `main.go:149-155`), `webhook.signature/rate_limiting/cors/max_*`, `app.max_workers`, `metrics.port/path`, `cache.*`, `lock.*`, `retry.*`, `telemetry.*`, `http_client.*`.
- **Impact:** ложное ощущение настройки; хуже всего `webhook.signature.enabled`.
- **Fix:** код удалить; ключи — подключить или удалить с WARN «not enforced» (breaking для указавших их — CHANGELOG с migration note).
- **Refs:** CODE-REVIEW-2026-10-21 M-23, § 4.4. Смежно `DEAD-WEBHOOK-SECURITY-CONFIG`.
- **Status:** open

## Low

### [low][Grouping][~0.5d] GROUPING-NEW-GROUP-TIMER-REARM-WINDOW
- **Title:** ложный re-arm `group_wait` в окне между `Store` новой группы и стартом её таймера
- **Problem:** с ROUTE-TIMINGS-BUNDLE таймер новой группы стартует после `storage.Store` (`go-app/internal/infrastructure/grouping/manager_impl.go:313`), чтобы нулевой `group_wait` не читал группу раньше записи. Второй алерт той же группы (или другая реплика в HA), пришедший в это окно, видит группу без таймера, и `ensureGroupTimer` (`manager_impl.go:940`) взводит `group_wait` сам: Warn «alert group has no timer scheduled, re-arming group_wait» и метрика `timer_rearm`. Корректность не страдает (исходный `StartTimer` заменяет таймер, в HA лишний fire отсекают lock/`fireStillDue`), но при `group_wait: 0s` возможен лишний немедленный flush.
- **Impact:** на всплесках алертов — ложные предупреждения и шум в метрике, которая должна сигналить о реальной потере таймера.
- **Fix:** взводить таймер под той же per-key сериализацией, что и создание группы (BUGS `GROUPING-NEW-GROUP-CREATE-RACE`), либо не считать поводом для re-arm отсутствие таймера у группы младше `CreatedAt + ε`.
- **Refs:** `tasks/archive/ROUTE-TIMINGS-BUNDLE/review-findings.md` § F2.
- **Status:** open

### [low][Test][~0.25d] GO-TESTS-REDIS-IMAGE
- **Title:** Go-интеграционные тесты поднимают `redis:7-alpine` (Redis 7.4, RSALv2/SSPL)
- **Problem:** `go-app/internal/infrastructure/inhibition/integration_test.go:24` через testcontainers; чарт и e2e-ha с 2026-10-19 на `valkey/valkey:8.1.10-alpine` (ADR-017). В поставку не попадает, но тесты проверяют не тот кеш, что ставит чарт.
- **Fix:** образ `valkey/valkey:8.1.10-alpine` в тесте; прогон пакета.
- **Refs:** ADR-017; `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/tasks.md` (вне scope).
- **Status:** open

### [low][Config][~0.1d] CONFIG-STANDARD-REPLICAS-TEXT
- **Title:** `config.go` описывает standard-профиль как «2-10 replicas»
- **Problem:** `go-app/internal/config/config.go:236,1575`; с 2026-10-19 чарт по умолчанию ставит одну реплику без HPA, HA — `values-production.yaml`.
- **Fix:** поправить текст (standard = PostgreSQL + Valkey, число реплик задаёт чарт).
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/tasks.md` (вне scope).
- **Status:** open

### [low][Config][~0.25d] INHIBIT-CONFIG-LOUDNESS
- **Title:** мелкие шероховатости загрузки inhibit-правил
- **Problem:**
  - Индексы ошибок валидатора для вложенных правил сдвигаются, если есть верхнеуровневые: правило `inhibition.inhibit_rules[j]` репортится как `inhibit_rules[len(top)+j]` (`go-app/internal/config/alertmanager_validation.go:101-105`) (F6).
  - Ключи секции в другом регистре (`Inhibition:`, `Inhibit_Rules:`, `Source_Match:`) yaml.v3 не узнаёт, и правила под ними пропадают молча (`config.go`, `loadInhibitRulesYAML`). Upstream такие ключи отвергает как неизвестные; в CHANGELOG — migration note (R2 N2).
  - `reload_coordinator.go:645-646` маппит `inhibit_rules`/`inhibition` в компонент, а diff отдаёт имена Go-полей (`InhibitRules`/`Inhibition`) ⇒ `affected_components` для ингибиции всегда пуст. На применение не влияет: правила пересобираются на каждом reload (F7).
  - Карт «секция → компонент» две: `reload_coordinator.go:641-655` и `update_diff.go:295-322`; `affected_components` пуст и для правок маршрутов (CODE-REVIEW-2026-10-21 L-24, D-14).
- **Impact:** вводящие в заблуждение путь в ошибке, `affected_components` и молча пропущенная секция.
- **Fix:** валидировать списки раздельно (или перенумеровывать в `formatConfigValidatorErrors`); ошибка на ключ, совпадающий с известным без учёта регистра; одна функция маппинга компонентов по mapstructure/yaml-имени.
- **Refs:** `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/review-findings.md` F6, F7, N2; CODE-REVIEW-2026-10-21 L-24.
- **Status:** open

### [low][Grouping][~0.25d] TIMER-ACTIVE-GAUGE-DRIFT
- **Title:** `alert_history_timer_active_total` расходится с числом таймеров
- **Problem:** `Inc`/`Dec` гейджа не сбалансированы на нескольких путях `DefaultTimerManager`: `StartTimer` заменяет существующий handle без `Dec` (`timer_manager_impl.go:441-445`), и если сработавший handle заменён внешним `StartTimer` во время проверки записи, `Inc` старого не компенсируется; ветки `gm == nil` и `GroupNotFound` в `onTimerExpired` удаляют handle без `Dec`; срабатывание с `nil`-handle (restore/reconcile) делает `Dec` без `Inc`. Ранний выход через `dropLocalHandle` сбалансирован в `GROUPING-TIMER-LOCK-FIX`.
- **Impact:** гейдж годится только для тренда; на доставку не влияет. _(PROD-GROUPING-DEFAULT G7: ещё один путь — удаление группы из callback'а даёт двойной `DecActiveTimers`, гейдж уходит в минус.)_
- **Fix:** считать гейдж от `len(tm.timers)` (GaugeFunc) вместо `Inc`/`Dec` по путям.
- **Refs:** `tasks/archive/GROUPING-TIMER-LOCK-FIX/review-findings.md` N3, F2.
- **Status:** open

### [low][Config][~0.1d] FILE-SECRET-READ-ERROR-HINT
- **Title:** на нечитаемый `*_file` подсказка говорит про взаимоисключение
- **Problem:** `resolveFileSecret` (`go-app/internal/infrastructure/routing/file_secrets.go`) для обеих ошибок — «задано и значение, и файл» и `failed to read secret file "<path>"` — даёт suggestion «Set either X or X_file, not both».
- **Impact:** при неверном `mountPath`/имени ключа Secret подсказка уводит диагностику в сторону.
- **Fix:** отдельная подсказка для ошибки чтения (проверить путь, mount, права); тест на текст.
- **Refs:** `tasks/archive/HELM-EXTRA-VOLUMES/review-findings.md` F12.
- **Status:** open

### [low][Config][~0.1d] CONFIG-PATH-RESOLUTION-DUP
- **Title:** путь конфига разрешается в двух местах по-разному
- **Problem:** `cmd/server` берёт `AMP_CONFIG_FILE` с `TrimSpace` (`resolveRuntimeConfigPath`), reload в `service_registry.go:327-330`, `:2642-2645` — без. Значение из пробелов или с пробелом в начале старт и reload разрешают по-разному. Существовало до PROD-CONFIG-FILE-FALLBACK.
- **Impact:** редкий: reload читает другой файл, чем старт.
- **Fix:** передавать в `ServiceRegistry` путь, уже разрешённый в `main`.
- **Refs:** review-findings F8.
- **Status:** open

### [low][Gate][~0.1d] RELEASE-GATE-UNQUOTED-ARGS
- **Title:** `scripts/release-gate.sh` собирает аргументы helm строкой без кавычек
- **Problem:** `args="-f $HELM_CHART_DIR/values-dev.yaml"` и `-f $HELM_CHART_DIR/tests/values-production-placeholders.yaml` раскрываются word-split'ом: путь с пробелом ломает шаг, `[`/`*` в значениях подвержены glob-раскрытию (поэтому placeholder'ы PROD-INGRESS-HARDENING ушли в файл, а не в `--set-json`).
- **Impact:** чекаут в каталоге с пробелом роняет helm-шаги гейта; следующий `--set` со спецсимволами — тихая порча аргументов.
- **Fix:** bash-массивы аргументов в `step_helm_values`/`step_helm_rbac`.
- **Refs:** deep-review PROD-INGRESS-HARDENING R14; 2026-10-01.
- **Status:** open

### [low][Gate][~0.1d] RELEASE-GATE-SMOKE-SHARED-PROJECT
- **Title:** два одновременных `scripts/release-gate.sh` ломают друг другу `amtool-compat`
- **Problem:** `step_amtool_compat` поднимает `deploy/smoke` под фиксированным compose-проектом `amp-smoke` и портом `18080`, а в начале шага делает `docker compose down -v`. Второй прогон (другая сессия/worktree) снимает или подменяет стек первого: health-check проходит по чужому контейнеру, затем `amtool` — `connection refused`.
- **Impact:** ложный FAIL гейта при параллельной работе нескольких сессий; под той же нагрузкой на Docker testcontainers-reaper ловит `unexpected container status "removing"` в шаге `test`.
- **Fix:** уникальный `COMPOSE_PROJECT_NAME` (например, с PID) и свободный порт на прогон, либо lock-файл на шаг.
- **Refs:** SLACK-WEBHOOK-PLAIN-OK-RESPONSE, `tasks/archive/SLACK-WEBHOOK-PLAIN-OK-RESPONSE/evidence/release-gate-run1.md`; 2026-10-20.
- **Status:** open

### [low][Grouping][~0.5d] GROUP-HASTIMER-REDIS-GET-PER-ALERT
- **Title:** `HasTimer` делает Redis GET на каждый алерт у реплики без локального handle
- **Problem:** `ensureGroupTimer` спрашивает `HasTimer`; у реплики, не владеющей таймером группы, это поход в Redis — в HA (N−1)/N алертов. При ошибке GET пишутся Warn и Error на каждый алерт, без rate-limit.
- **Impact:** лишний round-trip на ingest-пути и шум в логах при сбое Redis; на корректность не влияет.
- **Fix:** короткий локальный кэш «таймер есть» на группу; rate-limit логов ошибки.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H9; `go-app/internal/infrastructure/grouping/manager_impl.go` (`ensureGroupTimer`), `timer_manager_impl.go` (`HasTimer`).
- **Status:** open

### [low][Grouping][~0.5d] LEGACY-REPEAT-TIMER-AFTER-UPGRADE
- **Title:** legacy-таймер `repeat_interval` после апгрейда держит задержку до своего срока
- **Problem:** группа, пережившая апгрейд с таймером типа `repeat_interval`, считается «с таймером» (`HasTimer` = true), перевзвод не срабатывает; `RestoreTimers` такой таймер в `group_interval` не конвертирует — timer manager не знает `group_interval` группы. Новый алерт в такой группе ждёт остаток `repeat_interval` (до 4h), один раз на группу.
- **Impact:** разовая задержка нотификации после апгрейда HA-установки с живыми группами; названо в migration note п.10.
- **Fix:** при `RestoreTimers` сжимать legacy-таймер до `min(остаток, group_interval)` — нужен доступ к параметрам маршрута группы.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H7.
- **Status:** open

### [low][Grouping][~0.5d] REDIS-GROUP-STORAGE-UNLOCKED-WRITES
- **Title:** `RedisGroupStorage.Store`/`Delete` без блокировки группы и проверки версии
- **Problem:** `RedisGroupStorage.Store` делает `json.Marshal` и `Version++` без `group.mu` (в `MemoryGroupStorage.Store` это исправлено в PROD-GROUPING-DEFAULT); `storage.Delete` не проверяет версию, поэтому проверка «алерт всё ещё resolved» перед prune атомарна только в пределах объекта группы одной реплики. Пробой не воспроизведён: в Redis-режиме объект группы обычно свой у каждого вызова.
- **Impact:** теоретическая гонка между репликами: prune может удалить группу, в которую другая реплика только что добавила алерт.
- **Fix:** `Store` под `group.mu.RLock`; `Delete` с проверкой версии (Lua/WATCH).
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` M3, M4; `Spec.md` п.10 (оговорка про Redis).
- **Status:** open

### [low][Config][~0.25d] GROUPING-RELOAD-NO-RESTART-HINT
- **Title:** `/-/reload` с изменённым `grouping.*` не сообщает «restart required»
- **Problem:** `grouping.enabled` и остальные ключи `grouping.*` читаются один раз при старте; `reload_coordinator.go` числит `grouping` компонентом, но изменение этих ключей на reload молча не применяется и предупреждения W6xx нет. Предсуществующее.
- **Impact:** оператор меняет `grouping.enabled` в файле, делает reload и считает, что режим сменился.
- **Fix:** предупреждение W6xx «restart required» при изменении `grouping.*` на reload.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` F11; `go-app/internal/config/reload_coordinator.go`.
- **Status:** open

### [low][Config][~0.25d] DB-CONN-STRING-UNESCAPED
- **Title:** `ConnectionString()` и `config.GetDatabaseURL()` собирают строку подключения без экранирования
- **Problem:** `PostgresConfig.ConnectionString()` (`internal/database/postgres/config.go`, key=value через `fmt.Sprintf`) и `Config.GetDatabaseURL()` (`internal/config/config.go`, URL через `fmt.Sprintf`) подставляют пользователя и пароль как есть — тот же дефект, что был в `DSN()` до HELM-EXTERNAL-DATABASE: пароль с пробелом, `'`, `/ # ? %` ломает разбор или молча меняется. Дополнительно `GetDatabaseURL()` подставляет `sslmode=disable` при пустом значении, тогда как рантайм по умолчанию берёт `require`. В рантайме не вызываются: единственные вызовы — `internal/config/example.go:94` и `pool_test.go:322`.
- **Impact:** сейчас нет; ловушка для следующего, кто возьмёт эти функции вместо `DSN()`.
- **Fix:** удалить обе (и пример/тест) либо собрать через `url.URL`, как `DSN()`.
- **Refs:** `tasks/archive/HELM-EXTERNAL-DATABASE/research.md`, `evidence/dsn-password-escaping.txt`; 2026-10-16.
- **Status:** open

<!-- Code review 2026-10-21 -->

### [low][Silencing][~0.5d] DEAD-SILENCING-CACHE
- **Title:** второй кеш silences и прочие мёртвые хвосты
- **Problem:** `business/silencing` cache + sync worker + `IsAlertSilenced` (~1.5k, в бинаре) дублируют живой `memory.SilenceStore`. Плюс: мёртвые `internal/database/postgres/{retry,prometheus,example,errors}.go`, `CircuitBreakerHealthChecker`, `LoadFromEnv`, `PeriodicHealthChecker` (с гонкой); `lock/example.go`, `LockManager`, `grouping/parser.go`, `ValidateConfig`/`SanitizeConfig`, `fingerprintIndex`; `core.Cache`; `k8s.GetSecret`; `httperror.RetryStrategy`.
- **Impact:** дрейф состояний двух кешей (`SILENCE-SYNC-LIMIT-EXCEEDS-FILTER`); ложная поверхность.
- **Fix:** удалить; `lock/example.go` → `_test.go` (example-тест). `RunMigrationsDown` — в `MIGRATIONS-EMBED`.
- **Refs:** CODE-REVIEW-2026-10-21 D-11, § 4.4. Смежно `DB-CONN-STRING-UNESCAPED`.
- **Status:** open

### [low][Storage][~0.25d] DB-ORPHAN-TABLES
- **Title:** таблицы БД без читателей и писателей
- **Problem:** `filter_rules` (с seed), `alert_classifications`, `alert_publishing_history`, `system_metrics`, `migration_history` (`migrations/20250911094416_initial_schema.sql`), `config_versions`/`audit_log`/`backups`/`locks`. Живая только `alerts` (и таблицы silences/investigations).
- **Impact:** схема вводит в заблуждение (`HISTORY-API` строился на допущении, что история уже пишется).
- **Fix:** drop-миграция отдельным PR (Standard: схема БД); решение по `alert_classifications`/`alert_publishing_history` — вместе с `HISTORY-API`.
- **Refs:** CODE-REVIEW-2026-10-21 § 4.4, § 10.
- **Status:** open

### [low][Cache][~0.5d] REDIS-HELPERS
- **Title:** механические дубли вокруг Redis
- **Problem:** CAS-release Lua ×4 (`lock/distributed.go:139,180`, `grouping/redis_timer_storage.go:540`, `grouping/redis_notify_log.go:605`); ленивая чистка ZSET-индекса только в timer storage (`redis_timer_storage.go:452`); `redis_notify_log.go:283-330` — 4 последовательных round trip на таргет без атомарности.
- **Impact:** исправление в одной копии не доезжает до других (`REDIS-GROUP-INDEX-GHOSTS`).
- **Fix:** `var casDelete = redis.NewScript(...)`, общий helper «ZSET-индекс с ленивой чисткой», `TxPipelined` в nflog.
- **Refs:** CODE-REVIEW-2026-10-21 D-12, L-13, § 4.2.
- **Status:** open

### [low][Publishing][~0.5d] PUBLISH-CLIENT-PLUMBING
- **Title:** обвязка клиентов publishing повторена пять раз
- **Problem:** get-or-create с double-checked locking для 5 client maps (`publisher.go:342-361`); вложенные `select` с `time.After(100ms)` в воркере (`queue.go:815-850`) — high-задание ждёт до 100ms, таймер на итерацию; три `CircuitBreaker` (publishing, `core/resilience`, `llm`).
- **Impact:** копии расходятся (ключи кеша — `PUBLISH-PER-TARGET-STATE`).
- **Fix:** generic `cachedClient[T]` (после `PUBLISH-PER-TARGET-STATE`); неблокирующие проверки по приоритету + один блокирующий select; один breaker. После `PUBLISH-SINGLE-RETRY-LAYER`.
- **Refs:** CODE-REVIEW-2026-10-21 D-9, D-15, L-16, § 4.2.
- **Status:** open

### [low][Publishing][~0.5d] TEMPLATE-EXEC-CACHE
- **Title:** шаблон парсится заново на каждое поле каждого алерта
- **Problem:** `business/templating/template.go:380-420`: `Clone`+`Parse` на каждый вызов.
- **Impact:** CPU и аллокации на отправке растут с числом полей × алертов.
- **Fix:** LRU `expression → *Template`, сброс на reload; бенчмарк.
- **Refs:** CODE-REVIEW-2026-10-21 L-10.
- **Status:** open

### [low][Storage][~0.25d] MIGRATIONS-EMBED
- **Title:** миграции ищутся в FS по CWD; `RunMigrationsDown` откатывает не то
- **Problem:** `internal/database/migrations.go:108-140,190-215`: поиск каталога по CWD/`runtime.Caller`; `RunMigrationsDown(steps)` передаёт steps как целевую версию (`steps=1` откатит всё; вызовов нет).
- **Impact:** бинарь зависит от раскладки FS; ловушка для первого вызова Down.
- **Fix:** `//go:embed *.sql` в `goose.NewProvider`; Down удалить.
- **Refs:** CODE-REVIEW-2026-10-21 L-29.
- **Status:** open

### [low][Helm][~0.1d] HELM-AMP-SECCOMP
- **Title:** у пода AMP нет `seccompProfile`, токен SA монтируется без RBAC
- **Problem:** `helm/amp/values.yaml:51`, `templates/serviceaccount.yaml:13`: нет `seccompProfile: RuntimeDefault`; `automountServiceAccountToken: true` даже без RBAC.
- **Impact:** не проходит restricted PSS; лишний токен в поде.
- **Fix:** seccomp `RuntimeDefault`; automount только при `rbac.create && publishing.enabled` (или investigation k8s tool).
- **Refs:** CODE-REVIEW-2026-10-21 L-28. Digest-пины — `CI-SUPPLY-CHAIN`.
- **Status:** open

### [low][Config][~0.5d] RELOADER-WATCH-DEPENDENT-FILES
- **Title:** sidecar config-reloader не видит изменений зависимых файлов
- **Problem:** `cmd/config-reloader/hash.go:17` хеширует только основной конфиг: смена шаблонов, `*_file`-секретов, web config не триггерит reload.
- **Impact:** ротация секрета через Secret-файл требует ручного reload или рестарта.
- **Fix:** повторяемый `--watch-file`, хеш по набору; в чарте — пути из values.
- **Refs:** CODE-REVIEW-2026-10-21 L-23.
- **Status:** open

### [low][Test][~1d] TESTS-INTEGRATION-TAG
- **Title:** интеграционные тесты без build-тега, ожидания через `time.Sleep`
- **Problem:** тесты с Redis/Postgres/testcontainers идут в общем `go test ./...` и пропускаются по окружению; ожидания асинхронных событий через `time.Sleep`.
- **Impact:** flaky-тесты (часть записей `BUGS.md`) и медленный локальный прогон.
- **Fix:** build-тег `integration`, отдельная цель в гейте; `time.Sleep` → `require.Eventually`.
- **Refs:** CODE-REVIEW-2026-10-21 § 8.
- **Status:** open

## Entry Format

```markdown
### [priority][area][estimate] DEBT-SLUG
- **Title:** short title
- **Problem:** what makes maintenance risky
- **Impact:** why it matters
- **Fix:** likely direction
- **Refs:** code, issue, task, or review links
- **Status:** open | in-progress | blocked
```

## Bundle Format

```markdown
### AREA-CLEANUP-BUNDLE
- [priority][area][combined estimate] AREA-CLEANUP-BUNDLE
- **Combined verify:** <одна команда; exit 0 — вся пачка готова>
- **Members (ordered):**
  - [ ] DEBT-SLUG-ONE
  - [ ] DEBT-SLUG-TWO
```

Полные записи остаются у участников; приоритет родителя — максимум по участникам.
