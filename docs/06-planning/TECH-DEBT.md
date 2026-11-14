# TECH-DEBT

Работающее по контракту, но дорогое в сопровождении: плохая структура, хрупкий дизайн, нет тестов на рабочий код, слабая наблюдаемость. Если код нарушает свой контракт — это баг, место ему в `BUGS.md` (граница — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt).

Погашенный долг удаляется; отчёт — в `DONE.md`.

## Bundles

<!-- Мелкие записи одной подсистемы (одних и тех же файлов) — в одного claimable-родителя.
     В Queue `NEXT.md` идёт только slug бандла. См. docs/solo-kanban/artifact-contract.md § Bundles. -->

<!-- Code review 2026-10-21: участники — записи BUGS.md / TECH-DEBT.md. Пути verify — от корня репозитория. -->

<!-- ADR-019-V2-SLICING (2026-10-24): CONFIG-RELOAD-BUNDLE, API-COMPAT-BUNDLE, INHIBITION-LIFECYCLE-BUNDLE, REDIS-STORAGE-BUNDLE распущены —
     большинство участников в «Снимается переходом на upstream (ADR-019)»; CONFIG-ENV-KEYS-UNBOUND и SERVER-HARDENING остаются standalone в BUGS.md. -->

<!-- 2026-11-08, UPSTREAM-CORE-REMOVAL — obsolete: code removed in UPSTREAM-CORE-REMOVAL. Удалены секция «Снимается переходом на upstream (ADR-019)» (27 записей)
     и из Open: CACHE-RUNTIME-FAILFORWARD, CONFIG-VALIDATION-ERROR-REDACTION, HTTP-METRICS-ACCESS-LOG, DEAD-PKGS-NO-IMPORTERS, DEAD-CONFIG-SUBSYSTEMS,
     UPSTREAM-TARGETS-DISCOVERY-DEPENDENCY, E2E-HA-LEGACY-COVERAGE-GAP, CACHE-STARTUP-RETRY-ERROR-LOGS, REDIS-RELOAD-STARTUP-WAIT-DRIFT, GO-TESTS-REDIS-IMAGE,
     CONFIG-STANDARD-REPLICAS-TEXT, CONFIG-PATH-RESOLUTION-DUP, RELOADER-WATCH-DEPENDENT-FILES. Остаток мёртвого кода общей инфраструктуры — DEAD-SHARED-INFRA-CODE. -->

## Critical

## High

## Medium

