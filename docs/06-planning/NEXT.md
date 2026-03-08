# Очередь (Queue) и WIP

## Queue
1. (TECH-DEBT / Reliability) **PHASE-3-STORAGE-HARDENING** — Закрыть migrations/storage init и health decomposition в `internal/application/service_registry.go`. ~2d
2. (UX / Ops) **UI-PLACEHOLDER-REMOVAL** — Реализовать `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing`. ~1-2d
3. (DOCS / Product) **DOCS-HONESTY-PASS** — Синхронизировать README и compatibility docs с фактическим runtime. ~0.5d

## WIP (Max 2)

## Notes
- `SOLO-KANBAN-INIT` завершен и перенесен в `DONE.md`.
- `PHASE-4-PRODUCTION-PUBLISHING-PATH` завершен как production slice; открытые quality-gate blockers вынесены в `BUGS.md`.
- `PHASE-0` и core API unstubbing уже закрыты по коду и тестам; следующий основной фокус — bootstrap, publishing и storage.
- Баланс 50/50 сейчас лучше держать через чередование `PHASE-2/3` и docs/UI cleanup.
