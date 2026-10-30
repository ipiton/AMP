---
id: UPSTREAM-AI-CLASSIFY
slug: upstream-ai-classify
stream: Architecture / ADR-019
type: feature
status: active
created_at: 2026-10-30
updated_at: 2026-10-30
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: AI-классификация на upstream-ядре

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-30

## Touched Files

- `go-app/internal/infrastructure/llm/{errors.go,client.go}` - `Retry-After`
- `go-app/internal/config/config.go`, `config.yaml.example` - `llm.notify_wait`, `llm.drop_noise`
- `go-app/internal/upstreamcore/{core.go,reloader.go,doc.go}` - точки расширения
- `go-app/internal/upstreamai/{classifier.go,stage.go,convert.go}` - AI-слой
- `go-app/cmd/amp-upstream/main.go` - флаг и сборка
- тесты: `go-app/internal/upstreamai/*_test.go`, `go-app/internal/upstreamcore/core_test.go`, `go-app/internal/infrastructure/llm/*_test.go`, `go-app/internal/config/*_test.go`

## Phase 1: Implement

> **Wave 1** - независимые шаги

- [x] **1.1** `llm`: `HTTPError.RetryAfter`, разбор заголовка в обоих путях classify, checker с `MaxDelay`, ожидание остатка `Retry-After` перед повтором <!-- verify: go vet ./internal/infrastructure/llm/... && go test ./internal/infrastructure/llm/... -->
- [x] **1.2** `config`: `NotifyWait`, `DropNoise`, дефолты, валидация, `config.yaml.example` <!-- verify: go test ./internal/config/... -->
- [x] **1.3** `upstreamcore`: `Options.NewAlertCallback`, `Options.NotifyStage`, композитный колбэк, обёртка `RoutingStage`, `doc.go` <!-- verify: go test ./internal/upstreamcore/... -->

> **Wave 2** - зависит от Wave 1

- [x] **1.4** `upstreamai`: `Classifier` (колбэк, очередь, воркеры, silence, метрики), `NotifyStage`, конвертер <!-- depends: 1.3 | verify: go vet ./internal/upstreamai/... -->
- [x] **1.5** `cmd/amp-upstream`: `--amp.config.file`, сборка при `llm.enabled`, порядок остановки <!-- depends: 1.2, 1.4 | verify: go build ./cmd/amp-upstream -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/upstreamai/... ./internal/upstreamcore/... ./internal/infrastructure/llm/... ./internal/config/... ./cmd/amp-upstream/...`

## Phase 2: Deep review

- [x] **2.1** Независимый агент: `review-findings.md`, `review-verdict.json` с `"gate": "pass"` <!-- verify: jq .gate tasks/UPSTREAM-AI-CLASSIFY/review-verdict.json -->

## Phase 3: Tests

- [x] **3.1** `upstreamai`: PostStore неблокирующий и только firing; переполнение очереди; silence → skipped; ошибка → повтор; стадия ждёт до дедлайна, таймаут, `notify_wait: 0`; клон аннотаций (store не меняется); `drop_noise`; GC удаляет записи <!-- verify: go test -race ./internal/upstreamai/... -->
- [x] **3.2** Сквозной путь на реальном `upstreamcore` (`internal/upstreamai/integration_test.go`, закрывает F7): webhook группы получает `llm_*` и `commonAnnotations`, store без `llm_*`, поздняя классификация не вызывает повтор (dedup), `drop_noise` пропускает resolve; без опций — существующий `core_test.go` <!-- verify: go test -race ./internal/upstreamai/... ./internal/upstreamcore/... -->
- [x] **3.3** `llm`: `Retry-After` > MaxDelay → одна попытка; ≤ — пауза не меньше `Retry-After`; HTTP-date; мусор <!-- verify: go test ./internal/infrastructure/llm/... -->
- [x] **3.4** `config`: дефолты, отрицательный `notify_wait` <!-- verify: go test ./internal/config/... -->

## Phase 4: Gates and docs

- [ ] **4.1** `make -C go-app quality-gates-fast`, `scripts/release-gate.sh`, `scripts/upstream-core-gate.sh`, `git diff --check`
- [ ] **4.2** `CHANGELOG.md` `[Unreleased]`, `docs/UPSTREAM_UPGRADE.md`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed

## Implementation Notes

- Отклонение от Spec: порог `Retry-After` в `llmErrorChecker` применяется, только когда задан (`maxRetryAfter > 0`) — `investigate_with_tools.go` создаёт checker с нулевым значением, и без этого investigation перестал бы ретраить любой ответ с `Retry-After`.
- `--amp.config.file` проходит полную валидацию `config.LoadConfig`: файл должен быть валидным AMP-конфигом целиком (например, `profile: lite` требует `storage.backend: filesystem`), а не только секцией `llm`.
- Smoke 2026-10-30 (фейковый OpenAI + webhook, `group_wait: 1s`): `POST /api/v2/alerts` 200 за 4.7 мс; webhook группы получил `llm_severity`, `llm_confidence`, `llm_reasoning`, `llm_recommendations`, в том числе в `commonAnnotations`; `amp_upstream_ai_classifications_total{outcome="classified"} 1`.
