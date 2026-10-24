# Архитектурные решения (DECISIONS)

## ADR-001: Go как основной язык runtime
- **Дата**: 2026-02 (фиксация факта)
- **Контекст**: Проект начинался на Python, но core runtime переписан на Go для совместимости с Alertmanager API.
- **Решение**: Go — основной язык для серверной части. Python-код удалён.
- **Следствие**: API-совместимость с Alertmanager проще поддерживать на том же языке.

## ADR-002: Alertmanager Replacement Scope Is Active-Runtime-First
- **Дата**: 2026-03-08
- **Контекст**: Historical docs, DONE entries и parity tests начали описывать runtime шире, чем текущий active bootstrap в `go-app/cmd/server/main.go` и `go-app/internal/application/router.go`.
- **Решение**:
  - source of truth для replacement story — active runtime, а не historical docs/tests;
  - текущий допустимый claim — только `controlled replacement`, не `general-purpose drop-in replacement`;
  - текущий active scope ограничен alert ingest, silence CRUD, health/readiness, metrics и real publishing path;
  - deprecated `v1` endpoints не входят в current active scope;
  - wide-surface parity expectations фиксируются как future/backlog parity до отдельного runtime-restoration slice.
- **Следствие**:
  - planning/docs/tests должны синхронизироваться с active runtime first;
  - historical parity suites не должны автоматически определять публичные claims;
  - если нужен stronger Alertmanager replacement claim, это оформляется отдельной runtime/API задачей.

## ADR-003: Solo Kanban (SEMA) как процесс разработки
- **Дата**: 2026-03-08
- **Контекст**: Один разработчик + AI-агент. Нужен легковесный, но структурированный процесс.
- **Решение**: Solo Kanban с WIP max 2, балансом 50/50 maintenance/roadmap, вертикальными срезами и quality gates.
- **Следствие**: Planning files версионируются в `docs/06-planning/`, задачи в `tasks/`.

## ADR-004: Active Storage Bootstrap Is Profile-Aware And State-Aware
- **Дата**: 2026-03-09
- **Контекст**: Active runtime в `go-app/internal/application/service_registry.go` держал `nil` placeholder для `core.AlertStorage`, standard migrations были незавершенными, а health/readiness handlers отвечали статическим success независимо от реального storage/bootstrap state.
- **Решение**:
  - `ProfileLite` использует `internal/infrastructure.SQLiteDatabase` как canonical embedded storage runtime с обязательными `Connect()` и `MigrateUp()` до публикации storage;
  - `ProfileStandard` использует canonical path `PostgresPool.Connect -> goose migrations -> thin Postgres storage adapter`, работающий поверх уже созданного pool и не открывающий второй connection pool;
  - required storage и database bootstrap failures считаются fail-fast и не допускают pseudo-healthy startup;
  - `/health` и `/healthz` закреплены как liveness JSON endpoints, `/ready` и `/readyz` как readiness JSON endpoints, `/-/healthy` и `/-/ready` сохраняют Alertmanager-compatible plain-text contract;
  - optional degradations вроде cache fallback отражаются в runtime report как `degraded`, но не переводят readiness в failure, пока required dependencies healthy.
- **Следствие**:
  - active runtime больше не стартует с отсутствующим required storage и ложным `healthy`;
  - observable health contract теперь различает bootstrap, storage, database и optional degraded state;
  - active alert/silence handlers пока сознательно остаются на memory compatibility stores и требуют отдельного follow-up, если их нужно переводить на persistent backend.

## ADR-005: Active Dashboard Placeholder Pages Stay On Current `/dashboard/*` Surface
- **Дата**: 2026-03-09
- **Контекст**: В active runtime `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` были смонтированы, но возвращали placeholder body. При этом в репозитории уже существовал второй UI stack (`internal/ui` + `cmd/server/handlers`), который не был active source of truth для этих routes.
- **Решение**:
  - canonical active owner этих страниц остается в current `go-app/cmd/server` path, а не переносится на dormant `/ui/*` subsystem;
  - страницы реализованы как honest read-only UI через `go-app/cmd/server/legacy_dashboard.go`, `go-app/cmd/server/templates/legacy/*` и `go-app/internal/application/legacy_dashboard.go`;
  - page models строятся из узких runtime summaries `ServiceRegistry` и могут показывать `ready`, `empty`, `limited`, `disabled` или `metrics-only` state вместо placeholder/error semantics;
  - richer operator workflows, full routing editor и полная миграция legacy dashboard остаются отдельным follow-up work.
- **Следствие**:
  - active `/dashboard/*` surface больше не обещает незавершенный UI;
- default non-tagged `cmd/server` tests теперь защищают contract этих страниц;
- если репозиторий в будущем захочет единый UI stack, это должно идти отдельной задачей, а не скрытым follow-up к placeholder removal.

## ADR-006: Restored Operational Alertmanager Endpoints Stay Inside The Controlled-Replacement Scope
- **Дата**: 2026-03-09
- **Контекст**: После ADR-002 active runtime был сознательно сужен до controlled-replacement narrative, но practical Grafana / automation flows все еще требовали `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups` и `POST /-/reload` в current mounted router.
- **Решение**:
  - active runtime снова монтирует `status`, `receivers`, `alerts/groups` и `reload` в `go-app/internal/application/router.go`;
  - `ServiceRegistry` получает `startTime` и `ReloadCoordinator`, чтобы эти endpoints опирались на живое runtime state, а не на historical stubs;
  - current public claim остается `controlled replacement`, а не `general-purpose drop-in replacement`;
  - config/history/inhibition/classification surfaces, deprecated `v1` alias и broader dashboard/runtime parity остаются отдельным follow-up work.
- **Следствие**:
  - public docs и compatibility matrix больше не должны описывать эти четыре endpoints как backlog-only;
  - planning/tests не должны продолжать фиксировать их отсутствие в active router contract;
  - restoration этого operational surface не отменяет active-runtime-first policy и не возвращает repo к broad historical parity claim.

