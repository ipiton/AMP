# PHASE-4-PRODUCTION-PUBLISHING-PATH - Spec

**Status**: Draft v1  
**Date**: 2026-03-08  
**Inputs**: `requirements.md`, `research.md`

---

## 1. Problem Statement

Активный runtime уже умеет принимать и обрабатывать alerts, но его publishing path в `ServiceRegistry` все еще собран через `services.NewSimplePublisher(...)`. Это создает разрыв между реальными возможностями кодовой базы и фактическим поведением runtime:

- alert processing выглядит production-ready, но доставка фактически не выполняется;
- observability publishing path неполная и местами несогласованная;
- deployment/chart contract не совпадает с тем, что ожидает runtime discovery/config слой.

Цель этого spec: определить минимальный, но рабочий production publishing path для active runtime без возврата к legacy `main.go.full`.

---

## 2. Goals

1. Убрать `SimplePublisher` из active production bootstrap-path.
2. Подключить реальную доставку через существующие queue/coordinator/publisher components.
3. Зафиксировать единый config contract и единый target secret contract.
4. Встроить publishing lifecycle в `ServiceRegistry`.
5. Сохранить безопасную деградацию в `metrics-only`, если delivery path недоступен.

## 3. Non-Goals

1. Не переносить обратно весь legacy bootstrap из `go-app/cmd/server/main.go.full`.
2. Не делать в этом vertical slice полный rewire publishing management API/UI.
3. Не добавлять статический config-based target source для local/dev в первой итерации.
4. Не вводить fail-closed startup policy в первой итерации.

---

## 4. Key Decisions

### 4.1 Source of Truth

- `internal/business/publishing` становится source of truth для:
  - target discovery
  - refresh lifecycle
  - health monitoring
  - discovery/health stats collectors
- `internal/infrastructure/publishing` остается source of truth для:
  - concrete publishers
  - publisher factory
  - queue
  - coordinator
  - mode manager

### 4.2 Delivery Model

Active runtime использует **queue-based delivery path**:

`AlertProcessor -> services.Publisher adapter -> EnrichedAlert -> PublishingCoordinator -> PublishingQueue -> PublisherFactory -> target publishers`

Причина выбора:
- queue уже содержит retries, worker pool, DLQ/job tracking hooks и metrics;
- coordinator уже умеет fan-out по targets и учитывать `ModeManager`;
- это минимальный путь к реальной доставке без нового orchestration слоя.

### 4.3 Canonical Target Source

Для первого production slice canonical source targets: **Kubernetes Secrets discovery**.

- `standard` profile + in-cluster deployment: реальный delivery path доступен;
- `lite` profile: только `metrics-only` режим;
- static config targets и hybrid model выносятся в follow-up.

### 4.4 Canonical Secret Format

Canonical contract: Secret с label `publishing-target=true` и полем `data.config`, содержащим JSON `core.PublishingTarget`.

Выбран формат `config` JSON, потому что:
- его уже умеет разбирать `internal/business/publishing`;
- он расширяем без размножения key-specific parsers;
- он хорошо подходит для chart-generated and manually managed targets.

### 4.5 Degraded Startup Policy

Для первой итерации policy фиксирован:

- если publishing stack нельзя собрать полностью, runtime **не падает на старте**;
- runtime переходит в **metrics-only mode**;
- ingest/processing продолжаются;
- observability явно показывает degraded publishing state.

`SimplePublisher` при этом не используется вообще.

---

## 5. Assumptions For This Spec

1. Реальный publishing path в первой итерации нужен только для `standard` profile.
2. Запуск вне Kubernetes не обязан поддерживать real target discovery.
3. Publishing management HTTP surface может быть отложен, если internal delivery path и observability уже консистентны.
4. Helm chart можно менять в части publishing-related contracts.

---

## 6. Target Architecture

