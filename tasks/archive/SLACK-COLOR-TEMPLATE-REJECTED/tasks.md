---
id: SLACK-COLOR-TEMPLATE-REJECTED
slug: slack-color-template
stream: Config
type: bug
status: done
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - Spec.md
---

# Implementation Plan: шаблонные presentation-поля не валидируются как литералы

**Based on:** requirements.md / Spec.md
**Date:** 2026-10-20
**Tier:** Standard (`R`) → `implement → write-tests → testing → finalize`

## Touched Files

- `go-app/internal/infrastructure/routing/parser.go` — тег `template_expr`
- `go-app/internal/infrastructure/routing/receiver.go` — теги шести полей
- `go-app/internal/infrastructure/routing/template_fields_validation_test.go` (новый) — T1, T2
- `go-app/internal/infrastructure/publishing/template_formatter_test.go` — T3
- `go-app/internal/application/templates_delivery_e2e_test.go` — T4
- `CHANGELOG.md`, `docs/06-planning/{BUGS,NEXT,DONE}.md` — finalize

## Phase 1: implement

- [x] Зарегистрировать `template_expr` → `validateTemplateExpression` (`core.IsTemplateExpression`).
  Verify: `go build ./internal/infrastructure/routing/`
- [x] Перевести теги `Channel`, `IconEmoji`, `IconURL`, `TitleLink`, `Color`, `Severity` на `template_expr|<правило>`.
  Verify: `go test ./internal/infrastructure/routing/` — существующие тесты зелёные.

## Phase 2: write-tests

- [x] T1, T2 — `template_fields_validation_test.go`.
- [x] T3 — firing/resolved цвет в `template_formatter_test.go`.
- [x] T4 — e2e из YAML в `templates_delivery_e2e_test.go`.
  Verify: `go test ./internal/infrastructure/routing/ ./internal/infrastructure/publishing/ ./internal/application/`

## Phase 3: testing

- [x] `go vet` + `go test` по затронутым пакетам.
- [x] `make -C go-app quality-gates-fast`, `git status` после `go fmt`.
- [x] Конфиг sema (`.alertmanager.config` из `prometheus-stack-values.prod.yaml`) через production-путь `appconfig.LoadConfig` — загружается (`config parsed successfully routes=4 receivers=3`, `errors-slack` color сохранён verbatim); на `main` — ошибка `slack_color`. Временный probe-тест, не закоммичен (в конфиге webhook URL).
- [x] `scripts/release-gate.sh` → `evidence/release-gate.txt`.
- [x] `git diff --check`.

## Phase 4: finalize

- [x] BUGS.md: удалить SLACK-COLOR-TEMPLATE-REJECTED, завести follow-up по `email_configs[].to` / `slack_configs[].actions[]`.
- [x] NEXT.md: снять из WIP. DONE.md: отчёт. CHANGELOG.md `[Unreleased]` → Fixed.
- [x] Архивировать workspace в `tasks/archive/`.
