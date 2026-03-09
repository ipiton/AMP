# Requirements: APPLICATION-ROUTER-CONTRACT-DRIFT

## Context
После `RUNTIME-SURFACE-RESTORATION` active router в `go-app/internal/application/router.go` снова монтирует `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups` и `POST /-/reload`. Но `go test ./internal/application -run TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent -count=1` все еще проверяет pre-restoration contract и падает. Отдельно reload-path в test registry сейчас упирается в `reload coordinator not initialized`, поэтому suite не отражает реальное active behavior.

## Goals
- [x] Обновить `internal/application` contract tests под current active routes и их текущий method/status contract.
- [x] Сделать reload-related test path honest для active runtime, без фальшивого `reload coordinator not initialized` в expected success path.
- [x] Сохранить явное разделение между active runtime contract и historical `futureparity` harness.

## Constraints
- Не расширять active runtime дальше уже смонтированных endpoints.
- Не превращать задачу в новый `futureparity`/wide-surface parity pass.
- Предпочитать изменения внутри `internal/application` и связанных test helpers; не трогать production path шире необходимого.
- Посторонние untracked duplicate files вида `* 2.*`, мешавшие compile-level verification, уже удалены; не расширять cleanup дальше без необходимости.

## Success Criteria (Definition of Done)
- [x] `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -count=1` green; дополнительно green подтвержден для `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/... -count=1` (полный scope запускался вне sandbox из-за `httptest.NewServer`).
- [x] `go-app/internal/application/router_contract_test.go` больше не описывает restored endpoints как absent.
- [x] Planning/task docs обновлены только там, где это понадобилось для фиксации verified active contract truth.
