# Research: UI-PLACEHOLDER-REMOVAL

**Date**: 2026-03-09  
**Status**: completed  
**Inputs**: `requirements.md`, active `go-app/cmd/server` runtime, current dashboard templates/tests

## 1. Краткий вывод

Проблема шире, чем три `placeholder`-handler-а в `go-app/cmd/server/main.go`, но уже уже и лучше ограничена, чем полный UI rewrite.

Главный вывод:

- active routes `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` действительно остаются legacy-страницами в `main.go`;
- но current dashboard/template layer уже сам по себе находится в drift-состоянии: есть два разных UI stack-а, несогласованные route/link contracts и неполный template data/function contract;
- поэтому safest slice для `/spec` — не “поднять весь dormant UI”, а сделать честные active-runtime страницы для текущих `/dashboard/*` routes и не активировать скрытый `/ui/*` universe автоматически.

## 2. Что реально есть в коде

### 2.1 Active owner этих страниц

Текущие страницы монтируются только в legacy dashboard layer:

- `go-app/cmd/server/main.go`
  - `registerLegacyDashboardRoutes`
  - `silencesPageHandler`
  - `llmPageHandler`
  - `routingPageHandler`

Сейчас эти handlers просто пишут plain text `not yet implemented`.

### 2.2 Planning drift: bug говорит про `500`, код сейчас дает placeholder body

В `docs/06-planning/BUGS.md` bug `UI-PLACEHOLDER-PAGES` описан как `500 + not yet implemented`, но текущий код в `main.go` не делает `http.Error(...)`; он просто пишет body через `fmt.Fprintf(...)`.

То есть source of truth по коду сейчас ближе к:

- mounted routes существуют;
- route body placeholder;
- user-facing поведение все равно нечестное/незавершенное.

Это важно для `/spec`: чинить нужно не “server error semantics”, а незавершенный mounted UI contract.

## 3. Архитектурные находки

### 3.1 В repo уже есть два разных UI stack-а

#### Stack A: active legacy dashboard

Файлы:

- `go-app/cmd/server/main.go`
- `go-app/cmd/server/templates/*`
- `go-app/cmd/server/static/*`

Особенности:

- embed-based templates;
- ad-hoc `PageData` и `renderTemplate(...)`;
- mounted current `/dashboard*` routes.

#### Stack B: dormant richer UI subsystem

Файлы:

- `go-app/internal/ui/*`
- `go-app/cmd/server/handlers/dashboard_*`
- `go-app/cmd/server/handlers/templates/silences/*`

Особенности:

- собственный `internal/ui.TemplateEngine`;
- richer `ui.PageData`;
- отдельные handler packages/tests;
- своя route universe вокруг `/ui/silences*`.

Вывод:

- в active runtime сейчас используется Stack A;
- Stack B выглядит как незавершенный/неподключенный subsystem и не является бесплатным reuse.

### 3.2 Stack A уже внутренне broken / drifted

Текущие embedded templates в `go-app/cmd/server/templates/*` ожидают contract, который `main.go` не обеспечивает.

Конкретно:

- `layouts/base.html` использует `.Breadcrumbs`, `.Flash`, `.User`, но локальный `PageData` в `main.go` этих полей не содержит;
- `pages/dashboard.html` использует helper `dict`, которого нет в `webTemplateFuncMap()`;
- `pages/alert-list.html` ожидает богатую `.Data`-структуру (`Filters`, `Sorting`, `TotalPages`, etc.), а `alertsPageHandler` передает `nil`;
- template links указывают на неактивные или несогласованные routes:
  - `/alerts`
  - `/silences`
  - `/groups`
  - `/receivers`
  - `/ui/alerts`
  - `/ui/silences`
  - `/ui/settings`
- `layouts/base.html` подключает `/static/js/main.js`, но в embedded static tree такого файла нет.

Вывод:

- просто “добавить еще три template page” поверх текущего `renderTemplate(...)` недостаточно;
- нужно либо нормализовать page/template contract в active stack,
- либо делать новые страницы максимально простыми и независимыми от drifted templates.

### 3.3 Готовых LLM/routing page implementations нет

Что реально найдено:

- для silences есть отдельный UI template set в `go-app/cmd/server/handlers/templates/silences/*`;
- для dashboard overview/health/alerts есть JSON/API handlers в `go-app/cmd/server/handlers/dashboard_*`;
- для `LLM` и `routing` нет сопоставимого готового page layer в active stack.

Вывод:

- “reuse dormant silences UI” еще можно обсуждать;
- “reuse готовых LLM/routing страниц” по сути нечего.

## 4. Какие runtime data sources уже доступны

`ServiceRegistry` уже может дать часть нужного state, если legacy dashboard wiring начнет получать registry/provider:

- `AlertStore()`
- `SilenceStore()`
- `Config()`
- `LivenessReport(...)`
- `ReadinessReport(...)`
- `Publisher()`
- `PublishingMetricsCollector()`

Но важных UI accessors сейчас нет:

- нет явного accessor-а для `classificationSvc`;
- нет явного accessor-а для publishing discovery/health internals;
- нет готового page model builder для routing dashboard.

Вывод:

- read-only honest pages сделать можно;
- полноценные operator pages с rich data/edit flows потребуют нового wiring и быстро раздуют scope.

## 5. Тестовая и verification картина

