# Spec: PHASE-5B — Agentic Investigation Loop

## Архитектурное решение

**Паттерн**: OpenAI function calling loop (не ReAct).
**Расположение**: новый пакет `go-app/internal/core/investigation/` — domain-уровень, без инфраструктурных зависимостей.
**Интеграция**: `InvestigationQueue.processJob()` (Phase 5A) — добавить опциональный `AgentLoop`; если nil → старый `InvestigateAlert()`.
**Конфигурация**: `config.AgentConfig` — включает/выключает agentic mode.

```
Phase 5A worker ──┐
                  ↓
         if agentLoop != nil:
           AgentLoop.Run(alert, classification, tools)
             ├── LLM(messages, tools) → ToolCallResponse
             │     ↓
             │   ToolRegistry.Execute(name, params) → ToolResult
             │     ↓ append to messages
             │   loop (max MaxIterations)
             └── LLM(messages, tools) → FinalAnswer → InvestigationResult
         else:
           LLMClient.InvestigateAlert(alert, classification) → InvestigationResult
```

---

## 1. Миграция БД

**Файл**: `go-app/migrations/20260423000000_investigation_agent_steps.sql`

```sql
-- +goose Up
ALTER TABLE alert_investigations
    ADD COLUMN IF NOT EXISTS steps            JSONB,
    ADD COLUMN IF NOT EXISTS iterations_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_calls_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_inv_steps ON alert_investigations USING GIN (steps);

-- +goose Down
ALTER TABLE alert_investigations
    DROP COLUMN IF EXISTS steps,
    DROP COLUMN IF EXISTS iterations_count,
    DROP COLUMN IF EXISTS tool_calls_count;
```

**Применяется поверх** Phase 5A миграции `20260422000000_create_investigation_table.sql`.

---

## 2. Core типы — пакет `go-app/internal/core/investigation/`

**Новый пакет**, не зависит от infrastructure. Только interfaces + domain structs.

### 2.1 Tool Interface

**Файл**: `go-app/internal/core/investigation/tool.go`

```go
package investigation

import "context"

// ToolDefinition — описание инструмента для передачи в LLM (OpenAI function spec).
type ToolDefinition struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  JSONSchemaObject `json:"parameters"`
}

// JSONSchemaObject — параметры инструмента в JSON Schema формате.
type JSONSchemaObject struct {
    Type       string                     `json:"type"` // "object"
    Properties map[string]JSONSchemaField `json:"properties"`
    Required   []string                   `json:"required,omitempty"`
}

// JSONSchemaField — одно поле JSON Schema.
type JSONSchemaField struct {
    Type        string `json:"type"`
    Description string `json:"description,omitempty"`
    Default     any    `json:"default,omitempty"`
}

// ToolResult — результат выполнения инструмента.
type ToolResult struct {
    ToolName string
    CallID   string
    Content  string // JSON или plain text
    IsError  bool
    Error    string
}

// Tool — интерфейс инструмента.
type Tool interface {
    // Definition возвращает описание для LLM.
    Definition() ToolDefinition
    // Execute выполняет инструмент с переданными параметрами.
    Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}
```

### 2.2 ToolRegistry

**Файл**: `go-app/internal/core/investigation/registry.go`

```go
package investigation

import (
    "context"
    "fmt"
)

// ToolRegistry — регистрирует и выполняет инструменты по имени.
type ToolRegistry struct {
    tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
    return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register добавляет инструмент. Паникует при дубликате имени.
func (r *ToolRegistry) Register(tool Tool) {
    name := tool.Definition().Name
    if _, exists := r.tools[name]; exists {
        panic(fmt.Sprintf("investigation: duplicate tool name %q", name))
    }
    r.tools[name] = tool
}

// Execute выполняет инструмент по имени.
// Возвращает ToolResult{IsError: true} если инструмент не найден или вернул ошибку.
func (r *ToolRegistry) Execute(ctx context.Context, name string, params map[string]any, callID string) ToolResult {
    tool, ok := r.tools[name]
    if !ok {
        return ToolResult{ToolName: name, CallID: callID, IsError: true,
            Error: fmt.Sprintf("unknown tool: %q", name)}
    }
    result, err := tool.Execute(ctx, params)
    result.ToolName = name
    result.CallID = callID
    if err != nil {
        result.IsError = true
        result.Error = err.Error()
    }
    return result
}

// Definitions возвращает все зарегистрированные ToolDefinition для передачи в LLM.
func (r *ToolRegistry) Definitions() []ToolDefinition {
    defs := make([]ToolDefinition, 0, len(r.tools))
    for _, t := range r.tools {
        defs = append(defs, t.Definition())
    }
    return defs
}
```

