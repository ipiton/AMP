---
id: UPSTREAM-AI-HISTORY-INVESTIGATION
slug: upstream-ai-history-investigation
stream: Architecture / ADR-019
type: feature
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: история и investigation на upstream-ядре

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-11-08

## Touched Files

- `go-app/internal/upstreamai/history.go` — новый колбэк истории.
- `go-app/internal/upstreamai/chain.go` — композиция колбэков.
- `go-app/internal/upstreamai/classifier.go` — investigation, пропуск без записи.
- `go-app/internal/upstreamai/convert.go` — статус resolved.
- `go-app/internal/upstreamcore/{core.go,reloader.go,doc.go}` — mutes с inhibitor.
- `go-app/cmd/amp-upstream/{amp.go,main.go}` — wiring.
- `docs/UPSTREAM_UPGRADE.md`, `config.yaml.example`, `CHANGELOG.md`.

## Phase 1: upstreamai

> **Wave 1**

- [ ] **1.1** `convert.go`: статус по `a.Resolved()` <!-- verify: cd go-app && go build ./internal/upstreamai/ -->
- [ ] **1.2** `history.go`: `History` (шарды, воркеры, таймаут, метрика, Warn на эпизод) <!-- verify: go vet ./internal/upstreamai/ -->
- [ ] **1.3** `chain.go`: `Chain` <!-- verify: go build ./internal/upstreamai/ -->
- [ ] **1.4** `classifier.go`: `Submitter`, `Config.Investigations`, `Submit` после результата, пропуск по mute удаляет запись, outcome `skipped_muted` <!-- verify: go test ./internal/upstreamai/ (существующие тесты 2a — поправить ожидание outcome в write-tests) -->

## Phase 2: upstreamcore

- [ ] **2.1** `core.go`: `var r` до `mem.NewAlerts`, mutes = silence ∨ `r.inhibits`; `reloader.go`: `inhibits`; `doc.go`: отличие <!-- verify: go test ./internal/upstreamcore/ -->

## Phase 3: amp-upstream

- [ ] **3.1** `amp.go`: `newAMPLayer` — конфиг, PG + миграции (fail-open), история, классификатор (перенос из `main.go`), investigation, `Chain`, `stop` <!-- depends: 1.x, 2.1 | verify: go build ./cmd/amp-upstream/ -->
- [ ] **3.2** `main.go`: вызов `newAMPLayer` вместо `newClassifier` <!-- depends: 3.1 | verify: go vet ./cmd/amp-upstream/ -->

**Phase verification:** `make -C go-app quality-gates-fast`

## Phase 4: tests (после `review-verdict.json` = pass)

- [ ] **4.1** `history_test.go`: неблокирующий `PostStore`, порядок по fingerprint, дроп при переполнении, статусы, SHA-256 ключ, `PreStore` nil, GC no-op
- [ ] **4.2** `classifier_test.go`: investigation после classified/fallback, не после ошибки и mute; повтор после снятия mute; outcome `skipped_muted`
- [ ] **4.3** `convert_test.go`: FNV upstream == `UpstreamFingerprint`, SHA-256 == `alertconv.Fingerprint`, статус resolved
- [ ] **4.4** `chain_test.go`: порядок, первая ошибка `PreStore`
- [ ] **4.5** `upstreamcore/core_test.go`: `mutes` истинна для алерта под inhibition через API
- [ ] **4.6** `integration_test.go` (upstreamai): ingest → история и investigation по одному ключу

**Phase verification:** `cd go-app && go test -race ./internal/upstreamai/ ./internal/upstreamcore/ ./cmd/amp-upstream/`

## Phase 5: docs и гейты

- [ ] **5.1** `docs/UPSTREAM_UPGRADE.md`, `config.yaml.example`, `CHANGELOG.md` `[Unreleased]`
- [ ] **5.2** Гейты: затронутые пакеты, `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
