# Tasks: PHASE-5B — Agentic Investigation Loop

## Предусловие

**PHASE-5A должна быть завершена и смержена в main до начала реализации.**
Эта задача расширяет `InvestigationQueue`, `InvestigationRepository` и `LLMClient` из Phase 5A.

---

## Вертикальные слайсы

Каждый слайс — рабочий, тестируемый инкремент. Строгий порядок.

---

## Слайс 1: Core domain — Tool + ToolRegistry (день 1, утро)

**Цель**: абстракции Tool и Registry готовы, покрыты тестами, без LLM зависимости.

- [ ] **1.1** Создать пакет `go-app/internal/core/investigation/`
  - `tool.go` — `ToolDefinition`, `JSONSchemaObject`, `JSONSchemaField`, `ToolResult`, `Tool` interface
  - `registry.go` — `ToolRegistry`, `Register()`, `Execute()`, `Definitions()`
  - `message.go` — `MessageRole`, `ToolCallRequest`, `AgentMessage`
  - `response.go` — `AgentResponseKind`, `AgentResponse`
  - `trace.go` — `StepType`, `InvestigationStep`

- [ ] **1.2** Stub tools в `go-app/internal/infrastructure/investigation/tools/stub.go`
  - `EchoTool` — возвращает params как JSON
  - Реализует `Tool` interface

- [ ] **1.3** Unit-тесты Registry
  - `registry_test.go`: Register → Execute happy path
  - Execute unknown tool → ToolResult{IsError: true}
  - Duplicate Register → panic
  - Definitions() возвращает все зарегистрированные tools
  - EchoTool.Execute() возвращает JSON params

- [ ] **1.4** `go vet ./internal/core/investigation/...` проходит

---

## Слайс 2: AgentMessage history types + trimHistory (день 1, утро)

**Цель**: types для истории диалога готовы; trimHistory протестирован.

- [ ] **2.1** `trimHistory()` в `agent_loop.go` (внутренняя функция)
  - Если len(history) <= maxMsgs → без изменений
  - Если history[0].Role == RoleSystem → сохранить + последние maxMsgs-1
  - Иначе → последние maxMsgs

- [ ] **2.2** Unit-тест `trimHistory`
  - history меньше лимита → без изменений
  - С system message: system + tail сохранены
  - Без system message: только tail

---

## Слайс 3: AgentLoop core (день 1, день)

**Цель**: AgentLoop.Run() работает с mock LLM и stub tools, тесты проходят.

- [ ] **3.1** `AgentLLMClient` interface в `agent_loop.go`
  - `InvestigateWithTools(ctx, alert, classification, tools, history) (*AgentResponse, error)`

- [ ] **3.2** `AgentLoopConfig` struct + defaults
  - MaxIterations: 10
  - TotalTimeout: 5m
  - PerToolTimeout: 30s
  - MaxHistoryMsgs: 40

- [ ] **3.3** `AgentLoop.Run()` реализация
  - context.WithDeadline(TotalTimeout)
  - for loop: вызвать LLM → если tool_calls → execute all → append history → continue
  - Если final_answer → `parseFinalAnswer()` → return AgentRunResult
  - Max iterations достигнут → return с TerminationKind="max_iterations"
  - LLM error → return error + partial steps
  - Per-tool: `context.WithTimeout(PerToolTimeout)`
  - Trim history каждую итерацию

- [ ] **3.4** `parseFinalAnswer()` helper
  - Unmarshal JSON → `*core.InvestigationResult`
  - При parse error: summary = raw content, остальное пустое

- [ ] **3.5** `AgentRunResult` struct
  - Result, Steps, IterationsUsed, ToolCallsCount, TerminationKind

- [ ] **3.6** Unit-тесты AgentLoop
  - Mock `AgentLLMClient` (в test файле):
    ```go
    type mockAgentLLM struct {
        responses []AgentResponse
        callCount int
    }
    ```
  - **Happy path**: LLM → tool_call → tool executes → LLM → final_answer
    → TerminationKind="final_answer", ToolCallsCount=1, len(Steps)=3
  - **Direct answer**: LLM сразу → final_answer (0 tool calls)
    → IterationsUsed=1, ToolCallsCount=0
  - **Max iterations**: LLM всегда tool_calls → TerminationKind="max_iterations"
    → IterationsUsed==MaxIterations
  - **LLM error**: первый вызов error → return error + Steps содержит StepError
  - **Tool error**: tool.Execute() → IsError=true → observation с "ERROR:", LLM продолжает
  - **Unknown tool**: registry.Execute("nonexistent") → observation error, не panic
  - **Context deadline**: timeout → context.DeadlineExceeded от LLM → return error

