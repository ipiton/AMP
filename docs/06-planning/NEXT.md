# Очередь (Queue) и WIP

## Queue

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Phase 5A/5B закрыты, осталось наполнить агента реальными tools.
> Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base с auto-matching по alert labels. ~2d
- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d

### 2. Operations (из AMP-OSS)
- [ ] **RELOADABLE-COMPONENT-INTERFACES** — per-component Reloadable + wiring в ReloadCoordinator. ~2d
- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven SIGHUP. ~1d
- [ ] **HELM-PRODUCTION-VALUES** — `values-production.yaml` (PG cluster, DragonflyDB, sidecar). ~0.5d

### 3. Alertmanager Parity — Phase B (feature parity)
> Необязательно для controlled replacement, но закрывает полный feature set Alertmanager.

- [ ] **PARITY-B1-MUTE-TIME-INTERVALS** — maintenance windows (time interval parser, timezone, route wiring). ~5d
- [ ] **PARITY-B3-TELEGRAM-PUBLISHER** — популярен в СНГ. ~1-2d
- [ ] **PARITY-B6-WEB-ROUTE-PREFIX** — reverse proxy. ~0.5d

## WIP (Max 2)

_пусто — есть слот для старта следующей задачи_

- [ ] **PHASE-6A-BUILTIN-TOOLS** — Prometheus (PromQL) + Loki (LogQL) + K8s (pods/events/logs) + DB tools. ~7d (started: 2026-04-24)
## Notes
- Очередь обновлена 2026-04-23 после аудита: Parity Phase A закрыта полностью, PHASE-5A реализован на ~90% (остался config surface), PHASE-5B закрыт.
- **Приоритет 1**: PHASE-5A-TAIL — закрыть хвост и снять с WIP.
- **Приоритет 2**: PHASE-6A built-in tools — главный блокер полезности LLM-агента (без tools 5B-агент работает только с alert labels).
- **Приоритет 3**: Operations (reloadable + sidecar) — закрывает hot reload story.
- **Приоритет 4**: Parity Phase B — по запросу, не критично.
- Parity Phase C (clustering, remaining receivers) и Intelligence Phase 6C/6D/7 остаются в BACKLOG.
- Завершённые задачи: см. `DONE.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
