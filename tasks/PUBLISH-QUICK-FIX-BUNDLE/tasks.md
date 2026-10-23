---
id: PUBLISH-QUICK-FIX-BUNDLE
slug: publish-quick-fix-bundle
stream: Publishing
type: bug
status: active
created_at: 2026-10-23
updated_at: 2026-10-23
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: быстрые исправления publishing (бандл)

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-23

## Touched Files

- `go-app/internal/infrastructure/publishing/pagerduty_publisher_enhanced.go`, `pagerduty_cache.go` (удаляется), `pagerduty_errors.go`
- `go-app/internal/infrastructure/publishing/publisher.go` — фабрика, `HTTPPublisher`
- `go-app/internal/infrastructure/publishing/slack_publisher_enhanced.go`, `rootly_publisher_enhanced.go`, `rootly_incident_store.go` (новый)
- `go-app/internal/infrastructure/publishing/circuit_breaker.go`, `queue.go`
- `go-app/internal/infrastructure/publishing/webhook_auth.go`, `*_client.go`
- `go-app/internal/core/redact.go` (новый)
- `go-app/internal/application/publishing_runtime.go`, `business/publishing/discovery_impl.go`, `config/config.go`, `infrastructure/llm/{client.go,investigate_with_tools.go}`
- `CHANGELOG.md`
- тесты — Phase 3

## Phase 1: Resolve и изоляция состояния

- [x] **1.1** PD: resolve по `alert.Fingerprint`; удалить кеш из паблишера, фабрики, `pagerduty_cache.go`, `ErrEventNotTracked` <!-- verify: cd go-app && go build ./... -->
- [x] **1.2** `correlationKey(target, fingerprint)`; Slack и Rootly ключуют им кеш <!-- verify: cd go-app && go build ./... -->
- [x] **1.3** Ключ SMTP-клиента из всех полей `SMTPConfig` (пароль — sha256) <!-- verify: cd go-app && go build ./... -->
- [x] **1.4** Rootly: `rootly_incident_store.go` поверх `cache.Cache`, `SetRootlyIncidentStore`, wiring в `publishing_runtime.go`; продление на update; промах resolve — WARN + `not_tracked`, ошибка хранилища — ошибка доставки <!-- depends: 1.2 | verify: cd go-app && go build ./... -->

**Phase verification:** `cd go-app && go build ./...`

## Phase 2: Breaker, очередь, секреты

- [x] **2.1** Breaker: half-open с одной пробой и тайм-аутом пробы <!-- verify: cd go-app && go build ./... -->
- [x] **2.2** Очередь: `closeMu`/`closed`/`stopOnce`, дренаж всех каналов, `recover` в `processJob` <!-- verify: cd go-app && go build ./... -->
- [x] **2.3** `core.RedactURL`; `maskURL` делегирует; маскировка `*url.Error` и тела в `HTTPPublisher`; лог URL в фабрике и discovery <!-- verify: cd go-app && go build ./... -->
- [x] **2.4** `readLimited` во всех клиентах publishing, `io.LimitReader` в llm; пароль из ошибки `config.go` <!-- verify: cd go-app && go build ./... && go vet ./internal/config/ ./internal/infrastructure/llm/ -->
- [x] **2.5** `CHANGELOG.md` [Unreleased] Fixed; доки PD/Rootly — по grep <!-- verify: git diff --check -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/infrastructure/publishing/...` (vet компилирует тесты: красный до Phase 3 ожидаем из-за сигнатуры PD-паблишера)

## Phase 3: Тесты (после deep-review `pass`)

- [x] **3.1** Починить компиляцию тестов после удаления PD-кеша <!-- verify: cd go-app && go vet ./internal/infrastructure/publishing/... -->
- [x] **3.2** PD: resolve без trigger ⇒ `event_action=resolve`, `dedup_key=fingerprint` <!-- verify: go test -run PagerDuty ./internal/infrastructure/publishing/ -->
- [x] **3.3** Два Slack/Rootly-таргета с одним fingerprint не делят записи; SMTP: другой пароль/второй таргет ⇒ другой клиент <!-- verify: go test -run 'PerTarget|SMTPClientKey' ./internal/infrastructure/publishing/ -->
- [x] **3.4** Rootly: ID из общего хранилища виден новому экземпляру паблишера; промах resolve ⇒ `nil` + `not_tracked`, breaker не тронут; ошибка `Get` ⇒ ошибка, инцидент не создаётся <!-- verify: go test -run Rootly ./internal/infrastructure/publishing/ -->
- [x] **3.5** Breaker: конкурентные `CanAttempt` после `Timeout` ⇒ одна проба; переходы; тайм-аут пробы <!-- verify: go test -race -run CircuitBreaker ./internal/infrastructure/publishing/ -->
- [x] **3.6** Очередь: `Stop` с заданиями во всех приоритетах; `Submit` после `Stop`; двойной `Stop`; паника паблишера <!-- verify: go test -race -run 'Queue.*Stop|Panic' ./internal/infrastructure/publishing/ -->
- [x] **3.7** `RedactURL`; ошибка `HTTPPublisher` и `WebhookHTTPClient` без токена и с телом ≤512; лог фабрики/discovery без токена; ошибка пароля без значения <!-- verify: go test -run 'Redact|Secret' ./internal/core/ ./internal/infrastructure/publishing/ ./internal/business/publishing/ ./internal/config/ -->

**Phase verification:** `cd go-app && go test -race ./internal/infrastructure/publishing/... ./internal/business/publishing/...`

## Phase 4: Testing

- [ ] **4.1** Гейты `WORKFLOW.md` § Гейты AMP: затронутые пакеты, `make -C go-app quality-gates-fast`, `scripts/release-gate.sh` <!-- verify: evidence/release-gate.txt -->

## Notes
- 2026-10-23, implement: `IncidentIDCache.Size()` убран из интерфейса — у реализации поверх общего кеша нет дешёвого подсчёта, в проде `Size()` не вызывался (только тесты, Phase 3). `pagerduty_cache.go` удалён; `pagerduty_cache_test.go` и тесты с `cache.Size()` не компилируются до Phase 3 — ожидаемо.
- 2026-10-23, deep-review fix (F1, F3, F5, F7): `IncidentIDCache.Get` → `(id, found, err)`; `ErrIncidentNotTracked` удалён; `parseError` PD/Rootly цитируют тело через `errorBody`; ошибка `NewRequestWithContext` в `HTTPPublisher` маскируется. F2/F4/F6 — follow-ups на finalize.
- 2026-10-23, deep-review round 2 fix (F8, F9): `WebhookHTTPClient` маскирует `*url.Error` (создание запроса и транспорт) и цитирует тело через `errorBody`; Slack/Telegram fallback-сообщение ошибки — через `errorBody`. F10 — в TECH-DEBT вместе с F4.
- Отклонение от Spec не требовалось; хелперы тела/URL — `publishing/http_body.go` (`readLimited`, `errorBody`, `redactURLError`).