```text
                  +------------------+
                  |  AlertProcessor  |
                  +---------+--------+
                            |
                            v
              +-------------+--------------+
              | ApplicationPublishingAdapter|
              | implements services.Publisher|
              +-------------+--------------+
                            |
                            v
                   +--------+--------+
                   | EnrichedAlert    |
                   +--------+--------+
                            |
                            v
                 +----------+-----------+
                 | PublishingCoordinator|
                 +----------+-----------+
                            |
                            v
                    +-------+-------+
                    | PublishingQueue|
                    +-------+-------+
                            |
                            v
                    +-------+-------+
                    | PublisherFactory|
                    +-------+-------+
                            |
     +----------------------+----------------------+
     |                      |                      |
     v                      v                      v
  Rootly                PagerDuty               Slack/Webhook

Support Plane:

K8sClient
  -> business.TargetDiscoveryManager
  -> DiscoveryAdapter
  -> infrastructure.ModeManager
  -> business.RefreshManager
  -> business.HealthMonitor
  -> business.PublishingMetricsCollector
```

---

## 7. Component Design

### 7.1 New Application Components

#### A. `ApplicationPublishingAdapter`

**Proposed file**: `go-app/internal/application/publishing_adapter.go`

**Responsibility**:
- реализует `services.Publisher`;
- преобразует `*core.Alert` + optional `*core.ClassificationResult` в `*core.EnrichedAlert`;
- делегирует публикацию в `PublishingCoordinator`.

**Interface contract**:

```go
type Publisher interface {
    PublishToAll(ctx context.Context, alert *core.Alert) error
    PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error
}
```

**Behavior**:
- `PublishToAll(...)`:
  - строит `core.EnrichedAlert{Alert: alert}`
  - вызывает `coordinator.PublishToAll(...)`
- `PublishWithClassification(...)`:
  - строит `core.EnrichedAlert{Alert: alert, Classification: classification}`
  - вызывает `coordinator.PublishToAll(...)`

**Error semantics**:
- если coordinator/queue возвращают ошибку enqueue/publish path -> вернуть error наверх;
- если mode manager перевел runtime в `metrics-only`, adapter не подменяет это fake delivery stub-ом;
- skipped delivery отражается через mode/queue metrics и structured logs.

#### B. `MetricsOnlyPublisher`

**Proposed file**: `go-app/internal/application/publishing_metrics_only.go`

**Responsibility**:
- быть explicit fallback publisher-ом вместо `SimplePublisher`;
- использоваться, когда real publishing stack не инициализирован или отключен по profile/config.

**Behavior**:
- не делает external delivery;
- логирует причину (`disabled`, `lite_profile`, `publishing_stack_unavailable`, `metrics_only_mode`);
- не маскируется под “real publisher”.

`MetricsOnlyPublisher` нужен, потому что `AlertProcessor` требует non-nil `services.Publisher`, а `SimplePublisher` должен исчезнуть из active runtime.

#### C. `DiscoveryAdapter`

**Proposed file**: `go-app/internal/application/publishing_discovery_adapter.go`

**Responsibility**:
- адаптировать `internal/business/publishing.TargetDiscoveryManager` к интерфейсу, который нужен `internal/infrastructure/publishing.ModeManager` и `PublishingCoordinator`.

**Adapted methods**:
- `GetTarget(name string)`
- `ListTargets()`
- `GetTargetsByType(type string)`
- `GetTargetCount()`

Причина:
- delivery primitives уже ждут infrastructure-style discovery interface;
- dashboard/health/stats уже ждут business-style discovery interface;
- adapter дешевле и безопаснее, чем делать новый общий абстрактный слой.

### 7.2 `ServiceRegistry` Changes

`ServiceRegistry` должен стать владельцем publishing lifecycle.

**New fields**:
- `k8sClient k8s.K8sClient`
- `publishingDiscovery businesspublishing.TargetDiscoveryManager`
- `publishingDiscoveryAdapter *DiscoveryAdapter`
- `publishingRefresh businesspublishing.RefreshManager`
- `publishingHealth businesspublishing.HealthMonitor`
- `publishingMode infrapublishing.ModeManager`
- `publishingQueue *infrapublishing.PublishingQueue`
- `publishingCoordinator *infrapublishing.PublishingCoordinator`
- `publishingMetricsCollector *businesspublishing.PublishingMetricsCollector`
- `publisherFactory *infrapublishing.PublisherFactory`

**Initialization order**:
1. metrics/cache/storage base infrastructure
2. publishing discovery prerequisites
3. mode manager
4. publisher factory
5. queue
6. coordinator
7. refresh manager
8. health monitor
9. application publisher adapter
10. alert processor

