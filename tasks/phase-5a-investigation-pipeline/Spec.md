# Spec: PHASE-5A — Двухфазный Async Investigation Pipeline

## Архитектурное решение

**Паттерн**: Fire-and-forget queue (клон `PublishingQueue`, упрощённый).
**Место вставки**: `AlertProcessor.ProcessAlert()`, после `LLMClient.ClassifyAlert()`, до `FilterEngine.ShouldBlock()`.
**Принцип**: ошибка submit investigation НЕ блокирует и не возвращает ошибку в Phase 1.

```
ProcessAlert(ctx, alert)
  → dedup → inhibition → ClassifyAlert()
                              ↓
                  investigationQueue.Submit(alert, classification)  // fire-and-forget
                              ↓ (async, отдельные горутины)
                  InvestigationWorker.Investigate(alert, classification)
                    → LLM("расследуй алерт")
                    → InvestigationRepository.Save(findings)
                              ↓ (возврат в sync-путь)
  → filterEngine.ShouldBlock() → PublishWithClassification()
```

---

## 1. База данных — Миграция

**Файл**: `go-app/migrations/20260422000000_create_investigation_table.sql`

```sql
-- +goose Up

CREATE TABLE IF NOT EXISTS alert_investigations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint   VARCHAR(64)  NOT NULL,
    classification_id BIGINT,  -- FK alert_classifications.id (nullable, если classification отсутствовала)

    -- Статус жизненного цикла
    status        VARCHAR(20)  NOT NULL DEFAULT 'queued',
    -- queued | processing | completed | failed | dlq
    CONSTRAINT chk_inv_status CHECK (status IN ('queued','processing','completed','failed','dlq')),

    -- LLM-результат расследования
    summary       TEXT,                    -- короткое summary (1-2 предложения)
    findings      JSONB,                   -- структурированные выводы
    recommendations JSONB,                 -- шаги по устранению
    confidence    DECIMAL(4,3),            -- 0.000–1.000

    -- Мета
    llm_model     VARCHAR(100),
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    processing_time DECIMAL(8,3),          -- секунды

    -- Retry tracking
    retry_count   INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    error_type    VARCHAR(20),             -- transient | permanent | unknown

    -- Timestamps
    queued_at     TIMESTAMP NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMP,
    completed_at  TIMESTAMP,
    created_at    TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inv_fingerprint ON alert_investigations(fingerprint);
CREATE INDEX IF NOT EXISTS idx_inv_status      ON alert_investigations(status);
CREATE INDEX IF NOT EXISTS idx_inv_queued_at   ON alert_investigations(queued_at DESC);
CREATE INDEX IF NOT EXISTS idx_inv_classification ON alert_investigations(classification_id);

-- +goose Down
DROP TABLE IF EXISTS alert_investigations;
```

---

## 2. Core domain — новые типы

**Пакет**: `go-app/internal/core/interfaces.go` (дополнение)

```go
// InvestigationStatus — жизненный цикл расследования
type InvestigationStatus string

const (
    InvestigationQueued     InvestigationStatus = "queued"
    InvestigationProcessing InvestigationStatus = "processing"
    InvestigationCompleted  InvestigationStatus = "completed"
    InvestigationFailed     InvestigationStatus = "failed"
    InvestigationDLQ        InvestigationStatus = "dlq"
)

// InvestigationJob — задание для investigation worker
type InvestigationJob struct {
    ID               string
    Alert            *Alert
    Classification   *ClassificationResult // может быть nil
    Status           InvestigationStatus
    RetryCount       int
    SubmittedAt      time.Time
    StartedAt        *time.Time
    CompletedAt      *time.Time
    LastError        string
    ErrorType        string // transient | permanent | unknown
}

// InvestigationResult — результат LLM-расследования
type InvestigationResult struct {
    Summary          string         `json:"summary"`
    Findings         map[string]any `json:"findings"`
    Recommendations  []string       `json:"recommendations"`
    Confidence       float64        `json:"confidence"`
    LLMModel         string         `json:"llm_model"`
    PromptTokens     int            `json:"prompt_tokens"`
    CompletionTokens int            `json:"completion_tokens"`
    ProcessingTime   float64        `json:"processing_time"`
}

// Investigation — запись в БД
type Investigation struct {
    ID               string
    Fingerprint      string
    ClassificationID *int64
    Status           InvestigationStatus
    Result           *InvestigationResult  // nil пока не completed
    RetryCount       int
    ErrorMessage     string
    ErrorType        string
    QueuedAt         time.Time
    StartedAt        *time.Time
    CompletedAt      *time.Time
    CreatedAt        time.Time
    UpdatedAt        time.Time
}
```

---

## 3. Repository interface

**Пакет**: `go-app/internal/core/` (новый файл `investigation_repository.go`)