### [medium][Helm][~2d] HELM-CHART-GAPS
- **Title:** расхождения шаблонов чарта с values
- **Problem:** найдено при аудите `values-production.yaml` (INF-B, 2026-08-20), не исправлено (слишком большой темплейт-скоуп для values-аудита). _(Сверено с кодом 2026-11-08 после UPSTREAM-CORE-REMOVAL: `cache.*` удалён, чарт — один StatefulSet `amp-upstream`, Service выбирает поды по `amp.appSelectorLabels`. Сняты как устаревшие: `migration.backupBeforeMigration` (ключа нет), R24 и пункт PG-CONFIG-FIX про селектор Service (port-forward `svc/<fullname>` из `NOTES.txt` попадает в под AMP), пункты AMP-CACHE-NOAUTH A3/B5 про расхождение пароля Valkey у AMP — AMP к Valkey не подключается; вернутся вместе с `UPSTREAM-AI-HA-DEDUP`.)_
  - ~~`valkey.enabled` мёртвый~~ _(закрыто 2026-11-09, `PROD-RELEASE-V010`: все `templates/redis-*.yaml` гейтятся `valkey.enabled`, дефолт `false`; `valkey.replicas: 0` при включённом Valkey по-прежнему останавливает только pod.)_
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`POSTGRES_PASSWORD` в StatefulSet PG, `DATABASE_PASSWORD` у AMP) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION)_ HA встроенного PostgreSQL решён ADR-016: встроенный — dev/pilot, single instance, `postgresql.replicas != 1` падает на рендере; прод — внешний PostgreSQL. Реализации HA в чарте не будет.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` перекрывал его на `""`; с `HELM-EXTERNAL-DATABASE` (2026-10-16) блока `postgresql:` в нём нет (внешняя БД), и релиз, вернувший встроенный PG через `postgresql.enabled: true`, наследует этот литерал, если не задаёт пароль сам (CHANGELOG Breaking). Инконсистентность зафиксирована здесь, не исправлена.
  - _(2026-10-16, HELM-EXTERNAL-DATABASE R2)_ guard на слабый пароль нет: `postgresql.enabled: true` под `environment: production` с `postgresql.password` = дефолту `values.yaml` (`secure_password_123`, `values.yaml:268`) рендерится. AMP в production отсекает только короткие (< 12) и пароли из списка `weakPasswords` (`go-app/internal/config/config.go:298`), дефолт чарта (19 символов) проходит. Фикс: `fail` в `postgresql-secret.yaml` при `environment: production` и пароле, равном дефолту базового `values.yaml`; render-тест.
  - _(2026-10-01, PROD-INGRESS-HARDENING R17; NetworkPolicy сабчарта ушла вместе с ним 2026-10-19)_ политики postgres/redis (`postgresql-networkpolicy.yaml`, `redis-networkpolicy.yaml`) хардкодят `namespaceSelector` `name: monitoring` / `name: kube-system` вместо автоматической метки `kubernetes.io/metadata.name`; `ingress.yaml:32-35` безусловно добавляет nginx sticky-аннотации (`affinity: cookie` и др.) к любым `ingress.annotations`.
  - _(2026-10-14, HELM-DEFAULTS-VALIDATE review F7)_ `postgresql.config.ssl` не bool-safe: `ssl: on` без кавычек или `--set postgresql.config.ssl=true` → `eq: incompatible types` на рендере; `ssl: "true"` даёт PostgreSQL `ssl = true`, а AMP — `ssl_mode: disable` и `allow_insecure_ssl_in_production` (теперь `templates/amp-config.yaml:35-39`). Фикс: сравнивать через `toString`, `fail` на всё, кроме `"on"`/`"off"`.
  - _(2026-10-18, PG-CONFIG-FIX R10)_ `checksum/config` pod'а PostgreSQL считается только от `postgresql-configmap.yaml`; ConfigMap exporter'а (`postgresql-exporter-configmap.yaml`) в него не входит — правка `queries.yaml` без смены конфига PG не рестартует sidecar (на k3d понадобился `kubectl rollout restart`).
  - _(2026-10-18, PG-CONFIG-FIX R12)_ AMP и exporter ходят во встроенный PG ролью `postgresql.username`, а она суперпользователь (её создаёт entrypoint образа) ⇒ `superuser_reserved_connections` ничего не резервирует: при насыщении пулов не войдут ни exporter, ни `kubectl exec psql`. Нужна отдельная непривилегированная роль для AMP и роль `pg_monitor` для exporter'а.
  - _(2026-10-18, PG-CONFIG-FIX)_ `postgresql.config: null` не рендерится (`nil pointer` в `postgresql-configmap.yaml`, `NOTES.txt:94-101`, теперь и `amp-config.yaml:35`) — и на `main`; условия `if .Values.postgresql.config` в StatefulSet мёртвые. Либо guard'ы, либо убрать условия и объявить блок обязательным.
  - _(2026-11-09, pre-release review `PROD-RELEASE-V010`)_ образ `postgres:16.15` захардкожен и в StatefulSet PG, и в init container `wait-for-postgres` AMP: зеркало для air-gapped установок не задать. Фикс: значение `postgresql.image` для обоих.
- **Impact:** операторы, включающие `existingSecret` или выключающие valkey флагом, получают не то, что заявлено в values; «выключенный» Valkey оставляет в кластере объекты и алерты.
- **Fix:** гейтить все `redis-*.yaml` по `valkey.enabled` (или по `valkey.replicas > 0`) и убрать мёртвый флаг; провести `existingSecret` через `amp.postgresql.secretName`. HA Postgres — не делаем (ADR-016).
- **Refs:** `helm/amp/templates/{redis-*,secret,postgresql-secret,postgresql-statefulset,amp-config}.yaml`; `tasks/archive/UPSTREAM-CORE-REMOVAL/review-findings.md` C6.
- **Status:** open

### [low][Supply chain][~0.25d] IMAGE-BASE-DIGEST-PIN
- **Title:** базовые образы `Dockerfile` закреплены тегом, не digest
- **Problem:** `golang:1.27.4-alpine` и `alpine:3.24` (`Dockerfile:7`, `:47`) — перезаписываемые теги; публичный образ `ghcr.io/ipiton/amp` собирается из того, что лежит под тегом в момент сборки.
- **Impact:** воспроизводимость и supply chain публичного образа; Dependabot/Renovate для digest не настроен.
- **Fix:** `FROM image:tag@sha256:...` и обновление digest через Renovate/Dependabot (`docker` ecosystem).
- **Refs:** pre-release review `PROD-RELEASE-V010` F10 (`tasks/archive/PROD-RELEASE-V010/review-findings.md`).
- **Status:** open

### [medium][Config][~0.5d] CONFIG-GLOBAL-VIPER-STATE
- **Title:** `LoadConfig` на глобальном viper держит значения прошлого файла
- **Problem:** `config.LoadConfig` работает на глобальном `viper`. Отсутствующий файл не ошибка (PROD-CONFIG-FILE-FALLBACK), но `ReadInConfig` при этом не сбрасывает прочитанное ранее: повторный `LoadConfig` после исчезновения файла вернёт старые значения без ошибки. _(2026-11-08, UPSTREAM-CORE-REMOVAL: `ReloadCoordinator`, `loadRouteConfig`, `loadInhibitRulesYAML` удалены вместе с ядром AMP; `amp-upstream` зовёт `LoadConfig` один раз на старте (`cmd/amp-upstream/amp.go`), reload конфига Alertmanager — upstream-ядро, слой AMP не перечитывает. Пункт S1 (3–4 чтения файла за reload) устарел; `IgnoredKeys` уже на своём `viper.New()`.)_ Сейчас дефект латентный.
- **Impact:** любой новый вызов `LoadConfig` вне старта (reload слоя AMP, второй конфиг) может молча получить смесь старого и нового конфига; тесты вынуждены звать `resetViper`.
- **Fix:** `viper.New()` на каждый вызов — дёшево, пока вызов один.
- **Refs:** `go-app/internal/config/config.go` (`LoadConfig`); `tasks/archive/PROD-CONFIG-FILE-FALLBACK/review-findings.md` F5.
- **Status:** open

### [medium][Helm][~0.5d] PG-LIVENESS-UNDER-CPU-SATURATION
- **Title:** liveness-проба встроенного PostgreSQL перезапускает его при насыщении CPU-лимита
- **Problem:** k3d 2026-10-19, `values-small.yaml` (PG limit 500m): 52 сессии с тяжёлыми сортировками упёрли PG в CPU-лимит, проба (`pg_isready`, таймаут чарта) не уложилась, kubelet перезапустил контейнер — crash recovery, AMP переподключился. Не OOM (`oom_kill=0`). Проба меряет «успел ли ответить под троттлингом», а не «жив ли процесс».
- **Impact:** пик нагрузки на маленькой ноде превращается в рестарт БД и окно ошибок ingest (BACKLOG `INGEST-WITHOUT-SYNC-PG`).
- **Fix:** liveness мягче readiness (больший `timeoutSeconds`/`failureThreshold`) или только readiness + startupProbe; замер тем же сценарием (`evidence/k3d-acceptance.md` § A4).
- **Refs:** `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/k3d-acceptance.md` § A4.
- **Status:** open

<!-- Code review 2026-10-21 (`docs/06-planning/CODE-REVIEW-2026-10-21.md`). Пути — от `go-app/`. Удаление мёртвого кода и консолидация — только `refactor:`-PR без изменения поведения. -->

### [medium][Observability][~1d] METRICS-V2-ONLY
- **Title:** три системы метрик, часть `/metrics` — вечные нули
- **Problem:** `pkg/metrics/metrics.go` (v1), `pkg/metrics/v2/registry.go:82-121`, `internal/database/postgres/{metrics,prometheus}.go`. Сеттеры v2 Database/Cache не вызываются ⇒ нули; v1 и v2 регистрируют одинаковые `alert_history_database_*` ⇒ panic при создании v1 рядом с v2.
- **Impact:** дашборды по БД/кешу врут; скрытый panic для следующего, кто подключит v1.
- **Fix:** Business/Filter/Webhook перенести в v2, мёртвые группы удалить, пул — `GaugeFunc` от `pgxpool.Stat()`. После `DEAD-SHARED-INFRA-CODE` (`DEAD-PKGS-NO-IMPORTERS` закрыт UPSTREAM-CORE-REMOVAL).
- **Refs:** CODE-REVIEW-2026-10-21 M-26, D-10, R-5.
- **Status:** open

### [medium][Gate][~1d] LAYER-GATE-DEPGUARD
- **Title:** business-слой импортирует infrastructure; линтер этого не ловит
- **Problem:** _(примеры обновлены 2026-11-08: `business/routing`, `business/silencing`, `infrastructure/{grouping,silencing}` удалены UPSTREAM-CORE-REMOVAL.)_ `business/publishing/discovery_impl.go` → `infrastructure/k8s`; `core/services` → `infrastructure/cache`, `infrastructure/llm`. В `.golangci.yml` нет `depguard`, `bodyclose`, `gosec`, `noctx`.
- **Impact:** инверсия слоёв растёт незаметно; часть находок ревью (G112, тела ответов, запросы без ctx) поймал бы линтер.
- **Fix:** интерфейсы k8s-клиента/кеша/LLM — в core, реализации — в infrastructure; `depguard` с правилами слоёв + `bodyclose`/`gosec`/`noctx` в гейт. После `DEAD-SHARED-INFRA-CODE` (меньше ложных срабатываний).
- **Refs:** CODE-REVIEW-2026-10-21 M-25, R-8. Смежно `AMP-CORE-IMPORTS-INFRASTRUCTURE-NO-LAYER-GATE`.
- **Status:** open

### [medium][Deps][~0.5d] DEPS-CLIENT-GO-BUMP
- **Title:** `k8s.io/client-go v0.29.0` вне поддержки
- **Problem:** `go-app/go.mod:25-27` (`k8s.io/{api,apimachinery,client-go} v0.29.0`). _(2026-11-08, UPSTREAM-CORE-REMOVAL F7: `go mod tidy` выполнен, пункт «go.mod не tidy» снят; проверки `tidy -diff` в гейте по-прежнему нет.)_
- **Impact:** нет security-фиксов client-go; без проверки в гейте tidy снова уплывёт.
- **Fix:** поднять client-go отдельным PR (карантин 7 дней — проверить дату версии), `go mod tidy -diff` в гейт.
- **Refs:** CODE-REVIEW-2026-10-21 M-29. Смежно `GO-MOD-TIDY-CHECK`.
- **Status:** open

## Low

### [low][Build][~0.5d] K8S-CLIENTSET-BINARY-SIZE
- **Title:** `amp-upstream` вырос с 43.2 до 78.9 МБ из-за полного clientset client-go
- **Problem:** `internal/infrastructure/k8s` строит `kubernetes.NewForConfig` — все typed-клиенты всех API-групп; адаптеру K8s-таргетов (`UPSTREAM-NON-UPSTREAM-RECEIVERS`) нужен только `core/v1` Secrets и `/version`. _(`cmd/server` удалён UPSTREAM-CORE-REMOVAL; `amp-upstream` — единственный бинарь.)_
- **Impact:** образ и время загрузки больше; функционально не мешает.
- **Fix:** `corev1client.NewForConfig` (typed core/v1) + discovery-клиент вместо полного `kubernetes.Interface`; интерфейс `K8sClient` не меняется, тесты на fake clientset — через `fake.NewSimpleClientset().CoreV1()`.
- **Refs:** замер 2026-11-08, `tasks/archive/UPSTREAM-NON-UPSTREAM-RECEIVERS/evidence/testing.md`.
- **Status:** open

### [low][AI][~0.25d] UPSTREAM-AI-FALLBACK-STICKY
- **Title:** fallback-классификация на `amp-upstream` не заменяется LLM-ответом после восстановления провайдера
- **Problem:** `upstreamai.Classifier` классифицирует алерт один раз, пока тот в store; при outage LLM сервис отвечает fallback по правилам без ошибки, и запись остаётся до GC алерта. Сам `ClassificationService` кеширует fallback на полный TTL (1h, `classification.go` Step 3), так что и повторный вызов вернул бы его же.
- **Impact:** после outage долгоживущие алерты до конца жизни уходят с `llm_*` от правил; видно по `amp_upstream_ai_classifications_total{outcome="fallback"}`.
- **Fix:** не кешировать fallback (или короткий TTL) в сервисе; в `Classifier` — забывать fallback-запись через интервал, чтобы следующий `Put` переклассифицировал.
- **Refs:** deep-review `UPSTREAM-AI-CLASSIFY` F3; `go-app/internal/upstreamai/classifier.go`.
- **Status:** open

### [medium][Storage][~0.5d] UPSTREAM-HISTORY-PG-RECONNECT
- **Title:** `amp-upstream` не подхватывает PostgreSQL, поднявшийся после старта
- **Problem:** `newAMPLayer` подключается к PG и мигрирует один раз при старте (connect ограничен `database.connect_timeout`, миграции — 1m). При ошибке история и investigation выключены до рестарта; переподключения в фоне нет.
- **Impact:** PG, стартовавший позже `amp-upstream` (общий релиз, рестарт БД), оставляет процесс без истории и расследований до его рестарта; доставка не страдает. Видно по Error в логе старта.
- **Fix:** фоновый цикл connect+migrate с backoff, включающий `History` и очередь investigation атомарно; либо readiness, требующая PG при `profile: standard`.
- _(2026-11-09, pre-release review `PROD-RELEASE-V010` F1: поднято до medium — на свежей установке с встроенным PG AMP почти всегда стартовал раньше БД. Смягчено в чарте: init container `wait-for-postgres` ждёт встроенный PG до 120s; внешний PG и рестарт БД — по-прежнему без переподключения.)_
- **Refs:** deep-review `UPSTREAM-AI-HISTORY-INVESTIGATION` F3; `go-app/cmd/amp-upstream/amp.go` `connectDatabase`. Тот же корень с точки зрения оператора — BUGS `AMP-NO-DB-WAIT` (с UPSTREAM-CORE-REMOVAL `amp-upstream` — единственный бинарь, и PG позже старта = процесс без истории до рестарта); чинить вместе.
- **Status:** open

### [low][Performance][~0.25d] UPSTREAM-HISTORY-CONVERT-UNDER-LOCK
- **Title:** конвертация в `core.Alert` под локом провайдера на каждый `Put`
- **Problem:** `History` и `Classifier` вызывают `toCoreAlert` (две map, SHA-256) в `PostStore`/`PostDelete` под `a.mtx` провайдера; IO нет, но это CPU на глобальном локе ingest для каждого алерта и resend'а.
- **Impact:** при больших батчах растёт время удержания лока приёма; замеров нагрузки не было.
- **Fix:** класть в шард `*alert.Alert` (store заменяет алерты, не мутирует) и конвертировать в воркере; бенчмарк `PostStore` до и после.
- **Refs:** deep-review `UPSTREAM-AI-HISTORY-INVESTIGATION` F11; `go-app/internal/upstreamai/history.go`, `classifier.go`.
- **Status:** open

### [low][AI][~0.25d] UPSTREAM-AI-REFIRE-STALE-CLASSIFICATION
- **Title:** повторно сработавший алерт до GC получает классификацию прошлого инцидента
- **Problem:** запись `Classifier` живёт по fingerprint до GC алерта; resolve → firing с новым `StartsAt` до GC не создаёт новой классификации, и notify-стадия, и investigation получают старый результат (investigation всё же ставится новым инцидентом — dedup очереди по `StartsAt`).
- **Impact:** `llm_*` в уведомлении нового инцидента — от прошлого; LLM-кеш сервиса дал бы тот же ответ при тех же labels, так что расхождение заметно только при изменившихся аннотациях.
- **Fix:** хранить `StartsAt` в записи и переклассифицировать при его смене.
- **Refs:** re-review `UPSTREAM-AI-HISTORY-INVESTIGATION` N5 (поведение с 2a); `go-app/internal/upstreamai/classifier.go`.
- **Status:** open

### [low][Supply chain][~0.5d] UPSTREAM-BUMP-TRANSITIVE-AGE
- **Title:** карантин `upstream-bump.sh` для транзитивных require — по commit time
- **Problem:** `go list -m -json mod@ver` отдаёт `Time` — время коммита (`cmd/go/internal/modfetch/codehost/codehost.go`), его задаёт автор. Для `github.com/prometheus/alertmanager` скрипт берёт ещё дату GitHub-релиза, для остальных сдвинутых require — только commit time: новый тег на старый коммит проходит карантин.
- **Impact:** 7-дневный карантин для транзитивки обходим; смягчено тем, что скрипт печатает список сдвинутых модулей для ревью.
- **Fix:** время первого появления в прокси — deps.dev `publishedAt` (или `index.golang.org`, но полный скан за 7 дней тяжёлый); отказ при недоступности.
- **Refs:** deep-review `UPSTREAM-CORE-POC-GATE` F1, `scripts/upstream-bump.sh` `age_days`.
- **Status:** open

### [low][Gate][~0.25d] UPSTREAM-GATE-RUNNER-GAPS
- **Title:** `upstream-core-gate.sh`: падение пакета без паники и `t.Log` у упавшего родителя
- **Problem:** (1) падение уровня пакета без `panic:` (например, ошибка в `TestMain` после `m.Run`) не отличается от ожидаемого FAIL пакета из-за baseline; (2) собственное падение родителя определяется по строкам `_test.go:NN:` в его выводе — под шаблон попадают и `t.Log`, упавший родитель с логом даст ложный `NEW`.
- **Impact:** (1) пропуск редкого класса поломок; (2) ложно красный гейт; в текущих сьютах ни того, ни другого нет.
- **Fix:** (1) сверять exit code `go test` с ожидаемым по листьям или проверять вывод пакета на `FAIL` вне тестов; (2) отличать `t.Log` от `t.Error` нельзя по выводу — считать родителя упавшим только при отсутствии упавших подтестов или по маркеру `--- FAIL` без подтестов.
- **Refs:** deep-review `UPSTREAM-CORE-POC-GATE` F4 (п. 2), F12.
- **Status:** open

### [low][Security][~0.25d] INVESTIGATION-REDACT-GAPS
- **Title:** редактирование секретов в выводе investigation tools пропускает Digest-параметры и пароль с `@` в URL
- **Problem:** `go-app/internal/core/investigation/redact.go`: правило `Authorization` останавливается на первой кавычке (чтобы в JSON не съесть соседние поля), поэтому у `Authorization: Digest username="x", response="…"` значения в кавычках остаются. Правило URL берёт пароль до первого `@`: у `scheme://user:p@ss@host` хвост пароля остаётся.
- **Impact:** низкий: `response` Digest привязан к nonce, пароль с `@` в URL редок. Редактирование заявлено как best effort.
- **Fix:** `Authorization` без кавычки после разделителя — до конца строки, с кавычкой — до закрывающей; в URL — пароль до последнего `@` перед хостом; кейсы в `redact_test.go`.
- **Refs:** `tasks/archive/INVESTIGATION-PROMPT-ISOLATION/review-findings.md` F10 (2026-10-29).
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