**Shutdown order**:
1. refresh manager
2. health monitor
3. mode manager
4. queue
5. k8s client
6. database/cache

### 7.3 Active Runtime Wiring

Для первого slice active runtime не обязан поднимать весь publishing HTTP/API surface.

Обязательное wiring:
- `AlertProcessor` должен использовать новый application publisher adapter;
- `ServiceRegistry.Health(ctx)` должен учитывать publishing stack state;
- metrics collector должен быть доступен для publishing observability.

Отложенное wiring:
- `internal/infrastructure/publishing/handlers.go`
- полный `/api/v1|v2/publishing/*`
- dashboard pages / mode endpoints / DLQ endpoints

---

## 8. Runtime Behavior Matrix

| Profile / State | Behavior |
|---|---|
| `lite` | Всегда `metrics-only`, real delivery path не инициализируется |
| `standard` + `publishing.enabled=false` | `MetricsOnlyPublisher`, discovery/queue не создаются |
| `standard` + K8s available + targets found | Полный publishing stack, mode=`normal` |
| `standard` + K8s available + zero targets | Runtime стартует, mode=`metrics-only` |
| `standard` + K8s unavailable / discovery init failed | Runtime стартует, mode=`metrics-only` |
| `standard` + queue init failed | Runtime стартует, fallback в `MetricsOnlyPublisher` |

### Readiness / Health Policy

Для первого slice:
- runtime readiness не падает только из-за отсутствия publishing targets;
- publishing отсутствие трактуется как **degraded**, а не как hard startup failure;
- status должен быть наблюдаем через health/metrics, а не скрыт за stub publisher.

---

## 9. Config Contract

### 9.1 Typed Config Additions

**Proposed config shape**:

```yaml
publishing:
  enabled: true
  target_discovery:
    enabled: true
    namespace: "alert-history"
    label_selector: "publishing-target=true"
    refresh_interval: 5m
  health:
    enabled: true
    check_interval: 2m
    http_timeout: 5s
    failure_threshold: 3
  queue:
    worker_count: 10
    high_priority_queue_size: 500
    medium_priority_queue_size: 1000
    low_priority_queue_size: 500
    max_retries: 3
    retry_interval: 2s
```

**Proposed Go structs**:
- `PublishingConfig`
- `PublishingTargetDiscoveryConfig`
- `PublishingHealthConfig`
- `PublishingQueueConfig`

### 9.2 Environment Variable Mapping

Viper contract должен следовать уже используемому правилу `mapstructure path -> ENV_WITH_UNDERSCORES`.

Примеры:
- `APP_ENVIRONMENT`
- `PROFILE`
- `PUBLISHING_ENABLED`
- `PUBLISHING_TARGET_DISCOVERY_ENABLED`
- `PUBLISHING_TARGET_DISCOVERY_NAMESPACE`
- `PUBLISHING_TARGET_DISCOVERY_LABEL_SELECTOR`
- `PUBLISHING_TARGET_DISCOVERY_REFRESH_INTERVAL`
- `PUBLISHING_HEALTH_CHECK_INTERVAL`
- `PUBLISHING_HEALTH_HTTP_TIMEOUT`
- `PUBLISHING_QUEUE_WORKER_COUNT`

### 9.3 Helm Alignment Rule

Для Phase 4 chart не должен полагаться на dashed `envFrom` keys как canonical runtime contract.

Решение:
- publishing-related values передаются в приложение только через Viper-compatible env names;
- `APP_ENVIRONMENT` и `PROFILE` тоже выравниваются в chart, потому что publishing behavior зависит от них напрямую.

---

## 10. Canonical Secret Contract

### 10.1 Kubernetes Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: rootly-prod
  labels:
    publishing-target: "true"
    amp.io/target: "true"
    amp.io/target-name: "rootly-prod"
    amp.io/target-type: "rootly"
type: Opaque
data:
  config: <base64(json)>