### 2.3 AgentMessage — история диалога

**Файл**: `go-app/internal/core/investigation/message.go`

```go
package investigation

// MessageRole — роль участника диалога.
type MessageRole string

const (
    RoleSystem    MessageRole = "system"
    RoleUser      MessageRole = "user"
    RoleAssistant MessageRole = "assistant"
    RoleTool      MessageRole = "tool"
)

// ToolCallRequest — запрос LLM на вызов инструмента.
type ToolCallRequest struct {
    ID        string         // уникальный id вызова (из LLM ответа)
    ToolName  string
    Arguments map[string]any
    RawArgs   string         // оригинальная JSON строка из LLM
}

// AgentMessage — одно сообщение в истории диалога.
type AgentMessage struct {
    Role       MessageRole
    Content    string            // текстовое содержимое (для system/user/tool)
    ToolCallID string            // для role=tool: id вызова которому отвечаем
    ToolCalls  []ToolCallRequest // для role=assistant: запрошенные вызовы
}
```

### 2.4 AgentResponse — ответ LLM

**Файл**: `go-app/internal/core/investigation/response.go`

```go
package investigation

// AgentResponseKind — тип ответа от LLM.
type AgentResponseKind string

const (
    ResponseToolCalls   AgentResponseKind = "tool_calls"   // LLM хочет вызвать инструменты
    ResponseFinalAnswer AgentResponseKind = "final_answer" // LLM завершил расследование
)

// AgentResponse — ответ от одного LLM вызова.
type AgentResponse struct {
    Kind         AgentResponseKind
    ToolCalls    []ToolCallRequest // если Kind == ResponseToolCalls
    FinalContent string            // если Kind == ResponseFinalAnswer (JSON)
    PromptTokens     int
    CompletionTokens int
    LLMModel         string
}
```

### 2.5 InvestigationStep — трассировка

**Файл**: `go-app/internal/core/investigation/trace.go`

```go
package investigation

import "time"

// StepType — тип шага в трассировке расследования.
type StepType string

const (
    StepToolCall    StepType = "tool_call"    // запрос инструмента
    StepObservation StepType = "observation"  // результат инструмента
    StepConclusion  StepType = "conclusion"   // финальный ответ LLM
    StepError       StepType = "error"        // ошибка шага
)

// InvestigationStep — один шаг в агентном цикле.
type InvestigationStep struct {
    StepType   StepType       `json:"step_type"`
    Iteration  int            `json:"iteration"`
    ToolName   string         `json:"tool_name,omitempty"`
    ToolCallID string         `json:"tool_call_id,omitempty"`
    Params     map[string]any `json:"params,omitempty"`
    Content    string         `json:"content"` // tool result, conclusion text, error msg
    IsError    bool           `json:"is_error,omitempty"`
    Timestamp  time.Time      `json:"timestamp"`
    DurationMs int64          `json:"duration_ms,omitempty"`
}
```

### 2.6 AgentLoop

**Файл**: `go-app/internal/core/investigation/agent_loop.go`

