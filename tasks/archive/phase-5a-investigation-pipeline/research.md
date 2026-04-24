# Research: Phase 5A Investigation Pipeline

## Текущий код — точки интеграции

### 1. Главный оркестратор: `AlertProcessor`

**Файл**: `go-app/internal/core/services/alert_processor.go`

Цепочка в `ProcessAlert()`:
```
Step 0:   Deduplication (graceful degradation при ошибке)
Step 0.5: InhibitionCache update (add/remove firing alerts)
Step 1:   InhibitionCheck (skip publish если заматчилось)
Step 2:   Enrichment mode router:
           enriched → LLMClient.ClassifyAlert() → FilterEngine.ShouldBlock() → PublishWithClassification()
           transparent → PublishToAll()
           transparent_with_recommendations → PublishToAll() без фильтрации
```

**Точка интеграции Phase 5A**: после `LLMClient.ClassifyAlert()` и до `FilterEngine.ShouldBlock()`.
Если классификация успешна и alert.Status == firing → submit investigation job.
Это fire-and-forget: ошибка submit не должна блокировать публикацию.

```go
// Место вставки в ProcessAlert (enriched mode):
classification, err := p.llmClient.ClassifyAlert(ctx, alert)
// ... error handling ...

// ← ЗДЕСЬ: p.investigationQueue.Submit(alert, classification)

shouldBlock, reason := p.filterEngine.ShouldBlock(alert, classification)
```

### 2. Существующий паттерн очереди: `PublishingQueue`

**Файл**: `go-app/internal/infrastructure/publishing/queue.go`

Архитектура которую нужно воспроизвести:
```go
type PublishingQueue struct {
    highPriorityQueue   chan *PublishingJob
    mediumPriorityQueue chan *PublishingJob
    lowPriorityQueue    chan *PublishingJob
    workers             int
    jobTracker          *JobTrackingStore
    dlqRepo             DLQRepository
}
```

Ключевые файлы:
- `queue.go` — основная структура и Submit()
- `queue_priority.go` — Priority enum, determinePriority()
- `queue_dlq.go` — DLQRepository и move-to-DLQ logic
- `queue_job_tracking.go` — LRU in-memory job store
- `queue_error_classification.go` — transient vs permanent errors
- `queue_retry.go` — exponential backoff retry logic

Параметры по умолчанию:
- WorkerCount: 10
- HighPriorityQueueSize: 500 / MediumPriorityQueueSize: 1000 / LowPriorityQueueSize: 500
- MaxRetries: 3, RetryInterval: 2s

**Решение**: `InvestigationQueue` будет иметь аналогичную структуру но проще:
- Одна очередь (нет нужды в 3-tier priority для расследований)
- Меньший worker pool (3-5 воркеров — LLM-вызовы медленнее, но их меньше)
- Тот же error classification: transient retry, permanent → DLQ

### 3. LLM Client: `HTTPLLMClient`

**Файл**: `go-app/internal/infrastructure/llm/client.go`

Интерфейс:
```go
type LLMClient interface {
    ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
    Health(ctx context.Context) error
}
```

Конфигурация (`go-app/internal/config/config.go`):
```go
type LLMConfig struct {
    Enabled     bool
    Provider    string        // "proxy" | "openai" | "openai-compatible"
    APIKey      string
    BaseURL     string
    Model       string        // default: "gpt-4o"
    MaxTokens   int
    Temperature float64
    Timeout     time.Duration
    MaxRetries  int
}
```

Клиент поддерживает:
- `proxy` mode: POST `/classify`
- `openai-compatible` mode: POST `/chat/completions`
- Circuit breaker (3 состояния: Closed/Open/HalfOpen)
- Retry с exponential backoff
- Dry-run mock для тестов

**Решение**: `InvestigationService` использует тот же `LLMClient` (уже инжектирован в `AlertProcessor`).
Не нужен отдельный HTTP-клиент — добавляем метод `InvestigateAlert()` в интерфейс или создаём
отдельный `InvestigationLLMClient` с методом `InvestigateAlert()`.

### 4. База данных

**Файлы**: `go-app/migrations/`, `go-app/internal/infrastructure/repository/`

Существующие таблицы (из `20250911094416_initial_schema.sql`):
- `alerts` — fingerprint, status, labels, annotations
- `alert_classifications` — severity, confidence, reasoning, recommendations, llm_model
- `alert_publishing_history` — target, status, attempt_number
- `publishing_dlq` — job_id, fingerprint, error_message, retry_count, replayed

Паттерн repository:
```go
type AlertRepository interface {
    SaveAlert(ctx, alert) error
    GetAlertByFingerprint(ctx, fingerprint) (*Alert, error)
    // ...
}
```

**Решение**: Новая миграция `_create_investigation_table.sql` + `InvestigationRepository` интерфейс.

### 5. Config / Application wiring

**Файл**: `go-app/internal/application/` (предположительно сборка приложения)
**Файл**: `go-app/internal/config/config.go`

`AlertProcessorConfig` принимает зависимости через конструктор:
```go
type AlertProcessorConfig struct {
    LLMClient         LLMClient
    FilterEngine      FilterEngine
    Publisher         Publisher
    Deduplication     DeduplicationService
    InhibitionMatcher inhibition.InhibitionMatcher
    // ...
}
```

**Решение**: Добавить `InvestigationQueue InvestigationQueue` в `AlertProcessorConfig`.
Добавить `InvestigationConfig` секцию в `Config`.

### 6. Metrics

**Файл**: `go-app/pkg/metrics/`

Паттерн: `metrics.MetricsManager` — Prometheus registry.
`BusinessMetrics` — доменные метрики.

**Решение**: Добавить `InvestigationMetrics` по образцу существующих.

### 7. HTTP handlers

**Файл**: `go-app/cmd/server/handlers/`

Существующие: `webhook.go` (POST /api/v2/alerts), UI handlers.

**Решение**: Добавить `investigation.go` handler: `GET /api/v1/alerts/{fingerprint}/investigation`.

## Зависимости (go.mod)

Из `go-app/go.mod` (уже есть):
- `github.com/prometheus/client_golang` — метрики
- `github.com/google/uuid` (предположительно) — UUID для job ID
- `net/http` — LLM HTTP client

Новых зависимостей не нужно.

## Паттерны из reference implementations

**SherlockOps** (Go): двухфазный подход с tool execution loop.
**HolmesGPT** (Python): agentic investigation с structured output.
**Keep**: alert correlation + investigation workflow.

Phase 5A берёт только инфраструктурный паттерн: async queue + worker + LLM call.
Tool execution (Phase 6A) будет добавлен поверх этой инфраструктуры.