## ADR-007: Grafana Dashboard Identity (uid, filename) Is Stable On Purpose
- **Дата**: 2026-08-17
- **Контекст**: После `GRAFANA-DASHBOARD-BRANDING-DRIFT` (visible title → `AMP - Operations Dashboard`) остались identity-shaped поля: `uid = amp-alert-history` и путь `grafana/dashboards/alert-history-service.json`. `GRAFANA-DASHBOARD-IDENTITY-DRIFT` требовал явного решения, а не механического rename.
- **Решение**:
  - `uid` и filename — стабильный import/provisioning contract, не branding surface; они НЕ меняются без явного provisioning owner;
  - смена `uid` ломает существующие Grafana provisioning references и dashboard links у всех, кто уже импортировал дашборд, ради чисто косметической консистентности — trade-off отвергнут;
  - при появлении provisioning owner (Helm-managed provisioning или отдельный dashboards repo) допустима миграция с alias/redirect-планом отдельной задачей.
- **Следствие**:
  - `GRAFANA-DASHBOARD-IDENTITY-DRIFT` закрывается как «working as intended, документировано»;
  - historical naming в uid/filename не считается drift и не должен попадать в новые BUGS-записи.

## ADR-008: `http_config.oauth2` — Skip The Target Only When The Integration Asked For It
- **Дата**: 2026-08-19
- **Контекст**: `FU-HTTP-CONFIG` (AMP-PARITY-WAVE7 track C) доставляет per-integration `http_config`, но `oauth2` не поддержан (нужен token endpoint + refresh loop, это отдельная единица работы — `FU-HTTP-OAUTH2`). Первая реализация всегда логировала `WARN` и отправляла запрос БЕЗ OAuth2-креденшелов. Security-review предложил обратное: skip target (fail closed), по аналогии с нечитаемым `password_file`. Controller ruling: ни один из двух вариантов целиком не верен — решает ПРОВЕНАНС блока.
- **Решение**:
  - `oauth2` в СОБСТВЕННОМ `http_config` интеграции → **target пропускается** (`fail closed`) с громким `WARN`, содержащим receiver/kind/index. Endpoint явно объявил требование auth, поэтому доставка без него — осознанная неаутентифицированная отправка. Радиус — ровно одна интеграция, которая об этом попросила.
  - `oauth2`, унаследованный из `global.http_config` → интеграция **продолжает доставлять** (без OAuth2), с `WARN` **и** счётчиком `amp_publishing_unsupported_http_config_total{field="oauth2",target=...}`.
  - Провенанс сохраняется через неэкспортированное поле `routing.HTTPConfig.inheritedFromGlobal` (устанавливается только в `ResolveHTTPConfigFallback`), потому что parse-time resolution стирает структурную разницу между own и inherited. Поле неэкспортированное намеренно: `yaml.v3` и struct-validator игнорируют такие поля, поэтому оно не может протечь в `/api/v2/status`.
- **Обоснование асимметрии**:
  - `global.http_config` распространяется WHOLESALE на каждую `webhook`/`slack`/`pagerduty`/`telegram` интеграцию, которая не задала свой блок. При политике «всегда skip» ОДИН глобальный `oauth2` блок = **полный notification outage по всему процессу**, включая Slack/Telegram/PagerDuty, которые аутентифицируются своим URL/header-креденшелом и OAuth2 никогда не требовали.
  - Неаутентифицированный запрос уходит на **собственный настроенный host оператора**, который его auth-gate-ит и вернёт 401. Это отвергнутый запрос к предполагаемому получателю, а не раскрытие данных третьей стороне — материально слабее, чем C1 (где выбрасывались креденшелы, которые оператор ДАЛ).
- **Accepted risk**:
  - Интеграция, унаследовавшая `global.http_config.oauth2`, отправляет запросы без OAuth2-креденшелов до тех пор, пока `FU-HTTP-OAUTH2` не закрыт. Сигнал: `WARN` на каждый config load/reload + монотонный счётчик + 401 от самого endpoint.
  - Осознанно принято, потому что альтернатива (skip) превращает одну неподдерживаемую опцию в отказ всей доставки.
- **Следствие**:
  - `WARN` один раз на config load — не per-alert; поэтому счётчик обязателен, а не «nice to have» (review I1).
  - `docs/ALERTMANAGER_COMPATIBILITY.md` описывает оба варианта поведения в секции `Per-integration http_config`.
  - При закрытии `FU-HTTP-OAUTH2` этот ADR отменяется целиком: оба пути станут «поддержано».

## ADR-009: AMP машиночитаемо заявляет версию КОНТРАКТА Alertmanager, а не свою
- **Дата**: 2026-09-23
- **Контекст**: Инструменты экосистемы Alertmanager определяют версию сервера не через `GET /api/v2/status`, а разбирая `/metrics` и читая лейбл `version` у `alertmanager_build_info` (karma: `internal/verprobe/verprobe.go`). AMP такой метрики не экспортировал, проба падала на каждом цикле опроса. Наивное «отдать свою версию» оказалось не фиксом, а регрессом: `git describe` даёт `v0.0.2-518-g3b3c52e`, karma режет строку по первому дефису и проверяет остаток констрейнтом `>=0.22.0`; `0.0.2` его не проходит, маппер не находится, и karma **очищает уже показанные алерты**. Дефолт `buildinfo.Version = "dev"` не semver вовсе, а парсится он через `semver.MustParse`, который паникует. То есть до этой задачи karma работала с AMP именно потому, что проба ЛОМАЛАСЬ и срабатывал fallback «считать самым свежим» (`999.0`).
- **Решение**:
  - экспортируются ДВЕ метрики с разным смыслом: `alertmanager_build_info{version,revision,branch,goversion}`, где `version` — константа `0.27.0`, версия реализуемого upstream-контракта; и `amp_build_info{version,revision,branch,goversion,build_user,build_date}` с настоящими данными сборки;
  - лейблы `revision`/`branch`/`goversion` у compat-метрики — настоящие: `version` описывает контракт, остальное описывает бинарь, который этот контракт обслуживает. Подлога нет, своя версия не теряется;
  - `0.27.0` — машиночитаемая форма строки `**Alertmanager Version**: v0.27+ (API v2)` из `docs/ALERTMANAGER_COMPATIBILITY.md`, а не независимое число. Константа живёт в `go-app/internal/buildinfo/metrics.go` и меняется ТОЛЬКО вместе с этой строкой;
  - инвариант («валидный semver, проходящий `>=0.22.0`») защищён тестом, который воспроизводит алгоритм karma целиком (скрейп → `expfmt` → лейбл → `SplitN(v,"-",2)[0]` → `semver` → констрейнт), а не ищет строку `0.27.0`. Тест проверен негативно: подмены на `"dev"` и `"0.0.2"` его роняют.
