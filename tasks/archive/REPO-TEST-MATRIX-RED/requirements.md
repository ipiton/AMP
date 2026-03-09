# Requirements: REPO-TEST-MATRIX-RED

## Context
В репозитории накопилось значительное количество падающих тестов в различных пакетах инфраструктуры и бизнес-логики. Это блокирует полноценную проверку качества (Quality Gates) и замедляет разработку.

## Goals
- [x] Снять panic-level и obvious infrastructure-level test drift в перечисленных пакетах.
- [x] Разложить оставшийся residual red на более узкие follow-up bugs вместо сохранения одной широкой matrix-задачи.

## Scope
Исправление тестов в следующих пакетах (согласно `BUGS.md`):
- `internal/business/publishing` (ошибка: duplicate Prometheus collector registration)
- `internal/infrastructure/inhibition` (ошибка: Redis integration config)
- `internal/infrastructure/k8s` (ошибка: timeout/context error-chain mismatch)
- `internal/infrastructure/migrations` (ошибка: missing sqlite driver import in tests)
- `internal/infrastructure/publishing` (ошибки: mixed assertion failures + nil logger panic)
- `internal/infrastructure/repository` (ошибка: duplicate Prometheus collector registration)
- `internal/infrastructure/webhook` (ошибка: duplicate Prometheus collector registration)
- `pkg/telemetry` (ошибка: `TestResponseWriter` expectation drift)

## Acceptance Criteria
- [x] Либо весь targeted matrix green, либо remaining red честно декомпозирован в отдельные follow-up bugs с явным ownership.
- [x] Panic-level проблемы (duplicate metrics, nil logger, invalid config/driver drift) больше не доминируют результатом.
- [x] Остаток не выглядит скрытой инфраструктурной/panic-регрессией и описан как logic-level follow-up.

## Verified Outcome (2026-03-09)
- В sandbox green проходят `internal/application/handlers`, `internal/infrastructure/k8s`, `internal/infrastructure/migrations`, `internal/infrastructure/webhook`, `pkg/telemetry`, `pkg/httperror`.
- Combined run внутри sandbox дополнительно показал environment limits: `httptest.NewServer` bind restrictions и Docker/testcontainers restrictions больше не маскируют чисто кодовые остатки.
- Вне sandbox подтверждено, что `internal/infrastructure/inhibition` и `internal/infrastructure/publishing` уже green.
- Residual red декомпозирован в `PUBLISHING-HEALTH-REFRESH-DRIFT` и `REPOSITORY-FLAPPING-TRANSITIONS-DRIFT`, поэтому эта задача закрывается как stabilization slice, а не как финальный matrix-closure.
