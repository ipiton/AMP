---
id: INVESTIGATION-SAFETY-RELIABILITY
slug: investigation-safety-reliability
stream: Investigation / LLM
type: bug
priority: high
status: active
created_at: 2026-10-28
updated_at: 2026-10-28
---

# Requirements: надёжность LLM-вызовов и очереди расследований

Срез 1 бандла `INVESTIGATION-SAFETY-BUNDLE` (`TECH-DEBT.md` § Bundles). Участники: `LLM-RETRY-CLASSIFY`, `INVESTIGATION-ASYNC-DEDUP` (`BUGS.md`). Срез 2 — `INVESTIGATION-PROMPT-ISOLATION` (Queue). Источник — CODE-REVIEW-2026-10-21 H-7, H-8, L-8, M-10.

## Problem Framing

- **Symptom:**
  - Ответы LLM 429/5xx не ретраятся, ретраятся только таймауты: худший случай 30s × 4 внутри `POST /api/v2/alerts`. `max_retries: 0` молча превращается в 3 — ретраи выключить нельзя.
  - Тело ответа LLM целиком уходит в ERROR-лог и в сообщение ошибки (`llm/client.go:319,323,419,608`), оттуда — в БД через `SaveError`.
  - `InvestigationQueue.Submit` (`investigation/queue.go:122-160`) делает `repo.Create` синхронно на горутине запроса, без таймаута, хотя вызов из `alert_processor.go:851-853` задуман fire-and-forget.
  - Дедупа расследований нет; смена `EndsAt` на каждом resend считается апдейтом ⇒ новое расследование на каждый resend (~1/мин на горящий алерт).
  - Вывод investigation tools не ограничен: `kubernetes.go:185` (`io.Copy`, `tail_lines` выбирает LLM), `loki.go:82,114`, `prometheus.go:107`, `agent_loop.go` (observation без кэпа).
- **Root Cause:**
  - `llm.HTTPError` (`llm/errors.go:26`) — не `httperror.HTTPAPIError`, поэтому `httperror.IsRetryable` (`pkg/httperror/error.go:445`) его не узнаёт; `resilience.WithRetryFunc` (`core/resilience/resilience.go:55-57`) трактует `MaxRetries == 0` как «не задано».
  - Очередь писалась с персистом статуса `queued` до постановки в канал; дедуп был отложен в `PHASE-5A-INVESTIGATION-PIPELINE`.
- **Why Now:** голова живой части конвейера ревью 2026-10-21; код investigation переживает переход на upstream (ADR-019). Неограниченная стоимость LLM и задержка ingest из-за БД — эксплуатационный риск при первом же шумном алерте.
- **How We Measure:** тесты на httptest (503→200 ретраится, 400 — нет, `max_retries: 0` — ровно одна попытка), `Submit` не ходит в репозиторий, повторный `Submit` того же `fingerprint+StartsAt` в окне TTL не создаёт расследования, вывод tools обрезан до лимита.

## Risk Profile

- **Signals:** `S X R`
  - `S` — тело ответа LLM (может содержать эхо промпта с данными алерта) в логах и БД: усечение = обращение с PII.
  - `X` — `llm`, `core/resilience`, `pkg/httperror`, `infrastructure/investigation` (+ tools), `core/investigation`, `core/services` (точка `Submit`).
  - `R` — путь ingest (`POST /api/v2/alerts`): латентность и поведение ретраев; новые метрики дедупа.
- **Tier:** Full
- **Notes:** `S` эскалирует до Full независимо от размера; дифф, вероятно, > 200 строк.

## User Stories

1. Как оператор AMP, я хочу, чтобы кратковременный 429/503 у LLM-провайдера ретраился с backoff, а `max_retries: 0` действительно отключал ретраи, чтобы управлять латентностью ingest.
2. Как оператор, я хочу, чтобы медленная БД расследований не задерживала приём алертов.
3. Как владелец бюджета LLM, я хочу одно расследование на инцидент (`fingerprint+StartsAt`), а не на каждый resend.
4. Как оператор, я не хочу OOM воркера и раздувания контекста LLM из-за огромного вывода tool.

