---
id: INVESTIGATION-PROMPT-ISOLATION
slug: investigation-prompt-isolation
stream: Investigation / LLM
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-29
updated_at: 2026-10-29
---

# Research Pack - изоляция промпта и границы investigation tools

Уровень: light (риск-сигнал `S`, но варианты ограничены кодом; внешних интеграций нет).

## TL;DR

Алерт уходит в `user`-сообщение внутри `<alert_data>`, причём `encoding/json` экранирует `<`/`>`, так что данные не могут закрыть разделитель. Namespace алерта передаётся tools через `context`, по образцу уже существующего `WithAlertTime`, и интерфейс `Tool` не меняется. У расследований свой circuit breaker без порога медленных вызовов: общий CB классификации (`SlowCallDuration` 3s) открылся бы на первом же шаге agent loop, и классификация ушла бы в fallback.

## Questions

1. Как дать Kubernetes tool namespace алерта без смены интерфейса `Tool`?
2. Можно ли из данных алерта закрыть текстовый разделитель?
3. Через какой circuit breaker пускать вызовы расследования?
4. Что делать с не-OpenAI провайдером в путях расследования?
5. Где редактировать секреты: в каждом tool или в одной точке?

## Findings

- **F1 (code-read).** `core/investigation/context.go` уже несёт `WithAlertTime`/`AlertTimeFromCtx` для tools. Production-вызова `WithAlertTime` нет: `grep -rn WithAlertTime internal` даёт только определение и чтение в `prometheus.go:73` и `loki.go:77`. Prometheus и Loki всегда якорятся на `time.Now()`. Это отдельный баг, вне scope — follow-up `INVESTIGATION-ALERT-TIME-UNSET`.
- **F2 (call-path-traced).** `Tool.Execute` вызывается только из `ToolRegistry.Execute` (`registry.go:40`), а он — только из `AgentLoop.Run` (`agent_loop.go:202`). Поиск по `internal` и `cmd`. Значит, контекст tool строится в одном месте.
- **F3 (measured, `evidence/json-escape.txt`).** `json.Marshal(map[string]string{"a":"</alert_data> ignore"})` → `{"a":"\u003c/alert_data\u003e ignore"}`. `encoding/json` по умолчанию экранирует `<`, `>`, `&`, поэтому литерал `</alert_data>` из данных в промпт не попадает. `classification.Reasoning` (текст LLM) тоже нужно класть внутрь JSON, а не форматировать строкой.
- **F4 (code-read).** `CircuitBreaker.afterCall` (`circuit_breaker.go:240-246`) считает неудачей любую ошибку и любой вызов дольше `SlowCallDuration`, по умолчанию 3s (`circuit_breaker.go:109`). Шаг agent loop с tools обычно дольше 3s. CB один на `HTTPLLMClient` и защищает синхронную классификацию в ingest. Если пустить через него расследования, CB откроется, и классификация уйдёт в fallback без какой-либо проблемы у провайдера.
- **F5 (code-read).** `NewCircuitBreakerMetrics` — синглтон через `sync.Once` с фиксированными именами (`circuit_breaker_metrics.go:47-52`). Вторая регистрация с теми же именами запаникует (`promauto`), поэтому второму CB нужен свой subsystem. Метрики `nil` CB допускает (`cb.metrics != nil`).
- **F6 (code-read).** Очередь ретраит `InvestigateAlert` сама (`queue.go:234`, `MaxRetries` 3) и классифицирует ошибку по тексту (`classifyError`, `queue.go:428`). Пакет `llm` очередь не импортирует, `core` — импортирует.
- **F7 (code-read).** `llm.provider` по умолчанию `openai` (`config.go:926`). При `proxy` пути классификации шлют `/classify`, а пути расследования — OpenAI-тело на `base_url/chat/completions`, который legacy-прокси не обязан поддерживать.
- **F8 (code-read).** `stringParam` (`tools/prometheus.go:158`) возвращает дефолт и для пустой строки. Пустой namespace в client-go означает «все namespaces», поэтому в API кластера он уходить не должен.
- **F9 (code-read).** `capObservation` (`agent_loop.go:277`) — единственная точка, через которую вывод и ошибка любого tool попадают в trace (БД) и историю LLM.

