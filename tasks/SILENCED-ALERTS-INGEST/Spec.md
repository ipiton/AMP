---
id: SILENCED-ALERTS-INGEST
slug: silenced-alerts-ingest
type: bug
status: draft
created_at: 2026-10-23
updated_at: 2026-10-23
based_on:
  - requirements.md
  - research.md
---

# Spec: заглушенный алерт проходит ingest

## Design Premises

| Premise | Как проверено | Класс | Если неверна |
|---|---|---|---|
| Прямая публикация не проверяет silence | чтение `processTransparent*`/`processEnriched`: `PublishToReceiver*` без silence | call-path-traced | фикс в процессоре лишний, но безвреден |
| Notify chain группы подавляет заглушенные | `manager_impl.go:1215` `filterSilenced`, checker = `r.silenceStore` | call-path-traced | в режиме группировки заглушенный алерт уведомит — ловит тест 2.x |
| `GET` показывает заглушенный алерт как `suppressed` | `alertconv.ToGettableAlert`, `silenced=true` по умолчанию; существующий тест `setupSilencedAlertRegistry` | verified | контрактный тест красный |
| Кеш inhibition пополняется до проверки silence | порядок шагов в `ProcessAlert` (Step 0.5) | call-path-traced | тест 2.x |

## Design

1. `AlertProcessorConfig.SilenceChecker grouping.GroupSilenceChecker` → поле `silenceChecker`.
2. `ProcessAlert`, после `decision := p.evaluateRoute(alert)`: если `alert.Status == core.StatusFiring` и `silenceChecker.HasActiveMatch(alert.Labels, time.Now())` — Info-лог; при `shouldGroup(decision)` — `routeAlertToGroup` (результат не ведёт в прямую публикацию); `return nil`.
3. `service_registry.go`: `SilenceChecker` = `r.silenceStore` при не-nil.
4. `handleAlertsPost`: убрать silence-фильтр, параметр `silences` больше не нужен.
5. `CHANGELOG.md` [Unreleased]: Fixed + Breaking changes / migration notes — заглушенные алерты принимаются и видны как `suppressed` (v2 и v1).

## Acceptance

- [ ] Контракт: silence → POST алерта → `GET /api/v2/alerts` возвращает его со `state=suppressed`, `silencedBy` содержит ID silence.
- [ ] Прямой режим: заглушенный firing не публикуется; незаглушенный — публикуется; resolved заглушенного — публикуется как раньше.
- [ ] Группировка: заглушенный firing уходит в `GroupManager`, публикаций и LLM-вызовов нет.
- [ ] Заглушенный firing добавлен в кеш inhibition.

## Edge Cases

1. Silence создан после ingest — как раньше: состояние считает `GET`, уведомление режет notify chain (прямой режим уже опубликовал).
2. Silence истёк — следующий resend Prometheus публикуется обычным путём; в режиме группировки алерт уже в группе.
3. `SilenceChecker == nil` (тесты, конструкторы без silence) — проверки нет.
4. Ошибка группировки для заглушенного алерта — лог внутри `routeAlertToGroup`, прямой публикации нет.

## Impact Analysis

- **Affected modules:** `application/handlers`, `core/services`, `application` (registry).
- **Breaking changes:** API — `GET /api/v2/alerts` теперь содержит заглушенные алерты (как upstream); клиенты фильтруют `silenced=false`.
- **New dependencies:** none.
- **Risks:** рост объёма хранилища на заглушенные алерты (как в upstream); заглушенные алерты теперь пишутся в историю.

## Rollout / Rollback

- **Rollout:** обычный. **Rollback:** предыдущий образ. **Feature flag:** not applicable.

## Observability

- Info-лог «Alert silenced, skipping publish» с `alert`, `fingerprint`.

## Deep Review

- **Mandatory triggers present:** `C`, 3 сигнала (`C X R`).
