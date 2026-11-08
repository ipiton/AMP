---
id: UPSTREAM-NON-UPSTREAM-RECEIVERS
slug: upstream-non-upstream-receivers
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

# Implementation Plan: K8s-Secret таргеты и Rootly на upstream-ядре

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-11-08

## Touched Files

- `go-app/internal/upstreamcore/{core.go,reloader.go,doc.go}` — `Options.ExtendConfig` (wiring, минимально).
- `go-app/internal/upstreamtargets/*.go` — новый пакет адаптера.
- `go-app/internal/infrastructure/k8s/client.go` — `K8S-CLIENT-HEALTH-TIMEOUT`.
- `go-app/cmd/amp-upstream/{main.go,targets.go}` — загрузка AMP-конфига один раз, проводка адаптера (wiring).
- `go-app/go.mod` — `gopkg.in/yaml.v2` из indirect в direct (модуль уже в графе).
- Тесты: `upstreamtargets/*_test.go`, `upstreamcore/targets_e2e_test.go`, `infrastructure/k8s/client_test.go`.
- Доки: `docs/ALERTMANAGER_COMPATIBILITY.md` (Rootly), `CHANGELOG.md`, planning.

## Phase 1: Implement

> **Wave 1** — независимые шаги

- [x] **1.1** `upstreamcore.Options.ExtendConfig` + вызов в `reloader.reload`, отметка в `doc.go` <!-- verify: go build ./internal/upstreamcore/... -->
- [x] **1.2** `k8s.client`: `Health` с контекстом, пагинация `ListSecrets`, `Close` без обнуления <!-- verify: go test ./internal/infrastructure/k8s/... -->
- [x] **1.3** `upstreamtargets`: перевод таргета (`convert.go`), принадлежность и `ExtendConfig`, `Source` (`Refresh`, `Run`), метрика, редакция ошибок <!-- verify: go vet ./internal/upstreamtargets/... -->

> **Wave 2** — после 1.1, 1.3

- [x] **1.4** `cmd/amp-upstream`: один `LoadConfig`, `targets.go` (`newTargetSource`, namespace), запуск `Run` <!-- depends: 1.1, 1.3 | verify: go build ./cmd/amp-upstream -->

**Phase verification:** `go build ./... && go vet ./internal/upstreamcore/... ./internal/upstreamtargets/... ./internal/infrastructure/k8s/... ./cmd/amp-upstream/...`

## Phase 2: Deep review

- [ ] **2.1** Независимый агент, `review-findings.md`, `review-verdict.json` с `gate: pass` <!-- verify: review-verdict.json -->

## Phase 3: Tests

- [ ] **3.1** Юнит-тесты `upstreamtargets` (Spec § 5) <!-- verify: go test -race ./internal/upstreamtargets/... -->
- [ ] **3.2** e2e на `upstreamcore` с фейковым K8s <!-- verify: go test -race ./internal/upstreamcore/... -->
- [ ] **3.3** Тесты k8s-клиента <!-- verify: go test -race ./internal/infrastructure/k8s/... -->

## Phase 4: Docs & planning

- [ ] **4.1** `ALERTMANAGER_COMPATIBILITY.md` — Rootly через webhook; `CHANGELOG.md` `[Unreleased]` — заметка к `amp-upstream`
- [ ] **4.2** `BUGS.md`/`BACKLOG.md`: закрыть `K8S-CLIENT-HEALTH-TIMEOUT`, перенести флейки refresh и `ROOTLY-INCIDENT-KEY-STABLE` к срезу 5

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
