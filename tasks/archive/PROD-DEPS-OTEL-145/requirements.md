---
id: PROD-DEPS-OTEL-145
slug: prod-deps-otel-145
stream: Security
type: bug
priority: critical
status: complete
created_at: 2026-10-08
updated_at: 2026-10-08
---

# Requirements: OpenTelemetry v1.45.0 (GO-2026-6505)

## Problem Framing

- **Symptom:** required job `govulncheck` красный на `main` с 2026-10-05 (два прогона подряд, остальные job'ы зелёные). Пока он красный, красный любой PR, и включать branch protection (`MAIN-BRANCH-PROTECTION`) бессмысленно.
- **Root Cause:** GO-2026-6505 — OpenTelemetry-Go пишет URL эндпоинта экспортёра в INFO-лог при конфигурации. Затронуты `go.opentelemetry.io/otel/exporters/otlp/otlptrace`, `.../otlptracegrpc` и `go.opentelemetry.io/otel/sdk` v1.44.0; фикс — v1.45.0. Достижимо из `go-app/pkg/telemetry/tracer.go:102-118` (`otlptracegrpc.WithEndpoint` → `otlptrace.New`). Advisory вышла после `PROD-DEPS-VULN` (2026-09-29), который поднял otel до v1.44.0.
- **Why Now:** P0 в BACKLOG § Production Readiness; первая незаблокированная позиция (`HELM-DEFAULTS-VALIDATE` ждёт решения владельца). Разблокирует `MAIN-BRANCH-PROTECTION`.
- **How We Measure:** `govulncheck ./...` в `go-app` — 0 достижимых уязвимостей; `scripts/release-gate.sh` зелёный; job `govulncheck` в CI на PR зелёный.

## Risk Profile

- **Signals:** `S`
  - `S` — supply chain: апгрейд зависимостей, закрытие security advisory.
- **Tier:** Full
- **Notes:** дифф ожидается малым (`go.mod`/`go.sum`), но `S` по правилу Step Matrix даёт Full ⇒ `deep-review` обязателен. `R` не ставлю: поведение трейсинга не меняется, кроме того, что URL эндпоинта больше не логируется; деплоя в AMP нет, запись в `CHANGELOG.md` `[Unreleased]` — в рамках `finalize`.

## User Stories

1. Как владелец репозитория, я хочу зелёный required `govulncheck` на `main`, чтобы PR снова можно было мержить по зелёному CI и включить branch protection.
2. Как оператор AMP, я хочу, чтобы URL OTLP-эндпоинта (может содержать креды/внутренние хосты) не попадал в INFO-лог.

## Success Criteria

- [x] `go.opentelemetry.io/otel`, `otel/sdk`, `otel/trace`, `otel/metric`, `otlptrace`, `otlptracegrpc` подняты согласованно до v1.45.0 (+ неизбежная транзитивка); дата публикации проверена против карантина 7 дней.
- [x] `govulncheck ./...` в `go-app` — 0 достижимых уязвимостей (недостижимые без фикса, например GO-2026-5932 в `x/crypto`, — допустимы и перечислены).
- [x] `scripts/release-gate.sh` зелёный (или красный только из-за известных флейков, перепрогон зафиксирован).
- [x] Запись в `CHANGELOG.md` `[Unreleased]` (Security).
- [~] CI на PR: job `govulncheck` зелёный. _(PR не открывался по решению владельца; эквивалент — `govulncheck` на `go1.26.8` в deep-review; подтверждение — CI на `main` после push)_

## Non-Goals

- `schedule:` для `govulncheck` — это `CI-SUPPLY-CHAIN`.
- `go mod tidy` целиком — это `GO-MOD-TIDY-CHECK`; здесь не смешиваем с security-диффом.
- Апгрейд otel дальше v1.45.0 (есть v1.46.0/v1.47.0) — только если v1.45.0 не собирается или тянет новую advisory.
- Включение branch protection — действие владельца (`MAIN-BRANCH-PROTECTION`).

## Constraints

- **Scope:** `go-app/go.mod`, `go-app/go.sum`; правки Go-кода — только если без них не собирается, отдельным коммитом.
- **Security:** карантин 7 дней — v1.45.0 опубликован 2026-08-03 (proxy.golang.org `.info`, все четыре модуля одним тегом `93a693e`), проходит. Транзитивку проверить так же.
- **Compatibility:** `go`/`toolchain` не менять, если апгрейд не требует. Минорный patch-релиз внутри v1 — breaking API не ожидается. Известные флейки `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` и `PUBLISHING-WARMUP-TEST-FLAKY` не чинить, при падении перепрогнать и зафиксировать.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-DEPS-VULN/` (otel v1.39.0 → v1.44.0, вариант без правок кода; otel требовался согласованным с grpc ≥ 1.83.1).
- Relevant patterns: `go get <mod>@v1.45.0` по списку otel-модулей, без `go get -u ./...`.
- Duplicates: в `tasks/`, `tasks/archive/`, `DONE.md`, `archive/DONE-*.md` — нет (есть только родительский `PROD-DEPS-VULN`, закрыт 2026-09-29).
- Open unknowns: требует ли otel v1.45.0 более свежий grpc/genproto/`otel/proto/otlp` и не притащит ли транзитивка что-то моложе 7 дней.

## Research (mini)

- Source: `go-app/go.mod:28-32,141-142`; proxy.golang.org `go.opentelemetry.io/otel*/@v/v1.45.0.info`; BACKLOG § P0 `PROD-DEPS-OTEL-145`.
- Key finding:
  - Все затронутые модули — один релиз-тег upstream, поднимаются вместе; v1.45.0 от 2026-08-03 — вне карантина.
  - Уязвимый путь — только `pkg/telemetry/tracer.go` (OTLP gRPC exporter).
- Decision: точечный `go get` otel-модулей до v1.45.0, проверка транзитивки по датам, `govulncheck` + release-gate.
