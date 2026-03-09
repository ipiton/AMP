# UI-PLACEHOLDER-REMOVAL - Spec

**Status**: Implemented v1  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `active dashboard placeholder removal via honest read-only pages on current /dashboard/* routes`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/BACKLOG.md`

**Implemented Result**:
- active dashboard surface остался на current `/dashboard/*` routes, но route wiring вынесен в `go-app/cmd/server/legacy_dashboard.go`;
- render path использует отдельный простой template stack `go-app/cmd/server/templates/legacy/*` и `go-app/cmd/server/static/css/legacy-dashboard.css`, а не drifted generic templates;
- page-facing runtime summaries собираются в `go-app/internal/application/legacy_dashboard.go`;
- default active-path coverage закреплена в `go-app/cmd/server/legacy_dashboard_test.go`.

---

## 1. Problem Statement

В active runtime маршруты `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` уже смонтированы в `go-app/cmd/server/main.go`, но вместо реальных экранов возвращают placeholder-ответы `not yet implemented`.

При этом проблема шире, чем три незаполненных handler-а:

1. active dashboard layer уже находится в drift-состоянии по template/data contract;
2. в репозитории существует второй, richer UI stack (`internal/ui` + `cmd/server/handlers`), но он не является текущим source of truth для смонтированных `/dashboard/*` routes;
3. bug для этих страниц касается не только текста placeholder, а нечестного mounted UI contract: route существует, но operator page фактически отсутствует.

Цель этого spec: убрать placeholder behavior узким вертикальным slice, не превращая задачу в полный dashboard rewrite или скрытую активацию dormant `/ui/*` subsystem.

---

## 2. Goals

1. Убрать placeholder behavior для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`.
2. Зафиксировать active-runtime contract этих страниц как реальные HTML-экраны с честным limited-state UX.
3. Сохранить текущий dashboard route set и не ломать уже рабочие `/`, `/dashboard`, `/dashboard/alerts`.
4. Добавить default, non-tagged verification path именно для active mounted routes, а не только для dormant UI stack.
5. Держать diff узким: hardenить active dashboard surface, а не переносить весь UI на новый template engine.

---

## 3. Non-Goals

1. Не активировать dormant `/ui/*` route universe в этом slice.
2. Не переводить legacy dashboard на `internal/ui.TemplateEngine`.
3. Не реализовывать full CRUD/operator workflows для silences, routing или LLM management.
4. Не добавлять auth/session layer или новый navigation model.
5. Не чинить весь drift существующих dashboard templates, если это не нужно напрямую для трех целевых страниц.
6. Не делать websocket/live-update behavior.
7. Не переписывать публичные API `/api/v2/*` ради этих экранов.

---

## 4. Key Decisions

### 4.1 Source Of Truth Remains Current `/dashboard/*`

Owner-ом этого slice остается active runtime в current `go-app/cmd/server` path и текущий mounted dashboard surface.

Это означает:

- существующие `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` остаются теми же route entry points;
- dormant handler/template stack рассматривается только как источник идей или точечного reuse, но не как новая runtime truth.

Причина:
- это минимальный mergeable путь без смены route universe и без расширения задачи в dashboard migration.

### 4.2 This Slice Removes Placeholders, Not Dashboard Drift Globally

Мы не беремся в этой задаче привести весь legacy dashboard в идеальное состояние.

Допустимый объем:

- небольшой shared hardening текущего render contract;
- новые page-specific handlers/view models/templates для трех целевых страниц;
- локальная правка layout/navigation, только если без нее новые страницы остаются broken.

Недопустимый объем:

- массовая перестройка всех страниц dashboard;
- перенос всего UI на другой стек.

### 4.3 Honest Read-Only Pages Are Sufficient

Для acceptance достаточно, чтобы каждая из трех страниц:

- открывалась как обычная HTML page;
- не возвращала placeholder text;
- отражала реальное состояние runtime или честный limited-state/empty-state;
- не маскировала отсутствие backend-данных под internal error.

Полноценные operator actions не требуются.

### 4.4 Legacy Render Contract May Be Minimally Hardened

Исследование показало, что текущие embedded templates уже drifted:

- missing fields в page data;
- missing helper functions;
- broken links;
- reference на отсутствующий static asset.

Решение:

- не опираться слепо на текущие `dashboard.html` и `alert-list.html` как universal base для новых страниц;
- разрешить минимальный shared hardening layout/page contract, если без этого нельзя получить стабильный active UI;
- при необходимости предпочесть простые, page-specific templates вместо reuse drifted generic templates.

### 4.5 Data Source Policy Is Page-Specific And Narrow

#### `/dashboard/silences`

Предпочтительный источник данных:

- `ServiceRegistry.SilenceStore()` или эквивалентный active runtime accessor.

Допустимый contract:

- read-only summary/list существующих silence entries;
- если полноценная выборка/модель недоступна, честный empty-state или limited-state с понятным объяснением;
- link-out на compatible API surface допустим как secondary affordance, но не как замена страницы.

#### `/dashboard/llm`

Предпочтительный источник данных:

- active config и coarse runtime state, которые уже доступны без новой глубокой интеграции.

Допустимый contract:

- enabled/disabled status;
- базовые hints о configured provider/model, если они уже доступны в active config;
- honest limited-state, если runtime health/details недоступны без нового service exposure.

#### `/dashboard/routing`

Предпочтительный источник данных:

- active publishing/routing mode и доступные summary-сигналы из current runtime.

Допустимый contract:

- read-only summary того, как routing/publishing устроены сейчас;
- счетчики/summary только если они уже доступны через текущие accessors или узкий wiring;
- honest unavailable/limited-state вместо фальшивого editor UI.

### 4.6 No New Deep Service Exposure Just For UI Cosmetics

Если для rich routing/LLM screen нужен новый глубокий доступ к internals, который сейчас не экспонируется `ServiceRegistry`, то этот slice не расширяет service boundary без сильной необходимости.

Допустимо:

- передать registry/provider в legacy dashboard route registration;
- использовать уже существующие accessors;
- добавить очень узкий page-facing summary provider, если это локально и не меняет публичную архитектурную truth.

Недопустимо:

- вытаскивать наружу большой набор внутренних сервисов только ради красивого dashboard.

### 4.7 Default Active-Path Tests Are Required

Этот slice должен добавить non-tagged tests, которые защищают именно active mounted routes.

Минимум нужно покрыть:

- статус-коды;
- отсутствие placeholder body;
- базовый HTML contract или ключевые user-visible states;
- page behavior при empty/limited runtime state.

Исторические `futureparity` tests не считаются достаточным acceptance path для этой задачи.

---

## 5. Target Architecture

```text
cmd/server/main.go
  -> registerLegacyDashboardRoutes(..., dashboardProvider)
  -> page-specific handlers:
       - silences
       - llm
       - routing
  -> page-specific view models
  -> simple templates or minimally hardened shared layout

ServiceRegistry
  -> SilenceStore()
  -> Config()
  -> existing runtime accessors already available today

Tests
  -> non-tagged route tests for active /dashboard/* pages
```

Ключевая идея:

- wiring остается в current active server entrypoint;
- data extraction делается через уже существующий application runtime;
- presentation для трех страниц должна быть достаточно простой, чтобы не наследовать весь текущий template drift.

---

## 6. Component Design

### 6.1 Route Wiring

`registerLegacyDashboardRoutes` или соседний wiring слой должен получить доступ к runtime/provider-у, достаточному для построения page models.

Цель:

- убрать hardcoded placeholder handlers;
- дать page handlers доступ к active state без прямой зависимости от большого количества internals.

Реализация:

- route wiring вынесен в `go-app/cmd/server/legacy_dashboard.go`;
- active runtime передает `ServiceRegistry` как narrow dashboard provider.

### 6.2 Page Models

Для каждой страницы должен быть свой явный view model.

Причина:

- текущий generic `PageData` already drifted;
- отдельные typed models проще тестировать;
- это снижает риск случайного reuse сломанного template contract.

### 6.3 Templates

Реализация выбрала:

1. простой shared shell/layout в `templates/legacy/shared.html`;
2. page-specific templates для `overview`, `alerts`, `silences`, `llm`, `routing`;
3. полный уход от drifted `dashboard.html` / `alert-list.html` contract для active dashboard path.

### 6.4 UX Contract

Каждая страница должна явно показывать одно из состояний:

1. `ready` — данные доступны и отображаются;
2. `empty` — backend пустой, но страница рабочая;
3. `limited` — страница доступна, но runtime пока не экспонирует все детали;
4. `error` — только для реального сбоя обработки запроса, а не для незавершенной реализации.

---

## 7. Runtime Behavior Matrix

| Route | Runtime data available | Expected behavior |
| --- | --- | --- |
| `/dashboard/silences` | silence data available | render read-only list/summary |
| `/dashboard/silences` | silence data absent or empty | render honest empty-state |
| `/dashboard/llm` | config/runtime summary available | render read-only status page |
| `/dashboard/llm` | only partial config known | render limited-state page with explicit note |
| `/dashboard/routing` | routing/publishing summary available | render read-only summary page |
| `/dashboard/routing` | summary not available without deep new wiring | render limited-state page, not placeholder |

Во всех строках матрицы действует общее правило:

- никакого `not yet implemented`;
- никакой маскировки незавершенности под `500`.

---

## 8. Deliverables

1. Реальные handlers/pages для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`.
2. Минимальный hardening active dashboard render contract, если он нужен для стабильной отрисовки.
3. Non-tagged tests на active mounted route contract.
4. Обновленные planning/task docs, фиксирующие фактическое runtime behavior после реализации.

---

## 9. Acceptance Criteria

1. `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` больше не отдают placeholder body из `main.go`.
2. Каждая страница возвращает корректный HTTP success response при нормальном запросе и строит честный UI state.
3. Реализация не ломает `/`, `/dashboard`, `/dashboard/alerts` и не меняет active route set.
4. Slice не активирует dormant `/ui/*` routes и не требует полного dashboard migration.
5. В repo появляется default verification path для active dashboard placeholder removal.
6. Поведение зафиксировано в task docs и, при необходимости, в planning artifacts.

---

## 10. Risks

1. Existing legacy templates могут потянуть больше hidden drift, чем видно из research.
2. Для routing/LLM страниц может оказаться недостаточно уже доступных runtime accessors.
3. Embedded static/template contract может ломаться на seemingly small layout changes.
4. Есть риск незаметно начать миграцию на другой UI stack, если не держать slice boundary жестким.

---

## 11. Explicit Follow-Ups

Эта задача может осознанно оставить за пределами scope:

1. richer operator workflows для silences;
2. полноценный routing editor;
3. deeper LLM diagnostics/management UI;
4. harmonization всего legacy dashboard template contract;
5. решение, нужен ли репозиторию переход на `internal/ui` как единый UI stack.

Если после реализации останется заметный drift за пределами трех страниц, это должно идти отдельной задачей в `BACKLOG.md` или Queue.

---

## 12. Verification Strategy

Фактически подтвержденный verification path:

1. `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -count=1`
2. `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/... -count=1`
3. `cd go-app && GOCACHE=$(pwd)/.cache/go-build go build ./cmd/server`
4. `git diff --check`

Примечание:

- `./internal/application/...` внутри sandbox может упираться в `httptest.NewServer()` и невозможность bind'ить локальный порт; validated run был подтвержден вне sandbox.

Полный repo-wide quality gate желателен, но не является обязательным, если он уже красный по известным несвязанным причинам. Такие ограничения нельзя скрывать.
