---
id: PROD-DEPS-OTEL-145
slug: prod-deps-otel-145
stream: Security
type: bug
status: draft
created_at: 2026-10-08
updated_at: 2026-10-08
based_on:
  - requirements.md
---

# Specification: OpenTelemetry v1.45.0 (GO-2026-6505)

**Version:** 1.0  
**Status:** Draft

## Summary

Поднять OpenTelemetry-модули `go-app` с v1.44.0 до v1.45.0, чтобы закрыть GO-2026-6505 и вернуть зелёный required job `govulncheck`. Изменение — только `go.mod`/`go.sum`, без правок Go-кода.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| otel-модули согласованно на v1.45.0, карантин проверен | Target Design, Design Premises P1–P2, `evidence/spec-trial-go.mod.diff` |
| `govulncheck ./...` — 0 достижимых | Design Premises P3; повторный прогон в `testing` |
| `scripts/release-gate.sh` зелёный | `testing` |
| `CHANGELOG.md` `[Unreleased]` (Security) | Component Architecture, `finalize` |
| CI на PR: `govulncheck` зелёный | Rollout |

## Current State

- **Code:** `go-app/pkg/telemetry/tracer.go` — единственный пользователь OTLP-экспортёра (`otlptracegrpc.WithEndpoint` → `otlptrace.New`, `:102-118`). Пакет не импортируется ни одним другим пакетом модуля, бинари `cmd/...` его не содержат (P4).
- **Data:** not applicable.
- **Tests:** `go-app/pkg/telemetry/*_test.go` (компиляция и базовое поведение tracer'а); полный набор — `scripts/release-gate.sh`.
- **Docs:** `CHANGELOG.md` `[Unreleased]`; `docs/CI.md` — `govulncheck` уже required, правок не требует.
- **Deps (`go-app/go.mod`):** direct `otel`, `otlptrace`, `otlptracegrpc`, `otel/sdk`, `otel/trace` v1.44.0; indirect `otel/metric` v1.44.0, `otel/proto/otlp` v1.10.0; `go 1.26.0`, `toolchain go1.26.8`.

## Design Premises

| # | Premise | Confirmed by | Class | If wrong |
|---|---|---|---|---|
| P1 | `go get` пяти direct otel-модулей @v1.45.0 меняет только otel-семейство + `go-logr/logr` v1.4.4, `otel/proto/otlp` v1.11.0, `genproto/googleapis/{api,rpc}` @20260803; `grpc`, `x/*`, `go`/`toolchain` не меняются | пробный `go get` 2026-10-08, дифф `evidence/spec-trial-go.mod.diff`, откатан | measured | дифф шире — пересмотреть минимальность, проверить даты новой транзитивки |
| P2 | Все новые версии старше 7 дней: otel* 2026-08-03, genproto 2026-08-03, otlp proto 2026-07-22, logr 2026-07-20 | `go list -m -json <mod>@<ver>` → `Time`, 2026-10-08 | measured | взять предыдущую версию модуля или зафиксировать исключение |
| P3 | После апгрейда `govulncheck ./...` — 0 достижимых уязвимостей, 1 недостижимая в required-модулях (ожидаемо GO-2026-5932, `x/crypto/openpgp`, без фикса) | пробный прогон `govulncheck@v1.8.0` на апгрейженном дереве, 2026-10-08 (локально go1.27.1; предупреждения о версии source-processing — шум) | measured | искать новую advisory, при необходимости v1.46.0 |
| P4 | `pkg/telemetry` не импортируется ни одним пакетом модуля; `go list -deps ./cmd/...` его не содержит ⇒ GO-2026-6505 не попадает в поставляемые бинари, govulncheck считает путь достижимым через экспортированный `NewTracer` | `go list -f '{{.Imports}}' ./...`, `go list -deps ./cmd/...` по всему модулю | call-path-traced | если бы бинари его тянули — уязвимость реально эксплуатируема в проде, приоритет выше, но решение то же |
| P5 | v1.45.0 — минорный релиз внутри v1 без breaking API для используемых символов | `go build ./...` на апгрейженном дереве — OK | measured (сборка) | правки кода отдельным коммитом |
| P6 | CI (`setup-go` с `go-version-file: go-app/go.mod`) прогонит govulncheck на go1.26.x и получит тот же результат, что локальный прогон на go1.27.1 | — | assumed | см. Rollout: результат подтверждается job'ом на PR |

## Target Design

Точечный `go get` пяти direct otel-модулей до v1.45.0; Go сам поднимает согласованные indirect (`otel/metric`, `otel/proto/otlp`, genproto, `logr`). `go mod tidy` не запускается (уже известно, что он вносит несвязанные изменения — `GO-MOD-TIDY-CHECK`). Код не меняется. Проверка — `go build`, `govulncheck`, затем полный `scripts/release-gate.sh`. Запись в `CHANGELOG.md` `[Unreleased]` под Security. Альтернатива «v1.47.0 последняя» отклонена: шире дифф без нужды, требование — минимальная fix-версия.

## API Contracts

Not applicable.

## Data Model / Migrations

Not applicable.

## Component Architecture

- `go-app/go.mod`, `go-app/go.sum` — версии otel-семейства и транзитивки (P1).
- `CHANGELOG.md` — строка в `[Unreleased]` (Security): GO-2026-6505, otel v1.44.0 → v1.45.0.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation — not applicable.
- [x] Sensitive data not logged — суть фикса: upstream перестаёт писать URL эндпоинта в INFO. Собственный `Logger.Info(..., "endpoint", config.Endpoint)` в `pkg/telemetry/tracer.go:150-153` остаётся; значение — `host:port` для gRPC, а пакет мёртвый (P4) — в scope не берём, см. Open Questions.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC — not applicable.
- [x] Supply chain — все новые версии вне 7-дневного карантина (P2).

## Invariants

- [ ] `go`/`toolchain` в `go.mod` не меняются.
- [ ] `grpc`, `x/crypto`, `x/net`, `x/text` остаются на версиях из `PROD-DEPS-VULN`.
- [ ] Ни одна не-otel direct-зависимость не меняется.
- [ ] Go-код не меняется.

## Edge Cases

1. `go.sum` в CI не содержит хешей для `otel/sdk/metric` (скачивался при `go get`, но в `go.mod` не попал) → `go build`/`go test` с `-mod=readonly` в release-gate это покажет; лечится `go mod download`/правкой `go.sum` в том же коммите.
2. govulncheck в CI на go1.26.x находит что-то иное, чем локальный на go1.27.1 (P6) → разбирать по выводу job'а; stdlib-находки — отдельное решение, не в этой задаче.
3. Флейки `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` / `PUBLISHING-WARMUP-TEST-FLAKY` в release-gate → перепрогон, фиксация в `testing`.

## Impact Analysis

- **Affected modules:** `go-app` (только граф зависимостей).
- **Breaking changes:** none.
- **New dependencies:** none (только версии существующих).
- **Risks:** минимальные; поведение трейсинга в бинарях не меняется, т. к. они его не содержат (P4).

## Rollout / Rollback

- **Rollout:** PR в `main`; критерий — зелёные `gate` и `govulncheck` (закрывает assumed P6). Деплоя в AMP нет; в образ попадёт с ближайшим релизом.
- **Rollback:** revert коммита с `go.mod`/`go.sum`.
- **Feature flag:** not applicable.

## Observability

- **Logs:** upstream перестаёт логировать URL эндпоинта экспортёра; в бинарях AMP не наблюдаемо (P4).
- **Metrics:** not applicable.
- **Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** `S` (supply chain).
- **Discretionary triggers present:** none.
- **Decision:** required. Объём ревью соответствует диффу: проверка списка модулей/версий против P1–P2, инвариантов и вывода govulncheck.

## Open Questions

- [ ] **P6 (assumed):** совпадёт ли результат govulncheck в CI (go1.26.x) с локальным — закрывается job'ом на PR.
- [ ] **Follow-up, не в этой задаче:** `go-app/pkg/telemetry` — мёртвый пакет (нет импортёров), при этом тянет OTLP/gRPC-экспортёр в граф govulncheck и сам логирует endpoint в INFO. Удаление сняло бы этот класс красных `govulncheck` для кода, которого нет в бинарях. Предложить в `finalize` как TECH-DEBT/BACKLOG (`DEAD-PKG-TELEMETRY`), решение — за владельцем.