### [low][Config][~0.25d] DB-CONN-STRING-UNESCAPED
- **Title:** `ConnectionString()` собирает строку подключения без экранирования
- **Problem:** `PostgresConfig.ConnectionString()` (`go-app/internal/database/postgres/config.go:139`, key=value через `fmt.Sprintf`) подставляет пользователя и пароль как есть — тот же дефект, что был в `DSN()` до HELM-EXTERNAL-DATABASE: пароль с пробелом, `'`, `/ # ? %` ломает разбор или молча меняется. В рантайме не вызывается: единственный вызов — `pool_test.go:312`. _(2026-11-08: `Config.GetDatabaseURL()` и `internal/config/example.go` удалены UPSTREAM-CORE-REMOVAL.)_
- **Impact:** сейчас нет; ловушка для следующего, кто возьмёт её вместо `DSN()`.
- **Fix:** удалить вместе с тестом (кандидат в `DEAD-SHARED-INFRA-CODE`) либо собрать через `url.URL`, как `DSN()`.
- **Refs:** `tasks/archive/HELM-EXTERNAL-DATABASE/research.md`, `evidence/dsn-password-escaping.txt`; 2026-10-16.
- **Status:** open

<!-- Code review 2026-10-21 -->

### [low][Storage][~0.25d] DB-ORPHAN-TABLES
- **Title:** таблицы БД без читателей и писателей
- **Problem:** `filter_rules` (с seed), `alert_classifications`, `alert_publishing_history`, `system_metrics`, `migration_history` (`migrations/20250911094416_initial_schema.sql`), `config_versions`/`audit_log`/`backups`/`locks`. _(2026-11-08, UPSTREAM-CORE-REMOVAL: к ним добавились таблицы silences, `publishing_dlq` и templates (миграции `20251104*`, `20251112*`, `20251125*`) — сайленсы держит upstream-ядро, DLQ и шаблоны ушли с ядром AMP.)_ Живые — `alerts` и investigations.
- **Impact:** схема вводит в заблуждение (`HISTORY-API` строился на допущении, что история уже пишется).
- **Fix:** drop-миграция отдельным PR (Standard: схема БД); решение по `alert_classifications`/`alert_publishing_history` — вместе с `HISTORY-API`.
- **Refs:** CODE-REVIEW-2026-10-21 § 4.4, § 10.
- **Status:** open

