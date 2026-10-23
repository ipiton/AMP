---
id: SILENCED-ALERTS-INGEST
slug: silenced-alerts-ingest
stream: API
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Research Pack - заглушенный алерт проходит ingest

## Goal

Убрать silence-фильтр из ingest, не начав публиковать заглушенные алерты.

## Sources

- `go-app/internal/application/handlers/alerts.go:404-480` (`handleAlertsPost`)
- `go-app/internal/core/services/alert_processor.go` (`ProcessAlert`, `processTransparentWithRecommendations`/`processTransparent`/`processEnriched`, `shouldGroup`, `routeAlertToGroup`)
- `go-app/internal/application/service_registry.go:1798-1820` (silenceChecker для group manager), `:2385` (`AlertProcessorConfig`)
- `go-app/internal/infrastructure/grouping/manager.go:842` (`GroupSilenceChecker`), `manager_impl.go:1215` (`filterSilenced`)
- `go-app/cmd/server/handlers/webhook.go:157`, `prometheus_alerts.go:555`, `internal/infrastructure/webhook/handler.go:85,222` — другие входы в `ProcessAlert`
- `go-app/cmd/server/main_phase0_contract_test.go:1416`, `internal/application/handlers/alerts_test.go:311`

## Findings

- Запись BUGS говорит «подавление уже делает notify chain» — верно только при группировке (`GroupingEnabled = grouping.enabled && route-дерево`). В прямом режиме (нет `route:`, fail-open группировки, `shouldGroup=false`) `AlertProcessor` публикует через `PublishToReceiver*` без проверки silence. Просто убрать фильтр ⇒ заглушенные алерты начнут уходить в прямом режиме.
- Другие вызовы `ProcessAlert` (`cmd/server/handlers/webhook.go`, `prometheus_alerts.go`, `infrastructure/webhook/handler.go`) в проде не подключены (`router.go`: единственный вход — `AlertsHandler`, `POST /api/v1/alerts` проксирует в него). Проверка в процессоре закрывает и их, если их подключат.
- В enriched-режиме `processEnriched` до группировки вызывает LLM и `investigationQueue.Submit`. Если пустить заглушенный алерт по обычному пути, LLM будет вызываться для каждого его resend — раньше этого не было (хендлер отбрасывал).
- `ProcessAlert` до выбора режима: dedup (запись в БД/историю) → кеш inhibition (`AddFiringAlert`) → inhibition-проверка. Проверка silence после них сохраняет и историю, и роль источника inhibition.
- `GET` считает состояние в `alertconv.ToGettableAlert` по текущим silences: `suppressed` + `silencedBy`; фильтр `silenced` по умолчанию `true` — алерт виден.
- `services` уже импортирует `infrastructure/grouping`; `memory.SilenceStore` реализует `GroupSilenceChecker`.

## Decision

- `AlertProcessorConfig.SilenceChecker grouping.GroupSilenceChecker` (optional). В `ProcessAlert` после inhibition и `evaluateRoute`: firing + `HasActiveMatch` ⇒ при `shouldGroup` — `routeAlertToGroup` (notify chain подавит; после снятия silence группа уведомит по своему таймеру, как в upstream), без fail-open в прямую публикацию; иначе — ничего не публиковать. `return nil` до выбора режима — без LLM/investigation.
- `service_registry`: передать `r.silenceStore` (с nil-проверкой, как для group manager).
- Хендлер: убрать фильтр.

## Follow-ups

- [ ] нет
