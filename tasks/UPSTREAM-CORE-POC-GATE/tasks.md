---
id: UPSTREAM-CORE-POC-GATE
slug: upstream-core-poc-gate
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

# Implementation Plan: контрактные сьюты и amtool на upstream-ядре, обновляемость ядра

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-24

## Touched Files

- `go-app/cmd/server/{futureparity_compat.go,upstreamcore_compat.go,futureparity_compat_test.go}` — шов
- `go-app/cmd/server/testdata/upstreamcore-{divergences.tsv,alertmanager.yml}` — baseline, конфиг amtool
- `go-app/internal/upstreamcore/{doc.go,port_test.go}` — `PortedFrom`, дрейф порта
- `scripts/{upstream-core-gate.sh,upstream-bump.sh,release-gate.sh}` — раннер, обновление, гейт
- `docs/{UPSTREAM_UPGRADE.md,CI.md}` — процедура
- `tasks/UPSTREAM-CORE-POC-GATE/divergences.md` — классификация и вердикт

## Phase 1: Харнесс

- [x] **1.1** `registerRoutesImpl` + `upstreamcore_compat.go` + тег `!upstreamcore` у `futureparity_compat_test.go` <!-- verify: go test -tags futureparity ./cmd/server/ (без изменений) && go vet -tags futureparity,upstreamcore ./cmd/server/ -->

## Phase 2: Классификация и baseline

- [x] **2.1** `divergences.md` — каждое листовое падение: класс, причина, решение <!-- depends: 1.1 | verify: все 51 падение в таблице -->
- [x] **2.2** `testdata/upstreamcore-divergences.tsv` <!-- depends: 2.1 -->

## Phase 3: Раннер и amtool

- [x] **3.1** `scripts/upstream-core-gate.sh`: сравнение с baseline + amtool на `amp-upstream` <!-- depends: 2.2 | verify: PASS; удалить строку из baseline → FAIL; добавить фиктивную → FAIL -->
- [x] **3.2** `release-gate.sh`: amtool-образ из `go.mod`, шаг `upstream-core`; `docs/CI.md` <!-- depends: 3.1 | verify: bash -n, shellcheck если есть -->

## Phase 4: Обновляемость

- [x] **4.1** `PortedFrom` в `doc.go`, `port_test.go` <!-- verify: go test ./internal/upstreamcore/; подмена константы → FAIL -->
- [x] **4.2** `scripts/upstream-bump.sh` (карантин всех сдвинутых модулей, `--allow-fresh`, `--diff`) <!-- verify: --diff v0.34.0 v0.34.1; dry-run на текущей версии; отказ на свежей версии -->
- [x] **4.3** `docs/UPSTREAM_UPGRADE.md` <!-- depends: 4.2 -->

## Phase 5: Testing (после deep-review)

- [ ] **5.1** Гейты: `quality-gates-fast`, `release-gate.sh`, `git diff --check`, `govulncheck`

## Acceptance

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
