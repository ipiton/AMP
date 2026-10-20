# Deep Review Findings: наследование таймингов route как в upstream

**Trigger classification:** discretionary-running (дифф с тестами >200 строк, `X R`, путь таймеров HA, запрошено владельцем)
**Reviewer perspective:** независимый агент-ревьюер (не автор реализации)
**Reviewed at:** 2026-10-20
**Reviewed tree:** bugfix/route-timings @ 91265a3
**Verdict:** fix_required (см. `review-verdict.json`)

Линзы: correctness, premises, `X` (дерево ↔ grouping manager ↔ timer manager ↔ memory/Redis, hot reload, restore/reconciliation), `R` (поведение существующих конфигов, rollback, наблюдаемость), maintainability. Сверка с upstream `alertmanager@v0.32.0` (`dispatch/route.go:68-99`, `config/config.go:914-919`).

## Проверка Design Premises

| Premise | Итог | Комментарий |
|---|---|---|
| `Route.Defaults()` вызывают только два парсера | верно, класс заработан | В проде других вызовов нет; остались только тесты (`alert_processor_test.go`, `grouping/config_test.go`, `parser_test.go`, `parser_bench_test.go`). |
| Тайминги вне дерева читают только `DefaultGroupManager` (корень) и `/api/v2/status` | верно с оговорками | Ещё `grouping.ValidateConfig/ValidateRoute` (в проде не вызываются) и `pkg/configvalidator` — но тот работает по своей структуре `internal/alertmanager/config.Route` (`time.Duration`, без указателей), не по распарсенному route. Корневой фолбэк менеджера `global.*` не учитывает (F6). |
| Единственный продюсер `GroupTimings` — `alert_processor.go:487` | верно | `WithGroupTimings` вызывается только там; `RoutingDecision` строится только из `RouteNode` (`evaluator.go:232,447`, `application/route_evaluator.go:109`). Частичного `GroupTimings` в проде нет. |
| Сохранённые до фикса группы имеют `GroupWait > 0` | верно | |
| Дерево строится и валидируется одинаково при старте и hot reload | верно для построения, **неверно для обработки отказа** | Старт: ошибка `Build` нефатальна → routing деградирует (`service_registry.go:403-407`); reload: ошибка возвращается и старое дерево остаётся. См. F1. |
| `time.NewTimer(0)` и Redis-индекс корректны для нулевой длительности | верно (code-read) | TTL `<=0` → `grace`; `GroupTimer.Validate` (`ExpiresAt == StartedAt + 0`) проходит. Поведенческое подтверждение — в `testing`. |

## Findings

### F1 — Явный `group_interval: 0s`/`repeat_interval: 0s` не отвергается при загрузке конфига: на старте AMP поднимается без route-дерева
- **Severity:** major
- **Location:** `go-app/internal/business/routing/tree.go:515`, `go-app/internal/application/service_registry.go:403`, `go-app/pkg/configvalidator/validators/route.go:134`, `go-app/pkg/configvalidator/validators/common.go:76`
- **Issue:** Spec (D3, Impact) обещает «не загружаются — как upstream». Фактически ноль отвергается только в `RouteTree.validateDurations` при `TreeBuilder.Build`. Путь загрузки (`config.loadRouteConfig` → `validateAlertmanagerSubset` → `infraroute.Parse`) ноль пропускает: `configvalidator` E026 сознательно считает 0 «не задан» (комментарий в `common.go:76-79`), в `RouteConfigParser.validateRouteTree` проверок таймингов нет. На старте ошибка `Build` нефатальна: `initializeRouting` пишет Warn + degraded reason, и AMP работает **без route-tree evaluation** (алерты уходят мимо маршрутизации/группировки). До фикса такой конфиг работал (0 молча наследовался) — после выкатки он тихо ломает маршрутизацию, тогда как upstream падает на загрузке (`Route.UnmarshalYAML`: `group_interval cannot be zero`). То же для `global.group_interval/repeat_interval: 0s` (доходит до корня). На hot reload ошибка возвращается — там поведение приемлемое, но конфиг уже принят слоем загрузки.
- **Recommendation:** отвергать на этапе парсинга, как upstream: в `infrastructure/routing/parser.go` (`validateRouteTree`, по `*grouping.Duration`, на каждом route; плюс `global.group_interval/repeat_interval`) — `!= nil && == 0` для interval/repeat и `< 0` для group_wait → `ValidationErrors` → фатальная ошибка `config.Load` на старте и отказ reload. `validateDurations` оставить как backstop. Поправить комментарий `configvalidator/common.go:76-79` (0 для interval/repeat больше не «inherit default»; эта структура ноль от «не задан» не отличает, поэтому проверка живёт в прод-парсере). Тест — в `write-tests` (3.1/3.2).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — Окно между `storage.Store` и `startGroupWaitTimer` даёт ложный re-arm в `ensureGroupTimer`
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go:313`, `go-app/internal/infrastructure/grouping/manager_impl.go:940`
- **Issue:** После переноса старта таймера за `Store` второй алерт той же новой группы (или другая реплика в HA), пришедший в окно Store → `StartTimer.SaveTimer`, находит группу без таймера и сам взводит `group_wait` (Warn «alert group has no timer scheduled, re-arming group_wait» + метрика `timer_rearm`). Затем исходный вызов `StartTimer` заменяет таймер. Корректность не страдает (`StartTimer` отменяет локальный handle; в HA `fireStillDue`/lock отсекают лишний fire, при `group_wait: 0s` повторный flush дедуплицируется), но на всплесках алертов появляются ложные предупреждения и метрика ремонта, которая должна сигналить о реальной потере таймера. При `group_wait: 0s` возможен лишний немедленный flush вместо ожидания `group_interval` для второго алерта.
- **Recommendation:** зафиксировать как tech-debt; варианты — взводить таймер под тем же per-key сериализатором, что и создание группы (см. F3), или не считать отсутствие таймера у группы младше `CreatedAt + ε` поводом для re-arm. Поведение проверить в `testing` нагрузочным прогоном пакета grouping.
- **Disposition:** defer-tech-debt
- **Follow-up:** запись в `docs/06-planning/TECH-DEBT.md` в `finalize`

### F3 — `AddAlertToGroup` не сериализует Load→create→Store для новой группы (предсуществующее)
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go:246`, `go-app/internal/infrastructure/grouping/manager_impl.go:821`
- **Issue:** `createNewGroupUnsafe` документирован «Caller must hold write lock (m.mu.Lock)», но `AddAlertToGroup` лок не берёт. Два конкурентных алерта одной новой группы оба получают `not found`, создают две группы, последний `Store` затирает первый (алерт пропадает из storage), оба стартуют `group_wait`. Не внесено этим диффом (до фикса было так же или хуже: таймер стартовал до `Store`), но при `group_wait: 0s` проявляется как две отдельные нотификации вместо одной. В `BUGS.md` записи нет.
- **Recommendation:** отдельный bug: per-group-key сериализация создания (keyed mutex / singleflight) либо условная запись в storage.
- **Disposition:** defer-bug
- **Follow-up:** новая запись в `docs/06-planning/BUGS.md` в `finalize`

