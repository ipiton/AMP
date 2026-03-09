# Requirements: UI-PLACEHOLDER-REMOVAL

**Status**: Implemented v1  
**Date**: 2026-03-09

## Context
На старте задачи active runtime держал `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` как смонтированные, но незавершенные routes с placeholder body `not yet implemented`. Из-за этого operator UI выглядел частично доступным, а фактическое поведение расходилось с dashboard navigation и bug `UI-PLACEHOLDER-PAGES` в `docs/06-planning/BUGS.md`.

## Implemented Result
В результате active dashboard surface остался на текущих `/dashboard/*` routes, но теперь обслуживается через `go-app/cmd/server/legacy_dashboard.go` и отдельный простой template stack в `go-app/cmd/server/templates/legacy/*`. Страницы `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` стали честными read-only screens с runtime-aware `ready` / `empty` / `limited` / `metrics-only` state, а данные для них собираются через узкие summary methods в `go-app/internal/application/legacy_dashboard.go`.

## Goals
- [x] Убрать placeholder behavior для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`.
- [x] Зафиксировать для этих routes реальный active-runtime contract: рабочие страницы или явно ограниченный, но честный UI state без `500`/placeholder text.
- [x] Сохранить текущий dashboard route set и не расширять задачу в полный UI/platform rewrite.

## Constraints
- Задача затрагивает несколько экранов dashboard, поэтому перед реализацией нужен `/spec`; `/research` желателен, если после первичного просмотра останется несколько разумных вариантов wiring/data sources.
- Нельзя ломать уже рабочие dashboard routes (`/`, `/dashboard`, `/dashboard/alerts`) и active API/runtime surface.
- Предпочтительно переиспользовать существующий template/static stack в `go-app/cmd/server/main.go`, если это позволит держать diff узким.
- Нельзя маскировать незавершенную страницу под internal error; если данные или backend еще не готовы, это должно выражаться контролируемым UI state, а не `500`.

## Success Criteria (Definition of Done)
- [x] `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` больше не возвращают placeholder-ответы из active `cmd/server` path.
- [x] Поведение этих страниц зафиксировано в code/tests/docs как часть active runtime, без двусмысленности между mounted route и фактической реализацией.
- [x] Targeted verification path для затронутого UI/dashboard scope определен и выполняем.