### [low][Storage][~0.25d] MIGRATIONS-EMBED
- **Title:** миграции ищутся в FS по CWD; `RunMigrationsDown` откатывает не то
- **Problem:** `internal/database/migrations.go:108-140,190-215`: поиск каталога по CWD/`runtime.Caller`; `RunMigrationsDown(steps)` передаёт steps как целевую версию (`steps=1` откатит всё; вызовов нет).
- **Impact:** бинарь зависит от раскладки FS; ловушка для первого вызова Down.
- **Fix:** `//go:embed *.sql` в `goose.NewProvider`; Down удалить.
- **Refs:** CODE-REVIEW-2026-10-21 L-29.
- **Status:** open

### [low][Helm][~0.1d] HELM-AMP-SECCOMP
- **Title:** у пода AMP нет `seccompProfile`; Kubernetes-tool investigation без токена SA вне discovery
- **Problem:**
  - `helm/amp/values.yaml:54` (`podSecurityContext`): нет `seccompProfile: RuntimeDefault`.
  - _(2026-11-08, UPSTREAM-CORE-REMOVAL)_ «токен без RBAC» решён на уровне пода: `templates/statefulset.yaml:85` монтирует токен только при `amp.discoveryEnabled` (`profile: standard` и `publishing.enabled`). `serviceaccount.yaml` по-прежнему ставит `automountServiceAccountToken: true` — безвредно, под его перекрывает.
  - _(deep-review UPSTREAM-CORE-REMOVAL C5)_ обратная сторона: Kubernetes-tool investigation (`investigation.tools.kubernetes`, `go-app/internal/core/investigation/agent.go:44`) in-cluster ходит с токеном SA, а при выключенном discovery токена в поде нет — tool падает на каждом вызове; RBAC под него чарт тоже не создаёт.