- **Обоснование**:
  - у upstream эта метрика отвечает на вопрос «какой Alertmanager-контракт я получу», а не «какой бинарь у вас собран». Отдавать в неё версию AMP — отвечать не на тот вопрос, причём ответом, который ломает потребителя;
  - `0.28.x` не взят сознательно: это была бы заявка на фичи, против которых AMP никогда не проверялся.
- **Accepted risk**:
  - заявленная версия — обязательство перед инструментами: потребитель вправе включить кодовые пути 0.27. Смягчение: раздел «что неприменимо» в compat-доке, `amp_build_info` рядом, и этот ADR как граница заявления. На `/testing` 2026-09-23 парити-сьют по тому, что karma реально использует (alerts, groups, silences, status, receivers, reload), зелёный целиком — 20 PASS / 0 FAIL.
- **Следствие**:
  - повышать `AlertmanagerCompatVersion` можно только вместе со строкой 5 `ALERTMANAGER_COMPATIBILITY.md` и пересмотром парности, а не «чтобы было свежее»;
  - при `metrics.enabled: false` `/metrics` отдаёт 404, проба падает, потребитель уходит в fallback — это приемлемая деградация, кода она не касается;
  - `versionInfo.version` в `/api/v2/status` намеренно НЕ тронут: karma его не читает, семантика родственная, но это отдельное решение (`STATUS-VERSIONINFO-CONTRACT` в BACKLOG).

## ADR-010: `endsAt` из `resolve_timeout` — значение API-слоя, БД хранит присланное
- **Дата**: 2026-09-24
- **Контекст**: Алерт, пришедший без `endsAt`, отдавался из API с `endsAt == updatedAt`, то есть как отгоревший (`PARITY-RESOLVE-TIMEOUT-ENDSAT`). Upstream ставит `endsAt = receivedAt + resolve_timeout` и продлевает окно на каждом повторном POST. Вопрос был в том, где это значение появляется: на ingest, до `AlertProcessor.ProcessAlert`, или только в memory store, из которого читает API.
- **Решение**:
  - `endsAt` проставляет memory store (`internal/infrastructure/storage/memory/alert_store.go`) при нормализации: и на POST, и при rehydration из БД после рестарта. Только для горящих алертов без `endsAt`, строго после вычисления статуса;
  - `core.Alert`, который уходит в dedup, БД, inhibition, классификацию и publishing, не меняется: `EndsAt` остаётся `nil`, в БД `ends_at = NULL`;
  - `resolve_timeout` читается из живого конфига на каждый ingest-батч (`ServiceRegistry.newAlertStore`), фолбэк — `alertconv.DefaultResolveTimeout` (5m).
- **Обоснование**:
  - dedup считает изменение `EndsAt` поводом для update (`internal/core/services/deduplication.go`). Если штамповать до процессора, каждый повторный POST перестал бы быть дубликатом и гнал бы запись в БД, классификацию (LLM) и grouping;
  - upstream сам обнуляет `endsAt` у горящих алертов перед отправкой нотификаций, так что receivers таймаутное значение и не должны видеть;
  - БД хранит то, что прислал отправитель; таймаут — производное значение, зависящее от конфига в момент приёма.
- **Следствие**:
  - БД и API расходятся в `endsAt` для таймаутных алертов. При отладке через SQL `ends_at IS NULL` у горящего алерта означает «окно считается от времени приёма»;
  - после рестарта rehydrated-алерт получает свежее окно `restartTime + resolve_timeout` по текущему конфигу, а не исходное;
  - авто-резолв по истечении таймаута (`RESOLVE-TIMEOUT-AUTO-RESOLVE`) должен учитывать, что в БД для таких алертов `ends_at` пуст: срок истечения живёт только в memory store.

## ADR-011: HTTP-аутентификация — свой middleware в формате upstream `web.config`, по умолчанию выключена
- **Дата**: 2026-09-25
- **Контекст**: HTTP API AMP был полностью открыт (`PROD-AUTH`, P0 из аудита production readiness). Upstream Alertmanager защищается `--web.config.file` через `prometheus/exporter-toolkit`: только basic auth (bcrypt), на все пути без исключений, TLS там же.
- **Решение**:
  - формат файла — upstream (`basic_auth_users`), путь — флаг `-web.config.file`, `server.web_config_file` или `SERVER_WEB_CONFIG_FILE`;
  - реализация — собственный middleware (`internal/application/webauth.go`), а не `exporter-toolkit/web.Serve`: алгоритм проверки перенесён 1:1 (хеш-заглушка для неизвестного пользователя, кэш вердиктов bcrypt, мьютекс), но добавлены исключения путей;
  - без файла auth выключен, на старте громкий `WARN`. Явный opt-out (`server.auth.disabled`), глушащий WARN, рассмотрен и отклонён;
  - только basic, без bearer;
  - не реализованные ключи upstream (`tls_server_config`, `http_server_config`, `rate_limit`) — ошибка старта, а не молчаливое игнорирование.
- **Обоснование**:
  - `exporter-toolkit` не умеет исключать пути ⇒ kubelet-probes либо ломаются, либо креды попадают в pod spec; кроме того, он тянет 5–7 новых модулей и забирает Serve-цикл, который будет переделывать `PROD-GRACEFUL-SHUTDOWN`;
  - включить auth по умолчанию нельзя: пароль некому сгенерировать, все существующие деплои упали бы;
  - молча проигнорированный `tls_server_config` — ложное ощущение защиты;
  - bearer upstream не принимает, все клиенты экосистемы (Prometheus, Grafana, amtool, karma) умеют basic.
- **Осознанные отклонения от upstream**:
  - пустой `basic_auth_users` — ошибка (upstream: auth выключен);
  - невалидная правка файла оставляет прежних пользователей (upstream: `500` на всё) — правка не может ни открыть API, ни положить ingest;
  - `server.auth.unauthenticated_paths` (дефолт `/-/healthy`, `/-/ready`, точное совпадение, без route prefix) — upstream исключений не имеет.
