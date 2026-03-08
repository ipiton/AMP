# Анализ: может ли AMP заменить Alertmanager?

**Дата**: 2026-03-08
**Статус**: AMP пока не готов к честному позиционированию как полный `drop-in replacement` для Alertmanager.

## Статус После Truth-Alignment Slice

На задаче `ALERTMANAGER-REPLACEMENT-SCOPE` выбран и зафиксирован путь:

- **active runtime first**
- **current claim = controlled replacement**
- **wide runtime/API restoration = отдельный backlog**

Где это зафиксировано:

- decision record: `docs/06-planning/DECISIONS.md`
- open drift/blockers: `docs/06-planning/BUGS.md`
- future runtime restoration: `docs/06-planning/BACKLOG.md`
- queued broader docs cleanup: `docs/06-planning/NEXT.md` (`DOCS-HONESTY-PASS`)

## Короткий вывод

- **Да, ограниченно**: AMP уже можно использовать как controlled replacement для узкого production-сценария `ingest -> silence CRUD -> publish to targets`, если клиентская сторона подстроена под фактический API surface AMP.
- **Нет, не полностью**: текущий active runtime не соответствует части заявлений в README, migration docs, compatibility matrix и parity tests, поэтому общий тезис "может заменить Alertmanager без оговорок" пока недостоверен.

## Что уже реально работает

- Active runtime поднимает `ServiceRegistry` и использует реальный publishing path, а не `SimplePublisher` stub.
- `POST /api/v2/alerts` идет через `AlertProcessor` и publishing adapter/coordinator/queue.
- Работают `GET/POST /api/v2/silences` и `GET/DELETE /api/v2/silence/{id}`.
- Работают health/readiness probes и `/metrics`.
- Есть explicit `metrics-only` fallback для publishing runtime.

Это означает, что AMP уже годится для узких сценариев приема и доставки алертов, но не для общего обещания "заменяет Alertmanager" на уровне всей совместимости и операционных ожиданий.

## Что блокирует честный replacement claim

### 1. Active runtime уже уже, чем заявлено в docs/tests

В `go-app/internal/application/router.go` active runtime сейчас монтирует только:

- `/api/v2/alerts`
- `/api/v2/silences`
- `/api/v2/silence/{id}`
- `/health`, `/ready`, `/healthz`, `/readyz`, `/-/healthy`, `/-/ready`
- `/metrics`

При этом в `docs/ALERTMANAGER_COMPATIBILITY.md`, `README.md` и `go-app/cmd/server/main_upstream_parity_regression_test.go` заявлены или ожидаются более широкие active routes:

- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET /api/v2/alerts/groups`
- `POST /-/reload`
- alias `POST /api/v1/alerts`
- debug/static compatibility endpoints

Это главный зазор между текущим кодом и replacement narrative.

### 2. Даже реализованные endpoints еще не дотягивают до полной parity

`GET /api/v2/alerts` в active handler помечен как `simple list`, а advanced filtering и matcher semantics прямо оставлены "на потом". Значит даже там, где route существует, semantic parity еще не зафиксирована для общего replacement claim.

### 3. Quality gates остаются красными

Пока остаются открытыми:

- drift в `go-app/cmd/server/main_phase0_contract_test.go`
- red full `go test ./...` вне scope последнего slice

Это не всегда блокирует узкий controlled rollout, но блокирует сильные формулировки вроде `production-ready drop-in replacement`.

### 4. Public docs historically overstated состояние проекта

На момент первоначального анализа были найдены как минимум такие расхождения:

- `README.md` обещал `Production-Ready`
- `README.md` заявлял `Plugin system`, хотя в коде видны extension points, а не полноценный runtime plugin loader
- `README.md` и migration docs подавали AMP как легкую замену Alertmanager за несколько минут
- `docs/ALERTMANAGER_COMPATIBILITY.md` описывал active runtime шире, чем его реально монтирует код
- `README.md` конфликтовал по лицензии: badge/LICENSE указывали на AGPL, а нижняя секция README писала `Apache 2.0`

Статус после `DOCS-HONESTY-PASS`:

- core public/docs surface и chart metadata по этой теме выровнены;
- open residual cleanup теперь относится в основном к более глубоким internal/subpackage docs, а не к top-level replacement narrative.

## Решение по позиционированию до исправлений

До закрытия зазоров AMP стоит описывать так:

- **Alertmanager-compatible core runtime with phased parity**
- **real publishing path is active**
- **usable for controlled / scoped replacement scenarios**
- **not yet a general-purpose drop-in replacement claim**

## Инкрементальный план исправления

### Slice 1. Scope decision for replacement story

Статус: **закрыто** через `ALERTMANAGER-REPLACEMENT-SCOPE`.

Нужно выбрать один из двух путей:

1. **Дотянуть active runtime до заявленного scope**:
   вернуть/смонтировать `status`, `receivers`, `alerts/groups`, `/-/reload` и решить судьбу `/api/v1/alerts`.
2. **Сузить публичный scope до фактического runtime**:
   поправить ADR, docs и parity expectations под реально поддерживаемую поверхность.

Без этого следующий docs pass будет косметическим.

### Slice 2. Honest docs pass

Статус: **в основном закрыто** через `DOCS-HONESTY-PASS`.

Выполнено для:

- `README.md`
- `docs/MIGRATION_QUICK_START.md`
- `docs/MIGRATION_COMPARISON.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`

Результат:

- убраны `drop-in`, `100%` и прямые current overclaims из core public/docs scope;
- benchmark/resource claims и install narrative приведены к honest wording;
- chart README и metadata больше не обещают verified full parity;
- residual cleanup остался в internal/subpackage docs и вынесен в отдельный follow-up.

### Slice 3. Verification hardening

Статус: **частично начато**.

Нужно восстановить доверие к replacement claim:

- починить `main_phase0_contract_test.go` / `futureparity` suite
- явно определить, какие тесты являются gating для replacement story
- получить зеленый минимум для active runtime parity matrix

### Slice 4. Replacement acceptance criteria

Статус: **частично закрыто**: active-runtime contract tests уже выделены, но полный future replacement checklist ещё не оформлен как отдельный reproducible smoke document.

Добавить один короткий acceptance checklist для future claim "AMP can replace Alertmanager":

- Prometheus/VMAlert ingest smoke
- `amtool` read-path smoke
- silence CRUD smoke
- publishing smoke against real targets or stable stubs
- rollback/runbook smoke

## Что считать закрытием темы

Тезис "AMP может заменить Alertmanager" можно считать честно подтвержденным только когда одновременно выполнены условия:

- active runtime и публичные docs говорят об одном и том же scope
- parity tests компилируются и проходят на выбранном scope
- migration docs не обещают больше, чем реально поддерживается
- replacement story проверяется коротким reproducible smoke path

До этого момента формулировка должна оставаться ограниченной: AMP пригоден для частичной и контролируемой замены, но не для безоговорочного `drop-in replacement` claim.