### F4 — Неверное утверждение о rollback в Spec
- **Severity:** nit
- **Location:** `tasks/ROUTE-TIMINGS-BUNDLE/Spec.md:73`
- **Issue:** «Нулевой таймер … не пройдёт `Validate` при `RestoreTimers`» — `RestoreTimers` `Validate` не вызывает (он есть только в `SaveTimer` memory/Redis). Старый бинарь найдёт такой таймер как просроченный и выстрелит его через `onTimerExpired` (missed) — исход тот же безопасный, но механизм другой. В смешанном rolling update старый бинарь для группы с `Timings.GroupWait == 0` (`omitempty`) при re-arm возьмёт корневой `group_wait` — тоже безопасно.
- **Recommendation:** поправить формулировку Rollback.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — `Route.Defaults()` стал мёртвым в проде, его doc-комментарий вводит в заблуждение
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/grouping/config.go:149`
- **Issue:** Комментарий «This method should be called after parsing configuration» теперь прямо противоречит фиксу: вызов на распарсенном конфиге стирает наследование (ровно баг `ROUTE-TIMINGS-NOT-INHERITED`). Метод используют только тесты (в т.ч. `alert_processor_test.go` на корне).
- **Recommendation:** переписать doc: не вызывать на распарсенном дереве; тайминги разрешает `TreeBuilder`. Удалять ли метод — решить в `write-tests` по месту (тесты его используют).
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — Корневой фолбэк `DefaultGroupManager` не учитывает `global.*`
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go:897`, `go-app/internal/infrastructure/grouping/manager_impl.go:985`, `go-app/internal/infrastructure/grouping/manager_impl.go:1152`
- **Issue:** Теперь корень распарсенного конфига может иметь `nil` тайминги; фолбэк менеджера (`timings == nil`) берёт 30s/5m/4h, игнорируя `global.group_wait` и т.п. В проде недостижимо (`alert_processor` всегда передаёт разрешённые тайминги; группы без `Timings` — только до task 2.4), поэтому это расхождение двух источников дефолтов, а не дефект.
- **Recommendation:** оставить; упомянуть в комментарии `startGroupWaitTimer`, что фолбэк — только для вызовов без `WithGroupTimings`.
- **Disposition:** reject (reason: в прод-пути недостижимо, правка не окупается)
- **Follow-up:** n/a

### F7 — Обновление docs/CHANGELOG не заведено шагом в `tasks.md`
- **Severity:** nit
- **Location:** `tasks/ROUTE-TIMINGS-BUNDLE/tasks.md:31`
- **Issue:** `docs/ALERTMANAGER_COMPATIBILITY.md` и `CHANGELOG.md` перечислены в Touched Files, Spec Impact требует запись в CHANGELOG (поломка конфигов с `group_interval/repeat_interval: 0s`, снятие AMP-only `group_interval >= group_wait`, `config.original` без дефолтов), но чекбокса нет — легко потерять.
- **Recommendation:** добавить шаг (фаза 3 или `finalize`).
- **Disposition:** fix-here
- **Follow-up:** n/a

## Прочее (не findings)

- `go build ./...` — ок. `go test ./internal/infrastructure/routing/... ./internal/config/... ./pkg/configvalidator/... ./internal/application/handlers/... ./internal/core/services/...` — зелёные. Тесты `business/routing` и `infrastructure/grouping` не компилируются (ожидаемо, шаг `write-tests`).
- `internal/application` `TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica` (`GROUPING-ORPHAN-ADOPTION-TEST-FLAKY`): 2/20 падений на 91265a3 против 1/20 на 8cd9237 без нагрузки — статистически неразличимо; учащение флейка оценить в `testing` (критерий успеха из requirements).
- Удаление AMP-only `group_interval >= group_wait` соответствует upstream; `inheritDuration` совпадает с `dispatch.newRoute`; `/api/v2/status` `config.original` теперь сериализует только заданное.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
