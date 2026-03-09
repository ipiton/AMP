# Research: APPLICATION-ROUTER-CONTRACT-DRIFT

## What Was Checked

### Commands
- `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -run TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent -count=1`
- `sed -n '1,260p' go-app/internal/application/router_contract_test.go`
- `sed -n '1,220p' go-app/internal/application/router.go`
- `sed -n '1,240p' go-app/internal/application/handlers/alerts.go`
- `sed -n '1,220p' go-app/internal/application/handlers/status_api.go`
- `sed -n '540,620p' go-app/internal/application/service_registry.go`
- `sed -n '1,220p' go-app/internal/application/handlers/status_api_test.go`

## Findings

### 1. Active router contract already changed, but `router_contract_test.go` still encodes the old truth
- `go-app/internal/application/router.go` now mounts:
  - `GET /api/v2/status`
  - `GET /api/v2/receivers`
  - `GET /api/v2/alerts/groups`
  - `POST /-/reload`
- `go-app/internal/application/router_contract_test.go` still contains `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent`, which expects:
  - `404` for `/api/v2/status`
  - `404` for `/api/v2/receivers`
  - `404` for `/api/v2/alerts/groups`
  - `404` for `/-/reload`
- This is no longer a code bug in the router. It is a contract-test drift after `RUNTIME-SURFACE-RESTORATION`.

### 2. The current test helper registry does not model reload-capable active runtime
- `newActiveContractMux(...)` in `go-app/internal/application/router_contract_test.go` creates a narrow `ServiceRegistry` manually.
- That test registry sets:
  - `config`
  - `alertStore`
  - `silenceStore`
  - `alertProcessor`
  - `storageRuntime`
- But it does **not** initialize:
  - `startTime`
  - `reloadCoordinator`
- Because of that, the restored endpoints are only partially represented in the test mux:
  - `/api/v2/status` responds, but with zero-ish runtime state (`StartTime()` defaults to zero time)
  - `/-/reload` returns `500` with `reload coordinator not initialized`

### 3. Handler-level tests already define the local truth for the restored surface
- `go-app/internal/application/handlers/status_api_test.go` already covers:
  - `GET /api/v2/status`
  - `GET /api/v2/receivers`
  - `POST /-/reload`
- `go-app/internal/application/handlers/groups_test.go` covers `GET /api/v2/alerts/groups`.
- So the missing part is not handler behavior validation; it is synchronization of the higher-level router contract test.

### 4. The test name is now misleading
- `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent` still mixes two different responsibilities:
  - active runtime contract for still-absent endpoints (`/api/v2/config`, `/history`, classification API, deprecated v1 alias)
  - a historical claim that the restored operational endpoints are absent
- After the restoration slice, only the first responsibility is still valid.
- The test should likely be split into:
  - restored operational surface present
  - still-absent non-active wide surface

### 5. There was an immediate compile blocker unrelated to the task logic
- Initial worktree contained untracked duplicate files:
  - `go-app/internal/application/handlers/status_api 2.go`
  - `go-app/internal/application/handlers/groups_test 2.go`
- These duplicates were enough to break package compilation before the real contract drift was exercised:
  - redeclared `StatusResponse`, `VersionInfo`, `StatusAPIHandler`, `ReloadHandler`, `ReceiversHandler`, `configSHA256`
  - missing `extendedFakeRegistry` in duplicate `groups_test 2.go`
- `diff` shows `status_api 2.go` is effectively a copy of `status_api.go`.
- Cleanup was executed after research reproduction, and rerun now reaches the actual failing contract test instead of package-level compile errors.

## Root Cause Summary

The main problem is not that active runtime restoration is incomplete. The main problem is that `go-app/internal/application/router_contract_test.go` still asserts the pre-restoration shape of the router, while its helper registry also under-models reload-capable runtime state.

The package-level verification blocker from duplicate untracked `* 2.*` files has been removed. What remains is the actual router contract drift.

## Options

### Option A: Minimal contract refresh inside `router_contract_test.go`
- Keep the existing test helper.
- Change expectations for:
  - `/api/v2/status`
  - `/api/v2/receivers`
  - `/api/v2/alerts/groups`
  - `/-/reload`
- Add the minimal missing runtime fields to the test registry:
  - `startTime`
  - a fake or lightweight reload-capable path

Pros:
- Smallest diff.
- Keeps ownership inside `internal/application`.

Cons:
- Easy to keep the test semantically muddy.
- Reload behavior may still look artificial if the helper is too thin.

### Option B: Split contract tests into “restored present surface” and “still absent wide surface”
- Replace `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent` with two explicit tests:
  - operational restored endpoints are mounted and have the expected method/status contract
  - config/history/classification/v1 historical endpoints are still absent
- Extend `newActiveContractMux(...)` so it can model reload success/failure honestly.

Pros:
- Best alignment with current planning truth and ADRs.
- Test names become self-explanatory.
- Separates active runtime from historical parity.

Cons:
- Slightly larger diff than a minimal expectation tweak.

### Option C: Reuse more production initialization in tests
- Try to initialize a more realistic `ServiceRegistry` path in the contract test.
- Potentially construct a real `ReloadCoordinator`.

Pros:
- More realistic active runtime emulation.

Cons:
- Larger scope.
- Higher chance of dragging infra/bootstrap concerns into a narrow contract-fix task.
- Not justified for this slice.

## Recommendation

Choose **Option B**.

Reasoning:
- It matches the repo’s current truth model: active runtime contract and historical/wider parity are separate concepts.
- It keeps the fix inside `internal/application` without re-expanding runtime scope.
- It gives the cleanest long-term test semantics:
  - restored operational surface is explicitly present
  - wider non-active surface is explicitly absent

For reload, prefer a narrow test helper seam rather than full production bootstrap:
- either add a fake reload function/state to the contract test helper,
- or attach the minimum `reloadCoordinator`/state needed for deterministic `200` and `500` expectations.

## Recommended Next Step Implication

Now that the duplicate-file blocker is gone, the intended implementation slice is:
1. refresh `router_contract_test.go` naming and route expectations,
2. extend the contract test helper with minimal reload-capable state,
3. rerun `go test ./internal/application -count=1`,
4. update planning docs only if the final verification path changes.