```go
package investigation

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/amp/go-app/internal/core"
)

// AgentLLMClient — расширение LLMClient для tool calling.
// Реализуется в go-app/internal/infrastructure/llm/client.go.
type AgentLLMClient interface {
    InvestigateWithTools(
        ctx context.Context,
        alert *core.Alert,
        classification *core.ClassificationResult,
        tools []ToolDefinition,
        history []AgentMessage,
    ) (*AgentResponse, error)
}

// AgentLoopConfig — параметры цикла.
type AgentLoopConfig struct {
    MaxIterations    int           // default: 10 — лимит итераций (защита от infinite loop)
    TotalTimeout     time.Duration // default: 5m — бюджет на всё расследование
    PerToolTimeout   time.Duration // default: 30s — таймаут одного tool вызова
    MaxHistoryMsgs   int           // default: 40 — trim истории при переполнении
}

// AgentLoop — агентный цикл расследования.
type AgentLoop struct {
    llmClient AgentLLMClient
    registry  *ToolRegistry
    config    AgentLoopConfig
    logger    *slog.Logger
}

func NewAgentLoop(
    llmClient AgentLLMClient,
    registry *ToolRegistry,
    config AgentLoopConfig,
    logger *slog.Logger,
) *AgentLoop {
    return &AgentLoop{
        llmClient: llmClient,
        registry:  registry,
        config:    config,
        logger:    logger,
    }
}

// AgentRunResult — финальный результат агентного цикла.
type AgentRunResult struct {
    Result          *core.InvestigationResult
    Steps           []InvestigationStep
    IterationsUsed  int
    ToolCallsCount  int
    TerminationKind string // "final_answer" | "max_iterations" | "timeout" | "error"
}

// Run — запускает агентный цикл расследования.
// Возвращает InvestigationResult с полным trace.
func (a *AgentLoop) Run(
    ctx context.Context,
    alert *core.Alert,
    classification *core.ClassificationResult,
) (*AgentRunResult, error) {
    deadline := time.Now().Add(a.config.TotalTimeout)
    ctx, cancel := context.WithDeadline(ctx, deadline)
    defer cancel()

    var (
        history        []AgentMessage
        steps          []InvestigationStep
        iteration      int
        totalToolCalls int
        totalPrompt    int
        totalCompletion int
        lastModel      string
    )

    tools := a.registry.Definitions()

    for iteration = 0; iteration < a.config.MaxIterations; iteration++ {
        resp, err := a.llmClient.InvestigateWithTools(ctx, alert, classification, tools, history)
        if err != nil {
            steps = append(steps, InvestigationStep{
                StepType:  StepError,
                Iteration: iteration,
                Content:   err.Error(),
                IsError:   true,
                Timestamp: time.Now(),
            })
            return &AgentRunResult{
                Steps:           steps,
                IterationsUsed:  iteration + 1,
                ToolCallsCount:  totalToolCalls,
                TerminationKind: "error",
            }, fmt.Errorf("agent loop iteration %d: %w", iteration, err)
        }

        totalPrompt += resp.PromptTokens
        totalCompletion += resp.CompletionTokens
        lastModel = resp.LLMModel

        if resp.Kind == ResponseFinalAnswer {
            result, parseErr := parseFinalAnswer(resp.FinalContent)
            if parseErr != nil {
                result = &core.InvestigationResult{
                    Summary: resp.FinalContent,
                }
            }
            result.LLMModel = lastModel
            result.PromptTokens = totalPrompt
            result.CompletionTokens = totalCompletion

            steps = append(steps, InvestigationStep{
                StepType:  StepConclusion,
                Iteration: iteration,
                Content:   resp.FinalContent,
                Timestamp: time.Now(),
            })

            return &AgentRunResult{
                Result:          result,
                Steps:           steps,
                IterationsUsed:  iteration + 1,
                ToolCallsCount:  totalToolCalls,
                TerminationKind: "final_answer",
            }, nil
        }

        // Обработка tool calls
        assistantMsg := AgentMessage{
            Role:      RoleAssistant,
            ToolCalls: resp.ToolCalls,
        }
        history = append(history, assistantMsg)

        for _, tc := range resp.ToolCalls {
            toolCtx, toolCancel := context.WithTimeout(ctx, a.config.PerToolTimeout)
            start := time.Now()

            steps = append(steps, InvestigationStep{
                StepType:   StepToolCall,
                Iteration:  iteration,
                ToolName:   tc.ToolName,
                ToolCallID: tc.ID,
                Params:     tc.Arguments,
                Timestamp:  start,
            })

            toolResult := a.registry.Execute(toolCtx, tc.ToolName, tc.Arguments, tc.ID)
            toolCancel()
            totalToolCalls++

            steps = append(steps, InvestigationStep{
                StepType:   StepObservation,
                Iteration:  iteration,
                ToolName:   tc.ToolName,
                ToolCallID: tc.ID,
                Content:    toolResult.Content,
                IsError:    toolResult.IsError,
                Timestamp:  time.Now(),
                DurationMs: time.Since(start).Milliseconds(),
            })

            content := toolResult.Content
            if toolResult.IsError {
                content = fmt.Sprintf("ERROR: %s", toolResult.Error)
            }
            history = append(history, AgentMessage{
                Role:       RoleTool,
                Content:    content,
                ToolCallID: tc.ID,
            })
        }

        history = trimHistory(history, a.config.MaxHistoryMsgs)
    }

    // Max iterations достигнут
    return &AgentRunResult{
        Steps:           steps,
        IterationsUsed:  a.config.MaxIterations,
        ToolCallsCount:  totalToolCalls,
        TerminationKind: "max_iterations",
    }, nil
}

func parseFinalAnswer(content string) (*core.InvestigationResult, error) {
    var result core.InvestigationResult
    if err := json.Unmarshal([]byte(content), &result); err != nil {
        return nil, err
    }
    return &result, nil
}

// trimHistory удаляет старые сообщения, оставляя первое (system) и последние N.
func trimHistory(history []AgentMessage, maxMsgs int) []AgentMessage {
    if len(history) <= maxMsgs {
        return history
    }
    // Сохранить system message если есть
    if len(history) > 0 && history[0].Role == RoleSystem {
        tail := history[len(history)-maxMsgs+1:]
        return append([]AgentMessage{history[0]}, tail...)
    }
    return history[len(history)-maxMsgs:]
}
```

