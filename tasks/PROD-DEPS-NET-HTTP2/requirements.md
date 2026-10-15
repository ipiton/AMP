---
id: PROD-DEPS-NET-HTTP2
slug: prod-deps-net-http2
stream: Security
type: bug
priority: critical
status: active
created_at: 2026-10-15
updated_at: 2026-10-15
---

# Requirements: net/http и x/net — GO-2026-6610…6617

## Problem Framing

- **Symptom:** required job `govulncheck` красный на `main` с 2026-10-10 (CI run 38047566510 и следующие). Пока он красный, красный любой PR, и включать branch protection (`MAIN-BRANCH-PROTECTION`) бессмысленно.
- **Root Cause:** advisories GO-2026-6610, -6611, -6612, -6613, -6617 от 2026-10-08 — HTTP/2 и HTTP/1 в `net/http`, в том числе crash HTTP/2-сервера из-за гонки HPACK-энкодера. Достижимы через `golang.org/x/net` v0.58.0 (фикс v0.60.0) и stdlib `go1.26.8` (фикс `go1.26.9`). AMP — HTTP-сервер (`/api/v2/alerts`), так что уязвимый путь — основной ingest.
- **Why Now:** первая P0 в BACKLOG § Production Readiness; карантин `x/net` v0.60.0 истекает 2026-10-15. Разблокирует Owner `MAIN-BRANCH-PROTECTION`.
- **How We Measure:** `govulncheck ./...` в `go-app` — 0 достижимых уязвимостей с фиксом; `scripts/release-gate.sh` зелёный; job `govulncheck` на `main` после push зелёный.

## Risk Profile

- **Signals:** `S`
  - `S` — supply chain: апгрейд зависимости и toolchain, закрытие security advisory.
- **Tier:** Full
- **Notes:** дифф малый (`go.mod`/`go.sum`, две строки `FROM` в Dockerfile), но `S` по Step Matrix даёт Full ⇒ `deep-review` обязателен. `R` не ставлю: patch-релиз toolchain и `x/net` без изменения поведения AMP; деплоя в AMP нет, запись в `CHANGELOG.md` `[Unreleased]` — в `finalize`. `X` не ставлю: Dockerfile и `go.mod` — одна граница сборки.

## User Stories

1. Как владелец репозитория, я хочу зелёный required `govulncheck` на `main`, чтобы мержить по зелёному CI и включить branch protection.
2. Как оператор AMP, я хочу, чтобы HTTP/2-сервер AMP не падал от гонки HPACK-энкодера и не был уязвим к известным атакам на `net/http`.

## Success Criteria

- [ ] `toolchain go1.26.9` в `go-app/go.mod`; `go` directive не меняется без необходимости.
- [ ] `golang.org/x/net` v0.58.0 → v0.60.0 точечным `go get` (+ неизбежная транзитивка); дата публикации каждого поднятого модуля проверена против карантина 7 дней.
- [ ] `Dockerfile` и `Dockerfile.config-reloader`: builder `golang:1.26.9-alpine`; комментарий про Alpine-ветку остаётся верным; `ci.yml:36` и `docs/CI.md` § Go Version — тоже `1.26.9`.
- [ ] CI берёт `go1.26.9`: подтверждено, что `actions/setup-go` с `go-version-file` учитывает `toolchain`, а не только `go`.
- [ ] `govulncheck ./...` в `go-app` — 0 достижимых уязвимостей с фиксом (baseline на `go1.26.8` — 12 достижимых, `evidence/`) (недостижимые без фикса, например GO-2026-5932 в `x/crypto`, — перечислены).
- [ ] `scripts/release-gate.sh` зелёный (флейки — перепрогон и запись).
- [ ] Запись в `CHANGELOG.md` `[Unreleased]` (Security).

## Non-Goals

- `x/net` v0.61.0 (2026-10-09) и дальше — только если v0.60.0 не закрывает advisory; карантин v0.61.0 до 2026-10-16.
- Переход на Go 1.27.
- `go mod tidy` целиком (`GO-MOD-TIDY-CHECK`), `schedule:` для `govulncheck` (`CI-SUPPLY-CHAIN`).
- Включение branch protection — действие владельца (`MAIN-BRANCH-PROTECTION`).

## Constraints

- **Scope:** `go-app/go.mod`, `go-app/go.sum`, `Dockerfile`, `Dockerfile.config-reloader`, `CHANGELOG.md`; правки Go-кода — только если без них не собирается, отдельным коммитом.
- **Security:** карантин 7 дней. `x/net` v0.60.0 — `2026-10-08T17:22:58Z` по proxy.golang.org ⇒ `go get` не раньше **2026-10-15 17:23 UTC**. `go1.26.9` — `2026-10-02T20:28Z` (toolchain module), проходит. Тег `golang:1.26.9-alpine` перепушен 2026-10-10 — **исключение по решению владельца 2026-10-15** (перепуск официального образа ≠ новый релиз; Go 1.26.9 карантин прошёл), см. `research.md` § Decision.
- **Compatibility:** patch-релизы — breaking API не ожидается. Известные флейки (`GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`, `PUBLISHING-WARMUP-TEST-FLAKY`) не чинить, при падении перепрогнать и зафиксировать.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-DEPS-VULN/` (`x/net` v0.47.0 → v0.58.0), `tasks/archive/PROD-DEPS-OTEL-145/` (тот же сценарий: required `govulncheck` красный, точечный апгрейд, Full по `S`).
- Relevant patterns: `go get golang.org/x/net@v0.60.0` без `go get -u ./...`; `go get toolchain@go1.26.9` или ручная правка `toolchain`.
- Duplicates: в `tasks/`, `tasks/archive/`, `DONE.md`, `archive/DONE-*.md` — нет.
- Open unknowns: закрыты в `research.md` (2026-10-15).