```go
type InvestigationRepository interface {
    Create(ctx context.Context, job *InvestigationJob) error
    UpdateStatus(ctx context.Context, id string, status InvestigationStatus,
        startedAt *time.Time, completedAt *time.Time) error
    SaveResult(ctx context.Context, id string, result *InvestigationResult) error
    SaveError(ctx context.Context, id string, errMsg, errType string) error
    GetByFingerprint(ctx context.Context, fingerprint string) (*Investigation, error)
    GetLatestByFingerprint(ctx context.Context, fingerprint string) (*Investigation, error)
    MoveToDLQ(ctx context.Context, id string) error
}
```

**Реализация**: `go-app/internal/infrastructure/repository/investigation_repository.go`
(по образцу существующих репозиториев PostgreSQL в том же пакете)

---

## 4. LLM Interface — расширение

**Файл**: `go-app/internal/core/services/alert_processor.go` (существующий интерфейс `LLMClient`)

Добавить метод в интерфейс:

```go
type LLMClient interface {
    ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
    InvestigateAlert(ctx context.Context, alert *core.Alert,
        classification *core.ClassificationResult) (*core.InvestigationResult, error)
    Health(ctx context.Context) error
}
```

**Реализация** в `go-app/internal/infrastructure/llm/client.go`:
```go
func (c *HTTPLLMClient) InvestigateAlert(ctx context.Context,
    alert *core.Alert, classification *core.ClassificationResult) (*core.InvestigationResult, error)
```

Промпт системы (строго структурированный вывод):
```
You are an SRE investigating an alert. Analyze the alert and provide:
1. A brief 1-2 sentence summary of what likely happened
2. Structured findings (root_cause, affected_components, severity_rationale)
3. Specific recommendations for remediation (ordered by priority)
4. Confidence score (0.0–1.0) in your analysis

Alert: <alert name, labels, annotations>
Classification: <severity, reasoning>

Respond in JSON matching schema: {summary, findings, recommendations, confidence}
```

---

## 5. InvestigationQueue

**Пакет**: `go-app/internal/infrastructure/investigation/` (новый пакет)
**Файл**: `queue.go`

```go
type QueueConfig struct {
    WorkerCount   int           // default: 3
    QueueSize     int           // default: 200
    MaxRetries    int           // default: 3
    RetryInterval time.Duration // default: 5s
}

type InvestigationQueue struct {
    jobs       chan *core.InvestigationJob
    config     QueueConfig
    llmClient  services.LLMClient
    repo       core.InvestigationRepository
    metrics    *InvestigationMetrics
    logger     *slog.Logger
    wg         sync.WaitGroup
    stopped    atomic.Bool
}

func NewInvestigationQueue(cfg QueueConfig, llmClient services.LLMClient,
    repo core.InvestigationRepository, logger *slog.Logger) *InvestigationQueue

// Submit — fire-and-forget, НЕ блокирует при полной очереди (drop с метрикой)
func (q *InvestigationQueue) Submit(alert *core.Alert, classification *core.ClassificationResult) error

// Start — запускает N worker goroutines
func (q *InvestigationQueue) Start(ctx context.Context)

// Stop — graceful shutdown (drain queue или timeout)
func (q *InvestigationQueue) Stop()
```

**Worker loop** (внутри):
```go
func (q *InvestigationQueue) runWorker(ctx context.Context) {
    for {
        select {
        case job := <-q.jobs:
            q.processJob(ctx, job)
        case <-ctx.Done():
            return
        }
    }
}

func (q *InvestigationQueue) processJob(ctx context.Context, job *core.InvestigationJob) {
    q.repo.UpdateStatus(ctx, job.ID, core.InvestigationProcessing, ptr(time.Now()), nil)
    
    result, err := q.llmClient.InvestigateAlert(ctx, job.Alert, job.Classification)
    if err != nil {
        errType := classifyError(err) // transient | permanent | unknown
        if errType == "transient" && job.RetryCount < q.config.MaxRetries {
            job.RetryCount++
            time.AfterFunc(backoff(job.RetryCount, q.config.RetryInterval), func() {
                q.jobs <- job
            })
            q.repo.UpdateStatus(ctx, job.ID, core.InvestigationQueued, nil, nil)
        } else {
            q.repo.SaveError(ctx, job.ID, err.Error(), errType)
            q.repo.UpdateStatus(ctx, job.ID, core.InvestigationFailed, nil, ptr(time.Now()))
            if job.RetryCount >= q.config.MaxRetries {
                q.repo.MoveToDLQ(ctx, job.ID)
            }
        }
        return
    }
    
    q.repo.SaveResult(ctx, job.ID, result)
    q.repo.UpdateStatus(ctx, job.ID, core.InvestigationCompleted, nil, ptr(time.Now()))
}
```

---

## 6. Интеграция в AlertProcessor