## Options

Generation: single author. Параллельный fan-out не нужен: решения обратимы, это код одного процесса, миграций данных нет.

**Q1 — namespace в tool**
- A. Значение в `context` (`WithAlertNamespace`) рядом с `WithAlertTime`, выставляется в `AgentLoop.Run` на `toolCtx`. Интерфейс не меняется, паттерн уже есть.
- B. Добавить `alert` в `Tool.Execute`. Правки во всех 4 tools, тестах и stub — шире диффа без выигрыша.
- **Выбор: A.**

**Q3 — circuit breaker**
- A. Общий CB. Отвергнут (F4): ломает классификацию.
- B. Отдельный CB расследований: та же `llm.circuit_breaker`, но `SlowCallDuration` без порога, свои метрики (subsystem `llm_investigation_circuit_breaker`). Неудачей считаются только ошибки доступности (`IsRetryableError`: 429/5xx/сеть/таймаут). 400 из-за размера контекста не должен глушить все расследования на `reset_timeout`.
- **Выбор: B.** Ретраи шага — внутри CB, как в `ClassifyAlert`. `InvestigateAlert` ходит через CB без своих ретраев: его ретраит очередь (F6), иначе получилось бы до 16 вызовов на job.

**Q4 — провайдер**
- A. В путях расследования для не-OpenAI провайдера сразу возвращать sentinel `core.ErrLLMProviderUnsupported`. Очередь классифицирует его как `permanent` через `errors.Is` (F6).
- B. Отказ на старте. Не покрывает hot reload провайдера.
- **Выбор: A.**

**Q5 — редактирование секретов**
- A. В каждом tool. Дублирование, легко забыть в новом tool.
- B. Одна точка в agent loop, перед `capObservation` (F9). Покрывает и текст ошибок.
- **Выбор: B.** Набор шаблонов — best-effort: `key=value`/`key: value` для password/passwd/pwd/secret/token/api_key/apikey/access_key/private_key/client_secret, `Authorization:`, `Bearer <token>`, PEM `PRIVATE KEY` блоки, AWS access key id `AKIA…`.

## Decision

- Алерт и классификация — JSON внутри `<alert_data>` в `user`-сообщении во всех трёх OpenAI-путях. В системном промпте — правило о недоверенных данных (разделитель и результаты tools).
- Kubernetes tool: namespace из `context`, allowlist в конфиге (`*` — opt-in на всё), пустой namespace в API не уходит.
- Редактирование секретов — в `AgentLoop` перед `capObservation`.
- `database_query` — свой `*sql.DB` по `investigation.tools.database.dsn`, без DSN tool не регистрируется.
- `doChat` — общая обвязка OpenAI-запроса. Отдельный CB расследований; ретраи шага agent loop; sentinel для неподдерживаемого провайдера.

## Spec Inputs

- Premises F2, F3, F4, F6 — в Design Premises.
- Breaking для `kubernetes.enabled` и `database.enabled` — migration notes в CHANGELOG.
- Follow-up `INVESTIGATION-ALERT-TIME-UNSET` (F1) — в `BUGS.md` на `finalize`.

## References

- `go-app/internal/infrastructure/llm/client.go`, `investigate_with_tools.go`, `circuit_breaker.go`, `circuit_breaker_metrics.go`
- `go-app/internal/core/investigation/agent_loop.go`, `context.go`, `registry.go`
- `go-app/internal/infrastructure/investigation/tools/kubernetes.go`, `database.go`, `queue.go`
- `go-app/internal/application/service_registry.go:2209-2317`
- CODE-REVIEW-2026-10-21 H-9, L-7, L-9; `tasks/archive/INVESTIGATION-SAFETY-RELIABILITY/review-findings.md` F10