- **Следствие**:
  - смена пути к файлу и списка исключений — только рестартом (`W605`); содержимое файла подхватывается само;
  - Helm-probes по умолчанию переведены на `/-/healthy`/`/-/ready`;
  - config-reloader sidecar пока не умеет auth ⇒ чарт не рендерит его вместе с `webConfig` (`CONFIG-RELOADER-AUTH`);
  - путь к файлу читается из env и напрямую, мимо viper: иначе при отсутствии файла конфига (`CONFIG-MISSING-FILE-DROPS-ENV`) auth молча выключился бы.

## ADR-012: RBAC чарта = фактические вызовы кода, без cluster-scope
- **Дата**: 2026-09-28
- **Контекст**: чарт при любых values (включая `lite`) выдавал SA AMP `ClusterRole` на чтение `secrets`/`configmaps` во всём кластере и namespaced `Role` с `create/update/patch` на них же (`PROD-RBAC-SCOPE`, P0 из аудита production readiness). Тем же SA с автомонтируемым токеном пользуются Redis и backup-поды. Код при этом делает один вызов — `Secrets(ns).List(labelSelector)` в одном namespace, и только при `profile: standard` + `publishing.enabled`.
- **Решение**:
  - одна `Role` + `RoleBinding` с `list` на `secrets` в `publishing.discovery.namespace` (по умолчанию — namespace релиза); `get`, `watch`, `configmaps`, `namespaces` и write-глаголы не выдаются;
  - рендер только при `serviceAccount.create && serviceAccount.rbac.create && profile == "standard" && publishing.enabled` — зеркало условия создания K8s-клиента в `publishing_runtime.go`;
  - ничего cluster-scoped ни при каких values;
  - Redis и backup-поды не монтируют токен SA;
  - мёртвые values `targetDiscovery.*`, `serviceAccount.rbac.crossNamespace` удалены;
  - `scripts/release-gate.sh` (`helm-rbac`) проверяет форму RBAC на дефолте, dev, production и lite.
- **Отклонено**:
  - явный opt-in на cluster-scope (требование исходной записи BACKLOG) — код не умеет multi-namespace discovery, opt-in расширял бы поверхность без функциональности;
  - не рендерить RBAC вовсе и документировать ручную выдачу — ломает дефолтный `standard`-инсталл (discovery получает 403).
- **Следствие**:
  - правило: глагол/ресурс добавляется в `rbac.yaml` в том же изменении, где код начинает его вызывать; иначе 403 (громко: WARN, пустой кэш discovery);
  - RBAC не фильтрует `list` по label selector — Role читает все secrets discovery-namespace; рекомендация — отдельный namespace для publishing-target secrets (`helm/amp/README.md` → RBAC);
  - investigation Kubernetes tool (pods, pods/log, events, deployments) чартом не обслуживается — `INVESTIGATION-K8S-TOOL-HELM`.