**Файл**: `go-app/internal/core/services/alert_processor.go`

Добавить в `AlertProcessorConfig`:
```go
type AlertProcessorConfig struct {
    // ... существующие поля ...
    InvestigationQueue InvestigationQueue // optional, nil = investigation disabled
}
```

Новый интерфейс (в том же файле или отдельно):
```go
type InvestigationQueue interface {
    Submit(alert *core.Alert, classification *core.ClassificationResult) error
}
```

В `AlertProcessor.processEnrichedMode()` (или inline в ProcessAlert):
```go
classification, err := p.llmClient.ClassifyAlert(ctx, alert)
if err == nil && p.investigationQueue != nil && alert.Status == core.StatusFiring {
    if submitErr := p.investigationQueue.Submit(alert, classification); submitErr != nil {
        p.logger.Warn("Failed to submit investigation job",
            "error", submitErr,
            "fingerprint", alert.Fingerprint)
        // не возвращаем ошибку — investigation optional
    }
}
```

---

## 7. HTTP API

**Маршрут**: `GET /api/v1/alerts/{fingerprint}/investigation`

**Response (200)**:
```json
{
  "fingerprint": "abc123",
  "status": "completed",
  "summary": "Redis OOM caused by missing memory limits in staging namespace.",
  "findings": {
    "root_cause": "No memory limits set on redis pod",
    "affected_components": ["redis", "cache-service"],
    "severity_rationale": "Full outage of caching layer"
  },
  "recommendations": [
    "Set memory limits: resources.limits.memory: 512Mi",
    "Add OOM alerting rule with lower threshold"
  ],
  "confidence": 0.82,
  "llm_model": "gpt-4o",
  "queued_at": "2026-04-22T10:00:00Z",
  "completed_at": "2026-04-22T10:00:08Z"
}
```

**Response (404)**: investigation не найдено (алерт не проходил через Phase 2).
**Response (200, status=queued/processing)**: partial response без findings.

**Файл**: `go-app/internal/application/handlers/investigation_handler.go`

---

## 8. Config

**Файл**: `go-app/internal/config/config.go` — добавить:

```go
type InvestigationConfig struct {
    Enabled       bool          `yaml:"enabled" default:"false"`
    WorkerCount   int           `yaml:"worker_count" default:"3"`
    QueueSize     int           `yaml:"queue_size" default:"200"`
    MaxRetries    int           `yaml:"max_retries" default:"3"`
    RetryInterval time.Duration `yaml:"retry_interval" default:"5s"`
    OnlyFiring    bool          `yaml:"only_firing" default:"true"` // не расследовать resolved
}
```

---

## 9. Метрики Prometheus

```go
// go-app/internal/infrastructure/investigation/metrics.go
type InvestigationMetrics struct {
    QueueDepth       prometheus.Gauge        // amp_investigation_queue_depth
    Submitted        prometheus.Counter      // amp_investigations_submitted_total
    Completed        prometheus.CounterVec   // amp_investigations_total{status=completed|failed|dlq}
    Dropped          prometheus.Counter      // amp_investigations_dropped_total (queue full)
    Duration         prometheus.Histogram    // amp_investigation_duration_seconds
}
```

---

## 10. Wiring в ServiceRegistry

**Файл**: `go-app/internal/application/service_registry.go`

В `initializeAlertProcessor()`:
```go
var invQueue services.InvestigationQueue
if r.config.Investigation.Enabled {
    invRepo := repository.NewInvestigationRepository(r.db)
    invQueue = investigation.NewInvestigationQueue(
        investigation.QueueConfig{
            WorkerCount:   r.config.Investigation.WorkerCount,
            QueueSize:     r.config.Investigation.QueueSize,
            MaxRetries:    r.config.Investigation.MaxRetries,
            RetryInterval: r.config.Investigation.RetryInterval,
        },
        llmClient,
        invRepo,
        r.logger,
    )
    invQueue.Start(ctx)
}

config := services.AlertProcessorConfig{
    // ... существующие ...
    InvestigationQueue: invQueue,
}
```

---

## Решения

| Вопрос | Решение | Почему |
|--------|---------|--------|
| Одна очередь или 3-tier priority | Одна | Расследований мало, приоритизация избыточна |
| Когда submit investigation | После ClassifyAlert(), до Filter | Classification нужна для промпта |
| Submit при resolved-алертах | Нет (OnlyFiring=true) | Расследовать resolved бессмысленно |
| Новый HTTP-клиент для investigation | Нет, переиспользуем LLMClient | Добавляем метод в существующий интерфейс |
| Хранить investigation в том же LRU что jobs | Нет, только PostgreSQL | Findings нужны persistent, не ephemeral |
| Graceful shutdown | drain channel + context cancel | Повторяем паттерн PublishingQueue |