---

## Слайс 4: LLM InvestigateWithTools (день 1 вечер — день 2 утро)

**Цель**: HTTPLLMClient реализует AgentLLMClient; тест с mock HTTP server.

- [ ] **4.1** Расширить `LLMClient` interface в `go-app/internal/core/services/alert_processor.go`
  - Добавить `InvestigateWithTools(...)` сигнатуру (из Spec §3)
  - Обновить mock/dry-run реализации (добавить stub метод)

- [ ] **4.2** `HTTPLLMClient.InvestigateWithTools()` в `go-app/internal/infrastructure/llm/client.go`
  - Сбилдить `messages`: если history пустая → добавить system промпт (Spec §3)
  - Конвертировать `[]investigation.ToolDefinition` → OpenAI `tools` JSON
  - POST `/chat/completions` с `tool_choice: "auto"`
  - Парсить `finish_reason`:
    - `"tool_calls"` → `ResponseToolCalls`, заполнить `ToolCalls`
    - `"stop"` → `ResponseFinalAnswer`, `FinalContent = message.Content`
  - Парсить tool_call arguments: `json.Unmarshal(arguments, &map[string]any{})`
  - Использовать тот же circuit breaker что `classifyAlertOpenAI()`

- [ ] **4.3** Конвертеры (private helpers):
  - `toOpenAITools([]investigation.ToolDefinition) []openaiTool`
  - `toOpenAIMessages([]investigation.AgentMessage) []openaiMessage`
  - `fromOpenAIResponse(resp openaiResponse) (*investigation.AgentResponse, error)`

- [ ] **4.4** Unit-тест `InvestigateWithTools()`
  - Mock HTTP server: ответ с `finish_reason=tool_calls` → корректный parse
  - Mock HTTP server: ответ с `finish_reason=stop` → корректный ResponseFinalAnswer
  - Mock HTTP server: HTTP 429 → circuit breaker открывается
  - Проверить что tools передаются в тело запроса
  - Проверить что history messages → messages array в запросе

---

## Слайс 5: DB миграция + SaveSteps (день 2, утро)

**Цель**: схема БД расширена, SaveSteps работает.

- [ ] **5.1** Миграция `go-app/migrations/20260423000000_investigation_agent_steps.sql`
  - ADD COLUMN steps JSONB, iterations_count INTEGER, tool_calls_count INTEGER
  - CREATE INDEX GIN на steps
  - goose Down: DROP COLUMN
  - Проверить: `goose up` на dev БД

- [ ] **5.2** Расширить `InvestigationRepository` interface (`go-app/internal/core/`)
  - Добавить `SaveSteps(ctx, id, steps, iterationsCount, toolCallsCount) error`

- [ ] **5.3** Реализовать `SaveSteps` в postgres repository
  - `UPDATE alert_investigations SET steps=$2, iterations_count=$3, tool_calls_count=$4 WHERE id=$1`
  - steps → `json.Marshal(steps)` → передать как `[]byte`

- [ ] **5.4** Integration-тест (если есть test DB)
  - Create investigation → SaveSteps → GetLatestByFingerprint → steps в ответе

---

## Слайс 6: Интеграция в InvestigationQueue (день 2, день)

**Цель**: worker использует AgentLoop если агент включён.

- [ ] **6.1** Добавить `agentLoop *investigation.AgentLoop` в `InvestigationQueue` struct
  - Опциональное поле: nil = Phase 5A mode

- [ ] **6.2** Изменить `processJob()` (Spec §5):
  - if agentLoop != nil → `agentLoop.Run()` → SaveSteps + SaveResult/SaveError
  - else → старый `llmClient.InvestigateAlert()` (Phase 5A fallback не трогаем)

- [ ] **6.3** `NewInvestigationQueueWithAgent()` конструктор (или extend existing)
  - Принимает опциональный `*investigation.AgentLoop`

- [ ] **6.4** Unit-тест интеграции
  - Mock AgentLoop: проверить что Run() вызван при agentLoop != nil
  - Mock nil AgentLoop: fallback на InvestigateAlert()
  - AgentLoop возвращает ошибку: SaveError вызван, retry логика работает
  - AgentLoop TerminationKind="max_iterations": SaveSteps вызван, Result nil → SaveError

