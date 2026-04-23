# Research: PHASE-5B — Agentic Investigation Loop

## 1. Текущее состояние кода (Phase 5A не реализована)

На момент написания (2026-04-22) Phase 5A находится в WIP и ни один из целевых файлов ещё не создан:
- `go-app/internal/infrastructure/investigation/` — не существует
- `go-app/internal/core/investigation.go` — не существует
- `go-app/internal/infrastructure/repository/investigation_repository.go` — не существует

**Вывод**: Phase 5B документируется опережающе. Реализация начинается только после завершения Phase 5A.

---

## 2. Точки расширения Phase 5A для Phase 5B

### 2.1 InvestigationWorker (будет создан в Phase 5A)

По Spec.md Phase 5A, `processJob()` выглядит:
```go
func (q *InvestigationQueue) processJob(ctx context.Context, job *core.InvestigationJob) {
    q.repo.UpdateStatus(...)
    result, err := q.llmClient.InvestigateAlert(ctx, job.Alert, job.Classification)
    // save result or error
}
```

**Phase 5B меняет это на**:
```go
func (q *InvestigationQueue) processJob(ctx context.Context, job *core.InvestigationJob) {
    q.repo.UpdateStatus(...)
    var result *core.InvestigationResult
    if q.agentLoop != nil {
        result, err = q.agentLoop.Run(ctx, job.Alert, job.Classification)
    } else {
        result, err = q.llmClient.InvestigateAlert(ctx, job.Alert, job.Classification)
    }
    // save result or error
}
```

### 2.2 LLMClient интерфейс (Phase 5A добавляет InvestigateAlert)

Текущий `LLMClient` в `go-app/internal/core/services/alert_processor.go`:
```go
type LLMClient interface {
    ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
    Health(ctx context.Context) error
}
```

Phase 5A добавляет:
```go
InvestigateAlert(ctx context.Context, alert *core.Alert,
    classification *core.ClassificationResult) (*core.InvestigationResult, error)
```

Phase 5B добавляет:
```go
InvestigateWithTools(ctx context.Context,
    alert *core.Alert,
    classification *core.ClassificationResult,
    tools []ToolDefinition,
    history []AgentMessage) (*AgentResponse, error)
```

### 2.3 alert_investigations таблица (Phase 5A создаёт)

Phase 5A создаёт таблицу со схемой:
```sql
id, fingerprint, classification_id, status, summary,
findings JSONB, recommendations JSONB, confidence,
llm_model, prompt_tokens, completion_tokens,
retry_count, error_message, error_type,
queued_at, started_at, completed_at, created_at, updated_at
```

Phase 5B добавляет новую миграцию:
```sql
ALTER TABLE alert_investigations
  ADD COLUMN steps JSONB,                   -- массив InvestigationStep
  ADD COLUMN iterations_count INTEGER DEFAULT 0,
  ADD COLUMN tool_calls_count INTEGER DEFAULT 0;
```

---

## 3. OpenAI Function Calling Protocol

Существующий `HTTPLLMClient` поддерживает `openai-compatible` mode через POST `/chat/completions`.

### Запрос с tools (OpenAI Chat Completions v1):
```json
{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are an SRE..."},
    {"role": "user", "content": "<alert context>"},
    {"role": "assistant", "content": null, "tool_calls": [...]},
    {"role": "tool", "tool_call_id": "xxx", "content": "<result>"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "query_prometheus",
        "description": "Run a PromQL query...",
        "parameters": {
          "type": "object",
          "properties": {
            "query": {"type": "string", "description": "PromQL expression"},
            "range_minutes": {"type": "integer", "default": 60}
          },
          "required": ["query"]
        }
      }
    }
  ],
  "tool_choice": "auto"
}
```

### Ответ — tool call:
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "tool_calls": [{
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "query_prometheus",
          "arguments": "{\"query\": \"rate(http_errors_total[5m])\"}"
        }
      }]
    },
    "finish_reason": "tool_calls"
  }]
}
```

### Ответ — final answer:
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "{\"summary\": \"...\", \"findings\": {...}, ...}"
    },
    "finish_reason": "stop"
  }]
}
```

**Вывод**: `InvestigateWithTools()` отправляет один HTTP запрос с tools; LLM возвращает либо tool_calls (нужна ещё одна итерация), либо content (финальный ответ).

---

## 4. Паттерн AgentLoop — ReAct vs Tool Calling

### ReAct (Yao et al. 2023)
Explicit Thought → Action → Observation в текстовом формате. Сложнее парсить.

### OpenAI Tool Calling (выбранный подход)
- LLM сам управляет reasoning через tool calls
- Чистый структурированный вывод (JSON arguments)
- Нативная поддержка в `openai-compatible` LLM
- Конечный ответ — JSON (уже используется в ClassifyAlert)

**Решение**: OpenAI tool calling protocol. Цикл:
```
iteration 1: LLM([system, user:alert]) → tool_calls? → execute → add tool messages
iteration 2: LLM([..., assistant:tool_calls, tool:results]) → tool_calls? | final_answer
...
iteration N: LLM([...]) → final_answer → done
```

---

## 5. Reference Implementations

### SherlockOps (Go)
- Two-phase: detection + investigation
- Tool execution через Go interfaces
- Context window management (trim history при переполнении)

### HolmesGPT (Python)
- `ToolExecutor` абстракция
- Investigation trace как list of steps
- Timeout per step + total budget

### Keep
- Workflow-based: alert → enrichment → investigation → runbook

**Что берём**:
- Tool interface (SherlockOps)
- Step trace с типами (HolmesGPT)
- Max iterations + total timeout budget (HolmesGPT)

---

## 6. Существующие паттерны проекта

### PublishingQueue (образец для InvestigationQueue в Phase 5A)
```
go-app/internal/infrastructure/publishing/queue.go
```
Паттерн: channel-based queue, worker pool, graceful shutdown — уже описан в Phase 5A research.

### Config паттерн (mapstructure теги)
```go
type LLMConfig struct {
    Enabled     bool          `mapstructure:"enabled"`
    Provider    string        `mapstructure:"provider"`
    // ...
}
```
Phase 5B добавляет `AgentConfig` с тем же паттерном.

### Repository паттерн
```
go-app/internal/infrastructure/repository/postgres_history.go
```
CRUD через `pgx` или `database/sql`. Phase 5B добавляет только `ALTER TABLE` миграцию.

---

## 7. Ключевые риски и митигации

| Риск | Митигация |
|------|-----------|
| LLM бесконечный tool calling loop | `MaxIterations` (default: 10), принудительное завершение |
| Tool execution timeout | Per-tool timeout в `ToolConfig` + context deadline |
| Большой context window (много tool results) | Trim history: оставлять последние N tool messages |
| LLM не возвращает valid JSON для tool args | Fallback: raw string как params, log parse error |
| Tool вернул ошибку | Agent получает error observation, продолжает; после N ошибок → partial result |
| Phase 5A не завершена | Phase 5B реализуется строго после Phase 5A merge |
| `InvestigateAlert` и `InvestigateWithTools` дублируют код | Общий `buildInvestigationPrompt()` helper |