- **Impact:** не проходит restricted PSS; Kubernetes-tool в `profile: lite` или без publishing не работает без ручного `extraVolumes`/RBAC.
- **Fix:** seccomp `RuntimeDefault`; automount и read-only RBAC (`pods`, `events`, `deployments` в `allowed_namespaces`) ещё и при включённом Kubernetes-tool; render-тест на оба случая.
- **Refs:** CODE-REVIEW-2026-10-21 L-28; `tasks/archive/UPSTREAM-CORE-REMOVAL/review-findings.md` C5. Digest-пины — `CI-SUPPLY-CHAIN`.
- **Status:** open

### [low][Cleanup][~0.5d] DEAD-SHARED-INFRA-CODE
- **Title:** мёртвый код общей инфраструктуры после удаления ядра AMP
- **Problem:** UPSTREAM-CORE-REMOVAL удалил ядро, но не общие пакеты, у которых остались только тесты и примеры: `internal/database/postgres/{retry,prometheus,example,errors}.go`, `LoadFromEnv` (`postgres/config.go:55`), `ConnectionString` (`DB-CONN-STRING-UNESCAPED`), `PeriodicHealthChecker`/`CircuitBreakerHealthChecker` (`postgres/health.go:100,170`), `config.LoadConfigFromEnv` (`config.go:189`), `alertconv.ToGettableAlert` (вызовы только в тестах), части `pkg/{httperror,retry,metrics}` и `infrastructure/llm`. По `tasks/archive/UPSTREAM-CORE-REMOVAL/tasks.md` — порядка ~300 недостижимых функций (`deadcode`). Наследует остаток закрытых `DEAD-PKGS-NO-IMPORTERS`/`DEAD-SILENCING-CACHE`.
- **Impact:** ложные срабатывания будущего `depguard` (`LAYER-GATE-DEPGUARD`), шум в поиске и ревью, тесты на код без вызовов.
- **Fix:** `refactor:`-PR без изменения поведения: удалять по `deadcode` пакет за пакетом, release-gate после каждого; `deadcode` — в гейт как отчёт.
- **Refs:** `tasks/archive/UPSTREAM-CORE-REMOVAL/tasks.md` (фаза 5b, остаток); CODE-REVIEW-2026-10-21.
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
