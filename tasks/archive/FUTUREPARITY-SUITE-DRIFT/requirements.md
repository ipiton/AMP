# Requirements: FUTUREPARITY-SUITE-DRIFT

## Context
После `ALERTMANAGER-REPLACEMENT-SCOPE` historical wide-surface suites в `go-app/cmd/server` были вынесены под build tag `futureparity`, чтобы больше не определять default active-runtime contract. Сейчас этот opt-in path уже не компилируется: `go-app/cmd/server/main_phase0_contract_test.go` по-прежнему ссылается на удаленные или перемещенные helpers `runtimeStateFileEnv`, `registerRoutes` и `configSHA256`.

Очередь в `docs/06-planning/NEXT.md` пустая, поэтому эта задача взята как следующий узкий code/test follow-up из `docs/06-planning/BUGS.md` после recent docs/UI cleanup.

## Goals
- [x] Убрать helper drift, из-за которого `futureparity` suite перестал собираться.
- [x] Зафиксировать для historical `futureparity` path понятный и поддерживаемый contract относительно текущего active runtime.
- [x] Сохранить active-runtime-first narrative и не расширить задачу в runtime surface restoration.

## Constraints
- Перед реализацией нужен `/research`: есть несколько разумных путей (восстановить helpers, адаптировать tests к новым owners или сузить suite), и ошибка лежит на границе между historical tests и текущим bootstrap/router contract.
- После `/research` нужен отдельный `/spec`, потому что задача влияет на verification model и на то, как repo трактует future parity относительно active runtime.
- Нельзя ломать default non-tagged `go test ./cmd/server`, текущие `cmd/server` handlers и active-runtime truth, зафиксированный в `docs/06-planning/DECISIONS.md`.
- Если выяснится, что green `futureparity` требует возврата широкого runtime surface, это нужно вынести в отдельный follow-up, а не silently расширять текущий slice.
- Full repo matrix уже известна как red вне этого scope (`REPO-TEST-MATRIX-RED`), поэтому verification должен быть targeted и явно ограниченным.

## Success Criteria (Definition of Done)
- [x] Для `futureparity` suite определен и документирован воспроизводимый targeted verification path.
- [x] Drift по `runtimeStateFileEnv`, `registerRoutes`, `configSHA256` устранен или заменен на явно зафиксированные актуальные эквиваленты.
- [x] `futureparity` path больше не падает на текущем helper drift как минимум на compile/targeted test уровне.
- [x] Task artifacts и planning files отражают выбранный подход и любые остаточные gaps.

## Verified Outcome (2026-03-09)
- Build-tagged compatibility owner реализован в `go-app/cmd/server/futureparity_compat.go`; missing helper/env/bootstrap symbols больше не берутся из production `main.go`.
- Reproducible acceptance path для этого slice зафиксирован как compile gate + tagged smoke gate для harness registration и `configSHA256`.
- Non-tagged `go test ./cmd/server` остается green на compile/test уровне.
- Full `go test ./cmd/server -tags=futureparity -count=1` осознанно остается вне Definition of Done и классифицирован отдельно как residual historical/runtime gap.