## Success Criteria

- [ ] 429 и 5xx от LLM ретраятся (classify и investigate), 4xx кроме 429 — нет; покрыто тестом httptest 503→200 и 400→без ретрая.
- [ ] `max_retries: 0` — ровно одна попытка; незаданный ключ — прежний дефолт (3). Различие «не задано» / `0` покрыто тестом.
- [ ] Тело ответа LLM в логах и в тексте ошибки (→ `SaveError`) обрезано до 512 байт.
- [ ] `Submit` не обращается к репозиторию и не блокирует: только неблокирующая отправка в канал; `Create` — в воркере; при переполнении — drop + метрика (без записи в БД из горутины запроса).
- [ ] Повторный `Submit` с тем же `fingerprint+StartsAt` в пределах TTL не ставит новое расследование; дедуп работает между репликами, если есть общий кеш (решается в research); метрика дедупа.
- [ ] Вывод tools ограничен: `io.LimitReader` 256 KiB для kubernetes/loki/prometheus, clamp `tail_lines`/`limit` ≤ 500, кэп символов observation перед записью в history/контекст.
- [ ] `go test -race ./internal/infrastructure/investigation/... ./internal/infrastructure/llm/... ./internal/core/resilience/...` зелёный; гейты AMP (`WORKFLOW.md` § Гейты AMP).
- [ ] CHANGELOG `[Unreleased]`: Fixed (ретраи, дедуп, лимиты) + Changed (`max_retries: 0` теперь отключает ретраи).

## Non-Goals

- Изоляция промпта, allowlist namespace, DSN для `database_query`, CB в investigate-пути — срез 2 `INVESTIGATION-PROMPT-ISOLATION`.
- Бюджет времени LLM в ingest и вынос классификации из синхронного пути — `UPSTREAM-AI-HOOKS` (поглотил `PROD-LLM-ALERT-PATH-ISOLATION`).
- `INVESTIGATION-ALERT-TIME-NOT-SET`, `AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT` — отдельные баги, не расширяем.
- Изменения в ядре алертинга, которое удаляется по ADR-019.

## Constraints

- **Scope:** `internal/infrastructure/llm`, `internal/core/resilience`, `internal/infrastructure/investigation` (+ `tools`), `internal/core/investigation` (кэп observation), wiring в `internal/application/service_registry.go` при необходимости дедуп-хранилища.
- **Security:** усечение тела ответа LLM в логах/ошибках; данные алерта в лог не добавлять.
- **Compatibility:** `max_retries: 0` меняет смысл (было 3 ретрая, станет 0) — Changed в CHANGELOG с migration note. Схема БД не меняется. Статус `queued` в БД появляется позже (когда воркер взял job) — проверить, что API статуса расследования это переживает.

## Discovery Notes

- Similar tasks: `tasks/archive/PHASE-6B-RUNBOOK-ENGINE/` (investigation queue/agent loop), `tasks/archive/AMP-CACHE-NOAUTH/` (кеш Redis / in-memory fallback).
- Relevant patterns: `pkg/httperror` (`HTTPAPIError`, `LLMClassifier`), `pkg/retry`, кеш-абстракция AMP (Redis с in-memory fallback), `ConfigDefaults` через viper.
- Open unknowns:
  - Хранилище дедупа: Redis `SETNX` через существующую кеш-абстракцию vs in-memory TTL-map; поведение при in-memory fallback и нескольких репликах.
  - Как различить «не задано» и `0` для `max_retries` при загрузке через viper (pointer, `IsSet`, sentinel) — без изменения формы конфига.
  - Где TTL дедупа берётся из конфига (новый ключ `investigation.dedup_ttl`?) или константа.
  - Используется ли статус `queued` из БД API/UI до того, как воркер возьмёт job.
- Research level: 2 (`research.md` из light-шаблона).
