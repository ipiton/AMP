---
id: PROD-DEPS-NET-HTTP2
slug: prod-deps-net-http2
stream: Security
type: bug
status: draft
created_at: 2026-10-15
updated_at: 2026-10-15
based_on:
  - requirements.md
  - research.md
---

# Specification: net/http и x/net — GO-2026-6603…6617

**Version:** 1.0  
**Status:** Draft

## Summary

Поднять Go `toolchain` до `go1.26.9` и `golang.org/x/net` до v0.60.0, чтобы закрыть 12 достижимых уязвимостей (`net/http`, `net/textproto`, `crypto/tls`, `html/template`, `x/net/http2`) и вернуть зелёный required `govulncheck` на `main`. Версию Go во всех шести местах репозитория меняем одним коммитом.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `toolchain go1.26.9`, `go` не меняется | Component Architecture: `go-app/go.mod`; P3 |
| `x/net` v0.60.0 точечным `go get`, даты транзитивки | Component Architecture; P2; Rollout шаг 1–2 |
| Builder `golang:1.26.9-alpine`, `ci.yml` и `docs/CI.md` | Component Architecture; P5, P6 |
| CI берёт `go1.26.9` | P4 |
| `govulncheck` — 0 достижимых | P1; Verification |
| `scripts/release-gate.sh` зелёный | Verification |
| `CHANGELOG.md` `[Unreleased]` Security | Component Architecture (в `finalize`) |

## Current State

- **Code:** `go-app/go.mod:3,5` — `go 1.26.0`, `toolchain go1.26.8`; `golang.org/x/net v0.58.0 // indirect` (`go.mod:147`), прямых импортов `golang.org/x/net` в `*.go` нет (`git grep`). Direct: `x/crypto` v0.56.0, `x/text` v0.41.0.
- **Build:** `Dockerfile:7`, `Dockerfile.config-reloader:12` — `golang:1.26.8-alpine`; `Dockerfile:38` — комментарий «Same Alpine branch as the golang:1.26.8-alpine builder», runtime `alpine:3.24`.
- **CI:** `.github/workflows/ci.yml` — `setup-go` v7 `go-version-file: go-app/go.mod` во всех Go-job'ах, комментарий `:36` с `go1.26.8`; `govulncheck@v1.8.0 ./...`.
- **Docs:** `docs/CI.md:32` § Go Version — `go1.26.8` трижды.
- **Tests:** поведенческих тестов на версию нет; проверка — `govulncheck` и release-gate.
- **Data:** not applicable.

## Design Premises

| # | Premise | Confirmed by | Class | If wrong |
|---|---|---|---|---|
| P1 | Все 12 достижимых находок на текущем дереве закрываются `go1.26.9` + `x/net` v0.60.0, других фиксов нет | `govulncheck@v1.8.0` на `GOTOOLCHAIN=go1.26.8`, `evidence/govulncheck-baseline-go1.26.8.txt` (2026-10-15); поле `Fixed in` у всех 12 | measured | после апгрейда остаётся находка — разобрать отдельно, не расширять апгрейд вслепую |
| P2 | `go get golang.org/x/net@v0.60.0` меняет только `x/net`, `x/crypto`→v0.57.0, `x/sys`→v0.48.0, `x/term`→v0.46.0, `x/text`→v0.42.0 и, возможно, `x/sync`→v0.23.0, `x/tools`→v0.49.0, `x/mod`→v0.41.0; все опубликованы ≤ 2026-09-08 | `.mod` файлы на proxy.golang.org, сопоставление с `go list -m` текущего графа; пробный `go get` не делался — `x/net` v0.60.0 в карантине до 17:23 UTC | code-read | дифф шире — проверить дату каждого нового модуля; моложе 7 дней — стоп, вопрос владельцу |
| P3 | `x/net` v0.60.0 и цепочка требуют `go 1.26.0` — директива `go` не поднимается | `.mod` v0.60.0: `go 1.26.0` | code-read | `go get` поднимет `go` — допустимо в пределах 1.26.x, отметить в CHANGELOG |
| P4 | CI берёт версию из `toolchain`, а не из `go` | комментарий `ci.yml:36`; red run на `main` показывает `net/http@go1.26.8` при `go 1.26.0` | measured | CI останется на старой версии — правка `ci.yml` на `go-version` |
| P5 | В официальном `golang:*-alpine` стоит `GOTOOLCHAIN=local` ⇒ без бампа `FROM` образ молча собирается на версии образа | `docker-library/golang` `1.26/alpine3.24/Dockerfile:121` | code-read | не влияет на решение: `FROM` всё равно поднимаем |
| P6 | `golang:1.26.9-alpine` — Go 1.26.9 на Alpine 3.24, `Dockerfile:38` остаётся верным | Docker Hub tags (`1.26.9-alpine` и `1.26.9-alpine3.24` один пуш 2026-10-10), `docker-library` `ENV GOLANG_VERSION 1.26.9` | code-read | поправить комментарий / runtime-образ |
| P7 | Patch-релизы без breaking API для кода AMP | семантика patch/minor-релизов `x/*`, прямых импортов `x/net` нет | assumed | правки кода отдельным коммитом; риск — в Rollout |

