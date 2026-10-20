---
id: SLACK-WEBHOOK-PLAIN-OK-RESPONSE
slug: slack-webhook-plain-ok
stream: operations
type: bug
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - Spec.md
---

# Implementation Plan: ответ `ok` Slack Incoming Webhook считается ошибкой

**Based on:** requirements.md / Spec.md
**Date:** 2026-10-20

## Touched Files

- `go-app/internal/infrastructure/publishing/slack_client.go` — 2xx: JSON только если тело JSON, `ok`/пустое — успех.
- `go-app/internal/infrastructure/publishing/slack_publisher_enhanced.go` — не кешировать threading-запись без `ts`.
- `go-app/internal/infrastructure/publishing/slack_client_response_test.go` — новый: матрица ответов клиента.
- `go-app/internal/infrastructure/publishing/slack_publisher_test.go` — publisher при пустом `ts`.
- `go-app/internal/infrastructure/publishing/queue_target_publisher_test.go` — мок вебхука отвечает `ok`.
- `go-app/internal/application/config_only_delivery_e2e_test.go` — групповой путь: nflog записан, повтора нет.

## Phase 1: Implement

- [ ] **1.1** Ветка 2xx в `doRequestWithRetry` + `parseSlackSuccess` по Spec п.1–2 <!-- verify: go build ./... -->
- [ ] **1.2** `postMessage`: `cache.Store` только при `resp.TS != ""` <!-- verify: go vet ./internal/infrastructure/publishing/... -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/infrastructure/publishing/...`

## Phase 2: Tests

- [ ] **2.1** Таблица клиента: 200 `ok`, `ok\n`, 204 пусто, JSON ok=true (ts), JSON ok=false, text/plain JSON ok=false, 200 текст-ошибка, 400 `invalid_payload` (1 попытка), 404 `no_service`, 500 текст (3 попытки) <!-- verify: go test -run TestSlackClient_Response ./internal/infrastructure/publishing/ -->
- [ ] **2.2** Publisher: пустой `ts` → в кеше нет записи; повторный firing → `PostMessage`, не `ReplyInThread` <!-- verify: go test -run TestEnhancedSlackPublisher ./internal/infrastructure/publishing/ -->
- [ ] **2.3** Мок `queue_target_publisher_test.go:388` → `text/plain` `ok` <!-- verify: go test -run TestCreatePublisherForJob_Slack ./internal/infrastructure/publishing/ -->
- [ ] **2.4** e2e: `TestConfigOnlyDelivery_SlackWebhookOKConfirmsDelivery` — одна отправка за ≥3 `group_interval` <!-- verify: go test -run TestConfigOnlyDelivery_Slack ./internal/application/ ; без фикса — красный -->

**Phase verification:** `go test ./internal/infrastructure/publishing/... ./internal/application/...`

## Phase 3: Gates

- [ ] **3.1** `go vet` + `go test` затронутых пакетов
- [ ] **3.2** `make -C go-app quality-gates-fast`, `git status`
- [ ] **3.3** `scripts/release-gate.sh`
- [ ] **3.4** `git diff --check`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
