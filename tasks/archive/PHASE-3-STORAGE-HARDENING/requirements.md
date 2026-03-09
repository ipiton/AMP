# Requirements: PHASE-3-STORAGE-HARDENING

## Context
В `go-app/internal/application/service_registry.go` active bootstrap все еще держит незакрытый infrastructure debt: миграции PostgreSQL помечены как TODO, а storage backend инициализируется через `nil` placeholder при том, что downstream services уже опираются на `core.AlertStorage`. Это создает разрыв между заявленной инициализацией инфраструктуры и фактическим runtime-поведением. Дополнительно `ServiceRegistry.Health()` пока проверяет только database path и не декомпозирует storage/degraded state, из-за чего bootstrap может выглядеть успешным при неоформленном persistence path.

## Goals
- [x] Зафиксировать и реализовать реальный storage initialization path для активных deployment profiles в `ServiceRegistry`.
- [x] Подключить или явно специфицировать policy для migrations/bootstrap order вместо текущего TODO.
- [x] Декомпозировать health checks так, чтобы database, storage и degraded runtime states отражались явно и наблюдаемо.
- [x] Сохранить минимальный scope: hardening active bootstrap path без лишнего расширения на несвязанный persistence rewrite.

## Constraints
Нужны `/research` и `/spec` перед реализацией: задача затрагивает active runtime bootstrap, persistence contract, migrations ordering и production reliability.

Нельзя ломать текущие semantics для `ProfileLite` и `ProfileStandard` без явного решения по fallback/degradation path.

Нельзя маскировать отсутствие real storage или несостоявшиеся migrations под условно healthy состояние.

Нужно опираться на уже существующие runtime pieces (`internal/database/migrations.go`, текущий `ServiceRegistry`, существующие storage interfaces) и держать diff минимальным.

## Success Criteria (Definition of Done)
- [x] `ServiceRegistry` больше не завершает storage init через `nil` placeholder в active path.
- [x] Policy по выполнению или пропуску migrations зафиксирован в коде и planning/spec artifacts.
- [x] Health path различает database, storage и degraded states без ложноположительного `healthy`.
- [x] Для затронутого bootstrap path определен проверяемый verification path (targeted tests/build/checks).

## Outcome (2026-03-09)
- `ProfileLite` теперь поднимает `SQLiteDatabase` как реальный storage runtime с `Connect()` и `MigrateUp()` до публикации `r.storage`.
- `ProfileStandard` теперь идет по canonical path `PostgresPool.Connect -> goose migrations -> thin Postgres storage adapter` без второго connection pool.
- Required storage failures больше не деградируют молча: `ServiceRegistry.Initialize()` fail-fast на storage/database bootstrap errors.
- `/health|/healthz` и `/ready|/readyz` стали state-aware JSON endpoints, а `/-/healthy|/-/ready` сохраняют plain-text compatibility contract.
- Verification path зафиксирован и прогнан таргетированно: `go test ./internal/application/... ./internal/database`, `go test ./internal/infrastructure -run SQLiteDatabase`, `go build ./cmd/server`, `git diff --check`; полный `go test ./...` остается red на preexisting repo-wide проблемах из `docs/06-planning/BUGS.md`.