---

## 3. LLM Client — расширение

**Файл**: `go-app/internal/infrastructure/llm/client.go`

Добавить метод в интерфейс `LLMClient`:
```go
type LLMClient interface {
    ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
    InvestigateAlert(ctx context.Context, alert *core.Alert,
        classification *core.ClassificationResult) (*core.InvestigationResult, error)
    InvestigateWithTools(
        ctx context.Context,
        alert *core.Alert,
        classification *core.ClassificationResult,
        tools []investigation.ToolDefinition,
        history []investigation.AgentMessage,
    ) (*investigation.AgentResponse, error)
    Health(ctx context.Context) error
}
```

**Реализация** `InvestigateWithTools()` в `HTTPLLMClient`:
- Сбилдить `messages` из history + системный промпт (если history пустая)
- Сконвертировать `[]ToolDefinition` в OpenAI `tools` формат
- POST `/chat/completions` с полем `tool_choice: "auto"`
- Распарсить ответ: если `finish_reason == "tool_calls"` → `ResponseToolCalls`; если `finish_reason == "stop"` → `ResponseFinalAnswer`
- Использовать тот же circuit breaker что в `ClassifyAlert`

**Системный промпт для agent mode**:
```
You are an expert SRE investigating an alert. You have access to tools to gather
real data about the system. Use tools to gather evidence before concluding.
When you have enough evidence, provide a final JSON response with:
{"summary": "...", "findings": {...}, "recommendations": [...], "confidence": 0.0-1.0}
Only provide the final JSON when investigation is complete.
```

---

## 4. InvestigationRepository — расширение

**Файл**: `go-app/internal/infrastructure/repository/investigation_repository.go`

