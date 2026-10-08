---
id: PROD-DEPS-OTEL-145
slug: prod-deps-otel-145
stream: Security
type: bug
status: active
created_at: 2026-10-08
updated_at: 2026-10-08
based_on:
  - requirements.md
  - Spec.md
---

# Implementation Plan: OpenTelemetry v1.45.0 (GO-2026-6505)

**Based on:** requirements.md (Research mini) / Spec.md  
**Date:** 2026-10-08

## Touched Files

- `go-app/go.mod` — otel-семейство v1.44.0 → v1.45.0 + транзитивка из Spec P1.
- `go-app/go.sum` — хеши новых версий.
- `CHANGELOG.md` — строка в `[Unreleased]` → `### Security`.
- Тестовые файлы — не меняются: поведение кода не меняется (Spec § Invariants). Покрытие — существующие тесты `pkg/telemetry` + release-gate.

## Phase 1: Апгрейд зависимостей

> **Wave 1**

- [x] **1.1** `cd go-app && go get go.opentelemetry.io/otel@v1.45.0 go.opentelemetry.io/otel/sdk@v1.45.0 go.opentelemetry.io/otel/trace@v1.45.0 go.opentelemetry.io/otel/exporters/otlp/otlptrace@v1.45.0 go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.45.0` (без `-u`, без `go mod tidy`). <!-- verify: diff <(git diff go-app/go.mod) tasks/PROD-DEPS-OTEL-145/evidence/spec-trial-go.mod.diff — пусто (совпадает с Spec P1) -->

> **Wave 2** — depends on Wave 1

- [x] **1.2** Проверить инварианты: `go`/`toolchain`, `grpc`, `x/*` и не-otel direct-зависимости не изменились. <!-- depends: 1.1 | verify: git diff go-app/go.mod | grep '^[-+]' | grep -v -E 'opentelemetry|go-logr/logr|genproto|^(\+\+\+|---)' — пусто -->
- [x] **1.3** Сборка и тесты затронутого пакета в readonly-режиме (ловит недостающие хеши в `go.sum`, Spec Edge Case 1). <!-- depends: 1.1 | verify: cd go-app && GOFLAGS=-mod=readonly go build ./... && go vet ./pkg/telemetry/... && go test ./pkg/telemetry/... -->
- [x] **1.4** `govulncheck` — 0 достижимых. <!-- depends: 1.1 | verify: cd go-app && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... → "Your code is affected by 0 vulnerabilities"; вывод в evidence/govulncheck.txt -->
- [x] **1.5** Коммит `fix(deps): bump OpenTelemetry to v1.45.0 (GO-2026-6505)` — только `go-app/go.mod`, `go-app/go.sum`. <!-- depends: 1.2, 1.3, 1.4 | verify: git show --stat HEAD — два файла -->

**Phase verification:** `make -C go-app quality-gates-fast` + `git status` чистый после `go fmt`.

## Phase 2: Docs

- [x] **2.1** `CHANGELOG.md` `[Unreleased]` → `### Security`: `PROD-DEPS-OTEL-145` (дата, GO-2026-6505, otel v1.44.0 → v1.45.0, URL экспортёра больше не пишется в INFO-лог; в бинари AMP уязвимый путь не входил — `pkg/telemetry` не импортируется). Отдельный коммит `docs(changelog): …`. <!-- verify: grep -n "PROD-DEPS-OTEL-145" CHANGELOG.md -->

### Implementation notes (2026-10-08)

- 1.1: дифф `go.mod` побайтно совпал с `evidence/spec-trial-go.mod.diff`.
- 1.2: вне otel/logr/genproto изменений в `go.mod` нет.
- 1.3: `GOFLAGS=-mod=readonly go build ./...` OK, `go test ./pkg/telemetry/...` OK.
- 1.4: `evidence/govulncheck.txt` — 0 достижимых, 1 недостижимая в required-модулях (GO-2026-5932, по `-show verbose`).
- Phase verification: `make -C go-app quality-gates-fast` — passed, `go fmt` файлов не менял.
- 1.5: `go.sum` — только добавления (+21): старые хеши остались, т. к. `go mod tidy` не запускался (Spec § Target Design, `GO-MOD-TIDY-CHECK`). Отклонений от Spec нет.

## Phase 3: Deep review

- [x] **3.1** `/deep-review` (обязателен: `S`) по диффу ветки: модули/версии против Spec P1–P2, инварианты, вывод govulncheck. <!-- depends: 1.5, 2.1 | verify: jq -r .gate tasks/PROD-DEPS-OTEL-145/review-verdict.json → pass -->

## Phase 4: Tests

- [ ] **4.1** `/write-tests`: новых тестов не ожидается (Go-код не меняется); подтвердить и записать обоснование в `tasks.md`/`testing`. Если ревью потребует — тест на конкретное поведение. <!-- depends: 3.1 | verify: обоснование записано -->

## Phase 5: Testing & finalize

- [ ] **5.1** `scripts/release-gate.sh` — зелёный (флейки `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` / `PUBLISHING-WARMUP-TEST-FLAKY` — перепрогон и фиксация). <!-- depends: 4.1 | verify: exit 0, вывод в evidence/release-gate.txt -->
- [ ] **5.2** `git diff --check main...HEAD`; нет `_, _ :=` в диффе. <!-- depends: 5.1 | verify: пустой вывод -->
- [ ] **5.3** PR → CI: `gate` и `govulncheck` зелёные (закрывает Spec P6). <!-- depends: 5.2 | verify: gh pr checks -->
- [ ] **5.4** `/finalize`: DONE.md, NEXT.md (WIP → пусто), BACKLOG (P0 → Закрыто), предложить `DEAD-PKG-TELEMETRY` (Spec § Open Questions), отметить, что `MAIN-BRANCH-PROTECTION` разблокирован. <!-- depends: 5.3 | verify: workspace в tasks/archive/ -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
