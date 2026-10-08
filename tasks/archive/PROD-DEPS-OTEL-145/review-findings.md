# Deep Review Findings: PROD-DEPS-OTEL-145

**Trigger classification:** mandatory (S)
**Reviewer perspective:** independent general-purpose agent (fresh context)
**Reviewed at:** 2026-10-08
**Reviewed tree:** bugfix/prod-deps-otel-145 @ d71d640
**Verdict:** pass (see `review-verdict.json`)

## Checks performed

- `git diff main...HEAD`: код меняется только в `go-app/go.mod` (+10/−10), `go-app/go.sum` (+21, только добавления), `CHANGELOG.md` (+1); остальное — артефакты задачи. Go-код не менялся.
- Возраст модулей (`go list -m -json <mod>@<ver>` → `Time`) на 2026-10-08, все изменённые в go.mod + появившийся только в go.sum:
  - `otel`, `otlptrace`, `otlptracegrpc`, `otel/sdk`, `otel/trace`, `otel/metric`, `otel/sdk/metric` v1.45.0 — 2026-08-03T19:26:24Z (66 дней);
  - `genproto/googleapis/{api,rpc}` v0.0.0-20260803160001-6ac0973c030d — 2026-08-03 (66 дней);
  - `go.opentelemetry.io/proto/otlp` v1.11.0 — 2026-07-22 (78 дней);
  - `github.com/go-logr/logr` v1.4.4 — 2026-07-20 (80 дней).
  Все вне 7-дневного карантина; неожиданных модулей в go.mod/go.sum нет (новые строки go.sum — только эти 11 модулей).
- `go mod verify` — all modules verified.
- `GOFLAGS=-mod=readonly go build ./... && GOFLAGS=-mod=readonly go vet ./...` — OK (хешей в go.sum хватает; Spec Edge Case 1 не воспроизводится).
- `GOFLAGS=-mod=readonly go test ./pkg/telemetry/...` — 14 passed.
- Advisory GO-2026-6505 (`vuln.go.dev/ID/GO-2026-6505.json`): затронуты `otlptrace`, `otlptracegrpc`, `otlptracehttp`, `exporters/zipkin`, `otel/sdk`; диапазон `introduced 1.5.0, fixed 1.45.0` ⇒ v1.45.0 — первая версия с фиксом во всех затронутых пакетах, которые использует модуль. `otlptracehttp` v1.19.0 есть только в go.sum, в граф сборки не входит.
- `govulncheck@v1.8.0 -show verbose ./...` (локально go1.27.1): Symbol Results — none; Package Results — none; Module Results — только GO-2026-5932 (`x/crypto/openpgp`, Fixed in: N/A).
- `GOTOOLCHAIN=go1.26.8 govulncheck@v1.8.0 ./...` (toolchain из go.mod): 0 достижимых, 0 в импортируемых пакетах, 1 в required-модулях — совпадает с go1.27.1; stdlib-находок на 1.26.8 нет.
- P4 по всему модулю:
  - `go list -test` (Imports, TestImports, XTestImports по `./...`) — `pkg/telemetry` не импортирует никто, кроме него самого;
  - grep по `*.go` (`AMP/pkg/telemetry`, `go.opentelemetry.io`) — вне `pkg/telemetry` только комментарии в `internal/infrastructure/publishing/tracing.go` и gitignored-кэш `go-app/.cache/go-mod`;
  - `go list -deps -test ./cmd/...`, в том числе с `-tags futureparity` и `-tags integration` — 0 пакетов opentelemetry/`pkg/telemetry`/otlptrace.
- Инварианты: `go 1.26.0` / `toolchain go1.26.8` не изменились; `grpc` v1.83.2, `protobuf` v1.36.11, `x/crypto` v0.56.0, `x/text` v0.41.0, `x/sync`, `x/sys`, `x/term`, `x/time` не изменились; из direct-зависимостей изменились только otel-модули; история go.mod — предыдущее изменение `4def6ad` (PROD-DEPS-VULN), его версии не откатываются.
- CI (`.github/workflows/ci.yml`, job `govulncheck`): `setup-go` с `go-version-file: go-app/go.mod` → либо go1.26.8 из `toolchain`, либо 1.26.x с переключением `GOTOOLCHAIN=auto` на go1.26.8 — тот же тулчейн, на котором прогон выше зелёный.
- `git status -s` по окончании ревью — пусто.

## Premises

- **P1** — выполняется (сверено по `git diff main...HEAD -- go-app/go.mod`); measured заслужен.
- **P2** — выполняется (даты совпали, включая `otel/sdk/metric` из go.sum); measured заслужен.
- **P3** — выполняется (verbose-режим, два тулчейна); measured заслужен.
- **P4** — выполняется и сильнее заявленного: в `cmd/...` нет ни одного пакета opentelemetry, в том числе с тестами и build-тегами; call-path-traced заслужен.
- **P5** — выполняется (`go build`, `go vet ./...`, `go test ./pkg/telemetry/...` в readonly); measured (сборка) корректен.
- **P6** — в Spec `assumed`; ревью проверило измерением (`GOTOOLCHAIN=go1.26.8` = go1.27.1). Остаточный риск — среда CI или обновление vuln DB между прогонами; закрывается job'ом на PR (шаг 5.3). Не блокер.

## Findings

Находок нет.

Наблюдения, сознательно не оформленные как находки:

- В CHANGELOG в списке транзитивных апгрейдов нет `otel/metric` v1.45.0 — покрыт формулировкой «OpenTelemetry Go modules bumped». Остальные утверждения записи верны.
- Устаревшие хеши в go.sum (v1.39.0, v1.44.0) безвредны: `go mod verify` и `-mod=readonly` проходят; та же практика, что в PROD-DEPS-VULN, долг уже учтён в `GO-MOD-TIDY-CHECK`.
- `pkg/telemetry/tracer.go:150-155` сам логирует `endpoint` в INFO — уже в Spec § Open Questions как follow-up `DEAD-PKG-TELEMETRY`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
