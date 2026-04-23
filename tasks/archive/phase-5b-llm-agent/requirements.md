# PHASE-5B: Agentic Investigation Loop с Tool Calling

## Проблема

Phase 5A строит async pipeline с one-shot LLM вызовом:

```
InvestigationWorker → LLM("расследуй алерт") → одиночный ответ → DB
```

Этот подход слепой: LLM получает только текст алерта и генерирует предположение без доступа к реальным данным. Настоящий SRE не только «думает» — он итеративно запрашивает Prometheus, читает логи, проверяет состояние K8s-подов, и корректирует гипотезу по результатам.

**Phase 5B** добавляет инфраструктуру агентного цикла (tool calling loop) поверх Phase 5A pipeline:

```
InvestigationWorker
  ↓
AgentLoop.Run(alert, classification, tools)
  ↓ итеративно:
  LLM(history + alert) → ToolCallRequest | FinalAnswer
        ↓ если ToolCallRequest:
  ToolRegistry.Execute(toolName, params) → ToolResult
        ↓ добавить в history, повторить
  LLM получает наблюдение → делает следующий шаг
        ↓ если FinalAnswer:
  InvestigationResult с полным trace → DB
```

Phase 5A остаётся корректным частным случаем (ноль tool calls, direct answer).

---

## Что делает Phase 5B

1. **Tool abstraction layer** — интерфейс `Tool`, `ToolRegistry`, `ToolDefinition` (JSON Schema)
2. **AgentLoop** — цикл Think → Act → Observe → repeat с лимитом итераций и timeout budget
3. **LLM tool calling** — расширение `LLMClient` для OpenAI function calling protocol
4. **Investigation trace** — хранение всех шагов (reasoning + tool calls + observations) в DB
5. **Stub tools** — тестовые заглушки для CI (без реального Prometheus/K8s)
6. **Интеграция с Phase 5A** — AgentLoop заменяет one-shot `InvestigateAlert()` в worker

Phase 5B **НЕ включает** реальные инструменты — это Phase 6A (PromQL, LogQL, K8s API).

---

## Success Criteria

1. `AgentLoop.Run()` выполняет полный цикл: LLM запрашивает tool → tool выполняется → результат возвращается в LLM → LLM делает final answer
2. Loop завершается при любом из: финальный ответ, max iterations, timeout budget, критическая ошибка
3. Все шаги (thoughts, tool_calls, observations, conclusion) сохраняются в `alert_investigations.steps` (JSONB)
4. Зарегистрированный stub tool выполняется через агентный цикл в unit-тестах (без реального LLM)
5. Если tool вернул ошибку — агент получает error observation и может продолжить или завершить с partial findings
6. HTTP endpoint `GET /api/v1/alerts/{fingerprint}/investigation` возвращает steps в ответе
7. Phase 1 pipeline не замедляется: AgentLoop работает только в Phase 2 (async)
8. `go vet ./...` и `go test ./...` проходят без новых ошибок

---

## Scope

### В scope
- Пакет `go-app/internal/core/investigation/` — интерфейсы Tool, ToolRegistry, AgentLoop
- Типы AgentMessage, AgentResponse, InvestigationStep (trace)
- LLM tool calling — расширение `HTTPLLMClient.InvestigateWithTools()`
- Миграция: добавить `steps JSONB`, `iterations_count INTEGER`, `tool_calls_count INTEGER` в `alert_investigations`
- Wiring Phase 5A worker → AgentLoop (если AgentMode включён в конфиге)
- Stub tools: `echo`, `noop` для тестов
- Unit-тесты AgentLoop с mock LLM и stub tools

### Вне scope (следующие фазы)
- Реальные tools: PromQL, LogQL, K8s API — Phase 6A
- Runbook matching tool — Phase 6B
- UI для отображения trace — отдельная задача
- Webhook/нотификация с результатами расследования

### Зависимость
**PHASE-5A должна быть завершена до начала реализации PHASE-5B.**
Phase 5B расширяет `InvestigationWorker`, `InvestigationRepository`, `LLMClient` из Phase 5A.
