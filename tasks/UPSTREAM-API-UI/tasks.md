---
id: UPSTREAM-API-UI
slug: upstream-api-ui
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

# Implementation Plan: API среза 6 ADR-019

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-11-08

## Touched Files

- `go-app/migrations/20261108000000_alerts_upstream_fingerprint.sql` — новая миграция.
- `go-app/internal/core/history.go` — `AlertHistoryRecord`.
- `go-app/internal/infrastructure/postgres_storage_adapter.go` — запись колонки, ридеры, backfill.
- `go-app/internal/ampapi/` — новый пакет маршрутов.
- `go-app/cmd/amp-upstream/{amp.go,main.go}` — ридеры из AMP-слоя, композиция handler'а.
- Тесты (фаза 3): `internal/ampapi/*_test.go`, `cmd/amp-upstream/routes_test.go`, `internal/infrastructure/postgres_storage_adapter_upstream_fp_test.go`, `internal/database/migrations_upstream_fp_test.go`.
- `CHANGELOG.md`, `docs/06-planning/*` — фаза 4.

## Phase 1: Данные

- [ ] **1.1** Миграция `upstream_fingerprint` + индекс, down. <!-- verify: grep -c "goose Down" go-app/migrations/20261108000000_alerts_upstream_fingerprint.sql -->
- [ ] **1.2** `core.AlertHistoryRecord`. <!-- verify: cd go-app && go build ./internal/core/ -->
- [ ] **1.3** `SaveAlert`/`UpdateAlert` пишут колонку; `AlertRecordByFingerprint`, `AlertRecordByUpstreamFingerprint`, `BackfillUpstreamFingerprints`. <!-- depends: 1.1, 1.2 | verify: cd go-app && go vet ./internal/infrastructure/ && go test ./internal/infrastructure/ ./internal/core/services/ -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/...`

## Phase 2: HTTP

- [ ] **2.1** Пакет `internal/ampapi`: handler, валидация, ответы, метрика. <!-- depends: 1.2 | verify: cd go-app && go vet ./internal/ampapi/ -->
- [ ] **2.2** `newAMPLayer` → `*ampLayer` с ридерами; один repo investigation; backfill-горутина с отменой. <!-- depends: 1.3 | verify: cd go-app && go vet ./cmd/amp-upstream/ && go test ./cmd/amp-upstream/ -->
- [ ] **2.3** `withAMPRoutes` в `main.go` внутри `wrapWebAuth`; doc/help. <!-- depends: 2.1, 2.2 | verify: cd go-app && go build ./cmd/amp-upstream && scripts/upstream-core-gate.sh (из корня) -->
- [ ] **2.4** Ручная проверка: бинарь без `--amp.config.file` → `/api/amp/v1/alerts/<16hex>` 503, `/api/v2/status` 200. <!-- depends: 2.3 | verify: evidence/manual-smoke.md -->

**Phase verification:** `make -C go-app quality-gates-fast && git status`

## Phase 3: Тесты (после `review-verdict.json` `"gate": "pass"`)

- [ ] **3.1** `ampapi`: 400/404/503/500/200 для обоих маршрутов, FNV/SHA, регистр, префикс, 405. <!-- verify: cd go-app && go test ./internal/ampapi/ -->
- [ ] **3.2** Композиция: байты `/api/v2/status`, `/api/v2/alerts`, `/api/v2/inhibitions`, `/api/v1/alerts` = `core.Handler()`; `/api/amp/…` → AMP; префикс; auth закрывает AMP-маршруты. <!-- verify: cd go-app && go test ./cmd/amp-upstream/ -->
- [ ] **3.3** testcontainers: миграция up/down/up; `SaveAlert`/`UpdateAlert` пишут FNV = `model.LabelSet.Fingerprint()`; поиск по обоим ключам, коллизия; backfill заполняет NULL и идемпотентен. <!-- verify: cd go-app && go test ./internal/infrastructure/ ./internal/database/ -run 'Upstream|Migration' -count=1 -->
- [ ] **3.4** `AlertRecordBy*` + investigation end-to-end на PG: `ampapi` поверх настоящих ридеров. <!-- depends: 3.3 | verify: cd go-app && go test ./internal/ampapi/ -count=1 -->

**Phase verification:** `cd go-app && go test ./internal/ampapi/ ./cmd/amp-upstream/ ./internal/infrastructure/ ./internal/database/ -count=1`

## Phase 4: Testing, docs, finalize

- [ ] **4.1** `scripts/release-gate.sh` целиком зелёный; лог в `evidence/release-gate.txt`. <!-- verify: tail evidence/release-gate.txt -->
- [ ] **4.2** CHANGELOG `[Unreleased]`: Added (маршруты, колонка, метрика) и Breaking (investigation path/поле, inhibitions, group_by). <!-- verify: grep -n "UPSTREAM-API-UI" CHANGELOG.md -->
- [ ] **4.3** Planning: BACKLOG (`UPSTREAM-API-UI` [x], `STATUS-VERSIONINFO-CONTRACT`, `COMPAT-VERSION-CONFIG-KEY`, `HISTORY-API`, `UPSTREAM-DOCS-MIGRATION`, `PHASE-5A-…`), ROADMAP, DECISIONS (уточнение ADR-019), DONE, NEXT. <!-- verify: grep -n "UPSTREAM-API-UI" docs/06-planning/*.md -->
- [ ] **4.4** Архив `tasks/archive/UPSTREAM-API-UI/`, коммит, sync с `main`. <!-- verify: git status && git log --oneline main..HEAD -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
