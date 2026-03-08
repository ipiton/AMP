# Research: PHASE-4-PRODUCTION-PUBLISHING-PATH

Дата: 2026-03-08

## Scope
Исследование перед `/spec` для задачи замены `SimplePublisher` в активном runtime на реальный production publishing path.

## Current Runtime State
- Активный bootstrap в `go-app/internal/application/service_registry.go` поднимает `services.NewSimplePublisher(...)`.
- `initializeBusinessServices()` в `ServiceRegistry` пока не подключает publishing stack.
- `AlertProcessor` зависит от `services.Publisher`, который работает с `*core.Alert` и `*core.ClassificationResult`.
- Текущий `internal/application/router.go` публикует только базовые API/health/metrics routes; publishing endpoints и dashboard publishing wiring в активный runtime не включены.

## Existing Building Blocks
- В `go-app/internal/infrastructure/publishing/` есть delivery primitives:
  - `PublisherFactory`
  - `PublishingQueue`
  - `PublishingCoordinator`
  - `ModeManager`
  - HTTP handlers для publishing API
- В `go-app/internal/business/publishing/` есть более богатый operational слой:
  - `TargetDiscoveryManager`
  - `RefreshManager`
  - `HealthMonitor`
  - metrics collectors / trend analysis / health stats
- В `go-app/internal/infrastructure/k8s/` есть `K8sClient` для чтения Secrets и health-check'а Kubernetes API.

## Findings

### 1. Main integration gap: interface mismatch
- `AlertProcessor` ожидает `services.Publisher`:
  - `PublishToAll(ctx, *core.Alert) error`
  - `PublishWithClassification(ctx, *core.Alert, *core.ClassificationResult) error`
- Реальный publishing stack работает с `*core.EnrichedAlert`:
  - queue / coordinator / publishers принимают `*core.EnrichedAlert`
- Готового adapter-а между этими двумя контрактами в репозитории нет.

Вывод:
- Для замены `SimplePublisher` нужен adapter уровня application/service, который:
  - принимает `*core.Alert`
  - при необходимости добавляет `*core.ClassificationResult`
  - собирает `*core.EnrichedAlert`
  - отправляет его в queue/coordinator/parallel publisher

### 2. В кодовой базе две разные publishing-линии, и их интерфейсы несовместимы
- `internal/business/publishing.TargetDiscoveryManager` содержит `GetStats()` и `Health(ctx)`.
- `internal/infrastructure/publishing.TargetDiscoveryManager` содержит только discovery/list/get operations без `GetStats()` и `Health(ctx)`.
- Аналогично дублируются refresh/discovery responsibilities:
  - `internal/business/publishing/refresh_manager_impl.go`
  - `internal/infrastructure/publishing/refresh.go`

Вывод:
- До реализации нужно выбрать source of truth для discovery/refresh/health.
- Для dashboard/health integration удобнее `internal/business/publishing`, потому что существующие handlers уже завязаны именно на его интерфейсы.

### 3. Исторический bootstrap в `main.go.full` не является надёжной базой для переноса
- В историческом `go-app/cmd/server/main.go.full` discovery/refresh/health блоки для publishing в значительной части закомментированы.
- Там же mode management местами строится на stub discovery, а не на реальном discovery manager.
- Часть вызовов устарела относительно текущих конструкторов. Пример: вызов `NewPublishingQueue(...)` в `main.go.full` не совпадает с текущей сигнатурой в `internal/infrastructure/publishing/queue.go`.

Вывод:
- Нельзя просто “вернуть старый код”.
- Нужен новый согласованный bootstrap в `ServiceRegistry`, а не механический перенос из legacy `main.go.full`.

### 4. В typed config нет publishing contract для active runtime
- В `go-app/internal/config/config.go` нет `PublishingConfig`/`TargetDiscoveryConfig`.
- Исторический код опирался на ad-hoc env variables вроде:
  - `K8S_NAMESPACE`
  - `TARGET_REFRESH_INTERVAL`
  - `TARGET_HEALTH_CHECK_INTERVAL`
  - `TARGET_HEALTH_CHECK_TIMEOUT`
  - `PUBLISHING_WORKER_COUNT`
- Эти параметры не описаны как first-class поля typed config.

Вывод:
- `/spec` должен зафиксировать config contract для publishing path, иначе bootstrap снова будет собран на несогласованных env-переменных.

### 5. Helm/deployment contract расходится с runtime contract
- `internal/config` читает env в стиле `APP_ENVIRONMENT`, `PROFILE`, `DATABASE_HOST`.
- Helm deployment сейчас задаёт, например:
  - `ENVIRONMENT`
  - `DEPLOYMENT_PROFILE`
  - `POSTGRES_HOST`
- Это уже показывает расхождение между chart и runtime config model.

- Дополнительно:
  - chart имеет `publishing.enabled` и `targetDiscovery.*`, но в `go-app` нет читателя этих ключей как runtime config.
  - chart генерирует publishing target secrets через `helm/amp/templates/secret.yaml`.

### 6. Формат target secrets в Helm несовместим с discovery code
- `internal/business/publishing` по умолчанию ищет secrets по label selector `publishing-target=true` и парсит `secret.Data["config"]` как JSON `PublishingTarget`.
- `internal/infrastructure/publishing` тоже по умолчанию ищет label selector `publishing-target=true`, но ожидает discrete fields:
  - `name`
  - `type`
  - `url`
  - `api_key` / `auth_token`
