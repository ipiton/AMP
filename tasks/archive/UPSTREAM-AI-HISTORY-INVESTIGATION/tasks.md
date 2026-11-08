---
id: UPSTREAM-AI-HISTORY-INVESTIGATION
slug: upstream-ai-history-investigation
stream: Architecture / ADR-019
type: feature
status: done
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

- [x] **1.1** `convert.go`: статус по `a.Resolved()` <!-- verify: cd go-app && go build ./internal/upstreamai/ -->
- [x] **1.2** `history.go`: `History` (шарды, воркеры, таймаут, метрика, Warn на эпизод) <!-- verify: go vet ./internal/upstreamai/ -->
- [x] **1.3** `chain.go`: `Chain` <!-- verify: go build ./internal/upstreamai/ -->
- [x] **1.4** `classifier.go`: `Submitter`, `Config.Investigations`, `Submit` после результата, пропуск по mute удаляет запись, outcome `skipped_muted` <!-- verify: go test ./internal/upstreamai/ (существующие тесты 2a — поправить ожидание outcome в write-tests) -->

## Phase 2: upstreamcore

- [x] **2.1** `core.go`: `var r` до `mem.NewAlerts`, mutes = silence ∨ `r.inhibits`; `reloader.go`: `inhibits`; `doc.go`: отличие <!-- verify: go test ./internal/upstreamcore/ -->

## Phase 3: amp-upstream

- [x] **3.1** `amp.go`: `newAMPLayer` — конфиг, PG + миграции (fail-open), история, классификатор (перенос из `main.go`), investigation, `Chain`, `stop` <!-- depends: 1.x, 2.1 | verify: go build ./cmd/amp-upstream/ -->
- [x] **3.2** `main.go`: вызов `newAMPLayer` вместо `newClassifier` <!-- depends: 3.1 | verify: go vet ./cmd/amp-upstream/ -->

**Phase verification:** `make -C go-app quality-gates-fast`

## Deviations

- Investigation `Stop` — 5s (`investigationStopTimeout`, как в `cmd/server`), а не 10s из Spec.
- `TestSilencedAlertIsSkipped` (2a) заменён `TestMutedAlertIsSkippedUntilRecheck` (outcome `skipped_muted`, перепроверка).
- После deep-review: перепроверка mute через 1m вместо «пропуск без памяти» (F1), повторный `Submit` на resend (F2), миграции с таймаутом 1m (F3), resolved из GC в историю (F8), `stopped` в `Classifier` (N2). Доп. тесты: `TestNoSubmitAfterStop`, `TestWarnOnlyPassesWarningsUp` (`cmd/amp-upstream/amp_test.go`); 4.5 — в `upstreamcore/mutes_test.go`.

## Phase 4: tests (после `review-verdict.json` = pass)

- [x] **4.1** `history_test.go`: неблокирующий `PostStore`, порядок по fingerprint, дроп при переполнении, статусы, SHA-256 ключ, `PreStore` nil, GC no-op
- [x] **4.2** `classifier_test.go`: investigation после classified/fallback, не после ошибки и mute; повтор после снятия mute; outcome `skipped_muted`
- [x] **4.3** `convert_test.go`: FNV upstream == `UpstreamFingerprint`, SHA-256 == `alertconv.Fingerprint`, статус resolved
- [x] **4.4** `chain_test.go`: порядок, первая ошибка `PreStore`
- [x] **4.5** `upstreamcore/core_test.go`: `mutes` истинна для алерта под inhibition через API
- [x] **4.6** `integration_test.go` (upstreamai): ingest → история и investigation по одному ключу

**Phase verification:** `cd go-app && go test -race ./internal/upstreamai/ ./internal/upstreamcore/ ./cmd/amp-upstream/`

## Phase 5: docs и гейты

- [x] **5.1** `docs/UPSTREAM_UPGRADE.md`, `config.yaml.example`, `CHANGELOG.md` `[Unreleased]`
- [x] **5.2** Гейты: затронутые пакеты, `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`

## Definition of Done

- [x] All steps are complete or explicitly marked blocked/skipped
- [x] Success criteria from `requirements.md` are covered
- [x] Contracts from `Spec.md` are implemented or deviations are recorded
- [x] Deep review verdict is `pass`
- [x] Tests for changed behavior are added or updated
- [x] Phase checks pass
- [x] Docs/planning are updated
