# DEBT-STAGE1-SECURITY

Закрыть security-долги из плана debt-closure (этап 1). Без пушей, локальные коммиты в ветке `debt/stage1-security`.

## Scope

1. CSWSH: `go-app/cmd/server/handlers/silence_ws.go` — `CheckOrigin` пускал все origins. Ввести whitelist через конфиг `server.websocket.allowed_origins` (пусто = same-origin, `*` = все).
2. MANUAL-SQL-RISK (TECH-DEBT.md): конкатенация SQL-строк в фильтрах → параметризованные запросы.
3. OTLP TLS: `go-app/pkg/telemetry/tracer.go` — жёсткий `WithInsecure()` → TLS-опция в telemetry-конфиге.

## Out of scope

Валидаторы-заглушки, wiring-дыры, архитектурный долг (этап 4 плана).

## Definition of Done

- `go build ./... && go vet ./... && golangci-lint run` без новых issue
- unit-тесты на новое поведение зелёные
- статусы в `docs/06-planning/TECH-DEBT.md` обновлены