Добавить метод в интерфейс `core.InvestigationRepository`:
```go
type InvestigationRepository interface {
    // ... Phase 5A методы ...
    SaveSteps(ctx context.Context, id string,
        steps []investigation.InvestigationStep,
        iterationsCount, toolCallsCount int) error
}
```

Реализация: UPDATE alert_investigations SET steps=$2, iterations_count=$3, tool_calls_count=$4 WHERE id=$1.

---

## 5. InvestigationQueue — расширение (Phase 5A worker)

**Файл**: `go-app/internal/infrastructure/investigation/queue.go`

Добавить поле в `InvestigationQueue`:
```go
type InvestigationQueue struct {
    // ... Phase 5A поля ...
    agentLoop *investigation.AgentLoop // nil = Phase 5A mode (simple LLM call)
}
```

Изменить `processJob()`:
```go
func (q *InvestigationQueue) processJob(ctx context.Context, job *core.InvestigationJob) {
    q.repo.UpdateStatus(ctx, job.ID, core.InvestigationProcessing, ptr(time.Now()), nil)

    if q.agentLoop != nil {
        runResult, err := q.agentLoop.Run(ctx, job.Alert, job.Classification)
        // сохранить steps независимо от ошибки
        if len(runResult.Steps) > 0 {
            _ = q.repo.SaveSteps(ctx, job.ID, runResult.Steps,
                runResult.IterationsUsed, runResult.ToolCallsCount)
        }
        if err != nil || runResult.Result == nil {
            // ... обработка ошибки как в Phase 5A ...
            return
        }
        q.repo.SaveResult(ctx, job.ID, runResult.Result)
        q.repo.UpdateStatus(ctx, job.ID, core.InvestigationCompleted, nil, ptr(time.Now()))
        return
    }

    // Phase 5A fallback: simple LLM call
    result, err := q.llmClient.InvestigateAlert(ctx, job.Alert, job.Classification)
    // ... как в Phase 5A ...
}
```

---

## 6. Config — расширение

**Файл**: `go-app/internal/config/config.go`

Добавить в `InvestigationConfig` (Phase 5A):
```go
type InvestigationConfig struct {
    // ... Phase 5A поля (Enabled, WorkerCount, QueueSize, ...) ...

    // Agent mode (Phase 5B)
    AgentEnabled   bool          `mapstructure:"agent_enabled" default:"false"`
    MaxIterations  int           `mapstructure:"max_iterations" default:"10"`
    TotalTimeout   time.Duration `mapstructure:"total_timeout" default:"5m"`
    PerToolTimeout time.Duration `mapstructure:"per_tool_timeout" default:"30s"`
}
```

---

## 7. Stub Tools — для тестов

**Файл**: `go-app/internal/infrastructure/investigation/tools/stub.go`

```go
package tools

// EchoTool — возвращает params обратно как JSON. Для unit-тестов.
type EchoTool struct{}

func (t EchoTool) Definition() investigation.ToolDefinition {
    return investigation.ToolDefinition{
        Name:        "echo",
        Description: "Returns the input parameters as JSON. For testing only.",
        Parameters: investigation.JSONSchemaObject{
            Type: "object",
            Properties: map[string]investigation.JSONSchemaField{
                "message": {Type: "string", Description: "Text to echo"},
            },
        },
    }
}

func (t EchoTool) Execute(ctx context.Context, params map[string]any) (investigation.ToolResult, error) {
    b, _ := json.Marshal(params)
    return investigation.ToolResult{Content: string(b)}, nil
}
```

---

## 8. HTTP API — расширение ответа

**Файл**: `go-app/internal/application/handlers/investigation_handler.go`