## Target Design

1. В `go-app`: `go get golang.org/x/net@v0.60.0` (не раньше 2026-10-15 17:23 UTC) и `toolchain go1.26.9` (`go get toolchain@go1.26.9` или правка строки). Никаких `-u`, никакого `go mod tidy`.
2. Сверить дельту `go.mod` с P2; даты всех новых версий записать в `evidence/`.
3. Заменить `1.26.8` → `1.26.9` в `Dockerfile` (две строки), `Dockerfile.config-reloader`, комментарии `ci.yml:36`, `docs/CI.md:32`.
4. Проверить: `govulncheck` на `go1.26.9` — 0 достижимых; release-gate; сборка обоих образов.

Одно изменение версий, без правок Go-кода. Образ — по исключению из карантина (`research.md` § Decision).

## API Contracts

Not applicable — контрактов не меняем.

## Data Model / Migrations

Not applicable.

## Component Architecture

- `go-app/go.mod`, `go-app/go.sum` — `toolchain go1.26.9`, `x/net` v0.60.0 + транзитивка по P2.
- `Dockerfile` — `FROM golang:1.26.9-alpine`, комментарий строки 38.
- `Dockerfile.config-reloader` — `FROM golang:1.26.9-alpine`.
- `.github/workflows/ci.yml` — только комментарий `:36`.
- `docs/CI.md` — § Go Version.
- `CHANGELOG.md` `[Unreleased]` → Security — в `finalize`: 12 ID, `go1.26.9`, `x/net` v0.60.0.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation — не меняется; фиксы stdlib ужесточают разбор HTTP/1, HTTP/2, MIME и Range-заголовков.
- [x] Sensitive data not logged — логирование не меняется.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC — not applicable.
- [ ] Supply chain: карантин 7 дней для модулей Go — строго (`x/net` не раньше 17:23 UTC 2026-10-15, каждая новая версия в `go.sum` — с датой); исключение только для официального образа `golang:1.26.9-alpine` (решение владельца 2026-10-15).

## Invariants

- [ ] Директива `go 1.26.0` не меняется (иначе — явная запись).
- [ ] Версия Go одна во всех шести местах (`git grep 1.26.8` вне planning/архива/CHANGELOG пуст).
- [ ] Никаких модулей вне цепочки `x/net` в дельте `go.mod`.
- [ ] Ровно один ожидаемый невызываемый модульный finding (GO-2026-5932) и тот же «1 в импортируемых пакетах», что в baseline.

## Edge Cases

1. `go get` поднимает модуль вне таблицы P2 → проверить дату; моложе 7 дней — стоп.
2. Локальный Go 1.27.1 новее toolchain → все проверки с `GOTOOLCHAIN=go1.26.9` (как велит `docs/CI.md`).
3. После апгрейда `govulncheck` находит новую advisory (опубликована после 2026-10-15) → не чинить в этой задаче, если фикс в карантине; завести P0 и зафиксировать.
4. Известные флейки release-gate (`GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`, `PUBLISHING-WARMUP-TEST-FLAKY`) → перепрогон, запись.

## Impact Analysis

- **Affected modules:** весь модуль `go-app` (stdlib), сборка образов `amp` и `amp-config-reloader`, CI.
- **Breaking changes:** none.
- **New dependencies:** none (только версии существующих).
- **Risks:** P7 (patch без breaking) — проверка `go build`/тестами в release-gate; исключение из карантина для образа — принято владельцем.

## Rollout / Rollback

- **Rollout:** мерж в `main`; push — решение владельца; подтверждение — зелёный `govulncheck` на `main` в CI. После этого разблокируется Owner `MAIN-BRANCH-PROTECTION`. Деплоя/релиза нет.
- **Rollback:** `git revert` коммита версий (возвращает уязвимое состояние; только при поломке сборки/тестов).
- **Feature flag:** not applicable.
- **Риск `assumed` P7:** если тесты падают на новой версии — правка кода отдельным коммитом или стоп с вопросом.

## Observability

Not applicable — логи, метрики и алерты не меняются.

## Deep Review

- **Mandatory triggers present:** `S` (supply chain).
- **Discretionary triggers present:** none (дифф ожидается < 50 строк вне `go.sum`).
- **Decision:** required.

## Open Questions

- [ ] P2 (`code-read`) — точный набор транзитивки подтверждается только реальным `go get` после 17:23 UTC; проверяется в implement.
- [ ] P7 (`assumed`) — отсутствие breaking API подтверждается release-gate.
