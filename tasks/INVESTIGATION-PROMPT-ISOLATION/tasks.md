---
id: INVESTIGATION-PROMPT-ISOLATION
slug: investigation-prompt-isolation
stream: Investigation / LLM
type: bug
status: active
created_at: 2026-10-29
updated_at: 2026-10-29
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: изоляция промпта и границы investigation tools

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-29

## Touched Files

- `go-app/internal/core/investigation.go` — `ErrLLMProviderUnsupported`
- `go-app/internal/core/investigation/{context.go,redact.go,agent_loop.go}` — namespace в контексте, редактирование
- `go-app/internal/infrastructure/llm/{chat.go,client.go,investigate_with_tools.go,circuit_breaker_metrics.go}` — `doChat`, промпт, breaker, ретраи
- `go-app/internal/infrastructure/investigation/tools/kubernetes.go` — allowlist
- `go-app/internal/infrastructure/investigation/queue.go` — `classifyError`
- `go-app/internal/config/investigation_tools.go`, `go-app/internal/application/service_registry.go` — конфиг и wiring
- `config.yaml.example`, `go-app/internal/core/investigation/README.md`, `CHANGELOG.md`

Базовая проверка: `cd go-app && go build ./... && go vet ./internal/core/... ./internal/infrastructure/llm/... ./internal/infrastructure/investigation/... ./internal/application/... ./internal/config/...`

## Phase 1: Ядро (core)

- [x] **1.1** `ErrLLMProviderUnsupported` в `core/investigation.go` <!-- verify: go build ./internal/core/... -->
- [x] **1.2** `WithAlertNamespace`/`AlertNamespaceFromCtx` в `context.go` <!-- verify: go build ./internal/core/... -->
- [x] **1.3** `redactSecrets` в `redact.go`; в `AgentLoop.Run` — namespace в `toolCtx`, `redactSecrets` до `capObservation` <!-- depends: 1.2 | verify: go vet ./internal/core/investigation/... && go test ./internal/core/investigation/... -->

## Phase 2: LLM-клиент

- [x] **2.1** `chat.go`: `doChat`, `alertDataBlock`, `untrustedDataRule`, `requireOpenAIProvider` <!-- verify: go build ./internal/infrastructure/llm/... -->
- [x] **2.2** `classifyAlertOpenAI`, `InvestigateAlert` на `doChat` и `alertDataBlock` <!-- depends: 2.1 | verify: go test ./internal/infrastructure/llm/... -->
- [x] **2.3** `investigationBreaker` + метрики + `callInvestigation`; `InvestigateAlert` через него <!-- depends: 2.2 | verify: go test ./internal/infrastructure/llm/... -->
- [x] **2.4** `InvestigateWithTools`: `doChat`, ретраи шага, breaker; `buildOpenAIMessages` — алерт в `user` <!-- depends: 2.3 | verify: go test ./internal/infrastructure/llm/... -->

## Phase 3: Tools, очередь, wiring

- [x] **3.1** Kubernetes tool: allowlist, namespace из контекста, без пустого namespace <!-- depends: 1.2 | verify: go test ./internal/infrastructure/investigation/tools/... -->
- [x] **3.2** `classifyError`: `ErrLLMProviderUnsupported` → permanent <!-- depends: 1.1 | verify: go test ./internal/infrastructure/investigation/... -->
- [x] **3.3** Конфиг `AllowedNamespaces`, `DSN`; wiring в `service_registry.go` <!-- depends: 3.1 | verify: go build ./... && go vet ./internal/application/... -->

## Phase 4: Docs

- [x] **4.1** `config.yaml.example`, `core/investigation/README.md`, CHANGELOG `[Unreleased]` с migration notes <!-- verify: git diff --check -->

## Phase 5: Тесты (после deep-review pass)

- [ ] **5.1** Промпт: алерт только в `user`, разделитель не закрывается, правило в `system`, runbooks в `system` (все три пути) <!-- verify: go test ./internal/infrastructure/llm/... -->
- [ ] **5.2** `doChat`/breaker: 503→503→200 ретраится в шаге, 400 — нет и не открывает breaker; медленный вызов не открывает breaker; открытый breaker — без сети; `proxy` → `ErrLLMProviderUnsupported` без сети <!-- verify: go test -race ./internal/infrastructure/llm/... -->
- [ ] **5.3** Kubernetes allowlist: edge cases 1–3 Spec, `*`, явный список <!-- verify: go test ./internal/infrastructure/investigation/tools/... -->
- [ ] **5.4** `redactSecrets` таблицей; agent loop редактирует observation и ошибку, кладёт namespace <!-- verify: go test ./internal/core/investigation/... -->
- [ ] **5.5** `classifyError` для sentinel <!-- verify: go test ./internal/infrastructure/investigation/... -->

## Phase 6: Гейты

- [ ] **6.1** Затронутые пакеты `go vet` + `go test -race`; `make -C go-app quality-gates-fast`; `scripts/release-gate.sh`; `git diff --check` <!-- verify: см. WORKFLOW.md § Гейты AMP -->

## Deviations

- **Тесты пакетов `llm` и `tools` не компилируются между implement и write-tests.** `buildOpenAIMessages` теперь возвращает ошибку (раньше при неудаче маршалинга молча слал `"{}"`), `NewKubernetesTool` принимает allowlist. `investigate_with_tools_prompt_test.go` и `tools/*_test.go` правятся в 5.1/5.3: старые проверки (алерт в `system`, namespace `default`) всё равно противоречат новому поведению. Шаги 1.3 и 3.x проверены `go build ./...`, `go vet` по пакетам без затронутых тестов, `go test ./internal/core/investigation/...` (зелёный).
- **Начальное `user`-сообщение агентного цикла пересобирается на каждой итерации** (раньше было только на первой). Иначе со второго шага модель теряла бы данные алерта, перенесённые из `system`. Spec §1 уточнён этим пунктом.
- `pgx/v5/stdlib` в `service_registry.go` стал blank-импортом: `OpenDBFromPool` больше не используется, драйвер `pgx` нужен для `sql.Open`.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
