# Очередь (Queue) и WIP

## Queue
1. (TECH-DEBT / Reliability) **PHASE-3-STORAGE-HARDENING** — Закрыть migrations/storage init и health decomposition в `internal/application/service_registry.go`. ~2d
2. (UX / Ops) **UI-PLACEHOLDER-REMOVAL** — Реализовать `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`. ~1-2d

## WIP (Max 2)
- Нет активных задач.

## Notes
- `SOLO-KANBAN-INIT` завершен и перенесен в `DONE.md`.
- `PHASE-4-PRODUCTION-PUBLISHING-PATH` завершен как production slice; открытые quality-gate blockers вынесены в `BUGS.md`.
- `ALERTMANAGER-REPLACEMENT-SCOPE` завершен как truth-alignment slice; follow-up work теперь разнесен между `RUNTIME-SURFACE-RESTORATION`, `FUTUREPARITY-SUITE-DRIFT` и residual repo-doc cleanup.
- `DOCS-HONESTY-PASS` завершен как top-level public/docs honesty slice; residual doc cleanup сужен до `REPO-DOC-LICENSE-DRIFT`.
- Анализ replacement readiness и публичных claims зафиксирован в `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`; backlog items выше раскладывают его на отдельные incremental slices.
- `PHASE-0` и core API unstubbing уже закрыты по коду и тестам; следующий основной фокус — bootstrap, publishing и storage.
- Баланс 50/50 сейчас лучше держать через чередование `PHASE-2/3` и docs/UI cleanup.
