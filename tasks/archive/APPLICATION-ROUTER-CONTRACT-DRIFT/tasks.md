# Implementation Checklist: APPLICATION-ROUTER-CONTRACT-DRIFT

## Research & Spec
- [x] Выполнен `research.md` по active router contract drift, reload-path gap и compile blocker от duplicate files.
- [x] Подготовлен `Spec.md` с выбранным направлением: разделить restored operational surface и still-absent historical surface внутри `internal/application`.

## Vertical Slices
- [x] **Slice A: Active Router Contract Refresh** — обновить `go-app/internal/application/router_contract_test.go`, чтобы restored `/api/v2/status`, `/api/v2/receivers`, `/api/v2/alerts/groups` и `/-/reload` считались частью active runtime contract.
- [x] **Slice B: Verification Closure** — сделать reload-related test seam честным, прогнать `internal/application`, и только при необходимости синхронизировать planning/task truth.

## Implementation
- [x] Шаг 1: Пересобрать `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent` в более точное разбиение:
  - restored operational endpoints present;
  - still-absent historical surface remains absent.
- [x] Шаг 2: Обновить `newActiveContractMux(...)`, чтобы test registry задавал deterministic `startTime` и минимальный reload-capable path без полного production bootstrap.
- [x] Шаг 3: Зафиксировать method/status contract для restored endpoints на router-contract уровне, не дублируя детальные handler assertions.
- [x] Шаг 4: Сохранить explicit absence-checks для `/api/v1/alerts`, `/api/v2/config`, `/history`, `/api/v2/classification/health` и других реально неактивных routes.
- [x] Шаг 5: Не трогать `go-app/cmd/server`, `futureparity`, public docs и unrelated packages, если это не потребуется для green path.

## Testing
- [x] `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -run TestActiveRuntimeContract -count=1`
- [x] `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -count=1`
- [x] `cd go-app && mkdir -p .cache/go-build && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/... -count=1`
- [x] Полный `./internal/application` verification потребовал вне-sandbox запуск, потому что `service_registry_classification_test.go` использует `httptest.NewServer`, а sandbox блокирует bind на loopback-порт.
- [x] `git diff --check`
- [x] Verification не уперся в новый неожиданный drift вне `internal/application`; отдельная planning-эскалация не потребовалась.

## Write Tests
- [x] Router-contract coverage обновлен в `go-app/internal/application/router_contract_test.go` под current active runtime surface.
- [x] Дополнительный handler-level diff не потребовался: `handlers/status_api_test.go` и `handlers/groups_test.go` уже покрывают body/error semantics для `status`, `receivers`, `reload` и `alerts/groups`.

## Documentation & Cleanup
- [x] `requirements.md` и `Spec.md` синхронизированы с фактическим verified state; `Spec.md` переведен из `Planned` в `Implemented`.
- [x] `docs/06-planning/BUGS.md` обновлен: `APPLICATION-ROUTER-CONTRACT-DRIFT` больше не считается открытым residual drift.
- [x] Public docs не менялись, потому что runtime truth не расширялся; cleanup не выходил за already removed `* 2.*` duplicates.

## Expected End State
- [x] `go-app/internal/application/router_contract_test.go` описывает current active router truth, а не pre-restoration state.
- [x] Reload-path в contract tests проверяется через honest minimal state, а не через случайный `reload coordinator not initialized`.
- [x] `go test ./internal/application -count=1` green.
- [x] Active runtime contract и historical `futureparity` layer остаются четко разделены.

## Blockers / Stop Conditions
- [ ] Если green path потребует менять production router surface beyond current mounted routes, остановиться и не расширять scope.
- [ ] Если reload contract нельзя честно смоделировать без тяжелого production bootstrap, зафиксировать это как новый design issue вместо скрытого refactor-а.
- [ ] Если рядом всплывут unrelated failures из других пакетов, не превращать задачу в новый stabilization pass.
