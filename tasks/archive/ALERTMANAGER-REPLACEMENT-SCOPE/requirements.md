# Requirements: ALERTMANAGER-REPLACEMENT-SCOPE

## Context
В репозитории зафиксирован drift между тем, что AMP реально поддерживает в active runtime, и тем, что заявлено в README, migration docs, compatibility matrix, ADR и parity tests. После `PHASE-4-PRODUCTION-PUBLISHING-PATH` active ingest/publishing path стал существенно честнее, но тезис "AMP может заменить Alertmanager" все еще нельзя считать надежно подтвержденным. Нужно выбрать и закрепить реальный replacement scope: либо дотянуть runtime до заявленного API surface, либо сузить публичные claims и тестовые ожидания до фактического verified поведения.

## Goals
- [x] Определить честный scope replacement story для AMP относительно Alertmanager.
- [x] Синхронизировать source of truth между active runtime, ADR, parity tests и compatibility docs.
- [x] Разложить найденные расхождения на минимальные incremental slices: runtime scope, verification, docs honesty.
- [x] Зафиксировать acceptance criteria, после которых тезис "AMP может заменить Alertmanager" можно будет использовать без оговорок или с явно ограниченными оговорками.

## Constraints
- Перед реализацией нужен отдельный `/spec`: задача затрагивает API contract, replacement positioning и compatibility verification model.
- Нельзя расширять scope бессистемно: либо восстанавливаем конкретные endpoints/behaviors, либо сужаем claims и тестовые ожидания.
- Source of truth должен опираться на active runtime, а не на исторические docs или stale tests.
- Нельзя терять уже собранный анализ; базовый документ — `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.

## Selected Direction
- Для текущего slice выбран путь `narrow public claims`, а не `restore runtime surface`.
- Canonical source of truth: active runtime (`go-app/cmd/server/main.go` + `go-app/internal/application/router.go`).
- Current claim: только `controlled replacement`.
- Потенциальное восстановление широкого runtime/API surface вынесено в backlog как `RUNTIME-SURFACE-RESTORATION`.

## Success Criteria (Definition of Done)
- [x] Принято и зафиксировано решение по replacement scope: `restore runtime surface` или `narrow public claims`.
- [x] Для выбранного scope синхронизированы runtime/docs/tests/ADR без явных противоречий в active-runtime-first narrative.
- [x] Planning содержит отдельные follow-up slices для runtime gaps, docs pass и verification hardening.
- [ ] Есть короткий reproducible verification path для future replacement claim.

## Final Status

Задача закрыта как truth-alignment slice с явной фиксацией оставшихся follow-ups.

Реально доставлено:
- active runtime закреплен как canonical source of truth для replacement story;
- current claim сужен до `controlled replacement`;
- historical wide-surface parity вынесен из default verification path в `futureparity`;
- planning/public docs синхронизированы под active-runtime-first narrative;
- отдельные follow-up items заведены для docs honesty, runtime restoration и future parity refresh.

Незакрытый остаток:
- отдельный reproducible smoke/acceptance path для будущего сильного replacement claim еще не оформлен как самостоятельный проверочный артефакт;
- residual docs honesty cleanup и `futureparity` suite repair остаются за пределами этого slice.