```

### 10.2 JSON Payload Inside `data.config`

```json
{
  "name": "rootly-prod",
  "type": "rootly",
  "url": "https://api.rootly.com/v1/incidents",
  "enabled": true,
  "format": "rootly",
  "headers": {
    "Authorization": "Bearer <rootly-api-key>"
  },
  "filter_config": {
    "severity": ["critical", "warning"],
    "namespaces": ["production"]
  }
}
```

### 10.3 Migration Rule

Helm-generated publishing target secrets должны быть переведены на этот контракт.

Совместимость на Phase 4:
- auxiliary labels `amp.io/*` можно сохранить;
- canonical discovery label обязателен: `publishing-target=true`;
- canonical data key обязателен: `config`.

---

## 11. Observability Contract

### 11.1 What Must Be True

1. Если targets отсутствуют, runtime явно показывает `metrics-only`, а не “успешную публикацию”.
2. Если queue работает, dashboard/stats consumers получают реальные counters.
3. Health path умеет отличать `healthy` от `degraded`.

### 11.2 Metrics/Stats Alignment

Существующий mismatch по metric names решается на consumer side в этом slice.

Нормализуемые keys:
- `targets_total`
- `targets_valid`
- `targets_invalid`
- `jobs_submitted_total`
- `jobs_completed_total`
- `jobs_failed_total`

Правило:
- collectors не переименовываются массово;
- consumers (`dashboard_overview`-style providers или их active-runtime аналоги) читают canonical collector names.

### 11.3 Health Classification

Publishing state:
- `healthy`: stack initialized, targets > 0, queue available
- `degraded`: metrics-only fallback, zero targets, discovery unavailable, или health monitor reports unhealthy targets
- `not_configured`: publishing explicitly disabled

---

## 12. Implementation Slice

### Slice 1: Real Delivery Path In Active Runtime

Входит в scope:
- typed config для publishing
- `ServiceRegistry` bootstrap/shutdown publishing stack
- application publisher adapter
- metrics-only fallback publisher
- discovery adapter
- canonical secret parsing via business discovery manager
- queue/coordinator integration
- health/metrics alignment enough to наблюдать реальный state

Не входит в scope:
- полный `publishing handlers` HTTP surface
- DLQ/job management routes
- UI/dashboard pages
- static publishing target source
- fail-closed startup policy

---

## 13. Testing Strategy

### 13.1 Unit Tests

1. `ApplicationPublishingAdapter`
   - plain alert -> `EnrichedAlert`
   - classified alert -> `EnrichedAlert`
   - coordinator error propagation
2. `MetricsOnlyPublisher`
   - disabled/lite/degraded reasons
3. `DiscoveryAdapter`
   - business discovery -> infrastructure interface compatibility
4. config parsing
   - YAML + env overrides for publishing section

### 13.2 Integration Tests

1. `ServiceRegistry` initializes metrics-only mode when publishing disabled
2. `ServiceRegistry` initializes metrics-only mode when discovery fails
3. `ServiceRegistry` initializes full publishing stack when discovery returns valid targets
4. alert processing path enqueues delivery through coordinator/queue instead of stub

### 13.3 Regression Checks

1. existing alert ingest/query behavior unchanged
2. `go vet` passes
3. relevant tests for application + publishing packages pass
4. build succeeds for `./cmd/server`

---

## 14. Acceptance Criteria

1. В active runtime больше нет `SimplePublisher` в production bootstrap path.
2. `AlertProcessor` публикует через real adapter/coordinator/queue path.
3. При отсутствии real stack runtime уходит в explicit `metrics-only`, а не в stub delivery.
4. `standard` profile в Kubernetes умеет discover targets из canonical secrets.
5. `ServiceRegistry` корректно стартует и останавливает publishing lifecycle.
6. Publishing health/mode/metrics отражают реальное состояние delivery path.
7. Helm contract для publishing targets совместим с runtime discovery contract.

---

## 15. Follow-Ups

1. Phase 4b: publishing HTTP/API surface в active runtime
2. Phase 4c: dashboard/UI wiring поверх active runtime
3. Phase 4d: static targets / local development real publishing mode
4. Phase 4e: optional fail-closed startup policy for strict production environments

---

## 16. Summary

Phase 4 реализуется как **application-level adapter + lifecycle orchestration**, а не как возврат к legacy bootstrap.

Ключевая идея:
- discovery/health/stats берем из `internal/business/publishing`;
- real delivery берем из `internal/infrastructure/publishing`;
- active runtime связываем через небольшой слой в `internal/application`.

Это даёт минимальный рабочий production slice без переизобретения publishing stack и без сохранения `SimplePublisher` в активном пути.
