---
id: INVESTIGATION-SAFETY-RELIABILITY
slug: investigation-safety-reliability
stream: Investigation / LLM
type: bug
status: active
created_at: 2026-10-28
updated_at: 2026-10-28
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: надёжность LLM-вызовов и очереди расследований

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-28

## Touched Files

- `go-app/internal/infrastructure/llm/{errors.go,client.go}` — ретраибельность `*HTTPError`, усечение тела.
- `go-app/internal/core/resilience/resilience.go` — `MaxRetries: 0` без подмены.
- `go-app/internal/infrastructure/investigation/{queue.go,dedup.go,metrics.go}` — `Submit`, `processJob`, dedup.
- `go-app/internal/infrastructure/investigation/tools/{kubernetes.go,loki.go,prometheus.go}` — лимиты.
- `go-app/internal/core/investigation/agent_loop.go` — кэп observation.
- `go-app/internal/application/service_registry.go` — wiring Redis-дедупа.
- `CHANGELOG.md`, `config.yaml.example`, `go-app/internal/infrastructure/llm/README.md` — доки.

## Phase 1: Ретраи LLM

- [x] **1.1** `llm.IsRetryableError`: `*HTTPError` 429/5xx → true, прочие коды → false. <!-- verify: cd go-app && go build ./internal/infrastructure/llm/... -->
- [x] **1.2** `resilience.WithRetryFunc`: убрать подмену `MaxRetries==0`, отрицательное → 0. <!-- verify: cd go-app && go test ./internal/core/resilience/... -->
- [x] **1.3** `llm/client.go`: `truncateBody(body, 512)` в логе и `HTTPError.Message` (classify proxy/OpenAI, investigate). <!-- verify: cd go-app && go vet ./internal/infrastructure/llm/... && go test ./internal/infrastructure/llm/... -->

**Phase verification:** `cd go-app && go test ./internal/infrastructure/llm/... ./internal/core/resilience/...`

## Phase 2: Очередь и дедуп

- [x] **2.1** `investigation/dedup.go`: локальная TTL-карта + опциональный `redis.Cmdable` (`SETNX`, fail-open). <!-- verify: cd go-app && go build ./internal/infrastructure/investigation/... -->
- [x] **2.2** `metrics.go`: `DeduplicatedTotal{level}`. <!-- verify: go build -->
- [x] **2.3** `queue.go`: `Submit` без БД (claim local → неблокирующая отправка; full → drop + release); `processJob` — claim shared, `Create` с таймаутом; `SetSharedDedup`. <!-- depends: 2.1, 2.2 | verify: cd go-app && go test ./internal/infrastructure/investigation/ -->
- [x] **2.4** `service_registry.go`: `SetSharedDedup(redisCache.ShareClient())`, если `r.cache` — `*RedisCache`. <!-- depends: 2.3 | verify: cd go-app && go build ./... -->

**Phase verification:** `cd go-app && go test ./internal/infrastructure/investigation/... ./internal/application/...`

## Phase 3: Лимиты tools

- [x] **3.1** `tools`: `maxToolResponseBytes`, clamp `tail_lines`/`limit` `[1,500]`, `LimitReader` в kubernetes/loki/prometheus. <!-- verify: cd go-app && go test ./internal/infrastructure/investigation/tools/... -->
- [x] **3.2** `agent_loop.go`: кэп observation 16 KiB по границе руны. <!-- verify: cd go-app && go test ./internal/core/investigation/... -->

**Phase verification:** `cd go-app && go test ./internal/infrastructure/investigation/... ./internal/core/investigation/...`

## Phase 4: Доки

- [x] **4.1** CHANGELOG `[Unreleased]`: Fixed (ретраи 429/5xx, дедуп, `Submit` без БД, лимиты tools), Changed (`llm.max_retries: 0`, видимость `queued`), Security (усечение тела LLM). `config.yaml.example`/llm README — смысл `max_retries: 0`. <!-- verify: git diff --check -->

**Отклонения от Spec:** `llm/README.md` не менялся — в нём только список полей, смысл `max_retries: 0` описан в `config.yaml.example` и CHANGELOG. Усечение тела добавлено и в `investigate_with_tools.go:126` (четвёртое место, в Spec не упомянуто).

## Phase 5: Deep review

- [x] **5.1** Независимый агент, `review-findings.md` + `review-verdict.json`, `gate: pass`.

## Phase 6: Тесты (после `pass`)

- [x] **6.1** llm: httptest 503→200 ретраится, 400 — одна попытка, `max_retries: 0` — одна попытка, тело > 512 байт усечено в ошибке.
- [x] **6.2** resilience: `MaxRetries: 0` — одна попытка.
- [x] **6.3** queue: `Submit` не вызывает repo; повторный `Submit` того же инцидента не ставит job; full → drop и повтор возможен; shared-claim через miniredis (две очереди — одно расследование), ошибка Redis → fail-open; `Create` в воркере.
- [x] **6.4** tools: clamp `tail_lines`/`limit`, тело > 256 KiB → ошибка (loki/prometheus) / обрезка (k8s logs).
- [x] **6.5** agent_loop: observation > 16 KiB обрезан в steps и history.

**Дополнительно:** re-fire `StartsAt` (F1/F12) — `core/services/deduplication_test.go`; `reloadable_llm_test` переписан под `max_retries: 0` = 0; фикстура `alert_processor_silence_test.go` переведена на относительное время (на main тест краснел: правило фильтра «resolved старше 48h» + фиксированная дата 2026-10-23). Для k8s logs обрезка проверена на `tailBuffer` — fake clientset отдаёт фиксированный лог.

**Phase verification:** `cd go-app && go test -race ./internal/infrastructure/investigation/... ./internal/infrastructure/llm/... ./internal/core/resilience/... ./internal/core/investigation/...`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
