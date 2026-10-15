---
id: PROD-DEPS-NET-HTTP2
slug: prod-deps-net-http2
stream: Security
type: bug
status: active
created_at: 2026-10-15
updated_at: 2026-10-15
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: net/http и x/net — GO-2026-6603…6617

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-15

## Touched Files

- `go-app/go.mod` — `toolchain go1.26.9`, `golang.org/x/net` v0.60.0 + транзитивка из Spec P2.
- `go-app/go.sum` — хеши новых версий.
- `Dockerfile` — `FROM golang:1.26.9-alpine` (строка 7) и комментарий строки 38.
- `Dockerfile.config-reloader` — `FROM golang:1.26.9-alpine` (строка 12).
- `.github/workflows/ci.yml` — комментарий строки 36.
- `docs/CI.md` — § Go Version.
- `CHANGELOG.md` — `[Unreleased]` → `### Security`.
- Тестовые файлы — не меняются: Go-код не меняется (Spec § Invariants). Покрытие — release-gate.

## Phase 1: Апгрейд версий

> **Gate:** не начинать до **2026-10-15 17:23 UTC** (карантин `x/net` v0.60.0, Spec § Security Design).

> **Wave 1**

- [ ] **1.1** Проверить окно карантина. <!-- verify: date -u ≥ 2026-10-15T17:23Z; curl -s https://proxy.golang.org/golang.org/x/net/@v/v0.60.0.info → Time 2026-10-08T17:22:58Z -->

> **Wave 2** — depends on Wave 1

- [ ] **1.2** `cd go-app && go get golang.org/x/net@v0.60.0 toolchain@go1.26.9` (без `-u`, без `go mod tidy`). <!-- depends: 1.1 | verify: grep -E '^toolchain go1.26.9$|golang.org/x/net v0.60.0' go-app/go.mod — обе строки; grep '^go 1.26.0$' go-app/go.mod -->
- [ ] **1.3** Сверить дельту `go.mod` с Spec P2 и записать даты всех новых версий в `evidence/new-module-dates.txt`. Модуль вне таблицы P2 или моложе 7 дней — стоп, вопрос владельцу (Spec Edge Case 1). <!-- depends: 1.2 | verify: git diff -U0 go-app/go.mod > tasks/PROD-DEPS-NET-HTTP2/evidence/go.mod.diff; git diff go-app/go.mod | grep '^[-+]' | grep -v -E 'golang.org/x/(net|crypto|sys|term|text|sync|tools|mod) |^[-+]toolchain |^(\+\+\+|---)' — пусто; для каждой новой версии go list -m -json <mod>@<ver> → Time ≤ 2026-10-08 -->
- [ ] **1.4** Заменить `1.26.8` → `1.26.9` в `Dockerfile` (7, 38), `Dockerfile.config-reloader` (12), `.github/workflows/ci.yml` (36), `docs/CI.md` (§ Go Version). <!-- verify: git grep -n '1\.26\.8' -- ':!docs/06-planning' ':!tasks' ':!CHANGELOG.md' — пусто -->

> **Wave 3** — depends on Wave 2

- [ ] **1.5** Сборка в readonly-режиме на целевом toolchain (ловит недостающие хеши в `go.sum`). <!-- depends: 1.2 | verify: cd go-app && GOTOOLCHAIN=go1.26.9 GOFLAGS=-mod=readonly go build ./... && GOTOOLCHAIN=go1.26.9 go version → go1.26.9 -->
- [ ] **1.6** `govulncheck` на `go1.26.9` — 0 достижимых; невызываемые — как в baseline (GO-2026-5932). <!-- depends: 1.2 | verify: cd go-app && GOTOOLCHAIN=go1.26.9 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... > ../tasks/PROD-DEPS-NET-HTTP2/evidence/govulncheck-go1.26.9.txt; exit 0, "Your code is affected by 0 vulnerabilities" -->
- [ ] **1.7** Сборка обоих образов, builder на `go1.26.9`. <!-- depends: 1.4 | verify: docker build -f Dockerfile -t amp:net-http2 . && docker build -f Dockerfile.config-reloader -t amp-config-reloader:net-http2 . — оба OK; docker run --rm golang:1.26.9-alpine go version → go1.26.9 -->
- [ ] **1.8** Коммит `fix(deps): bump Go to 1.26.9 and x/net to v0.60.0` — `go.mod`, `go.sum`, оба Dockerfile, `ci.yml`, `docs/CI.md` (+ evidence). <!-- depends: 1.3, 1.5, 1.6, 1.7 | verify: git show --stat HEAD — только перечисленные файлы -->

**Phase verification:** `make -C go-app quality-gates-fast` + `git status` чистый после `go fmt`.

## Phase 2: Docs

- [ ] **2.1** `CHANGELOG.md` `[Unreleased]` → `### Security`: `PROD-DEPS-NET-HTTP2` (дата, 12 ID: GO-2026-6603, -6605, -6607…-6613, -6617 и две `html/template`; `go1.26.8` → `go1.26.9`, `x/net` v0.58.0 → v0.60.0 + транзитивка; образы на `golang:1.26.9-alpine`). Отдельный коммит `docs(changelog): …`. <!-- depends: 1.8 | verify: grep -n "PROD-DEPS-NET-HTTP2" CHANGELOG.md -->

## Phase 3: Deep review

- [ ] **3.1** `/deep-review` (обязателен: `S`) по диффу ветки: версии против Spec P2, инварианты (одна версия Go во всех местах, `go 1.26.0`, нет модулей вне цепочки), вывод `govulncheck`, исключение из карантина для образа записано. <!-- depends: 1.8, 2.1 | verify: jq -r .gate tasks/PROD-DEPS-NET-HTTP2/review-verdict.json → pass -->

## Phase 4: Tests

- [ ] **4.1** `/write-tests`: новых тестов не ожидается (Go-код не меняется); подтвердить и записать обоснование. Если ревью потребует — тест на конкретное поведение. <!-- depends: 3.1 | verify: обоснование записано в tasks.md -->

## Phase 5: Testing

- [ ] **5.1** Гейты AMP: затронутые пакеты — весь модуль (stdlib), поэтому сразу полный `scripts/release-gate.sh` на `GOTOOLCHAIN=go1.26.9`; флейки — перепрогон и запись. <!-- depends: 4.1 | verify: GOTOOLCHAIN=go1.26.9 scripts/release-gate.sh — все шаги PASS; вывод в evidence/release-gate.txt -->
- [ ] **5.2** `git diff --check main...HEAD`, нет `_, _ :=` в диффе. <!-- depends: 4.1 | verify: git diff --check main...HEAD — пусто -->

## Phase 6: Finalize

- [ ] **6.1** `/finalize`: `DONE.md`, `NEXT.md` (WIP снять; у `MAIN-BRANCH-PROTECTION` снять `Blocked-by: PROD-DEPS-NET-HTTP2` и оставить условие «зелёный CI на `main` после push»), BACKLOG P0 → закрытые, архив workspace. <!-- depends: 5.1, 5.2 | verify: ls tasks/archive/PROD-DEPS-NET-HTTP2; grep -c PROD-DEPS-NET-HTTP2 docs/06-planning/NEXT.md → 0 в WIP -->
- [ ] **6.2** `/merge-to-main`; push — по решению владельца. Подтверждение результата — зелёный `govulncheck` на `main` в CI. <!-- depends: 6.1 | verify: git log main -1 -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated (обоснование отсутствия — 4.1)
- [ ] Phase checks pass
- [ ] Docs/planning are updated (`CHANGELOG.md`, `docs/CI.md`, planning в finalize)
