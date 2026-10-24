---
id: UPSTREAM-CORE-POC-WIRING
slug: upstream-core-poc-wiring
stream: Architecture / ADR-019
type: feature
status: active
created_at: 2026-10-24
updated_at: 2026-10-24
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: ядро upstream Alertmanager в бинаре AMP

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-24

## Touched Files

- `go-app/go.mod`, `go-app/go.sum` — upstream v0.34.1, `go mod tidy`
- `go-app/internal/upstreamcore/{doc,core,reloader}.go` — wiring ядра
- `go-app/cmd/amp-upstream/main.go` — бинарь
- `go-app/internal/upstreamcore/core_test.go` — сквозной тест (write-tests)

## Phase 1: Зависимость

- [x] **1.1** `require github.com/prometheus/alertmanager v0.34.1`, `go mod tidy` <!-- verify: cd go-app && go build ./... && go build -tags futureparity ./... -->

## Phase 2: Ядро

- [x] **2.1** `doc.go` с назначением и атрибуцией <!-- verify: go vet ./internal/upstreamcore/ -->
- [x] **2.2** `reloader.go` — порт `app/reloader.go` без `peer` <!-- depends: 1.1 | verify: go build ./internal/upstreamcore/ -->
- [x] **2.3** `core.go` — `Options`, `New` (порт `setup` без кластера/UI/listeners), `Handler`, `Reload`, `Stop`, метрики <!-- depends: 2.2 | verify: go vet ./internal/upstreamcore/ -->

## Phase 3: Бинарь

- [x] **3.1** `cmd/amp-upstream/main.go`: флаги, `compat.InitFromFlags`, `extURL`, `http.Server`, сигналы <!-- depends: 2.3 | verify: go build -o /tmp/... ./cmd/amp-upstream && ручной смоук: старт на examples-конфиге, curl /-/healthy, POST/GET alerts -->

**Phase verification:** `go vet ./internal/upstreamcore/ ./cmd/amp-upstream/`

## Phase 4: Тесты (write-tests, после deep-review pass)

- [x] **4.1** `core_test.go`: ingest → `GET /api/v2/alerts` → группа → доставка в `httptest` webhook → silence → `suppressed` → `DELETE` silence; reload невалидного конфига → 500, прежний конфиг в силе; `Stop` идемпотентен <!-- verify: go test -race ./internal/upstreamcore/ -->

## Phase 5: testing + инвентаризация

- [ ] **5.1** `govulncheck ./...` <!-- verify: 0 reachable или запись в BUGS -->
- [x] **5.2** Размер `amp-upstream` против `cmd/server` (`-ldflags="-s -w"` и без) → research <!-- verify: числа в research.md -->
- [ ] **5.3** Гейты: `make -C go-app quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check` <!-- verify: PASS -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
