# Implementation Checklist: FUTUREPARITY-SUITE-DRIFT

## Research & Spec
- [x] Завершен `research.md` по compile drift, historical suite ownership и active-runtime boundary.
- [x] Подготовлен `Spec.md` с направлением `futureparity = dedicated compatibility harness`, а не runtime restoration.

## Vertical Slices
- [x] **Slice A: Build-Tagged Compatibility Owner + Compile Gate** — вернуть missing helper/env/bootstrap symbols в `futureparity`-owned layer и сделать `go test ./cmd/server -tags=futureparity -run TestDoesNotExist` green без изменений production `main.go`.
- [x] **Slice B: Harness Smoke + Residual Gap Classification** — добавить узкие tagged smoke tests для compatibility harness, затем явно зафиксировать, что осталось helper drift, а что остается runtime-surface gap вне этого slice.

## Implementation
- [x] Шаг 1: Выбрать и создать build-tagged file(s) в `go-app/cmd/server`, которые будут owner-ом для historical compatibility symbols.
- [x] Шаг 2: Вернуть в этом build-tagged слое минимально необходимый helper/env contract для historical suite:
  - `runtimeStateFileEnv`
  - `configSHA256(...)`
  - `runtimeClusterListenAddressEnv`
  - `runtimeClusterAdvertiseAddressEnv`
  - `runtimeClusterNameEnv`
- [x] Шаг 3: Реализовать `registerRoutes(mux)` как `futureparity` compatibility entrypoint, сохранив старый test seam и не возвращая ownership в production `main.go`.
- [x] Шаг 4: Если для `registerRoutes(mux)` потребуется дополнительный compatibility wiring, собрать его за internal helper functions build-tagged слоя, а не через массовый rewrite existing tests.
- [x] Шаг 5: Не расширять active `application.Router` и non-tagged `cmd/server` path ради прохождения historical suite; любые missing wide-surface routes трактовать как compatibility/runtime gap, а не как обязательную часть этого slice.
- [x] Шаг 6: Если `configSHA256` удобнее извлечь в shared helper, делать это только при действительно более чистом diff; в противном случае оставить helper локальным для `futureparity`.

## Write Tests
- [x] Добавить build-tagged smoke test на compatibility harness/mux registration.
- [x] Добавить build-tagged smoke test на deterministic behavior `configSHA256`.
- [ ] Если появится отдельный compatibility builder/helper, покрыть его focused test вместо попытки сразу раззеленить весь historical suite.

## Testing
- [x] Прогнать обязательный compile gate:
  - `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist`
- [x] Прогнать обязательный targeted smoke gate:
  - `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run 'TestFutureParityHarness|TestFutureParityConfigHash'`
- [x] Прогнать regression guard для active path:
  - `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -run TestDoesNotExist`
- [ ] Если touched code затронет `internal/application`, добрать targeted active-contract tests по месту изменения.
- [x] Прогнать `git diff --check`.
- [x] Не подменять acceptance полным `go test ./cmd/server -tags=futureparity` или full repo matrix; если они остаются red, явно описать это как residual gap.

## Write Doc
- [x] Синхронизировать `requirements.md`, если фактическая реализация сузит результат до compile-only или, наоборот, даст чуть более сильный smoke path.
- [x] Синхронизировать `Spec.md`, если final compatibility owner окажется уже, чем предполагалось.
- [x] На `/write-doc` обновить `docs/06-planning/BUGS.md` или другие planning artifacts, если residual problem после implementation нужно переформулировать из helper drift в runtime-surface gap.
- [x] Зафиксировать в task artifacts, что именно закрыто этим slice:
  - compile/harness drift;
  - и что остается вне scope.

## Expected End State
- [x] `futureparity` suite снова имеет явный build-tagged owner для historical helper/env layer.
- [x] `go test ./cmd/server -tags=futureparity -run TestDoesNotExist` проходит из `go-app/`.
- [x] В репозитории есть узкий smoke verification path для compatibility harness.
- [x] Non-tagged `cmd/server` compile path не ломается.
- [x] Planning/task docs больше не смешивают “helper drift fixed” и “wide runtime surface restored”.

## Testing Result
- [x] Green acceptance checks:
  - `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist -count=1`
  - `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run 'TestFutureParityHarness|TestFutureParityConfigHash' -count=1`
  - `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -count=1`
  - `git diff --check`
- [x] Diagnostic non-acceptance run:
  - `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -count=1`
- [x] Diagnostic run остается red уже не на helper drift, а на residual historical/runtime mismatch:
  - stale expectations для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` все еще ждут `500`, хотя после `UI-PLACEHOLDER-REMOVAL` они честно отдают `200`;
  - historical wide-surface tests по-прежнему ждут отсутствующие routes вроде `/api/v2/status`, `/api/v2/config*`, `/history*`, `/-/reload`, `/api/v1/alerts`, `/api/v2/receivers`, `/api/v2/alerts/groups`, `/api/dashboard/*`, `/webhook`, `/debug/pprof/`, `/script.js`;
  - часть historical health expectations все еще предполагает `healthy`, тогда как compatibility runtime может честно возвращать `degraded`;
  - один subtest упирается в sandbox/network limitation `httptest.NewServer` (`listen tcp6 [::1]:0: bind: operation not permitted`), поэтому это не нужно трактовать как helper regression.

## Open Assumptions
- [x] Предполагается, что старый seam `newPhase0TestMux -> registerRoutes(mux)` можно сохранить, не переписывая массово existing historical tests.
- [x] Предполагается, что для mergeable slice достаточно compile gate + harness smoke, а не полного green historical suite.
- [x] Предполагается, что build-tagged compatibility layer можно собрать без возврата wide surface в active runtime.

## Documentation Result
- [x] `requirements.md` и `Spec.md` синхронизированы с фактическим outcome: build-tagged compatibility owner + targeted smoke acceptance.
- [x] `docs/06-planning/BUGS.md` больше не держит missing helper symbols как open problem; residual full-suite red вынесен в отдельный `FUTUREPARITY-HISTORICAL-RUNTIME-GAP`.

## Final Status
- [x] Задача закрывается как `compile/harness` slice для historical `futureparity`, а не как full parity restoration.
- [x] Planning files обновлены: `NEXT.md` очищен от WIP задачи, `DONE.md` получил final entry, residual mismatch остается explicit в `BUGS.md`.
- [x] Workspace архивирован в `tasks/archive/FUTUREPARITY-SUITE-DRIFT/`.

## Blockers / Stop Conditions
- [ ] Если для green compile/smoke придется расширять active `main.go` или `application.Router`, остановиться и не размывать active-runtime-first truth.
- [ ] Если `futureparity` требует большого runtime route restoration (`status`, `config`, `history`, `reload`) уже на этом шаге, зафиксировать это как отдельный follow-up вместо скрытого расширения текущего slice.
- [ ] Если новые helper symbols начинают использоваться non-tagged production path, остановиться и вернуть ownership в build-tagged layer.