## ADR-013: CI = `release-gate.sh` без изменений; образы — в GHCR, отдельным workflow по тегу
- **Дата**: 2026-09-28
- **Контекст**: CI не было совсем, образы, на которые ссылается чарт (`ipiton/amp-llm`, `registry.example.com/amp/config-reloader`), проект не публикует (`PROD-CI-IMAGES`, P0-блокер доставки). Критерий «можно релизить» уже существовал — `scripts/release-gate.sh`, — но на чистой машине он падал на `helm dependency build`, а ошибка была скрыта (`PROD-HELM-CLEAN-CHECKOUT`).
- **Решение**:
  - job `gate` запускает `release-gate.sh` как есть, без пересказа его шагов в YAML. Скрипт сам добавляет репозитории из `Chart.lock` во временный helm-конфиг (шаг `helm-deps`);
  - два workflow: `ci.yml` (PR и push в `main`, только `contents: read`) и `release.yml` (только push тега `v*`, `packages: write` на одном job'е, workflow-level `permissions: {}`);
  - реестр — GHCR (`ghcr.io/ipiton/amp`, `ghcr.io/ipiton/amp-config-reloader`), авторизация через `GITHUB_TOKEN`, без внешних секретов;
  - multi-arch `linux/amd64,linux/arm64` кросс-компиляцией на build-платформе (`$BUILDPLATFORM` + `TARGETOS`/`TARGETARCH`), а не Go под QEMU;
  - теги образа: `X.Y.Z`, `X.Y`, `sha-<short>`, `latest` только для релизов без pre-release суффикса;
  - Go закреплён патчем: `toolchain go1.26.8` в `go.mod` и `golang:1.26.8-alpine` в Dockerfile;
  - все сторонние actions — по полному SHA; инструменты (`actionlint`, `govulncheck`, `golangci-lint`, `helm`) — точными версиями;
  - required-кандидаты: `gate`, `images (amp)`, `images (config-reloader)`, `actionlint`. `govulncheck` и `e2e-ha` запускаются, но не required.
- **Обоснование**:
  - один источник правды для гейта: локальный прогон и CI не расходятся, красный CI воспроизводится одной командой;
  - `packages: write` в PR-workflow дал бы любому PR (в т. ч. правке самого workflow) токен на push образов;
  - GHCR — рядом с репозиторием, не требует секретов и отдельного аккаунта; Docker Hub дал бы rate limits и внешний секрет;
  - `govulncheck` красный на зависимостях, которые старше CI (9 находок: grpc, otel, x/net, x/text, pgx) — required сделал бы все PR красными до `PROD-DEPS-VULN`; `e2e-ha` на фиксированных ожиданиях без статистики на shared runner'ах.
- **Отклонено**:
  - один workflow с `if:` на push образов — права выдаются на job, а job'ы PR и тега в одном файле легко перепутать; разделение проверяется глазами за секунду;
  - `workflow_dispatch` для публикации — публикация без тега даёт образ без версии;
  - вендоринг сабчарта valkey вместо `helm repo add` — больше диффа; вернуться, если отказ `groundhog2k.github.io` станет частым.
- **Следствие**:
  - пакеты GHCR создаются private — после первого тега их нужно один раз перевести в Public (шаг в `PROD-RELEASE-V010`, `docs/CI.md`);
  - branch protection с required checks включает владелец репозитория вручную; имена job'ов — контракт, переименование ломает protection;
  - новый шаг гейта сразу попадает в CI; нестабильный тест в шагах `test`/`race` краснит required `gate` (`GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`);
  - версию Go поднимать в трёх местах сразу: `toolchain` в `go.mod`, оба Dockerfile.

## ADR-014: Solo Kanban 1.1 — vendored-копия upstream + AMP-overlay; `DECISIONS.md` не ротируется
- **Дата**: 2026-09-29
- **Контекст**: процесс AMP жил на Solo Kanban 1.0, скопированном и поправленном руками: команды, навыки и `WORKFLOW.md` разошлись с upstream, и обновить их можно было только ручным сравнением. В 1.1 появились Risk Profile, `deep-review` с verdict'ом, `finalize` на ветке задачи и ротация журналов (`SOLO-KANBAN-UPGRADE`).
- **Решение**:
  - фреймворк лежит в `docs/solo-kanban/`, команды — в `.claude/commands/`, навыки — в `skills/solo-kanban-*/`, шаблоны — в `tasks/templates/`. Всё это vendored: копия upstream с единственной заменой путей `docs/<name>.md` → `docs/solo-kanban/<name>.md`, руками не правится;
  - обновление — `scripts/solo-kanban-sync.sh`; версия и коммит upstream — в `docs/solo-kanban/VERSION`;
  - всё специфичное для AMP — в `WORKFLOW.md` (overlay), и при расхождении прав он;
  - отклонения от фреймворка: шага `deploy` нет; `deep-review` обязателен перед каждым тегом `v*`; `DECISIONS.md` не ротируется;
  - Gemini-адаптеры (`GEMINI.md`, `.gemini/`) удалены.
- **Обоснование**:
  - vendored + overlay делает обновление механическим: sync, затем ревью диффа. Локальные правила не теряются, потому что их нет в vendored-файлах;
  - `DECISIONS.md` — реестр ADR с номерами, на которые ссылаются код и доки. Помесячная ротация разнесла бы ADR по файлам и сломала поиск по номеру;
  - `deploy` в upstream рассчитан на сервис, который выкатывает агент. AMP — продукт, релиз идёт через тег и GHCR (ADR-013).
- **Отклонено**:
  - git submodule — лишний шаг при clone, и пути фреймворка не переписать;
  - симлинки на локальный checkout upstream — не работают у того, у кого checkout'а нет;
  - продолжать ручной форк 1.0 — именно из-за него процесс разошёлся с upstream.
- **Следствие**:
  - правка vendored-файла пропадёт при следующем sync: локальное правило пишется в `WORKFLOW.md`;
  - «закрыт ли slug» проверяется по `DONE.md` **и** `archive/DONE-*.md`;
  - дискреционный hook, запрещающий `write-tests` без `review-verdict.json`, пока не сделан (BACKLOG `SOLO-KANBAN-VERDICT-HOOK`).

## ADR-015: Сетевая экспозиция в чарте — guard на Ingress, ingress-only NetworkPolicy, ServiceMonitor с обязательными кредами
- **Дата**: 2026-10-01
- **Контекст**: `values-production.yaml` открывал весь HTTP API на Ingress без auth (`PROD-INGRESS-HARDENING`, P0). Под AMP был доступен любому pod'у кластера. Bundled ServiceMonitor рендерился безусловно и не матчил ни один Service. Механизм auth в процессе к этому моменту уже был (ADR-011), но чарт его не требовал.
- **Решение**:
  - `fail` при `ingress.enabled` без `webConfig.existingSecret` и без `ingress.externalAuth: true`, в **любом** профиле. Признака «прод» в values нет: `environment: production` стоит и в дефолтном `values.yaml`;
  - `ingress.externalAuth: true` — заявление оператора, что auth обеспечивает Ingress или proxy. Чарт это не проверяет: аннотации зависят от контроллера;
  - `templates/networkpolicy.yaml`: только `policyTypes: [Ingress]` и только порт `http`. Источники — списки `NetworkPolicyPeer` по ролям: `ingressController` (учитывается при `ingress.enabled`), `alertSenders`, `metricsScrapers`, плюс `extraIngress`. Включённая политика без источников — `fail`. В `values-production.yaml` политика включена, а списки пустые;
  - pod'ы AMP получают метку `app.kubernetes.io/component: application` в template; Service получает её в `metadata`. `spec.selector` Deployment и Service не меняются;
  - ServiceMonitor рендерится только при `monitoring.prometheusEnabled && monitoring.serviceMonitor.enabled`. Он селектит Service AMP по `component: application` и поддерживает `basicAuth` из Secret. Если `webConfig` задан, а креды не заданы, — `fail`.
- **Обоснование**:
  - для alerting-системы худший исход — тишина. Каждая опасная комбинация (открытый Ingress, политика без отправителей, скрейп, обречённый на 401) обнаруживается на `helm upgrade`, а не по пропавшим алертам или пустым графикам. Упавший рендер ничего не меняет в кластере;
  - угаданные дефолтные источники (`ingress-nginx`, `monitoring`) при промахе молча режут отправителей, поэтому дефолтов нет;
  - egress не ограничиваем: адреса нотификаций (Slack, PagerDuty, webhooks, LLM API) произвольны, и default-deny молча убьёт доставку;
  - селектор Service не меняем: helm применит Service раньше, чем новые pod'ы станут ready, и старые pod'ы выпадут из endpoints — приём алертов прервётся.
- **Отклонено**:
  - выключить Ingress в `values-production.yaml` — любой `ingress.enabled=true` снова открывал бы анонимный API;
  - конвенции Bitnami целиком (`allowExternal`, client-label) — `allowExternal: true` по умолчанию fail-open;
  - один сырой список `networkPolicy.from` — в values не видно, кого именно надо перечислить;
  - предупреждение в `NOTES.txt` вместо `fail` для ServiceMonitor без кредов — пустые графики замечают поздно. Выход без кредов и так есть: `monitoring.serviceMonitor.enabled=false`.
- **Следствие**:
  - breaking для `values-production.yaml`: нужны auth, креды ServiceMonitor и хотя бы один источник (migration notes в `CHANGELOG.md`). При upgrade — один rolling restart из-за метки;
  - kill-switch — `--set networkPolicy.enabled=false`, удаляет политику без рестарта;
  - на CNI без NetworkPolicy политика ничего не делает (fail-open), проверка — в `helm/amp/README.md` → Network Exposure;
  - дефолты по-прежнему требуют CRD prometheus-operator (ServiceMonitor, PrometheusRule redis), BUGS `MONITORING-CRD-DEFAULT`. Порт Service `metrics:9090` ведёт на экспортёры postgres/redis, BUGS `SERVICE-METRICS-PORT-MISROUTED`.

## ADR-016: Прод-путь PostgreSQL — внешний PostgreSQL; встроенный — dev/pilot single instance без бэкапов
- **Дата**: 2026-10-15
- **Контекст**:
  - аудит 2026-10-06 (`PROD-POSTGRES-HA-DECISION`, P0): чарт обещал HA, PITR и ежедневные base backups встроенного PostgreSQL;
  - измерения на k3d 2026-10-15 (`tasks/archive/PROD-POSTGRES-HA-DECISION/evidence/`) показали, что ничего из этого не работает:
    - конфиг из ConfigMap не применяется: `max_connections=20`, `archive_mode=off`, pg_hba из образа (BUGS `PG-CONFIG-NOT-APPLIED`);
    - `pg_basebackup` в CronJob всегда отказывает по pg_hba replication;
    - PDB при одной реплике блокирует drain ноды (`ALLOWED DISRUPTIONS 0`);
  - репликации в шаблоне нет: `replicas > 1` даёт несколько независимых баз за одним Service;
  - PostgreSQL стоит в критическом пути ingest: БД недоступна ⇒ `POST /api/v2/alerts` отвечает 500.
- **Решение**:
  - прод — внешний PostgreSQL; чарт поставляет и проверяет 16-ю версию, нижняя граница не исследовалась. Бэкапы, PITR и HA — на стороне оператора: managed-сервис с PITR или CloudNativePG + barman/S3. Подключение через values — `HELM-EXTERNAL-DATABASE`; до него — `database:` в `configFile.content`;
  - встроенный PostgreSQL — dev/pilot: одна реплика, бэкапов нет, PDB нет. Образ закреплён на `postgres:16.15`;
  - `postgresql.replicas != 1` и `postgresql.backup.enabled: true` в values — `fail` на рендере, с объяснением и ссылкой на runbook;
  - удалены backup CronJob, backup PVC, WAL-блок ConfigMap, PDB Postgres, правила `PostgreSQLWALArchivingFailed` и `PostgreSQLBackupOld`;
  - восстановление — runbook `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` на `pg_dump -Fc` / `pg_restore --single-transaction --exit-on-error`. Команды для встроенного PG прошли restore-прогоны на k3d с дефолтным HPA (откат на месте, потеря PVC, потеря namespace, битый дамп): данные и silences целы, миграции не перезапускаются, до ready меньше минуты. Для внешнего PG там же описаны отличия (restore в пустую БД без `--clean`), но прогона не было.
- **Обоснование**:
  - чарт не должен обещать того, чего не делает. Неработающий бэкап хуже отсутствующего: оператор узнаёт о нём при восстановлении;
  - `backup.enabled: true` в values — сигнал, что оператор на бэкап рассчитывает. Поэтому он падает на `helm upgrade`, а не игнорируется молча (как в ADR-015). Под guard попадает и `--reuse-values` со старого релиза: старый дефолт чарта переносится в values. Текст ошибки называет обход (`--reset-then-reuse-values` или `--set postgresql.backup.enabled=false`);
  - бэкап на PVC рядом с базой не off-cluster: теряется вместе с нодой или namespace. Логический дамп на машину оператора или в его хранилище закрывает именно этот случай.
- **Отклонено**:
  - довести встроенный PostgreSQL до HA/PITR. Это меняет поведение существующих баз и по сути означает собственный PG-оператор в чарте (2–3 дня и больше);
  - логический бэкап-CronJob в чарте сейчас. Бэкап рядом с базой ≠ off-cluster; кандидат в отдельный слайс после `HELM-EXTERNAL-DATABASE`;
  - CloudNativePG как сабчарт. Новая зависимость и оператор с CRD — решение владельца кластера, не чарта приложения.
- **Следствие**:
  - breaking для values (migration notes в `CHANGELOG.md`):
    - ключи `postgresql.backup.*` и `postgresql.podDisruptionBudget.*` удалены, `backup.enabled: true` падает (в том числе при `--reuse-values`);
    - `replicas != 1` падает;
    - при upgrade удаляется PVC `<fullname>-postgres-backup`: base backup на нём ни разу не создавался;
    - один рестарт Postgres при upgrade, на это время ingest отвечает 500;
  - `helm rollback` возвращает CronJob, PDB и пустой backup PVC; том `postgres-data` не затрагивается ни в одну сторону;
  - `HELM-EXTERNAL-DATABASE` становится предпосылкой прод-установки;
  - `PG-CONFIG-NOT-APPLIED` остаётся открытым: блок `postgresql.config` в values по-прежнему не применяется. _(Закрыт 2026-10-18 задачей `PG-CONFIG-FIX`.)_

## ADR-017: Кеш AMP в чарте — Valkey; Redis ≥ 7.4 не используется
- **Дата**: 2026-10-19
- **Контекст**:
  - собственный StatefulSet чарта (`<fullname>-redis`) запускал `redis:7-alpine`; на 2026-10-19 этот тег резолвится в Redis 7.4.11, который с 7.4 распространяется под RSALv2/SSPLv1, а не BSD. AMP — AGPL-3.0, и такая лицензия кеша владельцу не подходит (решение владельца 2026-10-19, `HELM-SINGLE-NODE-DEFAULTS`);
  - рядом в каждом `standard`-рендере жил сабчарт `valkey` (groundhog2k 2.1.3, `condition: cache.enabled`): AMP в него не ходил (`cache.host` → `<fullname>-redis`), под падал в `Init:Error` (BUGS `VALKEY-SUBCHART-INIT-ERROR`), а ресурсы и PVC 5Gi занимал;
  - замеры (`tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/`): `valkey/valkey:8.1.10-alpine` поднимается с конфигом чарта без правок (`redis-server`/`redis-cli` в образе — симлинки на `valkey-*`), `redis_exporter` v1.55.0 с ним работает; Valkey 8.1 и 9.0 не читают данные Redis 7.4 (`Can't handle RDB format version 12`); Redis 7.4 читает данные Valkey 8.1 (RDB v11); Valkey 9.0 пишет сигнатуру `VALKEY080`, правило переноса её не трогает.
- **Решение**:
  - кеш AMP в чарте — один инстанс, собственный StatefulSet чарта на `valkey/valkey` (BSD-3), тег закреплён до патча (`8.1.10-alpine`);
  - сабчарт `valkey` и `Chart.lock` удалены, у чарта нет зависимостей;
  - init-контейнер `config-init` при образе `*valkey*` переносит в `/data/redis-pre-valkey-<ts>/` `dump.rdb` и `appendonlydir` — каждый отдельно, если его RDB имеет сигнатуру `REDIS` и версию ≥ 12; Valkey стартует без них; при образе без `valkey` в имени перенос не выполняется;
  - образы Redis ≥ 7.4 не используются в чарте и в стендах репозитория (`deploy/e2e-ha`). Внешний Redis-совместимый кеш (`cache.host`, как Dragonfly в `values-production.yaml`) — выбор и ответственность оператора.
- **Обоснование**:
  - один кеш вместо двух: меньше ресурсов на одной ноде, нет внешнего helm-репозитория в сборке, нет сломанного пода;
  - собственный StatefulSet уже несёт exporter, ServiceMonitor, PrometheusRule и дашборд, а `cache.host` и Service не меняются — пользовательские values работают без правок;
  - перенос, а не удаление данных: обычный `helm upgrade` не превращается в crashloop, а старые файлы остаются для отката.
- **Отклонено**:
  - Redis 7.2 (последняя BSD-версия): ветка без поддержки, тот же вопрос при следующем обновлении;
  - оставить сабчарт groundhog2k и убрать свой StatefulSet: сабчарт сломан, без exporter/правил/дашборда, меняет `cache.host`, тянет внешний репозиторий;
  - перенос ключей из Redis 7.4 в Valkey: DUMP/RESTORE несут ту же версию RDB, остаётся поштучный SCAN с временным Redis-сайдкаром — несоразмерно кешу, где долговечны только nflog, группы и таймеры с TTL ≤ `repeat_interval`.
- **Следствие**:
  - breaking при `helm upgrade` с дефолтами (migration notes в `CHANGELOG.md`): данные кеша сбрасываются ⇒ один повтор уведомлений по firing-группам, возможна потеря resolved по алертам, погасшим во время апгрейда; silences и алерты в PostgreSQL не затронуты. С 2026-10-20 (`AMP-CACHE-NOAUTH`) AMP на дефолтах авторизуется в этом кеше паролем Valkey (`<fullname>-redis-secret` или `valkey.password.existingSecret`), пароль переживает `helm upgrade` (`lookup`); до этого AMP держал состояние в памяти. Первый upgrade рестартует AMP и Valkey вместе: с 2026-10-22 (`AMP-CACHE-NO-RECONNECT`) AMP ждёт кеш на старте до `redis.startup_wait` (60s) и только потом уходит на in-memory до рестарта (подсказка в NOTES);
  - каталог `/data/redis-pre-valkey-*` чарт не удаляет, это ручной шаг;
  - при переходе на Valkey 9.x перепроверить правило переноса (9.0.6 пишет `VALKEY080` — замер на ревью);
  - гейт по имени репозитория образа: зеркало Valkey под другим именем пропускает перенос и падает на данных v12 — оператор задаёт `repository` и `tag` вместе (комментарий в `values.yaml`);
  - Go-тесты (`go-app/internal/infrastructure/inhibition/integration_test.go`) ещё поднимают `redis:7-alpine` через testcontainers — локально и в CI, не в поставке; перевод — отдельной задачей.

## ADR-018: AMP — AI-слой перед upstream Alertmanager (прокси на ingest), не замена
- **Дата**: 2026-10-23
- **Статус**: заменён ADR-019 (2026-10-24) — работа по нему не начиналась.
- **Контекст**:
  - явного решения «писать ядро Alertmanager самим или брать upstream» не было: ADR-001 фиксирует только язык, ADR-002 — объём заявлений;
  - из 128k prod-строк `go-app/` ~66k повторяют upstream (routing, dispatch, notify chain, nflog, silences, inhibition, интеграции, шаблоны, HA на Redis); то же ядро upstream v0.32 — ~20k строк. AMP-уникальное (LLM-классификация, investigation, история в PG, filter/dedup) — ~8–9k;
  - паритет — основная цена проекта: 7 из 13 critical/high ревью 2026-10-21 и ~63% открытого бэклога `BUGS.md`/`TECH-DEBT.md` — расхождения с upstream или следствие своего Redis/PG-состояния;
  - при группировке (по умолчанию) классификация в уведомление не попадает (`ALERTMANAGER_COMPATIBILITY.md` Known Gap #13): AI сейчас слабо влияет на доставку;
  - публичных релизов не было, база инсталляций пилотная — переход дешевле, чем когда-либо позже.
- **Решение** (владелец):
  - доставкой занимается upstream Alertmanager. AMP стоит перед ним: Prometheus → AMP `POST /api/v2/alerts` → AM `POST /api/v2/alerts`, пересылка во все пиры AM;
  - AMP обогащает алерт annotations `amp_*` (классификация, ссылка на investigation по fingerprint) — они доходят до всех уведомлений AM, включая сгруппированные; дроп шума — только за явным флагом, default off;
  - пересылка не зависит от LLM, PostgreSQL и кеша (fail-open): у классификации бюджет времени меньше `group_wait`, по истечении алерт уходит без annotations; история и investigation — после пересылки, best-effort;
  - паритетное ядро, интеграции, шаблоны, K8s-Secret таргеты и HA-состояние на Redis удаляются; AMP не отдаёт AM API кроме приёма алертов.
- **Обоснование**:
  - постоянная плата за паритет исчезает: контракт `POST /api/v2/alerts` стабилен, annotations — свободные строки;
  - AI влияет на доставку сильнее, чем сейчас (annotations в сгруппированных уведомлениях), а история полная — AMP видит алерты до silence/inhibit;
  - порог внедрения ниже: пользователь оставляет свой AM и меняет адрес в Prometheus;
  - обратимо: AI-код общий с вариантом «пакеты upstream в бинаре AMP», переход туда позже возможен без потерь.
- **Отклонено**:
  - своё ядро (как было): паритет — бесконечная работа, которую upstream уже сделал;
  - пакеты upstream как библиотеки в бинаре AMP: ~21–29d, Go API upstream ломается между минорами, ценой — позиция «замены», которая не является ценностью AMP; высокий порог внедрения;
  - AMP после AM через `webhook_configs`: AI не влияет на доставку, silenced/inhibited алерты до AMP не доходят, повторы `repeat_interval` надо дедуплицировать.
- **Следствие**:
  - AMP — критический путь доставки: в проде ≥ 2 реплики; повторная пересылка одного алерта несколькими репликами безопасна (AM сливает по fingerprint);
  - annotations, пришедшие после первого уведомления группы, повторной отправки не вызывают (nflog AM) — отсюда бюджет < `group_wait`;
  - эпик ~12–18d, шесть срезов, первым — PoC 4–5d (форвардер, annotations, fail-open, e2e Prometheus → AMP → AM на k3d); нарезка в `BACKLOG.md` — задача `ADR-018-V3-PROXY-SLICING`. `PROD-LLM-ALERT-PATH-ISOLATION` поглощается PoC;
  - паритетные позиции Queue, `REVIEW-PIPELINE-2026-10-21.md`, `BUGS.md`, `TECH-DEBT.md` не берутся; security/critical — только по коду, который переживает переход (ingest, LLM, investigation, история, UI, auth);
  - ADR-002, -006, -009, -016, -017 пересматриваются в срезах, которые их затрагивают;
  - открыто: интеграция Rootly (webhook AM или отказ), доставка результата investigation в тред (свой клиент мессенджера вне паритета).

## ADR-019: AMP — замена Alertmanager на пакетах upstream; HA — memberlist upstream
- **Дата**: 2026-10-24
- **Заменяет**: ADR-018.
- **Контекст**:
  - контекст ADR-018 в силе: ~66k из 128k prod-строк повторяют upstream, паритет — основная цена проекта, публичных релизов не было;
  - ADR-018 исходил из того, что позиция «замены AM» — не ценность AMP. Владелец уточнил цель продукта: «удобный Alertmanager с AI, историей, анализом алертов и HA» — то есть замена;
  - прокси перед upstream AM (ADR-018), в том числе в виде чарта, который ставит AM внутрь, — это два продукта с двумя конфигами: маршрутизация и silences в AM, AI и история в AMP.
- **Решение** (владелец):
  - ядро доставки — пакеты upstream Alertmanager как библиотеки в бинаре AMP: `dispatch`, `notify`, `provider/mem`, `silence`, `inhibit`, `nflog`, `api/v2`, `config`. Версия upstream закрепляется точно;
  - один конфиг — `alertmanager.yml` upstream; один API — `api/v2` upstream побайтно, AMP-маршруты (история, investigation, `/api/v2/inhibitions`) — отдельно;
  - HA — memberlist upstream (`cluster.Peer`) со снапшотами silences/nflog; своё HA-состояние на Redis (lock, heartbeat, группы, таймеры, nflog) удаляется;
  - AI встраивается в конвейер upstream: `AlertStoreCallback.PreStore` — только дешёвый фильтр без LLM (синхронен в пути `Put`), дроп шума — за флагом default off; `PostStore` — история в PG и асинхронный запуск классификации и investigation; своя notify-стадия перед интеграциями ждёт классификацию с бюджетом и отдаёт её шаблонам группы;
  - собственные routing, dispatcher, notify chain, nflog, таймеры, silences, inhibition, интеграции и шаблоны удаляются. Интеграции — все 18 из upstream `notify/*` (у AMP было 5); своё — только то, чего в upstream нет: Rootly и K8s-Secret таргеты.
- **Обоснование**:
  - паритет становится свойством кода, а не задачей: его поддерживает upstream;
  - пользователь получает один продукт: один бинарь, один конфиг, один API для amtool/Grafana/UI;
  - только встраивание в конвейер даёт AI на уровне группы в момент отправки и доставку результата investigation своим `notify.Notifier`; классификация уходит из ingest-пути.
- **Отклонено**:
  - своё ядро (до ADR-018): паритет — бесконечная работа, которую upstream уже сделал;
  - прокси перед upstream AM (ADR-018) и чарт с AM внутри: два продукта и два конфига, AI не видит группу целиком;
  - AMP после AM через `webhook_configs`: AI не влияет на доставку, история неполная;
  - полный форк upstream: AMP-код в дереве upstream, каждый rebase конфликтует, дороже библиотечного варианта.
- **Следствие**:
  - эпик ~20–28d, семь срезов; первым — PoC 4–5d с критерием выхода: upstream-ядро в бинаре AMP проходит `main_phase0_contract_test.go`, `main_upstream_parity_regression_test.go` и amtool-гейт, иначе — стоп и пересмотр. Нарезка — `BACKLOG.md` § «ADR-019 — AMP на пакетах upstream Alertmanager» (`ADR-019-V2-SLICING`, 2026-10-24);
  - постоянная плата: Go API upstream не библиотечный контракт и ломается между минорами (`NewDispatcher`, `mem.NewAlerts`, `NewInhibitor`, `PipelineBuilder` менялись в 0.28–0.32) — ~0.5–1d на минор, 2–3 в год;
  - breaking для пилотных инсталляций: silences переносятся из PostgreSQL в снапшот upstream, Redis-состояние сбрасывается, теряются AMP-расширения API (`?group_by=`, `global.group_by`), если не держать их отдельными маршрутами;
  - чарт: StatefulSet, headless Service и порт gossip; Valkey остаётся только как кеш классификации;
  - позиции Queue, `REVIEW-PIPELINE-2026-10-21.md`, `BUGS.md`, `TECH-DEBT.md` на совместимость не берутся — их закрывает код upstream; security/critical — по коду, который переживает переход; `PROD-LLM-ALERT-PATH-ISOLATION` закрывается срезом AI-хуков;
  - ADR-002, -006, -009, -016, -017 пересматриваются в срезах, которые их затрагивают;
  - открыто: K8s-Secret таргеты — адаптер в `config.Receiver` или удаление (решается в срезе интеграций).
