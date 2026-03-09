# Очередь (Queue) и WIP

## Queue
- Пусто.

## WIP (Max 2)
- Пусто.

## Notes
- `SOLO-KANBAN-INIT` завершен и перенесен в `DONE.md`.
- `PHASE-4-PRODUCTION-PUBLISHING-PATH` завершен как production slice; открытые quality-gate blockers вынесены в `BUGS.md`.
- `ALERTMANAGER-REPLACEMENT-SCOPE` завершен как truth-alignment slice; follow-up work теперь разнесен между `RUNTIME-SURFACE-RESTORATION`, `FUTUREPARITY-HISTORICAL-RUNTIME-GAP` и residual repo-doc cleanup.
- `DOCS-HONESTY-PASS` завершен как top-level public/docs honesty slice; residual doc cleanup сужен до `REPO-DOC-LICENSE-DRIFT`.
- `PHASE-3-STORAGE-HARDENING` завершен как bootstrap/storage hardening slice; required storage теперь fail-fast, а health/readiness contract стал state-aware.
- Анализ replacement readiness и публичных claims зафиксирован в `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`; backlog items выше раскладывают его на отдельные incremental slices.
- `PHASE-0` и core API unstubbing уже закрыты по коду и тестам; `FUTUREPARITY-SUITE-DRIFT` завершен как narrow compatibility-harness slice, а residual historical/runtime mismatch остается явным bug в `BUGS.md`.
- `REPO-DOC-LICENSE-DRIFT` закрыт как narrow four-file cleanup; более широкий residual repo-doc drift остается отдельным bug `SECONDARY-REPO-DOC-HISTORICAL-DRIFT`.
- Баланс 50/50 сейчас лучше держать через чередование `PHASE-2/3` и docs/UI cleanup.