### 5.1 Default active-path coverage для dashboard pages почти отсутствует

- `go test ./cmd/server` сейчас показывает `no test files`, потому что historical route suite в `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go` сидит под `futureparity` build tag.
- Значит placeholder dashboard routes сейчас не защищены default acceptance path.

### 5.2 Reusable tests есть, но они относятся к другому stack-у

Есть хорошие non-tagged tests для:

- `go-app/cmd/server/handlers/dashboard_handler_simple_test.go`
- `go-app/cmd/server/handlers/dashboard_overview_test.go`
- `go-app/cmd/server/handlers/dashboard_alerts_test.go`

Но они тестируют:

- `internal/ui.TemplateEngine`;
- отдельные handler packages;
- не current legacy mounted `/dashboard/silences|llm|routing` routes из `main.go`.

Вывод:

- для acceptance этого slice нужно добавить новые default tests именно на active dashboard page contract.

## 6. Варианты реализации

### Option A: Minimal active-runtime pages inside current `/dashboard/*` surface

Идея:

- оставить owner-ом active dashboard routes `cmd/server/main.go`;
- передавать в legacy dashboard layer registry/provider;
- сделать реальные HTML pages для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`;
- страницы могут быть read-only и с honest limited-state UX.

Плюсы:

- минимальный scope;
- сохраняет current active route set;
- не требует активации dormant `/ui/*` subsystem;
- хорошо соответствует queue item и bug scope.

Минусы:

- придется слегка нормализовать shared page/template contract;
- rich CRUD/operator UI не появится.

### Option B: Подключить dormant `internal/ui` / `cmd/server/handlers` stack

Идея:

- начать использовать `internal/ui.TemplateEngine`;
- тащить existing silence UI templates/handlers;
- возможно вводить `/ui/*` routes как real surface.

Плюсы:

- больше reuse существующих tests/templates;
- потенциально richer UI.

Минусы:

- это уже другая route universe;
- task быстро превращается в larger dashboard migration;
- `LLM` и `routing` все равно остаются без готовых page implementations;
- высокий риск выйти за 1-2d slice.

### Option C: Redirect/bridge to APIs or metrics only

Идея:

- убрать placeholder text, но вместо страниц отдавать redirect на API/metrics/docs.

Плюсы:

- минимальный код.

Минусы:

- слабый UX;
- mounted dashboard pages фактически перестают быть dashboard pages;
- плохо соответствует формулировке queue item “реализовать страницы”.

## 7. Рекомендация

Рекомендую для `/spec` выбрать **Option A** и явно сузить scope:

1. Не активировать dormant `/ui/*` subsystem в этом slice.
2. Считать source of truth только current `/dashboard/*` routes в `main.go`.
3. Для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` сделать **read-only honest pages**, а не full CRUD/editor.
4. Если для этого потребуется небольшой shared fix в legacy dashboard render contract, это допустимо:
   - передать registry/provider в dashboard route registration;
   - выровнять page data contract;
   - не тянуть whole `internal/ui` migration.
5. Не переиспользовать текущие `dashboard.html` / `alert-list.html` как есть без hardening: они уже завязаны на missing funcs, richer data models и broken links.

## 8. Рекомендуемая граница следующего slice

Что выглядит реалистично для current task:

- `/dashboard/silences`
  - страница списка/summary поверх `SilenceStore()` или честный empty state, если active runtime использует только compatibility store;
  - link-out на `/api/v2/silences` допустим как secondary action.

- `/dashboard/llm`
  - read-only page со статусом:
    - enabled/disabled по config,
    - current health/degraded hints, если доступно,
    - явный limited mode, если LLM не активен в runtime.

- `/dashboard/routing`
  - read-only page про current routing/publishing reality:
    - publishing mode,
    - target/discovery summary если доступно,
    - честный limited state вместо псевдо-редактора.

Что не стоит включать автоматически:

- новый `/ui/*` route set;
- websocket/dashboard live updates;
- полноценные routing editor / LLM management workflows;
- auth/session layer;
- full migration всех dashboard templates на `internal/ui`.

## 9. Риски и stop conditions

### Risk A: задача расползется в полную dashboard migration

Триггеры:

- попытка подключать `internal/ui.TemplateEngine` как новый global owner;
- попытка активировать `/ui/silences*` как новый public surface;
- попытка чинить все broken links/templates одним махом.

Stop condition:

- если без этого нельзя закрыть три страницы, надо сузить slice до honest limited-state pages, а не расширять задачу автоматически.

### Risk B: hidden drift затронет уже существующие `/dashboard` и `/dashboard/alerts`

Текущий shared render contract уже нестабилен.

Следствие:

- `/spec` должен прямо разрешить минимальный shared hardening, если он нужен для безопасного рендера новых страниц.

### Risk C: verification path окажется пустым

Так как default `cmd/server` route tests сейчас не активны, легко снова остаться без защиты regression path.

Следствие:

- acceptance path должен включать новые non-tagged tests для active dashboard pages.

## 10. Next Step Implication

На `/spec` стоит зафиксировать:

- narrow slice: honest active `/dashboard/*` pages без `placeholder` и без full UI rewrite;
- allowed dependency: minimal hardening shared dashboard rendering contract;
- explicit non-goal: не активировать dormant `/ui/*` subsystem;
- required verification: default tests для active dashboard page routes плюс targeted build/test path для `cmd/server`.
