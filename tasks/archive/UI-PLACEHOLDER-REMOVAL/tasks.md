# Implementation Checklist: UI-PLACEHOLDER-REMOVAL

## Research & Spec
- [x] Завершен `research.md` по active dashboard ownership, template drift и двум параллельным UI stack-ам.
- [x] Подготовлен `Spec.md` с узкой границей slice: current `/dashboard/*` остается source of truth, а `/ui/*` subsystem не активируется.

## Vertical Slices
- [x] **Slice A: Active Dashboard Contract Hardening + Silences Page** — убрать placeholder path для `/dashboard/silences`, ввести минимальный page/render contract для active legacy dashboard и добавить базовый active-path test scaffold.
- [x] **Slice B: LLM + Routing Pages + Verification Closure** — реализовать честные read-only страницы для `/dashboard/llm` и `/dashboard/routing`, закрыть default verification path и синхронизировать docs/planning truth.

## Implementation
- [x] Шаг 1: Уточнить active route wiring в `go-app/cmd/server/main.go`, чтобы legacy dashboard handlers могли получать runtime/provider state без активации `internal/ui` и без широкого refactor-а bootstrap path.
- [x] Шаг 2: Выбрать и реализовать минимальный render strategy для трех страниц:
  - либо небольшой shared shell/layout после safe hardening;
  - либо простые page-specific templates, если текущие `dashboard.html` / `alert-list.html` тянут лишний drift.
- [x] Шаг 3: Заменить `silencesPageHandler` на реальную read-only страницу в active `/dashboard/silences` surface с данными из current runtime или честным empty/limited state.
- [x] Шаг 4: Убрать из затронутого render path зависимость от известных broken contracts:
  - missing helper `dict`;
  - missing `Breadcrumbs` / `Flash` / `User` fields;
  - broken `/ui/*` links;
  - reference на отсутствующий `/static/js/main.js`, если он остается на critical render path.
- [x] Шаг 5: Реализовать `/dashboard/llm` как read-only status page на основе active config и уже доступных coarse runtime signals без глубокого нового service exposure.
- [x] Шаг 6: Реализовать `/dashboard/routing` как read-only summary page по текущему publishing/routing state; если нужных internals нет в active accessors, отдать honest limited-state вместо fake editor UI.
- [x] Шаг 7: Сохранить без регрессии `/`, `/dashboard`, `/dashboard/alerts`; любой shared hardening ограничить только тем, что нужно для стабильной отрисовки новых страниц.

## Testing
- [x] Добавить non-tagged tests в active server path (`go-app/cmd/server`) для `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`.
- [x] Проверить в тестах минимум:
  - success status code для нормального GET;
  - отсутствие `not yet implemented` в body;
  - базовый HTML contract и ключевые user-visible states;
  - empty/limited-state behavior там, где runtime не дает rich data.
- [x] Если в реализации появится общий page/render helper, покрыть его focused tests локально, а не полагаться только на dormant handler suite.
- [x] Targeted validation проходит:
  - `cd go-app && go test ./cmd/server -count=1`
  - `cd go-app && go build ./cmd/server`
  - `git diff --check`
- [x] Если дополнительно будут затронуты non-main server packages, добрать их targeted `go test` по месту изменения.
- [ ] Full repo gate (`cd go-app && go test ./... -count=1`, `make quality-gates`) остается вне обязательного acceptance path; если он все еще red по preexisting причинам, это не скрывать и не подменять им targeted verification.

## Documentation & Cleanup
- [x] Синхронизировать `requirements.md`, если реализация сузит page data sources или template approach относительно текущего spec.
- [x] Синхронизировать `Spec.md`, если фактический render strategy окажется уже или проще, чем планировалось.
- [x] На `/write-doc` обновить task artifacts и planning truth под реальное active behavior этих страниц.
- [x] Если acceptance будет закрыт, обновить `docs/06-planning/BUGS.md`: закрыть или переописать `UI-PLACEHOLDER-PAGES` в соответствии с фактическим runtime contract.

## Expected End State
- [x] `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` больше не возвращают placeholder body из `go-app/cmd/server/main.go`.
- [x] Active dashboard surface получает честные read-only страницы без скрытой миграции на dormant `/ui/*`.
- [x] Default active-path tests фиксируют новый route contract без build tags.
- [x] Targeted acceptance path зеленый и задокументирован.

## Open Assumptions
- [ ] Предполагается, что runtime state для этих страниц можно передать в legacy dashboard wiring через узкий provider/registry path без каскадного refactor-а server bootstrap.
- [ ] Предполагается, что `/dashboard/silences` можно реализовать поверх current silence runtime/state без активации richer silence UI subsystem.
- [ ] Предполагается, что для `/dashboard/llm` и `/dashboard/routing` честный limited-state UX приемлем, если active runtime не экспонирует richer internals.

## Blockers / Stop Conditions
- [ ] Если текущий template stack нельзя безопасно использовать без широкого ремонта legacy dashboard, остановиться на более простых dedicated templates вместо общего template rewrite.
- [ ] Если для routing/LLM страниц нужен широкий новый доступ к внутренним сервисам, не расширять scope автоматически; зафиксировать limited-state implementation и follow-up.
- [ ] Если default tests начинают зависеть от `futureparity` suite или иных preexisting broken paths, не чинить их в рамках этого slice; оставить coverage focused на active mounted routes.
