---
id: SILENCED-ALERTS-INGEST
slug: silenced-alerts-ingest
stream: API
type: bug
priority: high
status: active
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Requirements: заглушенный алерт проходит ingest

Запись `BUGS.md` `SILENCED-ALERTS-INGEST`, находки CODE-REVIEW-2026-10-21 H-1, D-4.

## Problem Framing

- **Symptom:** firing-алерт, совпавший с активным silence, `POST /api/v2/alerts` отбрасывает до обработки: его нет в `GET /api/v2/alerts` (karma, `amtool alert query -s` не видят `suppressed`), он не становится источником inhibition и не пишется в историю.
- **Root Cause:** `handleAlertsPost` (`internal/application/handlers/alerts.go:431-437`) фильтрует `silences.HasActiveMatch` до `ProcessAlert`/`IngestBatch`. В upstream silence — понятие только этапа уведомления.
- **Why Now:** high-позиция конвейера ревью (`REVIEW-PIPELINE-2026-10-21.md` №4).
- **How We Measure:** контрактный тест: после POST заглушенного алерта `GET` возвращает его со `status.state=suppressed`; публикаций нет.

## Risk Profile

- **Signals:** `C X R`
  - `C` — меняется ответ `GET /api/v2/alerts` (заглушенные алерты появляются) — breaking для клиентов API.
  - `X` — хендлер API, `core/services` (AlertProcessor), сборка в `service_registry`.
  - `R` — путь ingest и публикации.
- **Tier:** Full
- **Notes:** `C` + 3 сигнала ⇒ Full, deep-review обязателен.

## User Stories

1. Как дежурный, я хочу видеть заглушенный алерт в UI/`amtool` со статусом `suppressed`, а не терять его.
2. Как автор inhibit-правил, я хочу, чтобы заглушенный алерт подавлял цели, как в upstream.
3. Как оператор, я не хочу получать уведомления по заглушенным алертам ни в режиме группировки, ни в прямом.

## Success Criteria

- [ ] Firing-алерт под активным silence после `POST /api/v2/alerts` есть в `GET /api/v2/alerts` со `status.state=suppressed` и `silencedBy`.
- [ ] Такой алерт не публикуется в прямом режиме (без route-дерева / группировки) — на любом входе (`/api/v2/alerts`, webhook-эндпоинты).
- [ ] В режиме группировки алерт попадает в группу (notify chain подавляет его по silence), LLM-классификация и investigation для него не запускаются.
- [ ] Заглушенный firing-алерт попадает в кеш источников inhibition.
- [ ] Resolved-алерт обрабатывается как раньше.

## Non-Goals

- `receivers`/`inhibitedBy` в статусе (`API-ALERTS-RECEIVER-INHIBITED-STATUS`).
- Пересмотр порядка inhibition → silence на ingest.

## Constraints

- **Scope:** `internal/application/handlers/alerts.go`, `internal/core/services/alert_processor.go`, `internal/application/service_registry.go`, тесты, `CHANGELOG.md`.
- **Security:** не затрагивается.
- **Compatibility:** breaking для API-клиентов, которые полагались на отсутствие заглушенных алертов в `GET` без `silenced=false` — CHANGELOG.

## Discovery Notes

- Similar tasks: `INHIBIT-SOURCE-CACHE-EVICTION` (DONE 2026-10-22) — кеш источников inhibition.
- Relevant patterns: inhibition-проверка в `ProcessAlert` (ранний `return nil`); `grouping.GroupSilenceChecker` (`HasActiveMatch`), `memory.SilenceStore` его реализует; notify chain `filterSilenced` (`grouping/manager_impl.go:1215`).
- Open unknowns: нет.
