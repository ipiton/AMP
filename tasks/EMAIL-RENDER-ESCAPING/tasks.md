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

- [ ] **1.1** HTML-часть через `html/template` <!-- verify: cd go-app && go build ./... -->
- [ ] **1.2** `sanitizeHeaderValue` для Subject <!-- verify: cd go-app && go build ./... -->
- [ ] **1.3** CHANGELOG [Unreleased] Security <!-- verify: git diff --check -->

## Phase 2: Тесты (после deep-review `pass`)

- [ ] **2.1** HTML: `<script>`, `javascript:`; text не экранирован; дефолтные шаблоны рендерятся <!-- verify: go test -run Email ./internal/infrastructure/publishing/ -->
- [ ] **2.2** MIME: CR/LF в Subject (ASCII и не-ASCII) <!-- verify: go test -run 'BuildMIME|Email' ./internal/infrastructure/publishing/ -->

## Phase 3: Testing

- [ ] **3.1** Гейты `WORKFLOW.md` § Гейты AMP <!-- verify: evidence/release-gate.txt -->

## Notes
