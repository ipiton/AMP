---
id: UPSTREAM-API-UI
slug: upstream-api-ui
stream: Architecture / ADR-019
type: feature
status: archived
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

- [x] **1.1** Миграция `upstream_fingerprint` + индекс, down. <!-- verify: grep -c "goose Down" go-app/migrations/20261108000000_alerts_upstream_fingerprint.sql go-app/migrations/20261108000001_alerts_upstream_fingerprint_index.sql -->
- [x] **1.2** `core.AlertHistoryRecord`. <!-- verify: cd go-app && go build ./internal/core/ -->
- [x] **1.3** `SaveAlert`/`UpdateAlert` пишут колонку; `AlertRecordByFingerprint`, `AlertRecordByUpstreamFingerprint`, `BackfillUpstreamFingerprints`. <!-- depends: 1.1, 1.2 | verify: cd go-app && go vet ./internal/infrastructure/ && go test ./internal/infrastructure/ ./internal/core/services/ -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/...`

## Phase 2: HTTP

- [x] **2.1** Пакет `internal/ampapi`: handler, валидация, ответы, метрика. <!-- depends: 1.2 | verify: cd go-app && go vet ./internal/ampapi/ -->
- [x] **2.2** `newAMPLayer` → `*ampLayer` с ридерами; один repo investigation; backfill-горутина с отменой. <!-- depends: 1.3 | verify: cd go-app && go vet ./cmd/amp-upstream/ && go test ./cmd/amp-upstream/ -->
- [x] **2.3** `withAMPRoutes` в `main.go` внутри `wrapWebAuth`; doc/help. <!-- depends: 2.1, 2.2 | verify: cd go-app && go build ./cmd/amp-upstream && scripts/upstream-core-gate.sh (из корня) -->
- [x] **2.4** Ручная проверка: бинарь без `--amp.config.file` → `/api/amp/v1/alerts/<16hex>` 503, `/api/v2/status` 200. <!-- depends: 2.3 | verify: evidence/manual-smoke.md -->

**Phase verification:** `make -C go-app quality-gates-fast && git status`

## Phase 3: Тесты (после `review-verdict.json` `"gate": "pass"`)

- [x] **3.1** `ampapi`: 400/404/503/500/200 для обоих маршрутов, FNV/SHA, регистр, префикс (в том числе `/a{b}`), HEAD, 405 JSON с `Allow`, 404 JSON (неизвестный путь, trailing slash, пустой fp), таймаут в контексте ридера, метрики на `prometheus.NewRegistry()` (F15). <!-- verify: cd go-app && go test ./internal/ampapi/ -->
- [x] **3.2** Композиция: байты `/api/v2/status`, `/api/v2/alerts`, `/api/v2/inhibitions`, `/api/v1/alerts` = `core.Handler()`; `/api/amp/…` → AMP; префикс; auth закрывает AMP-маршруты. <!-- verify: cd go-app && go test ./cmd/amp-upstream/ -->
- [x] **3.3** testcontainers: обе миграции up/down/up; `SaveAlert`/`UpdateAlert` пишут FNV = `model.LabelSet.Fingerprint()`; поиск по обоим ключам, коллизия; backfill заполняет NULL и идемпотентен; `UpstreamFingerprintIndexValid` — true, false без индекса, false на INVALID (`UPDATE pg_index SET indisvalid=false`); `EXPLAIN` запроса backfill (F5) — в лог теста. <!-- verify: cd go-app && go test ./internal/infrastructure/ -run 'Upstream|Backfill' -count=1 -->
- [x] **3.4** `AlertRecordBy*` + investigation end-to-end на PG: `ampapi` поверх настоящих ридеров. <!-- depends: 3.3 | verify: cd go-app && go test ./internal/infrastructure/ -run AMPAPIOnPostgres -count=1 -->

**Phase verification:** `cd go-app && go test ./internal/ampapi/ ./cmd/amp-upstream/ ./internal/infrastructure/ ./internal/database/ -count=1`

## Phase 4: Testing, docs, finalize

- [x] **4.1** `scripts/release-gate.sh` целиком зелёный; лог в `evidence/release-gate.txt`. <!-- verify: tail evidence/release-gate.txt -->
- [x] **4.2** CHANGELOG `[Unreleased]`: Added (маршруты, колонка, индекс, метрика, backfill, `Warn` на INVALID) и Breaking (investigation path, `fingerprint` FNV + `amp_fingerprint`, `alert_name`; `/api/v2/inhibitions`; `?group_by=`; `global.group_*` — конфиг не загружается; `?status=resolved`; health-алиасы); Rollout: ручной `CREATE INDEX CONCURRENTLY` до выкатки на большой `alerts`, починка INVALID двумя командами, окно rolling update; F16 «no schema change». <!-- verify: grep -n "UPSTREAM-API-UI" CHANGELOG.md -->
- [x] **4.3** Planning: BACKLOG (`UPSTREAM-API-UI` [x], `STATUS-VERSIONINFO-CONTRACT`, `COMPAT-VERSION-CONFIG-KEY`, `HISTORY-API`, `UPSTREAM-DOCS-MIGRATION`, `PHASE-5A-…`), ROADMAP, DECISIONS (уточнение ADR-019), DONE, NEXT; у `HISTORY-API` — сдвиг `updated_at` backfill'ом (F8). <!-- verify: grep -n "UPSTREAM-API-UI" docs/06-planning/*.md -->
- [x] **4.4** Архив `tasks/archive/UPSTREAM-API-UI/`, коммит, sync с `main`. <!-- verify: git status && git log --oneline main..HEAD -->

## Definition of Done

- [x] All steps are complete or explicitly marked blocked/skipped
- [x] Success criteria from `requirements.md` are covered
- [x] Contracts from `Spec.md` are implemented or deviations are recorded
- [x] Deep review verdict is `pass`
- [x] Tests for changed behavior are added or updated
- [x] Phase checks pass
- [x] Docs/planning are updated
- [ ] **open (owner):** Владелец подтвердил решения research Q3–Q5 (удалённые `/api/v2/inhibitions`, `?status=resolved`, health-алиасы; class d в `divergences.tsv`) до `merge-to-main`

## Deviations

- 2.4: smoke нашёл 500 на строке с `starts_at = NULL` (колонка nullable, такие строки возможны от старых писателей). `AlertRecordBy*` читает `starts_at` как nullable, ответ опускает пустые времена; все времена ответа — UTC. В Spec § API `starts_at` — «если есть».
- Backfill пропускает строку с нечитаемыми labels (`Warn`), а не останавливается на ней навсегда.
- 3.3/3.4: PG-тесты лежат в `internal/infrastructure/postgres_alert_records_test.go`, а не в `internal/database/`: up/down/up обеих миграций там же, через goose provider. Тест 3.4 там же, потому что `ampapi` проверяется поверх настоящих ридеров. `EXPLAIN` запроса backfill на тестовой таблице (8 строк) — seq scan; на таком объёме это ожидаемо и ничего не говорит о F5 на большой таблице, там остаётся рассуждение (B-tree индексирует NULL).