Расширить ответ `GET /api/v1/alerts/{fingerprint}/investigation`:
```json
{
  "fingerprint": "abc123",
  "status": "completed",
  "summary": "...",
  "findings": {...},
  "recommendations": [...],
  "confidence": 0.87,
  "iterations_count": 3,
  "tool_calls_count": 2,
  "steps": [
    {
      "step_type": "tool_call",
      "iteration": 0,
      "tool_name": "query_prometheus",
      "params": {"query": "rate(http_errors_total[5m])"},
      "timestamp": "2026-04-22T10:00:01Z"
    },
    {
      "step_type": "observation",
      "iteration": 0,
      "tool_name": "query_prometheus",
      "content": "{\"result\": [[...]],...}",
      "duration_ms": 120,
      "timestamp": "2026-04-22T10:00:01Z"
    },
    {
      "step_type": "conclusion",
      "iteration": 1,
      "content": "{\"summary\": \"...\" ...}",
      "timestamp": "2026-04-22T10:00:03Z"
    }
  ]
}
```

---

## 9. Wiring в ServiceRegistry

**Файл**: `go-app/internal/application/service_registry.go`

```go
if r.config.Investigation.AgentEnabled {
    registry := investigation.NewToolRegistry()
    // Phase 6A добавит реальные tools; пока только stub в non-prod
    if r.config.Debug {
        registry.Register(tools.EchoTool{})
    }

    agentLoop := investigation.NewAgentLoop(
        llmClient,
        registry,
        investigation.AgentLoopConfig{
            MaxIterations:  r.config.Investigation.MaxIterations,
            TotalTimeout:   r.config.Investigation.TotalTimeout,
            PerToolTimeout: r.config.Investigation.PerToolTimeout,
            MaxHistoryMsgs: 40,
        },
        r.logger,
    )
    invQueue = investigation.NewInvestigationQueue(..., agentLoop)
} else {
    invQueue = investigation.NewInvestigationQueue(..., nil)
}
```

---

## Решения

| Вопрос | Решение | Почему |
|--------|---------|--------|
| ReAct vs OpenAI tool calling | OpenAI tool calling | Нативная поддержка в openai-compatible LLM; чистый JSON; нет парсинга text |
| Отдельный AgentLLMClient или расширить LLMClient | Расширить единый LLMClient | Один клиент — одна конфигурация, circuit breaker, retry |
| Хранить steps в findings или отдельном поле | Отдельное поле `steps JSONB` | findings = structured result, steps = trace; разные назначения |
| Отдельная таблица для steps или в alert_investigations | В alert_investigations | Избегаем JOIN для простых читающих запросов |
| Stub tools в prod или только dev | Только при `Debug: true` | В prod лишние tools путают LLM и увеличивают стоимость |
| Trim history | trimHistory(history, MaxHistoryMsgs=40) | Защита от context overflow при длинных расследованиях |
| Tool timeout | Per-tool context.WithTimeout(PerToolTimeout) | Медленный tool не должен блокировать всё расследование |
| Max iterations | 10 (конфигурируемо) | 10 итераций достаточно для реальных инцидентов; защита от runaway |

---

## Файловый манифест

### Новые файлы
```
go-app/migrations/20260423000000_investigation_agent_steps.sql
go-app/internal/core/investigation/tool.go
go-app/internal/core/investigation/registry.go
go-app/internal/core/investigation/message.go
go-app/internal/core/investigation/response.go
go-app/internal/core/investigation/trace.go
go-app/internal/core/investigation/agent_loop.go
go-app/internal/core/investigation/agent_loop_test.go
go-app/internal/core/investigation/registry_test.go
go-app/internal/infrastructure/investigation/tools/stub.go
go-app/internal/infrastructure/investigation/tools/stub_test.go
```

### Изменяемые файлы
```
go-app/internal/infrastructure/llm/client.go           -- InvestigateWithTools() impl
go-app/internal/infrastructure/investigation/queue.go   -- agentLoop field + processJob()
go-app/internal/infrastructure/repository/investigation_repository.go  -- SaveSteps()
go-app/internal/config/config.go                        -- AgentEnabled, MaxIterations, etc.
go-app/internal/application/service_registry.go         -- wiring AgentLoop
go-app/internal/application/handlers/investigation_handler.go  -- steps в ответе
```