- Helm template `helm/amp/templates/secret.yaml` генерирует другой контракт:
  - labels: `amp.io/target`, `amp.io/target-name`, `amp.io/target-type`
  - data keys: `target-name`, `webhook-url`, `api-key`, `format`, `enabled`

Вывод:
- Текущий chart-generated publishing target secret не совместим ни с business discovery parser, ни с infrastructure discovery parser.
- `/spec` обязан выбрать один canonical secret format и привести Helm + runtime к одному контракту.

### 7. Observability wiring пока неполное и местами несогласованное
- Активный `internal/application/router.go` не регистрирует publishing management/statistics endpoints.
- `cmd/server/handlers/dashboard_health.go` ожидает `internal/business/publishing.TargetDiscoveryManager` и `HealthMonitor`.
- `cmd/server/handlers/dashboard_overview.go` берёт publishing stats через metrics snapshot, но ищет ключи вида:
  - `discovery.total_targets`
  - `queue.jobs_succeeded_total`
  - `queue.jobs_failed_total`
- При этом существующие collectors отдают имена без этих префиксов:
  - `targets_total`
  - `jobs_submitted_total`
  - `jobs_completed_total`
  - `jobs_failed_total`

Вывод:
- Даже после подключения queue/discovery нужен отдельный слой согласования observability contract.
- Иначе dashboard будет показывать `unknown/0` при формально работающем publishing stack.

### 8. Router integration потребует отдельного решения
- Активный runtime использует `net/http.ServeMux`.
- Publishing handlers в `internal/infrastructure/publishing/handlers.go` рассчитаны на `*gorilla/mux.Router`.

Вывод:
- В `/spec` нужно решить, как именно встраивать publishing HTTP surface:
  - отдельный subrouter/adaptor
  - перепись route registration на `ServeMux`
  - или отложить публичные publishing endpoints и сначала подключить только внутренний delivery path

### 9. Profile-aware behavior надо определить явно
- `internal/infrastructure/k8s.NewK8sClient()` использует `rest.InClusterConfig()`, то есть полноценный secret discovery path завязан на запуск внутри Kubernetes.
- Это не подходит как обязательный startup path для local/lite/dev.
- При этом `SimplePublisher` уже специально запрещён для production semantics.

Вывод:
- `/spec` должен явно описать поведение по профилям:
  - `standard` в Kubernetes
  - `lite` / local development
  - `standard` без доступного K8s API / без targets

## Recommended Direction For Spec

### A. Выбрать архитектурный split
- `internal/business/publishing`:
  - source of truth для discovery/refresh/health/stats
- `internal/infrastructure/publishing`:
  - source of truth для delivery primitives (factory/queue/coordinator/mode manager/publishers)
- `internal/application`:
  - adapter + bootstrap + lifecycle orchestration

### B. Ввести adapter для `services.Publisher`
- Новый application-level publisher должен реализовать `services.Publisher`.
- Внутри:
  - собирать `core.EnrichedAlert`
  - передавать alert в coordinator/queue
  - учитывать `ModeManager`
  - не имитировать успех через stub

### C. Зафиксировать canonical config + secret contract
- Нужен typed config раздел для:
  - enable/disable publishing path
  - queue settings
  - discovery namespace/label selector
  - refresh/health intervals
  - fallback policy
- Нужен единый canonical secret format.
- Helm templates должны генерировать ровно тот формат, который понимает runtime.

### D. Встроить lifecycle в `ServiceRegistry`
- Инициализация:
  - K8s client
  - discovery manager
  - refresh manager
  - health monitor
  - mode manager
  - publisher factory
  - queue/coordinator
  - adapter publisher
- Shutdown:
  - refresh manager
  - health monitor
  - mode manager
  - queue
  - k8s client

### E. Не смешивать первый срез задачи с полным HTTP/UI rewire
- Минимально полезный вертикальный срез:
  - заменить stub publisher в active processing path
  - получить реальные delivery attempts
  - обеспечить health/mode/metrics внутри runtime
- Publishing management HTTP endpoints можно сделать отдельным подэтапом, если это ускорит безопасную доставку первого production slice.

## Questions For Spec
1. Какой canonical источник targets выбираем:
   - Kubernetes Secrets discovery
   - статическая config-модель
   - гибрид
2. Как runtime ведёт себя в `standard` profile, если K8s доступен, но targets = 0:
   - `metrics-only`
   - startup warning
   - fail startup
3. Как runtime ведёт себя в `standard` profile, если K8s API недоступен:
   - fail closed
   - degraded `metrics-only`
   - configurable policy
4. Входит ли publishing HTTP/API surface в scope ближайшего vertical slice, или сначала меняем только internal delivery path?
5. Какой именно secret format становится canonical:
   - JSON `config`
   - discrete fields
   - новый третий формат

## Research Outcome
- Задача требует `/spec` перед кодом.
- Реализация сводится не к “подключить готовый publisher”, а к согласованию трёх контрактов:
  - application publisher interface
  - publishing runtime components
  - deployment/config/secret model
