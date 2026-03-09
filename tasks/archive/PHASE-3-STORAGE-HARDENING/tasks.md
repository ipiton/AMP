# Implementation Checklist: PHASE-3-STORAGE-HARDENING

## Research & Spec
- [x] Завершен `research.md` по storage/bootstrap drift, migrations contract и health semantics active runtime.
- [x] Подготовлен `Spec.md` с profile-aware storage owner model, fail-fast policy и state-aware health/readiness contract.

## Vertical Slices
- [x] **Slice A: Real Storage Bootstrap Path** — убрать `nil` placeholder из `ServiceRegistry`, подключить real storage runtime по профилям и зафиксировать canonical migrations/init order.
- [x] **Slice B: Observable Health And Verification** — перевести health/readiness endpoints на registry state, добавить degraded/reporting semantics и закрыть targeted verification path.

## Implementation
- [x] Шаг 1: Ввести application-local storage runtime boundary в `internal/application` без расширения `core.AlertStorage`.
- [x] Шаг 2: Реализовать `lite` storage bootstrap через `internal/infrastructure/sqlite_adapter.go`: `Connect()`, `MigrateUp()`, assign в runtime/storage fields.
- [x] Шаг 3: Harden `internal/database/migrations.go`: гарантировать регистрацию `pgx` driver и deterministic lookup для `migrations` / `go-app/migrations`.
- [x] Шаг 4: Реализовать thin standard storage adapter поверх текущего `PostgresPool` без второго connection pool.
- [x] Шаг 5: Перевести `ProfileStandard` storage init на последовательность `PostgresPool.Connect -> goose migrations -> standard storage adapter`.
- [x] Шаг 6: Зафиксировать fail-fast policy для required storage failures в `ServiceRegistry.Initialize()` и не оставлять pseudo-healthy fallback для core storage.
- [x] Шаг 7: Перевести existing non-fatal degradations (минимум cache/classification) в явный runtime state/reporting, а не только structured logs.
- [x] Шаг 8: Добавить в `ServiceRegistry` state-aware методы для liveness/readiness/health report.
- [x] Шаг 9: Заменить static health handlers в `internal/application/handlers/status.go` на registry-aware handlers c JSON body для `/health|/healthz|/ready|/readyz` и plain-text contract для `/-/healthy|/-/ready`.
- [x] Шаг 10: Обновить `internal/application/router.go`, не меняя route set, но переключив его на новые state-aware handlers.

## Testing
- [x] Unit/integration tests для `ServiceRegistry` bootstrap matrix:
  - `lite` happy path с real SQLite runtime
  - `standard` startup failure при DB connect/migration/storage init error
  - required storage больше не оставляет `r.storage == nil` после успешного init
- [x] Tests для migration helper path resolution / driver registration behavior, если diff остается локальным и не требует external DB.
- [x] Tests для health/readiness handlers:
  - liveness/readiness success path
  - readiness failure при missing/unhealthy required storage
  - degraded body при non-fatal optional fallback
  - Alertmanager-compatible endpoints сохраняют ожидаемые status codes и text contract
- [x] Обновить существующие router contract tests, если они сейчас предполагают unconditional success bodies для health endpoints.
- [x] Targeted validation проходит:
  - `go test ./internal/application/...`
  - `go test ./internal/infrastructure -run SQLiteDatabase`
  - дополнительные targeted tests для затронутых database/storage packages по мере необходимости
- [x] `go build ./cmd/server` проходит.
- [x] `git diff --check` проходит.
- [x] Full repo gate (`go test ./...`, `make quality-gates`) остается вне обязательного acceptance path и, если продолжит падать, фиксируется как preexisting limitation из `docs/06-planning/BUGS.md`.

## Documentation & Cleanup
- [x] Синхронизировать `requirements.md`, если реализация сузит или расширит bootstrap scope относительно текущего spec.
- [x] Синхронизировать `Spec.md`, если реальный standard storage adapter потребует иного owner/contract решения.
- [x] Перед `/write-doc` и `/end-task` обновить planning/task artifacts с фактическим verification path и residual limitations.

## Final Status
- Scope задачи реализован и синхронизирован в planning/public docs.
- `lite` и `standard` получили реальный storage bootstrap path; required storage failures теперь fail-fast.
- Active health/readiness contract стал state-aware и различает liveness, readiness и optional degraded state.
- Targeted acceptance path зеленый: `go test ./internal/application/... ./internal/database`, `go test ./internal/infrastructure -run SQLiteDatabase`, `go build ./cmd/server`, `git diff --check`.
- Full repo gate `go test ./...` остается red на preexisting проблемах вне scope текущего slice; они зафиксированы в `docs/06-planning/BUGS.md`.

## Open Assumptions
- [x] Предполагается, что CRUD logic для standard storage можно reuse/extract из `internal/infrastructure/postgres_adapter.go` без открытия второго pool и без перехода на `PostgresDatabase` как новый runtime owner.
- [ ] Предполагается, что изменение JSON body health/readiness endpoints допустимо, пока route set и high-level status semantics сохраняются.
- [ ] Предполагается, что current active alert/silence handlers остаются на memory compatibility stores в этом slice и это не считается regression.

## Blockers / Stop Conditions
- [ ] Если standard storage adapter нельзя собрать без большого refactor-а `internal/infrastructure/postgres_adapter.go`, остановиться и сузить реализацию до extractable adapter boundary вместо wholesale storage rewrite.
- [ ] Если health/readiness contract упирается в неожиданные внешние зависимости dashboard/UI, не расширять scope автоматически; зафиксировать drift как follow-up.
- [ ] Если targeted tests упрутся в preexisting red state вне затронутого scope, не маскировать это и не подменять acceptance path полным repo gate.
