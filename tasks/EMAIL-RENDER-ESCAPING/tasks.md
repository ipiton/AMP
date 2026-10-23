---
id: EMAIL-RENDER-ESCAPING
slug: email-render-escaping
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

# Implementation Plan: экранирование HTML и Subject в email

## Touched Files

- `go-app/internal/infrastructure/publishing/email_publisher_enhanced.go`
- `go-app/internal/infrastructure/publishing/email_client.go`
- `go-app/internal/infrastructure/publishing/email_publisher_test.go` — Phase 2
- `CHANGELOG.md`

## Phase 1: Implement

- [x] **1.1** HTML-часть через `html/template` <!-- verify: cd go-app && go build ./... -->
- [x] **1.2** `sanitizeHeaderValue` для Subject <!-- verify: cd go-app && go build ./... -->
- [x] **1.3** CHANGELOG [Unreleased] Security <!-- verify: git diff --check -->

## Phase 2: Тесты (после deep-review `pass`)

- [x] **2.1** HTML: `<script>`, `javascript:`; text не экранирован; дефолтные шаблоны рендерятся, в том числе алерт только с `alertname` <!-- verify: go test -run Email ./internal/infrastructure/publishing/ -->
- [x] **2.2** MIME: CR/LF в Subject (ASCII и не-ASCII) <!-- verify: go test -run 'BuildMIME|Email' ./internal/infrastructure/publishing/ -->

## Phase 3: Testing

- [x] **3.1** Гейты `WORKFLOW.md` § Гейты AMP <!-- verify: evidence/release-gate.txt -->

## Notes
- 2026-10-23, deep-review `pass` @ `cbe4330`; F1 (major, был defer-bug) взят сюда — две строки в той же функции, без него письмо по алерту без `instance` не уходит вовсе; F2/F4 — доки. F3 — TECH-DEBT на finalize.
- 2026-10-23, testing: `go vet` + `go test` publishing/notification зелёные, `make -C go-app quality-gates-fast` зелёный, `scripts/release-gate.sh` — 11/12 PASS; `lint` FAIL только в `delivery_confirmation_test.go` и `silencing/redis_event_bus_test.go` (предсуществующий `LINT-ERRCHECK-CHANNEL-RECEIVE`, файлы не тронуты). `git diff --check` чистый.
