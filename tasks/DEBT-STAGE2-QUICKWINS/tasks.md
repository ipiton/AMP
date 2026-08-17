# Tasks — DEBT-STAGE2-QUICKWINS

- [x] 1. golangci-lint → 0 по всему репо (было 142 по обрезанному списку, реально ~400+ с учётом снятых cap'ов); `.golangci.yml` фиксирует набор линтеров (errcheck, govet, ineffassign, staticcheck, unused)
- [x] 2. Протухшие TODO: `sync_worker.go` кастомный `max()` удалён (builtin), INTEGRATION.md TN-135 помечены выполненными, `isSlackRetryableErrorOld` + `nolint:deadcode` удалены
- [x] 3. Флаки postgres_history_test: testcontainers StartupTimeout 5s → 60s
- [ ] 4. Deprecated-шимы errors-файлов НЕ удалены — активно используются; уходит в этап 4 (ERROR-REINVENTION). Deprecated `pkg/metrics`: 8 импортов под `//nolint` — миграция на v2 требует новых групп метрик, этап 4.