---

## Слайс 7: Config + Wiring (день 2, вечер)

**Цель**: agent mode включается через конфиг.

- [ ] **7.1** Расширить `InvestigationConfig` в `go-app/internal/config/config.go`
  - Добавить: `AgentEnabled bool`, `MaxIterations int`, `TotalTimeout duration`, `PerToolTimeout duration`
  - Defaults: false, 10, 5m, 30s

- [ ] **7.2** Wiring в `ServiceRegistry`
  - Если `config.Investigation.AgentEnabled`:
    - Создать `ToolRegistry`
    - Зарегистрировать `EchoTool{}` при `config.Debug`
    - Создать `AgentLoop` с конфигом из `InvestigationConfig`
    - Передать в `NewInvestigationQueue` или `NewInvestigationQueueWithAgent`
  - Если не AgentEnabled: создать queue без agent (Phase 5A)

- [ ] **7.3** Обновить config-файлы примеров
  - `examples/` или `helm/values.yaml`: добавить `investigation.agent_enabled: false` с комментарием

---

## Слайс 8: HTTP response — steps (день 3, утро)

**Цель**: API возвращает steps трассировку.

- [ ] **8.1** Расширить `Investigation` domain struct (`go-app/internal/core/`)
  - Добавить поля: `Steps []investigation.InvestigationStep`, `IterationsCount int`, `ToolCallsCount int`

- [ ] **8.2** Расширить `GetLatestByFingerprint()` в repository
  - Читать `steps`, `iterations_count`, `tool_calls_count`
  - Unmarshal `steps` JSON → `[]investigation.InvestigationStep`

- [ ] **8.3** Расширить `InvestigationHandler` ответ
  - Добавить `steps`, `iterations_count`, `tool_calls_count` в JSON response
  - Если steps nil/empty → пропустить поле (`omitempty`)

- [ ] **8.4** Unit-тест handler
  - completed investigation с steps → steps в JSON ответе
  - queued investigation без steps → steps отсутствует в ответе

---

## Слайс 9: Финальная проверка (день 3)

**Цель**: quality gate пройден.

- [ ] **9.1** `go vet ./...` — без новых ошибок
- [ ] **9.2** `go test ./...` — без новых падений
- [ ] **9.3** Все mock реализации `LLMClient` обновлены (добавлен `InvestigateWithTools` stub)
- [ ] **9.4** Integration smoke test (если есть test DB + dry-run LLM):
  - Послать алерт с `investigation.agent_enabled: true`
  - GET investigation → steps содержит хотя бы StepConclusion
- [ ] **9.5** Обновить `docs/06-planning/NEXT.md`
  - Перевести PHASE-5B в WIP / Done

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
go-app/internal/core/services/alert_processor.go           -- InvestigateWithTools в LLMClient interface
go-app/internal/infrastructure/llm/client.go                -- InvestigateWithTools impl + helpers
go-app/internal/infrastructure/investigation/queue.go       -- agentLoop field, processJob extension
go-app/internal/infrastructure/repository/investigation_repository.go  -- SaveSteps + GetLatestByFingerprint
go-app/internal/core/investigation_repository.go            -- SaveSteps в interface
go-app/internal/core/investigation.go                       -- Steps/IterationsCount/ToolCallsCount в Investigation
go-app/internal/config/config.go                            -- AgentEnabled, MaxIterations, etc.
go-app/internal/application/service_registry.go             -- AgentLoop wiring
go-app/internal/application/handlers/investigation_handler.go  -- steps в HTTP response
```

---

## Блокеры и риски

| Риск | Митигация |
|------|-----------|
| Phase 5A не завершена | Не начинать реализацию; дождаться merge Phase 5A |
| LLMClient интерфейс расширяется → сломает все моки | Добавить stub-реализацию `InvestigateWithTools` в DryRunClient и все test mocks |
| LLM не поддерживает tool calling (proxy mode) | `InvestigateWithTools` в proxy mode → fallback на `InvestigateAlert`; warn log |
| context.DeadlineExceeded в AgentLoop не сохраняет steps | `defer` block в Run() сохраняет steps до возврата ошибки |
| Большой JSONB steps замедляет запросы к alert_investigations | GIN index; steps только при SELECT по id, не по fingerprint scan |
